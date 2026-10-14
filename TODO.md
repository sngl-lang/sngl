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

3. [ ] **Unit table rebuilt per literal.** `optimize.parseLiteral` ->
   `interp.UnitLiteralValue` -> `buildUnitTableFromDef` builds a fresh table
   for every unit literal: 0.45 GB, 7% of TestGolden. Memoize by `*ir.UnitDef`
   (`unitTable.Decl` already compares by declaration).

4. [ ] **Reflective cloner reads field names per node.** `ir/clone.go`
   `cloneStruct` calls `v.Type().Field(i).Name` several times per field;
   `rtype.Field` is 6.6% of TestGolden CPU. Cache a field plan per
   `reflect.Type` (frozen-skip, lambda body, DeclInfo.Pkg, override key).
   Consider the same plan for `internal/testutil` `walker.write`.

5. [ ] **Share the frozen library; copy a declaration on its first write.**
   ~94% of what `ClonePackageFor` copies is library IR (1.8 MB for
   `examples/hello`); it is 2.2 GB (34%) of TestGolden. Clone the program,
   share frozen library declarations, and have each writer call an
   `own(decl)` that copies on first write into a per-build map.
   `ir.MustWriteDecl` panics find the unlisted writers; the TestGolden
   fingerprint confirms nothing leaked. Largest step; agree the design before
   starting.

6. [ ] **passInlinePure clones slot content into both branches.**
   `ir.SlotSplicer.Substitute` / `lower.deepCloneStmts`: 2.8 GB (22%) of the
   docs build, 0.6 GB of TestGolden.

7. [ ] **`ExprCtx.WithLocal` clones its maps per local**
   (`codegen/exprctx.go:284`): 1.2 GB on the docs build. A parent-chained
   scope instead of a copy.

8. [ ] **Test-only guards (owner's call).** TestGolden fingerprints the checked
   package and its library around every build: 7.2 s of 28.5 s CPU and
   0.6 GB; `walkDivergence` adds 0.23 GB. With `Frozen`/`MustWriteDecl`
   catching the known writers, fingerprinting only the program's package (or
   a sample) is the option -- at the cost of unknown writers. Ask first.

## Deferred

- **Slice-backed IR regions.** Cheap clones and serializable checked IR, but a
  rewrite of every pass. Revisit after step 5; where it would pay is the LSP
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
