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

- LIST SURFACE COMMITTED: `b0980b6 feat: add the final list container` adds
  list[T] with the full specified method set, the callback ABI, `for`,
  indexing, builtin `len`, the package-mode type registration and descriptor
  id fixes, and two CLI tests. HEAD is `b0980b6`; the worktree had only the
  user's `README.md` change after that commit.
- Array migration attempt (round 15): a temporary tool
  (`build/retype/main.go`) rewrote 121 `.will` files, replacing `Array<T>`
  with `list[T]` and `new Array<T>(n)` with `list[T](n)`, and the corpus was
  re-recorded. It does not build yet, so the diff was saved to
  `build/array-migration.patch` (921 KB) and the tree was restored with
  `git checkout -- std tests bench`. The failure classes, all reproducible
  from the patch:
  1. C shims still expect the three-word Array handle: `raw(self.fds)` in
     `std/io/poller.will` and the exec output arrays pass a list payload
     pointer where C reads data/len/cap. The C boundary needs a view-based or
     pinned-buffer protocol before this migration can proceed.
  2. `list` field layout fails for `list[rc<Inner>]` and similar old rc
     element types: the layout error names `...list` without arguments. The
     rc corpora (`build/rcarray*`) are deprecated and should be deleted rather
     than kept working.
  3. Builtin conversions `str(list[u8])` / `bytes(list[u8])` are not lowered
     (`tests/build/arraytostr`, `tests/build/strbytes`).
  4. `xs[a:b]` slice syntax has no list lowering; only the `slice` method is
     implemented (std/time path hits it).
  5. `std/time/format.will` shadow warnings (N0003) are now visible on a cold
     cache and break run tests that expect empty stdout; rename the locals.
- Migration blockers closed this round (commits `b820c46`, `1d0c63e`,
  `c86e7c2`): `list[lo:hi]` / `list[lo:]` slice syntax is lowered with
  bounds checks and covered by `TestRunListSliceSyntax`; the rc container
  corpora were deleted; `std/time/format.will` no longer shadows
  `local`/`date` (N0003 warnings no longer leak into run output). Corpora
  were re-recorded, including genericasync/multiasync because container
  descriptor ids changed.
- Layout fix (commit `e9c7922`): `internal/layout` now treats `list`/`dict`/
  `set` as predeclared generic types with a one-word reference layout, and
  `fieldLLVMType` resolves a class-like field text to `ptr` before struct
  lookup. Replaying the retype tool on top of this fix makes `tests/std/io`
  compile (it previously failed with `the layout of type std/io.list`).
  The current migration diff is saved as `build/array-migration-v2.patch`
  (281 KB) and the tree was restored to the non-migrated surface.
