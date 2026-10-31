/* Colored load/store barriers plus SATB and card work queues. The load path
   checks forwarding; the store path publishes a good-colored value and records
   old values and card dirt without taking a global lock. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

_Atomic uint32_t gc_good_color;
_Atomic uint32_t gc_mark_color;
_Atomic int gc_marking;
_Atomic int gc_evacuating;

static _Atomic(gc_satb_chunk*) gc_satb_active;
static _Thread_local gc_satb_chunk* gc_satb_local;
static _Atomic uint32_t gc_satb_epoch = 1;

static void gc_satb_push_chunk(gc_satb_chunk* chunk) {
    gc_satb_chunk* head;

    if (chunk == NULL || chunk->count == 0) {
        return;
    }
    head = atomic_load_explicit(&gc_satb_active, memory_order_relaxed);
    do {
        atomic_store_explicit(&chunk->next, head, memory_order_relaxed);
    } while (!atomic_compare_exchange_weak_explicit(
        &gc_satb_active, &head, chunk, memory_order_release,
        memory_order_relaxed));
}

void gc_satb_flush_local(void) {
    if (gc_satb_local != NULL && gc_satb_local->count != 0) {
        gc_satb_chunk* chunk = gc_satb_local;

        gc_satb_local = NULL;
        gc_satb_push_chunk(chunk);
    }
}

static int gc_satb_record(void* object) {
    gc_satb_chunk* chunk;
    uint32_t epoch;

    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    epoch = atomic_load_explicit(&gc_satb_epoch, memory_order_acquire);
    chunk = gc_satb_local;
    if (chunk == NULL || chunk->count == WILLRT_GC_WORK_CHUNK ||
        chunk->epoch != epoch) {
        if (chunk != NULL) {
            gc_satb_push_chunk(chunk);
        }
        chunk = gc_chunk_acquire();
        chunk->epoch = epoch;
        gc_satb_local = chunk;
    }
    chunk->objects[chunk->count++] = object;
    return 1;
}

void gc_satb_cycle_begin(void) {
    uint32_t epoch = atomic_load_explicit(&gc_satb_epoch,
                                          memory_order_acquire) + 1u;

    if (epoch == 0u) {
        epoch = 1u;
    }
    atomic_store_explicit(&gc_satb_epoch, epoch, memory_order_release);
}

void* gc_read_barrier(const void* ref) {
    void* object;
    willrt_gc_header* header;
    uint64_t state;
    uint32_t color;
    gc_region* region;

    if (ref == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(ref);
    region = gc_region_by_address(object);
    if (region == NULL) {
        /* The address may be a static or foreign pointer whose high address
           bits happen to overlap the color mask. It is not a GC reference, so
           return the original pointer unchanged. */
        return (void*)ref;
    }
    color = WILLRT_GC_COLOR_OF(ref);
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) == 0 &&
        color == atomic_load_explicit(&gc_good_color, memory_order_acquire)) {
        return object;
    }
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        return WILLRT_GC_STATE_FORWARD_ADDRESS(state);
    }
    if (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0) {
        gc_mark_object(object);
    }
    if (atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0) {
        region = gc_region_by_address(object);
        if (region != NULL && region->kind == GC_REGION_YOUNG &&
            (region->flags & GC_REGION_EVACUATING) != 0) {
            void* forwarded = gc_object_forward(object);

            if (forwarded != NULL) {
                return forwarded;
            }
        }
    }
    return object;
}

void* gc_read_barrier_slot(void** slot) {
    void* ref;
    void* object;
    void* current;
    uint32_t good;

    if (slot == NULL) {
        return NULL;
    }
    ref = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    object = gc_read_barrier(ref);
    if (object == NULL) {
        return NULL;
    }
    if (gc_region_by_address(WILLRT_GC_UNTAG(object)) == NULL) {
        /* Not a GC reference. Keep the exact bits the mutator stored. */
        return object;
    }
    good = atomic_load_explicit(&gc_good_color, memory_order_acquire);
    current = WILLRT_GC_TAG(object, good);
    atomic_compare_exchange_strong_explicit((_Atomic(void*)*)slot, &ref,
                                            current, memory_order_acq_rel,
                                            memory_order_acquire);
    /* Self-healing a slot in an old object can introduce an old-to-young
       reference without running the write barrier. Record the card here so a
       later young collection still scans this slot. */
    {
        gc_region* owner = gc_region_by_address(slot);
        gc_region* target = gc_region_by_address(object);
        if (owner != NULL && target != NULL && target->kind == GC_REGION_YOUNG &&
            (owner->kind != GC_REGION_YOUNG ||
             atomic_load_explicit(&owner->pin_count, memory_order_acquire) != 0)) {
            gc_region_note_card(
                owner, (size_t)((uint8_t*)slot - owner->base));
        }
    }
    return object;
}

