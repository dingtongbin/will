# Rust async baselines

These are the strong Rust comparators for the will async acceptance matrix.
They use the same message counts and work per task as the will scenarios.

- `rustbench -- chan`: crossbeam-channel bounded(64), one send plus one receive
  per iteration, 1,000,000 iterations, five measured repetitions.
- `rustbench -- select`: four crossbeam-channel receivers with a default arm,
  500,000 iterations, five measured repetitions.
- `rustbench -- tail`: one bounded(64) round trip, sampled per operation for
  500,000 round trips; p999 and throughput per core.
- `rustbench -- select-tail`: four receivers with a default arm, sampled per
  operation for 500,000 waits; p999 and throughput per core.
- `spawn`: Tokio multi-thread runtime, 10,000 spawned tasks, each running the
  same 100-iteration integer loop, then awaiting every handle; reports per-task
  p999 and throughput.
- `compio_spawn`: the same workload on one compio runtime per available core,
  the thread-per-core (TPC) comparator; reports per-task p999 and throughput.
- `compio_spawn_single`: the same burst on one compio runtime with one producer,
  the per-core TPC comparator.
- `mixed`: 64 Tokio readers, 100 reads each, plus 2,743 Rayon CPU jobs, which is
  the 70/30 mixed workload; reports IO, CPU, and combined p999 plus throughput.

Build and run offline with the checked-in lock file:

```text
cargo build --release --offline
target/release/rustbench.exe chan
target/release/rustbench.exe select
target/release/rustbench.exe tail
target/release/rustbench.exe select-tail
target/release/spawn.exe
target/release/compio_spawn.exe
target/release/compio_spawn_single.exe
target/release/mixed.exe
```

The versions used for the recorded numbers are crossbeam-channel 0.5.17,
crossbeam-utils 0.8.23, tokio 1.54.1, compio 0.20.0-beta.1, rayon 1.12.0, and
Rust 1.97.0.
