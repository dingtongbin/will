/* GC coordinator. One dedicated worker runs young and full cycles; mutators
   stop only at per-lane safepoints while exact roots are scanned. Marking and
   evacuation run while other lanes continue. No global compaction pause. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static willrt_os_thread gc_thread;
static willrt_os_mutex gc_cycle_lock;
static willrt_os_cond gc_cycle_cond;
static atomic_int gc_cycle_started;
static int gc_cycle_stop;
static int gc_cycle_requested;
static int gc_cycle_full;
static int gc_cycle_running;
static uint32_t gc_next_color = WILLRT_GC_COLOR_MARKED0;

static gc_region** gc_sources;
static size_t gc_source_count;
static size_t gc_source_capacity;

static void gc_source_reset(void) {
    size_t i;

    for (i = 0; i < gc_source_count; i++) {
        gc_sources[i]->flags &= ~GC_REGION_EVACUATING;
    }
    gc_source_count = 0;
}

static int gc_source_add(gc_region* region) {
    if (gc_source_count == gc_source_capacity) {
        size_t capacity = gc_source_capacity == 0 ? 32u
                                                  : gc_source_capacity * 2u;
        gc_region** next = (gc_region**)willrt_alloc(
            capacity * sizeof(*next));

        if (next == NULL) {
            return -1;
        }
        if (gc_sources != NULL) {
            memcpy(next, gc_sources, gc_source_count * sizeof(*next));
            willrt_free(gc_sources);
        }
        gc_sources = next;
        gc_source_capacity = capacity;
    }
    region->flags |= GC_REGION_EVACUATING;
    gc_sources[gc_source_count++] = region;
    return 0;
}

static void gc_mark_visit(void* context, void** slot) {
    (void)context;
    gc_mark_slot(slot);
}

static void gc_count_object(void* context, void* object) {
    gc_region* region = (gc_region*)context;
    size_t start;
    size_t end;

    if (!gc_object_marked(object)) {
        return;
    }
    if (gc_region_object_bounds(region,
                                (size_t)((uint8_t*)object - region->base) -
                                    WILLRT_GC_OBJECT_PAYLOAD_OFFSET,
                                &start, &end)) {
        gc_region_add_live(region, end - start);
    }
}

static void gc_count_live(gc_region* region) {
    atomic_store_explicit(&region->live_bytes, 0, memory_order_release);
    gc_region_scan_objects(region, gc_count_object, region);
}

static void gc_scan_mark_roots(void) {
    gc_roots_scan(gc_mark_visit, NULL);
    gc_handles_scan(gc_mark_visit, NULL);
    gc_lanes_scan(gc_mark_visit, NULL);
    gc_card_scan(gc_mark_visit, NULL);
    gc_satb_scan(gc_mark_visit, NULL);
    gc_mark_drain();
}

static void gc_scan_evac_roots(void) {
    gc_roots_scan(gc_evac_slot, NULL);
    gc_handles_scan(gc_evac_slot, NULL);
    gc_lanes_scan(gc_evac_slot, NULL);
    gc_card_scan(gc_evac_slot, NULL);
    gc_satb_scan(gc_evac_slot, NULL);
    gc_copy_drain();
}

static void gc_select_young(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];

        if (region == NULL || region->kind != GC_REGION_YOUNG ||
            atomic_load_explicit(&region->pin_count, memory_order_acquire) !=
                0) {
            continue;
        }
        (void)gc_source_add(region);
    }
}

static void gc_select_old(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];
        size_t live;

        if (region == NULL || region->kind != GC_REGION_OLD ||
            atomic_load_explicit(&region->pin_count, memory_order_acquire) !=
                0) {
            continue;
        }
        gc_count_live(region);
        live = atomic_load_explicit(&region->live_bytes, memory_order_acquire);
        if (live * 4u < region->size * 3u) {
            (void)gc_source_add(region);
        }
    }
}

static void gc_free_sources(void) {
    size_t i;

    for (i = 0; i < gc_source_count; i++) {
        gc_region* region = gc_sources[i];

        region->flags &= ~GC_REGION_EVACUATING;
        if (atomic_load_explicit(&region->pin_count, memory_order_acquire) ==
            0) {
            gc_region_free(region);
        }
    }
    gc_source_count = 0;
}

static void gc_run_young(void) {
    uint32_t color = WILLRT_GC_COLOR_REMAPPED;

    atomic_store_explicit(&gc_good_color, color, memory_order_release);
    gc_satb_scan(gc_evac_slot, NULL);
    gc_copy_reset();
    gc_select_young();
    if (gc_source_count == 0) {
        return;
    }
    gc_heap_epoch_bump();
    atomic_store_explicit(&gc_evacuating, 1, memory_order_release);
    gc_scan_evac_roots();
    gc_scan_evac_roots();
    gc_remap_heap();
    gc_copy_drain();
    gc_scan_evac_roots();
    gc_remap_heap();
    gc_copy_drain();
    gc_free_sources();
    atomic_store_explicit(&gc_evacuating, 0, memory_order_release);
}

static void gc_run_full(void) {
    uint32_t color = gc_next_color;

    gc_next_color = color == WILLRT_GC_COLOR_MARKED0
                        ? WILLRT_GC_COLOR_MARKED1
                        : WILLRT_GC_COLOR_MARKED0;
    atomic_store_explicit(&gc_good_color, color, memory_order_release);
    gc_mark_begin(color);
    gc_scan_mark_roots();
    gc_scan_mark_roots();
    gc_weak_clear();
    gc_reclaim_nonmoving();
    gc_select_young();
    gc_select_old();
    if (gc_source_count == 0) {
        gc_mark_end();
        return;
    }
    gc_copy_reset();
    gc_heap_epoch_bump();
    atomic_store_explicit(&gc_evacuating, 1, memory_order_release);
    gc_scan_evac_roots();
    gc_scan_evac_roots();
    gc_remap_heap();
    gc_copy_drain();
    gc_scan_evac_roots();
    gc_remap_heap();
    gc_copy_drain();
    gc_free_sources();
    atomic_store_explicit(&gc_evacuating, 0, memory_order_release);
    gc_mark_end();
}

static void gc_run_cycle(int full) {
    gc_pacer_cycle_started();
    if (full) {
        gc_run_full();
    } else {
        gc_run_young();
    }
}

static void gc_cycle_thread(void* argument) {
    (void)argument;
    for (;;) {
        int full;

        willrt_os_mutex_lock(&gc_cycle_lock);
        while (!gc_cycle_stop && !gc_cycle_requested) {
            willrt_os_cond_wait(&gc_cycle_cond, &gc_cycle_lock);
        }
        if (gc_cycle_stop) {
            willrt_os_mutex_unlock(&gc_cycle_lock);
            break;
        }
        full = gc_cycle_full;
        gc_cycle_full = 0;
        gc_cycle_requested = 0;
        gc_cycle_running = 1;
        willrt_os_mutex_unlock(&gc_cycle_lock);
        gc_run_cycle(full);
        willrt_os_mutex_lock(&gc_cycle_lock);
        gc_cycle_running = 0;
        willrt_os_cond_broadcast(&gc_cycle_cond);
        willrt_os_mutex_unlock(&gc_cycle_lock);
    }
}

void gc_cycle_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_cycle_started, memory_order_acquire) == 2) {
        return;
    }
    if (!atomic_compare_exchange_strong_explicit(
            &gc_cycle_started, &expected, 1, memory_order_acq_rel,
            memory_order_acquire)) {
        while (atomic_load_explicit(&gc_cycle_started,
                                    memory_order_acquire) != 2) {
            willrt_os_thread_yield();
        }
        return;
    }
    willrt_os_mutex_init(&gc_cycle_lock);
    willrt_os_cond_init(&gc_cycle_cond);
    gc_cycle_stop = 0;
    gc_cycle_requested = 0;
    gc_cycle_full = 0;
    gc_cycle_running = 0;
    if (willrt_os_thread_start(&gc_thread, gc_cycle_thread, NULL) != 0) {
        abort();
    }
    atomic_store_explicit(&gc_cycle_started, 2, memory_order_release);
}

void gc_cycle_shutdown(void) {
    if (atomic_load_explicit(&gc_cycle_started, memory_order_acquire) != 2) {
        return;
    }
    willrt_os_mutex_lock(&gc_cycle_lock);
    gc_cycle_stop = 1;
    willrt_os_cond_broadcast(&gc_cycle_cond);
    willrt_os_mutex_unlock(&gc_cycle_lock);
    willrt_os_thread_join(&gc_thread);
    gc_source_reset();
    willrt_free(gc_sources);
    gc_sources = NULL;
    gc_source_capacity = 0;
    willrt_os_cond_destroy(&gc_cycle_cond);
    willrt_os_mutex_destroy(&gc_cycle_lock);
    atomic_store_explicit(&gc_cycle_started, 0, memory_order_release);
}

void gc_cycle_kick(void) {
    gc_cycle_init();
    willrt_os_mutex_lock(&gc_cycle_lock);
    gc_cycle_requested = 1;
    willrt_os_cond_signal(&gc_cycle_cond);
    willrt_os_mutex_unlock(&gc_cycle_lock);
}

void gc_cycle_request_full(void) {
    gc_cycle_init();
    willrt_os_mutex_lock(&gc_cycle_lock);
    gc_cycle_requested = 1;
    gc_cycle_full = 1;
    willrt_os_cond_signal(&gc_cycle_cond);
    willrt_os_mutex_unlock(&gc_cycle_lock);
}

int gc_cycle_idle(void) {
    int idle;

    gc_cycle_init();
    willrt_os_mutex_lock(&gc_cycle_lock);
    idle = !gc_cycle_requested && !gc_cycle_running;
    willrt_os_mutex_unlock(&gc_cycle_lock);
    return idle;
}
