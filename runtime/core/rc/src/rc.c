/* Slot table for rc and weak handles.
 *
 * A handle packs a 32-bit slot index in the high half and a 32-bit generation
 * in the low half. One 64-bit atomic word per slot holds the generation and
 * the strong count together, so a generation check and a count change are a
 * single compare-and-swap. A released slot becomes a tombstone: the generation
 * advances, the object storage is returned, and the slot joins the free list.
 * Old handles fail the generation check and are safe to pass to deref, retain,
 * release, and upgrade. When the generation would wrap to zero the slot is
 * retired for good, because a second life at generation zero could make an
 * ancient handle name a new object.
 *
 * The table is one flat array and doubles under a spin lock. Retain, release,
 * deref, and upgrade never take that lock; they change the packed slot word
 * with atomics. Growth copies the slots only while no writer is between its
 * load and its compare-and-swap, which the epoch gate below enforces. The old
 * table stays mapped because a reader may still hold its pointer; the retained
 * pages stay below the size of the final table.
 *
 * Object storage comes from willrt_alloc and willrt_free. Those calls are
 * single-threaded, so creation and the final release both run under the table
 * lock; the ordinary retain and release paths never reach the allocator.
 *
 * There is no weak count in this table. The interface has no weak release, so
 * a weak handle is a generation snapshot: it does not pin the slot, and a
 * reused slot makes the weak handle fail the generation check instead of
 * resurrecting a different object.
 */

#include "rc.h"

#include <stdatomic.h>
#include <string.h>

enum {
    WILLRT_RC_INITIAL_SLOTS = 256
};

/* Marks the end of the free list. Slot indices use 32 bits, so this value is
   outside every valid index. */
#define WILLRT_RC_NO_SLOT UINT32_MAX

struct willrt_rc_slot {
    /* High 32 bits: generation. Low 32 bits: strong count. */
    _Atomic(uint64_t) state;
    _Atomic(void*) value;
    /* drop runs on the final release, before the object storage is freed. */
    void (*drop)(void*);
    /* Only the table lock and its owner touch this field. */
    uint32_t next_free;
};

struct willrt_rc_table {
    size_t count;
    struct willrt_rc_slot slots[];
};

/* The current table is replaced only by growth. A release store publishes a
   fully built table, and a load that sees the new pointer sees the copied
   slots. */
static _Atomic(struct willrt_rc_table*) willrt_rc_table;

/* The lock guards table creation, slot allocation, slot retirement, and the
   free list. Its sections are short and never wait on a user thread, so a
   plain spin is the whole implementation. */
static atomic_flag willrt_rc_table_lock = ATOMIC_FLAG_INIT;

/* The free list and its head are read and written only under the table lock. */
static uint32_t willrt_rc_free_head = WILLRT_RC_NO_SLOT;

/* Growth flips the epoch to odd, waits for the writer count to reach zero,
   copies the slots, and flips the epoch back to even. A writer increments the
   count, then re-reads the epoch; either its increment is visible to the
   growth wait or the writer sees the odd epoch and backs out. */
static _Atomic(uint64_t) willrt_rc_epoch;
static _Atomic(uint64_t) willrt_rc_active_writers;

/* willrt_rc_state_generation returns the generation held in state. */
static uint32_t willrt_rc_state_generation(uint64_t state) {
    return (uint32_t)(state >> 32);
}

/* willrt_rc_state_count returns the strong count held in state. */
static uint32_t willrt_rc_state_count(uint64_t state) {
    return (uint32_t)(state & UINT64_C(0xFFFFFFFF));
}

/* willrt_rc_state_make packs generation and count into one word. */
static uint64_t willrt_rc_state_make(uint32_t generation, uint32_t count) {
    return ((uint64_t)generation << 32) | (uint64_t)count;
}

/* willrt_rc_table_current returns the published table, or NULL before the
   first object exists. The load is sequentially consistent so a deref that
   runs after a creation or a growth observes at least that table. */
