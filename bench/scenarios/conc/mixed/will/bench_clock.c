// bench_clock.c pulls the shared clock into the will program and owns the
// per-operation start and end cells of the mixed scenario.
#include "../../../../csupport/bench.c"

enum { bench_latency_cells = 100000 };

static unsigned long long bench_starts[bench_latency_cells];
static unsigned long long bench_ends[bench_latency_cells];

void bench_latency_start(int index) {
    bench_starts[index] = bench_now_ns();
}

void bench_latency_end(int index) {
    bench_ends[index] = bench_now_ns();
}

unsigned long long bench_latency(int index) {
    return bench_ends[index] - bench_starts[index];
}
