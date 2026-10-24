// main.c is the C baseline of the conc/chan scenario. It moves the same
// number of messages through a hand written ring buffer, the way the will
// side moves them through a channel.
//
// The exchange is not contended: one thread sends and one thread receives on
// the same buffer. The acceptance line of the method volume asks the
// channel to be at least as fast as a hand written queue in this case.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// capacity is the number of slots of the ring buffer. A file-scope array needs
// an integer constant expression, so this is an enum rather than a const.
enum { capacity = 64 };

// count is the number of messages the workload moves.
static const int32_t count = 1000000;

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
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
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
        uint64_t start = bench_now_ns();
        int64_t total = 0;
        for (int32_t i = 0; i < count; i++) {
            ring_send(&ring, (int64_t)i);
            total += ring_recv(&ring);
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
    bench_metric("messages", (uint64_t)count, "messages");
    bench_metric("capacity", (uint64_t)capacity, "slots");
    bench_checksum((uint64_t)checksum);
    free(samples);
    return 0;
}
