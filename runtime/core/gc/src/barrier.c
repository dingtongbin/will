/* Read and write barriers. The fast path removes the color bits; the write
   barrier publishes the new reference and records the old one for SATB. The
   marker consumes the SATB buffer in its own module. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>

#define WILLRT_GC_SATB_CAP 1024u

static willrt_os_mutex gc_satb_lock;
static atomic_int gc_satb_state;
static void* gc_satb[WILLRT_GC_SATB_CAP];
static size_t gc_satb_count;
static size_t gc_satb_overflow;

static void gc_satb_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_satb_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_satb_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_satb_lock);
        atomic_store_explicit(&gc_satb_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_satb_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

void* willrt_gc_read_barrier(const void* ref) {
    return WILLRT_GC_UNTAG(ref);
}

void willrt_gc_write_barrier(void** slot, void* value) {
    void* old;

    if (slot == NULL) {
        return;
    }
    old = *slot;
    if (old != NULL && old != value) {
        gc_satb_init();
        willrt_os_mutex_lock(&gc_satb_lock);
        if (gc_satb_count < WILLRT_GC_SATB_CAP) {
            gc_satb[gc_satb_count++] = old;
        } else {
            gc_satb_overflow++;
        }
        willrt_os_mutex_unlock(&gc_satb_lock);
    }
    *slot = value;
}

size_t willrt_gc_test_satb_count(void) {
    return gc_satb_count;
}

size_t willrt_gc_test_satb_overflow(void) {
    return gc_satb_overflow;
}
