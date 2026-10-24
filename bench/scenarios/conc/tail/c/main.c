// main.c is the C baseline of the conc/tail scenario. It measures the time
// of one send and receive round trip, the way the will side does, and it
// reports the percentiles of those samples.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// capacity is the number of slots of the ring buffer. A file-scope array needs
// an integer constant expression, so this is an enum rather than a const.
enum { capacity = 64 };

// count is the number of round trips the workload measures.
static const int32_t count = 100000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// ring is the hand written queue.
typedef struct {
    // The queue is volatile so the optimizer cannot replace the measured
    // sends and receives with a closed form.
    volatile int64_t slots[capacity];
    volatile int32_t head;
    volatile int32_t tail;
} Ring;

static void ring_init(Ring* ring) {
    ring->head = 0;
    ring->tail = 0;
}

static void ring_send(Ring* ring, int64_t value) {
    ring->slots[ring->head] = value;
    ring->head = (ring->head + 1) % capacity;
}

static int64_t ring_recv(Ring* ring) {
    int64_t value = ring->slots[ring->tail];
    ring->tail = (ring->tail + 1) % capacity;
    return value;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)count);
    uint64_t* rounds = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL || rounds == NULL) {
        return 1;
    }
    Ring ring;
    ring_init(&ring);
    // The warm up pays for the first touch of the pages and of the caches.
    for (int32_t i = 0; i < 1000; i++) {
        ring_send(&ring, (int64_t)i);
        (void)ring_recv(&ring);
    }

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t roundStart = bench_now_ns();
        for (int32_t i = 0; i < count; i++) {
            uint64_t start = bench_now_ns();
            ring_send(&ring, (int64_t)i);
            checksum += ring_recv(&ring);
            uint64_t stop = bench_now_ns();
            samples[i] = stop - start;
        }
        rounds[r] = bench_now_ns() - roundStart;
        bench_sink((uint64_t)checksum);
    }

    bench_sort_u64(samples, (size_t)count);
    bench_sort_u64(rounds, (size_t)repetitions);

    bench_metric("p50", bench_percentile(samples, (size_t)count, 50, 100), "ns");
    bench_metric("p95", bench_percentile(samples, (size_t)count, 95, 100), "ns");
    bench_metric("p99", bench_percentile(samples, (size_t)count, 99, 100), "ns");
    bench_metric("p999", bench_percentile(samples, (size_t)count, 999, 1000), "ns");
    bench_metric("max", samples[count - 1], "ns");
    bench_metric("round_p50", bench_percentile(rounds, (size_t)repetitions, 50, 100), "ns");
    bench_metric("round_max", rounds[repetitions - 1], "ns");
    bench_metric("samples", (uint64_t)count, "samples");
    bench_checksum((uint64_t)checksum);
    free(samples);
    free(rounds);
    return 0;
}