static void gc_write_barrier_common(void** slot, void* value, int tag_value) {
    void* old;
    void* stored;
    gc_region* region;
    size_t offset;
    uint32_t good;

    if (slot == NULL) {
        return;
    }
    if (value != NULL && gc_region_by_address(WILLRT_GC_UNTAG(value)) == NULL) {
        /* A foreign pointer may carry bits that look like a GC color. It is
           not an object, so preserve the original address exactly. */
        atomic_store_explicit((_Atomic(void*)*)slot, value,
                              memory_order_release);
        return;
    }
    old = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    if (old != NULL && old != value &&
        (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0 ||
         atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0)) {
        (void)gc_satb_record(old);
    }
    if (value != NULL &&
        atomic_load_explicit(&gc_marking, memory_order_acquire) != 0) {
        /* A root slot can be filled after its snapshot was taken. Marking the
           inserted value is the second half of the barrier; the SATB record
           above keeps the overwritten value alive. */
        gc_mark_object(value);
    }
    if (atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0) {
        gc_region* target;

        if (value != NULL) {
            target = gc_region_by_address(WILLRT_GC_UNTAG(value));
            if (target != NULL && target->kind == GC_REGION_YOUNG &&
                (target->flags & GC_REGION_EVACUATING) != 0) {
                void* forwarded = gc_object_forward(value);

                if (forwarded != NULL) {
                    value = tag_value
                                ? WILLRT_GC_TAG(forwarded,
                                                atomic_load_explicit(
                                                    &gc_good_color,
                                                    memory_order_acquire))
                                : forwarded;
                }
            }
        }
    }
    region = gc_region_by_address(slot);
    if (region != NULL &&
        (region->kind != GC_REGION_YOUNG ||
         atomic_load_explicit(&region->pin_count, memory_order_acquire) != 0)) {
        offset = (size_t)((uint8_t*)slot - region->base);
        gc_region_note_card(region, offset);
    }

    good = atomic_load_explicit(&gc_good_color, memory_order_acquire);
    if (value == NULL) {
        stored = NULL;
    } else if (tag_value) {
        stored = WILLRT_GC_TAG(WILLRT_GC_UNTAG(value), good);
    } else {
        stored = WILLRT_GC_UNTAG(value);
    }
    atomic_store_explicit((_Atomic(void*)*)slot, stored, memory_order_release);
}

void gc_write_barrier(void** slot, void* value) {
    gc_write_barrier_common(slot, value, 1);
}

void gc_write_barrier_untagged(void** slot, void* value) {
    gc_write_barrier_common(slot, value, 0);
}

void* willrt_gc_read_barrier(const void* ref) {
    return gc_read_barrier(ref);
}

void* willrt_gc_read_barrier_slot(void** slot) {
    return gc_read_barrier_slot(slot);
}

void willrt_gc_write_barrier(void** slot, void* value) {
    gc_write_barrier(slot, value);
}

void willrt_gc_write_barrier_untagged(void** slot, void* value) {
    gc_write_barrier_untagged(slot, value);
}

void gc_satb_scan(willrt_gc_root_visit visit, void* context) {
    gc_satb_chunk* chunk;
    uint32_t epoch = atomic_load_explicit(&gc_satb_epoch,
                                          memory_order_acquire);

    gc_satb_flush_local();
    chunk = atomic_exchange_explicit(&gc_satb_active, NULL,
                                     memory_order_acquire);
    while (chunk != NULL) {
        gc_satb_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);
        size_t i;

        if (chunk->epoch == epoch) {
            for (i = 0; i < chunk->count; i++) {
                void* object = chunk->objects[i];

                visit(context, &object);
            }
        }
        gc_chunk_release(chunk);
        chunk = next;
    }
}

typedef struct gc_card_young_context {
    int found;
} gc_card_young_context;

static void gc_card_young_visit(void* context, void** slot) {
    gc_card_young_context* young = (gc_card_young_context*)context;
    gc_region* target;

    if (young->found || slot == NULL || *slot == NULL) {
        return;
    }
    target = gc_region_by_address(WILLRT_GC_UNTAG(*slot));
    if (target != NULL && target->kind == GC_REGION_YOUNG) {
        young->found = 1;
    }
}

