// main.c is the C baseline of the text/utf8 scenario. It walks the same
// text, with the same decoder, the same split and the same number parsing,
// as the will side.
//
// The text holds ASCII, three byte characters and a four byte character, so
// the decoder does every case.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// text is the input of the scenario. The characters above the ASCII range
// are written as byte escapes, so the source stays ASCII.
static const char* text =
    "the quick brown fox 42 jumps over 1337 lazy dogs "
    "\xe4\xb8\xad\xe6\x96\x87\xe6\xb5\x8b\xe8\xaf\x95 "
    "\xf0\x9f\x98\x80 end";

// workload is the number of times the text is walked.
static const int32_t workload = 100000;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// next_scalar decodes one code point of the text and moves the position
// past it.
static uint32_t next_scalar(const uint8_t* data, size_t size, size_t* at) {
    uint8_t lead = data[*at];
    if (lead < 0x80u) {
        (*at)++;
        return lead;
    }
    if ((lead & 0xE0u) == 0xC0u) {
        uint32_t value = ((uint32_t)(lead & 0x1Fu) << 6) | ((uint32_t)data[*at + 1] & 0x3Fu);
        *at += 2;
        return value;
    }
    if ((lead & 0xF0u) == 0xE0u) {
        uint32_t value = ((uint32_t)(lead & 0x0Fu) << 12) | ((uint32_t)data[*at + 1] & 0x3Fu) << 6 |
                         ((uint32_t)data[*at + 2] & 0x3Fu);
        *at += 3;
        return value;
    }
    uint32_t value = ((uint32_t)(lead & 0x07u) << 18) | ((uint32_t)data[*at + 1] & 0x3Fu) << 12 |
                     ((uint32_t)data[*at + 2] & 0x3Fu) << 6 | ((uint32_t)data[*at + 3] & 0x3Fu);
    *at += 4;
    (void)size;
    return value;
}

// walk goes over the text once. It counts the scalars, hashes them, counts
// the fields of the split and parses the numbers.
//
// The seed takes part in the work, so two calls with different seeds give
// different results. Without it the compiler may run the loop once and
// reuse the result, and the measurement would time nothing.
static uint64_t walk(const uint8_t* data, size_t size, uint64_t seed) {
    uint64_t hash = seed;
    uint64_t fields = 0;
    uint64_t numbers = 0;
    uint64_t value = 0;
    int inNumber = 0;
    size_t at = 0;
    while (at < size) {
        uint32_t scalar = next_scalar(data, size, &at);
        hash = hash * 31u + (uint64_t)scalar;
        if (scalar == ' ') {
            fields++;
            inNumber = 0;
            value = 0;
        } else if (scalar >= '0' && scalar <= '9') {
            value = value * 10u + (uint64_t)(scalar - '0');
            inNumber = 1;
        } else if (inNumber) {
            numbers += value;
            value = 0;
            inNumber = 0;
        }
    }
    if (inNumber) {
        numbers += value;
    }
    return hash ^ (numbers * 1000003u) ^ (fields << 40);
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    const uint8_t* volatile data = (const uint8_t*)text;
    size_t size = 0;
    while (text[size] != '\0') {
        size++;
    }
    // The warm up pays for the first touch of the pages and of the caches.
    uint64_t warm = walk(data, size, 0);
    (void)warm;

    uint64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
        uint64_t start = bench_now_ns();
        uint64_t hash = 0;
        for (int32_t i = 0; i < workload; i++) {
            hash += walk(data, size, (uint64_t)r);
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        checksum += hash;
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
    bench_metric("bytes", (uint64_t)size, "bytes");
    bench_metric("workload", (uint64_t)workload, "walks");
    bench_checksum(checksum);
    free(samples);
    return 0;
}
