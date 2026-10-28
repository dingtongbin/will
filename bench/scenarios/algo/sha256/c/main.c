// main.c is the C baseline of the algo/sha256 scenario. It hashes a fixed
// message with the SHA-256 of the standard, the way the will side does.
//
// The message is padded before the measurement starts, so the measurement
// covers the compression of the blocks and nothing else.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// messageSize is the size of the message in bytes.
static const size_t messageSize = 119;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

// k holds the round constants of SHA-256.
static const uint32_t k[64] = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu, 0x59f111f1u,
    0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u, 0x243185beu, 0x550c7dc3u,
    0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u, 0xc19bf174u, 0xe49b69c1u, 0xefbe4786u,
    0x0fc19dc6u, 0x240ca1ccu, 0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau,
    0x983e5152u, 0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu, 0x53380d13u,
    0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u, 0xa2bfe8a1u, 0xa81a664bu,
    0xc24b8b70u, 0xc76c51a3u, 0xd192e819u, 0xd6990624u, 0xf40e3585u, 0x106aa070u,
    0x19a4c116u, 0x1e376c08u, 0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au,
    0x5b9cca4fu, 0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u
};

// ror rotates a word to the right.
static uint32_t ror(uint32_t value, int32_t amount) {
    return (value >> amount) | (value << (32 - amount));
}

// compress runs the compression of one block into the state.
static void compress(uint32_t state[8], const uint8_t* block, uint32_t words[64]) {
    for (int32_t i = 0; i < 16; i++) {
        words[i] = ((uint32_t)block[i * 4] << 24) | ((uint32_t)block[i * 4 + 1] << 16) |
                   ((uint32_t)block[i * 4 + 2] << 8) | (uint32_t)block[i * 4 + 3];
    }
    for (int32_t i = 16; i < 64; i++) {
        uint32_t s0 = ror(words[i - 15], 7) ^ ror(words[i - 15], 18) ^ (words[i - 15] >> 3);
        uint32_t s1 = ror(words[i - 2], 17) ^ ror(words[i - 2], 19) ^ (words[i - 2] >> 10);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }
    uint32_t a = state[0];
    uint32_t b = state[1];
    uint32_t c = state[2];
    uint32_t d = state[3];
    uint32_t e = state[4];
    uint32_t f = state[5];
    uint32_t g = state[6];
    uint32_t h = state[7];
    for (int32_t i = 0; i < 64; i++) {
        uint32_t s1 = ror(e, 6) ^ ror(e, 11) ^ ror(e, 25);
        uint32_t choose = (e & f) ^ ((~e) & g);
        uint32_t t1 = h + s1 + choose + k[i] + words[i];
        uint32_t s0 = ror(a, 2) ^ ror(a, 13) ^ ror(a, 22);
        uint32_t major = (a & b) ^ (a & c) ^ (b & c);
        uint32_t t2 = s0 + major;
        h = g;
        g = f;
        f = e;
        e = d + t1;
        d = c;
        c = b;
        b = a;
        a = t1 + t2;
    }
    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

// pad returns the padded message of the given size.
static uint8_t* pad(size_t* outSize) {
    size_t blocks = (messageSize + 8) / 64 + 1;
    size_t size = blocks * 64;
    uint8_t* message = (uint8_t*)calloc(size, 1);
    if (message == NULL) {
        return NULL;
    }
    bench_fill_bytes(message, messageSize, 0xA5A5);
    message[messageSize] = 0x80;
    uint64_t bits = (uint64_t)messageSize * 8;
    for (int32_t i = 0; i < 8; i++) {
        message[size - 1 - (size_t)i] = (uint8_t)((bits >> (8 * i)) & 0xffu);
    }
    *outSize = size;
    return message;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    if (samples == NULL) {
        return 1;
    }
    size_t size = 0;
    uint8_t* message = pad(&size);
    if (message == NULL) {
        return 1;
    }
    uint32_t words[64];
    // The warm up pays for the first touch of the pages and of the caches.
    {
        uint32_t state[8] = {0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
                             0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u};
        compress(state, message, words);
    }

    uint32_t digest[8] = {0, 0, 0, 0, 0, 0, 0, 0};
    for (int32_t r = 0; r < repetitions; r++) {
        uint32_t state[8] = {0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
                             0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u};
        uint64_t start = bench_now_ns();
        for (size_t at = 0; at < size; at += 64) {
            compress(state, message + at, words);
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        for (int32_t i = 0; i < 8; i++) {
            digest[i] += state[i];
        }
        bench_sink((uint64_t)digest[0]);
    }

    bench_sort_u64(samples, (size_t)repetitions);
    bench_metric("p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("p95", bench_percentile(samples, (size_t)repetitions, 95, 100), "ns");
    bench_metric("p99", bench_percentile(samples, (size_t)repetitions, 99, 100), "ns");
    bench_metric("max", samples[repetitions - 1], "ns");
    bench_metric("message", (uint64_t)messageSize, "bytes");
    bench_metric("blocks", (uint64_t)(size / 64), "blocks");
    uint64_t checksum = 0;
    for (int32_t i = 0; i < 8; i++) {
        checksum = checksum * 31u + digest[i];
    }
    bench_checksum(checksum);
    free(samples);
    free(message);
    return 0;
}
