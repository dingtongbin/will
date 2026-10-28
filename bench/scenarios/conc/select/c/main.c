// main.c is the C baseline of the conc/select scenario. It waits on many
// queues and takes the first one that has something, the way the will side
// waits on many channels with a select.
//
// A select takes the first arm that is ready, in the order the arms are
// written, so the C baseline walks its queues in the same order.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// queues is the number of queues the select waits on.
static const int32_t queues = 4;

// count is the number of times the workload waits.
static const int32_t count = 500000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// queue is a hand written queue with one slot.
typedef struct {
    // A volatile queue prevents the optimizer from folding the whole polling
    // loop into a closed form when the checksum is only read after the loop.
    volatile int64_t value;
    volatile int32_t filled;
} Queue;

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    Queue* ring = (Queue*)malloc(sizeof(Queue) * (size_t)queues);
    if (samples == NULL || ring == NULL) {
        return 1;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    for (int32_t i = 0; i < 1000; i++) {
        ring[0].value = (int64_t)i;
        ring[0].filled = 1;
        for (int32_t q = 0; q < queues; q++) {
            if (ring[q].filled) {
                ring[q].filled = 0;
                break;
            }
        }
    }

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        int64_t total = 0;
        for (int32_t i = 0; i < count; i++) {
            ring[0].value = (int64_t)i;
            ring[0].filled = 1;
            for (int32_t q = 0; q < queues; q++) {
                if (ring[q].filled) {
                    total += ring[q].value;
                    ring[q].filled = 0;
                    break;
                }
            }
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        checksum += total;
        bench_sink((uint64_t)checksum);
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
    bench_metric("waits", (uint64_t)count, "waits");
    bench_metric("queues", (uint64_t)queues, "queues");
    bench_checksum((uint64_t)checksum);
    free(samples);
    free(ring);
    return 0;
}
