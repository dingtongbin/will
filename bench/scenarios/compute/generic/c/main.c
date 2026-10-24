// main.c is the C baseline of the compute/generic scenario. It calls the
// hand written monomorphic function the will side writes as a generic one,
// with the same values and the same checksum.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// workload is the number of iterations of one measurement.
static const int32_t workload = 2000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// max_i64 is the monomorphic function the will side writes as a generic one.
static int64_t max_i64(int64_t a, int64_t b) {
    if (a > b) {
        return a;
    }
    return b;
}

// work runs the measured loop and returns its checksum.
//
// The seed takes part in the work, so two calls with different seeds give
// different results. Without it the compiler may run the loop once and
// reuse the result, and the measurement would time nothing.
static int64_t work(int32_t n, int32_t seed) {
    int64_t acc = 0;
    for (int32_t i = 0; i < n; i++) {
        acc += max_i64((int64_t)i ^ (int64_t)seed, (int64_t)i + 1);
    }
    return acc;
}

// baseline runs an empty loop, so the cost of the loop and of the counter is
// measured apart from the calls.
static int64_t baseline(int32_t n) {
    int64_t acc = 0;
    for (int32_t i = 0; i < n; i++) {
        acc ^= (int64_t)i;
    }
    return acc;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    int64_t warm = work(1000, 0);
    (void)warm;

    uint64_t base = 0;
    {
        uint64_t start = bench_now_ns();
        int64_t acc = baseline(workload);
        uint64_t stop = bench_now_ns();
        base = stop - start;
        (void)acc;
    }

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        checksum += work(workload, r);
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        bench_sink((uint64_t)checksum);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    uint64_t p50 = bench_percentile(samples, (size_t)repetitions, 50, 100);
    uint64_t p95 = bench_percentile(samples, (size_t)repetitions, 95, 100);
    uint64_t p99 = bench_percentile(samples, (size_t)repetitions, 99, 100);
    uint64_t worst = samples[repetitions - 1];

    bench_metric("baseline", base, "ns");
    bench_metric("p50", p50, "ns");
    bench_metric("p95", p95, "ns");
    bench_metric("p99", p99, "ns");
    bench_metric("max", worst, "ns");
    bench_metric("p50_net", p50 > base ? p50 - base : 0, "ns");
    bench_metric("workload", (uint64_t)workload, "iterations");
    bench_checksum((uint64_t)checksum);
    free(samples);
    return 0;
}
