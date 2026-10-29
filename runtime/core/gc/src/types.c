/* Static type descriptors. The compiler assigns the id; registration validates
   the descriptor once and publishes an immutable hash table. Allocation reads
   type->id without a lock, and the marker resolves ids through the current
   table. There is no runtime type interning or fixed type table. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct gc_type_entry {
    uint32_t id;
    const willrt_gc_type* type;
} gc_type_entry;

typedef struct gc_type_table {
    size_t capacity;
    size_t count;
    gc_type_entry entries[];
} gc_type_table;

static _Atomic(gc_type_table*) gc_types;
static willrt_os_mutex gc_type_lock;
static atomic_int gc_type_state;

static void gc_types_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_type_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_type_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        gc_type_table* table;
        size_t capacity = 256;

        willrt_os_mutex_init(&gc_type_lock);
        table = (gc_type_table*)willrt_alloc(
            sizeof(gc_type_table) + capacity * sizeof(gc_type_entry));
        if (table == NULL) {
            abort();
        }
        table->capacity = capacity;
        table->count = 0;
        memset(table->entries, 0, capacity * sizeof(gc_type_entry));
        atomic_store_explicit(&gc_types, table, memory_order_release);
        atomic_store_explicit(&gc_type_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_type_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

static size_t gc_type_hash(uint32_t id, size_t capacity) {
    uint64_t hash = (uint64_t)id * UINT64_C(11400714819323198485);

    return (size_t)(hash & (capacity - 1u));
}

static const willrt_gc_type* gc_type_find(const gc_type_table* table,
                                           uint32_t id) {
    size_t index;

    if (table == NULL || id == 0) {
        return NULL;
    }
    index = gc_type_hash(id, table->capacity);
    for (;;) {
        const gc_type_entry* entry = &table->entries[index];

        if (entry->id == 0) {
            return NULL;
        }
        if (entry->id == id) {
            return entry->type;
        }
        index = (index + 1u) & (table->capacity - 1u);
    }
}

static void gc_type_insert(gc_type_table* table, uint32_t id,
                           const willrt_gc_type* type) {
    size_t index = gc_type_hash(id, table->capacity);

    while (table->entries[index].id != 0) {
        index = (index + 1u) & (table->capacity - 1u);
    }
    table->entries[index].id = id;
    table->entries[index].type = type;
    table->count++;
}

static int gc_type_validate(const willrt_gc_type* type) {
    uint32_t i;

    if (type == NULL || type->id == 0 || type->id > WILLRT_GC_TYPE_ID_MAX) {
        return -1;
    }
    if (type->align == 0 || (type->align & (type->align - 1u)) != 0 ||
        type->align > 4096u) {
        return -1;
    }
    if (type->reference_count != 0 && type->reference_offsets == NULL) {
        return -1;
    }
    if (type->weak_count != 0 && type->weak_offsets == NULL) {
        return -1;
    }
    if ((type->flags & WILLRT_GC_TYPE_SEQUENCE) != 0) {
        if (type->element_size == 0) {
            return -1;
        }
        return 0;
    }
    if ((type->flags & (WILLRT_GC_TYPE_NESTED | WILLRT_GC_TYPE_ENUM)) != 0) {
        if (type->reference_offsets == NULL) {
            return -1;
        }
        return 0;
    }
    for (i = 0; i < type->reference_count; i++) {
        uint32_t offset = type->reference_offsets[i];

        if (offset > type->size ||
            sizeof(void*) > (size_t)type->size - (size_t)offset) {
            return -1;
        }
    }
    for (i = 0; i < type->weak_count; i++) {
        uint32_t offset = type->weak_offsets[i];

        if (offset > type->size ||
            sizeof(void*) > (size_t)type->size - (size_t)offset) {
            return -1;
        }
    }
    return 0;
}

int32_t gc_type_register(const willrt_gc_type* type) {
    gc_type_table* old;
    gc_type_table* next;
    const willrt_gc_type* existing;
    size_t capacity;
    size_t i;

    if (gc_type_validate(type) != 0) {
        return -1;
    }
    gc_types_init();
    willrt_os_mutex_lock(&gc_type_lock);
    old = atomic_load_explicit(&gc_types, memory_order_acquire);
    existing = gc_type_find(old, type->id);
    if (existing != NULL) {
        int32_t result = existing == type ? 0 : -1;

        willrt_os_mutex_unlock(&gc_type_lock);
        return result;
    }
    capacity = old->capacity;
    if ((old->count + 1u) * 10u >= capacity * 7u) {
        capacity *= 2u;
    }
    next = (gc_type_table*)willrt_alloc(
        sizeof(gc_type_table) + capacity * sizeof(gc_type_entry));
    if (next == NULL) {
        willrt_os_mutex_unlock(&gc_type_lock);
        return -1;
    }
    next->capacity = capacity;
    next->count = old->count;
    memset(next->entries, 0, capacity * sizeof(gc_type_entry));
    for (i = 0; i < old->capacity; i++) {
        if (old->entries[i].id != 0) {
            gc_type_insert(next, old->entries[i].id, old->entries[i].type);
        }
    }
    gc_type_insert(next, type->id, type);
    atomic_store_explicit(&gc_types, next, memory_order_release);
    willrt_os_mutex_unlock(&gc_type_lock);
    return 0;
}

const willrt_gc_type* gc_type_get(uint32_t id) {
    gc_type_table* table;

    if (id == 0) {
        return NULL;
    }
    table = atomic_load_explicit(&gc_types, memory_order_acquire);
    return gc_type_find(table, id);
}

size_t gc_type_payload_size(const willrt_gc_type* type) {
    if (type == NULL || (type->flags & WILLRT_GC_TYPE_SEQUENCE) != 0) {
        return 0u;
    }
    return (size_t)type->size;
}
