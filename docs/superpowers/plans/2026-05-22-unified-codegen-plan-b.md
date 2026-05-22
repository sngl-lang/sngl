# Unified Code Generation — Platform Migration + Cutover (Plan B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to execute task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Cut every platform over to the unified `Sink`/`CodeWriter` API, then delete the legacy `OutputFile`/`Response` and dead `LangTranslator` v2 stubs. Wire AST position marks at translator statement boundaries so source maps actually populate.

**Architecture:** Each platform's `Generate(req *Request) (*Response, error)` becomes `Generate(req *Request, sink Sink) error`. Code-emitting paths route through `OpenCodeFile`; non-source artifacts call `sink.Create` directly. Each Go-emitting platform drops its private `goImports` map — translator calls `w.Import(...)` at the emit site instead. Cutover runs in two stages: (a) add a sibling `GenerateSink` method per platform via a transitional `SinkGenerator` interface so platforms migrate in independent PRs, and (b) once every platform implements `SinkGenerator`, rename the new method to `Generate` and drop the old signature plus the `OutputFile` slice in one cutover commit.

**Tech Stack:** Same as Plan A — Go, the existing `codegen.Sink`/`CodeWriter` types.

**Spec:** `docs/superpowers/specs/2026-05-22-unified-codegen-design.md`
**Foundations (Plan A):** `docs/superpowers/plans/2026-05-22-unified-codegen-foundations.md` (complete)

---

## Migration Surface

- **6 platforms** with `Generate(req) (*Response, error)`:
  `android`, `bubbletea`, `fyne`, `gtk4`, `html`, `none`.
- **5 CLI/host call sites** dispatching `Generate`:
  `cmd/sngl/compile.go:395`, `internal/playground/api.go:195`, `internal/playground/api.go:324`, `internal/snapshot/compile.go:56`, `internal/playground/cmd/main.go:130`, `internal/playground/cmd/main.go:273`.
- **71 internal references** to `resp.Files` / `BytesFile` / `TemplateFile` / `RenderTemplates` to retire.
- **4 dead stubs per language** to delete from `LangTranslator`: `WriteExpr`, `WriteStmt`, `WriteType`, `Eval`. Stub bodies exist in all 4 langs.

---

## Phase 0 — Transitional dispatch (one PR)

### Task 0.1: Add `SinkGenerator` interface + CLI fallback

**Files:**
- Modify: `codegen/codegen.go`
- Modify: `cmd/sngl/compile.go`
- Modify: `internal/playground/api.go`, `internal/playground/cmd/main.go`, `internal/snapshot/compile.go`

- [ ] **Step 1: Add transitional interface to `codegen/codegen.go`**

```go
// SinkGenerator is the post-migration shape of PlatformGenerator.Generate.
// Implemented incrementally per platform; once every platform implements it,
// the legacy `Generate(req) (*Response, error)` signature is removed and this
// becomes the only Generate method.
type SinkGenerator interface {
	GenerateSink(req *Request, sink Sink) error
}
```

- [ ] **Step 2: Add a host-side helper that dispatches preferring `SinkGenerator`**

Append to `codegen/codegen.go`:

```go
// RunGenerate runs a PlatformGenerator into the given sink. When plat
// implements SinkGenerator, the sink is used directly. Otherwise the legacy
// Generate path is used and its OutputFiles are streamed into sink. This
// helper is the single dispatch point so the legacy path disappears in one
// commit when every platform has migrated.
func RunGenerate(plat PlatformGenerator, req *Request, sink Sink) error {
	if sg, ok := plat.(SinkGenerator); ok {
		return sg.GenerateSink(req, sink)
	}
	resp, err := plat.Generate(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	for _, f := range resp.Files {
		w, err := sink.Create(f.Name)
		if err != nil {
			return err
		}
		_, werr := f.WriteTo(w)
		if cerr := w.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			if errors.Is(werr, ErrSkip) {
				continue
			}
			return werr
		}
	}
	return nil
}
```

(Add `"errors"` and `"fmt"` imports if not already present.)

- [ ] **Step 3: Replace direct `plat.Generate(...)` calls at host sites**

In `cmd/sngl/compile.go` around line 395, replace the existing block:

