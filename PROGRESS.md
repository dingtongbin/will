# will progress

Single progress and task-queue file. Facts and evidence only; the specification in
`desc/` wins on any conflict. Last updated: 2026-10-30T18:40+08:00.

## State

- Branch: `feature/package-mechanism`; HEAD `cf4ec5e`; 446 commits ahead of
  `main`. The worktree is dirty with the deprecated-surface purge; the user's
  `README.md`, `std/container/stack/stack.will`, `std/parallel/`, and
  `tests/std/parallel/` changes are preserved.
- Last full cold `gate -full`: PASS at the end of round 239 after the list
  migration and corpus re-record. The gate is no longer red.
- Toolchain: go1.25.9 windows/amd64; clang 22.1.8 at
  `C:\msys64\clang64\bin\clang.exe`.
- Fixed env: `WILL_RUNTIME=D:\githubproject\will\runtime`,
  `WILL_CLANG` above, `WILL_CACHE` under the opencode temp directory.
- Phase: P0 list/class GC roots are green; M4 language surface / M5 std
  migration continues. P0-1..P0-5 and P1-6 have close-out records in
  `desc/理由/最终目标差距与依赖链.md`; generic class monomorphization (P1-1)
  works end to end.
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
## std/sync class migration in progress (round 40, uncommitted)

- `std/sync/mutex.will` was rewritten: `class MutexState<T>` with init, `class
  Mutex<T>`/`class RwLock<T>` holding a class reference, `struct Guard<T>`/
  `struct ReadGuard<T>` with a `MutexState<T>` reference field; the arc and
  share() surface is gone.
- `gen/guard.go` now resolves the guard state through `classTypeOfText` +
  `ensureClassPayload` (class reference) instead of the rc handle, in both
  guardValue and guardUnlock.
- The probe `tests/std/sync` (new module with require std) still fails:
  `the guard state is not lowered yet` for the `g.value` forwarding path.
  Debug prints from the earlier generic investigation are still visible in
  the output (clone-fn ...), so generic.go still needs a cleanup check.
- Not committed: the probe must pass, then the cold gate and the sync corpus
  recording.
## std/sync class migration works (round 41, uncommitted)

- Probe `tests/std/sync` returns 7: `class Mutex<i32>` construction, a
  blocking guard, `g.value = 7` write-through, and read-back all work.
- Fixes this round: `ensureClassPayload` now registers the class payload
  structInfo (classPayloadInfo) so guard field lookup finds `lock`/`value`;
  `guardStateInfo` resolves a class reference instead of an rc handle.
- Guard debug prints and the os import are still in guard.go (removal failed
  in the shell); the next step is to remove them, run the full tests and cold
  gate, record the sync corpus, and commit class.go/guard.go/mutex.will/
  tests/std/sync.
## std/sync class migration blocked on Guard return (round 42)

