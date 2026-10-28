// main.c is the C baseline of the algo/sort scenario. It sorts with the
// same quicksort the will side writes: the last element of the range is the
// pivot, the partition moves every value below the pivot to the left.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// smallSize is the size of the first array.
static const int32_t smallSize = 1024;

// largeSize is the size of the second array.
static const int32_t largeSize = 1000000;

// smallRepetitions is the number of measured repetitions of the first array.
static const int32_t smallRepetitions = 10;

// largeRepetitions is the number of measured repetitions of the second
// array. The sort of a million values costs enough on its own.
static const int32_t largeRepetitions = 1;

// seed is the seed of the input generator.
static const uint64_t seed = 0x9E3779B97F4A7C15ULL;

// partition puts every value below the pivot left of it and returns the
// place of the pivot.
static int32_t partition(int64_t* xs, int32_t lo, int32_t hi) {
    int64_t pivot = xs[hi];
    int32_t i = lo;
    for (int32_t j = lo; j < hi; j++) {
        if (xs[j] < pivot) {
            int64_t tmp = xs[i];
            xs[i] = xs[j];
            xs[j] = tmp;
            i++;
        }
    }
    int64_t last = xs[i];
    xs[i] = xs[hi];
    xs[hi] = last;
    return i;
}

// quicksort sorts the range of the array.
static void quicksort(int64_t* xs, int32_t lo, int32_t hi) {
    if (lo >= hi) {
        return;
    }
    int32_t pivot = partition(xs, lo, hi);
    quicksort(xs, lo, pivot - 1);
    quicksort(xs, pivot + 1, hi);
}

// fill fills the array with the values of the generator. The will side
// writes the same generator, so both sides sort the same values.
static void fill(int64_t* xs, int32_t size) {
    uint64_t state = seed;
    for (int32_t i = 0; i < size; i++) {
        xs[i] = (int64_t)(bench_lcg_next(&state) >> 1);
    }
}

// sum adds the values of the array, so the checksum covers every element.
static uint64_t sum(const int64_t* xs, int32_t size) {
    uint64_t total = 0;
    for (int32_t i = 0; i < size; i++) {
        total += (uint64_t)xs[i];
    }
    return total;
}

int main(void) {
    uint64_t* smallSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)smallRepetitions);
    uint64_t* largeSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)largeRepetitions);
    int64_t* small = (int64_t*)malloc(sizeof(int64_t) * (size_t)smallSize);
    int64_t* large = (int64_t*)malloc(sizeof(int64_t) * (size_t)largeSize);
    if (smallSamples == NULL || largeSamples == NULL || small == NULL || large == NULL) {
        return 1;
    }
    fill(small, smallSize);
    fill(large, largeSize);

    // The warm up pays for the first touch of the pages and of the caches.
    quicksort(small, 0, smallSize - 1);
    quicksort(large, 0, largeSize - 1);

    uint64_t checksum = 0;
    for (int32_t r = 0; r < smallRepetitions; r++) {
        fill(small, smallSize);
        uint64_t start = bench_now_ns();
        quicksort(small, 0, smallSize - 1);
        uint64_t stop = bench_now_ns();
        smallSamples[r] = stop - start;
        checksum += sum(small, smallSize);
        bench_sink(checksum);
    }
    for (int32_t r = 0; r < largeRepetitions; r++) {
        fill(large, largeSize);
        uint64_t start = bench_now_ns();
        quicksort(large, 0, largeSize - 1);
        uint64_t stop = bench_now_ns();
        largeSamples[r] = stop - start;
        checksum += sum(large, largeSize);
        bench_sink(checksum);
    }

    bench_sort_u64(smallSamples, (size_t)smallRepetitions);
    bench_sort_u64(largeSamples, (size_t)largeRepetitions);

    bench_metric("p50_1k", bench_percentile(smallSamples, (size_t)smallRepetitions, 50, 100), "ns");
    bench_metric("p95_1k", bench_percentile(smallSamples, (size_t)smallRepetitions, 95, 100), "ns");
    bench_metric("max_1k", smallSamples[smallRepetitions - 1], "ns");
    bench_metric("p50_1m", bench_percentile(largeSamples, (size_t)largeRepetitions, 50, 100), "ns");
    bench_metric("max_1m", largeSamples[largeRepetitions - 1], "ns");
    bench_metric("small_size", (uint64_t)smallSize, "values");
    bench_metric("large_size", (uint64_t)largeSize, "values");
    bench_checksum(checksum);
    free(smallSamples);
    free(largeSamples);
    free(small);
    free(large);
    return 0;
}
