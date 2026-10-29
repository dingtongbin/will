# will progress

Single progress and task-queue file. Facts and evidence only; the specification in
`desc/` wins on any conflict. Last updated: 2026-10-30T04:15+08:00.

## State

- Branch: `feature/package-mechanism`; HEAD `dbc376a`; ahead 42 commits.
- Worktree has `README.md` modified; no staged changes.
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
- Running the re-recorded std corpora found an invalid-IR regression: a general
  state-machine entry block could contain indirect-root stores after its
  `switch`, so clang rejected `std/fmt`, `std/io` and `std/show` with
  `expected instruction opcode` at `sm.init:`. `allocas` no longer emits
  indirect roots for a general machine, and the machine clears its root maps.
  Those three programs now run with exit 0 and the IR compiles.
- `std/os/exec` crashes with `0xC0000005` on about 55-80% of runs on a simple
  `command(...).output()` program. The same crash reproduces with the `3041a5e`
  compiler, so it is a pre-existing std/exec defect, not a regression from
  this round. The `tests/std/exec` corpus only records IR and never ran the
  program, which hid it. The old `Cmd`/`Output` surface also contradicts the
  specification (`run`/`start`/`Child`), so migration belongs to P1-11 and is
  blocked on `std/io` pipes.
- Forced-cycle stress: `go test ./internal/gen -run
  'GC|Class|Async|Select|Container' -count=3` PASS.
- P2 replay determinism: two cold caches produce identical
  `emit-decisions` and `index` output for async, std/io and cross-package
  generic corpora. `TestEmitDecisionsAndIndexAreDeterministic` covers both.
- Cache LRU fixed: `Trim` aggregated file mtimes, which a cache hit never
  moved, so tied timestamps dropped an arbitrary entry. The entry directory
  mtime is the last-use clock now; `go test ./internal/cache -count=20`
  PASS.
- Lowering cache key completed: it now includes LTO, `gcmeta.ABIVersion`, the
  clang path/version, and the bytes of `willrt.h`/`willrt_gc.h`.
  `TestLoweringKeyCoversLinkStrategy` failed before the change;
  `TestLoweringKeyCoversRuntimeABIHeaders` covers the runtime side.
- Build determinism pinned at eight cold-cache runs for `emit-willc`,
  `emit-llvm`, `emit-decisions`, and `index`
  (`TestReplayEightTimesIsByteIdentical`, 1.16s).
- `std/os/path` now matches the specified surface: `separator`, `join`,
  `clean`, `glob`, `abs`. The unsupported `base`, `dir`, `ext`, and `is_abs`
  functions and their corpus are deleted; `abs` inlines the rooted check.
  The eleven helpers and six scalar constants are `__`-private. `pathnative`
  emits IR and runs with exit 0.
- While doing this, `elementText` was found to reject a `str` index when the
  expression type record was not consulted (inside a short-circuit `or`);
  a `str` index is one `u32` scalar and now types correctly.
- Benchmark probe (not release acceptance, one machine): `conc/tail`
  p999 1200-1500 ns, p50 100 ns, max 715 us, round_p50 29.3 ms with
  `will 0.0.0-dev 6cdc08268730`; the C comparator p999 is 100 ns. The tail
  moved from the old `342c24f` 200 ns number; re-measure on an idle host
  before treating it as a regression.
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

- `gate -full` PASS (`gate: ok`, exit 0) at `dbc376a`; `go vet ./...` PASS;
  `gofmt -l .` empty; `checkcode` clean.
- `will run tests/build/genericclass` exits 42; `will build` then running
  `build/x86_64-win-gnu/genericclass.exe` exits 42 with identical stdout and
  stderr. `tests/build/genericclassimport` behaves the same with exit 41.
- `will build build/probestack` and the release binary exit 0 for the 200k
  array/clone loop.
- P0 matrix (class/deadline/select/async, two rounds) PASS.
- `TestReplayEightTimesIsByteIdentical`; `TestLoweringKeyCoversLinkStrategy`
  and `TestLoweringKeyCoversRuntimeABIHeaders`; cache trim passes 20 rounds.
- Commits this round: `6f64783`, `4e0afa8`, `5fc21e9`, `0c9c918`, `193c9b4`,
  `a300078`, `6aa910a`, `5e48039`, `dd8f252`, `dce2004`, `d8b2567`,
  `dbc376a`.

Evidence commands:

- `go run ./cmd/will run tests/build/genericclassimport`
- `go test ./internal/types ./internal/gcmeta ./internal/gen ./internal/cli`

## P1-3 design notes (final containers)