static struct willrt_rc_table* willrt_rc_table_current(void) {
    return atomic_load_explicit(&willrt_rc_table, memory_order_seq_cst);
}

/* willrt_rc_table_lock_acquire waits until this thread owns the table lock. */
static void willrt_rc_table_lock_acquire(void) {
    while (atomic_flag_test_and_set_explicit(&willrt_rc_table_lock,
                                             memory_order_acquire)) {
        /* The owning section is bounded by a copy of the slot array or one
           object allocation, so spinning loses nothing else. */
    }
}

/* willrt_rc_table_lock_release hands the table lock to another thread. */
static void willrt_rc_table_lock_release(void) {
    atomic_flag_clear_explicit(&willrt_rc_table_lock, memory_order_release);
}

/* willrt_rc_writer_begin joins the set of writers whose compare-and-swap must
   finish before growth copies the slots. */
static void willrt_rc_writer_begin(void) {
    for (;;) {
        uint64_t epoch = atomic_load_explicit(&willrt_rc_epoch,
                                              memory_order_seq_cst);

        if ((epoch & UINT64_C(1)) != 0) {
            continue;
        }
        atomic_fetch_add_explicit(&willrt_rc_active_writers, 1,
                                  memory_order_seq_cst);
        if (atomic_load_explicit(&willrt_rc_epoch, memory_order_seq_cst)
            == epoch) {
            return;
        }
        atomic_fetch_sub_explicit(&willrt_rc_active_writers, 1,
                                  memory_order_seq_cst);
    }
}

/* willrt_rc_writer_end leaves the writer set. */
static void willrt_rc_writer_end(void) {
    atomic_fetch_sub_explicit(&willrt_rc_active_writers, 1,
                              memory_order_seq_cst);
}

/* willrt_rc_grow_begin closes the writer set and waits for every writer that
   already joined. The caller holds the table lock. */
static void willrt_rc_grow_begin(void) {
    for (;;) {
        uint64_t epoch = atomic_load_explicit(&willrt_rc_epoch,
                                              memory_order_seq_cst);

        if ((epoch & UINT64_C(1)) == 0
            && atomic_compare_exchange_strong_explicit(
                   &willrt_rc_epoch, &epoch, epoch + 1,
                   memory_order_seq_cst, memory_order_seq_cst)) {
            break;
        }
    }
    while (atomic_load_explicit(&willrt_rc_active_writers,
                                memory_order_seq_cst) != 0) {
        /* A writer that already joined is inside one compare-and-swap. */
    }
}

/* willrt_rc_grow_end reopens the writer set. */
static void willrt_rc_grow_end(void) {
    atomic_fetch_add_explicit(&willrt_rc_epoch, 1, memory_order_seq_cst);
}

/* willrt_rc_table_allocate returns page-backed storage for count slots, or
   NULL when the size overflows or the system refuses the pages. The pages are
   zero-filled. */
static struct willrt_rc_table* willrt_rc_table_allocate(size_t count) {
    struct willrt_rc_table* table;
    size_t bytes;

    if (count == 0 || count > (size_t)UINT32_MAX) {
        return NULL;
    }
    if (count > (SIZE_MAX - sizeof(struct willrt_rc_table))
                    / sizeof(struct willrt_rc_slot)) {
        return NULL;
    }
    bytes = sizeof(struct willrt_rc_table)
            + count * sizeof(struct willrt_rc_slot);
    table = (struct willrt_rc_table*)willrt_os_alloc_pages(bytes);
    if (table == NULL) {
        return NULL;
    }
    table->count = count;
    return table;
}

/* willrt_rc_table_grow_locked builds a table twice as large as the current
   one, copies every slot with its generation, and links the added slots into
   the free list. Growth from no table builds the initial table. The caller
   holds the table lock. Returns 1 on success and 0 when the new table cannot
   be allocated. */
