// lib.c is the library part of the compile corpus. It lives in its own
// translation unit, so the compiler has to work on two units of code.
#include "lib.h"

int64_t lib_count(const int64_t* values, size_t count) {
    int64_t total = 0;
    for (size_t i = 0; i < count; i++) {
        if (values[i] >= 0) {
            total += 1;
        }
    }
    return total;
}

int64_t lib_sum(const int64_t* values, size_t count) {
    int64_t total = 0;
    for (size_t i = 0; i < count; i++) {
        total += values[i];
    }
    return total;
}

int64_t lib_max(const int64_t* values, size_t count) {
    int64_t best = 0;
    int first = 1;
    for (size_t i = 0; i < count; i++) {
        if (first || values[i] > best) {
            best = values[i];
            first = 0;
        }
    }
    return best;
}

int64_t lib_min(const int64_t* values, size_t count) {
    int64_t best = 0;
    int first = 1;
    for (size_t i = 0; i < count; i++) {
        if (first || values[i] < best) {
            best = values[i];
            first = 0;
        }
    }
    return best;
}

int64_t lib_mean(const int64_t* values, size_t count) {
    if (count == 0) {
        return 0;
    }
    return lib_sum(values, count) / (int64_t)count;
}

size_t lib_histogram(const int64_t* values, size_t count, int64_t width, int64_t* buckets, size_t room) {
    size_t used = 0;
    if (width <= 0) {
        return 0;
    }
    for (size_t i = 0; i < count; i++) {
        int64_t bucket = values[i] / width;
        if (bucket < 0) {
            bucket = -bucket;
        }
        if ((size_t)bucket >= room) {
            continue;
        }
        buckets[bucket] += 1;
        if ((size_t)bucket + 1 > used) {
            used = (size_t)bucket + 1;
        }
    }
    return used;
}

size_t lib_rolling(const int64_t* values, size_t count, int32_t size, int64_t* out, size_t room) {
    size_t used = 0;
    if (size <= 0) {
        return 0;
    }
    int64_t total = 0;
    size_t index = 0;
    for (size_t i = 0; i < count; i++) {
        total += values[i];
        index++;
        if ((int32_t)index > size) {
            total -= values[index - (size_t)size - 1];
        }
        if ((int32_t)index >= size && used < room) {
            out[used] = total / size;
            used++;
        }
    }
    return used;
}
