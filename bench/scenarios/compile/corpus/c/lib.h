// lib.h is the header of the library part of the compile corpus.
#ifndef LIB_H
#define LIB_H

#include <stdint.h>
#include <stddef.h>

int64_t lib_count(const int64_t* values, size_t count);
int64_t lib_sum(const int64_t* values, size_t count);
int64_t lib_max(const int64_t* values, size_t count);
int64_t lib_min(const int64_t* values, size_t count);
int64_t lib_mean(const int64_t* values, size_t count);
size_t lib_histogram(const int64_t* values, size_t count, int64_t width, int64_t* buckets, size_t room);
size_t lib_rolling(const int64_t* values, size_t count, int32_t size, int64_t* out, size_t room);

#endif