static int willrt_rc_table_grow_locked(void) {
    struct willrt_rc_table* old = willrt_rc_table_current();
    struct willrt_rc_table* table;
    size_t old_count = old != NULL ? old->count : 0;
    size_t new_count;
    size_t index;

    if (old_count == 0) {
        new_count = WILLRT_RC_INITIAL_SLOTS;
    } else {
        if (old_count > (size_t)UINT32_MAX / 2) {
            return 0;
        }
        new_count = old_count * 2;
    }

    willrt_rc_grow_begin();
    table = willrt_rc_table_allocate(new_count);
    if (table == NULL) {
        willrt_rc_grow_end();
        return 0;
    }

    for (index = 0; index < old_count; index++) {
        uint64_t state = atomic_load_explicit(&old->slots[index].state,
                                              memory_order_acquire);
        void* value = atomic_load_explicit(&old->slots[index].value,
                                           memory_order_acquire);

        atomic_store_explicit(&table->slots[index].state, state,
                              memory_order_relaxed);
        atomic_store_explicit(&table->slots[index].value, value,
                              memory_order_relaxed);
        table->slots[index].drop = old->slots[index].drop;
        table->slots[index].next_free = old->slots[index].next_free;
    }
    for (index = old_count; index < new_count; index++) {
        atomic_store_explicit(&table->slots[index].state,
                              willrt_rc_state_make(1, 0),
                              memory_order_relaxed);
        atomic_store_explicit(&table->slots[index].value, NULL,
                              memory_order_relaxed);
        table->slots[index].drop = NULL;
        table->slots[index].next_free =
            index + 1 < new_count ? (uint32_t)(index + 1) : WILLRT_RC_NO_SLOT;
    }

    if (old_count == 0) {
        willrt_rc_free_head = 0;
    } else {
        table->slots[new_count - 1].next_free = willrt_rc_free_head;
        willrt_rc_free_head = (uint32_t)old_count;
    }

    atomic_store_explicit(&willrt_rc_table, table, memory_order_seq_cst);
    willrt_rc_grow_end();
    return 1;
}

/* willrt_rc_slot_retain adds one reference when the slot state still carries
   generation and a nonzero count. Returns 1 on success. The count saturates
   at UINT32_MAX so a wrap cannot free an object that is still referenced. */
static int willrt_rc_slot_retain(struct willrt_rc_slot* slot,
                                 uint32_t generation) {
    uint64_t state = atomic_load_explicit(&slot->state, memory_order_acquire);

    for (;;) {
        uint32_t count;

        if (willrt_rc_state_generation(state) != generation) {
            return 0;
        }
        count = willrt_rc_state_count(state);
        if (count == 0 || count == UINT32_MAX) {
            return 0;
        }
        if (atomic_compare_exchange_weak_explicit(
                &slot->state, &state,
                willrt_rc_state_make(generation, count + 1),
                memory_order_acq_rel, memory_order_acquire)) {
            return 1;
        }
    }
}

/* willrt_rc_slot_retire turns a slot whose count reached zero into a
   tombstone. The object storage is released under the table lock because the
   allocator is single-threaded and creation also holds this lock. */
static void willrt_rc_slot_retire(uint32_t index, uint32_t generation) {
    struct willrt_rc_table* table;
    struct willrt_rc_slot* slot;
    uint64_t state;
    uint32_t next_generation;
    void* object;
    void (*drop)(void*);

    willrt_rc_table_lock_acquire();
    table = willrt_rc_table_current();
    if (table == NULL || (size_t)index >= table->count) {
        willrt_rc_table_lock_release();
        return;
    }
    slot = &table->slots[index];
    state = atomic_load_explicit(&slot->state, memory_order_acquire);
    if (willrt_rc_state_generation(state) != generation
        || willrt_rc_state_count(state) != 0) {
        willrt_rc_table_lock_release();
        return;
    }

    object = atomic_exchange_explicit(&slot->value, NULL, memory_order_acq_rel);
    drop = slot->drop;
    slot->drop = NULL;
    next_generation = generation + 1;
    atomic_store_explicit(&slot->state,
                          willrt_rc_state_make(next_generation, 0),
                          memory_order_release);
    /* A wrapped generation leaves the slot out of the free list for the rest
       of the process, so no later object can carry generation zero. */
    if (next_generation != 0) {
        slot->next_free = willrt_rc_free_head;
        willrt_rc_free_head = index;
    }
    willrt_rc_table_lock_release();

    /* Drop glue may release nested handles, and a nested final release also
       needs the table lock. Run it with the lock released, after the slot is
       already a tombstone so a second release cannot retire it again. */
    if (drop != NULL && object != NULL) {
        drop(object);
    }
    willrt_rc_table_lock_acquire();
    willrt_free(object);
    willrt_rc_table_lock_release();
}

