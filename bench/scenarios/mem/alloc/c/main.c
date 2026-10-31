// main.c is the C baseline of the mem/alloc scenario. It allocates and
// releases one million small objects, the same way the will side does with
// its GC cells.
//
// The allocators are not the same: the C side uses the system allocator, the
// will side the allocator of the runtime. The numbers therefore report the
// cost of the two allocators, not the cost of the language.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// count is the number of objects the workload allocates.
static const int32_t count = 1000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// Cell is the object the workload allocates.
typedef struct {
    int64_t value;
} Cell;

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    Cell* warm = (Cell*)malloc(sizeof(Cell));
    free(warm);

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        int64_t total = 0;
        for (int32_t i = 0; i < count; i++) {
            Cell* cell = (Cell*)malloc(sizeof(Cell));
            cell->value = (int64_t)i;
            // The read is volatile, so the allocator cannot drop the block
            // and turn the loop into nothing.
            total += *(volatile int64_t*)&cell->value;
            free(cell);
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
    bench_metric("allocations", (uint64_t)count, "objects");
    bench_checksum((uint64_t)checksum);
    free(samples);
    return 0;
}
