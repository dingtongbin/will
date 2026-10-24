// bench.c is the implementation of the shared benchmark support.
#include "bench.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#include <windows.h>
#include <psapi.h>
#else
#include <sys/resource.h>
#include <time.h>
#endif

uint64_t bench_rss_bytes(void) {
#if defined(_WIN32)
    PROCESS_MEMORY_COUNTERS counters;
    if (GetProcessMemoryInfo(GetCurrentProcess(), &counters, sizeof(counters))) {
        return (uint64_t)counters.PeakWorkingSetSize;
    }
    return 0;
#else
    struct rusage usage;
    if (getrusage(RUSAGE_SELF, &usage) == 0) {
#if defined(__APPLE__)
        return (uint64_t)usage.ru_maxrss;
#else
        return (uint64_t)usage.ru_maxrss * 1024;
#endif
    }
    return 0;
#endif
}

uint64_t bench_now_ns(void) {
#if defined(_WIN32)
    LARGE_INTEGER counter;
    LARGE_INTEGER frequency;
    QueryPerformanceCounter(&counter);
    QueryPerformanceFrequency(&frequency);
    return (uint64_t)((counter.QuadPart * 1000000000ULL) / (uint64_t)frequency.QuadPart);
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
#endif
}

uint64_t bench_step(uint64_t value) {
    return value + 1;
}

void bench_sink(uint64_t value) {
    static volatile uint64_t sink;
    sink = value;
}

void bench_metric(const char* name, uint64_t value, const char* unit) {
    printf("bench metric %s %llu %s\n", name, (unsigned long long)value, unit);
}

void bench_checksum(uint64_t value) {
    printf("bench checksum %llu\n", (unsigned long long)value);
}

void bench_check(const char* name, uint64_t value) {
    printf("bench check %s %llu\n", name, (unsigned long long)value);
}

static int compare_u64(const void* left, const void* right) {
    uint64_t a = *(const uint64_t*)left;
    uint64_t b = *(const uint64_t*)right;
    if (a < b) {
        return -1;
    }
    if (a > b) {
        return 1;
    }
    return 0;
}

void bench_sort_u64(uint64_t* samples, size_t count) {
    if (count < 2) {
        return;
    }
    qsort(samples, count, sizeof(uint64_t), compare_u64);
}

uint64_t bench_percentile(const uint64_t* sorted, size_t count, uint64_t num, uint64_t den) {
    if (count == 0) {
        return 0;
    }
    // rank is ceil(count * num / den), counted from one.
    uint64_t rank = (uint64_t)count * num;
    rank = (rank + den - 1) / den;
    if (rank < 1) {
        rank = 1;
    }
    if (rank > count) {
        rank = count;
    }
    return sorted[rank - 1];
}

uint64_t bench_lcg_next(uint64_t* state) {
    *state = *state * 6364136223846793005ULL + 1442695040888963407ULL;
    return *state;
}

void bench_fill_bytes(uint8_t* data, size_t size, uint64_t seed) {
    uint64_t state = seed;
    size_t i = 0;
    while (i + 8 <= size) {
        uint64_t value = bench_lcg_next(&state);
        memcpy(data + i, &value, 8);
        i += 8;
    }
    if (i < size) {
        uint64_t value = bench_lcg_next(&state);
        for (; i < size; i++) {
            data[i] = (uint8_t)(value & 0xffu);
            value >>= 8;
        }
    }
}
