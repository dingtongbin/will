// bench.h is the support every C baseline of the benchmark suite uses: the
// monotonic clock, the metric lines both languages print, the percentile rule
// both languages apply, and the deterministic input generator.
//
// The will side of a scenario reads the clock through a C shim that includes
// bench.c, so both sides measure the same clock and use the same percentile
// rule. The rule is the nearest rank: the sample at ceil(count * num / den)
// of the ascending samples, counted from one.
#ifndef BENCH_H
#define BENCH_H

#include <stddef.h>
#include <stdint.h>

// bench_now_ns returns a monotonic clock reading in nanoseconds. The origin
// is arbitrary; only differences are meaningful.
uint64_t bench_now_ns(void);

// bench_rss_bytes returns the peak resident set size of this process in
// bytes. The value is read from the operating system, never estimated.
uint64_t bench_rss_bytes(void);

// bench_step returns the value it is given plus one. It lives in another
// translation unit, so a call to it cannot be inlined. The call cost
// scenario uses it to measure the cost of a call that crosses a boundary.
uint64_t bench_step(uint64_t value);

// bench_sink keeps the compiler from removing the work of a measured loop.
// The value is written to a volatile cell, so the loop that produced it has
// to run. Both sides of a scenario call it, so neither side gets a loop
// that the optimizer folded away.
void bench_sink(uint64_t value);

// bench_metric prints one metric line: "bench metric <name> <value> <unit>".
void bench_metric(const char* name, uint64_t value, const char* unit);

// bench_checksum prints the checksum line: "bench checksum <value>". Both
// sides of a scenario print the same value, which proves they did the same
// work.
void bench_checksum(uint64_t value);

// bench_check prints one check line: "bench check <name> <value>". A
// scenario prints one line per part of the work, so the tool can tell which
// part of the two sides agrees and which does not.
void bench_check(const char* name, uint64_t value);

// bench_sort_u64 sorts samples in ascending order.
void bench_sort_u64(uint64_t* samples, size_t count);

// bench_percentile returns the nearest rank percentile of ascending samples.
// num and den give the rank as the fraction num/den, so 99/100 is p99.
uint64_t bench_percentile(const uint64_t* sorted, size_t count, uint64_t num, uint64_t den);

// bench_fill_bytes fills data with a deterministic pseudo random sequence.
// The same seed and size give the same bytes on every platform.
void bench_fill_bytes(uint8_t* data, size_t size, uint64_t seed);

// bench_lcg_next returns the next value of the linear congruential
// generator the input generator uses.
uint64_t bench_lcg_next(uint64_t* state);

#endif