uint64_t willrt_rc_new_drop(const void* value, size_t size,
                            void (*drop)(void*)) {
    struct willrt_rc_table* table;
    struct willrt_rc_slot* slot;
    void* object;
    uint32_t index;
    uint32_t generation;

    if (value == NULL && size != 0) {
        return 0;
    }

    willrt_rc_table_lock_acquire();
    table = willrt_rc_table_current();
    if (table == NULL || willrt_rc_free_head == WILLRT_RC_NO_SLOT) {
        if (!willrt_rc_table_grow_locked()) {
            willrt_rc_table_lock_release();
            return 0;
        }
        table = willrt_rc_table_current();
    }

    index = willrt_rc_free_head;
    slot = &table->slots[index];
    willrt_rc_free_head = slot->next_free;

    object = willrt_alloc(size);
    if (object == NULL) {
        slot->next_free = willrt_rc_free_head;
        willrt_rc_free_head = index;
        willrt_rc_table_lock_release();
        return 0;
    }
    if (size != 0) {
        memcpy(object, value, size);
    }

    generation = willrt_rc_state_generation(
        atomic_load_explicit(&slot->state, memory_order_relaxed));
    atomic_store_explicit(&slot->value, object, memory_order_relaxed);
    slot->drop = drop;
    atomic_store_explicit(&slot->state, willrt_rc_state_make(generation, 1),
                          memory_order_release);
    willrt_rc_table_lock_release();

    return willrt_rc_handle_make(index, generation);
}

uint64_t willrt_rc_new(const void* value, size_t size) {
    return willrt_rc_new_drop(value, size, NULL);
}

void willrt_rc_retain(uint64_t handle) {
    struct willrt_rc_table* table;
    uint32_t index;
    uint32_t generation;

    if (handle == 0) {
        return;
    }
    index = willrt_rc_handle_slot(handle);
    generation = willrt_rc_handle_generation(handle);

    willrt_rc_writer_begin();
    table = willrt_rc_table_current();
    if (table == NULL || (size_t)index >= table->count) {
        willrt_rc_writer_end();
        return;
    }
    (void)willrt_rc_slot_retain(&table->slots[index], generation);
    willrt_rc_writer_end();
}

void willrt_rc_release(uint64_t handle) {
    struct willrt_rc_table* table;
    struct willrt_rc_slot* slot;
    uint64_t state;
    uint32_t index;
    uint32_t generation;
    int final = 0;

    if (handle == 0) {
        return;
    }
    index = willrt_rc_handle_slot(handle);
    generation = willrt_rc_handle_generation(handle);

    willrt_rc_writer_begin();
    table = willrt_rc_table_current();
    if (table == NULL || (size_t)index >= table->count) {
        willrt_rc_writer_end();
        return;
    }
    slot = &table->slots[index];
    state = atomic_load_explicit(&slot->state, memory_order_acquire);
    for (;;) {
        uint32_t count;

        if (willrt_rc_state_generation(state) != generation) {
            break;
        }
        count = willrt_rc_state_count(state);
        if (count == 0) {
            break;
        }
        if (atomic_compare_exchange_weak_explicit(
                &slot->state, &state,
                willrt_rc_state_make(generation, count - 1),
                memory_order_acq_rel, memory_order_acquire)) {
            final = count == 1;
            break;
        }
    }
    willrt_rc_writer_end();
    if (final) {
        willrt_rc_slot_retire(index, generation);
    }
}