static int gc_card_object_has_young_ref(void* object) {
    willrt_gc_header* header;
    uint64_t state;
    const willrt_gc_type* type;
    uint32_t i;

    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
    }
    type = gc_type_get(WILLRT_GC_STATE_TYPE(state));
    if (type == NULL) {
        return 0;
    }
    if ((type->flags & (WILLRT_GC_TYPE_NESTED | WILLRT_GC_TYPE_ENUM)) != 0) {
        gc_card_young_context young = {0};

        willrt_gc_scan_value(object, type, gc_card_young_visit, &young);
        return young.found;
    }
    if ((type->flags & WILLRT_GC_TYPE_SEQUENCE) != 0) {
        size_t payload = gc_object_payload_size(object);
        size_t count = type->element_size == 0
                           ? 0
                           : payload / type->element_size;
        size_t i;
        const willrt_gc_type* element_type = type->element_type;

        if (element_type == NULL) {
            return 0;
        }
        {
            gc_card_young_context young = {0};

            for (i = 0; i < count; i++) {
                uint8_t* element =
                    (uint8_t*)object + i * type->element_size;

                willrt_gc_scan_value(element, element_type,
                                     gc_card_young_visit, &young);
                if (young.found) {
                    return 1;
                }
            }
        }
        return 0;
    }
    for (i = 0; i < type->reference_count; i++) {
        void** slot = (void**)((uint8_t*)object +
                               type->reference_offsets[i]);
        gc_region* target;

        if (*slot == NULL) {
            continue;
        }
        target = gc_region_by_address(WILLRT_GC_UNTAG(*slot));
        if (target != NULL && target->kind == GC_REGION_YOUNG) {
            return 1;
        }
    }
    return 0;
}

typedef struct gc_card_scan_context {
    gc_region* region;
    willrt_gc_root_visit visit;
    void* context;
    int only_live;
} gc_card_scan_context;

static void gc_card_scan_object(void* context, void* object) {
    gc_card_scan_context* scan = (gc_card_scan_context*)context;
    size_t offset;
    size_t card;
    uint64_t word;

    offset = (size_t)((uint8_t*)object - scan->region->base);
    card = offset / WILLRT_GC_CARD_SIZE;
    if (card >= scan->region->card_count) {
        return;
    }
    word = atomic_load_explicit(&scan->region->card_bits[card >> 6],
                                memory_order_acquire);
    if ((word & (UINT64_C(1) << (card & 63u))) == 0) {
        return;
    }
    if (scan->only_live && !gc_object_marked(object)) {
        return;
    }
    gc_scan_object_fields(object, scan->visit, scan->context);
    if (atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0) {
        gc_scan_object_weak(object, gc_weak_slot, NULL);
    }
}

static void gc_card_remark_object(void* context, void* object) {
    gc_card_scan_context* scan = (gc_card_scan_context*)context;
    size_t offset;
    size_t card;

    if (scan->only_live && !gc_object_marked(object)) {
        return;
    }
    if (!gc_card_object_has_young_ref(object)) {
        return;
    }
    offset = (size_t)((uint8_t*)object - scan->region->base);
    card = offset / WILLRT_GC_CARD_SIZE;
    if (card < scan->region->card_count) {
        atomic_fetch_or_explicit(
            &scan->region->card_bits[card >> 6],
            UINT64_C(1) << (card & 63u), memory_order_release);
    }
}

static int gc_region_has_dirty_card(gc_region* region) {
    size_t word;

    if (region->card_bits == NULL) {
        return 0;
    }
    for (word = 0; word < (region->card_count + 63u) / 64u; word++) {
        if (atomic_load_explicit(&region->card_bits[word],
                                 memory_order_acquire) != 0) {
            return 1;
        }
    }
    return 0;
}

void gc_card_scan(willrt_gc_root_visit visit, void* context, int only_live) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];
        gc_card_scan_context scan;

        if (region == NULL || region->kind == GC_REGION_FREE ||
            (region->kind == GC_REGION_YOUNG &&
             atomic_load_explicit(&region->pin_count,
                                  memory_order_acquire) == 0) ||
            !gc_region_has_dirty_card(region)) {
            continue;
        }
        /* A card can cover several small objects. Scan every object whose
           start lies in that card; card_objects keeps only one offset and
           would silently miss the rest. */
        scan.region = region;
        scan.visit = visit;
        scan.context = context;
        scan.only_live = only_live;
        gc_region_scan_objects(region, gc_card_scan_object, &scan);
        for (size_t card = 0; card < region->card_count; card++) {
            atomic_fetch_and_explicit(
                &region->card_bits[card >> 6],
                ~(UINT64_C(1) << (card & 63u)), memory_order_acq_rel);
        }
        gc_region_scan_objects(region, gc_card_remark_object, &scan);
    }
}

void gc_barrier_init(void) {
    atomic_store_explicit(&gc_good_color, WILLRT_GC_COLOR_YOUNG,
                          memory_order_release);
    atomic_store_explicit(&gc_mark_color, WILLRT_GC_MARK_UNMARKED,
                          memory_order_release);
    atomic_store_explicit(&gc_marking, 0, memory_order_release);
    atomic_store_explicit(&gc_evacuating, 0, memory_order_release);
}

void gc_barrier_shutdown(void) {
    gc_satb_chunk* chunk = atomic_exchange_explicit(
        &gc_satb_active, NULL, memory_order_acquire);

    while (chunk != NULL) {
        gc_satb_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);

        gc_chunk_release(chunk);
        chunk = next;
    }
    if (gc_satb_local != NULL) {
        gc_chunk_release(gc_satb_local);
        gc_satb_local = NULL;
    }
}
