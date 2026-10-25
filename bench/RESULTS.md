# Async baseline results

Acceptance is **p999 plus throughput/TPC**, not p50. p50 is recorded only to
diagnose the body of the distribution.

Machine: Intel Core i5-12500H, Windows 11, x86_64-win-msvc.
Toolchain: clang 22.1.8, Rust 1.97.0, crossbeam-channel 0.5.17,
crossbeam-utils 0.8.23, tokio 1.54.1, compio 0.20.0-beta.1.
Will: `e290b27`; six measured repetitions for spawn, five for channel and
select.
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
| will, 16 lanes, auto group | 27.5-79.8 us | 0.97-1.06 M tasks/s | 61-66 k/lane/s |
| compio TPC, 16 runtimes | 1.45-2.68 ms | 3.1-3.9 M tasks/s | 0.20-0.24 M/core/s |
| Tokio work stealing, 16 workers | 68-96 us | 1.2-1.5 M tasks/s | 75-95 k/core/s |

The will number is six clean runs. Auto now places a run of eight spawns across
the three least-loaded lanes, enqueues every task before returning from
`spawn`, and wakes each chosen lane once for the run. Results up to 32 bytes stay
in the task object, so a scalar result no longer allocates and frees a block per
task.

will has the lowest p999 in this table: 27.5-79.8 us against Tokio 68-96 us and
compio 1.45-2.68 ms. Its throughput is 0.65-0.88x Tokio and 0.25-0.34x compio.
The compio number is not apples to apples: `compio_spawn` uses one producer per
runtime, while the will and Tokio runs use one producer thread. A single-producer
compio TPC baseline is still missing and stays on W34.

The 10 us p999 target in `并发.md` is the park-to-running wake latency, not the
whole spawn-to-completion latency measured here; this table does not replace
that wake test. Channel and select are ahead of crossbeam on both p999 and
throughput.

## Commands

```text
# will channel/select/spawn tail
build\will.exe run bench\scenarios\conc\tail\will
build\will.exe run bench\scenarios\conc\selecttail\will
build\will.exe run bench\scenarios\conc\spawntail\will

# Rust strong baselines
cd bench\baselines\rust
cargo build --release --offline
target\release\rustbench.exe tail
target\release\rustbench.exe select-tail
target\release\spawn.exe
target\release\compio_spawn.exe
```

`WILL_LANES=1|2|4|8|16` selects the will lane count for scaling measurements.
