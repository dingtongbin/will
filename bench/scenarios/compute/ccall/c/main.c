// main.c is the C baseline of the compute/ccall scenario. It measures the
// cost of one call three ways: a call into a function of the same unit, a
// call into another unit, and the clock call itself.
//
// The will side does the same three measurements: a call into a function of
// its own module, a call into a C function, and the clock call.
#include "bench.h"

#include <stdint.h>
#include <stdlib.h>

// calls is the number of calls one measurement makes.
static const int32_t calls = 10000000;

// empty_local is a function of this unit. A call to it is inlined, so it
// shows the cost of the loop alone.
static uint64_t empty_local(uint64_t value) {
    return value;
}

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)calls);
    if (samples == NULL) {
        return 1;
    }
    uint64_t sink = 0;
    uint64_t clockSink = 0;
    uint64_t acc = 0;

    // The clock call on its own. It is the resolution the other two
    // measurements are read with.
    uint64_t t0 = bench_now_ns();
    for (int32_t i = 0; i < calls; i++) {
        clockSink = bench_now_ns();
    }
    uint64_t t1 = bench_now_ns();
    (void)clockSink;

    // A call into another unit.
    uint64_t t2 = bench_now_ns();
    for (int32_t i = 0; i < calls; i++) {
        sink = bench_step(sink);
    }
    uint64_t t3 = bench_now_ns();

    // A call into a function of this unit.
    uint64_t t4 = bench_now_ns();
    for (int32_t i = 0; i < calls; i++) {
        acc = empty_local(acc);
    }
    uint64_t t5 = bench_now_ns();

    bench_metric("clock_call_ns", (t1 - t0) / (uint64_t)calls, "ns");
    bench_metric("c_call_ns", (t3 - t2) / (uint64_t)calls, "ns");
    bench_metric("inlined_call_ns", (t5 - t4) / (uint64_t)calls, "ns");
    bench_metric("calls", (uint64_t)calls, "calls");
    bench_check("calls_made", sink);
    bench_check("acc", acc);
    bench_checksum(sink + acc);
    free(samples);
    return 0;
}