- Still open for the Array migration: `tests/std/exec` now fails with
  `key is an aggregate, not a scalar` (node #43), `str(list[u8])` is not
  lowered, `view.clone()` still returns `Array<u8>`, and the C boundary
  protocol (poller/exec) is unchanged.
- Still open for the Array migration: the C boundary protocol (view buffer
  instead of the three-word handle for `std/io/poller` and `std/os/exec`),
  builtin `str(list[u8])` / `bytes(list[u8])`, and `view.clone()` returning
  `Array<u8>` instead of `list[u8]`. The saved migration diff is
  `build/array-migration.patch`.

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
- Tests added: `TestRunListMethods` (returns 54; the whole specified surface,
  including `map`/`filter`/`fold`/`find`/`any`/`all`) and
  `TestRunListForcedCycles` (2500 forced cycles, returns 3) in
  `internal/cli/run_list_test.go`; both pass. A gate run is in flight before
  the list surface is committed as its own feature; deleting `Array` follows
  in the next commits.
- Working now: the full specified list method set: push, get, set, len,
  insert, remove, clear, clone, reserve, shrink_to_fit, append, slice, pop,
  any, all, filter, find, map, fold; builtin `len(xs)`, `for v in xs`,
  `xs[i]`. Probes: 9 cases pass, including `map`/`fold`/`find`
  (`sum=12 total=6 found=2 missing=100`, exit 20) and the forced-cycle
  2500-round case (`first=1 second=2 len=2`).
- `list.go` (414 lines), `listmethods.go` (444), `listfuncs.go` (503) are
  split by responsibility to stay under the 800-line rule; the nine probes
  pass after the split.
- Callback ABI: a function value is `{ ptr code, ptr env }`; the lowering
  calls `code(env, ...)` and reads the callback result type from the lambda
  record (its `type` field), not from a call type argument.
- Probes live in `build/probelist*` (gitignored); cold `WILL_CACHE`.
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

## In progress: dict and set (uncommitted)

- Spec gap recorded first: `desc/理由/缺口清单.md` now has the bytes/view to
  `str` conversion gap, because the Array-to-list migration of std/time and
  the arraytostr corpus depends on the old `str(Array<u8>)` builtin. That
  migration stays paused until the conversion is specified.
- Front end is in place: `dict[K,V]` / `set[T]` are predeclared types with
  arity 2/1, bracket constructors, and checker methods (get/set/has/delete/
  clear/keys/values/add). `will check` passes on a dict+set probe.
- FIXED (uncommitted): the exit hang was a deep-release loop, not GC. The
  `_ = d` path sent `dict[i32,i32]` through `releaseOwnedValue` ->
  `releaseEntries`, which treated the pointer variable slot as the old
  three-word Map handle and looped over a garbage length. `isFinalContainerText`
  now skips list/dict/set in that release path. With a cold cache, the traced
  dict probe exits 0, `dict[i32,i32](4)` returns 10, and `set[i32](4)`
  returns 1. `dict[str,i32]` works too (set overwrite, has, delete, len).
  `keys()`/`values()` now build a fresh `list` for dict and are tested.
- New CLI tests `TestRunDictScalarKeys` (38), `TestRunDictStrKeys` (14),
  `TestRunSetAddHasDelete` (0) and `TestRunDictSetEach` (prints 1=10, 2=20,
  v=3, v=4) all pass with a cold cache. The dict/set core is committed as
  `cc2bd62`; the `each` callbacks (dict f(K,V) and set f(T)) are implemented
  and tested but still uncommitted. `for` over a dict/set is not specified
  (iteration order is unspecified; each is the specified way).
- Uncommitted files: `internal/gen/{dictset.go,container.go,gen.go,
  genericstruct.go,len.go,list.go,new.go}`, `internal/{resolve,types,willc}`
  changes.
- Next: debug the exit hang (print-free run of the dict probe under a job
  timeout, then instrument `willrt_gc_pin`/`unpin` balance or the shutdown
  join), then str-key hashing, then `for`/`each`/keys/values, tests and
  corpus.

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

## Map/Set migration attempt (round 23, uncommitted)

- Tool `build/retypemap/main.go` rewrote 11 files (`Map<` -> `dict[`,
  `Set<` -> `set[`, removing `new`). Three failure classes; tree restored and
  the diff saved as `build/mapset-migration.patch` (25 KB).
  1. `tests/std/os/main.will` declares `var set: Result<(), Error>`, which now
     shadows the predeclared `set` type (N0003); rename the variable.
  2. `var keys: Array<i32> = m.keys()` must become `list[...]` because dict
     keys/values return list; the stale Array declaration makes gen report
     "the call result is not an aggregate".
  3. `std/task/task.will:20:28` and `tests/build/deferdeep` report O0002
     "a copy is required here and cannot be proven" on `out.push(await
     task!)`; the mover/generic instance rule for the final containers is
     still open.
- HEAD is `e83f8ab` (dict/set each). Next: fix the three classes, re-apply
  the patch, re-record, then delete Map/Set.
## Map/Set migration progress (round 24, uncommitted)

- Re-ran `build/retypemap` (11 files). Fixed two classes: renamed the `var
  set` local in `tests/std/os/main.will` to `result`; changed the four
  `var keys/values: Array<...>` declarations to `list[...]`.
- Split the type recognizers: `mapTypeText`/`setTypeText` now only match the
  old `Map<`/`Set<`, while `dictTypeText`/`finalSetTypeText` match the final
  bracket forms. `containerMethod`/`containerCallResult` normalize a dict/set
  receiver to the old spelling for their shared entry logic and keep an
  `isDictRecv` flag for list-returning keys/values. This fixed the return
  type and layout paths that treated dict as the three-word handle.
- Compiles now: `tests/std/os`, `tests/std/container/map`, `tests/build/map`.
- Still failing: `std/task/task.will:20:28` and `tests/build/deferdeep:36:13`
  report O0002 "a copy is required here and cannot be proven". The mover's
  copy rule for a final-container reference is the remaining blocker; the
  check/move.go placeCopy path needs to treat dict/set (and list) as
  non-owning references the same way `ast.OwnsHeap` already does.
- The tree is NOT committed; gate would fail on the two O0002 cases and the
  corpora are not re-recorded yet.
## P0: cold-cache gate fails on an existing corpus (round 25)

- A clean build of HEAD (`98d45e2`) in a temporary worktree, run on the
  unmodified `tests/build/deferdeep/main.will`, reports
  `main.will:36:13: O0002 a copy is required here and cannot be proven`
  (`xs.push(inner)` where `inner: Array<i32>` is used again afterwards).
  The same failure appears for `std/task/task.will:20:28`
  (`out.push(await task!)` with an unbound generic U).
- Therefore the earlier `gate: ok` results were **cache artifacts**: a warm
  `WILL_CACHE` serves the `.willc` and skips the check pass. The gate must be
  verified with a cold cache; a cold-cache `gate -full` is expected to fail
  until these two O0002 cases are fixed.
- Decisions to make: the deferdeep corpus should clone before pushing an
  Array that is used again (corpus bug), while the generic-U push in
  std/task needs a mover rule for unbound type parameters (conservative
  owning currently forces a Clone that generic code cannot express). Both are
  P0 for gate trustworthiness.
## Round 26 status

- Cold-cache `gate -full` reported 6 failing corpora (build/map among them) because
  the Map/Set migration changed the emitted IR and the goldens are not
  re-recorded; this is expected during the migration, not a code failure. The
  report also raced with an in-flight debug edit (transient `undefined: fmt`);
  the debug print and its imports were removed and `go build ./...` is clean.
- deferdeep was fixed as a corpus bug (`xs.push(inner.clone())`, Array is
  owning) and now compiles clean.
- `std/task/task.will:20:28` still reports O0002. The placeMoves last-use
  check and the `await task!` path were instrumented; the diagnostic does not
  come from that loop, so the remaining entry is still unidentified. Do not
  commit until it is fixed with evidence.
- Compiler changes in the worktree: `check/move.go` (skip copying a task that
  `await ...!` consumes), `check/ownership.go` (list/dict/set are non-owning
  references), `gen/container.go` (Map/Set vs dict/set recognizers).
## O0002 root cause narrowed (round 27)

- Instrumented `copyAt` with `runtime.Caller`: the std/task O0002 comes from
  `move.go` IndexExpr branch (`m.copyAt(x.Pos())` after `IndexType`), not from
  placeMoves. It fires for `await task!` at task.will:20:28.
- Tried skipping the copy when IndexExpr.Recv is a Name task (via `taskVars`),
  when Recv is Task-typed, and when Recv is Await/Unwrap/Try; none matched the
  actual AST shape, so the diagnostic remains. The debug instrumentation was
  removed again and the binary rebuilt.
- Next: print the AST node type of the placeCopy argument at that position
  (`fmt.Sprintf("%T", e)`) to learn how `await task!` is represented, then fix
  that case and add a move test. Do not commit until green.
## O0002 fixed; a deeper P1 surfaced (round 28)

- Root cause of the std/task O0002: `var value: T = xs[i]` copies the view
  element. `xs: []T` has element type T (an unbound type parameter), which the
  mover conservatively treats as owning, so it demanded a Clone. Fix: in the
  IndexExpr place-copy path, skip the copy when the element type is a type
  parameter (`m.own.IsTypeParam`); the instance should be checked after
  monomorphization. deferdeep's clone and all Map/Set corpora still pass.
- With O0002 gone, `tests/std/task` now reaches gen and fails with
  `array element U is not lowered yet`. Evidence: `emit-willc
  std/task/task.will` contains only the generic prototype
  (`func parallel_map type="Array<U>"`) and the call site is
  `to="std/task.parallel_map"` with no instance name. gen lowers the
  prototype, so U has no layout.
- This is a real P1: **generic function monomorphization is not implemented**
  for cross-package calls. It predates the Map/Set migration and was masked by
  the O0002 error. The migration tree cannot pass a cold-cache gate until
  monomorphization exists, so nothing is committed this round.
- Open spec question: the mover's "unbound type parameter is owning" rule
  conflicts with generic bodies like `out.push(await task!)`; the definition
  can only be checked per instance. Recorded here before changing the rule.
## Monomorphization investigation (round 29)

- The pipeline already has the machinery: `planInstances` scans calls,
  `genericTarget` finds a generic function in any package and infers the type
  arguments, `cloneInstance` clones the function subtree with
  `substituteTypeText`, and `funcDecl` skips type-parameter declarations
  (`gen.go:1274`) so only instances are lowered.
- `substituteTypeText` replaces identifiers inside `Array<U>` and nested
  types, and `cloneFuncInstance` drops typeparam records, so the remaining
  failure (`array element U is not lowered`) is in an instance document or in
  the argument inference path, not in the prototype lowering. The exact
  residue is not yet located.
- Next: dump the cloned instance's records (temporary `emit-willc` of the
  planned docs or a debug print in `cloneInstance`) and find which record
  still spells `U`; then fix the substitution or inference and re-run the
  cold-cache gate.
