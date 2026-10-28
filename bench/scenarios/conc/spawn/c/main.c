// main.c is the C baseline of the conc/spawn scenario. It creates one
// thread per task and joins every one of them, the way the will side
// creates one task per unit of work and lets the region wait for them.
//
// The threads are the baseline the method volume names: the acceptance line
// compares the task throughput against the thread throughput.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <pthread.h>
#endif

// count is the number of tasks the workload creates.
static const int32_t count = 10000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// work is the work one task does.
static int32_t work(int32_t index) {
    int32_t acc = 0;
    for (int32_t i = 0; i < 100; i++) {
        acc += i;
    }
    return acc + index;
}

// results holds the value every task produced.
static int32_t* results = NULL;

#if defined(_WIN32)
static DWORD WINAPI worker(LPVOID arg) {
    int32_t index = (int32_t)(intptr_t)arg;
    results[index] = work(index);
    return 0;
}
#else
static void* worker(void* arg) {
    int32_t index = (int32_t)(intptr_t)arg;
    results[index] = work(index);
    return NULL;
}
#endif

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    results = (int32_t*)malloc(sizeof(int32_t) * (size_t)count);
    if (samples == NULL || results == NULL) {
        return 1;
    }
    // The warm up pays for the first touch of the pages and of the caches.
#if defined(_WIN32)
    HANDLE warm = CreateThread(NULL, 0, worker, (LPVOID)(intptr_t)0, 0, NULL);
    if (warm != NULL) {
        WaitForSingleObject(warm, INFINITE);
        CloseHandle(warm);
    }
#else
    pthread_t warm;
    if (pthread_create(&warm, NULL, worker, (void*)(intptr_t)0) == 0) {
        pthread_join(warm, NULL);
    }
#endif

    int64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
#if defined(_WIN32)
        HANDLE* threads = (HANDLE*)malloc(sizeof(HANDLE) * (size_t)count);
        for (int32_t i = 0; i < count; i++) {
            threads[i] = CreateThread(NULL, 0, worker, (LPVOID)(intptr_t)i, 0, NULL);
        }
        for (int32_t i = 0; i < count; i++) {
            if (threads[i] != NULL) {
                WaitForSingleObject(threads[i], INFINITE);
                CloseHandle(threads[i]);
            }
        }
        free(threads);
#else
        pthread_t* threads = (pthread_t*)malloc(sizeof(pthread_t) * (size_t)count);
        for (int32_t i = 0; i < count; i++) {
            pthread_create(&threads[i], NULL, worker, (void*)(intptr_t)i);
        }
        for (int32_t i = 0; i < count; i++) {
            pthread_join(threads[i], NULL);
        }
        free(threads);
#endif
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
    }

    int64_t total = 0;
    for (int32_t i = 0; i < count; i++) {
        total += results[i];
    }
    checksum = total;

    bench_sort_u64(samples, (size_t)repetitions);
    uint64_t p50 = bench_percentile(samples, (size_t)repetitions, 50, 100);
    uint64_t p95 = bench_percentile(samples, (size_t)repetitions, 95, 100);
    uint64_t p99 = bench_percentile(samples, (size_t)repetitions, 99, 100);
    uint64_t worst = samples[repetitions - 1];

    bench_metric("p50", p50, "ns");
    bench_metric("p95", p95, "ns");
    bench_metric("p99", p99, "ns");
    bench_metric("max", worst, "ns");
    bench_metric("tasks", (uint64_t)count, "tasks");
    bench_checksum((uint64_t)checksum);
    free(samples);
    free(results);
    return 0;
}
