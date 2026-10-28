// main.c is the C baseline of the io/file scenario. It writes a file, reads
// it in order, reads it at random places and writes it again.
//
// The will side waits for the file package of the standard library, so only
// the C numbers exist today.
#include "bench.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

// fileSize is the size of the file in bytes.
static const size_t fileSize = 4 * 1024 * 1024;

// reads is the number of blocks the read workloads move.
static const int32_t reads = 4096;

// blockSize is the size of one block in bytes.
static const size_t blockSize = 4096;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// path is the file the workload uses.
static const char* path = "bench_file.tmp";

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    uint64_t* randomSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    uint64_t* writeSamples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL || randomSamples == NULL || writeSamples == NULL) {
        return 1;
    }
    uint8_t* block = (uint8_t*)malloc(blockSize);
    if (block == NULL) {
        return 1;
    }
    bench_fill_bytes(block, blockSize, 0x5EED);

    // The write workload fills the file once.
    FILE* file = fopen(path, "wb");
    if (file == NULL) {
        return 1;
    }
    for (size_t i = 0; i < fileSize / blockSize; i++) {
        fwrite(block, 1, blockSize, file);
    }
    fclose(file);

    // The warm up pays for the first touch of the pages and of the caches.
    file = fopen(path, "rb");
    if (file == NULL) {
        return 1;
    }
    fread(block, 1, blockSize, file);
    fclose(file);

    uint64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        file = fopen(path, "rb");
        for (int32_t i = 0; i < reads; i++) {
            fread(block, 1, blockSize, file);
        }
        fclose(file);
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        checksum += block[0];
    }

    uint64_t state = 0x1234;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        file = fopen(path, "rb");
        for (int32_t i = 0; i < reads; i++) {
            size_t at = (size_t)(bench_lcg_next(&state) % (fileSize / blockSize)) * blockSize;
            fseek(file, (long)at, SEEK_SET);
            fread(block, 1, blockSize, file);
        }
        fclose(file);
        uint64_t stop = bench_now_ns();
        randomSamples[r] = stop - start;
        checksum += block[1];
    }

    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        file = fopen(path, "wb");
        for (size_t i = 0; i < fileSize / blockSize; i++) {
            fwrite(block, 1, blockSize, file);
        }
        fclose(file);
        uint64_t stop = bench_now_ns();
        writeSamples[r] = stop - start;
    }
    remove(path);

    bench_sort_u64(samples, (size_t)repetitions);
    bench_sort_u64(randomSamples, (size_t)repetitions);
    bench_sort_u64(writeSamples, (size_t)repetitions);

    bench_metric("sequential_read_p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("random_read_p50", bench_percentile(randomSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("write_p50", bench_percentile(writeSamples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("bytes", (uint64_t)fileSize, "bytes");
    bench_metric("blocks", (uint64_t)reads, "blocks");
    bench_checksum(checksum);
    free(samples);
    free(randomSamples);
    free(writeSamples);
    free(block);
    return 0;
}
