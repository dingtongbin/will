# GC p999 probe

This directory holds an opt-in probe for the concurrent collector. It keeps a
cycle thread requesting full cycles, while a mutator allocates through the
TLAB and samples allocation and safepoint latency over a bounded rooted ring.

Run it on an otherwise idle machine:

```text
go test -tags p999 -run TestP999 ./bench/p999 -count=1 -v
```

The probe is not part of the normal gate and does not replace the release
matrix. It measures one host and one heap shape. A release p999 claim still
needs `will build`, the fixed CPU quota, the full workload matrix, and the
recorded machine conditions from `desc/语言/性能.md`.

Current Windows x86_64 probe:

```text
alloc_ns p50=100 p99=300 p999=2500
safepoint_ns p50=0 p99=100 p999=600
```
