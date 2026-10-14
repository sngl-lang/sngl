# Allocation and walk sweep

Working notes for `perf/allocation-sweep`: one branch, one MR at the end.
Delete this file before opening the MR, and move the numbers into the MR
description.

## Baseline (main at 65f4ef1b4, 2026-10-14, quiet box)

| Workload                                 | Allocated | CPU        | Dominated by                                         |
|------------------------------------------|-----------|------------|------------------------------------------------------|
| docs site (`sngl generate website.sngl`) | 12.2 GB   | 120 s      | `ir.Walk` ~78% of CPU                                |
| `TestGolden` (~1150 target builds)       | 6.35 GB   | 28.5 s     | per-target clone 34% alloc, fingerprint 25% CPU      |
| `cmd/sngl` `TestScript`                  | 22.1 GB   | 94 s user  | gtk4 registry decode 52% of alloc                    |
| `TestFixtures`                           | 0.36 GB   | 0.9 s      | already cheap                                        |
| `go test ./...`                          | --        | 12m44 user | wall 4m08; html 243 s, cmd/sngl 168 s, docsgen 159 s |

## How to measure

Prefer CPU totals and bytes over wall clock (other sessions share the box).

- Docs site, after staging `internal/playground/assets/sngl.wasm`:
  `go build -o $S/sngl ./cmd/sngl && SNGL_NO_PROXY=1 SNGL_MEMPROFILE=$S/docs.mem SNGL_CPUPROFILE=$S/docs.cpu $S/sngl generate --allow-eval=go:duckfam.us/sngl/docs --allow-eval=go:duckfam.us/sngl/docs/lookup --platform html --lang none --out $S/site website.sngl`
  Run it twice; the first run after a compiler rebuild pays the cold store.
- Root tests: `go test -c -o $S/root.test . && $S/root.test -test.run '^TestGolden$' -test.memprofile $S/g.mem -test.memprofilerate=65536 -test.cpuprofile $S/g.cpu`
- CLI scripts: `go test -c -o $S/cli.test ./cmd/sngl && cd cmd/sngl && $S/cli.test -test.run '^TestScript$' -test.memprofile … -test.cpuprofile …`
- Per-phase allocation on one example: `go run ./tmp/allocscan examples/todo go/fyne,none/html,kotlin/android`
  (`MEM=file` writes an alloc profile; the first run is the cold store).
- What a per-target clone copies, by owning package: `go run ./tmp/clonescan examples/hello`.
- Totals: `go tool pprof -sample_index=alloc_space -top -cum <bin> <prof>`.

## Steps, in order

Re-measure the affected workload after each step and note the number here.

1. [X] **html walks the whole package per document and per handler.**
   `respellWindowWrites` (`codegen/platform/html/html.go:519`, walk at `:3929`)
   runs once per document over `req.Pkg`; `windowIDs` is memoized on
   `htmlGen`, which is per document, and every `addEventHandler` ->
   `newHTMLTranslator` reaches it; `walkThroughCatch(g.pkg, …)` at `:3687`
   has the same shape. Compute the package-wide half once per compilation and
   walk only `doc.Body` per document. ~94 s of the docs build's 120 s, and
   docsgen's `TestWebsiteProducesContent` (157 s) is the same build. Guard:
   the docs site output stays byte-identical.

