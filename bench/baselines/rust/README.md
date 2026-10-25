# Rust async baselines

These are the strong Rust comparators for the will async acceptance matrix.
They use the same message counts and work per task as the will scenarios.

- `rustbench -- chan`: crossbeam-channel bounded(64), one send plus one receive
  per iteration, 1,000,000 iterations, five measured repetitions.
- `rustbench -- select`: four crossbeam-channel receivers with a default arm,
  500,000 iterations, five measured repetitions.
- `spawn`: Tokio multi-thread runtime, 10,000 spawned tasks, each running the
  same 100-iteration integer loop, then awaiting every handle.

Build and run offline with the checked-in lock file:

```text
cargo build --release --offline
target/release/rustbench.exe chan
target/release/rustbench.exe select
target/release/spawn.exe
```

The versions used for the recorded numbers are crossbeam-channel 0.5.17,
crossbeam-utils 0.8.23, tokio 1.54.1, and Rust 1.97.0.