void* willrt_rc_deref(uint64_t handle) {
    uint32_t index;
    uint32_t generation;

    if (handle == 0) {
        return NULL;
    }
    index = willrt_rc_handle_slot(handle);
    generation = willrt_rc_handle_generation(handle);

    for (;;) {
        struct willrt_rc_table* table = willrt_rc_table_current();
        struct willrt_rc_slot* slot;
        uint64_t state;
        void* object;

        if (table == NULL || (size_t)index >= table->count) {
            return NULL;
        }
        slot = &table->slots[index];
        state = atomic_load_explicit(&slot->state, memory_order_acquire);
        if (willrt_rc_state_generation(state) != generation
            || willrt_rc_state_count(state) == 0) {
            if (willrt_rc_table_current() != table) {
                continue;
            }
            return NULL;
        }

        object = atomic_load_explicit(&slot->value, memory_order_acquire);
        if (willrt_rc_table_current() != table) {
            continue;
        }
        state = atomic_load_explicit(&slot->state, memory_order_acquire);
        if (willrt_rc_state_generation(state) != generation
            || willrt_rc_state_count(state) == 0) {
            return NULL;
        }
        return object;
    }
}

uint64_t willrt_weak_new(uint64_t handle) {
    uint32_t index;
    uint32_t generation;

    if (handle == 0) {
        return 0;
    }
    index = willrt_rc_handle_slot(handle);
    generation = willrt_rc_handle_generation(handle);

    for (;;) {
        struct willrt_rc_table* table = willrt_rc_table_current();
        struct willrt_rc_slot* slot;
        uint64_t state;

        if (table == NULL || (size_t)index >= table->count) {
            return 0;
        }
        slot = &table->slots[index];
        state = atomic_load_explicit(&slot->state, memory_order_acquire);
        if (willrt_rc_table_current() != table) {
            continue;
        }
        if (willrt_rc_state_generation(state) != generation
            || willrt_rc_state_count(state) == 0) {
            return 0;
        }
        return handle;
    }
}

int32_t willrt_weak_upgrade(uint64_t weak, void* out) {
    struct willrt_rc_table* table;
    uint32_t index;
    uint32_t generation;
    uint64_t* destination = (uint64_t*)out;

    if (weak == 0 || destination == NULL) {
        return WILLRT_EMPTY;
    }
    index = willrt_rc_handle_slot(weak);
    generation = willrt_rc_handle_generation(weak);

    willrt_rc_writer_begin();
    table = willrt_rc_table_current();
    if (table == NULL || (size_t)index >= table->count) {
        willrt_rc_writer_end();
        return WILLRT_EMPTY;
    }
    if (!willrt_rc_slot_retain(&table->slots[index], generation)) {
        willrt_rc_writer_end();
        return WILLRT_EMPTY;
    }
    *destination = weak;
    willrt_rc_writer_end();
    return WILLRT_OK;
}

size_t willrt_rc_slot_capacity(void) {
    struct willrt_rc_table* table = willrt_rc_table_current();

    return table != NULL ? table->count : 0;
}

#ifdef WILLRT_RC_TEST
void willrt_rc_test_set_generation(uint32_t slot, uint32_t generation) {
    struct willrt_rc_table* table;
    struct willrt_rc_slot* entry;
    uint64_t state;

    willrt_rc_table_lock_acquire();
    table = willrt_rc_table_current();
    if (table == NULL || (size_t)slot >= table->count) {
        willrt_rc_table_lock_release();
        return;
    }
    entry = &table->slots[slot];
    state = atomic_load_explicit(&entry->state, memory_order_acquire);
    atomic_store_explicit(
        &entry->state,
        willrt_rc_state_make(generation, willrt_rc_state_count(state)),
        memory_order_release);
    willrt_rc_table_lock_release();
}

uint32_t willrt_rc_test_slot_of(uint64_t handle) {
    return willrt_rc_handle_slot(handle);
}
#endif
