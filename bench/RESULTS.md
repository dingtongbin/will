# Async baseline results

Acceptance is **p999 plus throughput/TPC**, not p50. p50 is recorded only to
diagnose the body of the distribution.

Machine: Intel Core i5-12500H, Windows 11, x86_64-win-msvc.
Toolchain: clang 22.1.8, Rust 1.97.0, crossbeam-channel 0.5.17,
crossbeam-utils 0.8.23, tokio 1.54.1, compio 0.20.0-beta.1.
Will: `342c24f`; six measured repetitions for spawn, five for channel and
select on Windows; Linux numbers use the same tree on the four-core VM.
The Windows clock has 100 ns granularity, so sub-100 ns differences in the
channel/select tables are quantization, not a measured win.

## Channel and select tail

`conc/tail` and `conc/selecttail` sample every operation and report the p999 of
500,000 operations. TPC does not change these single-task paths; the strong
comparator is crossbeam-channel.

| scenario | will p999 | crossbeam p999 | will throughput | crossbeam throughput |
| --- | --- | --- | --- | --- |
| `conc/tail`, send+recv round trip | 200 ns | 200 ns | 7.7-8.3 M/s | 7.4-7.8 M/s |
| `conc/selecttail`, 4 arms + default | 200 ns | 200-300 ns | 8.0-8.2 M/s | 5.8-6.4 M/s |

The C hand-written ring reports 100 ns p999 for `conc/tail`, but it has no task,
await, or channel contract; it is not the acceptance baseline. The async
channel/select paths now meet or beat the strongest Rust comparator on p999 and
throughput.

## Spawn: TPC and work-stealing baselines

`conc/spawntail` starts 10,000 tasks, records each task from just before spawn
to its completion, and reports the p999 over 50,000 samples. The will run uses
the default auto placement. The Rust baselines use the same task body:
compio is the thread-per-core (TPC) runtime; Tokio is the work-stealing runtime.

| runtime | p999 | throughput | per-core throughput |
| --- | --- | --- | --- |
| will, 16 lanes, auto group | 28.9-77.5 us | 0.95-1.04 M tasks/s | 59-65 k/lane/s |
| compio TPC, 1 runtime, 1 producer | 1.84-2.95 ms | 2.9-3.5 M tasks/s | 2.9-3.5 M/core/s |
| compio TPC, 16 runtimes, 16 producers | 1.45-2.68 ms | 3.1-3.9 M tasks/s | 0.20-0.24 M/core/s |
| Tokio work stealing, 16 workers | 68-96 us | 1.2-1.5 M tasks/s | 75-95 k/core/s |

The will number is six clean runs. Auto now places a run of eight spawns across
the three least-loaded lanes, enqueues every task before returning from
`spawn`, and wakes each chosen lane once for the run. Results up to 32 bytes stay
in the task object, so a scalar result no longer allocates and frees a block per
task.

will has the lowest p999 in this table: 28.9-77.5 us against Tokio 68-96 us and
compio 1.45-2.68 ms. Its throughput is 0.63-0.87x Tokio and 0.24-0.34x compio.
`compio_spawn_single` closes the producer-count gap: one compio runtime and one
producer run the same 10,000-task burst at 2.9-3.5 M tasks/s on one core. will is
therefore behind compio on task throughput even against the single-producer
baseline; compio's p999 is high because it queues the whole burst before driving
the runtime. The 16-runtime row is kept to show that compio does not scale the
burst with more runtimes.

The 10 us p999 target in `并发.md` is the park-to-running wake latency, not the
whole spawn-to-completion latency measured here; this table does not replace
that wake test. Channel and select are ahead of crossbeam on both p999 and
throughput.

## Mixed CPU and IO

`conc/mixed` keeps 64 readers in flight, each doing 100 asynchronous reads, and
submits 2,743 CPU jobs. That is 70/30 by operation count. Every operation reports
its own p999; a single burst of ten thousand queued tasks is not used because it
measures queue depth, not service latency. The Rust comparator uses a two-worker
Tokio runtime for the reads and a 14-thread Rayon pool for the jobs.

| runtime | IO p999 | CPU p999 | combined p999 | combined p99 | throughput |
| --- | --- | --- | --- | --- | --- |
| will, 16 lanes, default reserve | 4.4-10.8 ms | 1.3-2.0 ms | 4.3-10.8 ms | 3.4-5.7 ms | 46-50 k ops/s |
| will, 16 lanes, no reserve | 5.4-6.7 ms | 0.97-1.87 ms | 5.3-6.5 ms | 3.7-4.6 ms | 45-49 k ops/s |
| Tokio + Rayon | 5.25-11.4 ms | 0.28-0.70 ms | 5.20-11.4 ms | 3.3-5.1 ms | 49.4-50.7 k ops/s |

will throughput is 0.91-1.01x the Rust comparator: the low end is below the
async 100% gate and the high end only touches it. CPU p999 is about three times
the Rust tail, while the combined p99 and p999 are in the same band. At 8 lanes
the round is about 253 ms and at 16 lanes about 252 ms; 1/2/4 lanes are much
slower. The Windows file path now uses four fixed submit workers plus a
completer, not the per-lane IOCP batches required by W33; this table records the
remaining architecture gap rather than a completion-engine win.

## Linux VM cross-check

Ubuntu 24.04, four cores, clang 18, Go 1.27.1. The whole test suite and
`gate -full` pass. The mixed scenario on four lanes measures:

| runtime | round | throughput | combined p999 |
| --- | --- | --- | --- |
| will, pread workers | 0.38-0.47 s | 19-24 k ops/s | 37-82 ms |
| will, io_uring ring (`WILL_IO_URING=1`) | 0.50-0.54 s | 17-18 k ops/s | 37-64 ms |
| Tokio + Rayon | 0.178-0.189 s | 46.9-51 k ops/s | 7.3-9.1 ms |

The shared io_uring ring is correct but slower than the four pread workers on
this VM and is not per-lane, so it stays behind the measurement switch. The
Linux mixed throughput is 0.4-0.5x Rust; W33 remains open.

## Commands

```text
# will channel/select/spawn tail
build\will.exe run bench\scenarios\conc\tail\will
build\will.exe run bench\scenarios\conc\selecttail\will
build\will.exe run bench\scenarios\conc\spawntail\will
build\will.exe run bench\scenarios\conc\mixed\will

# Rust strong baselines
cd bench\baselines\rust
cargo build --release --offline
target\release\rustbench.exe tail
target\release\rustbench.exe select-tail
target\release\spawn.exe
target\release\compio_spawn.exe
target\release\mixed.exe
```

`WILL_LANES=1|2|4|8|16` selects the will lane count for scaling measurements.
