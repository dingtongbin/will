/* Per-lane safepoints. A lane publishes its exact frame chain at a poll point
   and parks only that lane while the collector scans it. C/native regions are
   skipped; the caller must have pinned or handled every object visible there. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum gc_lane_state {
    GC_LANE_FREE = 0,
    GC_LANE_RUNNING = 1,
    GC_LANE_PARKED = 2,
    GC_LANE_NATIVE = 3
};

struct gc_lane_record {
    int32_t lane;
    _Atomic int32_t state;
    _Atomic int32_t stop;
    _Atomic int32_t ack;
    _Atomic int32_t native_depth;
    willrt_gc_frame* frame;
    willrt_os_cond cond;
};

static willrt_os_mutex gc_lane_lock;
static atomic_int gc_lane_ready;
static gc_lane_record** gc_lanes;
static size_t gc_lane_capacity;
static size_t gc_registered_lanes;
static _Thread_local gc_lane_record* gc_tls_lane;

static void gc_lane_init_once(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_lane_ready, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_lane_ready, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_lane_lock);
        atomic_store_explicit(&gc_lane_ready, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_lane_ready, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

static gc_lane_record* gc_lane_find(int32_t lane) {
    size_t i;

    for (i = 0; i < gc_lane_capacity; i++) {
        gc_lane_record* record = gc_lanes[i];

        if (record != NULL && record->lane == lane &&
            atomic_load_explicit(&record->state, memory_order_acquire) !=
                GC_LANE_FREE) {
            return record;
        }
    }
    return NULL;
}

static gc_lane_record* gc_lane_record_new(int32_t lane) {
    gc_lane_record* record;
    size_t i;

    for (i = 0; i < gc_lane_capacity; i++) {
        if (gc_lanes[i] == NULL ||
            atomic_load_explicit(&gc_lanes[i]->state, memory_order_acquire) ==
                GC_LANE_FREE) {
            break;
        }
    }
    if (i == gc_lane_capacity) {
        size_t capacity = gc_lane_capacity == 0 ? 16u : gc_lane_capacity * 2u;
        gc_lane_record** next =
            (gc_lane_record**)willrt_alloc(capacity * sizeof(*next));

        if (next == NULL) {
            return NULL;
        }
        memset(next, 0, capacity * sizeof(*next));
        if (gc_lanes != NULL) {
            memcpy(next, gc_lanes, gc_lane_capacity * sizeof(*next));
            willrt_free(gc_lanes);
        }
        gc_lanes = next;
        gc_lane_capacity = capacity;
    }
    record = gc_lanes[i];
    if (record == NULL) {
        record = (gc_lane_record*)willrt_alloc(sizeof(*record));
        if (record == NULL) {
            return NULL;
        }
        memset(record, 0, sizeof(*record));
        willrt_os_cond_init(&record->cond);
        gc_lanes[i] = record;
    }
    record->lane = lane;
    atomic_store_explicit(&record->state, GC_LANE_RUNNING, memory_order_release);
    atomic_store_explicit(&record->stop, 0, memory_order_release);
    atomic_store_explicit(&record->ack, 0, memory_order_release);
    atomic_store_explicit(&record->native_depth, 0, memory_order_release);
    record->frame = NULL;
    gc_registered_lanes++;
    return record;
}

void gc_lane_init(void) {
    gc_lane_init_once();
}

void gc_lane_shutdown(void) {
    size_t i;

    gc_lane_init_once();
    willrt_os_mutex_lock(&gc_lane_lock);
    for (i = 0; i < gc_lane_capacity; i++) {
        if (gc_lanes[i] != NULL) {
            willrt_os_cond_destroy(&gc_lanes[i]->cond);
            willrt_free(gc_lanes[i]);
            gc_lanes[i] = NULL;
        }
    }
    willrt_free(gc_lanes);
    gc_lanes = NULL;
    gc_lane_capacity = 0;
    gc_registered_lanes = 0;
    gc_tls_lane = NULL;
    willrt_os_mutex_unlock(&gc_lane_lock);
}

void willrt_gc_lane_enter(int32_t lane) {
    gc_lane_record* record;

    gc_lane_init_once();
    if (gc_tls_lane != NULL) {
        if (gc_tls_lane->lane == lane) {
            return;
        }
        abort();
    }
    willrt_os_mutex_lock(&gc_lane_lock);
    record = gc_lane_find(lane);
    if (record != NULL) {
        willrt_os_mutex_unlock(&gc_lane_lock);
        abort();
    }
    record = gc_lane_record_new(lane);
    willrt_os_mutex_unlock(&gc_lane_lock);
    if (record == NULL) {
        abort();
    }
    gc_tls_lane = record;
    gc_lane_set_current(lane);
}

void willrt_gc_lane_leave(void) {
    if (gc_tls_lane == NULL) {
        return;
    }
    gc_satb_flush_local();
    gc_mark_flush_local();
    gc_copy_flush_local();
    willrt_os_mutex_lock(&gc_lane_lock);
    atomic_store_explicit(&gc_tls_lane->state, GC_LANE_FREE,
                          memory_order_release);
    atomic_store_explicit(&gc_tls_lane->stop, 0, memory_order_release);
    atomic_store_explicit(&gc_tls_lane->ack, 0, memory_order_release);
    gc_tls_lane->frame = NULL;
    gc_registered_lanes--;
    gc_tls_lane = NULL;
    willrt_os_mutex_unlock(&gc_lane_lock);
}

void willrt_gc_frame_enter(willrt_gc_frame* frame,
                           const willrt_gc_stack_map_data* map, void* base) {
    if (frame == NULL) {
        return;
    }
    frame->map = map;
    frame->base = base;
    frame->parent = gc_tls_lane == NULL ? NULL : gc_tls_lane->frame;
    if (gc_tls_lane != NULL) {
        gc_tls_lane->frame = frame;
    }
}

void willrt_gc_frame_leave(willrt_gc_frame* frame) {
    if (gc_tls_lane == NULL || frame == NULL) {
        return;
    }
    if (gc_tls_lane->frame == frame) {
        gc_tls_lane->frame = frame->parent;
    }
}

static void gc_lane_park(gc_lane_record* record) {
    gc_satb_flush_local();
    gc_mark_flush_local();
    gc_copy_flush_local();
    willrt_os_mutex_lock(&gc_lane_lock);
    while (atomic_load_explicit(&record->stop, memory_order_acquire) != 0) {
        atomic_store_explicit(&record->state, GC_LANE_PARKED,
                              memory_order_release);
        atomic_store_explicit(&record->ack, 1, memory_order_release);
        willrt_os_cond_signal(&record->cond);
        willrt_os_cond_wait(&record->cond, &gc_lane_lock);
    }
    atomic_store_explicit(&record->state, GC_LANE_RUNNING, memory_order_release);
    atomic_store_explicit(&record->ack, 0, memory_order_release);
    willrt_os_mutex_unlock(&gc_lane_lock);
}

void gc_safepoint_poll(void) {
    gc_lane_record* record = gc_tls_lane;

    if (record == NULL) {
        return;
    }
    if (atomic_load_explicit(&record->stop, memory_order_acquire) != 0) {
        gc_lane_park(record);
    }
}

void willrt_gc_safepoint(enum willrt_gc_safepoint_kind kind) {
    (void)kind;
    gc_safepoint_poll();
}

void willrt_gc_native_enter(void) {
    gc_lane_record* record = gc_tls_lane;

    if (record == NULL) {
        return;
    }
    gc_safepoint_poll();
    atomic_fetch_add_explicit(&record->native_depth, 1, memory_order_acq_rel);
    atomic_store_explicit(&record->state, GC_LANE_NATIVE, memory_order_release);
}

void willrt_gc_native_leave(void) {
    gc_lane_record* record = gc_tls_lane;
    int32_t depth;

    if (record == NULL) {
        return;
    }
    depth = atomic_fetch_sub_explicit(&record->native_depth, 1,
                                      memory_order_acq_rel) -
            1;
    if (depth == 0) {
        atomic_store_explicit(&record->state, GC_LANE_RUNNING,
                              memory_order_release);
    }
}

int gc_lane_stop(int32_t lane) {
    gc_lane_record* record;
    int result = 0;

    gc_lane_init_once();
    willrt_os_mutex_lock(&gc_lane_lock);
    record = gc_lane_find(lane);
    if (record == NULL) {
        willrt_os_mutex_unlock(&gc_lane_lock);
        return 0;
    }
    if (atomic_load_explicit(&record->state, memory_order_acquire) ==
        GC_LANE_NATIVE) {
        willrt_os_mutex_unlock(&gc_lane_lock);
        return 0;
    }
    atomic_store_explicit(&record->stop, 1, memory_order_release);
    while (atomic_load_explicit(&record->ack, memory_order_acquire) == 0 &&
           atomic_load_explicit(&record->state, memory_order_acquire) !=
               GC_LANE_NATIVE &&
           atomic_load_explicit(&record->state, memory_order_acquire) !=
               GC_LANE_FREE) {
        willrt_os_cond_wait(&record->cond, &gc_lane_lock);
    }
    if (atomic_load_explicit(&record->state, memory_order_acquire) ==
        GC_LANE_PARKED) {
        result = 1;
    } else {
        atomic_store_explicit(&record->stop, 0, memory_order_release);
    }
    willrt_os_mutex_unlock(&gc_lane_lock);
    return result;
}

void gc_lane_scan(int32_t lane, willrt_gc_root_visit visit, void* context) {
    gc_lane_record* record;
    willrt_gc_frame* frame;

    willrt_os_mutex_lock(&gc_lane_lock);
    record = gc_lane_find(lane);
    frame = record == NULL ? NULL : record->frame;
    willrt_os_mutex_unlock(&gc_lane_lock);
    while (frame != NULL) {
        const willrt_gc_stack_map_data* map = frame->map;
        size_t i;

        if (map != NULL) {
            for (i = 0; i < map->slot_count; i++) {
                const willrt_gc_stack_slot* slot = &map->slots[i];

                if ((slot->flags & WILLRT_GC_SLOT_REFERENCE) != 0) {
                    void** address = (void**)((uint8_t*)frame->base +
                                              slot->slot);
                    visit(context, address);
                }
            }
        }
        frame = frame->parent;
    }
}

void gc_lanes_scan(willrt_gc_root_visit visit, void* context) {
    size_t i;

    gc_lane_init_once();
    for (i = 0; i < gc_lane_capacity; i++) {
        gc_lane_record* record;
        int32_t lane;

        willrt_os_mutex_lock(&gc_lane_lock);
        record = gc_lanes[i];
        if (record == NULL ||
            atomic_load_explicit(&record->state, memory_order_acquire) ==
                GC_LANE_FREE) {
            willrt_os_mutex_unlock(&gc_lane_lock);
            continue;
        }
        lane = record->lane;
        willrt_os_mutex_unlock(&gc_lane_lock);
        if (gc_lane_stop(lane)) {
            gc_lane_scan(lane, visit, context);
            gc_lane_resume(lane);
        }
    }
}

void gc_lane_resume(int32_t lane) {
    gc_lane_record* record;

    willrt_os_mutex_lock(&gc_lane_lock);
    record = gc_lane_find(lane);
    if (record != NULL &&
        atomic_load_explicit(&record->state, memory_order_acquire) ==
            GC_LANE_PARKED) {
        atomic_store_explicit(&record->stop, 0, memory_order_release);
        willrt_os_cond_signal(&record->cond);
    }
    willrt_os_mutex_unlock(&gc_lane_lock);
}

int32_t gc_lane_current(void) {
    return gc_tls_lane == NULL ? -1 : gc_tls_lane->lane;
}

int gc_lane_count(void) {
    return (int)gc_registered_lanes;
}

void gc_safepoint_init(void) {
    gc_lane_init_once();
}

void gc_safepoint_shutdown(void) {
    gc_lane_shutdown();
}
