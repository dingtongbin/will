// bench_clock.h is the clock, the memory reading and the latency cells the
// spawn-tail scenario uses. The implementation is shared with the rest of the
// suite, so both sides measure with the same clock.
unsigned long long bench_now_ns(void);
unsigned long long bench_rss_bytes(void);
void bench_sink(unsigned long long value);
void bench_latency_start(int index);
void bench_latency_end(int index);
unsigned long long bench_latency(int index);
