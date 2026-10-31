/* Allocation pacer. The pacer keeps a debt in bytes and asks the GC worker for
   a young cycle before the nursery grows large; a full cycle is requested only
   when the retained debt is high. The worker owns the actual pace. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define GC_PACER_YOUNG_BYTES (UINT64_C(8) * 1024u * 1024u)
#define GC_PACER_FULL_BYTES (UINT64_C(64) * 1024u * 1024u)

static _Atomic size_t gc_pacer_debt;
static _Atomic uint64_t gc_memory_limit;
static atomic_int gc_pacer_state;

static uint64_t gc_parse_limit(const char* text) {
    uint64_t value;
    char* end;

    if (text == NULL || text[0] == '\0') {
        return 0;
    }
    value = strtoull(text, &end, 10);
    if (end == text) {
        return 0;
    }
    while (*end == ' ' || *end == '\t') {
        end++;
    }
    if (strcmp(end, "KiB") == 0 || strcmp(end, "K") == 0 ||
        strcmp(end, "k") == 0) {
        value *= 1024u;
    } else if (strcmp(end, "MiB") == 0 || strcmp(end, "M") == 0 ||
               strcmp(end, "m") == 0) {
        value *= 1024u * 1024u;
    } else if (strcmp(end, "GiB") == 0 || strcmp(end, "G") == 0 ||
               strcmp(end, "g") == 0) {
        value *= 1024u * 1024u * 1024u;
    }
    return value;
}

void gc_pacer_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong_explicit(&gc_pacer_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
        atomic_store_explicit(&gc_memory_limit,
                              gc_parse_limit(getenv("WILL_GC_MEMLIMIT")),
                              memory_order_release);
        atomic_store_explicit(&gc_pacer_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

void gc_pacer_on_alloc(size_t bytes) {
    if (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        return;
    }
    atomic_fetch_add_explicit(&gc_pacer_debt, bytes, memory_order_relaxed);
}

/* A cycle may only start at a safepoint. An allocation returns an object
   before the caller has rooted it; requesting a cycle from the allocation slow
   path lets the collector select the still-unrooted object and reclaim or move
   it. The pacer accumulates debt during allocation and asks for the cycle at
   the next published root set. */
void gc_pacer_poll(void) {
    size_t debt;

    if (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2 ||
        !gc_cycle_idle()) {
        return;
    }
    {
        uint64_t limit = atomic_load_explicit(&gc_memory_limit,
                                              memory_order_acquire);

        if (limit != 0 && (uint64_t)gc_heap_used_bytes() >= limit) {
            atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
            gc_cycle_request_full();
            return;
        }
    }
    debt = atomic_load_explicit(&gc_pacer_debt, memory_order_acquire);
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

uint64_t willrt_gc_set_memory_limit(uint64_t bytes) {
    if (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        gc_pacer_init();
    }
    return atomic_exchange_explicit(&gc_memory_limit, bytes,
                                    memory_order_acq_rel);
}

uint64_t willrt_gc_memory_limit(void) {
    if (atomic_load_explicit(&gc_pacer_state, memory_order_acquire) != 2) {
        gc_pacer_init();
    }
    return atomic_load_explicit(&gc_memory_limit, memory_order_acquire);
}

void gc_pacer_shutdown(void) {
    atomic_store_explicit(&gc_pacer_debt, 0, memory_order_release);
    atomic_store_explicit(&gc_memory_limit, 0, memory_order_release);
    atomic_store_explicit(&gc_pacer_state, 0, memory_order_release);
}
