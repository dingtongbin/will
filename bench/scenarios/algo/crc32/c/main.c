// main.c is the C baseline of the algo/crc32 scenario. It computes the
// CRC-32 of a generated buffer with the same bitwise algorithm the will
// side uses, so the two sides agree on the checksum.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// bufferSize is the size of the buffer in bytes.
static const size_t bufferSize = 262144;

// seed is the seed of the input generator.
static const uint64_t seed = 0x12345678;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 3;

// crc32 computes the CRC-32 of a buffer. The algorithm is the reflected
// IEEE 802.3 variant, one bit at a time.
//
// The seed takes part in the work, so two calls with different seeds give
// different results. Without it the compiler may run the loop once and
// reuse the result, and the measurement would time nothing.
static uint32_t crc32(const uint8_t* data, size_t size, uint32_t seed) {
    uint32_t crc = seed ^ 0xFFFFFFFFu;
    for (size_t i = 0; i < size; i++) {
        crc ^= (uint32_t)data[i];
        for (int32_t k = 0; k < 8; k++) {
            if ((crc & 1u) != 0u) {
                crc = (crc >> 1) ^ 0xEDB88320u;
            } else {
                crc = crc >> 1;
            }
        }
    }
    return crc ^ 0xFFFFFFFFu;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    uint8_t* data = (uint8_t*)malloc(bufferSize);
    if (samples == NULL || data == NULL) {
        return 1;
    }
    bench_fill_bytes(data, bufferSize, seed);
    // The warm up pays for the first touch of the pages and of the caches.
    uint32_t warm = crc32(data, 1024, 0);
    (void)warm;

    uint64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        checksum += crc32(data, bufferSize, (uint32_t)r);
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        bench_sink(checksum);
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
    bench_metric("bytes", (uint64_t)bufferSize, "bytes");
    bench_checksum(checksum);
    free(samples);
    free(data);
    return 0;
}
