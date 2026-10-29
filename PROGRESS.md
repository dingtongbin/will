# will progress

Single progress and task-queue file. Facts and evidence only; the specification in
`desc/` wins on any conflict. Last updated: 2026-10-30T04:15+08:00.

## State

- Branch: `feature/package-mechanism`; HEAD `4e0afa8`; ahead 34 commits.
- Worktree has `README.md` modified; `PROGRESS.md` is this file; no staged
  changes.
- Toolchain: go1.25.9 windows/amd64; clang 22.1.8 at
  `C:\msys64\clang64\bin\clang.exe`.
- Fixed env: `WILL_RUNTIME=D:\githubproject\will\runtime`,
  `WILL_CLANG` above, `WILL_CACHE` under the opencode temp directory.
- Phase: M4 language surface / M5 std migration in progress. P0-1..P0-5 and
  P1-6 have close-out records in `desc/理由/最终目标差距与依赖链.md`; generic
  class monomorphization (P1-1) now works end to end.
- No active background jobs.

## Recent docs cleanup

Removed stale or duplicate `desc` files:

- `desc/实施计划.md` (mojibake and stale milestones).
- `desc/改动清单.md` (duplicate decisions and obsolete sync IO text).
- `desc/交接.md` (duplicate entry/constraint material).

Removed the stale reference in `desc/设计.md`, removed a duplicate rejection
bullet, and clarified the standard-library/external-module boundary in
`desc/标准库/总览.md`. Rewrote `README.md` to the current GC, class/struct,
async IO, run/build, and standard-library model.

## Goal

Close all P0/P1, settle P2, complete the per-package standard library matrix,
keep run/build differential green, then clear P3 performance/platform/soak. No
placeholders, no old surface, no skipped tests.

## Task queue

P0 (correctness main beams; re-verify with forced GC):
1. Re-run class field container, Array, async Error, select class-local and inline
   aggregate forced-cycle tests.
2. Add missing forced-cycle corpus where the unverified list still says "remaining":
   select class local, closure/interface box multi-cycle, container element roots.

P1 (final object model and surface):
1. `Weak<T>` source surface and upgrade semantics.
2. `Pinned<T>`/`Handle<T>` compiler lowering and lifetime checks.
3. ~~Generic class monomorphization~~ done (`4e0afa8`); next: cross-package
   instance corpus and generic enum corpus.
4. Final `list`/`dict`/`set`; delete `Array`/`Map`/`Set`.
5. Delete `rc`/`arc`; move `std/sync` to final class surface.
6. `std/parallel` real chunked CPU jobs.
7. Migrate std text/container/encoding/IO/os to final surface.
8. Per-package API/test/example/cross-platform/perf matrix.

P2: cache keys (GC params/barrier/stackmap/target), corpus migration,
deterministic `.willc`/decisions/index replay (lowering determinism fixed,
`TestEmitLLVMIsDeterministic`), `gate -full` green (green this round).

P3: allocation fast path inline, barriers, p999, soak, platform matrix.

## Verified this round

- `go build ./...` PASS; `gofmt -l .` clean after formatting
  `internal/gcmeta/nested.go`, `internal/gen/gen.go`, `build/migrate-w40/main.go`.
- Full `go test -p=1 -count=1 ./...` PASS (all packages; cli 118s, task 53s,
  sync 9s, io 8.2s) with `WILL_RUNTIME`/`WILL_CLANG`/`WILL_CACHE` set.
- Generic class `Box<T>` now compiles and runs end to end: constructor, field
  access, inside-class method, class-typed field, forced full cycles.
  `Box<Node>` with a 200k allocation loop returns 41 under `WILL_GC_MEMLIMIT=1`;
  the chained case `b.get().value` passes 10/10 runs on a cold cache.
- Two independent compiler defects were found and fixed with this probe:
  1. `alloca` for expression temporaries was emitted inside loop bodies, so the
     stack grew every iteration and overflowed at 65536 iterations
     (`0xC00000FD`). `allocSlot` defers every slot to the entry block, and all
     body-level array, clone, share, rc, str and interface temporaries now use
     `tempAlloca`. A 200k-iteration loop that allocates, pushes and clones an
     Array exits 0 after the change.
  2. A class local/parameter was read from its duplicate local slot after a
     cycle; only the frame root array is updated by the collector. Reads now go
     through `gcRootSlots` when present.
- Cache incident: after rebuilding the compiler several times, `will run`
  appeared to reuse objects produced by an earlier intermediate compiler
  (registration trace showed ids 2,2,3 with no id 1). Clearing `WILL_CACHE`
  removed the wrong binaries. This is not yet reproduced as a cache-key bug;
  treat as P2-1 evidence and keep compiler changes and cache resets together.
- Determinism defect found while re-recording the corpus: two cold-cache
  `emit-llvm` runs of the same input differed byte for byte. `HEAD` reproduces
  it, so it predates this round. Root cause: state-machine frame layout and
  inline-root refresh followed map iteration order (`g.recs`,
  `gcInlineRoots`). `statemachine_async_sequence.go`, `selectmachine.go`,
  `prepareGeneralInlineRoots`, `generalGCSlots` and `refreshInlineRoots` now
  use sorted record order. Six consecutive cold-cache runs of
  `tests/concurrency/async` produce the same IR.
  `TestEmitLLVMIsDeterministic` fails on `HEAD` and passes now.
