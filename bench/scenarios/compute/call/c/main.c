// main.c is the C baseline of the compute/call scenario. It runs the same
// four kinds of call, with the same values and the same checksum, as the
// will side: a direct call, a call into another translation unit, a method
// call on a struct and a call through an interface, which C spells as a
// function pointer.
#include "bench.h"
#include "lib.h"

#include <stdint.h>
#include <stdlib.h>

// workload is the number of iterations of one measurement.
static const int32_t workload = 2000000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// Counter is the struct the method call works on.
typedef struct {
    int64_t total;
} Counter;

// counter_add is the method of Counter.
static void counter_add(Counter* counter, int64_t value) {
    counter->total += value;
}

// Shape is the interface of the scenario. C spells an interface as a
// function pointer plus the value it works on.
typedef int64_t (*AreaFn)(const void* self, int64_t scale);

typedef struct {
    AreaFn area;
    const void* self;
} Shape;

typedef struct {
    int64_t side;
} Square;

static int64_t square_area(const void* self, int64_t scale) {
    const Square* square = (const Square*)self;
    return square->side * square->side + scale;
}

// direct is the direct call of the scenario.
static int64_t direct(int64_t value) {
    return value * 3 + 1;
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
    uint64_t* baseSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL || baseSamples == NULL) {
        return 1;
    }
    Counter counter = {0};
    Square square = {3};
    // The pointer is volatile, so the call cannot be inlined and the loop
    // cannot be folded into a formula. An interface call is a real
    // dispatch, and the C baseline has to pay for one.
    AreaFn volatile area = square_area;
    Shape shape = {square_area, &square};

    // The warm up pays for the first touch of the pages and of the caches.
    // It uses a counter of its own, so the checksum covers the measured work
    // only.
    Counter warmCounter = {0};
    int64_t warm = direct(1000) + lib_double(1000);
    counter_add(&warmCounter, 1000);
    warm += shape.area(shape.self, 1000);
    (void)warm;

    uint64_t base = 0;
    {
        uint64_t start = bench_now_ns();
        int64_t acc = baseline(workload);
        uint64_t stop = bench_now_ns();
        base = stop - start;
        (void)acc;
    }

    int64_t directSum = 0;
    int64_t crossSum = 0;
    int64_t ifaceSum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        int64_t acc = 0;
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            acc += direct((int64_t)i ^ (int64_t)r);
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        directSum += acc;
        bench_sink((uint64_t)directSum);
    }

    uint64_t* crossSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    for (int32_t r = 0; r < repetitions; r++) {
        int64_t acc = 0;
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            acc += lib_double((int64_t)i ^ (int64_t)r);
        }
        uint64_t stop = bench_now_ns();
        crossSamples[r] = stop - start;
        crossSum += acc;
        bench_sink((uint64_t)crossSum);
    }

    uint64_t* methodSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            counter_add(&counter, (int64_t)i ^ (int64_t)r);
        }
        uint64_t stop = bench_now_ns();
        methodSamples[r] = stop - start;
        bench_sink((uint64_t)counter.total);
    }

    uint64_t* ifaceSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    for (int32_t r = 0; r < repetitions; r++) {
        int64_t acc = 0;
        uint64_t start = bench_now_ns();
        for (int32_t i = 0; i < workload; i++) {
            acc += area(shape.self, (int64_t)i);
        }
        uint64_t stop = bench_now_ns();
        ifaceSamples[r] = stop - start;
        ifaceSum += acc;
        bench_sink((uint64_t)ifaceSum);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    bench_sort_u64(crossSamples, (size_t)repetitions);
    bench_sort_u64(methodSamples, (size_t)repetitions);
    bench_sort_u64(ifaceSamples, (size_t)repetitions);

    bench_metric("baseline", base, "ns");
    bench_metric("direct_p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("cross_p50", bench_percentile(crossSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("method_p50", bench_percentile(methodSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("iface_p50", bench_percentile(ifaceSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("workload", (uint64_t)workload, "iterations");
    bench_check("direct", (uint64_t)directSum);
    bench_check("cross", (uint64_t)crossSum);
    bench_check("method", (uint64_t)counter.total);
    bench_check("iface", (uint64_t)ifaceSum);
    bench_checksum((uint64_t)(directSum + crossSum + counter.total + ifaceSum));
    free(samples);
    free(baseSamples);
    free(crossSamples);
    free(methodSamples);
    free(ifaceSamples);
    return 0;
}
