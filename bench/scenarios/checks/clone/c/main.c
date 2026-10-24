// main.c is the C baseline of the checks/clone scenario. It copies a value
// that is big enough to matter, once by value and once through a pointer.
//
// The will side waits for the clone decision of the memory model, so only
// the C numbers exist today. They are the reference the decision will be
// measured against.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// workload is the number of copies of one measurement.
static const int32_t workload = 5000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// Big is the value the scenario copies. It is 64 bytes wide, so a copy does
// not fit into a register.
typedef struct {
    int64_t words[8];
} Big;

// copy_by_value copies the value.
static int64_t copy_by_value(Big value) {
    Big copy = value;
    return copy.words[0] + copy.words[7];
}

// copy_by_pointer copies the value through a pointer.
static int64_t copy_by_pointer(const Big* value) {
    Big copy = *value;
    return copy.words[0] + copy.words[7];
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    Big value;
    for (int32_t i = 0; i < 8; i++) {
        value.words[i] = i;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    int64_t warm = copy_by_value(value) + copy_by_pointer(&value);
    (void)warm;

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            checksum += copy_by_value(value);
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        bench_sink((uint64_t)checksum);
    }

    uint64_t* pointerSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    int64_t pointerChecksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            pointerChecksum += copy_by_pointer(&value);
        }
        uint64_t stop = bench_now_ns();
        pointerSamples[r] = stop - start;
        bench_sink((uint64_t)pointerChecksum);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    bench_sort_u64(pointerSamples, (size_t)repetitions);

    bench_metric("by_value_p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("by_pointer_p50", bench_percentile(pointerSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("copies", (uint64_t)workload, "copies");
    bench_check("by_value", (uint64_t)checksum);
    bench_check("by_pointer", (uint64_t)pointerChecksum);
    bench_checksum((uint64_t)(checksum + pointerChecksum));
    free(samples);
    free(pointerSamples);
    return 0;
}
