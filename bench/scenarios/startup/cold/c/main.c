// main.c is the C baseline of the startup/cold scenario. It is an empty
// program, so the time of the process is the time of the start up.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * 5);
    if (samples == NULL) {
        return 1;
    }
    // The program does nothing, so the tool measures the time of the process
    // itself. The metric line keeps the shape of the other scenarios.
    bench_metric("empty", 1, "program");
    free(samples);
    return 0;
}
