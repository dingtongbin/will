// main.c is the C baseline of the checks/int scenario. It runs the same
// integer chain twice: once with the overflow checks the compiler can
// prove, and once with the checks written out.
//
// The will side has no switch to turn the checks off, so its numbers always
// carry the checks of the language. The comparison shows what the checks
// cost.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// workload is the number of iterations of one measurement.
static const int32_t workload = 5000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// work runs the chain with the checks written out. The overflow of the
// multiply is reported by the builtin, so the chain stops there.
static int64_t work_checked(int32_t n, int32_t seed) {
    int64_t acc = 0;
    for (int32_t i = 0; i < n; i++) {
        int64_t x = ((int64_t)i * 3 + 1) ^ (int64_t)seed;
        int64_t product = 0;
        if (__builtin_mul_overflow(x, 3, &product)) {
            return -1;
        }
        int64_t sum = 0;
        if (__builtin_add_overflow(product, 1, &sum)) {
            return -1;
        }
        acc += sum;
    }
    return acc;
}

// work runs the same chain without the checks.
static int64_t work_plain(int32_t n, int32_t seed) {
    int64_t acc = 0;
    for (int32_t i = 0; i < n; i++) {
        int64_t x = ((int64_t)i * 3 + 1) ^ (int64_t)seed;
        acc += x * 3 + 1;
    }
    return acc;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    int64_t warm = work_checked(1000, 0);
    (void)warm;

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        checksum += work_checked(workload, r);
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        bench_sink((uint64_t)checksum);
    }

    uint64_t* plainSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    int64_t plainChecksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        plainChecksum += work_plain(workload, r);
        uint64_t stop = bench_now_ns();
        plainSamples[r] = stop - start;
        bench_sink((uint64_t)plainChecksum);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    bench_sort_u64(plainSamples, (size_t)repetitions);

    bench_metric("checked_p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("checked_max", samples[repetitions - 1], "ns");
    bench_metric("plain_p50", bench_percentile(plainSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("plain_max", plainSamples[repetitions - 1], "ns");
    bench_metric("workload", (uint64_t)workload, "iterations");
    bench_check("checked", (uint64_t)checksum);
    bench_check("plain", (uint64_t)plainChecksum);
    bench_checksum((uint64_t)checksum);
    free(samples);
    free(plainSamples);
    return 0;
}