## Async generic monomorphization defect (round 30)

- Reproduced with a self-contained probe (`build/probegen4`): a plain
  `def mapall<T,U>(xs: []T, f: func(T)->U) -> Array<U>` instance lowers and runs
  (returns 8), and a cross-package generic call (`probegen2`) runs (returns 5),
  but the same function body written as `async def amap<T,U>(...)->Array<U>`
  with `spawn`/`await` fails with `array element U is not lowered`.
- Debug prints in `cloneFuncInstance` showed no clone failure, so the instance
  document is created; the residue is an async-specific record whose type text
  is still the bare U (likely a state-machine/suspend record whose typeparam
  parent is not the declared function, so `subst` misses it). Instrumentation
  was removed again; `go build ./...` is clean.
- Next: dump the cloned async instance's records and find the bare-U text,
  then substitute it (or fix the typeparam parent walk) and rerun the probe.
## Generic call result bug fixed; async state machine defect remains (round 31)

- Real bug found and fixed in `genericCallResult`: it used `substituteText`,
  which only replaces a text that is exactly a type parameter, so the call
  result `Array<U>` stayed `Array<U>` at the call site. It now builds a
  substitution map and uses `substituteTypeText` (recursive identifier
  replacement), matching `cloneFuncInstance`. Evidence: `build/probegen4`
  moved from `array element U is not lowered` to LLVM verification output.
