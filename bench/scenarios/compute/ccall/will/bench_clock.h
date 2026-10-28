// bench_clock.h is the clock, the memory reading and the anchor the will
// side of a scenario uses. The implementation is the shared support of the
// suite, so both sides measure with the same clock and neither side gets a
// loop the optimizer folded away.
unsigned long long bench_now_ns(void);
unsigned long long bench_rss_bytes(void);
void bench_sink(unsigned long long value);
unsigned long long bench_step(unsigned long long value);
