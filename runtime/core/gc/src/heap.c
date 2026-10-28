/* Heap startup, TLAB allocation, large/pinned allocation, and pin handling.
   Regions and bitmaps live in region.c; this file owns the view-mapped arena
   and the allocation fast path. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static atomic_int gc_heap_state;
static willrt_os_mutex gc_heap_lock;
static willrt_os_gc_views gc_views;
static gc_region* gc_current_young;
static _Atomic uint32_t gc_tlab_epoch;
static _Atomic int gc_alloc_freeze_flag;
static _Atomic size_t gc_active_allocs;
static _Thread_local gc_tlab gc_tlab_current;

static size_t gc_round_up(size_t value, size_t align) {
    size_t rest;

    if (align <= 1u) {
        return value;
    }
    rest = value % align;
    return rest == 0u ? value : value + align - rest;
}

static size_t gc_region_order_for(size_t bytes) {
    size_t needed = gc_round_up(bytes, (size_t)WILLRT_GC_GRANULE_SIZE);
    size_t order = 0;
    size_t size = (size_t)WILLRT_GC_GRANULE_SIZE;

    while (size < needed && order < WILLRT_GC_REGION_ORDER_MAX) {
        size <<= 1u;
        order++;
    }
    return size < needed ? WILLRT_GC_REGION_ORDER_MAX + 1u : order;
}

static void gc_heap_fail(const char* message) {
    (void)message;
    abort();
}

static void gc_heap_setup(void) {
    int expected = 0;
    size_t page;
    size_t heap_size;
    int mapped = 0;

    if (atomic_load_explicit(&gc_heap_state, memory_order_acquire) == 2) {
        return;
    }
    if (!atomic_compare_exchange_strong_explicit(&gc_heap_state, &expected, 1,
                                                 memory_order_acq_rel,
                                                 memory_order_acquire)) {
        while (atomic_load_explicit(&gc_heap_state, memory_order_acquire) !=
               2) {
            willrt_os_thread_yield();
        }
        return;
    }
    willrt_os_mutex_init(&gc_heap_lock);
    page = willrt_os_page_size();
    heap_size = gc_round_up((size_t)WILLRT_GC_HEAP_MAX, page);
    for (;;) {
        if (heap_size < (size_t)WILLRT_GC_HEAP_MIN) {
            break;
        }
        if (willrt_os_gc_views_open(&gc_views, heap_size,
                                    (size_t)WILLRT_GC_VIEW_STRIDE) == 0) {
            mapped = 1;
            break;
        }
        if (heap_size < (size_t)WILLRT_GC_GRANULE_SIZE * 2u) {
            break;
        }
        heap_size >>= 1u;
    }
    if (!mapped) {
        gc_heap_fail("GC multi-view mapping failed");
    }
    gc_heap_size = gc_views.size;
    gc_heap_base = (uint8_t*)gc_views.base;
    gc_region_init();
    gc_lane_init();
    gc_barrier_init();
    gc_mark_init();
    gc_pacer_init();
    gc_cycle_init();
    gc_tlab_current.region = NULL;
    gc_tlab_current.cur = NULL;
    gc_tlab_current.limit = NULL;
    gc_tlab_current.start = NULL;
    gc_tlab_current.lane = 0;
    gc_tlab_current.epoch = 1;
    atomic_store_explicit(&gc_tlab_epoch, 1, memory_order_release);
    atomic_store_explicit(&gc_heap_state, 2, memory_order_release);
}

_Noreturn void gc_oom(void) {
    fputs("error: out of memory\n", stderr);
    fflush(stderr);
    abort();
}

void gc_alloc_freeze(void) {
    atomic_store_explicit(&gc_alloc_freeze_flag, 1, memory_order_release);
    while (atomic_load_explicit(&gc_active_allocs, memory_order_acquire) !=
           0) {
        willrt_os_thread_yield();
    }
}

void gc_alloc_thaw(void) {
    atomic_store_explicit(&gc_alloc_freeze_flag, 0, memory_order_release);
}

void gc_heap_init(void) {
    gc_heap_setup();
}

void gc_heap_shutdown(void) {
    if (atomic_load_explicit(&gc_heap_state, memory_order_acquire) != 2) {
        return;
    }
    gc_cycle_shutdown();
    gc_pacer_shutdown();
    gc_barrier_shutdown();
    gc_mark_shutdown();
    gc_safepoint_shutdown();
    gc_chunk_shutdown();
    gc_region_shutdown();
    willrt_os_gc_views_close(&gc_views);
    gc_heap_base = NULL;
    gc_heap_size = 0;
    gc_current_young = NULL;
    gc_tlab_current.region = NULL;
    gc_tlab_current.cur = NULL;
    gc_tlab_current.limit = NULL;
    gc_tlab_current.start = NULL;
    gc_tlab_current.epoch = 0;
    atomic_store_explicit(&gc_heap_state, 0, memory_order_release);
}

void gc_heap_epoch_bump(void) {
    uint32_t epoch;

    willrt_os_mutex_lock(&gc_heap_lock);
    epoch = atomic_load_explicit(&gc_tlab_epoch, memory_order_acquire) + 1u;
    if (epoch == 0u) {
        epoch = 1u;
    }
    atomic_store_explicit(&gc_tlab_epoch, epoch, memory_order_release);
    gc_current_young = NULL;
    willrt_os_mutex_unlock(&gc_heap_lock);
}

static gc_region* gc_tlab_refill(void) {
    gc_region* region;
    size_t start;
    size_t limit;

    willrt_os_mutex_lock(&gc_heap_lock);
    region = gc_current_young;
    if (region == NULL || region->kind != GC_REGION_YOUNG ||
        region->start_bits == NULL ||
        atomic_load_explicit(&region->pin_count, memory_order_acquire) != 0 ||
        (region->flags & GC_REGION_EVACUATING) != 0) {
        region = gc_region_alloc(0, GC_REGION_YOUNG);
        if (region != NULL) {
            gc_current_young = region;
        }
    }
    if (region == NULL) {
        willrt_os_mutex_unlock(&gc_heap_lock);
        return NULL;
    }
    start = atomic_load_explicit(&region->top, memory_order_relaxed);
    if (start > region->size ||
        (size_t)WILLRT_GC_TLAB_SIZE > region->size - start) {
        region = gc_region_alloc(0, GC_REGION_YOUNG);
        if (region == NULL) {
            willrt_os_mutex_unlock(&gc_heap_lock);
            return NULL;
        }
        gc_current_young = region;
        start = 0;
    }
    limit = start + (size_t)WILLRT_GC_TLAB_SIZE;
    atomic_store_explicit(&region->top, limit, memory_order_release);
    gc_tlab_current.region = region;
    gc_tlab_current.start = region->base + start;
    gc_tlab_current.cur = gc_tlab_current.start;
    gc_tlab_current.limit = region->base + limit;
    gc_tlab_current.epoch = atomic_load_explicit(&gc_tlab_epoch,
                                                 memory_order_acquire);
    willrt_os_mutex_unlock(&gc_heap_lock);
    return region;
}

void gc_object_publish(gc_region* region, size_t offset, size_t total,
                       uint32_t type_id, uint32_t flags) {
    willrt_gc_header* header = (willrt_gc_header*)(region->base + offset);
    uint32_t card;
    uint32_t first_card = (uint32_t)(offset / WILLRT_GC_CARD_SIZE);
    uint32_t last_card =
        (uint32_t)((offset + total - 1u) / WILLRT_GC_CARD_SIZE);

    gc_region_set_start(region, offset);
    gc_region_set_end(region, offset + total);
    for (card = first_card; card <= last_card && card < region->card_count;
         card++) {
        atomic_store_explicit(&region->card_objects[card], (uint32_t)offset,
                              memory_order_release);
    }
    atomic_store_explicit(&header->state,
                          WILLRT_GC_STATE_MAKE(region->id, type_id, flags),
                          memory_order_release);
}

static size_t gc_type_align(const willrt_gc_type* type) {
    size_t align = type == NULL ? WILLRT_GC_OBJECT_ALIGN : type->align;

    return align < WILLRT_GC_OBJECT_ALIGN ? WILLRT_GC_OBJECT_ALIGN : align;
}

static void* gc_alloc_large(size_t payload_size, uint32_t type_id,
                            uint32_t flags, uint32_t kind, size_t align) {
    size_t object_size =
        gc_round_up(WILLRT_GC_OBJECT_PAYLOAD_OFFSET + payload_size,
                    WILLRT_GC_OBJECT_ALIGN);
    size_t base_offset = gc_round_up(WILLRT_GC_OBJECT_PAYLOAD_OFFSET, align) -
                         WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    size_t total = base_offset + object_size;
    size_t order = gc_region_order_for(total);
    gc_region* region;
    uint8_t* payload;

    if (order > WILLRT_GC_REGION_ORDER_MAX) {
        gc_oom();
    }
    region = gc_region_alloc(order, kind);
    if (region == NULL) {
        gc_oom();
    }
    region->flags = (kind == GC_REGION_LARGE ? GC_REGION_LARGE_FLAG : 0u) |
                    GC_REGION_RETIRED;
    atomic_store_explicit(&region->top, total, memory_order_release);
    atomic_fetch_add_explicit(&region->active_allocs, 1,
                              memory_order_acq_rel);
    gc_object_publish(region, base_offset, object_size, type_id,
                      flags | (kind == GC_REGION_LARGE
                                   ? WILLRT_GC_STATE_LARGE
                                   : WILLRT_GC_STATE_PINNED_REGION));
    payload = region->base + base_offset + WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    memset(payload, 0, payload_size);
    atomic_fetch_sub_explicit(&region->active_allocs, 1,
                              memory_order_acq_rel);
    gc_pacer_on_alloc(total);
    return payload;
}

static void* gc_alloc_inner(size_t size, const willrt_gc_type* type) {
    size_t payload_size;
    size_t object_size;
    size_t align;
    uint32_t type_id = type == NULL ? 0u : type->id;
    uint32_t flags = type == NULL ? 0u : type->flags;
    uint32_t state_flags = 0;
    gc_region* region;
    size_t offset;
    uint8_t* payload;
    uint8_t* base;

    gc_heap_setup();
    if (gc_lane_current() < 0) {
        willrt_gc_lane_enter(0);
    }
    if (type != NULL && type->size > size) {
        size = type->size;
    }
    if (size > SIZE_MAX - WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
        gc_oom();
    }
    payload_size = size;
    align = gc_type_align(type);
    object_size = gc_round_up(WILLRT_GC_OBJECT_PAYLOAD_OFFSET + payload_size,
                              WILLRT_GC_OBJECT_ALIGN);
    if ((flags & WILLRT_GC_TYPE_PINNED) != 0) {
        return gc_alloc_large(payload_size, type_id, flags, GC_REGION_PINNED,
                              align);
    }
    if ((flags & WILLRT_GC_TYPE_LARGE) != 0 ||
        object_size > WILLRT_GC_LARGE_THRESHOLD) {
        return gc_alloc_large(payload_size, type_id, flags, GC_REGION_LARGE,
                              align);
    }
    for (;;) {
        if (gc_tlab_current.region == NULL ||
            gc_tlab_current.epoch != atomic_load_explicit(
                                        &gc_tlab_epoch,
                                        memory_order_acquire) ||
            gc_tlab_current.region->kind != GC_REGION_YOUNG ||
            gc_tlab_current.region->start_bits == NULL) {
            if (gc_tlab_refill() == NULL) {
                gc_oom();
            }
            continue;
        }
        region = gc_tlab_current.region;
        atomic_fetch_add_explicit(&region->active_allocs, 1,
                                  memory_order_acq_rel);
        if (region->kind != GC_REGION_YOUNG || region->start_bits == NULL ||
            gc_tlab_current.epoch != atomic_load_explicit(
                                        &gc_tlab_epoch,
                                        memory_order_acquire) ||
            (region->flags & GC_REGION_EVACUATING) != 0) {
            atomic_fetch_sub_explicit(&region->active_allocs, 1,
                                      memory_order_acq_rel);
            if (gc_tlab_refill() == NULL) {
                gc_oom();
            }
            continue;
        }
        base = (uint8_t*)(gc_round_up(
                              (uintptr_t)gc_tlab_current.cur +
                                  WILLRT_GC_OBJECT_PAYLOAD_OFFSET,
                              align) -
                          WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
        if (base >= gc_tlab_current.cur &&
            object_size <= (size_t)(gc_tlab_current.limit - base)) {
            break;
        }
        atomic_fetch_sub_explicit(&region->active_allocs, 1,
                                  memory_order_acq_rel);
        if (gc_tlab_refill() == NULL) {
            gc_oom();
        }
    }
    if (type != NULL && (flags & WILLRT_GC_TYPE_LARGE) != 0) {
        state_flags |= WILLRT_GC_STATE_LARGE;
    }
    offset = (size_t)(base - region->base);
    gc_object_publish(region, offset, object_size, type_id, state_flags);
    payload = base + WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    memset(payload, 0, payload_size);
    gc_tlab_current.cur = base + object_size;
    if ((region->flags & GC_REGION_EVACUATING) != 0 ||
        gc_tlab_current.epoch != atomic_load_explicit(&gc_tlab_epoch,
                                                      memory_order_acquire)) {
        void* forwarded = gc_object_forward(payload);

        if (forwarded == NULL) {
            gc_oom();
        }
        payload = forwarded;
    }
    atomic_fetch_sub_explicit(&region->active_allocs, 1,
                              memory_order_acq_rel);
    gc_pacer_on_alloc(object_size);
    return payload;
}

void* gc_alloc(size_t size, const willrt_gc_type* type) {
    void* result;

    for (;;) {
        while (atomic_load_explicit(&gc_alloc_freeze_flag,
                                    memory_order_acquire) != 0) {
            willrt_os_thread_yield();
        }
        atomic_fetch_add_explicit(&gc_active_allocs, 1,
                                  memory_order_acq_rel);
        if (atomic_load_explicit(&gc_alloc_freeze_flag,
                                 memory_order_acquire) == 0) {
            break;
        }
        atomic_fetch_sub_explicit(&gc_active_allocs, 1,
                                  memory_order_acq_rel);
    }
    result = gc_alloc_inner(size, type);
    atomic_fetch_sub_explicit(&gc_active_allocs, 1, memory_order_acq_rel);
    return result;
}

void* willrt_gc_alloc(size_t size, const willrt_gc_type* type) {
    return gc_alloc(size, type);
}

void willrt_gc_shutdown(void) {
    if (gc_lane_current() >= 0) {
        willrt_gc_lane_leave();
    }
    gc_heap_shutdown();
}

int32_t willrt_gc_register_type(const willrt_gc_type* type) {
    return gc_type_register(type);
}

static willrt_gc_header* gc_header_from_object(void* object) {
    return (willrt_gc_header*)((uint8_t*)object -
                               WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
}

void* willrt_gc_pin(void* object) {
    willrt_gc_header* header;
    uint64_t old;
    uint64_t next;
    uint32_t count;

    if (object == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(object);
    header = gc_header_from_object(object);
    for (;;) {
        old = atomic_load_explicit(&header->state, memory_order_acquire);
        if ((old & WILLRT_GC_STATE_FORWARDED) != 0) {
            object = WILLRT_GC_STATE_FORWARD_ADDRESS(old);
            header = gc_header_from_object(object);
            continue;
        }
        count = (uint32_t)((old & WILLRT_GC_STATE_PIN_MASK) >>
                           WILLRT_GC_STATE_PIN_SHIFT);
        if (count == 0xffffu) {
            gc_heap_fail("GC pin count overflow");
        }
        next = (old & ~WILLRT_GC_STATE_PIN_MASK) |
               ((uint64_t)(count + 1u) << WILLRT_GC_STATE_PIN_SHIFT) |
               WILLRT_GC_STATE_PINNED;
        if (atomic_compare_exchange_weak_explicit(
                &header->state, &old, next, memory_order_acq_rel,
                memory_order_acquire)) {
            gc_region* region = gc_region_by_id(WILLRT_GC_STATE_REGION(next));

            if (region != NULL) {
                atomic_fetch_add_explicit(&region->pin_count, 1,
                                          memory_order_acq_rel);
            }
            return object;
        }
    }
}

void willrt_gc_unpin(void* object) {
    willrt_gc_header* header;
    uint64_t old;
    uint64_t next;
    uint32_t count;

    if (object == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    header = gc_header_from_object(object);
    for (;;) {
        old = atomic_load_explicit(&header->state, memory_order_acquire);
        if ((old & WILLRT_GC_STATE_FORWARDED) != 0) {
            object = WILLRT_GC_STATE_FORWARD_ADDRESS(old);
            header = gc_header_from_object(object);
            continue;
        }
        count = (uint32_t)((old & WILLRT_GC_STATE_PIN_MASK) >>
                           WILLRT_GC_STATE_PIN_SHIFT);
        if (count == 0u) {
            gc_heap_fail("GC unpin without a matching pin");
        }
        next = old - (UINT64_C(1) << WILLRT_GC_STATE_PIN_SHIFT);
        if (count == 1u) {
            next &= ~WILLRT_GC_STATE_PINNED;
        }
        if (atomic_compare_exchange_weak_explicit(
                &header->state, &old, next, memory_order_acq_rel,
                memory_order_acquire)) {
            gc_region* region = gc_region_by_id(WILLRT_GC_STATE_REGION(next));

            if (region != NULL) {
                atomic_fetch_sub_explicit(&region->pin_count, 1,
                                          memory_order_acq_rel);
            }
            return;
        }
    }
}


void gc_lane_set_current(int32_t lane) {
    gc_tlab_current.lane = lane;
}
