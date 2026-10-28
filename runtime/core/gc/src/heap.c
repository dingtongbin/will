/* Heap foundation for the collector: reserved arena, region table, TLAB bump
   allocation, large and pinned regions, and the object header state. This file
   deliberately stops at allocation; marking, copying, and paging stay in their
   own modules. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct gc_object {
    uint8_t* base;
    size_t size;
} gc_object;

willrt_os_mutex willrt_gc_heap_lock;
gc_region gc_regions[WILLRT_GC_REGION_MAX];
size_t gc_region_count;
size_t willrt_gc_arena_used;

static atomic_int willrt_gc_heap_state;
static uint8_t* willrt_gc_arena;
static size_t willrt_gc_arena_size;
static atomic_size_t willrt_gc_allocated;
static atomic_size_t willrt_gc_object_total;
static gc_object gc_objects[65536u];
static size_t willrt_gc_refills;
static size_t willrt_gc_large_count;
static size_t willrt_gc_pinned_count;
static _Thread_local gc_tlab gc_local_tlab;

static size_t gc_round_up(size_t size, size_t align) {
    size_t rest;

    if (align <= 1) {
        return size;
    }
    rest = size % align;
    return rest == 0 ? size : size + align - rest;
}

static void gc_abort(const char* message) {
    (void)message;
    abort();
}

static void gc_heap_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&willrt_gc_heap_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&willrt_gc_heap_state, &expected,
                                                1, memory_order_acq_rel,
                                                memory_order_acquire)) {
        size_t page = willrt_os_page_size();

        willrt_os_mutex_init(&willrt_gc_heap_lock);
        willrt_gc_arena_size = gc_round_up(WILLRT_GC_ARENA_SIZE, page);
        willrt_gc_arena =
            (uint8_t*)willrt_os_reserve_pages(willrt_gc_arena_size);
        if (willrt_gc_arena == NULL) {
            gc_abort("GC arena reservation failed");
        }
        atomic_store_explicit(&willrt_gc_heap_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&willrt_gc_heap_state, memory_order_acquire) !=
           2) {
        willrt_os_thread_yield();
    }
}

static size_t gc_region_new_locked(uint32_t kind, size_t size) {
    size_t id;
    size_t page = willrt_os_page_size();
    size_t region_size = gc_round_up(size, page);
    uint8_t* base;

    if (region_size < WILLRT_GC_REGION_SIZE && kind <= WILLRT_GC_REGION_OLD) {
        region_size = WILLRT_GC_REGION_SIZE;
    }
    for (id = 0; id < WILLRT_GC_REGION_MAX; id++) {
        if (gc_regions[id].base == NULL) {
            break;
        }
    }
    if (id == WILLRT_GC_REGION_MAX) {
        gc_abort("GC region table is full");
    }
    if (kind == WILLRT_GC_REGION_YOUNG || kind == WILLRT_GC_REGION_OLD) {
        if (willrt_gc_arena_used > willrt_gc_arena_size ||
            region_size > willrt_gc_arena_size - willrt_gc_arena_used) {
            gc_abort("GC arena is full");
        }
        base = willrt_gc_arena + willrt_gc_arena_used;
        willrt_gc_arena_used += region_size;
    } else {
        base = (uint8_t*)willrt_os_reserve_pages(region_size);
        if (base == NULL) {
            gc_abort("GC large region reservation failed");
        }
    }
    if (willrt_os_commit_pages(base, region_size) != 0) {
        gc_abort("GC region commit failed");
    }
    gc_regions[id].base = base;
    gc_regions[id].size = region_size;
    gc_regions[id].top = region_size;
    gc_regions[id].kind = kind;
    gc_regions[id].age = 0;
    gc_region_count++;
    if (kind == WILLRT_GC_REGION_LARGE) {
        willrt_gc_large_count++;
    } else if (kind == WILLRT_GC_REGION_PINNED) {
        willrt_gc_pinned_count++;
    }
    return id;
}

static gc_region* gc_tlab_refill(size_t total) {
    size_t id;

    gc_heap_init();
    if (total > WILLRT_GC_TLAB_SIZE) {
        willrt_os_mutex_lock(&willrt_gc_heap_lock);
        id = gc_region_new_locked(WILLRT_GC_REGION_YOUNG, total);
        gc_regions[id].top = total;
        willrt_os_mutex_unlock(&willrt_gc_heap_lock);
        gc_local_tlab.region = &gc_regions[id];
        gc_local_tlab.cur = gc_local_tlab.region->base;
        gc_local_tlab.limit = gc_local_tlab.region->base + total;
        return gc_local_tlab.region;
    }
    willrt_os_mutex_lock(&willrt_gc_heap_lock);
    id = gc_region_new_locked(WILLRT_GC_REGION_YOUNG, WILLRT_GC_REGION_SIZE);
    gc_local_tlab.region = &gc_regions[id];
    gc_local_tlab.cur = gc_local_tlab.region->base;
    gc_local_tlab.limit = gc_local_tlab.region->base + WILLRT_GC_REGION_SIZE;
    willrt_gc_refills++;
    willrt_os_mutex_unlock(&willrt_gc_heap_lock);
    return gc_local_tlab.region;
}

uint64_t willrt_gc_state_make(uint32_t region, uint32_t type,
                              uint32_t flags) {
    return ((uint64_t)type << WILLRT_GC_STATE_TYPE_SHIFT) |
           ((uint64_t)region << WILLRT_GC_STATE_REGION_SHIFT) | flags;
}

uint32_t willrt_gc_state_region(uint64_t state) {
    return (uint32_t)((state & WILLRT_GC_STATE_REGION_MASK) >>
                      WILLRT_GC_STATE_REGION_SHIFT);
}

uint32_t willrt_gc_state_type(uint64_t state) {
    return (uint32_t)((state & WILLRT_GC_STATE_TYPE_MASK) >>
                      WILLRT_GC_STATE_TYPE_SHIFT);
}

uint32_t willrt_gc_state_pin(uint64_t state) {
    return (uint32_t)((state & WILLRT_GC_STATE_PIN_MASK) >>
                      WILLRT_GC_STATE_PIN_SHIFT);
}

void* willrt_gc_alloc(size_t size, const willrt_gc_type* type) {
    size_t total;
    uint32_t flags = 0;
    uint32_t kind = WILLRT_GC_REGION_YOUNG;
    uint32_t region;
    uint8_t* base;
    willrt_gc_header* header;
    gc_region* owner;
    uint64_t state_flags = 0;
    uint32_t type_id = gc_type_id(type);

    if (size > SIZE_MAX - WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
        gc_abort("GC allocation size overflow");
    }
    total = gc_round_up(WILLRT_GC_OBJECT_PAYLOAD_OFFSET + size,
                        WILLRT_GC_OBJECT_ALIGN);
    if (type != NULL) {
        flags = type->flags;
    }
    if ((flags & WILLRT_GC_TYPE_PINNED) != 0) {
        kind = WILLRT_GC_REGION_PINNED;
    } else if ((flags & WILLRT_GC_TYPE_LARGE) != 0 ||
               size > WILLRT_GC_LARGE_THRESHOLD) {
        kind = WILLRT_GC_REGION_LARGE;
    }
    if (kind != WILLRT_GC_REGION_YOUNG) {
        size_t id;

        gc_heap_init();
        willrt_os_mutex_lock(&willrt_gc_heap_lock);
        id = gc_region_new_locked(kind, total);
        gc_regions[id].top = total;
        willrt_os_mutex_unlock(&willrt_gc_heap_lock);
        owner = &gc_regions[id];
        base = owner->base;
        region = (uint32_t)id;
        if (kind == WILLRT_GC_REGION_LARGE) {
            state_flags |= WILLRT_GC_STATE_LARGE;
        } else if (kind == WILLRT_GC_REGION_PINNED) {
            state_flags |= WILLRT_GC_STATE_PINNED_REGION;
        }
    } else {
        gc_heap_init();
        if (gc_local_tlab.cur == NULL ||
            total > (size_t)(gc_local_tlab.limit - gc_local_tlab.cur)) {
            owner = gc_tlab_refill(total);
        } else {
            owner = gc_local_tlab.region;
        }
        base = gc_local_tlab.cur;
        gc_local_tlab.cur += total;
        region = (uint32_t)(owner - gc_regions);
    }
    header = (willrt_gc_header*)base;
    atomic_store_explicit(&header->state,
                          willrt_gc_state_make(region, type_id, state_flags),
                          memory_order_relaxed);
    {
        size_t index = atomic_fetch_add_explicit(&willrt_gc_object_total, 1,
                                                 memory_order_relaxed);
        if (index >= 65536u) {
            gc_abort("GC object table is full");
        }
        gc_objects[index].base = base;
        gc_objects[index].size = total;
    }
    atomic_fetch_add_explicit(&willrt_gc_allocated, total, memory_order_relaxed);
    return base + WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
}

void* willrt_gc_pin(void* object) {
    willrt_gc_header* header;
    uint64_t old;
    uint32_t count;

    if (object == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(object);
    header = (willrt_gc_header*)((uint8_t*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    old = atomic_load_explicit(&header->state, memory_order_relaxed);
    count = willrt_gc_state_pin(old);
    if (count == 0xffu) {
        gc_abort("GC pin count overflow");
    }
    atomic_fetch_add_explicit(&header->state,
                              (uint64_t)1 << WILLRT_GC_STATE_PIN_SHIFT,
                              memory_order_acq_rel);
    atomic_fetch_or_explicit(&header->state, WILLRT_GC_STATE_PINNED,
                             memory_order_release);
    return object;
}

void willrt_gc_unpin(void* object) {
    willrt_gc_header* header;
    uint64_t old;
    uint32_t count;

    if (object == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    header = (willrt_gc_header*)((uint8_t*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    old = atomic_load_explicit(&header->state, memory_order_relaxed);
    count = willrt_gc_state_pin(old);
    if (count == 0) {
        gc_abort("GC unpin without a matching pin");
    }
    atomic_fetch_sub_explicit(&header->state,
                              (uint64_t)1 << WILLRT_GC_STATE_PIN_SHIFT,
                              memory_order_acq_rel);
    if (count == 1) {
        atomic_fetch_and_explicit(&header->state, ~WILLRT_GC_STATE_PINNED,
                                  memory_order_release);
    }
}

void willrt_gc_tlab_new_young(void) {
    size_t id;

    gc_heap_init();
    willrt_os_mutex_lock(&willrt_gc_heap_lock);
    id = gc_region_new_locked(WILLRT_GC_REGION_YOUNG, WILLRT_GC_REGION_SIZE);
    willrt_os_mutex_unlock(&willrt_gc_heap_lock);
    gc_local_tlab.region = &gc_regions[id];
    gc_local_tlab.cur = gc_local_tlab.region->base;
    gc_local_tlab.limit = gc_local_tlab.region->base + WILLRT_GC_REGION_SIZE;
}

int willrt_gc_heap_offset(const void* ptr, size_t* offset) {
    const uint8_t* address = (const uint8_t*)ptr;

    if (atomic_load_explicit(&willrt_gc_heap_state, memory_order_acquire) != 2 ||
        address == NULL || address < willrt_gc_arena ||
        address >= willrt_gc_arena + willrt_gc_arena_size) {
        return 0;
    }
    if (offset != NULL) {
        *offset = (size_t)(address - willrt_gc_arena);
    }
    return 1;
}

size_t willrt_gc_object_count(void) {
    return atomic_load_explicit(&willrt_gc_object_total, memory_order_acquire);
}

int willrt_gc_object_at(size_t index, void** base, size_t* size) {
    if (index >= willrt_gc_object_count()) {
        return 0;
    }
    if (base != NULL) {
        *base = gc_objects[index].base;
    }
    if (size != NULL) {
        *size = gc_objects[index].size;
    }
    return 1;
}

size_t willrt_gc_test_object_count(void) {
    return willrt_gc_object_count();
}

size_t willrt_gc_test_region_count(void) {
    return gc_region_count;
}

size_t willrt_gc_test_allocated_bytes(void) {
    return atomic_load_explicit(&willrt_gc_allocated, memory_order_relaxed);
}

size_t willrt_gc_test_tlab_refills(void) {
    return willrt_gc_refills;
}

size_t willrt_gc_test_large_regions(void) {
    return willrt_gc_large_count;
}

size_t willrt_gc_test_pinned_regions(void) {
    return willrt_gc_pinned_count;
}

size_t willrt_gc_test_tlab_size(void) {
    return WILLRT_GC_TLAB_SIZE;
}

size_t willrt_gc_test_region_size(void) {
    return WILLRT_GC_REGION_SIZE;
}