- `concurrency/syncmutex` exposed a second generic-class bug: `rawDocs` fed the
  GC type graph the cloned generic-instance documents, so `std/sync.MutexState`
  was declared twice. Clone documents are now excluded from the type graph;
  `emit-llvm` on that case exits 0.
- Cross-package generic class now works: `lib.Box<lib.Node>(...)` in an
  importing package. Two defects were fixed:
  1. gcmeta could not resolve `alias.Name`; the type graph now records each
     package's imports and canonicalizes the alias to the module path.
  2. gen cloned a method instance into the defining package but kept the
     caller's `lib.Node` spelling; type arguments now use the qualified `to`
     text of the type record and are canonicalized recursively.
  `tests/build/genericclassimport` and `TestRunGenericClassAcrossPackages`
  cover the path; the run exits 41.
- P0 forced-cycle matrix re-run against the new root-read path:
  `go test ./internal/cli -run 'TestRunClass|TestRunDeadline|TestRunSelect|TestRunAsync' -count=2`
  PASS (75.7s).
- C full-cycle probe: median throughput 0.40 M/s, p50 111.6 ms;
  allocation-only 13.8 M/s. Not release acceptance.
- Crash recovery: the session was interrupted after the alloca rewrite. All
  edits are on disk, `go build ./...` and `go test ./internal/gen` are green.
  The previous background gate run was lost with the process; rerun it from
  scratch.

## Generic class work (P1-1)

Implemented:

1. Checker: constructor type arguments specialize field and init contracts
   (`instantiateNamed` in `internal/types/infer.go`). Methods written inside a
   generic type carry the receiver type arguments, and the receiver names are
   free type parameters in the method body.
2. willc: `new` records the instantiated type text (`type="Box<i32>"`); the
   record has no `typearg` field.
3. gcmeta: generic class/struct/enum declarations are templates; concrete uses
   create instances keyed by canonical `pkg.Name<Arg,...>`. Instances enter
   `Program.Types()` and get class IDs in sorted order. `TypeText` resolves
   generic spellings. Type texts inside templates are not instantiated.
4. gen: payload, field access, `new`, init and method instances resolve the
   canonical instance; `typeDeclIn`/`genericStructDoc` cover classes as well as
   structs.

Remaining for P1-1:

- Generic enum instances are implemented in gcmeta but have no corpus yet.
- `Weak`/`Pinned`/`Handle` and the final container surface stay P1.

Final verification this round:

- `gate -full` PASS (`gate: ok`, exit 0) after re-recording the lowering
  corpus; `go vet ./...` PASS; `gofmt -l .` empty; `checkcode` clean.
- `will run tests/build/genericclass` exits 42; `will build` then running
  `build/x86_64-win-gnu/genericclass.exe` exits 42 with identical stdout and
  stderr. `tests/build/genericclassimport` behaves the same with exit 41.
- `will build build/probestack` and the release binary exit 0 for the 200k
  array/clone loop.
- P0 matrix (class/deadline/select/async, two rounds) PASS.
- Commits: `6f64783` (deterministic, loop-safe lowering), `4e0afa8` (generic
  class monomorphization), plus the cross-package generic class fix.

Evidence commands:

- `go run ./cmd/will run tests/build/genericclassimport`
- `go test ./internal/types ./internal/gcmeta ./internal/gen ./internal/cli`

## Known failures / blockers

1. Standard library is not complete: many P1 packages and APIs are still on the
   old surface or missing.
2. The cache-key incident is not reproduced as a deterministic test; compiler
   changes must still be validated with a cold `WILL_CACHE`.
3. Disk headroom is tight; C pagefile is large and can hide output-space
   failures.

## Next three

1. Generic enum instance corpus (template + class/struct argument), including a
   cross-package case.
2. P1-4: `Weak<T>` source surface, `upgrade()`, and concurrent weak clearing.
3. P1-5: `Pinned<T>`/`Handle<T>` lowering and lifetime checks across the C
   boundary.

## Unverified risks

- Async state-machine roots and interface boxes under forced cycles.
- `str`/`bytes` view owner keep-alive for `[]u8` views.
- GC card/pinned scanning precision and full-cycle cost.
- POSIX/Android/iOS views, pin, collector, async IO never run on this host.
- Full release-build performance matrix is pending.
- `mentionsTypeParam` treats a type-parameter name as template-only by word
  match; a user type or package with the same short name needs an audit.

## Key decisions / rejected options

- No conservative stack scanning; precise roots and descriptors only.
- No second backend, interpreter, or run-only semantics; run/build differ only in
  optimization and compile strategy.
- Deleted surfaces are removed, not shimmed; the parser fails on old syntax
  naturally.
- Standard library completion is part of final delivery, not an optional
  post-core task.
- Long design, build, test, gate, benchmark, fuzz, and soak work runs in the
  background while foreground coding continues.
