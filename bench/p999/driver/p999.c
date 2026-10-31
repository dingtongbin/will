/* p999 probe for the real concurrent collector.

   One thread keeps requesting full cycles; the mutator allocates through the
   TLAB, keeps a bounded rooted ring, and samples the safepoint and allocation
   fast paths. This is a measurement driver, not a release test: it is built by
   the p999-tagged Go test and never enters the normal gate. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define SAMPLES 100000
#define ROOTS 4096

static void* roots[ROOTS];
static int64_t alloc_samples[SAMPLES];
static int64_t pause_samples[SAMPLES];
static atomic_int stop_cycles;

static int compare_i64(const void* a, const void* b) {
    int64_t x = *(const int64_t*)a;
    int64_t y = *(const int64_t*)b;
    return x < y ? -1 : x > y ? 1 : 0;
}

static void root_scan(void* context, willrt_gc_root_visit visit,
                      void* visit_context) {
    size_t i;

    (void)context;
    for (i = 0; i < ROOTS; i++) {
        visit(visit_context, &roots[i]);
    }
}

static void cycle_worker(void* argument) {
    (void)argument;
    while (!atomic_load_explicit(&stop_cycles, memory_order_acquire)) {
        gc_cycle_request_full();
        while (!gc_cycle_idle() &&
               !atomic_load_explicit(&stop_cycles, memory_order_acquire)) {
            willrt_os_thread_yield();
        }
    }
}

static int64_t percentile(int64_t* values, size_t n, int per_mille) {
    size_t index = (n * (size_t)per_mille) / 1000u;

    if (index >= n) {
        index = n - 1u;
    }
    return values[index];
}

int main(void) {
    static const uint32_t refs[] = {0};
    static const willrt_gc_type type = {
        1, 32, 8, WILLRT_GC_TYPE_REFERENCES, 1, refs, 0, NULL
    };
    willrt_os_thread cycle_thread;
    int i;

    willrt_gc_lane_enter(0);
    if (willrt_gc_register_type(&type) != 0) {
        return 1;
    }
    willrt_gc_root_register(root_scan, NULL);
    atomic_store(&stop_cycles, 0);
    if (willrt_os_thread_start(&cycle_thread, cycle_worker, NULL) != 0) {
        return 1;
    }
    for (i = 0; i < SAMPLES; i++) {
        int64_t start;
        int64_t end;

        start = willrt_os_now_ns();
        roots[i % ROOTS] = willrt_gc_alloc(32, &type);
        end = willrt_os_now_ns();
        alloc_samples[i] = end - start;
        start = willrt_os_now_ns();
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_ALLOC);
        end = willrt_os_now_ns();
        pause_samples[i] = end - start;
    }
    atomic_store(&stop_cycles, 1);
    willrt_os_thread_join(&cycle_thread);
    while (!gc_cycle_idle()) {
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_BACKEDGE);
    }
    qsort(alloc_samples, SAMPLES, sizeof(alloc_samples[0]), compare_i64);
    qsort(pause_samples, SAMPLES, sizeof(pause_samples[0]), compare_i64);
    printf("alloc_ns p50=%lld p99=%lld p999=%lld\n",
           (long long)percentile(alloc_samples, SAMPLES, 500),
           (long long)percentile(alloc_samples, SAMPLES, 990),
           (long long)percentile(alloc_samples, SAMPLES, 999));
    printf("safepoint_ns p50=%lld p99=%lld p999=%lld\n",
           (long long)percentile(pause_samples, SAMPLES, 500),
           (long long)percentile(pause_samples, SAMPLES, 990),
           (long long)percentile(pause_samples, SAMPLES, 999));
    willrt_gc_root_unregister(root_scan, NULL);
    willrt_gc_shutdown();
    return 0;
}