2. [X] **gtk4 registry decoded from SNGL text on every cold store.**
   `produceWidgets` (`codegen/platform/gtk4/girstore.go`) decodes the registry
   `produceRegistry` just encoded, through `parser.ParseNativeValue`; every
   check reaches it because every check asks every platform `Unavailable()`,
   which produces the widgets. The 16 scripts with their own
   `SNGL_GENCACHE_DIR` pay it per invocation (11.7 GB of cmd/sngl's 22 GB); a
   CLI run after a compiler rebuild pays 268 MB instead of 25 MB.
   - Memoize the decoded registry per process by entry digest (as
     `internal/plugin` memoizes scheme output).
   - ~~Answer availability from the GIR probe rather than by producing
     widgets.~~ Not done: a stat-only probe moves a malformed or directory
     GIR's report from "platform gtk4 is unavailable" to an error inside
     `import "gir:Gtk-4.0"`. Owner's call: keep the reports and memoize the
     produced registry per process by the GIR's content digest instead.

3. [X] **Unit table rebuilt per literal.** `optimize.parseLiteral` ->
   `interp.UnitLiteralValue` -> `buildUnitTableFromDef` builds a fresh table
   for every unit literal: 0.45 GB, 7% of TestGolden. Memoize by `*ir.UnitDef`
   (`unitTable.Decl` already compares by declaration).

4. [X] **Reflective cloner reads field names per node.** `ir/clone.go`
   `cloneStruct` calls `v.Type().Field(i).Name` several times per field;
   `rtype.Field` is 6.6% of TestGolden CPU. Cache a field plan per
   `reflect.Type` (frozen-skip, lambda body, DeclInfo.Pkg, override key).
   Consider the same plan for `internal/testutil` `walker.write`.

5. [X] **The second optimize folds every imported package again.**
   `foldPkg` memoizes per run, so the optimize after lowering refolded the whole
   library the first had folded: 0.94 s and 380 MB of TestGolden, more than
   half of that optimize. What lowering adds that the program calls is moved
   into the program by passLibFuncs, which the second fold reaches. Remember
   on the `Config` what an earlier `Optimize` folded and skip it.

6. [ ] **Narrow the library passes, then share what no build reaches.**
   Every build *writes* most of the library it copies: on `examples/todo` 85
   of 142 library declarations (~90% by weight) change between the clone and
   codegen, 83 of them in the first `optimize.Optimize` -- `foldPkg` folds
   every imported package (it rewrites `ui.tabs` and `time.parseDate` for a
   program using neither), and `SpecializeOverriddenBodies`, `DataFamilies`
   and `InstallOverlay` write every declaration they touch. A program reaches
   65-69% of the library's objects (`tmp/reachscan`), a third of those through
   the program's overlay, which holds every override a target declares; what
   it reaches is what specialization and folding write by design, so the
   shareable part is the unreached rest.
   Re-measured at e6e1a36e2 (TestGolden, step 5 in): 5.56 GB, 22.9 s user.
   The clone is 2.19 GB and 2.9 s CPU in the profile (6.2 s and 2.29 GB on
   one core, collection included). The first optimize is 1.21 s and 541 MB on
   one core: the library fold 0.71 s / 311 MB, the program's own 0.08 s /
   65 MB, `SpecializeOverriddenBodies` 0.12 s / 46 MB, `DataFamilies` 0.11 s /
   72 MB, `InstallOverlay` 0.03 s. The second is 0.63 s / 220 MB, folds no
   library, and runs `SpecializeOverriddenBodies` (0.11 s / 47 MB) and
   `DataFamilies` (0.09 s / 55 MB) again. (Step 4's numbers were 2.14 GB and
   5.1 s for the clone, and 0.84 GB / 1.46 s for `foldPkg` over both
   optimizes.)
   Owner's decisions:
   - Two parts. (1) `foldPkg`, `SpecializeOverriddenBodies` and
     `DataFamilies` touch only the library declarations the program reaches;
     a win alone, and the goldens show whether output moves. (2) The clone
     copies the program and the library declarations it reaches, sharing the
     rest frozen; library packages copied shallowly so their lists name the
     copies.
   - Every further writer of an unreached declaration (a `MustWriteDecl`
     panic, the fingerprint, `tmp/writescan`) is raised with the owner, one at
     a time.
   - `build.Emit` first; then `interp.Prepare`, dump, the playground, docs
     preview, snapshots, the LSP color fold and the fixture fold.
   - Its own MR, on a branch stacked on this one; this MR carries the other
     steps.
     `tmp/writescan` is the probe: the file and a patch to `emitTarget` that
     reports, per phase, which library declarations a build wrote.
   - [X] (1) `foldPkg`, `SpecializeOverriddenBodies` and `DataFamilies` touch
     only the library declarations the program reaches (`ir.ReachedFrom`).
   - [X] (1b) The copy records the declarations it carries this target's
     override of (`Package.Overridden`), and specialization swaps those and
     iterates no library symbol table. Owner's call, between a registry
     every body reader asks (~1,250 sites, at most 14 more points of the
     clone shared) and this one.
   - [ ] (2) The clone copies the program and the library declarations it
     reaches, sharing the rest frozen.
     - [X] `ClonePackageFor`: `build.Emit`, dump, the playground, the docs
       preview and the snapshot compile paths.
     - [X] `interp.Prepare`.
     - [X] The batch snapshot paths.
     - [X] The LSP color fold.
     - [ ] The fixture fold, `sngl test`'s per-group copy (`ClonePackage`
       today).

7. [ ] **passInlinePure clones slot content into both branches.**
   `ir.SlotSplicer.Substitute` / `lower.deepCloneStmts`: 2.8 GB (22%) of the
   docs build, 0.6 GB of TestGolden.

