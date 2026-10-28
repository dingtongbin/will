// main.c is the C baseline of the mem/rc scenario. It counts references
// with an atomic counter, the way the runtime of the language does, and it
// touches the object the same number of times the will side does.
#include "bench.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>

// count is the number of reference count changes of the workload.
static const int32_t count = 1000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// Counter is the object the workload shares.
typedef struct {
    _Atomic int64_t refs;
    int64_t value;
} Counter;

static void counter_retain(Counter* counter) {
    atomic_fetch_add(&counter->refs, 1);
}

static void counter_release(Counter* counter) {
    atomic_fetch_sub(&counter->refs, 1);
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    Counter* cell = (Counter*)malloc(sizeof(Counter));
    if (cell == NULL) {
        return 1;
    }
    atomic_init(&cell->refs, 1);
    cell->value = 0;
    // The warm up pays for the first touch of the pages and of the caches.
    counter_retain(cell);
    counter_release(cell);

    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < count; i++) {
            // The copy retains the object, the end of the loop releases it.
            counter_retain(cell);
            cell->value += 1;
            counter_release(cell);
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        bench_sink((uint64_t)cell->value);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    uint64_t p50 = bench_percentile(samples, (size_t)repetitions, 50, 100);
    uint64_t p95 = bench_percentile(samples, (size_t)repetitions, 95, 100);
    uint64_t p99 = bench_percentile(samples, (size_t)repetitions, 99, 100);
    uint64_t worst = samples[repetitions - 1];

    bench_metric("p50", p50, "ns");
    bench_metric("p95", p95, "ns");
    bench_metric("p99", p99, "ns");
    bench_metric("max", worst, "ns");
    bench_metric("operations", (uint64_t)count, "pairs");
    bench_checksum((uint64_t)cell->value);
    free(samples);
    free(cell);
    return 0;
}