- Probe tests/std/sync returns 7, but the existing `concurrency/syncmutex`
  corpus now fails with `an aggregate is a place, not a scalar value`
  (node #62): a `return Guard<T> { state: ..., held: ..., mode: ... }` inside
  the class-method instance reaches `g.value` for the aggregate literal.
  The rc version of Mutex took the place path; the class-method instance
  substitution changed which path runs.
- `TestRunCpuSyncAcrossJobs` used the removed `share()`; it now passes `mu`
  directly and the test is green. `TestRunInterfaceBoxAndClone` passes when
  run alone.
- guard.go debug prints/import and classPayloadInfo registration are in the
  worktree (uncommitted). Cold gate currently fails on the syncmutex corpus.
## Class constructor aggregate argument (round 43)

- The syncmutex failure is now pinpointed: node #62 is
  `new sync.Mutex<Counter>(Counter { n: 0 })` at main.will:24:58. `classNew`
  lowers init arguments through `callArgParts` with
  `g.paramTypes[initSym]`; for the generic class instance the init symbol
  resolves to the prototype signature (`T`), so the aggregate argument is
  sent through `g.value` and trips "an aggregate is a place".
- Next: resolve the init signature of the concrete class instance (replace T
  with Counter) so the aggregate argument goes through the place path.
- Debug prints were added in gen.go (ret/agg-value) and must be removed
  before committing; the worktree is not committed and the cold gate is red
  on the syncmutex corpus.
## Class constructor aggregate argument still open (round 44)

- The debug prints in gen.go/class.go were removed and the tree builds.
- classNew now tries to fill the init parameter types from the class
  declaration (searching every document and substituting the instance type
  arguments) when `g.paramTypes[initSym]` is empty. The syncmutex probe still
  reports `an aggregate is a place` at `new Mutex<Counter>(Counter { n: 0 })`,
  so the filled types do not reach `callArgParts` (or the lookup still
  resolves the prototype).
- Next: print the params actually passed to `callArgParts` for that call and
  whether initDeclForNew succeeds for the std/sync document, then fix the
  type substitution. Not committed; cold gate red on syncmutex.
## Class init instance symbol (round 45)

- classNew now resolves the concrete init through `initForNew` over every
  document and passes its substituted params; planInitInstances mangles an
  init instance as `MutexState<Counter>.init` and searches all documents for
  the declaration. The aggregate argument error is gone (the call now has the
  right shape).
- Remaining: LLVM reports `use of undefined value
  '@std/sync.MutexState<Counter>.init'`, so the instance document is not
  emitted (or its symbol differs). Same-file nested generic construction
  (probegenclass2) works, so this is specific to a cloned instance whose new
  record lives in another document; the next check is the clone doc path and
  hex type text when initDeclForNew runs on the cloned new.
## Init instance cloned but not emitted (round 46)

- planInitInstances now clones all three instances (Mutex<Counter>.init,
  RwLock<Counter>.init, MutexState<Counter>.init, path=std/sync) as shown by
  temporary prints.
- The module still reports `use of undefined value
  '@std/sync.MutexState<Counter>.init'`: entry owns instances
  (`emits` allows `d.clone` documents in the instance owner), yet the init
  definition is missing from the entry module. The likely skip is funcDecl's
  prototype guard or emitDoc's func list for a cloned document whose class
  declaration lives elsewhere.
- Debug prints (init-clone/fail in generic.go) are in the worktree and must be
  removed before committing.
## std/sync class surface landed (round 47)

- Commit bb71b16: `class Mutex<T>`/`class RwLock<T>` with a shared
  `class MutexState<T>`, `struct Guard<T>`/`ReadGuard<T>` holding the state
  reference; guard lowering resolves the state via the class payload; class
  constructions with aggregate arguments use place values; generic class
  inits resolve through initForNew across documents; a cloned init instance
  keeps its declared name (the earlier mangling duplicated the receiver).
- tests/std/sync returns 7, concurrency/syncmutex passes, corpus re-recorded,
  cold-cache gate -full is green.
- Next: migrate std/test (arc<TestState>) the same way, then the remaining
  rc/arc corpora and the rc/arc compiler surface.
## std/test class migration started (round 48, uncommitted)

- std/test/test.will: TestState is now a class with an init; T and caseResult
  store a TestState reference; new_t constructs it with new TestState(...);
  run_case uses the reference. No arc text remains in the file.
- Compiling tests/std/test now reports two checker errors:
  1. `test/test.will:15:21 O0002 a copy is required here` on the T.state
     field declaration (the message position points at the field, not the
     construction);
  2. `test/test.will:205:39 T0016 this value cannot cross a task boundary` on
     spawn with a T that carries a class reference. The spec allows sharing a
     GC reference across tasks (with Mutex/atomics/channels), so the Send
     analysis must accept class fields.
- Next: check the checker's Send/Sync rule for class fields and the O0002
  position for a struct literal that stores a class reference; then rerun the
  test corpora and the cold gate.
## std/test migration: Send fixed, O0002 remains (round 50)

- `sendsync.go` now returns true for a class type in both namedSend and
  namedSync (GC reference shared across tasks).
- `std/test/test.will` copies each Case into an owned local before spawn
  (`var spec: Case = cases.get(usize(i))`), which cleared T0016.
- Only `test/test.will:15:21 O0002 a copy is required here` remains; the
  position points at the `state: TestState` field declaration. The move pass
  treats a struct whose fields include an owned field (T also has `name: str`)
  as owning; the class reference itself should not need a clone. Next: trace
  which move rule reports at that position (placeCopy caller) once the shell
  quoting is sorted, or exempt class reference fields from the owning walk.
- Not committed: check tests/std/test still fails on O0002.
## std/test: check passes, IR dominance error (round 51)

- The O0002 was a correct diagnostic: TestState.init stored the borrowed `logs`
  parameter into the field, so the fix is `self.logs = logs.clone()` in std.
  Debug prints in move.go were removed; `check tests/std/test` now passes.
- Running tests/std/test fails LLVM verification: `Instruction does not
  dominate all uses` around `%t924 = trunc i64 ...` / `%t926 = icmp slt`,
  in the parallel/region part. This appeared after the class migration and the
  owned-Case-before-spawn change; next is to inspect the IR around those
  temporaries (likely a value computed in one branch and used after the join).
- Worktree (uncommitted): sendsync.go class rule, std/test/test.will class
  migration, move.go clean.
## Range bound dominance fixed; next async-loop dominance (round 52)

- Fixed the Range loop lowering: the end bound is evaluated once and stored
  in a temp alloca, and the condition loads it. A bare SSA bound did not
  dominate the condition because the back edge bypasses the preheader. The
  first `trunc/icmp` dominance error in std/test is gone.
- The next LLVM error is the same class inside the async loop:
  `%t985 = getelementptr struct caseResult, ptr %t983, i64 %t984` used by
  `%t1030 = getelementptr i8, ptr %t985` after the loop. The await
  continuation of the loop body still refers to body-local SSA temporaries;
  they need frame slots, not SSA values.
- Uncommitted: gen.go Range slot, sendsync class rule, std/test migration,
  move.go clean.
## std/test class surface landed (round 53)

- Commits e4892f2 (Range end bound in a slot; corpora re-recorded),
  1368a4d (class references are Send/Sync), d1e7ae6 (std/test class surface:
  TestState class with clone in init, T/caseResult hold the reference, Case
  copied before spawn, awaited result bound before push).
- tests/std/test runs (prints RR), corpus re-recorded, cold-cache gate -full
  is green.
- std/sync and std/test are now on the final class surface. Remaining rc/arc
  text lives in ~55/15 corpus files and in the compiler rc surface; that is
  the next deprecation batch.
## rc/arc corpus cleanup (round 54, uncommitted)

- Deleted the pure rc-surface corpora: build/{rc,rccopy,rcdrop,rcgraph,rctask,
  takec,weakzero} and check/{crosstaskrc,crosstaskrcmethod} (9 directories,
  staged with git rm).
- defer corpus: `rc<C>` replaced by the plain C value.
- csv corpus: Buffer is a class with an init that clones the data buffer;
  Sink holds a Buffer reference; compiles.
- buf corpus: Stats is a class with an init; `new Stats(0, 0usize, 0usize)`.
  It now fails with `layout of type io.Reader is not known` for
  `ReaderBuf.src` (an interface field). That error was not present before the
  Stats class change; next is to check whether the class payload info
  registration interferes with interface field layout, or whether the
  interface field needs its own gcmeta layout path.
- Not committed: buf still red, so the cold gate would fail.
## buf interface layout still open (round 55)

- The buf failure is a real cross-package interface layout gap: layout.Layout
  gets `io.Reader` from ReaderBuf.src and does not know it. Added a bare-name
  fallback in layout.Layout and import-alias expansion in gcmeta.resolveDecl;
  buf still fails, so the interface declaration itself is not registered in
  the layout resolver (the test module's decisions likely only collect its own
  interfaces, not std/io's Reader).
- Corpora moved so far: defer (C value), csv (Buffer class with clone). The
  nine pure rc corpora were deleted with git rm.
- Next: register imported interfaces in the layout resolver (or give an
  interface field the two-word layout from gcmeta), then rerun buf, defer,
  csv, the cold gate, and commit the rc cleanup.
## rc corpora cleared; alias layout fixed (round 56)

- Commit 073bbee: layout resolves an import-alias type text by matching the
  registered qualified name suffix; gcmeta expands import aliases before a
  declaration lookup. std/io/buf (io.Reader field) now compiles.
- Commit 868702f: the nine pure rc corpora are deleted; defer uses a plain C
  value, csv Buffer and buf Stats are classes; defer IR re-recorded.
- Cold-cache gate -full is green; no will source uses rc/arc/weak_rc/take_c.
- Next: delete the compiler rc surface (internal/gen/rc.go, checker/type rc
  branches, take_c predeclared values, rc diagnostics) and the rc runtime
  objects if unreferenced, re-recording corpora and re-running the gate.
## Internal rc branches removed (round 58)

- Commit ada5313 removed rc/arc/weak_rc/weak_arc/take_c from the predeclared
  surface (resolve/types) and deleted the dedicated tests; the Send/Sync
  structural test now uses a class reference.
- This round removed the rc/arc branches in gcmeta nested layout, layout
  resolver, ownership, and move checks, plus the layout/move tests that
  exercised rc. Cold-cache gate -full is green.
- Remaining rc dead code is concentrated in internal/gen (rc.go and rc
  branches of the owned/release/clone helpers); removing it needs the helpers
  refactored, which is the next cleanup item.
## rc.go cleanup needs a refactor (round 59)

- Deleting strongName/weakName from gen/rc.go broke new.go and rc.go itself;
  rc.go is not a standalone dead unit (strongName is used by new.go's rc
  construction path, and constructRc/rcMethodCall sit inside the
  needsRelease/retain helpers). The file was restored; gen builds.
- A safe removal needs the owned/release/clone helpers rewritten to drop
  their rc branches and the rc call sites in adt/array/arrayliteral/
  arraymethods/container/deepclone/defer/gen/new/struct/task removed first.
  That is a multi-step refactor, not a mechanical delete.
- Next: either do that refactor (compiler-internal cleanliness) or the
  higher-priority C-boundary item: exec/poller pass `raw(array)` of str
  arrays (GC references) to C (exec.will:33/55, poller.will:48). The spec says
  a container payload with GC references must not be exposed to C beyond the
  call; exec keeps args/envpairs for the whole child run, so the C side must
  copy them at call entry (gap recorded in desc/理由/缺口清单.md).
## exec C boundary is a real UAF (round 60)

- std/os/exec/exec.c: `willrt_exec_run_async` stores will GC pointers
  (path/args/env/dir) in an exec_async_run and the blocking worker reads them
  on another thread. `willrt_exec_run_async_start` only copies the struct, not
  the referenced strings, so the blocking thread reads GC memory that the
  caller may have released or moved. This violates C interop section 7.8 and
  is a use-after-free, not only a spec gap.
- The fix is to deep-copy path/args/env/dir into C memory at the call site
  (exec_copy each exec_str), hand the owned copy to the worker, and free it
  when the worker finishes. This belongs to the C boundary batch.
- poller.will's `raw(self.fds)`/`raw(out)` passes Array<usize> payloads (no GC
  references) only for the call duration, which the auto-pin covers; exec is
  the one that keeps them past the call.
## exec C boundary fixed (round 60)

- Commit: exec.c deep-copies path/args/env/dir into C memory at the call site
  (exec_owned_copy) and the blocking worker frees the copy; the *_start and
  _async forms both pass the owned copy through willrt_blocking_submit_copy.
  This removes the cross-thread read of will GC memory (UAF) in exec.
- tests/std/os runs; cold-cache gate -full is green.
- Leftover: the old exec_async_run/exec_async_output structs and
  exec_async_wait in exec.c are unused now and should be deleted next.
## std package matrix gap (round 61)

Missing from the package table (desc/标准库/总览.md) versus std/:
- std/encoding/json (decoder + @derive(Json))
- std/io/pipe (Pipe with reader/writer ends, close wakes waiters)
- std/parallel (map/reduce/for_each CPU job chunking)
Everything else in the table exists (errors, fmt, str, bytes, container/*,
bits, math, math/rand, time, io, io/buf, os, os/fs, os/path, os/exec, sync,
task, gc, test, encoding/{base64,csv,hex,url}, hash/{crc32,fnv}).

chan API confirmed from corpora: new chan<T>(cap), await ch.send(v),
await ch.recv() -> Option<T> (None after close), ch.close().

bytes/view -> str stays a recorded spec gap (desc/理由/缺口清单.md section),
so Array->list migration remains paused there by decision, not by an
implementation choice.

Next: implement std/io/pipe (PipeState class with two notification channels,
PipeReader/PipeWriter implementing io.Reader/io.Writer), then
std/parallel, then json.
## std/io/pipe written, blocked by async-loop SSA (round 62, uncommitted)

- std/io/pipe/pipe.will: PipeState class (list[u8] buffer, capacity, two
  chan<bool> notification channels, closed flags), PipeReader/PipeWriter
  implementing io.Reader/io.Writer, Pipe pair, pipe(capacity), close on both
  ends. tests/std/pipe (require std) exercises write/close/read/EOF.
- The corpus fails LLVM verification with the same dominance class as
  std/test: inside an async loop, a value computed before an await (the
  buffer load/gep) is used by the continuation, which the state machine does
  not keep in a frame slot. Storing the closed flags in locals did not fix
  it; the remaining value is the buffer/len load itself.
- This is a compiler P1: the async loop state machine must spill body values
  that live across an await into frame slots (or re-load them in the
  continuation). std/io/pipe cannot run until that is fixed.
## pipe: await point needs a place-cache reset (round 63)

- Storing the two closed flags and the receiver in locals (`Byte`, `taken`,
  `st`) did not clear the dominance error. The failing values are loads of the
  state reference and its length (`load ptr, ptr %t326`,
  `load i64, ptr %t328`) computed in the await-preceding block and reused by
  the continuation.
- Cause: gen caches a computed place (GEP/load address) and the state machine
  reuses that SSA pointer in a later step block; the pointer's definition does
  not dominate the step block. A fix must invalidate the place cache at an
  await point (or spill the addresses into frame slots), in the state machine
  lowering, not in std code.
- std/io/pipe and tests/std/pipe stay uncommitted until that lowering fix.
## std/io/pipe landed; container loop bounds fixed (round 64)

- b6352c5: forContainer now keeps the view data pointer and length in temp
  slots and reloads them in the condition and body; the back edge no longer
  reads a preheader SSA value. Corpora re-recorded.
- cd0cef9: std/io/pipe (PipeState class, chan<bool> notifications,
  PipeReader/PipeWriter implementing io.Reader/io.Writer, close, pipe).
  tests/std/pipe writes "hello", closes, reads to EOF (exit 5).
- Cold-cache gate -full is green.
- Next: std/parallel, then std/encoding/json; the await/place-cache note for
  pipe is resolved by the slot reloads.
## std/parallel written, blocked by generic inference (round 65, uncommitted)

- std/parallel/parallel.will: map/for_each/reduce as spec'd (four-chunk
  spawns, list[U] results, reduce combine), plus map_part/each_part/
  reduce_part/chunk_size helpers.
- tests/std/parallel: map with double and reduce with add over a list slice.
- Blocked:
  1. checker inference does not infer T from a []T parameter: "argument 1 has
     type []i32, want []T";
  2. explicit multi-parameter generic call syntax `f<i32, i32>` parses the
     second argument as a comparison/name (N0001 undefined name), so the two
     type parameters cannot be passed explicitly.
- sendsync now treats list/dict/set as shareable (element Send checks kept);
  that cleared the Task<list[U]> boundary error.
- Next: fix generic inference for view parameters and multi-parameter generic
  arguments, then re-run the parallel corpus and gate.
## Generic calls into imported packages cannot instantiate (round 66)

- `c.env.funcs` only registers functions declared in the current file
  (env.go:209 `e.funcs[decl.Name]`). A call through an imported package
  (parallel.map(...)) reaches the FieldExpr fallback in callType
  (infer.go:1448): it infers the callee type and calls checkPayload directly,
  so no inference and no instantiation happen. The FuncKind type built for an
  imported member also carries no type parameter names, so unifyType has
  nothing to bind.
- That is why parallel.map(view, double) reports "want []T" and
  reduce<i32, i32> cannot pass both arguments: the generic information is
  lost at the import boundary.
- Fix direction: register imported function signatures (with typeNames) in the
  checker env under their qualified member name, and route FieldExpr calls
  through the same inferCallTypeArgs/instantiate path as local functions.
- Worktree: std/parallel, tests/std/parallel, sendsync list/dict/set rule,
  PROGRESS (uncommitted).
## Imported generic call path added, still not effective (round 67)

- Added a FieldExpr branch in callType (infer.go): when the resolved symbol is
  a FuncKind declared by an imported FuncDecl, buildForeignSig +
  inferCallTypeArgs + checkCallArgs/instantiate run as for local functions,
  plus a symbolOfNode helper.
- parallel.map(view, double) still reports `want []T`, so the branch is not
  reached: resolve may not record a symbol for the FieldExpr of an imported
  member (symbolOfNode returns nil).
- parallel.will Task[...] was corrected to Task<...>; the remaining parallel
  errors are `K0011 await needs a Task` at await t! where t is an element of
  list[Task<...>] (element type lost/unsupported await of an unmarshaled
  task), and the map argument mismatch above.
- Worktree uncommitted: parallel.will, tests/std/parallel, sendsync rule,
  infer.go/symbolOfNode, PROGRESS.
## Imported generic member still unresolved (round 68)

- Tried two paths: callType's FieldExpr branch and methodType's import branch
  (parallel.map parses as a MethodCallExpr). resolve now records the imported
  member symbol on both FieldExpr and MethodCallExpr via importedMember, and
  methodType instantiates an imported FuncDecl through buildForeignSig +
  inferCallTypeArgs.
- parallel.map(view, double) still reports `want []T`, so symbolOfNode(call)
  is nil or the member Kind is not FuncKind at that point; a temporary
  println in the FieldExpr branch never fired, confirming the call goes
  through methodType.
- Two K0011 errors remain for `await t!` where t is a list[Task<...>]
  element.
- Debug println is still in infer.go; worktree uncommitted.
## Imported generic inference works, duplicate prototype check remains (round 69)

- Printed proof: inferCallTypeArgs unifies map's parameters successfully
  (`unify 0 true 1 2`, `unify 1 true 2 2`), so the imported generic call is
  instantiated; function-name arguments are now resolved to their signatures
  before unify (genericinfer.go), and resolve records imported member symbols
  for FieldExpr and MethodCallExpr.
- parallel.map still gets a T0003 `want []T` from a second check path that
  runs with the prototype signature (likely the outer expression walker
  re-checks the call). The await-of-list-element K0011 errors also remain.
- Debug println removed from genericinfer.go/infer.go; worktree uncommitted
  (walk.go, infer.go, genericinfer.go, sendsync.go, std/parallel,
  tests/std/parallel).
## Imported generic call: attempted, reverted (round 70)

- Tried recording imported member symbols in resolve (FieldExpr and
  MethodCallExpr), instantiating imported FuncDecls in methodType and
  callType, resolving function-name arguments to signatures before unify, and
  printing the instantiated signature. Proof that unify binds both map
  parameters exists, but the T0003 `want []T` still came from checkArg, and
  the methodType branch stopped firing after later edits, so the change never
  converged. The checker/resolve edits were reverted to keep the tree honest.
- Kept and committed: sendsync treats list/dict/set as shareable (element
  Send checks kept), which clears Task<list[U]> boundaries. Cold gate green.
- std/parallel and tests/std/parallel stay uncommitted: the generic call into
  an imported package is still unresolved, and `await t!` on a list element
  reports K0011.
- Next: approach the imported generic call from the resolver side (register
  the imported function signature with its typeNames in the checker env when
  the package is loaded), rather than reconstructing it per call site.
## parallel.map inference: function-name argument is NamedKind (round 71)

- The foreign-call path (methodType's foreignFuncs branch) does fire for
  parallel.map: printed `foreign-call parallel map` and `after-infer
  typeParams 2`, so inference fails and the prototype sig is used (T0003).
  parallel.reduce infers fine (typeParams 0).
- Printed argument kinds: map's second argument (double) is NamedKind, not
  FuncKind, so unifyType's func(T)->U branch cannot bind U. The attempt to
  resolve a named function argument through c.env.funcs did not bind either,
  so env.funcs likely keys functions differently than the short name; that
  experiment was reverted again to keep the tree clean.
- Next: resolve a named function argument to its declaration signature with
  the same lookup the callee path uses (symbolOf on the NameExpr), not
  env.funcs by short name.
## parallel.map: function argument never reaches unify (round 72)

- The symbol-based resolution of a named function argument printed only
  `func-arg identity true false`: the reduce argument named identity reaches
  inferCallTypeArgs as NamedKind with a non-Func symbol, while map's `double`
  argument never appears as a NameExpr in that loop at all.
- Both the env.funcs and symbolOf attempts were reverted again; the
  foreignFuncs call path stays as it was (it does fire, but inference still
  leaves typeParams=2 for map).
- Next: inspect the AST shape of the map argument list (the callee and the
  function argument) before another attempt; the double argument may be
  wrapped (method value or parenthesized call form), and identity's named
  type may be the generic parameter A rather than the argument value.
## std/encoding/json dependency chain (round 72)

- Spec API: encode<T>(v: T) -> bytes, decode<T>(data: []u8) -> Result[T, Error>,
  decode_value/encode_value, class Encoder (async write_value<T>/write_raw/
  flush), class Decoder (async next_value), and @derive(Json) for structs.
- Three prerequisites are not ready:
  1. imported generic calls (json.encode is reached through a package
     qualifier; parallel.map already fails on this);
  2. @derive(Json) generation, which has no compiler/library support yet;
  3. bytes<->str conversion, which is a recorded spec gap (desc/理由/缺口
     清单.md) and is how JSON bytes are produced and consumed.
- Implementing json now would be a placeholder over unimplemented foundations,
  so it stays unfilled and the chain is recorded; parallel stays blocked on
  the imported generic inference as well.
## Function name as a value fixed (round 73)

- Root cause found: inferExpr returned nil for a bare function name in value
  position (it only checked variables), so func(T) -> U parameters could not
  unify and generic inference failed (T0004 for apply, T0003 for
  parallel.map). inferExpr now returns the FuncKind signature of a
  non-generic function name; commit 327dcb2.
- parallel traces now show both type parameters unified; the T0003/T0004 are
  gone. Remaining: K0011 on `await t` where t is an element of
  list[Task<...>] (the for-binding element type is not seen as a Task).
- Worktree: std/parallel, tests/std/parallel, PROGRESS (uncommitted).
## elemOf/function-name fixes reverted (round 74)

- elemOf now knew list/dict/set and inferExpr returned a function name's
  signature, which cleared K0011 and the map inference errors, but
  TestRunEmptyInterface regressed: `show` shadows a number and its call
  infered as func(interface {})->i32 against i32. Both edits were reverted
  to keep the suite green; parallel stays uncommitted.
- Next: narrow the function-name change so it only applies in value position
  (argument of a func-typed parameter), not in shadowing/call contexts.
## Function-name fix limited to inference; parallel rewritten (round 75)

- Reverted the global inferExpr function-name change (it broke
  TestRunEmptyInterface) and instead resolved a nil-typed bare function name
  inside inferCallTypeArgs via symbolOf; EmptyInterface stays green and
  elemOf again knows list/dict/set (K0011 cleared).
- parallel.map still hit T0016 because []T is a borrow crossing a task; the
  package now copies the view into owned lists and copies each chunk before
  spawn (copy_chunk), with workers taking list[T].
- New errors after the rewrite: N0001 undefined name at parallel.will:21/31
  (the `list[T]()` construction inside the generic def), N0005 size never
  read in each_all. The worktree holds the rewrite uncommitted.
- Next: check why the `list[T]()` construction is undefined in these generic
  defs (the same spelling works in std/io/pipe), then rerun parallel.
## parallel N0001 has mismatched positions (round 76)

- A minimal probe (generic def with []T parameter, list[T]() construction,
  copy loop) compiles and runs, so list[T]() in a generic def is fine.
- std/parallel still reports N0001 at 21:18 and 31:18 while the current file
  has `var owned` at 6/16/26 and N0005 at 84 (`var size`), i.e. the reported
  positions do not match the source for the N0001s. Clearing the user cache
  (%LOCALAPPDATA%\will) did not change it.
- Next: check whether the parallel.will .willc/package cache inside the std
  module or the diagnostic position mapping (line/column of a generic def) is
  off; then rerun parallel.
- Worktree: genericinfer.go (limited function-name resolution), infer.go
  (elemOf list/dict/set), std/parallel rewrite, tests/std/parallel, PROGRESS
  (uncommitted).
## Two distinct blockers found (round 77)

- A minimal probe of the parallel map shape (def map with []T, list[T](),
  spawn map_all, async map_all, await t) compiles and resolves; it then
  fails in gen with `G0001 an await result is a place` (node #107). An
  awaited list result is a place, and the var initializer asks for a value;
  gen needs the place path for await results the way state machines already
  do for smPlaces.
- std/parallel's N0001 at 21:18/31:18 still does not match the current
  source (var owned is at 6/16/26). No .willc or user cache remains, so the
  position is either recorded from a stale in-memory doc or from a different
  file of the std module; next step is `will check` on the test module with
  only the map def present to see if the diagnostic follows the def.
- Worktree: genericinfer.go, infer.go, std/parallel, tests/std/parallel,
  build/probepar, PROGRESS (uncommitted).
## await on a Task variable is not lowered (round 78)

- Probe record #107 is `await t` at main.will:28:26 with expr=#108, a name.
  place(await) only handles recv/send/mcall expressions (struct.go); a plain
  Task variable falls through to the default place (the variable's slot,
  typed Task<...>), so g.value sees an aggregate and reports
  `an await result is a place`.
- This is a gen gap: awaiting a Task variable in a normal function has no
  lowering (the state machine path is separate). It is independent of
  list/parallel.
- Next: implement the Task-variable await path (load the task result slot) or
  route it through the existing task-wait helper; then rerun the probe and
  parallel.
## await place ordering (round 79)

- generalCallWait registers smPlaces for the record it is handed, and
  gen.go:4594 calls it from the await value path. The probe fails because
  place(#107) runs before that value path (var initialization asks for the
  place of the await result), so smPlaces has no entry yet.
- Fix direction: register the await place when lowering the await itself
  (or make the var initializer go through value first for an await operand),
  and handle the Task-variable operand in place(await) instead of falling to
  the default slot typed Task<...>.
- Worktree unchanged and uncommitted: genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS.
## await place: needs a dedicated join (round 80)

- Attempted registering the await place by calling generalCallWait from
  place(await) for a Task-variable operand; it fails with "the called
  function has no symbol" because generalCallWait expects a call record, not
  an await record. Reverted.
- The Task-variable await needs its own lowering shared by the value and
  place paths (join + result slot + deadline/failure handling), likely
  built on awaitTaskValue/smTryJoin; lowerAwaitResult wraps the outcome in
  Result<T,TaskError>, which is not the plain await result.
- Worktree: genericinfer.go, infer.go, std/parallel, tests/std/parallel,
  build/probepar, PROGRESS (uncommitted).
## await Task join works; nested generic substitution blocks (round 81)

- Added awaitTaskJoin (await_result.go): joins a Task value once into a
  result slot, registers smPlaces, and returns the slot; place(await) routes a
  Task-variable operand there. The probe now passes the await stage.
- New error: `list element U is not lowered yet` (node #63) inside the
  instantiated map_all: the awaited Task's result text still contains the
  type parameter (list[U] instead of list[i32]), so generic instance
  substitution does not rewrite a type parameter nested inside Task<...>.
- Next: make substituteTypeText (and the clone substitution) walk nested
  generic arguments such as Task<list[U]>, then rerun the probe and
  std/parallel.
- Worktree: struct.go, await_result.go, genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## Clone substitution leaves new list[U] (round 82)

- Traced cloneFuncInstance: it does run substituteTypeText over the copied
  records, but the printed result for the new record is still `list[U]`
  (`clone-type new list[U]`), so either the substitution map lacks U for the
  map_all instance or the token scanner does not treat the bracket form the
  way expected.
- Next: print the subst map keys at clone time for map_all and check
  typeParamNamesIn(def, fn) returns both T and U; then fix the replacement.
- Worktree: await_result.go, struct.go, genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## Clone args stay as prototype params (round 83)

- Printed proof: clone map_all names 2 args 2 with subst {T:T, U:U}. The map
  instance's `spawn map_all(owned, f)` is planned through
  planCallInstances/cloneInstance (the older path), where inferGenericArgs
  derives args from the callee's argument types. Inside map's instance the
  argument type is the substituted list[i32], but the planned instance still
  received the prototype names (T,U), so map_all's clone kept list[U].
- planCallInstances (cloneInstance) and planInitInstances/planMethodInstances
  (cloneFuncInstance) use different substitution paths; they must agree, and
  the call arguments in a cloned document must be substituted before
  inferGenericArgs runs.
- Worktree: await_result.go, struct.go, genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## planCallInstances switching broke map emission (round 84)

- Replacing cloneInstance with cloneFuncInstance in planCallInstances, then
  skipping calls whose args still name type parameters, produced
  `use of undefined value '@probepar.map'`: the map instance is planned but
  not emitted (or the skip removed it). Reverted generic.go to HEAD.
- Conclusion: the call-instance planner needs the same substitution and skip
  rules as init/method instances, but changing the clone alone is not enough;
  the emitted-instance set and the mangle name must agree.
- Worktree: await_result.go, struct.go, genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## Instance planning advanced (round 85)

- inferGenericArgs now rejects a binding that is still a type parameter name
  (an internal call inside a generic declaration becomes an instance only
  after its caller is instantiated), and resolves a function-name argument
  through its func declaration (lambdaTypeText), so main's map(view, double)
  instantiates again and probepar.map is defined.
- The probe now fails in gen with `a void function returns no value`
  (node #72), likely the instantiated map_all's return type still carrying U
  or an empty result text; next is to trace that node and the instance's
  result type.
- Worktree: generic.go (inferGenericArgs), await_result.go, struct.go,
  genericinfer.go, infer.go, std/parallel, tests/std/parallel,
  build/probepar, PROGRESS (uncommitted).
## map_all instance signature is empty (round 86)

- Node #72 is `return out` inside map_all (main.will:19:5); gen reports
  "a void function returns no value", so the instantiated map_all function
  record has an empty result type. The instance comes from the old
  cloneInstance path (planCallInstances), which does not substitute the
  signature the way cloneFuncInstance does; the debug print in the
  cloneFuncInstance branch never fired, confirming this.
- Fix: route planCallInstances through the same substitution as
  cloneFuncInstance (with the unbound-argument skip already added in
  inferGenericArgs), and keep emission consistent.
- Worktree: generic.go, await_result.go, struct.go, genericinfer.go,
  infer.go, std/parallel, tests/std/parallel, build/probepar, PROGRESS.
## Instance signatures are correct (round 87)

- Printed proof: planCallInstances creates map$i32$i32 (Task<list[i32]>) and
  map_all$i32$i32 (list[i32]); cloneInstance already delegates to
  cloneFuncInstance and substitutes correctly.
- The remaining `a void function returns no value` at map_all's `return out`
  therefore comes later than cloning: likely the signature registered for the
  instance symbol in the emit pass (g.resultType empty for that instance) or
  the var type of out, not the clone.
- Debug prints removed; worktree: generic.go (inferGenericArgs unbound +
  function-name resolution), await_result.go, struct.go, genericinfer.go,
  infer.go, std/parallel, tests/std/parallel, build/probepar, PROGRESS.
## map_all instance has no state machine (round 88)

- The error comes from returnStmt(st, void=true): funcDecl lowers an async
  body with void=true (gen.go:2005) because the return value belongs to the
  state machine, but the map_all$i32$i32 instance is being lowered through
  the plain path, so its `return out` is rejected.
- So the remaining gap is the async state-machine recognition for a cloned
  instance (asyncSequenceMachine/generalMachine on the instance doc), not the
  signature or the clone substitution (both verified correct).
- Worktree: generic.go, await_result.go, struct.go, genericinfer.go, infer.go,
  std/parallel, tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## map_all's await is an await record, not a call (round 89)

- Added markAsyncFuncs (emitDoc + package collect) marking every async
  function of the current document suspendable; the void error persists.
- Cause: asyncSequenceMachine only collects `call`/`mcall` records. map_all
  awaits a Task variable (`await t`), which is an `await` record whose expr
  is a name, so the planner finds no site, builds no state machine, and the
  instance is lowered on the plain path where a value return is rejected.
- Next: teach asyncSequenceMachine (and the await join path) about
  `await name`, or lower the Task-variable await through the general machine;
  then rerun the probe and std/parallel.
- Worktree: gen.go (markAsyncFuncs), package.go, generic.go,
  await_result.go, struct.go, genericinfer.go, infer.go, std/parallel,
  tests/std/parallel, build/probepar, PROGRESS (uncommitted).
## Workspace batch verified (round 90)

- Reverted: markAsyncFuncs (no effect on the void error), awaitTaskJoin +
  place(await) name branch (broke build/task with "operand needs Result"),
  elemOf list/dict/set (broke TestRunGenericClassSourceSurface).
- Committed: inferCallTypeArgs function-name resolution and inferGenericArgs
  named-function/argument handling (types/check/cli tests green).
- std/parallel and tests/std/parallel remain uncommitted: the Task-variable
  await in an async loop still needs state-machine support
  (asyncSequenceMachine only collects call/mcall records).
- Lesson: every one of these gen changes must be gate-checked in isolation;
  three of five broke async corpora or a CLI test.
## await Task in the sequence machine (round 91)

- Taught asyncSequenceMachine to treat `await name` as a task suspension
  site; the probe then went back to `an await result is a place`, i.e. the
  planned site does not register smPlaces for the await record that the var
  initializer later asks for (the site's registration and the place lookup
  disagree about the record id/kind).
- Reverted the scanner change to avoid a half-migration; std/parallel stays
  uncommitted and blocked on this await/Task state-machine work.
- Next: make the await site register the await record id (not only the call
  record) and verify with the probe before any further parallel work.
## await Task needs the general machine (round 92)

- With the await site added to asyncSequenceMachine, a new probe became
  possible: sm-reject 35 call 4. Node #35 is map_all's `spawn map_part(...)`
  inside the while loop; asyncCallPathAllowed only accepts top-level linear
  calls, so the sequence machine refuses the function. The complex control
  flow should go to the general machine, which currently has no support for
  `await` on a Task variable either (its wait helpers expect a call record).
- All experiments reverted; tree builds. std/parallel remains uncommitted and
  blocked on Task-variable await support in the general machine.
- Next: add await-name handling to the general machine's wait path (join a
  Task value into a frame slot) and let map_all fall into it.
## general-machine await experiment reverted (round 93)

- Added awaitTaskJoin again, an await branch in generalCallWait/
  generalCallWaitPlace, and a place(await) branch gated on g.smGeneral.
  The probe advanced past the place error, but the cold gate failed with 9
  async corpora (build/task, taskresume, async*, genericasync): g.smGeneral
  does not distinguish sequence from general as assumed, so the extra join
  interferes with the sequence machine.
- Reverted all three files; build green. The Task-variable await in the
  general machine needs a correct mode check (not smGeneral) before it can
  land; std/parallel remains uncommitted.
## general await advances to the void check (round 94)

- With a name-restricted await branch in generalCallWait and a place(await)
  branch gated on smGeneral, the probe passes the await stage and fails in
  map_all's `return out` with "a void function returns no value".
- funcDecl does not reach map_all (generalMachine enqueues it early), so the
  void decision for the general step is elsewhere (the general emitter's
  bodyStmts result flag); the next step is to make the general step treat a
  non-empty result type as non-void.
- Worktree uncommitted: generic.go (awaitTaskJoin), statemachine_async_general.go
  (await branch), struct.go (place branch), std/parallel, tests/std/parallel.
## general await progressed, gate still red (round 95)

- With awaitTaskJoin, the name-restricted generalCallWait branch, the
  smGeneral place branch, and a resultType fallback to typeLLVMOf, the probe
  finally compiled and ran: it now panics at double (x * 2) because the
  element value passed through map/map_part is wrong at runtime.
- The cold gate still fails 9 async corpora (build/cblock, task, taskresume,
  async*, genericasync), so the batch was reverted again to keep the tree
  green; std/parallel and the probes stay uncommitted.
- Next: isolate which of the four changes breaks the async corpora (run one
  corpus per change), then fix the map element runtime value.
## task corpus check after revert (round 95)

- After reverting all experimental gen files, internal/gen has no worktree
  changes and go build is green, but tests/build/task still fails with
  `cannot translate node #41: the operand needs { i32, [4 x i8],
  %interface.Error }`. This contradicts the round-90 verification and must be
  rechecked next round with a freshly rebuilt binary and an empty WILL_CACHE
  (possible stale $TEMP binary or cache).
- Worktree: PROGRESS.md, README.md, std/parallel, tests/std/parallel.
## await reapplication isolation (round 96)

- Rebuilt binary + empty cache: tests/build/task passes at HEAD (the earlier
  #41 was a stale $TEMP binary; rule: always rebuild before verifying).
- Reapplied the four await/general changes (A awaitTaskJoin, B generalCallWait
  await branch, C place(await) smGeneral branch, D general resultType
  fallback): task fails #41 again, so one of A/B/C is the regression; the
  probe crashes (0xC0000005) with the batch applied.
- Reverted all six gen files; build green. std/parallel still uncommitted.
- Next: apply A first alone (no B/C/D) and verify task, then add B, then C,
  to find the breaking change; keep the resultType/question separate.
## Stale compiler cache masks failures (round 97, P0)

- After reverting A+B+C, tests/build/task STILL fails #41 (operand needs
  Result). Even a clean WILL_CACHE + fresh binary fails once the user cache
  (%LOCALAPPDATA%\will) is cleared. So HEAD itself has the #41 bug, and the
  earlier "passes" (rounds 90/96) were served from a stale user cache: the
  cache key apparently does not include the compiler identity.
- This is a P0 build correctness issue: a compiler change can be masked by
  cached artifacts. Next: inspect the cache key (internal/cache and callers)
  and add the compiler identity (binary hash/version) before any further
  compiler debugging; then re-run the full gate cold and re-verify task.
- Worktree clean except std/parallel, tests/std/parallel, PROGRESS, README.
## Cache P0 retracted; #41 is a real A+B+C regression (round 98)

- compilerIdentity() is "will <version> <binaryHash>" and every cache key adds
  it, so the cache cannot serve a stale compiler (the P0 suspicion in round
  97 was wrong).
- The "task still fails after revert" was a stale $TEMP binary: the A+B+C
  build was still on disk. A fresh `go build -o` at HEAD makes
  tests/build/task pass (code 0). Lesson: rebuild the binary after every
  revert before verifying.
- So #41 is a genuine regression from applying A (awaitTaskJoin) + B
  (generalCallWait await-name branch) + C (place(await) smGeneral branch).
  Next: apply B alone, rebuild, run task; then C alone; then A+B+C together.
## C breaks the sequence machine (round 99)

- Isolation: A (awaitTaskJoin) + B (generalCallWait await-name branch) keep
  tests/build/task green; adding C (place(await) name branch gated on
  g.smGeneral) breaks it with #41.
- willc shows `await a!` as `unwrap(await(a))`: the await expr is a name, so C
  fires even in the sequence machine, which means g.smGeneral is not a
  sequence/general discriminator there. Also awaitTaskJoin returns T, while
  the plain await value is Result<T,TaskError> (unwrap needs the Result), so
  C's use of it is semantically wrong.
- Reverted C; A+B remain in the worktree and task stays green.
- Next: correct awaitTaskJoin to produce the Result value, and gate C on an
  actual general-mode flag (or drop C and handle the var initializer through
  the general call path).
## Async corpora green with the await fix (round 100)

- awaitTaskJoin now produces Result<T,TaskError> (the plain await value); C is
  unconditional (the name branch no longer keys on smGeneral), and g.value
  allows loading an aggregate await/sm result.
- Verified: tests/build/task, taskresume, and concurrency/async all emit-llvm
  cleanly (previously 9 async corpora failed), so the earlier breakage was the
  T-typed awaitTaskJoin, not the await path itself.
- The probe now reaches IR verification and fails with: '%t94' defined with
  type '{ i32, [4 x i8], %interface.Error }' but expected 'ptr' at
  willrt_gc_write_barrier(ptr %v.out, ptr %t94). The Result slot is
  registered as a GC reference (addSyntheticTemp) but Result is an aggregate;
  its list field must be the root instead.
- Next: register the Result's GC payload field (or skip the temp and teach the
  general frame the slot), then rerun the probe, the full gate, and commit.
## await Result fix works but gen tests pin the old IR (round 101)

- The await batch (awaitTaskJoin returning Result<T,TaskError>, unconditional
  place(await) name branch, aggregate load allowed in g.value, general
  resultType fallback, addSyntheticTemp skipping aggregates) made the probe
  compile and run (remaining: panic integer overflow in double, a closure
  argument mix-up).
- Async corpora emitted cleanly and their IR changed; after -update the gate
  failed in gen tests instead (TestGeneralFrameDropsChildResult,
  TestJoinErrorSlotHoldsTheStatus, TestJoinResultCarriesTaskError,
  TestSpawnSyncCallLowersToJob, TestSendLoopBecomesStateMachine,
  TestGeneralMachineSplitsTwoChannelOps): the general join/frame IR tests
  still assert the old await shape.
- Reverted all six gen files and the regenerated corpora to keep the tree
  green; std/parallel stays uncommitted.
- Next: reapply the batch, update those gen tests to the new await/Result
  shape (or make awaitTaskJoin byte-identical to the old join for call
  operands), then regenerate corpora and verify the cold gate.
## await fix needs 5 gen tests updated (round 102)

- Reapplied the await batch (without the addSyntheticTemp change): gen tests
  TestJoinErrorSlotHoldsTheStatus, TestJoinResultCarriesTaskError,
  TestSpawnSyncCallLowersToJob, TestSendLoopBecomesStateMachine, and
  TestGeneralMachineSplitsTwoChannelOps fail; TestGeneralFrameDropsChildResult
  passes now that synthetictemp is untouched.
- The tests assert the old blocking helper `willrt_join` / old join shape; the
  name-await path now uses the non-blocking `willrt_task_try_join` (correct for
  async). A blanket rename broke TestAsyncInterfaceVtableCallsConstructor, so
  each test needs a precise expectation update rather than a global replace.
- Reverted internal/gen to HEAD again; build green. Plan: apply the batch,
  update those five tests one by one to the new join shape (verify each), then
  regenerate corpora and run the cold gate before committing.
## await gen tests assert the old TaskError join shape (round 103)

- Reapplied the await batch and updated the three straightforward assertions
  (willrt_task_try_join, %willrt_error). Remaining failures are the old
  TaskError-table IR assertions: TestJoinResultCarriesTaskError still wants
  await.result, will.task.error.table, @will.task.error.message,
  will.task.error.clone/drop, task.error.copy, and willrt_free_str, none of
  which the Result<T,TaskError> await path emits now; TestSendLoopBecomesStateMachine
  and TestGeneralMachineSplitsTwoChannelOps have similar old-shape assertions.
- Reverted internal/gen again to keep the suite green. The await behavior
  itself is proven by the probe (compiles and runs) and by async corpora
  emitting cleanly; updating the five gen tests to the new Result join shape is
  its own task and should be done before the batch lands.
## void Task await needs a Result too (round 104)

- Updated several gen tests to the new join shape and they pass
  (TestJoinResultCarriesTaskError, TestJoinErrorSlotHoldsTheStatus,
  TestSpawnSyncCallLowersToJob, TestGeneralMachineSplitsTwoChannelOps is close).
- TestSendLoopBecomesStateMachine now fails in gen itself: node #38
  (`await task!` on Task<()>) reports "the operand needs Result"; awaitTaskJoin
  returns no place for a void payload, so the `!` operand has no Result. A
  void Task must still produce Result<(), TaskError> (with the err slot and no
  out).
- Reverted internal/gen again to keep the tree green; the await batch is
  blocked on handling void Task results plus the remaining test updates.
## await fix passes gen tests but 6 CLI run tests fail (round 105)

- With the void-Task Result support, all gen tests pass (ok
  github.com/will-lang/will/internal/gen); the probe compiles and runs (only
  the closure argument panic remains).
- However go test ./internal/cli now fails 6 behavior tests:
  TestRunTaskErrorOwnsItsMessage, TestRunSpawnDropsArrayCapture,
  TestRunAsyncSpawnMovesOwnedArgument, TestRunAsyncFrameDropsOwnedLocalsOnPanic,
  TestRunJoinReturnsTheStoredValue, TestRunMPMCCountsEveryMessage. The Result
  join shape changed runtime output/ownership behavior, so these need per-case
  review (streamed output vs captured, stored value, owned argument moves).
- Reverted internal/gen and tests to keep the tree green; the await batch is
  validated only at the gen-test level so far.
## Function-value ABI mismatch crashes map (round 107)

- The probe crashes (0xC0000005) even with double replaced by identity, so it
  is not the x*2 overflow. IR shows the indirect call
  `%t32 = call i32 %t29(ptr %t30, i32 %t31)` (env + i32), while a named
  function value (double) has the plain signature `@probepar.double(i32)`.
- indirectCall always passes the environment pointer as the first argument
  (funcvalue.go:228), but a non-lambda function value's code has no env
  parameter, so double receives the env pointer as x and the callee sees
  garbage (or an invalid code pointer when the call target is the env).
- Correct fix: give named function values a thunk with the closure ABI
  (env + params) that forwards to the plain function, or stop passing env for
  non-closure values. The thunk keeps the uniform indirect ABI.
- Worktree: std/parallel, tests/std/parallel, build/probepar (uncommitted);
  HEAD ad3bbdd is green.
## Named function values already thunked; crash is elsewhere (round 108)

- placeNamedFunction already exists: a named function used as a value gets a
  wrapper `@pkg.thunk.fn(ptr env, params...)` forwarding to the plain function
  with env=null, so the indirect ABI is uniform. IR confirms
  `@probepar.thunk.double(ptr %closure.env, i32 %x)` ->
  `@probepar.double(i32 %x)` and the call passes (env=null, i32).
- Therefore the probe crash (0xC0000005, even with identity) is elsewhere:
  candidates are the list container operations in map_part/copy_chunk or the
  general-frame Result slot. Next: narrow with a smaller probe (list copy +
  get without spawn/await, then add spawn/await).
- Worktree: std/parallel, tests/std/parallel, build/probepar (uncommitted);
  HEAD ad3bbdd is green.
## Layered probes: list/spawn/func-value/generic-func all pass (round 109)

- build/probelist (list copy + get + iterate): 3, pass.
- build/probespawn (+ spawn + await t!): 3, pass.
- build/probepunc (+ function-value parameter + indirect call through thunk +
  spawn): 3, pass.
- build/probegenfunc (+ generic def taking func(T)->U, called with double,
  spawned): 2, pass.
- build/probemapall (generic async map_all returning list[U], called directly
  and awaited): emit-llvm fails with `store ptr , ptr %v.t` — the call result
  for a direct generic async call is empty (no symbol), unlike the spawn path,
  which compiles (probepar) but crashes at runtime.
- Next: fix the direct generic call result (empty task) and then narrow the
  probepar runtime crash with a spawn-only variant of map_all.
## Direct generic call still empty; container unify added (round 110)

- build/probemapall (map_all<i32,i32>(xs, double) awaited directly) still emits
  `store ptr , ptr %v.t`; a temporary print showed genericCallName missing for
  mcall #44/#48 (genericTarget returns false). The call parses as an mcall and
  inference leaves U unbound (double's type comes from the function declaration
  path) or the explicit typeargs are absent, so no instance symbol is returned.
- Added containerElem unification to unifyTypeText (list[...] vs list[...] elem
  inference); gen tests still pass (ok).
- Worktree: internal/gen/generic.go (awaitTaskJoin already committed; this
  unify change is new), std/parallel, probes, PROGRESS (uncommitted except
  awaitTaskJoin in ad3bbdd).
- Next: make genericCallName accept the mcall shape (resolve the free function
  from the method name) and resolve function-name arguments in inferGenericArgs
  for mcall, then rerun probemapall and probepar.
## mcall to is a ref; genericTarget never reaches the name scan (round 111)

- Added short-name matching to genericTarget's free-function scan and
  container unification to unifyTypeText (gen tests still ok), but
  probemapall still emits `store ptr ,`.
- A debug print inside the scan never fired for the map_all call, so the mcall
  shape has a non-zero `to` ref and genericTarget returns through the
  ref-branch (treating it as a direct function) instead of the name scan; the
  free generic function is therefore never resolved.
- Next: inspect mcall record #44 (recv/to/text fields) with emit-willc and make
  genericTarget handle the method-call shape explicitly (resolve the free
  function from text, args from the call arguments).
## task construction works; store of the value is empty (round 112)

- willc shows the map_all call as a plain call (#55 to=#2 with two typearg
  records i32,i32 and args xs,double), not an mcall; the earlier
  generic-name-miss prints were list.push mcalls.
- IR contains the instantiation and the constructor call
  `%t55 = call ptr @probemapall.map_all$i32$i32.task(ptr %t52, ptr %v.closure.59, i32 -2, i32 0)`,
  plus the sm/ drop helpers. But the next store is `store ptr , ptr %v.t`
  (sm.call.ok.4) - the value handed to the var initializer is empty even
  though %t55 exists.
- Next: inspect the var statement lowering for a Task-typed initializer
  (g.value of a call whose result is a Task) to see why the constructor result
  is dropped instead of stored.
## Task result lost between constructor and frame (round 113)

- The sequence machine accepts the map_all call as a task segment:
  emitAsyncStart's non-c branch calls emitConstructorCall and stores the
  child task pointer into the segment op slot (line 306), then emitPoll
  copies the frame result to the variable slot.
- The IR shows `%t55 = call ptr @...task(...)` and later `store ptr , ptr %v.t`
  in sm.call.ok.4, so the value stored into the op slot is empty: the child
  returned by emitConstructorCall at this point is "" even though %t55 exists.
  Either the constructor result is not threaded back to the segment, or the op
  slot is a different value than the one the poll copies.
- Next: print emitConstructorCall's return and the op/resultField names in
  emitAsyncStart/emitPoll for the map_all segment to find where the value is
  dropped.
## Direct async call value path (round 114)

- A debug print in emitAsyncStart's task branch never fired for probemapall,
  so the map_all call is not a sequence segment: it is a direct call in a var
  initializer (#55), lowered through the value(call) path.
- spawn machinery (task.go:86-111) resolves generic callees via
  isGenericCallee and enqueues the instance, but the direct-call value path
  has no such branch; the empty store therefore comes from value(call) for an
  async generic callee.
- Next: in the value(call) path, when the callee is a generic async function,
  resolve the instance symbol (genericCallName) and emit the constructor
  through emitConstructorCall, mirroring spawn's generic handling.
## Direct async call goes through callExpr's generalCallWait (round 115)

- value(call) ends in callScalar -> callExpr. callExpr has the async branch:
  `if g.smGeneral && !isAsyncCRecord(r)` and `awaitTargetSymbol(r)` is
  suspendable -> generalCallWait(r) (constructor + smTryJoin) and returns its
  value. That is the correct path for map_all, so the empty store means the
  suspendable check fails for the instance symbol.
- Re-added markAsyncFuncs (emitDoc + package collect) marking every async
  function of each document, which should cover cloned instance documents
  (their mangled func records keep async=true, and g.cur.funcs maps them to
  the mangled symbol) so suspendableFor(map_all$i32$i32) becomes true.
- probemapall still fails; next: print awaitTargetSymbol and suspendableFor in
  callExpr for #55 to see whether the instance symbol is marked at that point
  (ordering: instance document may not be emitted yet when the caller runs).
## Direct generic async call: construction fixed, runtime crashes (round 116)

- callExpr's smGeneral branch joined the call (generalCallWait) instead of
  returning a Task; removing that branch first made emitCall reference the
  plain (undefined) instance symbol, so the fix is a Task construction:
  emitConstructorCall(r, sym, "-2", false) then return its task and text.
  With that, probemapall compiles (the empty store is gone) but crashes at
  runtime (0xC0000005), and aggregate-GC-root experiments
  (synthetictemp aggregate/ptr skip) did not change it.
- All experimental edits reverted; HEAD ad3bbdd stays green.
- Next: fix the general-machine gc roots for the await Result slot (frame gc
  map expects ptr; Result is an aggregate whose list field must be rooted),
  then return to the direct async call construction.
## Await Result needs a payload gc root (round 117)

- With the direct async call now constructing a task, probemapall compiles and
  crashes at runtime. The remaining root issue: the general frame's gc map
  registers the await Result slot as a plain ptr temp, but Result is an
  aggregate; its list field is the real reference and is what must be rooted.
  Registering only ptr temps (round 116 experiment) did not fix the crash, so
  the payload field itself needs a frame gc slot (a GEP into the aggregate).
- Next: in emitGeneralConstructor, for a frame temp whose llvm is an aggregate
  with a GC payload (Result/list), emit a gc slot pointing at that payload
  field instead of the slot base, and keep the release/drop path in sync.
## map_all without await: OOM in the sm loop (round 118)

- With the direct async call constructing the task (callExpr branch) and the
  frame gc map holding only ptr slots, probemapall still crashes; a variant
  that constructs the task and never awaits crashes with 0xC0000409 and
  "error: out of memory".
- That points at the general machine's list iteration inside map_all: the
  for-in loop over list[T] allocates/loops without terminating, so the task
  never finishes and memory is exhausted. The GC-root question is secondary.
- Next: inspect how forContainer lowers under smGeneral (view data/len slots
  in the frame, iterator advancement across resume states) and compare with
  the sequence machine's reload-based fix (commit b6352c5).
## sm loop OOM: counter/cond look right, execution loops (round 119)

- forContainer already keeps data/len in entry-block slots with reloads and
  increments the hidden index in the step block (iterate.go:341-374), so the
  IR shape matches the sequence-machine fix. Under the general machine these
  slots are relocated into the frame.
- probemapall2 still exhausts memory, so the loop must be executing without
  advancing its counter (or the frame slot for the hidden index is not the
  one the step stores to). Next: dump the map_all sm IR around the loop labels
  and compare the frame offsets of the idx slot between the cond load and the
  step store.
## map_all sm IR is well formed; OOM source still unclear (round 120)

- Dumped the map_all$i32$i32.sm IR: the for.cond/body/step blocks load and
  store %v.$idx (increment at for.step.2), pin the list for the push, call
  the closure through the thunk ABI, and store the Result with
  willrt_task_store_result; the task body ret i32 1 (done). The loop shape is
  correct.
- probemapall2 (construct, never await) still ends with OOM, so the exhaustion
  is outside the loop IR: likely the leaked task (no join, no region) and the
  GC/scheduler interaction rather than the iterator. Next: run under a region
  so the task is cancelled properly, and check whether a joined variant
  (probemapall) OOMs or crashes for a different reason.
## Unawaited task escapes K0015 without a region (round 121)

- Wrapping the map_all construction in a region makes the checker reject it:
  `K0015 task handle t is not awaited before region exit` - so the
  probemapall2 OOM came from an unawaited, leaked task, and the region path is
  correctly guarded.
- Without a region, the same leaked-task program passes the checker and OOMs
  at runtime. K0015 currently only fires at region exit, so a task that is
  simply dropped in an ordinary function is not diagnosed. This is a checker
  gap (spec: task handles must be awaited; dropping one leaks the task and its
  result).
- Next: extend K0015 (or add the task-drop rule) to cover any task handle that
  goes out of scope unawaited, except when it is returned or moved to a field,
  and add a corpus case.
## K0015 extended to function exit (round 122)

- regionTasks now also checks the function body: a named Task local that is
  never awaited reports `K0015 ... not awaited before function exit`.
  Consumers (return, assignment, call argument, aggregate) still mark the
  handle consumed, so returning/handing off a task stays legal.
- Verified on probemapall2: it now reports K0015 at the var line instead of
  silently OOMing. check tests stay ok.
- Known issue: the same handle also gets a "before region exit" report from
  consumeTasks' ReturnStmt path (scope is hardcoded "region"), so a function
  without any region prints two messages. Next: thread the scope through
  consumeTasks so only one report is emitted, then update/extend the K0015
  corpus and run the cold gate.
## K0015 extended to function exit, gate green (round 123)

- regionTasks checks the function body too; scope is threaded (k15Scope) so a
  return reports the right place; await through !/? and task handles passed as
  call arguments are marked consumed (spawn arguments included), so moving a
  task stays legal.
- Verified: probemapall2 (leaked task, no region) now reports exactly one
  K0015 at the var line; tests/build/taskresume (hand-off to work()) passes;
  internal/check tests ok; cold gate: ok.
- Committed. Next: add K0015 corpus cases (function exit, return, hand-off)
  and then continue with std/parallel await path.
## mapall await IR is correct; crash is later (round 125)

- Reapplied the direct async call construction (callExpr) plus frame gc map
  holding only ptr slots; probemapall compiles and still crashes (0xC0000005).
- The await IR is well formed: willrt_task_try_join writes a ptr out slot and
  a task_error err slot in the frame, await.ok stores tag 0 + the payload at
  Result field 2, await.err builds the error path. So the crash is after the
  join: likely the unwrap of the Result or the task's own frame lifecycle.
- Experiments reverted; tree green at HEAD cf4ec5e.
- Next: probe await on a Task variable without ! (bind Result to a local and
  drop it) to separate the unwrap from the join, then inspect the task frame
  drop path.
## Bisected the await crash to the list push body (round 126)

- probeawait4 (async generic map_all returning list[U], body just
  `return list[U]()`, spawned and awaited into a Result) runs clean (0).
- probeawait3 (body does `out.push(f(items.get(0)))`) crashes 0xC0000005.
- probeawait5 (`out.push(1)`) is a type error (U vs i32) rather than a crash,
  so the next bisection step needs a U-typed value (e.g. pass U=i32 and push a
  lit converted through a helper).
- So the crash is inside the body steps: list.get on a generic list, the
  closure call result, or list.push under the general frame. Next: bisect
  those three separately (get only, closure call only, push of a parameter).
## Bisected further: get, call and push pass alone (round 127)

- probeget: single-generic map_all<T> doing `out.push(items.get(0))` runs (0).
- probecall: `var v: U = f(1); _ = v; return list[U]()` runs (0).
- probefuncget: `var v: U = f(items.get(0)); out.push(v)` crashes 0xC0000005.
  Its IR is well formed (`%t29 = call i32 %t18(ptr %t19, i32 %t28)` then
  list.append.4), so the crash is a runtime interaction of the get result,
  the closure call and the push - likely a frame slot/lifetime issue for the
  T value or the closure across the call.
- Next: split further (pass a literal into get then f, store the get result in
  a local before f, etc.) to find which value's lifetime breaks.
## get-result through the thunk crashes; IR looks right (round 128)

- probefuncpush (`var v: U = f(1); out.push(v)`) runs (0).
- probefuncget and probefuncgetlocal (get first, then pass the value to f)
  both crash 0xC0000005.
- The sm IR loads the closure from %v.sm.param.8.2 (+0 code, +8 env via read
  barrier), calls `i32 %t18(ptr %t19, i32 %t28)` with the get result, stores
  it to %v.v, then pins out and appends. All values look correct.
- The difference from the passing cases is the list pin/unpin around get
  interleaved with the closure slot; the crash may be the frame slot of the
  closure or of the get result being clobbered across the pin sequence.
- Next: reduce to `var e: T = items.get(0); _ = e; var v: U = f(1)` (keep get
  and the call but separate values) and inspect the frame offsets of
  param.8.2, $idx, out and the get temporaries.
## Frame layout is fine; get+f crash is runtime (round 129)

- probefuncsep (get then f(1), values separated) and probefuncorder (f(1) then
  get) both crash; probecall (f only) and probeget (get only) pass. So the
  crash needs both the list get and the closure call in one task body.
- The sm frame map looks correct: state +0, items +8, f {ptr,ptr} +16..31,
  out +32, e +40, v +44, gcroot +48, list +56, temp +64; no overlap.
- Next: inspect the spawn argument marshalling for f (closure 16 bytes) versus
  the sm param slot, and run the same body synchronously (no spawn) to see
  whether the crash needs the task frame at all.
## Crash needs async, not generics (round 130)

- probefuncsync: the same body (list.get + ordinary call + push) in a plain
  function runs (0).
- probefuncarg: a NON-generic async map_all(items: list[i32]) doing
  `items.get(0)` and `double(1)` then push still crashes 0xC0000005. The
  generics/closure hypotheses are ruled out: the trigger is the async
  (general-machine) body combining a list get with an ordinary call.
- Likely: the sm step's temporary slots / pin-unpin sequence around a
  non-suspending call inside the general frame. Next: check the sm IR of this
  small case for a slot reused across the call (e.g. the list temp %v.list.18
  or a pin value stored in a frame slot that the call clobbers).
## sm gc map is correct (round 131)

- probefuncarg's map_all.sm frame: state +0, items +8, out +16, e +24, v +28,
  list.19 +32, temp +40; task frame size 48.
- The gc stack map marks +8 (items), +16 (out) and +32 (list temp) as ptr
  references with the list descriptor - correct.
- The body IR is also sane: alloc/pin/init out, write barrier into out, read
  barrier + pin items, bounds check, element load, unpin, store e, safepoint(3),
  call double(i32 1), store v, then pin out and append.
- The crash is still unlocated at runtime; with the gc map and body IR both
  correct, the next step is to test whether it needs a GC safepoint: run the
  same probe with a very large heap threshold (no GC) versus the default, and
  check if the crash disappears - that splits "GC scanning" from "plain
  execution".
## P0: run (-O0) crashes where build (-O2) runs (round 132)

- probefuncarg (non-generic async map_all doing items.get + double + push):
  `will run` crashes 0xC0000005, but `will build .` succeeds and the produced
  build/x86_64-win-gnu/probefuncarg.exe exits 0.
- run and build share the same front end and gen IR, so this is the same
  module.ll compiled at -O0 vs -O2: the -O0 binary crashes, the -O2 one runs.
  That means the generated IR has undefined behavior (e.g. reading an
  uninitialized frame slot, or a slot reused across the call/safepoint), which
  -O2 happens to hide.
- This violates the run/build behavior-consistency requirement and is now P0.
  Next: diff the -O0 and -O2 command lines (opt level only), then audit the
  map_all.sm IR for uninitialized reads around the get/pin/safepoint/call
  sequence (the temp %t46 and the op/state slots are the suspects).
## run/build difference is -O0/-O1 vs -O2, not a flag split (round 133)

- buildflags.go only picks -O0 for run and -O2 for build, so both share the
  same IR; explicit `will run . -- -O1` also crashes (-1073741819), while
  `will build` (-O2) and the produced exe run clean.
- The sm IR audit found no obvious uninitialized read: %t46 (frame +40) is
  stored from %t45 before willrt_task_store_result reads it, and the gc map
  slots (+8 items, +16 out, +32 list temp) are all written first.
- So an -O2 optimization is hiding the defect (or -O2 eliminates something
  the -O0/-O1 code paths execute). Next: dump the -O1 and -O2 IR for
  map_all.sm with clang -emit-llvm and diff the get/pin/safepoint/call region;
  also try -O1 with -fno-inline to see if inlining of double is the trigger.
## Inline is not the hider; -O2 IR looks sane (round 134)

- `will run . -- -O1 -fno-inline` and `-- -O2 -fno-inline` both crash, while
  plain -O2 passes. So the hider is an -O2 optimization other than inlining
  (likely attributes/scalar work that removes a bad read).
- clang -O2 -fno-inline on the generated module.ll yields
  `define internal noundef i32 @probefuncarg.map_all.sm(ptr initializes((32, 40)) %frame)`,
  and the body is structurally sane (alloc+store list temp, safepoint 0,
  read barrier+pin items, load element, safepoint 3, constant-folded double,
  pin out, append). LLVM inferred that the function initializes frame bytes
  32..39 (the list temp), which lets it drop an uninitialized-read UB only the
  -O2 pipeline eliminates.
- Next: check whether any path reads frame +32 (list temp) or the result slot
  before its store at -O0 (e.g. the task entry runs the sm before the frame
  field is written, or a drop/handler reads it), and add an explicit zero init
  for the sm frame slots at construction time.
## Frame zeroing: dead helper, partial init in ctor (round 135)

- emitGeneralFrameZero exists only in statemachine_general_drop.go with no
  caller (dead code). The task constructor actually emits per-slot null
  stores: frame +8 (items), +16 (out), +32 (list temp) and +0 (state); the
  slots +24 (e), +28 (v) and +40 (temp) stay uninitialized.
- In map_all.sm those slots are written before read, so the -O0 crash is not
  explained by them alone, but drop/panic paths could read them, and LLVM's
  `initializes((32, 40))` on the sm parameter shows exactly this gap.
- Next: emit a full-frame memset (or zero every slot) in the task constructor
  instead of the per-slot stores, remove the dead helper, then re-run the
  probefuncarg run/build differential.
## Full-frame memset does not change the crash (round 135)

- Replacing emitGeneralFrameZero with a whole-frame llvm.memset in the task
  constructor (frame +0..size) still crashes probefuncarg at -O0, so the
  uninitialized +24/+28/+40 slots are not the trigger.
- The IR has %v.out (+16) written by the gc write barrier, and read barriers
  and task_store_result use it after that; layout and gc map are fine.
- Reverted; tree green. Next: instrument the runtime path (add temporary prints
  in willrt_task_store_result/join or run the probe under Application
  Verifier) to catch the exact faulting instruction, since the generated IR
  itself looks correct.
## lldb pinpoints the -O0 crash in the call safepoint (round 136)

- built probefuncarg at -O0 and ran it under lldb: access violation at
  probefuncarg.map_all.sm +418 (`movq -0x8(%rbx), %rax`, address
  0x202568c0040). That offset is the `willrt_gc_safepoint(3)` call
  (WILLRT_GC_SAFEPOINT_CALL per contract_test): its gc_safepoint_poll reads a
  garbage lane pointer from %rbx.
- So on the task worker thread the GC lane/TLS state is not initialized or not
  attached, and poll dereferences it. -O2 removes the call safepoint, which is
  why build passes. This is a real GC/TLS bug, not an IR shape issue.
- Next: check how a task worker thread attaches to the GC before running the
  state machine (willrt_task_run / task thread entry), and make the lane
  record available (attach or initialize TLS) so safepoint poll is safe.
## -O0 crash is willrt_gc_pin on a garbage items pointer (round 137)

- lldb: fault at map_all.sm +418 (`movq -0x8(%rbx), %rax`), which matches the
  atomic load of header->state inside willrt_gc_pin (heap.c:424); rbx is the
  object argument (garbage).
- The pinned object is the loop's items list: IR loads frame +8
  (%v.sm.param.10.1), read-barrier it, then willrt_gc_pin. The task ctor
  stores arg0 into +8, so the garbage means arg0 (the spawn argument) or the
  +8 slot is not the expected items value.
- Next: print/`memory read` frame +8 and arg0 under lldb at the pin site (or
  add a temporary assert in the ctor), and check finishAsyncSpawnArgs'
  argument order for a generic instantiation.
## gc_pin header pointer carries an object tag (round 138)

- lldb registers at the crash: object/tag in rdi=0x22DD63E0040 (tag 0x22 top
  bits), rax=0x22DD63E0048 (object+8), rbx=0x20DD63E0048. After WILLRT_GC_UNTAG
  the object is 0xD63E0040, header = 0xD63E0038, and header->state should live
  at 0xD63E0040. The faulting rbx is 0x20DD63E0048 - the untagged address plus
  tag 0x20 - which is unmapped.
- So willrt_gc_pin computes the state address from a pointer that still carries
  a tag (or applies the tag before forming the atomic address), depending on
  how gc_header_from_object/WILLRT_GC_UNTAG are defined. Next: check those two
  helpers in heap.c/gc.h and whether the object was tagged twice (e.g. spawn
  copied a tagged pointer and the ctor tagged again).
## pin object itself is an unmapped address (round 139)

- gc_header_from_object is a plain object - PAYLOAD_OFFSET, and gc_pin untags
  first, so header math is fine. The lldb fault address 0x20DD63E0040 is the
  header->state address, i.e. the pin object (untagged) is exactly
  0x20DD63E0040, which is unmapped: the items pointer handed to pin is garbage.
- Its source chain: main loads xs (read barrier slot) -> map_all.task(arg0) ->
  task ctor stores arg0 into frame +8 -> sm read-barriers +8 -> pin. One of
  those values is wrong (likely the spawn marshalling of the list argument for
  the generic-free async callee, or main's xs slot).
- Next: print main's xs slot and the ctor's arg0 under lldb (breakpoint at
  probefuncarg.map_all.task) to see which step produces 0x20DD63E0040.
## The -O0 crash is intermittent and GC-timing related (round 140)

- Repeated `will run` on probefuncarg: run 1 exits 0, runs 2 and 3 crash
  0xC0000005. So the -O0 fault depends on GC timing (safepoint/allocations),
  which is why plain -O2 (call safepoints removed) is stable.
- The fault is willrt_gc_pin reading header->state on a garbage object; that
  object is the items list handed to the task, so a GC cycle either forwards
  or overwrites the frame +8 slot incorrectly around the call safepoint.
- Next: breakpoint willrt_gc_pin in lldb (C symbol is available) and inspect
  its argument on the failing run, plus check the general frame gc scan
  (gc stack map use) for the +8 slot across a collection.
## Spawn arg handling looks correct; need a runtime breakpoint (round 141)

- finishAsyncSpawnArgs moves owned arguments by zeroing the source
  (zeroTransferredSource), and the task ctor reads arg0 (read-barrier of xs)
  before that zeroing, storing it into frame +8. So the ctor input is sane in
  the IR.
- The failing pin still receives an unmapped object intermittently, tied to GC
  timing, so the next step has to be a runtime breakpoint on willrt_gc_pin
  (C symbol resolves in lldb) with a condition or repeated runs, inspecting its
  argument and the sm frame +8 value on the crashing iteration.
- Worktree clean; std/parallel and probes remain uncommitted.
## lldb breakpoint on gc_pin does not resolve (round 142)

- The runtime binary carries no debug info, so `breakpoint set -n
  willrt_gc_pin` stays pending; the process still faults inside the function
  (before the breakpoint would fire). Register dumps at the fault show the
  same pattern as before (object from frame +8 is unmapped).
- To see the argument and the +8 slot at the pin site, the next step needs
  either a -g build of the runtime + probe (will build with -g) or a temporary
  fprintf in willrt_gc_pin with a rebuilt runtime.
- Worktree clean; std/parallel and probes remain uncommitted.
## -g build resolves the pin breakpoint, but the fault is earlier (round 143)

- `will build . -- -O0 -g` succeeds; lldb resolves `willrt_gc_pin` at
  0x140006e90, yet the process still faults in map_all.sm +418 before the
  breakpoint fires. That instruction reads header->state, which both
  willrt_gc_read_barrier_slot and an inlined pin path do, so the crash happens
  in the read-barrier/pin step on the garbage items value from frame +8.
- The -g build now gives resolvable runtime frames, so next: breakpoint on
  willrt_gc_read_barrier_slot (also resolvable now) and on the sm frame use,
  then print frame +8 and its owner to find who wrote the unmapped address.
- Worktree clean; std/parallel and probes remain uncommitted.
## lldb breakpoints resolve but do not stop on Windows (round 144)

- With the -g build, `willrt_gc_read_barrier` resolves at 0x14000a100, yet the
  run that exited 0 never hit it, and the read_barrier_slot breakpoint did not
  fire either on the crashing run. lldb's breakpoints on these runtime
  functions are unreliable here (static link / no PDB).
- So the next reliable step is runtime instrumentation: add a temporary
  fprintf in willrt_gc_read_barrier/read_barrier_slot/pin that prints the
  slot address, its value and the frame pointer, rebuild the runtime, and run
  probefuncarg until the crash prints the garbage value's origin.
- Worktree clean; std/parallel and probes remain uncommitted.
## Runtime instrumentation needs the exact signature (round 145)

- The fprintf edit did not apply (signature text mismatch), so the runtime
  rebuild from WILL_RUNTIME sources (will compiles them, user cache) produced
  no prints. Note: clearing %LOCALAPPDATA%\will + WILL_CACHE makes will rebuild
  the runtime from source, so instrumentation does take effect once inserted.
- probefuncarg crashed again (0xC0000005) on that run.
- Next: print the exact willrt_gc_read_barrier definition text, insert the
  fprintf after the opening brace, rebuild runtime (clean caches), and capture
  the RB lines around the crash to see the garbage pointer's origin.
## Runtime prints show only the main xs barrier (round 145)

- After inserting `fprintf(stderr, "RB %p\n", ref)` in willrt_gc_read_barrier
  and rebuilding the runtime from WILL_RUNTIME sources (clean user cache), the
  line prints once per run: the main xs barrier (e.g. 0x2A109780048, a valid
  cage address). On crashing runs it prints only that one line, so the sm's
  read-barrier on frame +8 never runs, while the fault still happens at
  map_all.sm +418 (the pin/read-barrier instruction sequence).
- So the bad pointer comes from the pin path, not the barrier: next
  instrument willrt_gc_pin itself (heap.c) with the same fprintf and re-run.
- Reverted barrier.c; tree clean.
## Pin sequence: the faulting object is a valid cage address on an uncommitted page (round 146)

- Instrumented willrt_gc_pin. The fixed prefix is
  0x10000000008, 0x10000000008, 0x0, 0x10000010008; the fifth pin (main's xs)
  is the interesting one: on the passing run its object is a normal cage
  address and the program continues with more pins, while on crashing runs the
  same step gets a cage-shaped address whose page is not committed, so
  header->state faults.
- That means gc_alloc handed out (or pin was given) an address inside the cage
  whose page is not mapped/committed yet - an occasional GC page-commit issue,
  not a language-level bad pointer.
- Next: inspect the cage/page commit path (virtual memory reserve vs commit) in
  the GC page/region code and how safepoint timing can observe a page before
  its commit; reverted the heap.c instrumentation.
## Not a cage page: the pin object is an unmapped heap-shaped pointer (round 147)

- The runtime has no cage code at all (no reserve/commit split either); platform
  win.c only exposes allocate/reserve/commit helpers, and the GC uses
  willrt_alloc, so the faulting 0x21F5B320048 is a heap-shaped but unmapped
  address, i.e. a stale/freed pointer handed to pin.
- Given the print order, the stale pointer appears at the fifth pin (main's xs
  list) on failing runs, after the spawn move zeroes xs. That points at a
  GC/move interaction: the task argument (arg0) is read before the zeroing in
  IR, but the value pinned later looks freed.
- Next: inspect gc_alloc/free for the list object (does the move-based spawn
  free or zero the object rather than the slot?) and the main frame gc map
  around the spawn call.
## zeroTransferredSource only zeroes slots; the page is still a GC alloc question (round 148)

- zeroTransferredSource for a name/var just storeZero's the slot; it never
  frees the object, so the move-based spawn does not explain a freed object.
- The fifth pin in the print sequence is main's xs pin of the gc_alloc result
  (an SSA value held pinned), yet its page faults as unmapped - so the object
  returned by gc_alloc occasionally lives on an uncommitted/unmapped page.
- Next: read willrt_gc_alloc -> region bump/commit (heap.c/region.c) and the
  win.c allocate path to see how a returned object's page could be unmapped at
  that point; add a temporary fprintf in gc_alloc with the returned address and
  the region base to correlate with the pin print.
## gc_alloc path reviewed; region reuse needs the print (round 149)

- willrt_gc_alloc -> gc_alloc (active_alloc counters) -> gc_alloc_inner (bump in
  a region) or gc_alloc_large; regions come from gc_region_alloc, whose base is
  a platform-allocate (MEM_RESERVE|COMMIT) page, and region.c only recycles
  mapped regions (its header says no mmap/munmap there).
- The IR pins immediately after gc_alloc (%t2 -> %t3), so an intermediate GC
  cannot explain an unmapped page unless the region is released inside
  gc_alloc or the reused region's base is stale.
- Next: temporary fprintf in gc_alloc_inner with region base/offset and the
  returned payload, plus in gc_region_alloc/free, to correlate the fifth pin's
  address with region lifecycle on a failing run.
## Only three allocs happen; pin 5 gets a non-allocated address (round 150)

- With ALLOC/REGION prints: one REGION ALLOC (order 0) and exactly three
  ALLOCs, all inside region 0x10000000000 at off 0/32/65536.
- The fifth PIN on failing runs receives 0x21F5B320048, which is not any of
  those three alloc results, so the sm frame +8 slot no longer holds the spawn
  argument at that point: something overwrote it between task construction and
  the sm's read-barrier/pin (GC frame scan/forward using a wrong slot or a
  frame layout overlap).
- Next: print frame +8 right after the task ctor stores arg0 and right before
  the sm reads it (temporary fprintf in the ctor/sm entry), or check the gc map
  offsets against the ctor's stores.
- Reverted the runtime prints.
## Task frame +8 is null at run time (round 151)

- Temporary print in willrt_task_run shows task->frame +8 = 0 and +16 = 0 at
  entry, so the sm receives a null items pointer (the pin then faults on a
  garbage-looking address built from it).
- main's xs slot is written with willrt_gc_write_barrier(ptr %v.xs, ptr %t72);
  the task ctor stores arg0 (= read-barrier of %v.xs) into frame +8. If +8 is
  0, then either %t72 is null (xs's new list) or the read-barrier returned null
  before the write became visible.
- Reverted the task.c print; next: inspect %t72 (xs's list allocation) and the
  ordering between the write barrier and the spawn argument read.
## %t72 comes from an initialized list temp; task +8 still 0 (round 151)

- IR: %v.list.47 (frame +56) is stored from %t63 (list alloc), pinned, then
  %t72 = load %v.list.47 and written into %v.xs (frame +8) through
  willrt_gc_write_barrier; %t93 = read-barrier(%v.xs) feeds map_all.task arg0.
  All sources are non-null in the IR, yet willrt_task_run printed frame +8 = 0.
- So either the write barrier did not store into %v.xs in that run, or the read
  happened before the write became visible (main is a general state machine;
  the slot may live in a frame copy). Next: print %v.xs right after the write
  barrier and at the spawn call, or check whether the sm frame pointer used by
  the barrier differs from the frame the task ctor sees.
## Write barriers show xs stored correctly; ctor store to +8 is the gap (round 152)

- WB prints: slot 0x2EE094900118 <- 0x10000000008 (xs slot written with the
  list), then 0x2EE09490140 <- heap, then 0x2EE097D0050 <- 0x10000010008.
  So the xs write barrier does store the list value.
- Yet willrt_task_run printed task->frame +8 = 0, so the constructor's
  `store arg0 -> frame +8` (IR %t53/%t54) is not reflected at run time. The
  remaining suspect is the task constructor lowering itself: the arg0 read or
  the store offset in the emitted ctor under -O0.
- Next: print in the ctor (IR-level: add a temporary call) or read the ctor's
  disassembly to see whether the store targets the same frame it passes to
  willrt_task_new_gc.
- Reverted barrier.c.
## Frame +8 is correct at task_new_gc but zero at task_run (round 153)

- Printed in willrt_task_new_gc: frame +8 = 0x10000000028 (the list) at
  construction. In willrt_task_run the same frame +8 is 0.
- willrt_task_new_impl only sets task->frame; neither run nor run_auto touches
  the frame. So the zeroing happens in generated code between the ctor call and
  task_run - the prime suspect is finishAsyncSpawnArgs' zeroTransferredSource,
  which may resolve the moved xs place to the task frame +8 instead of main's
  xs slot.
- Next: check what place zeroTransferredSource picks for the spawn argument
  (main's xs vs task frame) and add a guard that it never targets the task
  frame.
## IR writes arg0 at ctor line 127; +8 is zero at run entry (round 154)

- The ctor IR in pfa.ll:115-141 allocates the 48-byte frame, stores nulls,
  stores state 0, loads arg0 into %t54 and stores it at frame +8 (line 127),
  then calls willrt_task_new_gc(frame) at 134/139 and run_auto/run at 136/141.
  willrt_task_new_impl only sets task->frame, and run/run_auto do not touch the
  frame, yet the run-entry print showed +8 = 0.
- Two possibilities remain: the print ran after the step already zeroed the
  slot (sm's init writes), or task->frame was reassigned. Next: print +8 at the
  very first instruction of willrt_task_run and again right after
  willrt_task_run_step returns, to separate "before step" from "after step".
## RUN entry already sees +8 = 0 (round 155)

- Printed at the very first line of willrt_task_run: +8 = 0, while
  willrt_task_new_gc saw +8 = 0x10000000028. So the zeroing happens between the
  ctor call and the run entry.
- The IR between them is just willrt_task_new_gc (134/139) and
  task_run_auto/task_run (136/141). willrt_task_new_impl does not touch the
  frame; next suspect is willrt_task_run_auto's own path (it runs the step in
  the lane branch) or a generated store right after new_gc.
- Next: print +8 at run_auto entry and read run_auto lines 1000-1020 to see
  whether it runs the step before run, then confirm with IR order.
## AUTO entry has the arg; the run print may be a different task (round 156)

- willrt_task_run_auto entry prints +8 = 0x10000000028 (correct), and run_auto's
  lane-started==0 path calls willrt_task_run_step itself.
- The earlier willrt_task_run print showed +8 = 0; since AUTO holds the arg, the
  zero-print likely belongs to a different task/frame than the AUTO one (e.g.
  main's own task whose +8 slot is the xs variable, not the map_all argument).
- Next: print the task pointer/frame pointer in both prints (AUTO and RUN) to
  confirm they are different frames, and correlate with the WB slot address; if
  so, the earlier "frame +8 zero" conclusion was a false lead and the real
  fault is elsewhere (the pin sequence itself).
## AUTO and RUN are different tasks; WB slot is a third frame (round 157)

- AUTO: task 0x1DA77870650 frame 0x1DA77B70040; RUN: task 0x1DA77870040 frame
  0x1DA77830110. They are different tasks/frames, so the earlier "frame +8 = 0"
  belonged to main's own task, not the map_all task.
- The write barrier that stores xs used slot 0x2EE094900118, which is neither of
  those frames (RUN frame +8 = 0x1DA77830118). So the WB slot and the task frame
  are different frame copies: generated code keeps more than one frame view
  (main's sm frame vs the task frame) and they are not the same allocation.
- Next: identify which allocation 0x2EE094900118 belongs to (main sm frame vs a
  copy) and whether CPU-job/frame relocation creates the duplicate; that
  duplicate frame is the likely root of the stale +8 value pin sees.
## Two main frames: the write barrier hits a relocated copy (round 158)

- WB wrote the xs slot at 0x2EE094900118, i.e. its frame is 0x2EE094900110.
  The main task frame in willrt_task_run is 0x1DA77830110. Both carry main's sm
  state, but they are different allocations.
- So main's sm frame is relocated/copied (likely when the task frame is
  installed), the write barrier updates the copy, while the task step reads the
  original frame: xs stays 0 there and the spawn argument is built from a null
  read (arg0 null -> sm items null -> pin sees a garbage address).
- The root cause is the frame relocation path not sharing/syncing state slots
  (xs at +8) between the source frame and the installed task frame.
- Next: find the relocation/install code (task_new taking a frame vs a copy)
  and either keep one frame or copy the whole slot set including +8.
## WB frame and RUN frame are both main's sm frame instances (round 159)

- willrt_task_new_impl only assigns task->frame = frame, so no copy happens
  there. Yet the WB slot's frame (0x2EE094900110) and the RUN task's frame
  (0x1DA77830110) are different allocations, both carrying main's sm state.
- There are two willrt_task_new_gc calls (start -1 and -2) in main's ctor IR,
  and main's sm frame is a single %v.sm.frame = willrt_alloc(160); the two
  distinct frames therefore suggest main's sm frame was allocated twice (two
  main task instances) or the frame pointer differs between the two calls.
- Next: print in willrt_task_new_gc the frame pointer and in main's sm entry the
  frame pointer, counting allocations of size 160, to see whether main's state
  machine is instantiated twice.
## One NEWGC only; main's frame appears twice via entry vs gen frame (round 160)

- NEWGC fires exactly once: frame 0x17B61710040, size 48, step map_all.sm - the
  map_all task, and its +8 is the arg (verified earlier).
- RUN's task is main's (frame 0x1DA77830110, created by the runtime entry),
  while the write barrier's slot sits in frame 0x2EE094900110, which is gen's
  %v.sm.frame = willrt_alloc(160). So main's state machine runs over two frames:
  the runtime entry's frame and the generated one.
- Next: inspect how willrt_entry starts main.sm (which frame it passes) and
  whether generated main.sm expects the frame it allocates; the mismatch is the
  root of xs landing in one frame while the task step reads the other.
## Runtime has no willrt_entry; main has two task launches (round 161)

- grep finds no willrt_entry in the runtime: main's task comes from generated
  will$main.task (which allocates %v.sm.frame = willrt_alloc(160), 0x2EE...) and
  the RUN print showed a different main task/frame (0x1DA...), so main is
  launched twice or two task objects wrap different frames.
- Next: grep the generated IR for the will$main.task calls and willrt_task_run
  call sites to see who launches main and with which frame; the second launch is
  the suspect.
## main task frame and WB frame are different allocations (round 162)

- IR: will$main.task (line 377) allocates %v.sm.frame = willrt_alloc(160),
  builds the task and willrt_task_run at 403 runs it - that is the 0x1DA...
  frame. The write barrier that stores xs runs in main.sm with a frame at
  0x2EE..., so main.sm executes over two distinct 160-byte frames.
- So either will$main.task is entered twice (two allocs) or main.sm is called
  directly (not through the task) with its own frame. The WB slot belongs to the
  direct/other frame, while the task step reads the task frame's +8 (null).
- Next: grep the IR for `call @probefuncarg.will$main.sm` (direct sm calls) and
  count willrt_alloc(160) sites to find the second frame's origin.
## main.sm runs only through its task; WB slot is not main's frame (round 163)

- IR: will$main.sm is defined once (147) and launched only through
  willrt_task_new_drop_gc at 396/401 with the single %v.sm.frame from
  willrt_alloc(160) at 379. There is no direct call to will$main.sm.
- Therefore there is exactly one main frame, and the write barrier's slot
  0x2EE094900118 cannot be main's frame +8 (which lives at the task frame
  0x1DA77830118). The earlier assumption that 0x2EE was main's sm frame was
  wrong: the WB slot belongs to some other object.
- Next: print %v.xs's real address at sm entry and compare with WB slot
  0x2EE094900118 to identify which allocation that slot belongs to (possibly
  map_all's 48-byte frame +8, i.e. the ctor's own store with a write barrier).
## WB slot (0x2EE) differs from main task frame (0x1DA); entry path suspect (round 164)

- IR has exactly one alloc(160) and main.sm is launched only via the task
  (willrt_task_new_drop_gc at 396/401). Yet the write-barrier slot 0x2EE094900118
  is not the task frame +8 (0x1DA77830118), nor map_all's frame +8
  (0x17B61710048).
- So 0x2EE is another main.sm frame, which can only come from the program entry
  path: the generated will$main entry (or the runtime call into it) passes its
  own frame to main.sm once, while the task path passes the alloc(160) frame.
- Next: inspect the generated will$main/entry IR (the function that the runtime
  calls at startup) to see what frame it passes to main.sm, and whether main.sm
  is reachable from both the entry and the task.
## No will$main entry function in IR (round 165)

- The IR defines only will$main.sm (147), will$main.task (377) and
  will$main.drop (409); there is no separate will$main entry function, so
  main.sm is started only through the task (396/401/403) with the single
  alloc(160) frame.
- The WB slot 0x2EE still has no known owner; it must be another allocation
  (possibly a heap object whose first word the barrier treats as a reference
  slot, or the map_all task frame under a different address).
- Next: print in willrt_gc_write_barrier the caller return address
  (__builtin_return_address(0)) and slot to map each WB to its originating
  function, which will identify the 0x2EE owner directly.
## WB return addresses are all main.sm; slots are +0x18/+0x40/+0x50 (round 166)

- With __builtin_return_address printed, the three write barriers return into
  will$main.sm and write frame offsets +0x18, +0x40, +0x50 - none of them is
  xs's slot (+8). So the xs store is not going through the write barrier at
  all (a plain store), and my earlier assumption that the WB slot was xs was
  wrong.
- This invalidates the last few rounds' chain: the pin garbage is not explained
  by a WB/xs mismatch. Next: print the actual %v.xs value right after its store
  and at the spawn call (temporary check in the sm body via a runtime helper or
  by inspecting the disassembly around the store), and align that with the task
  arg0 read.
## xs store is plain; RUN +8=0 may be the post-move state (round 167)

- IR: xs is written with a plain store (%t72 = load %v.list.47, stored into
  %v.xs at +8); the task arg0 reads it via a read barrier. All sources are
  non-null, and the WB prints showed no barrier for +8.
- The "RUN +8 = 0" print belongs to main's own task and may be observed after
  main's step already moved xs (spawn zeroes the source), so it is not
  necessarily a bug.
- The real fault stays in map_all's sm: pin on items (+8) fails on some runs.
  Next: print the sm's +8 value right after the task ctor stores arg0 and right
  before the read barrier/pin (temporary runtime helper called from the IR, or
  a hand-edited module.ll for a one-off probe).
## AUTO sees the arg; read barrier may forward it to an unmapped address (round 168)

- run_auto entry prints map_all frame +8 = arg (0x10000000028, valid). The sm
  never stores +8 again (slots are +16/+24/+28/+32/+40), and the read barrier
  should return the arg for pin.
- Yet pin faults on a cage-shaped unmapped object, so the read barrier itself
  likely resolves a forwarding/self-heal for the arg and returns a bad address
  (the barrier's forwarded branch / gc_object_forward path).
- Next: instrument gc_read_barrier (barrier.c) to print ref and its return for
  the sm's +8 call, and check whether the returned address differs from the
  input on failing runs.
## Read barrier returns input; the list is already freed (round 169)

- willrt_gc_read_barrier prints in = out (no forwarding): 0x23159850048 to
  0x23159850048 on the crashing run, and a different but valid-looking address
  on a passing run.
- So pin receives the raw arg and faults because that page is unmapped: the
  list object was already freed/unmapped before the sm pinned it. The address is
  neither in the GC region (0x10000000000) nor the map_all frame, so it is a
  willrt_alloc object that has been released.
- That means the task frame's +8 gc root did not keep the list alive (the gc
  stack map claims +8 is a ptr list, so either the frame address passed to the
  collector differs from the sm frame or the map is not applied).
- Reverted barrier.c. Next: print the task frame pointer at willrt_task_run and
  the frame the GC stack map scans for it, to see whether they match.
## Task frame gc scan looks correct on paper (round 170)

- willrt_task_scan_gc walks willrt_all_tasks, reads each task's gc_frame_map and
  visits frame + slot->slot for reference slots, honoring flags (inline scan vs
  slot visit). For map_all's map (slots 8/16/32, frame_size 48) the +8 items
  slot should be rooted.
- Yet the list is freed before pin, so either the task is not in the
  willrt_all_tasks list at scan time, or task->frame/task->gc_frame_map is not
  what we think. Next: print in willrt_task_scan_gc the task pointer, frame and
  frame_map while scanning map_all, and compare with the AUTO print.
## AUTO +8 = arg, but the read barrier's input is a different address (round 171)

- The GC scan print never fired (no SCAN line before the crash), so the list was
  not freed by a collection: read_barrier printed in = 0x231... while AUTO saw
  +8 = arg 0x100... - i.e. the sm frame +8 changed between task start and the
  read, to a cage-shaped but unmapped address.
- The sm never stores +8 in the IR (only param slot loads and +16/+24/+28/+32/
  +40 stores), so the write must come from outside the sm body or the frame
  pointer differs in step. Next: grep the IR for stores into %v.sm.param.10.1
  and check the sm body between entry and the read barrier.
- No runtime instrumentation remains; tree clean.
## Last remaining writer: finishAsyncSpawnArgs / zeroTransferredSource (round 172)

- willrt_task_run_step only sets willrt_task_self and calls task->step(task->frame);
  it never touches the frame. main.sm reads %frame+8 once (line 15/47) and never
  stores it. The ctor store (NEWGC print showed +8 = arg) is therefore the only
  legitimate write, and something after it changes +8 to a garbage address.
- Everything runtime/GC/barrier/scan related has been ruled out. The remaining
  candidate is generated code between the ctor and run: finishAsyncSpawnArgs'
  zeroTransferredSource, whose place resolution may target the sm frame +8 (same
  offset 8) instead of main's xs slot.
- Next: print the place pointer that zeroTransferredSource uses for the spawn
  argument (gen side) or add a temporary store guard that aborts if it targets
  the sm frame.
## zeroTransferredSource writes main's xs slot, not the sm frame (round 173)

- Reading zeroTransferredSource: it resolves the moved value with g.place(id)
  and storeZero(src, typ). For the spawn argument (name xs) that is main's
  %v.xs slot - the same offset 8 but a different frame than the sm's +8. So it
  does not touch the sm frame.
- Combined with earlier rounds (runtime step, GC scan, barriers, entry path all
  ruled out), the sm frame +8 rewrite has no identified writer in gen or
  runtime sources. The remaining step is instruction-level: disassemble
  there and record which store writes the garbage pointer, then map it back to
  the function it came from (a debugger watchpoint on the address is the
  cleanest next move).
## Static investigation exhausted for P0 (round 174)

- All source-level candidates for the sm frame +8 rewrite are now ruled out:
  runtime step, task construction, GC scan, write/read barriers, entry path and
  zeroTransferredSource (which writes main's xs slot, not the task frame).
- The next step must be runtime instrumentation independent of lldb (which has
  been unreliable here): take the emitted module.ll, insert a debug print (or
  an abort when +8 changes) at the sm entry and before the read barrier, then
  compile that file with instead of the generated one.
- Worktree clean; std/parallel and probes remain uncommitted.
## Instrumentation plan settled; implementation next round (round 175)

- To find the sm frame +8 writer without relying on lldb, the plan is a runtime
  helper (willrt_debug_ptr) printed from the generated sm entry, plus an abort
  when +8 changes between entry and the read barrier.
- Attempted the runtime side (temporary debug function) but ran out of round
  time before wiring the gen declaration/call; nothing committed and no
  worktree changes were left behind.
- Worktree: std/parallel and probes only, PROGRESS updated.
## Debug print reached the sm; runtime debug.c is not in the build (round 176)

- Added runtime/core/task/src/debug.c (willrt_debug_ptr with fprintf) and a call
  at sm.init in the general emitter. After fixing placement (after the label)
  and operand (%frame), the module compiles and links fail: willrt_debug_ptr is
  undefined because will compiles its runtime from a fixed source list that
  does not include the new file.
- Reverted the emitter change and removed debug.c; tree clean.
- Next: either add the helper to an existing runtime source (task.c) or wire
  debug.c into the runtime build list, then rerun the probe.
## Breakthrough: two main.sm frames (round 177)

- Instrumented the general machine (willrt_debug_ptr in task.c, called at
  sm.init) and printed each sm frame's +8:
    main.sm  frame 0x1AA4C870110  +8 = 0
    map_all.sm frame 0x1AA4CBB0040 +8 = 0x10000000028 (arg, correct)
- main.sm runs with +8 = 0 (xs unset) while the write barrier had stored xs
  into a *different* main frame (slot 0x172A85A0118). So main.sm is executed
  over two distinct frame instances: one receives the write barrier store, the
  other is what the sm steps actually use.
- That is the root cause of the stale arg: map_all gets its arg from the xs
  slot of the frame main's step reads, which was never written.
- Instrumentation reverted; tree clean. Next: find the second main.sm frame's
  origin (two task launches or a duplicated sm) and unify them.
## Only two sm entries; the WB frame belongs to the ctor's frame (round 178)

- The DBG instrument fired exactly twice: main.sm frame 0x1AA4C870110 (+8 = 0)
  and map_all.sm frame 0x1AA4CBB0040 (+8 = arg). So main.sm runs once.
- Yet the write barrier stored xs into slot 0x172A85A0118, i.e. frame
  0x172A85A0110 - the ctor's %v.sm.frame (willrt_alloc(160)) - not the frame
  main.sm actually ran with (0x1AA4...). task->frame should be the ctor frame
  and willrt_task_run_step calls task->step(task->frame), so these two must be
  equal; they are not.
- Next: print task->frame inside willrt_task_run_step and the %frame at sm
  entry in the same run; the mismatch will show where the frame pointer is
  replaced (likely task->frame assigned later or the step invoked with another
  frame).
## STEP prints show task->frame equals the sm frame; the ctor frame differs (round 179)

- Instrumented willrt_task_run_step and sm.init:
    STEP task 0x14EC4F00040 frame 0x14EC4CE0110   (main)
    DBG frame 0x14EC4CE0110 +8 0                 (main sm's own frame)
    STEP task 0x14EC4F00650 frame 0x14EC5200040  (map_all)
    DBG frame 0x14EC5200040 +8 0x10000000028     (arg, correct)
    STEP task 0x14EC4F00040 frame 0x14EC4CE0110  (main resumes)
- So step and sm agree on the frame. But the constructor's %v.sm.frame (the
  WB slot's frame 0x172A85A0110 in round 177) is a different allocation than
  task->frame: willrt_task_new_drop_gc receives the ctor frame, yet
  task->frame at run time is another one.
- Therefore the ctor's frame and the task's frame diverge inside
  willrt_task_new_impl/task_new_drop_gc. Next: print frame at entry of
  willrt_task_new_impl and the task->frame right after assignment.
## IMPL prints did not fire; link issue was a stale binary (round 180)

- Added prints in willrt_task_new_impl around task->frame = frame. The first run
  failed to link (undefined willrt_debug_ptr) because the $TEMP will binary was
  stale from the previous round's instrumentation; rebuilding will and clearing
  both caches made it run again, but the IMPL prints never appeared before the
  crash.
- So the probe now crashes before willrt_task_new_impl, which is earlier than
  the frames we have been comparing. task.c reverted; tree clean.
- Next: print at the very entry of will$main.task / willrt_task_new_drop_gc to
  locate the new earliest crash point, and rebuild will before every probe run
  (stale binary hazard is now documented).
## DROPGC frame == sm frame; DBG fires before the xs store (round 181)

- Printed willrt_task_new_drop_gc and sm.init:
    DROPGC frame 0x21984AB0110 size 160   (main task ctor)
    DBG    frame 0x21984AB0110 +8 0       (main.sm entry - same frame)
    DBG    frame 0x21984DF0040 +8 0x10000000028 (map_all, arg)
- So task frame and sm frame are the same allocation, and +8 = 0 at sm.init is
  expected: the xs store happens later in the body. The earlier "frame +8 zero"
  alarms were a timing artifact of printing at sm entry.
- The remaining question is the read-barrier input (0x231... seen in round 169)
  which differs from both frames; that is after the store, so next print at the
  read barrier's slot instead of sm entry.
- Instrumentation reverted.
## The read barrier input is main's xs list, not map_all's arg (round 182)

- Round 169's read-barrier print (in = out = 0x231...cage) is main.sm reading
  its own xs slot, while AUTO's 0x10000000028 is map_all's arg. Two different
  sm frames, two different +8 values - both normal. No single frame is
  overwritten; my earlier cross-comparison was mixing frames.
- The remaining real anomaly: the pin on that xs list faults because its page
  is unmapped, while no GC scan ran and no explicit free appears in the path.
  Next: check willrt_task_store_result / task drop paths for a free of the list
  that belongs to main's xs.
## store_result only memcpy; drop runs after the step (round 183)

- willrt_task_store_result copies the result into task->result/inline_result
  under the task lock - no free. The frame drop runs when the task finishes,
  after the step, so it cannot explain an unmapped list at pin time inside the
  step.
- Remaining inconsistency: main's xs list is the gc_alloc'd 0x100... address in
  the IR chain, but the read barrier reads 0x231... from frame +8, and map_all's
  AUTO print shows 0x100... as its arg. So the +8 value seen by main.sm's read
  is still unexplained.
- Next: print in willrt_gc_read_barrier the slot address (not just ref) and the
  caller for the 0x231 read; then print frame +8 right before it in the same
  run to catch the writer between store and read.
## P0 needs a hardware watchpoint on frame +8 (round 184)

- Every source-level and runtime print has been tried: the sm frame and task
  frame agree, store_result only memcpy's, drops run after the step, GC scans
  never execute, and read/write barriers return their inputs. Yet main.sm reads
  0x231... from frame +8 while the IR stored 0x100... there.
- The remaining move is a hardware watchpoint on the exact address
  (frame+8) in lldb, or an injected check after the store that aborts when +8
  changes. This is the only way left to catch the writer.
- Worktree clean; std/parallel and probes remain uncommitted.
## Injected-check plan not implemented this round (round 185)

- The plan is to inject a check right after the xs store (or before the read
  barrier) that prints/aborts when the value changes, avoiding lldb.
- Time ran out before wiring it into gen's body emitter; no worktree changes
  were left.
- Worktree: std/parallel and probes only, PROGRESS updated.
## Injected-check wiring still pending (round 186)

- The injection point for the self-check is the xs store (storeValue/declStmt)
  or the read barrier; wiring it into gen ran out of round time again and no
  worktree changes were left.
- Worktree: std/parallel and probes only, PROGRESS updated. P0 still open.
## Store vs read mismatch narrowed to one store (round 187)

- The read barrier's input (frame +8 of main.sm) is 0x231..., while the IR store
  writes 0x100... (xs list). The sm body never writes +8, so the store itself
  is the last suspect: either its target address is not frame+8 or it executes
  on a different frame than the step's.
- The minimal next experiment is a check injected right after that store (gen
  side), printing the slot address and value; if the store writes 0x100... into
  the right slot, the read must be reading another frame, and vice versa.
- Worktree clean; std/parallel and probes remain uncommitted.
## P0 downgraded: toolchain limit reached; moving to standard library matrix (round 188)

- P0 (run -O0 crashes where build -O2 passes) has exhausted the available
  investigation paths on this host: the sm frame and task frame agree,
  store_result only memcpys, drops run after the step, GC scans never execute,
  barriers echo their input, and every runtime print shows consistent state -
  yet one store (xs -> frame +8) is followed by a different value at the read
  barrier, and neither source inspection nor injected runtime checks (limited by
  the runtime source list) can catch the writer. lldb is unreliable here.
- Full evidence chain is preserved in earlier rounds; P0 stays open but is no
  longer the active work item. Per the standing rule "do not burn rounds on one
  point", the next work item is the standard library per-package matrix.
- Starting point: std/container (stack, queue, deque, ring, heap, tree) -
  inventory what exists, run its tests, and record the matrix.
## P1 found: all std/container packages fail the move checker (round 188)

- Running the per-package tests: container/deque, stack, queue, ring, heap,
  tree all exit 1 with O0002 "a copy is required here and cannot be proven"
  (deque.will:185/190/196/201, heap.will:24/25/41/55, and similar in the
  others).
- So the container packages do not compile under the current move rules - a
  real P1 standard-library gap, independent of the parked P0.
- Next: fix the move-check violations in the container packages (clone or
  restructure the borrowed uses), one package per commit, starting with
  stack (smallest), and record results in the matrix.
## std/container is still built on the deprecated Array (round 189)

- stack.will uses `struct Stack<T> { items: Array<T> }` and Array methods
  (new Array<T>(0), push/get/len/cap/clear/clone). The O0002 at 29:17
  (`Some(s.items.get(n - 1))`) and 54:20 (`grown.push(s.items.get(i))`) is the
  move checker rejecting a borrowed element where an owned value is required.
- The real fix is the container migration that the spec requires: rebuild stack
  (and the other five containers) on the final `list[T]` surface, which removes
  both the deprecated Array and the borrow-to-owned violations.
- This is a multi-package P1; next round starts with stack converted to list[T]
  (peek returns a clone, reserve appends clones), then the same for queue, deque,
  ring, heap and tree.
## stack migrated to list[T]; test now runs but exits 6 (round 190)

- Rewrote std/container/stack/stack.will on the final surface:
  items: list<T>, init = list<T>(0), reserve delegates to list.reserve,
  clone delegates to list.clone. O0002 is gone and the module compiles.
- The stack test now executes but exits 6 with no stdout/stderr, so an
  assertion or expectation mismatch remains. Next: read the stack test and
  compare against the expected stdout.
- Worktree: stack.will modified (uncommitted).
## stack test exits 6: clone does not isolate the item storage (round 191)

- The test at line 32-36 clones the stack, pushes into the copy, then expects
  len(s)==2 and len(copy)==3. It returns 6, so after copy.push(9) s.len()
  changed too: Stack.clone delegates to list.clone, and that clone appears to
  share the backing storage instead of copying it.
- Need to check the list clone contract in desc (deep vs shallow) and the
  compiler's list.clone implementation before deciding whether the bug is in
  std/container or in the built-in list.
- Worktree: stack.will migrated (uncommitted).
## Spec says clone is an independent copy; list.clone is shared (round 191)

- 文本与容器.md states an independent copy is produced by clone (line 24),
  and list[T].clone() is in the API (line 57).
- The stack test proves list.clone shares storage: after clone, pushing into
  the copy changes the original length. So the built-in list.clone (compiler
  side) violates the spec - a P1 in the core container, not in std/container.
- Next: locate the list.clone lowering in internal/gen and make it allocate a
  new backing storage and copy the elements (respecting element semantics).
## listClone looks correct on inspection (round 191)

- internal/gen/listmethods.go:121-185 allocates a fresh object
  (willrt_gc_alloc), a fresh data sequence (gcAllocSequence), memcpys the live
  elements, writes the new data via the untagged barrier and returns the new
  list - an independent copy as the spec requires.
- So the stack test's shared-storage symptom (copy.push changes s.len) must come
  from elsewhere: either Stack.clone's out.items assignment aliases s.items, or
  the generic instantiation passes the wrong receiver. Next: print the item list
  addresses in the stack test (or read the generated IR for stack.clone) to see
  whether out.items really is the new list.
- Worktree: stack.will migrated (uncommitted).
## stack.clone IR is an independent copy (round 192)

- The generated IR for std/container/stack.Stack<i32>.clone allocates a fresh
  list (%t14), a fresh data sequence (%t18), memcpys %t10 elements, writes the
  new data with the untagged barrier, sets len/cap, and returns %t24 - a fully
  independent copy.
- So the stack test's exit 6 cannot be explained by clone aliasing. Next: print
  s.len() and copy.len() in the test (temporary) to see the actual values, and
  check list.push/pop semantics on a cloned list.
- Worktree: stack.will migrated (uncommitted).
## stack test exit 6 is s.len()!=2: list.pop does not shrink the length (round 193)

- Split the assertions: exit 6 (s.len() != 2) fires, not 7. The pop match earlier
  succeeded (v == 3), so list.pop returns the right value but leaves len at 3 -
  the generated list.pop does not decrement the length field.
- Next: read listPop in internal/gen and fix the missing length decrement (with
  a failing test that asserts len after pop on a plain list).
- Worktree: stack.will migrated (uncommitted); test file restored.
## listPop does decrement the length (round 193)

- Reading listPop (listmethods.go:393-443): it loads the length, computes next =
  length - 1, loads the element at next, sets the tag, and stores next back into
  lenField. The implementation is correct in isolation.
- Yet Stack.pop (which calls s.items.pop()) leaves len at 3. So the receiver
  reaching listPop is not the caller's list: likely the `s.items` access copies
  the list struct/value before the method mutates it, or the mut receiver is not
  threaded through the nested member call.
- Next: read the generated IR for Stack<i32>.pop and check whether the pop
  mutates the same list object the stack holds.
## Stack.pop discovery needs the mangled symbol (round 194)

- Looking for "Stack<i32>.pop(" found nothing; the mangled name format differs
  (likely std/container/stack.Stack<i32>.pop or another separator).
- Next: search the emitted IR for ".pop" to get the exact symbol, then read its
  body to see whether it mutates the same list object the stack holds.
- Worktree: stack.will migrated (uncommitted).
## Stack.pop IR is correct, yet the test sees len 3 (round 195)

- std/container/stack.Stack<i32>.pop IR: t1 = s.items, t2 = read barrier, t3 =
  pin(t2), t5 = t3 + 8 (len field), t6 = load len, if len != 0 then t8 = len-1,
  load element, set tag, and store t8 back into t5. So pop mutates the same
  pinned list the stack holds.
- The test still exits 6 (s.len() != 2 after pop). Either Stack.len reads a
  different list, or the list object visible to pop is not the one s.items
  names at that moment. Next: read Stack<i32>.len IR and the test's s.pop call
  site (%v.s) to see whether the receiver slot changes between calls.
- Worktree: stack.will migrated (uncommitted).
## pop and len IR both operate on the same %v.s (round 196)

- Stack.len IR: t1 = s.items, read barrier, pin, t4 = t3+8, load len, unpin,
  ret - the same list field pop mutates. The test calls both through the same
  %v.s alloca (line 12, pop at line 80).
- The IR is therefore consistent, yet the test exits 6 (s.len()!=2). The
  remaining way to see the actual value is a temporary print in the test (std
  show/fmt) or checking whether the test's len call is inlined from a stale
  copy. Next: add a temporary fmt-based print of s.len() and copy.len() in the
  test and read the numbers.
- Worktree: stack.will migrated (uncommitted).
## s.len() is 6: reserve inflates the length (round 197)

- Returning s.len() as the exit code yields 6 (not 3 or 2), so after three
  pushes plus s.reserve(8) the list length reads 6 - reserve evidently increases
  the length instead of only the capacity (or push double counts).
- Next: read internal/gen listReserve and check whether it stores the requested
  capacity into the length field; then fix and add a failing test that asserts
  len is unchanged after reserve.
- Worktree: stack.will migrated (uncommitted); test file restored.
## listGrowTo only writes capacity; len=6 must come from push/layout (round 197)

- listReserve -> listGrowTo loads capacity/length, grows data, memcpys, writes
  the new capacity, and never touches the length field. So reserve cannot be the
  source of len=6.
- The measured len after three pushes + reserve is 6, so either each push
  increments len twice, or the len field getelementptr offset differs between
  push and len/pop (layout mismatch). Next: read listPushValue's len store and
  compare field indices with list.len/pop (all should be index 1).
## push increments len by one per call (round 198)

- listPushValue: lenField/capField/dataField are indices 1/2/0; it loads length,
  grows when full (only capacity is written), stores the element at length, and
  stores length+1 back into lenField. Each call adds exactly one.
- listGrowTo/reserve never writes len. So len=6 after three Stack.push calls
  means Stack.push itself runs the list push twice (or the test loop runs six
  times). Next: read Stack<i32>.push IR to count the list.push invocations.
- Worktree: stack.will migrated (uncommitted).
## Stack.push and reserve IR are both correct (round 199)

- Stack<i32>.push: t4=len(1), t5=cap(2), t6=data(0); loads len/cap; grows when
  full (writes cap only); stores the element at data[len]; stores len+1 into t4.
  One increment per call.
- Stack<i32>.reserve: reads cap/data/len and grows, writing cap only.
- Both match the spec, yet the test measured s.len()=6 after three pushes plus
  reserve. Since s.clone() runs before copy.push(9), the only remaining
  explanation is that the copy still shares the stack's list (clone aliasing)
  or that the test's three pushes ran twice. Next: print s.items and copy.items
  addresses right after clone in the test to settle it.
## s.len() returns different values on consecutive calls (round 200)

- Encoding both lengths as (s.len()*16 + copy.len()) yields 6, while returning
  s.len() alone yielded 6 earlier. These can only both hold if the two s.len()
  calls disagree (e.g. first 6 then 0, making 6*16+copy overflow-ish or 0*16+6).
- An unstable length read points at a GC/mutation issue on the list object or
  an uninitialized field, not at push/reserve/clone codegen. Next: read the
  test IR around the two len calls to see whether one is inlined against a
  stale list pointer, and whether a write barrier/GC step runs between them.
## s.len() is stably 6 across runs (round 201)

- Three runs of the test all exit 6 with s.len() as the return value, so the
  read is stable at 6: three pushes plus reserve leave the length at 6, i.e.
  each push appears to add two (or the list starts at 3).
- The earlier combined expression measuring 6 is likely an artifact of the
  temporary test edit (as i32 truncation), not additional evidence.
- So the inconsistency is between the generated push (+1, verified in IR) and
  the observed +2. Next: run a minimal probe that pushes once on a fresh list
  and prints len (expect 1) to isolate whether Stack.push or list initial state
  is at fault.
- Worktree: stack.will migrated (uncommitted); test file restored.
## Minimal probes: one push gives len 1, two pushes still give 1 (round 202)

- Probe A (plain list): fresh list, one push, len -> exit 1, correct.
- Probe B (Stack): one push, len -> exit 1, correct.
- Probe C (Stack): two pushes, len -> exit 1 (expected 2): the second push's
  length update does not take effect.
- So a single list/stack push is correct and the second push on the same stack
  loses its length write - a reproducible, minimal case to fix (likely the
  second inlined push reads a stale length or the write is dropped).
- Next: read probe C's IR for the two push calls and compare their length
  loads/stores; then fix and add the probe as a regression test.
## Probe C needs the right std require line (round 203)

- Rebuilding the probe failed on will.mod: "require std" is invalid syntax
  (B0002) and without it the import "std/container/stack" is an unknown path
  (B0008). The test corpus will.mod shows the accepted form.
- The minimal reproducer (two Stack pushes leave len at 1) is already established
  from the earlier probe run; next round completes the probe with a correct
  will.mod, emits its IR, and compares the two push length load/stores.
## Probe reproduces 2-push len=1; emit-llvm still failing (round 204)

- With the corrected will.mod (require std@v0.1.0 + replace ../../std), the probe
  runs and exits 1: two Stack pushes leave len at 1, reproducing the bug.
- emit-llvm for the probe fails (code 1); stderr is being checked next. Once the
  IR is available, compare the two push length loads/stores to locate the lost
  update.
## Probe IR shows two pushes on the same %v.s and len +1 each (round 205)

- emit-llvm now succeeds (i32(...) cast). IR: push(%v.s, 7) at line 29,
  push(%v.s, 8) at line 31, len(%v.s) at 33; push body stores t21 = t7 + 1 into
  the length field (line 79). All consistent with len becoming 2.
- Yet run exits 1 (len reads 1). So the second push's length store is lost at
  runtime despite correct IR - a runtime/memory effect around push (pin/unpin,
  GC or list field aliasing), not codegen.
- Next: instrument the runtime side of list push (or add a temporary print of
  the length field before/after each push) to catch the lost update.
## `as` is a downcast operator; `usize as i32` should be rejected (round 206)

- internal/gen/downcast.go handles only "is"/"as" as interface downcasts.
  Numeric conversion in will is T(x) (the test diagnostic itself says "convert
  explicitly with T(x)").
- So the stack test's `s.len() as i32` is a type error that the checker misses:
  at run time it yields 1 instead of 2, which is what made the whole container
  investigation look like a push/len bug. The probe confirms i32(s.len()) == 2.
- Two fixes: (a) reject `as` on non-interface operands in the type checker so
  this class of bug cannot compile; (b) update the stack test to i32(...).
- Next: add the checker rule with a failing test, then fix the test corpus.
## `as` is only handled in gen/downcast.go; the checker has no rule (round 207)

- internal/check has no downcast/`as` handling, so `usize as i32` passes the type
  checker and reaches gen/downcast.go, which treats it as an interface downcast
  and emits wrong code (observed value 1 instead of 2).
- The fix belongs in the checker: reject `as` unless the operand is an interface
  (or the target is an interface), pointing users at T(x) for numeric
  conversion. Next: find where binary/cast expressions are checked and add the
  rule plus a failing test.
## `as` is call-like in infer.go:2108 (round 207)

- grep shows `if call.Name == "is" || call.Name == "as"` at infer.go:2108; the
  exact package path still needs confirmation (infer.go is not under
  internal/check). Next round locates that file and adds the interface-only
  restriction for `as`, with a failing test for `usize as i32`.
## `as` is only validated inside the interface branch (round 208)

- infer.go:2105-2124 implements is/as only when the receiver is an interface
  (isIface). For a non-interface receiver such as `usize as i32`, control falls
  through to the field/method lookups and no diagnostic is produced, so the
  call reaches gen/downcast.go and emits a bogus downcast.
- The fix: when call.Name is "is" or "as", require the receiver (or the target)
  to be an interface and otherwise report a normal type error pointing at T(x).
- Next: add that guard with a failing test (`usize as i32` must reject), then
  fix the stack test to use i32(...).
## Guard added but `as` does not pass through visitSelector (round 209)

- Added the non-interface guard before the NamedKind check in infer.go's selector
  visitor, but `s.len() as i32` still compiles with no diagnostic and exits 1,
  so this code path is not where `as` on a scalar receiver is decided.
- The change builds and is harmless (a guard for interface-less selectors), but
  the effective fix must go where the `as` expression is typed/lowered (likely
  the call/expr inference path). Next: locate the `as` call site handling in
  internal/types and add the rejection there, then add a failing unit test.
- Worktree: infer.go modified (uncommitted).
## The `as` guard is still not reached (round 210)

- internal/types contains only two "as" sites: my guard at 2100 and the
  interface branch at 2114. `s.len() as i32` produces no diagnostic, so this
  selector visitor is not on the path that types it - the call must be handled
  earlier (generic call/expr inference) or resolved straight to a downcast.
- Next: trace how the parser represents `expr as Type` (keyword vs call) and
  where visitCall/inferCall dispatches it; add the rejection at that dispatch
  point instead, with a failing test.
- Worktree: infer.go guard (uncommitted), stack.will migration (uncommitted).
## Locating the call/selector typing entry (round 211)

- internal/types has no visitCall symbol; the selector logic lives in a method
  whose name still needs identifying (grep of func ...call/...selector).
- The `as` guard I added is in that selector method but is not reached for
  `s.len() as i32`, so the call must be typed by a different entry (call
  expression inference, generic instantiation, or the parser's downcast node).
- Next: list the typing entry points and trace `as` from the call node, then add
  the rejection there with a failing test.
## callType is the entry; `as` hits the NameExpr/symbolOf path (round 211)

- infer.go:1426 callType: a Callee that is a NameExpr goes to
  sym := c.symbolOf(name); `as` is not a function symbol, so it falls to the
  default branch with no diagnostic - this is where `usize as i32` slips through.
- Fix: in callType, when call.Callee is NameExpr with Name "as" (or "is") and
  there is no interface operand, emit a type error pointing at T(x) and return
  nil. Next round implements that plus the failing test.
## `as` checkpoint: guard not reachable in 4 attempts; moving on (round 212)

- Tried the guard in the selector visitor and (planned) in callType, but neither
  path types `s.len() as i32`: the AST representation of `as` stays unclear and
  the callType NameExpr branch does not cover a FieldExpr callee. No diagnostic
  was produced in any attempt; infer.go's added guard is harmless but ineffective.
- Per the standing rule (do not burn rounds on one point), the `as`-on-scalar
  rejection is parked alongside the P0 crash and recorded with its evidence. The
  workaround for the corpus is to use i32(...) in the stack test.
- Next: revert the ineffective infer.go guard, fix the stack test cast, verify
  the container test, and commit the container migration; then pick the next
  standard-library package.
## stack test still exits 6; probe with reserve/pop needed (round 213)

- The migrated stack test still returns 6 (s.len() != 2), while the minimal probe
  with two pushes reads len 2 correctly. The difference between the two is
  reserve(8) and pop() (and a third push), so the length is inflated somewhere in
  reserve/pop rather than in push.
- Next: extend the probe to three pushes + reserve(8) + pop, printing len at each
  step, to find which operation adds length; then fix and commit the container
  migration.
- Worktree: stack.will migrated (uncommitted).
## Probe isolates the failure to clone aliasing (round 214)

- Full sequence without clone (3 pushes + reserve + pop) reads len 2 - correct.
- Adding `clone + copy.push(9)` makes the combined read s.len()*10 + c.len() = 33,
  i.e. s.len() = 3 after copy.push: the clone shares storage with the original.
- This is the actual cause of the stack test's exit 6. The clone IR looked
  independent (fresh list + fresh data + memcpy), so the aliasing must come from
  the runtime side (write barrier/pin returning the source list) or the copy
  receiver resolving to the original.
- Next: instrument listClone's object allocation vs the returned pointer, or
  print the two list pointers in the probe, to see whether clone returns the
  source object.
## Clone aliasing: IR looks independent, runtime shares (round 215)

- Reviewing Stack<i32>.clone IR again: %v.list.112 = new list (%t14), elements
  copied into a new data sequence (%t18), %t23 = load %v.list.112, written into
  out.items via write barrier, and out is returned. Every step produces a fresh
  list, yet the probe shows c.push mutating s.
- So either the write barrier stores the source list into out.items, or the
  returned struct is read from the wrong slot (s instead of out). Next: print in
  the probe via exit-code encoding the two list pointers, or add a temporary
  runtime print in listClone comparing %t14 (new object) with the value stored
  into out.items.
- Worktree: stack.will migrated (uncommitted).
## Probe confirms clone aliasing: s.len()==3 after copy.push (round 216)

- Return i32(s.len()) after clone+copy.push exits 3, confirming the copy and the
  original share the list (or the copy's push wrote through to the original).
- The clone IR still reads as fully independent (new object %t14, new data %t18,
  memcpy, write barrier into out.items, return of out). The remaining
  discriminator is whether the value written into out.items is %t14 or the
  source list: next round adds a temporary print of %t14 and of out.items right
  after the barrier store, then removes it once identified.
- Worktree: stack.will migrated (uncommitted).
## listClone debug print did not fire (round 217)

- Added a temporary willrt_debug_ptr call after the object allocation in
  listClone (plus the runtime helper), rebuilt will, and ran the clone probe:
  exit code still 3 (aliasing reproduced) but no DBG line printed, so the edit
  either matched a different function or the call was optimized away.
- Reverted both files to keep the tree clean. Next: insert the print with an
  unambiguous anchor inside listClone (e.g. right before its return
  g.load("ptr", slot)) and verify the emitted IR contains the call.
## go build ./... hit an out-of-memory failure (round 217)

- After reverting the debug edits, `go build ./...` failed with
  "fatal error: runtime: cannot allocate memory" while building
  build/migrate-docs - an environment/memory condition, not a code failure.
  Will retry the build next round after the machine settles; the edited files
  are already reverted and the tree holds only PROGRESS, README, stack.will and
  std/parallel.
## listClone print never runs: it is inlined into Stack.clone (round 218)

- go build ./... recovered (no OOM this time); the temporary print placed before
  listClone's return built cleanly but produced no DBG line - the clone probe
  still exits 3.
- Reason: Stack<i32>.clone's IR already contains the cloned-list construction
  inline (new object/data/memcpy), so listClone is emitted in place and the
  standalone function with my print is never called.
- Reverted the edits. Next: put the diagnostic print where the value is stored
  into out.items inside Stack.clone's inline expansion (or print from the
  caller side), then fix the aliasing.
## listClone inline assumption contradicted (round 219)

- The earlier claim that Stack.clone inlines listClone conflicts with the print
  never firing: the clone IR does contain the list construction, yet the
  diagnostic inserted at listClone's return produced nothing.
- The remaining explanations are (a) the value written into out.items inside
  Stack.clone is not produced by listClone's own return slot, or (b) the print
  landed in an unreferenced branch. Next: put the print in the Stack.clone
  path itself (struct.go call site) right after the items field is written.
- Worktree: stack.will migrated (uncommitted).
## Stack.clone call-site instrumentation is the next step (round 220)

- The aliasing evidence is complete (probe exit 3 after clone+push); what remains
  is confirming whether out.items receives the new list. listClone's own return
  print proved unreachable, so the print has to go at Stack.clone's items-field
  write in internal/gen/struct.go (the generic struct-member assignment path).
- No code changed this round; the fix and instrumentation target is recorded.
- Worktree: stack.will migrated (uncommitted).
## Clone's out.items write is a plain field assignment (round 221)

- struct.go has no clone logic; `out.items = s.items.clone()` lowers through the
  ordinary field-assignment path. The print target is therefore the generator's
  field store (setField/storeField/assign), not a clone-specific routine.
- Next: find that field store, add a temporary print of the stored value and the
  destination, and confirm whether the new list or the source list lands in
  out.items.
## assignStmt resolves the target pointer before the store (round 222)

- gen.go:2831 assignStmt pins the field owner, resolves ptr/typ (direct class
  field address or place), then performs the store. A temporary print of ptr and
  the value right before that store will show whether out.items receives the new
  list or the source list.
- No edit made this round; the insertion point is recorded for the next one.
- Worktree: stack.will migrated (uncommitted).
## assignStmt store site still to be pinpointed (round 223)

- Read further into assignStmt after ptr/typ resolution; the actual store call is
  further down and this round only confirmed the function shape (owner pin,
  place resolution, then store with op handling).
- Next: grep the store invocation inside assignStmt and insert the temporary
  pointer/value print immediately before it, then run the clone probe.
## assignStmt print did not fire either (round 224)

- Inserted a temporary print right after `value, err := g.value(valueID)` in
  assignStmt; the clone probe still exits 3 with no DBG output. So the
  `out.items = s.items.clone()` write does not flow through assignStmt.
- Combined with the earlier listClone return print (also silent), the only place
  that writes out.items is inside listClone's own inline expansion (the
  write_barrier call on the data field seen in the clone IR). The next
  instrumented point is that barrier call site in listmethods.go (dataField /
  willrt_gc_write_barrier_untagged), not the generic assignment path.
- Both temporary edits reverted.
## listClone's barrier site also never fires (round 225)

- Instrumented the willrt_gc_write_barrier_untagged call in listClone's data
  field write: the clone probe still exits 3 with no DBG output.
- Taken with the earlier silent prints at listClone's return and at assignStmt,
  this means listClone is not the routine that emits the clone seen in the IR:
  some other path (a different list-clone lowering, possibly in struct/member
  handling) produces the fresh object/data and the out.items barrier.
- Next: grep for other sites emitting gc_alloc + memcpy for lists
  (listClone is one; there may be a member-assignment clone path) and instrument
  that path instead.
- Reverted both edits.
## Status audit: test matrix and two corrected misjudgements (round 234)

- Ran `will test .` over every tests/**/will.mod package (163 total):
  pass=147, fail=16. Failures cluster as:
  - negative tests whose stdout.txt still mismatches (check/featureoutsideif,
    check/featureundeclared, module/{cycle,cycle/dep,twopaths,wrong-name,app},
    willc/program): the diagnostics themselves are correct (T0010/B0010/B0001),
    so these are corpus/matching issues, not code defects.
  - real defects: std/text/base64* (N0007 duplicate top-level name decode in
    std/encoding/base64), std/parallel (S0001 unexpected token, expected ] in
    the uncommitted WIP), concurrency/{mpmc,pipeline}, container/bits,
    std/base64.
- CORRECTED: tests/std/container/stack now PASSES (`ok example.com/stdtest/stack
  0/0`). The Array->list migration fixed it; the earlier exit 6 was from my
  temporary test edits.
- CORRECTED: the "clone aliasing" theory is wrong. The probe returned 3 because a
  bare `s.pop();` statement is a discard and did not shrink the stack; the real
  test (which matches on s.pop()) passes. listClone and Stack.clone are correct.
- The marker experiment proved listClone IS emitted (`; MARKER listClone
  list[i32]` at IR line 195); earlier silent prints were PowerShell replace
  mismatches, not missing code paths. Always grep the file after editing.

## Deprecated surface purge (round 235, cold gate green)

Removed the deprecated source and lowering surfaces in one batch:

- `parallel` block: deleted `ast.ParallelStmt`, the `parallel` keyword, every
  checker/walker/marks/willc/gen case, and the stale suspendable checks. The
  parser now rejects `parallel { ... }` like any other unknown syntax.
- `rc`/`arc`/`weak_rc`/`weak_arc`/`take_c`: deleted the front-end leftovers,
  `willc` type/release vocabulary, `gen/rc.go` paths (`constructRc`,
  `rcMethodCall`, `upgradeCall`, `rcCallResult`, take_c lowering, rc
  declarations and retain/release branches), `runtime/core/rc`, the runtime
  threaded-table calls, the `willrt_rc_*`/`willrt_weak_*`/`willrt_take_c`
  declarations, the `mem/rc` benchmark, and the tests that only exercised the
  removed handles.
- old `Map`/`Set` spellings: removed from `layout`, `gcmeta`, `check`,
  `types`, `gen/container`, and `gen/containerloop`. The shared dict/set
  lowering now recognizes only `dict[K,V]`/`set[T]`; keys/values always return
  `list`.
- `new T(...)`: deleted the parser branch, `ast.NewExpr`, `newType`, and every
  AST walk case. `Array<T>(...)`, `chan<T>(...)`, local class/struct
  constructors, and imported constructors now share the specified `T(...)`
  form. 104 `.will` files and the Go test fixtures were migrated; `new` is a
  normal identifier again, so a user function named `new` still parses.
- `list<T>`/`dict<...>`/`set<...>` in type and constructor positions: the
  parser now reports `container type arguments use brackets`; all sources use
  `list[T]`/`dict[K,V]`/`set[T]`.
- runtime header and tests no longer list `rc.c`; `willrt_closure_share` keeps
  only the closure share callback. `runtime/core/task` and the C contract
  tests pass without the rc module.
- Stale diagnostics/comments that told users to use `rc`, `arc`, or `new` were
  rewritten. `T0007` is now `CodeBadConstruct`; the old `new-without-init`
  corpus records the current missing-field T0011 instead.

Verification (cold `WILL_CACHE`, fixed `WILL_RUNTIME`/`WILL_CLANG`):

- `go build ./...` PASS; `go vet ./internal/... ./cmd/... ./tools/...` PASS;
  `gofmt -l` empty; `checkcode: clean`.
- `go test -p=1 -count=1 ./internal/... ./cmd/... ./tools/... ./runtime/...`
  PASS (cli 117s, runtime/task 52s).
- `go run ./tools/gate -full -update` rewrote
  `tests/type/new-without-init/stdout.txt` and `tests/willc/nodes/willc.txt`;
  the follow-up cold `go run ./tools/gate -full` ended `gate: ok`.
- `will run tests/build/new` exits 12 after the constructor migration;
  `will run build/probenew` (local class/struct, `Array<i32>`, `chan<i32>`,
  imported `lib.Box<i32>`) exits 15.

Remaining deprecated surfaces, blocked on specification decisions:

- `Array<T>`: still used by std/时间 and the C boundary. Removal waits on the
  recorded `str(list[u8])` conversion gap and the C container/view boundary
  decision in `desc/理由/缺口清单.md`; deleting it now would break std/time
  and the poller/exec shims.
- ~~`std/os/exec` still exposes `Cmd`/`Output`~~ DELETED in round 236; the
  specified `run`/`start`/`Child` surface is still unimplemented.

## Array and old exec surface deleted (round 236, gate intentionally not chased)

Per the user's direction, the remaining deprecated surfaces were removed
directly instead of keeping compatibility to stay green.

- `Array<T>` deleted from `resolve`, `types`, `ast`, `layout`, `gcmeta`,
  `check`, `willc`, and `gen`; `gen/array.go` and `gen/arraymethods.go` are
  deleted. View helpers and `intLitValue` moved to `gen/view.go`, and
  `arrayCallResult` became `gen/listresult.go`.
- All `.will` sources and Go test fixtures were retyped `Array<T>` -> `list[T]`
  (159 files touched). `list` is now a one-word GC reference in
  `llvmTypeText` as well; the old three-word handle paths are gone.
- `raw(list)` now passes the GC payload pointer, whose first three words are
  data/length/capacity, so the existing C poller shims read the same shape.
  `str(list[u8])` reads the payload data/len; `[]u8.clone()` builds a GC list.
- `std/os/exec` (`Cmd`/`Output`/`ExecStatus`) and its three corpora
  (`tests/std/exec`, `tests/std/system/exec`, `tests/concurrency/asyncexec`)
  are deleted. Its C shim is gone with the package.
- Tests that only asserted removed Array three-word ownership/IR were deleted
  (`check` move-field tests, `gen` Array IR tests, `willc` Array release
  tests, `cli` class Array stress and async mut-Array tests).
- List aggregate elements now have a place path (`listGetPlace`,
  `listPushAggregate`), and pattern-bound scalar class references are
  materialized from the pattern value.

Current state, not gate-green:

- `go build ./...`, `gofmt -l`, `go vet`, `checkcode` are clean.
- `go test` for parser/types/check/gen/willc/layout/gcmeta PASS.
- `will run tests/std/container/stack` exits 6; `tests/std/io` exits 5;
  `tests/std/time` and `tests/std/fmt` exit 0.
- `tests/std/container/map` reaches IR but crashes at run time on a
  pattern-bound `Option<list[i32>]`; `tests/std/text/csv` still hits a
  list-slice path. These are list GC-root/place defects exposed by the
  migration, not Array compatibility work.
- `will check std` still reports the pre-existing ring/base64/parallel
  diagnostics recorded in round 234.
- Full `gate -full` was not rerun after the deletion; the user explicitly
  accepted a red gate to avoid stalling the migration.

## Remaining compatibility layers removed (round 237)

- `listElemText` no longer accepts the old `list<T>` angle spelling; only
  `list[T]` is recognized. The parser already rejects the angle form.
- Removed the old predeclared `wait(...)` builtin and the `Cancel`/`Priority`
  predeclared values; neither is in the current language. `task.cancel()` stays.
- Removed the legacy GC-decision gate (`HasClasses`): `gcmeta.DecisionRecords`
  now builds the GC graph for every batch, so list-only batches are not treated
  as legacy.
- Deleted the unused old-Array `constructMapSet`, `eachElement`, and
  `storeContainerHeader` helpers and the stale `Array`/`Map`/`Set` comments.
- Deleted the ignored one-off migration tools under `build/`
  (`migrate-*`, `rename-*`, `retype*`, `strip*`, `fix-*`). No tracked
  compatibility or migration path remains.
- Parser/types/check/gen/willc/layout/gcmeta tests still PASS after the removal.
  `will check std` reports only the pre-existing ring/base64/parallel issues.

## Remaining old surface and share plumbing removed (round 238)

- Deleted the rc-era share/crossing machinery: `gen/share.go`, `Crossing`,
  `crossings`, `crossingPos`, `quietShare`, every `shareValue` call and
  `crossingRecords`; the decision record no longer carries `share` obligations.
- Deleted closure share thunks and `willrt_closure_share`; the closure header
  is now drop + refcount only, and `willrt_closure_new` takes `(bytes, drop)`.
- Removed the old `std/task` helpers `parallel_for`/`parallel_map` and their
  corpus; they are not the specified `std/task` surface.
- Removed the non-specified `std/io/buf.Buffer` and `PooledBuffer` (and the
  `tests/std/iobuf` corpus); `Reader`/`Writer` remain.
- Removed the duplicate public `base64.decode(s, url)`: the internal form is now
  `decode_mode`, so only `decode(s)` and `decode_url(s)` are public.
- Removed the old `parallel` scope field, `wait(...)` checks, stale comments
  that described the removed rc/Array paths, and the ignored one-off migration
  tools under `build/`.

Verification: `go build ./...`, `gofmt -l`, `checkcode` clean; parser, types,
check, gen, willc, gcmeta, runtime/mem, runtime/task, runtime/io tests PASS.
The gate remains intentionally red while the list GC-root defects and the
unimplemented final `std/os/exec`/`std/task` surfaces are worked off.

## Final syntax migration (round 240)

- Deleted every old source form the specification does not contain:
  - `mut T` / `ref T` type prefixes and `mut x` / `ref x` expressions
  - flat receiver declarations `def (recv: T) name(...)`
  - the `from` source clause and its AST, parser, checker, and willc fields
- Migrated 167 flat methods and 214 `mut`/`ref` uses in `std`, `tests`, and
  `bench` into type-body methods with explicit `self`; all `.will` files now
  have zero `mut`, `ref`, or flat receiver declarations.
- Deleted the willc vocabulary and emission for `borrow`, `from`, `mut`, and
  the `ref`/`mutref` type kinds; the index no longer writes or accepts
  `recv="value|ref|mut"`.
- Deleted old syntax tests (`check/borrow_test.go`, `gen/ref_test.go`,
  `gen/async_mut_test.go`), old cross-package method tests, and old
  mutable-scalar ABI tests.
- Deleted nonconforming standard-library material:
  - `std/test/capture.will` (unused capture API); `capture.c/h` became the
    clock-only `clock.c/h`
  - incomplete `std/parallel/` and `tests/std/parallel/`
  - rewrote `std/container/ring` to the specified class/`init(capacity)`/
    `push -> Option[T]` surface
- The mover now treats a value parameter as owned and movable; only pattern
  bindings remain borrow-transfer sites. `will check std` is clean.

Verification:

- `go build ./...`, `gofmt`, `go vet`, and `checkcode` pass.
- `go test ./internal/...` passes after updating index lines and goldens.
- Cold-cache `gate -full -update` then `gate -full` end `gate: ok`, exit 0.
- Every `.will` under `std`, `tests`, and `bench` parses with the new grammar;
  `will check std` exits 0.


## Internal borrow model removed (round 241)

- Removed the remaining compiler-internal borrow representation:
  - `ast.BorrowKind`, `ast.Type.Borrow`, `ast.Type.ViewMut`, `ast.Receiver.Mut`,
    `ast.Param.Mut`, and the `IsRef`/`IsMutRef`/`Unref` helpers
  - `types.Type.borrow`/`viewMut`, `Borrow`, `IsRef`, `IsMutRef`, `ViewMut`,
    `Unref`, and the borrowed-type interner
  - `check/borrow.go`, `check/borrow_pin.go`, and `check/effects.go`
  - the borrow-source, return-root, and mut-effect passes
  - borrow/mut diagnostic codes O0003, O0004, O0005, O0006, O0008 and N0006
- `gen` type helpers now treat every source value as owned; the dead
  ref/mut branches collapse to ordinary values and pointer receivers.
- The mover keeps only concrete transfer rules: pattern bindings stay copies;
  value parameters are owned and movable; no borrow-transfer model remains.
- Deleted the old probes, migration tools, and pre-migration backups under
  `build/`; only generated target directories are recreated by builds.

Verification after the removal:

- `go build ./...`, `gofmt`, `go vet`, and `checkcode` pass.
- `go test ./internal/...` passes (cli 231s).
- `will check std` exits 0.
- Cold-cache `gate -full` ends `gate: ok`, exit 0.


## P0 list migration (round 239)

The list-migration P0 defects are fixed:

1. A user-defined `clone` method was intercepted by the universal clone
   fallback before method dispatch. `Stack.clone` now calls its declared method;
   `build/probestackp0` returns 3334 and `tests/std/container/stack` exits 0.
2. `cloneOwnedValue` treated final containers as "no copy" and never wrote the
   pointer word, so `Option<list[T>>` payloads were null. It now copies the
   pointer while still sharing the payload. `tests/std/container/map` exits 0;
   its nested-list check expects the shared reference the memory volume
   specifies.
3. `sliceElem` recognizes `list[T]`, so `list[lo:hi]` resolves to `[]T`;
   `tests/std/text/csv` compiles and exits 0.
4. Borrowed parameters are the caller's slot address. The frame mirror and
   `classPointerValue` read through `willrt_gc_read_barrier_slot`, so a
   `mut list[u8]` parameter reaches the list pointer. `tests/std/io` exits 0.
5. A class receiver arrives as the object pointer, not as a caller slot. Both
   the prologue mirror and class field access now treat it as a value pointer;
   this removed the `std/encoding/csv` writer access violation.
6. `forList` pins the data sequence for the loop, because the body may reach a
   safepoint while an element address is live.
7. `listFromSequence` reports `list[E]` as its will type instead of the LLVM
   spelling `ptr`; `tests/build/strbytes` (`[]u8.clone()`) emits and runs again.
8. The decision record deduplicates layout lines shared by the front-end
   layout pass and the GC type graph, and the formatter no longer inserts a
   space between `]` and `)`.

Verification:

- P0 matrix (`ClassContainerRoots`, `ClassFieldContainers`,
  `ClassContainerMillion`, `ClassStrLifetime`, `ClassInlineAggregateRoot`,
  `SelectClassLocalRoot`, `DeadlineChildReturnsDeadline`, `ListForcedCycles`,
  `AsyncStructFieldRoot`, `CpuSyncAcrossJobs`) passed three rounds.
- `TestRunClassContainerRoots` alone passed 20 consecutive forced-GC rounds.
- `go test -p=1 -count=1 ./internal/... ./runtime/...` passed (cli 181s,
  runtime/task 68s).
- `tests/std/text/csv`, `tests/std/io`, `tests/std/container/{stack,map}`,
  `tests/std/time`, `tests/std/fmt`, and `tests/build/strbytes` all exit 0.
- `go build ./...`, `gofmt`, `go vet`, and `checkcode` are clean.
- The full gate was re-recorded for the new list/class IR and the cold-cache
  confirmation ended `gate: ok` (exit 0).
- `run`/`build` differential on `tests/std/text/csv`, `tests/std/io`,
  `tests/std/container/stack`, `tests/std/container/map`, and
  `tests/build/strbytes`: identical stdout/stderr and exit 0 for both modes.

## Specified containers and remaining std gaps (round 242)

- Rewrote every `std/container` package to the documented surface:
  - `Stack`, `Queue`, `Deque`, `Ring`, `Heap`, and `Tree` are classes.
  - Constructors take the documented capacity or no argument.
  - `Ring.push` returns the overwritten oldest value.
  - `Heap` is a max heap over `T: Ord` with no comparator closure.
  - `Tree.set`/`delete` return `Option[V]`; `keys`/`values` return ordered
    lists. Tree keeps parallel key/value lists so no nested generic element
    leaks into the class layout.
- Fixed generic class method discovery for package-qualified receivers and
  stopped parameter-for-parameter method instances from being lowered as
  concrete instances.
- Rewrote the six container corpora to the documented surface; all exit 0.
- `will check std` exits 0; cold `gate -full` is green after re-recording.
- Commits: `ec82247 feat: complete the specified container surfaces`.
- Recorded two specification gaps that block the next std packages:
  - `std/task.snapshot` needs a source-level `TaskError` construction surface.
  - `std/parallel` chunk jobs need a CPU-job join surface; an ordinary `def`
    cannot await child CPU jobs, while making the API `async def` would change
    the documented signature.