8. [ ] **`ExprCtx.WithLocal` clones its maps per local**
   (`codegen/exprctx.go:284`): 1.2 GB on the docs build. A parent-chained
   scope instead of a copy.

9. [ ] **Test-only guards (owner's call).** TestGolden fingerprints the checked
   package and its library around every build: 7.2 s of 28.5 s CPU and
   0.6 GB; `walkDivergence` adds 0.23 GB. With `Frozen`/`MustWriteDecl`
   catching the known writers, fingerprinting only the program's package (or
   a sample) is the option -- at the cost of unknown writers. Ask first.

## Deferred

- **Slice-backed IR regions.** Cheap clones and serializable checked IR, but a
  rewrite of every pass. Revisit after step 6; where it would pay is the LSP
  cold start and the WASM playground loading a checked library from a blob.
- Parser buffer pool: 7641e47ca on `perf/parser-one-buffer` never reached
  main. `Parser.open` is 74 MB of a cold check.

## Results

1. **html script walks** -- docs site 126.6 s -> 27.4 s CPU, 12.5 -> 12.1 GB
   allocated; output byte-identical (1187 files). docsgen
   `TestWebsiteProducesContent` 157 s -> 21 s. TestGolden, TestFixtures and
   `codegen/platform/html` green; `TestWindowSharedWalksTheScriptOnce` fails
   with either memo removed.

2. **gtk4 registry per process** -- `TestScript` 22.6 -> 6.2 GB allocated,
   93.9 -> 64.1 s user (19.0 -> 16.9 s sys). Decoded registries are kept by the
   stored file's digest and produced ones by the GIR's, so a fresh store
   neither parses the GIR nor decodes the registry again; what is left under
   `Unavailable` (~0.7 GB) is each command re-reading the stored files to
   validate them. Docs site byte-identical (1187 files); TestGolden,
   TestFixtures, TestScript and `codegen/platform/gtk4` green;
   `TestStoredRegistryIsDecodedOncePerContent` and
   `TestGIRIsParsedOncePerProcess` each fail with their memo removed.

3. **Unit table per declaration** -- TestGolden 6.36 -> 6.03 GB allocated
   (`parseLiteral` 466 -> 145 MB), CPU 26.9 -> 26.1 s user (within noise).
   The tables live on the compilation's `EvalCache` (`interp.UnitTables`),
   since each target's clone has UnitDefs of its own and a process-wide map
   would keep every build's. What is left is the per-literal amounts map.
   Docs site byte-identical; TestGolden, TestFixtures, `internal/optimize` and
   `internal/interp` green; `TestUnitLiteralsShareTheirTable` fails with the
   memo removed.

4. **Cloner field plan** -- TestGolden 26.4 -> 24.1 s user in the profiled
   run (two unprofiled pairs: 25.3 -> 22.8 and 25.3 -> 23.1 s), cloner CPU
   7.7 -> 5.1 s with `rtype.Field` gone from the profile (`planOf`'s
   `sync.Map` load is 0.2 s); allocation unchanged, 6.0 GB. Each struct
   type's plan is read once (`ir.planOf`). Docs site byte-identical (1187
   files) at 26.6 s CPU warm, unchanged since cloning is a small share of it.
   TestGolden, TestFixtures, TestLibraryShared and `ir` green; the four tests
   in `ir/clone_test.go` and `TestCopyOfAFrozenPackageIsWritable` each fail
   when their field kind's case is removed from the plan.
   `internal/testutil`'s `walker.write` reads no field metadata (none of
   `rtype.Field`'s samples are its), so a plan has nothing to save there; its
   5.6 s is hashing and the seen map, which is step 9's question.

5. **Each imported package folded once per target** -- TestGolden 5.91 ->
   5.52 GB allocated, 24.2 -> 22.7 s CPU in the profiled run (two unprofiled
   pairs: 23.3 -> 21.8 and 22.9 -> 21.8 s user). A one-core probe put the
   second optimize at 1.55 s / 595 MB before and 0.62 s / 221 MB after. The
   first optimize stays: `DataFamilies` and the override bodies have to be in
   place before anything folds, and its fold and shake are what lowering
   works on. Docs site byte-identical (1187 files) at 25.8 s CPU warm.
   TestGolden, TestFixtures, `cmd/sngl`, `internal/optimize`, `internal/build`,
   `internal/lower`, `internal/snapshot`, `docs`, `codegen/...`,
   `internal/interp/...`, `internal/playground/...` and `internal/lsp/...`
   green; `TestImportedPackageIsFoldedOncePerConfig` fails with the skip
   removed.

