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
   Measured before starting (TestGolden after step 4): the clone is 2.14 GB
   and 5.1 s CPU, library IR 64-91% of it. But every build *writes* most of
   the library it copies: on `examples/todo` 85 of 142 library declarations
   (~90% by weight) change between the clone and codegen, nearly all in the
   first `optimize.Optimize` -- `foldPkg` folds every imported package
   (0.84 GB, 1.46 s; it rewrites `ui.tabs` and `time.parseDate` for a program
   using neither), and `SpecializeOverriddenBodies`, `DataFamilies` and
   `InstallOverlay` write every declaration they touch. A program reaches
   about two thirds of the library (`tmp/reachscan`), and that two thirds is
   what specialization and folding write by design, so the shareable part is
   the unreached third: ~0.5-0.9 GB and 2-3.5 s, not the 34% first assumed.
   `foldPkg`'s 0.84 GB counted both optimizes; step 5 took the second's
   0.38 GB, so re-measure before starting.
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

7. [ ] **passInlinePure clones slot content into both branches.**
   Re-measured after step 5 (e6e1a36e2): on the docs build passInlinePure is
   2.65 GB of 11.7 GB and 1.46 s of 26.8 s CPU, `ir.SlotSplicer.Substitute`
   2.26 GB, `lower.deepCloneStmts` 2.82 GB; on TestGolden 555 MB, 509 MB and
   614 MB of 5.59 GB. The two branches are no longer the largest part: counting
   statements cloned per callee, `markup.list` and `markup.listItem` copy
   ~0.41M, while the single-insertion wrappers every page passes through
   (`md` `site`, `nav.stack`, `nav.page`, `PageLayout`, `ui.vbox`) copy the
   whole page content again at each level, ~1.1M.
   - [X] a. Move content a body inserts once instead of copying it: the
     call site is replaced by the splice and nothing else holds it. Content
     inserted more than once, or beside a fallback or population that inserts
     slots of its own (spliced once per use), is still copied.
   - [X] b. Take the body of a component one node instantiates rather than
     copy it, emptying the declaration: md's `site` and each page component.
     Only where passNoInlineComponents runs next and drops what nothing
     instantiates, so no lowered output shows an emptied declaration
     (`TestLower`'s goldens, which have no root, showed `{}` before that
     gate); only at a call site outside any spliced body, which may have been
     copied before its own declaration was walked; never a component a test
     takes or the harness root.
   - [ ] c. What is left on the docs build after (b), 771 MB of
     `deepCloneStmts`: content inserted more than once -- the two branches of
     `markup.list` and `markup.listItem` (337 MB) -- and passNoInlineComponents'
     slot splice (247 MB). The last cannot move as (a) does: its call site's
     content is still read after the splice (`markdown_document`'s golden
     changes, `Set__ctx_listDepth(… + 1)` becoming `Set__p1(…)`), and 174
     statements are already reachable twice after that pass on TestGolden
     before any change. The branches need sharing IR between them, which
     later passes write in place, or html's overrides inserting once: an
     `html.element(tag=ordered ? "ol" : "ul")` is refused before the
     per-document fold decides `ordered` for md.document (`tag` and `attrs`
     must be literals in `validateRawElements`), and a task item wraps its
     content in a `div` a plain one does not, which no single insertion says.

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

7a. **Slot content inserted once is moved** -- docs site 11.67 -> 9.94 GB
allocated, passInlinePure 2.65 -> 0.97 GB, `SlotSplicer.Substitute` 2.26 ->
0.59 GB, `deepCloneStmts` 2.82 -> 1.12 GB; two unprofiled pairs 29.4 ->
24.2 and 26.4 -> 24.5 s user. TestGolden 5.59 -> 5.49 GB, passInlinePure
555 -> 370 MB; pairs 23.7 -> 23.0 and 23.5 -> 22.9 s user. No call site is
spliced twice (108k splices over the root, internal, codegen and docs
tests, 9.1k over `TestScript`) and no statement is reachable twice after
the pass (TestGolden, `TestScript`, the docs build), checked with a probe
not committed. Docs site byte-identical (1187 files); `go test ./...`
green. Each case of `TestInlinePureMovesContentInsertedOnce` fails under
its own revert: the call cloning again, the use count dropped, and the
fallback check dropped.

7b. **A sole instance takes its component's body** -- docs site 9.94 ->
9.59 GB allocated, passInlinePure 973 -> 614 MB, `deepCloneStmts` 1.12 ->
0.77 GB; four unprofiled pairs 28.3 -> 23.5, 30.2 -> 22.8, 25.4 -> 23.4 and
25.2 -> 23.1 s user (the first two under load). TestGolden unchanged at
5.49 GB, its programs instantiating little once each. A probe not
committed found, over the root, internal, codegen and docs tests,
`TestScript` and the docs build, 940 bodies taken, no node or identifier
naming an emptied declaration at the end of lowering, and no statement
reachable twice after the pass. Docs site byte-identical (1187 files);
`go test ./...` green. `TestInlinePureTakesTheBodyOfASoleInstance` fails
under each revert: taking nothing, dropping the use count, and dropping
the spliced-body guard.