```go
resp, err := plat.Generate(&codegen.Request{ ... })
if err != nil { ... }
if resp.Error != "" { ... }
for _, file := range resp.Files {
    path := filepath.Join(outDir, file.Name)
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { ... }
    f, err := os.Create(path)
    ...
}
```

With:

```go
req := &codegen.Request{ ... }
sink := codegen.NewDirSink(outDir)
if err := codegen.RunGenerate(plat, req, sink); err != nil {
    return fmt.Errorf("%s: %w", filename, err)
}
```

Apply the same swap in `internal/playground/api.go`, `internal/playground/cmd/main.go`, `internal/snapshot/compile.go`. Each constructs its own sink:
- `cmd/sngl` → `NewDirSink(outDir)`.
- `playground` → `NewMemSink()`; afterward iterate `sink.Files()` to populate whatever response shape the playground uses.
- `snapshot` → `NewMemSink()`; downstream consumers iterate `Files()`.

- [ ] **Step 4: Verify**

- `go build ./...` — clean.
- `go test ./...` — full suite passes (existing platform `Generate` impls still produce `*Response`; `RunGenerate` adapts them transparently).
- `go vet ./...` — clean.

- [ ] **Step 5: Commit**

```bash
git add codegen/codegen.go cmd/sngl/compile.go internal/playground/api.go internal/playground/cmd/main.go internal/snapshot/compile.go
git commit -m "codegen: SinkGenerator interface + RunGenerate dispatch

Adds the transitional signature platforms migrate to one at a time.
RunGenerate prefers SinkGenerator; falls back to the legacy Response
path through a memory adapter. After Phase 0, every host emits via
Sink — only the inside of each platform's Generate still allocates
OutputFiles."
```

---

## Phase 1 — Migrate platforms (one per PR)

For each platform, add a `GenerateSink(req, sink) error` method that emits directly through `Sink` / `OpenCodeFile`. Keep the existing `Generate(req) (*Response, error)` working until Phase 2 (cutover). The platform's old impl can call into the new one via the inverse-adapter — `CollectOutputFiles(memSink) []*OutputFile`. This lets each platform PR be self-contained and reviewable.

### Task 1.0: Add `CollectOutputFiles` helper

**Files:**
- Modify: `codegen/codegen.go`

- [ ] **Step 1: Implement and test**

```go
// CollectOutputFiles drains a MemSink into the legacy OutputFile slice.
// Used inside a platform's legacy Generate while migration is in flight:
// the platform's new emit path writes into a MemSink, and the legacy
// wrapper converts it back to Files. Deleted in Phase 2 cutover.
func CollectOutputFiles(s *MemSink) []*OutputFile {
	files := s.Files()
	out := make([]*OutputFile, 0, len(files))
	for name, content := range files {
		out = append(out, BytesFile(name, content))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
```

Add a test in `codegen/sink_test.go` that round-trips a MemSink through `CollectOutputFiles`.

- [ ] **Step 2: Commit**

```bash
git add codegen/codegen.go codegen/sink_test.go
git commit -m "codegen: CollectOutputFiles for legacy-path adapter

Drains a MemSink into []*OutputFile so a platform can implement its
new Sink-based path internally and forward the result through the
legacy Response shape until Phase 2."
```

### Task 1.1: Migrate `none` platform (smallest, exercise template)

**Files:**
- Modify: `codegen/platform/none/none.go`

The `none` platform produces no output. It's the simplest possible migration.

- [ ] **Step 1: Add GenerateSink**

```go
func (g *Generator) GenerateSink(req *codegen.Request, sink codegen.Sink) error {
	return nil
}
```

- [ ] **Step 2: Verify** — `go test ./...` clean.

- [ ] **Step 3: Commit**

```bash
git commit -m "codegen/none: implement SinkGenerator (no-op)"
```

### Task 1.2: Migrate `html` platform

This is the highest-payoff PR. It exercises:
- ES module imports (translator-driven via `w.Import(ImportSpec{Kind: ImportEsModule})`).
- WASM extern bridge (`Kind: ImportWasmExtern`).
- JS source-map sidecar.
- Multiple file kinds: HTML, JS, manifest, WASM blob.
- Static vs route mode (per CLAUDE.md: route mode delegates to `HTTPCompiler`).

