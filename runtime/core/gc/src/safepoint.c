/* Safepoint stack maps. Code registers its static maps once; a safepoint
   publishes the current frame and, while a mark is active, marks every
   reference slot before the mutator continues. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdlib.h>

#define WILLRT_GC_STACKMAP_MAX 1024u
#define WILLRT_GC_FRAMEMAP_MAX 1024u

static willrt_os_mutex gc_safepoint_lock;
static atomic_int gc_safepoint_state;
static const willrt_gc_stack_map_data*
    gc_stack_maps[WILLRT_GC_STACKMAP_MAX];
static const willrt_gc_frame_map_data*
    gc_frame_maps[WILLRT_GC_FRAMEMAP_MAX];
static size_t gc_stack_map_count;
static size_t gc_frame_map_count;

static void gc_safepoint_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_safepoint_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_safepoint_state, &expected,
                                                1, memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_safepoint_lock);
        atomic_store_explicit(&gc_safepoint_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_safepoint_state, memory_order_acquire) !=
           2) {
        willrt_os_thread_yield();
    }
}

void willrt_gc_stackmap(const willrt_gc_stack_map_data* map) {
    size_t i;

    if (map == NULL) {
        return;
    }
    gc_safepoint_init();
    willrt_os_mutex_lock(&gc_safepoint_lock);
    for (i = 0; i < gc_stack_map_count; i++) {
        if (gc_stack_maps[i] == map) {
            willrt_os_mutex_unlock(&gc_safepoint_lock);
            return;
        }
    }
    if (gc_stack_map_count == WILLRT_GC_STACKMAP_MAX) {
        willrt_os_mutex_unlock(&gc_safepoint_lock);
        abort();
    }
    gc_stack_maps[gc_stack_map_count++] = map;
    willrt_os_mutex_unlock(&gc_safepoint_lock);
}

void willrt_gc_frame_map(const willrt_gc_frame_map_data* map) {
    size_t i;

    if (map == NULL) {
        return;
    }
    gc_safepoint_init();
    willrt_os_mutex_lock(&gc_safepoint_lock);
    for (i = 0; i < gc_frame_map_count; i++) {
        if (gc_frame_maps[i] == map) {
            willrt_os_mutex_unlock(&gc_safepoint_lock);
            return;
        }
    }
    if (gc_frame_map_count == WILLRT_GC_FRAMEMAP_MAX) {
        willrt_os_mutex_unlock(&gc_safepoint_lock);
        abort();
    }
    gc_frame_maps[gc_frame_map_count++] = map;
    willrt_os_mutex_unlock(&gc_safepoint_lock);
}

void willrt_gc_safepoint(enum willrt_gc_safepoint_kind kind,
                         const willrt_gc_stack_map_data* map, void* frame) {
    (void)kind;
    if (!willrt_gc_marking_active()) {
        return;
    }
    willrt_gc_mark_stack(map, frame);
}