6. (1) **Library passes narrowed to what the program reaches** -- TestGolden
   5.56 -> 5.20 GB allocated; two unprofiled pairs 21.7 -> 20.8 and 21.9 ->
   20.6 s user. In the profile `optimize.Optimize` is 729 MB / 1.53 s -> 282 MB
   / 0.71 s: `foldPkg` 449 -> 160 MB, `DataFamilies` 123 -> 32 MB,
   `SpecializeOverriddenBodies` 89 MB -> under the profile's threshold (it now
   iterates the reached set instead of every library symbol table).
   `ir.ReachedFrom` itself is 15 MB / 0.10 s; a reflective walk tried first
   cost 271 MB / 1.15 s, more than it saved. On `examples/todo` the first
   optimize writes 7 of 142 library declarations, down from 83, and a build 34,
   down from 85: the rest are lowering's (`SpecializeForTarget` swaps every
   override in the library), which part (2) meets. No golden moved; docs site
   byte-identical (1187 files), 25.6 s CPU before and after, allocation within
   run-to-run noise (the site reaches most of the library). `go test ./...`
   green. `TestUnreachedImportsAreLeftAlone` fails on each of its three
   assertions when the matching pass is given everything;
   `TestImportedPackageIsFoldedOncePerConfig` now plants in `ui.window`'s
   `title` default, which the program reaches (step 5's `sngl:time` func was
   unreached), and still fails with step 5's skip removed.

   (1b) **Specialization reads the copy's record** -- the first writer of
   unreached declarations part (2) would meet is lowering's
   `SpecializeForTarget`, which swapped every overridden component it found in
   the library's symbol tables (27 the program never reaches on
   `examples/todo`). `ClonePackageFor` now records the copies carrying an
   override for its target, and both specializations swap only those.
   `tmp/sharecost` measured the two designs on the clone over five examples
   and five targets: sharing what a build does not reach saves 42% of the
   clone's bytes (16-53% per build), and sharing every library declaration,
   the ceiling of a registry every reader asks, 56%. TestGolden 5.20 -> 5.21 GB
   (`SpecializeForTarget` 47 MB / 0.11 s drops out of the profile), 21.6 s
   user; no golden moved; docs site byte-identical (1187 files).
   `TestSpecializationReadsTheCopysRecord` fails with the symbol-table walk
   back.

   (2) **`ClonePackageFor` shares what the build does not reach** -- each
   declaration a library package lists that `ir.ReachedFrom` does not hold is
   left as the checked one, frozen, in the copy's lists and scopes, and the
   copy's overlays drop its overrides. `ReachedFrom` finds an override through
   the checked package's overlays, as the copy's installed maps would hold it.
   No further writer turned up: after (1) and (1b) nothing in a build touches
   such a declaration. TestGolden 5.21 -> 4.25 GB allocated; two unprofiled
   pairs 20.95 -> 17.66 and 20.57 -> 17.72 s user; the clone 2.25 GB / 2.7 s
   -> 1.31 GB / 1.6 s (-42%, what `tmp/sharecost` predicted). Docs site
   byte-identical (1187 files), 25.3 s CPU, 11.6 GB. `go test ./...` green
   (the TestGolden fingerprint of the checked package and its library
   included). `TestCopySharesWhatTheBuildDoesNotReach` fails with the share
   off, and with the overlay drop off `InstallOverlay` panics on a frozen
   `ui.checkbox`.

   (2) **`interp.Prepare`** copies for the interpreter's platform rather than
   every target: `TestRunFixtures` 778 -> 320 MB allocated (`Prepare` 578 ->
   127 MB, since `ClonePackage` also copied every target's overrides), 3.3 ->
   1.1 s CPU. `TestPrepareSharesWhatTheProgramDoesNotReach` fails with
   `ClonePackage` back.

   (2) **Batch snapshots** copy per target as `snapshotTarget` already did.
   A target's copy of the five examples is 60% smaller than a whole one
   (`tmp/copycost`: 83.9 -> 33.6 MB over 25 copies). No test reaches
   `GenerateBatch` (android is its only implementer), so none was added.

   (2) **LSP color fold** copies for no target, which shares what the document
   does not reach and every override: its copy in the color tests 1.5 ->
   0.64 MB per fold. The fold hands back colors, not the package, so no test
   can see the copy without an allocation bound; none was added.