- The specified surface is `list[T]`/`dict[K,V]`/`set[T]` with bracketed type
  arguments. The parser currently accepts only `<...>` for user generics.
  A probe established the needed parser path (bracket type arguments in type
  and constructor positions); it was reverted because lowering is not ready.
- `gcmeta.layoutValue` already models `list`/`dict`/`set` as one-word GC
  references. `gen` still lowers them like the old three-word `Array` value.
  Implementing the final containers therefore means a GC container object
  (class-style allocation, descriptor, roots, barriers) with a payload that
  holds data/len/cap, not aliasing `Array`.
- Completion needs: constructor, every specified method, `for`, builtin `len`,
  nesting (`list[list[i32]]`), class fields, forced cycles, cross-package;
  then delete `Array`/`Map`/`Set` and migrate std/corpus in the same series.
- Half-finished container lowering must not be committed; the probe was
  reverted and this note is the handoff.

## In progress: final list container

- HEAD `36746a3`; the worktree also carries the uncommitted list work below.
  `README.md` stays user-modified.
- Surface: `list[T]` with bracketed type arguments and constructor; the parser,
  resolve, types, willc and gcmeta bracket paths are in place.
- gcmeta synthesises a GC payload type per instance (`list[i32]`): KindClass,
  24 bytes, refs [0], fields data/len/cap. A `TestContainerPayloadType` unit
  test pins the layout and class id.
- gen lowers list as a GC payload pointer. Construction, `push` (with growth
  and memcpy), `get`, `len` work end to end: a probe returns 3 with
  `get0=1 get1=2 len=2`. The growth path pins the old data and allocates
  `cap * size` bytes.
- Still missing before it can be committed: set/insert/remove/clear/pop/clone/
  append/slice/reserve/shrink_to_fit, `for` iteration, `xs[i]`, builtin
  `len(xs)`, run/build differential, corpus, and the migration that deletes
  `Array` in the same series. Do not commit the half-migration.
- Working now: constructor, push (growth + memcpy), get, set, len, insert,
  remove, clear, clone, `for v in xs`, and `xs[i]`. Probes return the expected
  values (`get0=1 get1=2 len=2`; insert/remove 13; clone xs=2 ys=3).
- FIXED (P0, GC type registry): forced GC ran 4/4 clean after two changes in
  `internal/gen/gcmeta.go`. (1) Package mode filtered synthesised container
  types out of the registration list because `packageOfQualified("list[i32]")`
  is empty; a type without a package prefix now belongs to the emitting
  package and is registered. (2) Container/inline descriptor ids now start
  after the module-wide maximum class id instead of the package's class
  count, so a sequence descriptor can no longer take the list payload's id.
  Before the fix the runtime resolved id 1 to the sequence descriptor
  (`size=0 refs=0 flags=8`) while the payload header carried id 1; the
  collector scanned the payload as a sequence, never marked its data word, and
  the data was reused. Probe evidence: `first=1 second=2 len=2` for four
  consecutive 2500-cycle runs; `go test ./internal/gen ./internal/gcmeta`
  PASS. A gate run is in flight.
- Probe: `build/probelist`, `build/probelist2`, `build/probelistclone`,
  `build/probelistgc`, `build/probeclassgc` (all gitignored), cold
  `WILL_CACHE`.

## Known failures / blockers

1. Standard library is not complete: many P1 packages and APIs are still on the
   old surface or missing.
2. The cache-key incident is not reproduced as a deterministic test; compiler
   changes must still be validated with a cold `WILL_CACHE`.
3. Disk headroom is tight; C pagefile is large and can hide output-space
   failures.

## Next three

1. Implement the final `list[T]`/`dict[K,V]`/`set[T]` GC containers and remove
   `Array`/`Map`/`Set` per the design note above.
2. Migrate `std/sync` to the final class surface once containers exist
   (`Mutex<T>`/`RwLock<T>`/`Guard<T>`), then `std/parallel`.
3. Re-run the full run/build differential and release-build performance probe
   after the container migration; re-measure the `conc/tail` p999 on an idle
   host.

## Spec gaps (do not invent)

- `Weak<T>` has no defined construction syntax and no defined atomic upgrade
  contract; `Pinned<T>`/`Handle<T>` have no defined source entry. Recorded in
  `desc/理由/缺口清单.md`; P1-4/P1-5 are blocked on that decision.
- Generic enum construction syntax is undefined (`Enum<Variant>` placement and
  inference). gcmeta instance layout exists, but no front-end surface is added.
- `std/os/exec` is still the old `Cmd`/`Output` surface while
  `desc/标准库/进程与文件.md` specifies `run`/`start`/`Child`; migration waits
  for `std/io` Reader/Writer and pipe support.

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