- After the fix the async generic probe now fails LLVM verification with
  `Instruction does not dominate all uses!` (a load inside the loop feeds a
  store outside it). `tests/std/task` instead reports
  `the call result has no place` for node #80. Both are async-generic state
  machine lowering defects, deeper than the type substitution.
- Debug instrumentation was removed; `go build ./...` is clean. Not committed:
  the async lowering must be fixed and the cold-cache gate must pass first.
## Async generic and migration corpora green (round 32)

- Two fixes: (1) `genericCallResult` now substitutes recursively (previous
  round); (2) `generalCallWaitPlace` delegates void children
  (text "" or "()") to `generalCallWait`, so an awaited void call no longer
  reports "the call result has no place".
- `tests/std/exec` now uses dict keys/values (list-returning) instead of the
  unsupported `for k, v in dict`; `std/os/exec/cmd.will` copies env with
  `each`. Both compile.
- Compile-clean set with a cold cache: tests/std/task, tests/std/os,
  tests/std/io, tests/std/exec, tests/build/map, tests/build/deferdeep,
  tests/std/container/map. Remaining known issue: the self-contained async
  generic probe `build/probegen4` still hits an LLVM dominance error in the
  state machine after the type fixes; task corpora do not.
- Next: cold `gate -full -update`, re-record, confirm, then commit the
  Map/Set migration plus the compiler fixes (move/ownership/generic/container
  /statemachine).
## Map/Set migration landed (round 33, cold-cache gate green)

- Cold-cache `gate -full` is green. Commits:
  - `78446bd fix: allow generic moves for type-parameter elements`
  - `87bfca4 fix: substitute generic call results and join void async children`
  - `c790785 feat: lower dict and set method calls and loops`
  - `132b687 refactor: migrate std and corpora from Map/Set to dict/set`
- All std and corpus uses now spell dict[...]/set[...]; keys/values return
  list; exec copies env with each or keys/values; corpora re-recorded.
- Next: delete the old Map/Set names from the checker, gen (mapTypeText/
  setTypeText/container.go old handle paths), gcmeta layout, and the old
  rc-map/rc-set corpora, then re-record and re-run the cold gate.
## Old Map/Set names removed from the front end (round 33, uncommitted)

- resolve predeclared types, types builtin arity, and the infer container
  branches no longer accept `Map`/`Set`; only `list`/`dict`/`set` remain.
  `go build ./...` is clean.