Step list (high-level — the actual mechanics will be hashed out in the PR):

- [ ] Step A: Move HTML page emission to `sink.Create(name)` (HTML is not source code; doesn't need `OpenCodeFile`).
- [ ] Step B: Move JS emission to `OpenCodeFile(sink, name, jsLang, WriterOptions{Maps: req.Maps})`. Each `output()` window's JS becomes one `CodeWriter`.
- [ ] Step C: Replace `BundledNativePkgs` thread-through on `ExprScope` with per-import `w.Import(spec)` calls inside the JS translator's `evalNativeCall` path. Set `ImportKind: ImportEsModule` for `js://` imports, `ImportWasmExtern` for `go://`-via-WASM.
- [ ] Step D: Inline static assets (icon, i18n manifest, wasm_exec.js, app.wasm) via `sink.Create`.
- [ ] Step E: Move HTTP route mode through the same path: language-supplied router/main code uses `OpenCodeFile`; html still owns asset emission.
- [ ] Step F: Implement `GenerateSink` that calls all of the above directly. Update legacy `Generate` to call `GenerateSink` into a `MemSink`, then `codegen.CollectOutputFiles(sink)`.
- [ ] Step G: Verify `go test ./codegen/platform/html/...` and full CLI script tests under `cmd/sngl/testdata/compile_html_*.txt` still produce byte-identical output (the only intentional change is that source-map sidecars now appear when `maps: true`).
- [ ] Step H: Commit:

```bash
git commit -m "codegen/html: implement SinkGenerator + drop BundledNativePkgs

JS emission now routes through CodeWriter; native imports are
collected at the translator emit site via w.Import. Static assets
write directly to sink. Legacy Generate keeps working through a
MemSink → CollectOutputFiles adapter."
```

This task is large enough that it may warrant its own sub-plan written after Task 1.0 lands.

### Task 1.3: Migrate `bubbletea` platform

Similar shape, simpler than html (single file generally, no asset zoo).

- [ ] Step A: Replace `info.goImports[...]` map writes with `w.Import(ImportSpec{Path: "...", Kind: ImportNative})` calls inside the Go translator's `evalNativeCall` and at intrinsic-translation sites. Delete the `goImports` field on bubbletea's compile-info struct (`codegen/platform/bubbletea/compiler_ir.go:93,118,135,141,165,201,242,266,297`).
- [ ] Step B: Emit `model.go` (and any other go files) via `OpenCodeFile`.
- [ ] Step C: Implement `GenerateSink`; update legacy `Generate` to adapt.
- [ ] Step D: Verify text snapshot + integration tests pass byte-identical.
- [ ] Step E: Commit.

### Task 1.4: Migrate `fyne` platform

- [ ] Step A: Same import-map collapse as 1.3 (`codegen/platform/fyne/compiler_ir.go:80,86,149,179,184`).
- [ ] Step B: Same Go-file emission via `OpenCodeFile`. Fyne uses cgo for native UI — verify `EmitCHeader` plays cleanly with the new Import path. The `ImportCgo` kind handles this: translator emits a single `// #include` preamble when any cgo import is present.
- [ ] Step C: `GenerateSink` + legacy adapter.
- [ ] Step D: Snapshot tests pass.
- [ ] Step E: Commit.

### Task 1.5: Migrate `gtk4` platform

- [ ] Step A: Import-map collapse (`codegen/platform/gtk4/compiler_ir.go:172,175,180,181,245,337`).
- [ ] Step B: Per-window go file via `OpenCodeFile`.
- [ ] Step C: `GenerateSink`. gtk4 doesn't have a snapshotter (orthogonality #15), so test coverage is thinner — rely on `cmd/sngl/testdata/*` script tests if any exist for gtk4; otherwise compile-only verification.
- [ ] Step D: Commit.

### Task 1.6: Migrate `android` platform

Trickier because android has Kotlin and Go paths plus the Gradle scaffold.

- [ ] Step A: Distinguish source files (`MainScreen.kt`, optional `golib/golib.go`) from build-system files (`build.gradle`, `AndroidManifest.xml`, `gradlew`, icons). Source files use `OpenCodeFile`; the rest use `sink.Create`.
- [ ] Step B: Kotlin import path: translator calls `w.Import(ImportSpec{Kind: ImportNative})` for kotlin native packages. SMAP support stays stubbed per Plan A.
- [ ] Step C: Go path (when `--lang go`) collapses `gogen_ir.go`'s implicit import set into translator calls. Direct-build vs gradle-build branches must both work.
- [ ] Step D: `GenerateSink`. Legacy adapter.
- [ ] Step E: Snapshot tests + APK build path verify.
- [ ] Step F: Commit.

---

## Phase 2 — Cutover

Once every platform has a working `GenerateSink`, the legacy path goes.

### Task 2.1: Rename `GenerateSink` → `Generate`, drop legacy

**Files:**
- Modify: `codegen/codegen.go`
- Modify: every `codegen/platform/*/*.go` containing the dual methods.
- Modify: `cmd/sngl/compile.go`, `internal/playground/api.go`, `internal/playground/cmd/main.go`, `internal/snapshot/compile.go`.

- [ ] **Step 1: Change the interface in `codegen/codegen.go`**

```go
type PlatformGenerator interface {
	ir.Platform
	SupportedLangs() []string
	Capabilities() lower.Caps
	Generate(req *Request, sink Sink) error
}
```

Remove `Response`, `OutputFile`, `BytesFile`, `TemplateFile`, `RenderTemplates`, `ErrSkip` (if its only consumer was `TemplateFile`), and the `SinkGenerator` shim. Remove `RunGenerate` and `CollectOutputFiles` (no longer needed; call `plat.Generate(req, sink)` directly).

- [ ] **Step 2: In every platform, delete the old `Generate(req) (*Response, error)` impl and the `MemSink → CollectOutputFiles` adapter. Rename `GenerateSink` → `Generate`.**

- [ ] **Step 3: Update host call sites to call `plat.Generate(req, sink)` directly** (drop the `RunGenerate` indirection).

- [ ] **Step 4: Verify** — `go build ./...`, `go test ./...`, all golden tests pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "codegen: cutover — drop Response/OutputFile, Generate takes Sink

Every platform now implements Generate(req, sink) error. The legacy
OutputFile/Response/BytesFile/TemplateFile/RenderTemplates surface
plus the transitional SinkGenerator shim are deleted."
```

### Task 2.2: Delete dead `LangTranslator` v2 stubs

**Files:**
- Modify: `codegen/codegen.go`
- Modify: `codegen/lang/{golang,javascript,kotlin,none}/*.go`

- [ ] **Step 1: Remove from the `LangTranslator` interface in `codegen/codegen.go`:**

```go
WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error
WriteStmt(w io.Writer, expr ir.Stmt, scope *ir.Scope) error
WriteType(w io.Writer, t *ir.Type) error
Eval(expr ir.Expr) string
```

- [ ] **Step 2: Delete the stub methods on each `Translator`:**

`codegen/lang/golang/golang.go:50,54,58,66`; `codegen/lang/javascript/javascript.go:29,33,37,45`; `codegen/lang/kotlin/kotlin.go:104,108,112,120`; `codegen/lang/none/none.go:30,31,32,34`.

- [ ] **Step 3: Verify** — `go build ./...`, `go test ./...`.

- [ ] **Step 4: Commit**

```bash
git commit -m "codegen: drop dead WriteExpr/WriteStmt/WriteType/Eval stubs

These were never finished and never called. The new CodeWriter
surface (RenderHeader / RenderSourceMap / future EmitFile) replaced
them."
```

---

## Phase 3 — Position threading + real source maps

Plan A wired the renderers. Plan B Phase 3 makes translators actually call `w.Mark` so the maps populate.

### Task 3.1: Mark statements in the Go translator

**Files:**
- Modify: `codegen/lang/golang/ircontext.go` (or wherever `EvalStmt` lives).

- [ ] **Step 1: Locate the statement-emission path.** `golang.GoIRContext.EvalStmt(stmt ir.Stmt) []string` is the main entry. It returns rendered lines.

- [ ] **Step 2: Pass `CodeWriter` down to `EvalStmt`.** Either via the `GoIRContext` struct or as a new parameter. The cleanest path: store `Writer codegen.CodeWriter` on `GoIRContext` and have `EvalStmt` call `c.Writer.Mark(stmt.Pos())` at entry before returning lines. The translator's higher-level orchestration concatenates lines and writes them via `c.Writer.Write(...)`. Audit existing concatenation paths (`compiler_ir.go` in each platform) to make sure `Write` calls retain the mark order.

- [ ] **Step 3: Add a test fixture** that compiles a multi-statement function and asserts `//line` directives appear at the right offsets in the generated output. Place under `cmd/sngl/testdata/compile_maps_go.txt`.

- [ ] **Step 4: Commit**

```bash
git commit -m "lang/go: Mark stmt positions in EvalStmt for source maps"
```

### Task 3.2: Mark statements in the JS translator

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go`.

- [ ] Same approach as Task 3.1 but in `JsIRContext.EvalStmt`. Add fixture `cmd/sngl/testdata/compile_maps_js.txt` that asserts the `.js.map` sidecar appears with `maps: true`.

- [ ] Commit.

### Task 3.3: Document deferred Kotlin SMAP

**Files:**
- Modify: `docs/superpowers/specs/2026-05-22-unified-codegen-design.md`.

- [ ] Update spec status to note Kotlin source maps remain deferred. File a `glab issue` referencing the deferred work and link in the spec.

---

## Phase 4 — Import-collection cleanup audit

After per-platform migrations, audit that no platform still keeps its own import bookkeeping.

### Task 4.1: Verify zero remaining `goImports`-style maps

**Files:**
- Grep: `git grep -n "goImports\|jsImports\|kotlinImports\|nativeImports" codegen/platform/`

- [ ] **Step 1:** Run the grep. Should return zero matches in `codegen/platform/`. If anything remains, delete it and route through `w.Import(...)`.

- [ ] **Step 2: Delete `ExprScope.BundledNativePkgs`** from `codegen/codegen.go`. Its only consumer was the JS translator's "is this an ES-import call" check, which Task 1.2 should have moved onto `*ir.NativeImport.BindingKind` or directly into the translator's emit-time decision based on the import path/scheme.

- [ ] **Step 3: Commit**

```bash
git commit -m "codegen: drop ExprScope.BundledNativePkgs and audit zero import maps

Every native-import binding decision now lives in the translator at
emit time, keyed off ImportSpec.Kind. No platform threads import
bookkeeping anymore."
```

---

## Self-Review

**Spec coverage:**
- §PlatformGenerator changes (Generate takes Sink) → Tasks 0.1, 1.1–1.6, 2.1.
- §Import collection migration → Tasks 1.2 (html) through 1.6 (android) + 4.1.
- §Position-marker call sites → Tasks 3.1, 3.2.
- §Source-map rendering per language → Plan A landed renderers; Phase 3 wires them at translator stmt level.
- §LangTranslator changes (deletions) → Task 2.2.
- §Filesystem abstraction → Plan A.
- §Migration plan (atomic per-platform, no shim) → Phase 0 + Phase 1 + Phase 2 reflect this.

**Placeholder scan:**
- Task 1.2 (html) is high-level rather than bite-sized — flagged. The actual mechanics depend on what comes up during migration; suggested follow-up: after Task 1.0 lands, write a focused sub-plan for html alone.
- "Verify byte-identical output" appears multiple times — each platform task should run the existing `cmd/sngl/testdata/compile_<platform>_*.txt` golden tests as the source of truth.

**Type consistency:**
- `SinkGenerator.GenerateSink` lives only in Phase 0–1; renamed to `Generate` in Phase 2. Plan text consistent.
- `CollectOutputFiles`, `RunGenerate` are transitional helpers — both deleted in Phase 2.
- Final `PlatformGenerator.Generate(req *Request, sink Sink) error` matches spec §150.

**Decomposition note:**
Phase 1 platform tasks (1.2–1.6) are each big enough that a dedicated sub-plan per platform is reasonable. This plan defines the migration framework and high-level per-platform checklists; expect to write `docs/superpowers/plans/2026-05-22-unified-codegen-html-migration.md` and similar before executing 1.2 in detail.
