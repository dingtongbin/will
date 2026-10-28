/* Allocation pacer. The pacer keeps a debt in bytes and asks the GC worker for
   a young cycle before the nursery grows large; a full cycle is requested only
   when the retained debt is high. The worker owns the actual pace. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#define GC_PACER_YOUNG_BYTES (UINT64_C(8) * 1024u * 1024u)
#define GC_PACER_FULL_BYTES (UINT64_C(64) * 1024u * 1024u)

static _Atomic size_t gc_pacer_debt;
static atomic_int gc_pacer_state;

void gc_pacer_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong_explicit(&gc_pacer_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
        atomic_store_explicit(&gc_pacer_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

void gc_pacer_on_alloc(size_t bytes) {
    size_t debt;

    if (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        return;
    }
    debt = atomic_fetch_add_explicit(&gc_pacer_debt, bytes,
                                     memory_order_relaxed) +
           bytes;
    if (!gc_cycle_idle()) {
        return;
    }
    if (debt >= GC_PACER_FULL_BYTES) {
        atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
        gc_cycle_request_full();
    } else if (debt >= GC_PACER_YOUNG_BYTES) {
        atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
        gc_cycle_kick();
    }
}

void gc_pacer_cycle_started(void) {
    atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
}

void gc_pacer_shutdown(void) {
    atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
    atomic_store_explicit(&gc_pacer_state, 0, memory_order_release);
}
