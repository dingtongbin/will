// lib.c holds the call that leaves the translation unit of the benchmark
// program. It stands for the call into another package on the will side.
#include "lib.h"

int64_t lib_double(int64_t value) {
    return value * 2;
}