- `go test ./internal/resolve` now fails: its fixture source still spells the
  removed names at test.will:15 (N0001). The fixture must be updated to
  dict/set before this lands; the cold gate currently fails on it.
- Remaining cleanup after that: gen's old Map/Set handle paths, gcmeta layout
  branches, and the rc-map/rc-set corpora.
## Old Map/Set names removed (round 34, gate green)

- resolve predeclared types, types builtin arity, and infer container
  branches now accept only list/dict/set; Array stays (migration blocked).
  CLI container tests were updated to dict/set.
- Cold-cache `gate -full` is green again.
- Next: delete gen's old Map/Set handle paths, gcmeta layout branches, and
  rc-map/rc-set corpora.
## Dead Map/Set code removed (round 35)

- `deepclone.go` lost the old handle clone (cloneMapValue, cloneSetValue,
  cloneEntries) and releaseEntries; final containers are shared references
  in the value clone, the field clone check, and the release path. Cold-cache
  `gate -full` is green after the removal.
## Remaining old Map/Set code paths removed (round 36)

- share.go, rc.go, gc_container.go, and iterate.go no longer recognize the
  removed Map/Set spellings; shareEntries was dead and is deleted; the GC
  container test uses dict/set. Cold-cache `gate -full` is green.
- New spec gap recorded: the C boundary for containers/views (std/io
  poller and std/os/exec pass handle addresses to C). Until the spec defines
  the supported form, those packages are not touched by the Array migration.
- rc/arc still appear in 55/15 will files (std/sync, std/test, corpora);
  replacing them with GC references is the next large deprecated-surface
  migration.
## Borrowed generic class fields (round 37)

- Fixed `gcmeta/nested.go`: a field spelled `mut Box<T>` reached the nested
  layout resolver with the borrow prefix and was qualified as a package type
  (`type mut pkg.Box<i32> is not declared`). A borrowed field is now laid out
  as one word before canonicalizing the pointee; probe `build/probegenclass2`
  advanced to the next error.
- The fix changes the GC layout of borrowed fields, so 16 corpus IR files
  differ; `gate -full -update` is re-recording them in the background and the
  result is not yet confirmed. HEAD already contains the nested.go fix.
- Next: field access still uses the `mut pkg.Box<i32>` text when looking up
  the class fields (`has no fields`), so the checker/gen must strip the
  borrow prefix in field access. Then re-run the generic-class probe, confirm
  the cold gate, and continue the std/sync class migration (P1-9).
## Borrowed class field lookup fixed (round 38)

- `classTypeOfText` no longer rejects a borrowed text: it resolves the
  pointee after stripping the borrow prefix. This cleared `has no fields`
  for `self.box` where the field is `mut Box<T>`; corpora were re-recorded
  and the cold-cache gate is green.
- The generic-class probe (`build/probegenclass2`) now fails at
  `Holder<T>.init` instantiating `new Box<T>(value)`: the instance still
  spells Box<T> with T unbound. Root cause: generic class method/init
  instances do not substitute type parameters inside nested generic
  constructions. This is P1-1's remaining half before std/sync can move to
  class Mutex.
## Generic class init prototype lowered (round 38 finding)

- `emit-willc build/probegenclass2/main.will` shows the `new` record as
  `type="Box<T>"`; `initDeclForNew` correctly collects the class type
  parameters and `planInitInstances` clones an init instance with them.
- The error `type T is not lowered` therefore means the **prototype**
  `Holder<T>.init` is being lowered as well: `hasTypeParams(init)` is false
  because the type parameter belongs to the class, and `genericMethodDecl`
  apparently does not recognize the `mut Holder<T>` receiver. The skip in
  `funcDecl` (gen.go:1274) must treat a method of a generic class as
  prototype-only.
- Next: fix that recognition, then re-run the probe and the cold gate.
## Generic class nested construction fixed (round 39)

- `planInitInstances` cloned an init instance for `new Box<T>(value)` inside
  the generic declaration (type argument still T), which failed with
  `type T is not lowered`. Constructions whose argument is a declaration type
  parameter are skipped; the concrete Holder<i32>.init instance still clones.
- Probe `build/probegenclass2` returns 9; cold-cache `gate -full` is green.
- The generic-class surface (`class Box<T>`, borrowed generic fields, nested
  generic construction) is now usable, which unblocks migrating std/sync from
  struct+arc to class Mutex/RwLock (P1-9).