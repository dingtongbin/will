// main.c is the C baseline of the mem/steady scenario. The loop must not
// allocate: the array is filled before the measurement starts, and the
// program reports the growth of the peak resident set size, which has to
// stay at zero.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// count is the number of values the loop walks.
static const int32_t count = 1000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// seed is the seed of the input generator.
static const uint64_t seed = 0x2545F4914F6CDD1DULL;

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    int64_t* values = (int64_t*)malloc(sizeof(int64_t) * (size_t)count);
    if (samples == NULL || values == NULL) {
        return 1;
    }
    uint64_t state = seed;
    for (int32_t i = 0; i < count; i++) {
        values[i] = (int64_t)(bench_lcg_next(&state) >> 1);
    }
    // The warm up pays for the first touch of the pages and of the caches.
    int64_t warm = 0;
    for (int32_t i = 0; i < count; i++) {
        warm += values[i];
    }
    (void)warm;

    uint64_t rssBefore = bench_rss_bytes();
    uint64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        uint64_t total = 0;
        for (int32_t i = 0; i < count; i++) {
            total += (uint64_t)values[i] ^ (uint64_t)r;
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        checksum += total;
        bench_sink(checksum);
    }
    uint64_t rssAfter = bench_rss_bytes();

    bench_sort_u64(samples, (size_t)repetitions);
    uint64_t p50 = bench_percentile(samples, (size_t)repetitions, 50, 100);
    uint64_t p95 = bench_percentile(samples, (size_t)repetitions, 95, 100);
    uint64_t p99 = bench_percentile(samples, (size_t)repetitions, 99, 100);
    uint64_t worst = samples[repetitions - 1];

    bench_metric("p50", p50, "ns");
    bench_metric("p95", p95, "ns");
    bench_metric("p99", p99, "ns");
    bench_metric("max", worst, "ns");
    bench_metric("rss_before", rssBefore, "bytes");
    bench_metric("rss_after", rssAfter, "bytes");
    bench_metric("rss_delta", rssAfter > rssBefore ? rssAfter - rssBefore : 0, "bytes");
    bench_metric("values", (uint64_t)count, "values");
    bench_checksum(checksum);
    free(samples);
    free(values);
    return 0;
}
