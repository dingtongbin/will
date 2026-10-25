# Async baseline results

Machine: Intel Core i5-12500H, Windows 11, x86_64-win-msvc.
Toolchain: clang 22.1.8, Rust 1.97.0, crossbeam-channel 0.5.17,
crossbeam-utils 0.8.23, tokio 1.54.1.
Will: `844fb8e` with the channel and select ready paths.
Runs: five measured repetitions for the will harness, three process runs for
the Rust baselines. The benchmarks ran alone; stale test processes were removed
before the final runs.

| scenario | will p50 | Rust p50 | comparison |
| --- | --- | --- | --- |
| `conc/chan`, 1,000,000 round trips | 21.99 ms (22.0 ns/round) | crossbeam 26.1 ms (26.1 ns/round) | will is 1.19x faster (meets the 100% async gate) |
| `conc/select`, 500,000 waits | 15.15 ms (30.3 ns/wait) | crossbeam 32.1 ms (64.2 ns/wait) | will is 2.12x faster (meets the 100% async gate) |
| `conc/spawn`, 10,000 tasks | 20.12 ms (2.01 us/task) | tokio 6.30 ms (0.63 us/task) | will is 3.19x slower (does not meet the 100% async gate) |

The C thread-per-task spawn figure (about 530 ms) is not an acceptance
baseline: it creates one OS thread per task. The C ring and polling figures are
also not acceptance baselines for channel and select; crossbeam is the strong
comparator.

The spawn gap is scheduling, not channel or select work: the will run
distributes a burst of tiny tasks across all lanes and pays the cross-lane
wake/placement cost. Closing it needs the W35/W30 follow-up (bounded local
backlog plus work-stealing for not-yet-started auto tasks), not a weaker
baseline.
