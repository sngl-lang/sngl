# Unified Code Generation Design

**Status:** Plan A (foundations) implemented; Plan B (per-platform migrations + cutover) pending
**Date:** 2026-05-22
**Tracking:** glab #69 (source maps)

## Problem

Current code-generation surface is uneven:

- `codegen.OutputFile{Name, WriteTo(io.Writer)}` is stream-ready, but platforms eagerly populate `[]*OutputFile` in `Response.Files`. No streaming into a sink.
- No mechanism to collect imports while writing the body. Every Go-emitting platform reinvents a `goImports` map (orthogonality audit #6); JS uses `BundledNativePkgs`; Kotlin reaches into `golang.SnglI18nImportPath`.
- AST positions are not threaded into generated text, so source maps (Go `//line`, JS `.map`) cannot be produced.
- File I/O assumes a real filesystem. WASM playground has to special-case.
- `LangTranslator` has dead `WriteExpr`/`WriteStmt`/`WriteType` stubs (orthogonality audit #3) — half-finished v2 surface.

This spec defines a single stream-based writer abstraction that owns body buffering, import collection, AST-position capture, and sink-agnostic output. Language translators do the rendering; platforms orchestrate file layout and write non-source artifacts directly to the sink.

## Goals

1. Stream-based code emission with no intermediate `[]byte`-per-file slice in the public API.
2. Import collection during body rendering, flushed into a file header on close.
3. AST source position tracking that powers per-language source maps.
4. Filesystem-abstract output sink so WASM playground uses the same API.
5. Language translators own all source-language emission; platforms direct flow and emit binary/manifest/scaffold artifacts.
6. Delete the dead `LangTranslator` v2 stubs and the parallel per-platform import bookkeeping.

## Non-goals

- Cross-translator source-map merging across optimization passes.
- Multi-file emission from a single `EmitFile` call. Platforms still decide splits.
- Kotlin SMAP support in v1 (documented as future work).
- Back-compat shim for `OutputFile`/`Response.Files` past cutover.

## Architecture

```
Platform.Generate(req, sink)
   ├── for each source file the platform wants:
   │     w := codegen.OpenCodeFile(sink, name, langTranslator)
   │     langTranslator.EmitFile(w, pkg, scope)
   │       └── w.Mark(pos); w.Write(...); w.Import(spec); ...
   │     w.Close()      // flushes header + imports + body + sourcemap sidecar
   └── for each non-source artifact (manifest, icon, go.mod, scaffold):
         f := sink.Create(name); write; f.Close()
```

Three new types in `codegen/`:

### `Sink`

```go
type Sink interface {
	Create(name string) (io.WriteCloser, error)
}
```

- `name` is a relative path. Forward slashes. Sink translates as needed.
- Sink is responsible for parent directory creation.
- Implementations:
  - `DirSink(root string)` — disk-backed. File: `codegen/sink_dir.go`, build-tagged `!js`.
  - `MemSink` — in-memory `map[string][]byte`. Available on all builds. Used by tests and WASM playground.

### `CodeWriter`

```go
type CodeWriter interface {
	io.Writer                                   // raw body bytes
	WriteAt(pos ast.Pos, p []byte) (int, error) // mark position then write
	Mark(pos ast.Pos)                           // mark position only
	Import(spec ImportSpec)                     // dedup'd; flushed in header
	Section(name string)                        // optional logical sectioning hint
	Close() error                               // header → imports → body → sidecar
}
```

Concrete implementation `codeWriter` (unexported) holds:

- `body *bytes.Buffer` — all writes append here.
- `imports map[ImportSpec]struct{}` — dedup set (per `feedback_set_type`).
- `importOrder []ImportSpec` — insertion order, for stable output.
- `positions []posEntry{byteOffset int, pos ast.Pos}` — for source map.
- `lang LangTranslator` — provides header + source-map rendering.
- `sink Sink`, `name string` — for the final write and sidecar.
- `closed bool`.

`Section(name)` records a marker into the position list so language headers can group output (e.g. Go's `// generated declarations` vs `// generated functions`). Default: no-op.

`Close()` performs:

1. Ask `lang.RenderHeader(name, importOrder)` → header bytes.
2. Ask `lang.RenderSourceMap(name, positions, body.Bytes())` → `SourceMapResult{InlineBody []byte, Sidecar []byte, SidecarName string}`. The lang may rewrite the body to splice directives in (Go `//line`); otherwise it returns the body untouched.
3. Open the file via `sink.Create(name)`, write header + (possibly rewritten) body, close.
4. If `Sidecar != nil`, open `sink.Create(SidecarName)`, write, close.
5. Idempotent on double-close.

Helper: `codegen.OpenCodeFile(sink Sink, name string, lang LangTranslator) CodeWriter` is the only public constructor.

### `ImportSpec`

```go
type ImportSpec struct {
	Path  string     // "fmt", "git.duckfam.us/.../pkg/go/i18n", "react"
	Alias string     // "" default; "_" blank import
	Kind  ImportKind // controls how the translator renders the import line
}

type ImportKind int

const (
	ImportNative        ImportKind = iota // language-native import
	ImportStdlibRuntime                   // sngl pkg/<lang>/<name>
	ImportCgo                             // #include via cgo preamble
	ImportEsModule                        // ES module (JS bundler)
	ImportWasmExtern                      // WASM extern bridge
)
```

`Kind` resolves the orthogonality audit #16 split: the language translator looks at `Kind` to decide whether to write `import X` (ES), an extern declaration, or a `// #include` line. Platform never has to thread `BundledNativePkgs` into the scope.

## LangTranslator changes

Replace dead stubs with the writer-driven surface:

```go
type LangTranslator interface {
	// ... existing identifier/name/type/expr eval methods stay ...

	EmitFile(w CodeWriter, pkg *ir.Package, scope *ExprScope) error
	RenderHeader(name string, imports []ImportSpec) []byte
	RenderSourceMap(name string, positions []PosEntry, body []byte) SourceMapResult
}

type PosEntry struct {
	ByteOffset int
	Pos        ast.Pos
}

type SourceMapResult struct {
	InlineBody  []byte // may be nil to mean "use body unchanged"
	Sidecar     []byte // nil if no sidecar
	SidecarName string // e.g. "model.js.map"
}
```

Delete from `LangTranslator`: `WriteExpr`, `WriteStmt`, `WriteType`, `Eval` (the four stubs noted in orthogonality audit #3).

`EvalExpr`/`EvalStmt` keep their string return shape; lang translators internally route through a buffered writer when serving `EmitFile`. Migration to fully streaming string-leaf-rendering is out of scope; only the file-level surface is unified now.

## PlatformGenerator changes

```go
type PlatformGenerator interface {
    ir.Platform
    Lower(...) // unchanged
    Generate(req *Request, sink Sink) error
    SupportedLangs() []string
}
```

- `Response`, `OutputFile`, `BytesFile`, `TemplateFile`, `RenderTemplates` deleted.
- Errors returned directly. No `Response.Error` string field.
- Platform calls `OpenCodeFile` for each generated source file; calls `sink.Create` directly for manifests, icons, binaries, `go.mod`, gradle scaffolds, etc.
- Templates: platforms that used `TemplateFile`/`RenderTemplates` switch to executing `text/template` against a `CodeWriter` or directly against `sink.Create(name)` for non-source files. Skip behavior moves into platform code.

CLI (`cmd/sngl/compile.go`, `build.go`, `run.go`, `snapshot.go`, dump paths) constructs a `DirSink(outDir)` and passes it to `Generate`. WASM playground constructs a `MemSink` and reads back via a method on the sink (`MemSink.Files() map[string][]byte`).

## Gating: the `maps` option

Source-map emission is opt-in via the top-level `maps bool` field on
`Options` (declared in `lib/options.sngl`). Default `false`.

- `Request` carries the resolved value through `req.Options`.
- `OpenCodeFile` accepts a `WriterOptions{Maps bool}` (or a fluent setter)
  so the writer knows whether to record positions and call
  `lang.RenderSourceMap` at close. When `Maps == false`, position lists
  stay empty, no sidecar opens, no `//line` rewriting happens — zero cost.
- Position-marking calls (`w.Mark`, `w.WriteAt`) remain in translators
  unconditionally; the writer drops them when maps are disabled.

## Source-map rendering per language

**Go.**

- `RenderSourceMap` walks `positions` and, where line changes between consecutive entries, splices `//line <file>:<line>\n` into the body at the prior byte offset.
- Sidecar: nil.
- Output: rewritten body in `InlineBody`.

**JS.**

- `RenderSourceMap` generates a source-map v3 JSON sidecar from `positions` (VLQ-encoded mappings) and appends `//# sourceMappingURL=<name>.map\n` to the body.
- `SidecarName = name + ".map"`.

**Kotlin.**

- v1: returns `SourceMapResult{}` (zero value, body untouched). Document as future SMAP work.

**none.**

- Returns zero value. Interpreter path does not produce files.

## Import collection migration

The four duplicated `goImports` maps (audit #6) and `BundledNativePkgs` thread (audit #16) collapse:

- Wherever today a platform does `info.goImports["time"] = ""`, the translator instead calls `w.Import(ImportSpec{Path: "time", Kind: ImportNative})` at the exact site that emits the `time.X` call. This is reachable because every Go-emitting intrinsic already routes through the Go translator's `evalNativeCall` / `evalCall`.
- `golang.SnglI18nImportPath` cross-references from fyne/bubbletea/gtk4 are removed. The Go translator emits the import when it lowers the i18n intrinsic.
- `BundledNativePkgs` on `ExprScope` is deleted. The decision moves to `ImportSpec.Kind` on the `*ir.NativeImport` (set during checker/lower from `Scheme`).

Order of imports in the rendered header is insertion order, then a per-language post-sort hook (`RenderHeader` may sort). Stable across compiles.

## Position-marker call sites

`*ir.Func`, `*ir.Stmt`, `*ir.Expr` carry `Pos ast.Pos` where available. Audit IRContext translation paths:

- `EvalStmt(stmt)` calls `w.Mark(stmt.Pos())` before producing the stmt's bytes.
- `EvalExpr(expr)` does **not** mark per-expr (too noisy for source maps); only stmt-level positions.
- `EmitFuncDef(fn)` marks `fn.Pos`.

The translator-internal buffered string path captures positions by recording marks against the in-progress buffer offset; on flush to `CodeWriter`, the offsets translate.

## Filesystem abstraction

```go
// codegen/sink.go (no build tag)
type Sink interface { Create(name string) (io.WriteCloser, error) }

type MemSink struct { ... }
func NewMemSink() *MemSink
func (m *MemSink) Create(name string) (io.WriteCloser, error)
func (m *MemSink) Files() map[string][]byte
```

```go
// codegen/sink_dir.go (//go:build !js)
type DirSink struct{ Root string }

func NewDirSink(root string) *DirSink
func (d *DirSink) Create(name string) (io.WriteCloser, error) // MkdirAll parent
```

CDP/rod test runners that previously took `[]*OutputFile`: switch to taking `*MemSink` (or any `Sink` plus a list of names), or — where they truly need on-disk paths — write a `MemSink` to a temp `DirSink` at the call site, behind `!js`.

## Migration plan

1. **Add primitives.** `codegen/sink.go`, `codegen/sink_dir.go`, `codegen/writer.go`, `codegen/imports.go`. `ImportSpec`, `ImportKind`, `PosEntry`, `SourceMapResult`. No platform wiring yet.

2. **Migrate html end-to-end.** Biggest payoff — exercises imports (ES modules + WASM externs), sourcemaps (JS `.map`), and many file kinds (HTML, JS, manifest). Delete its `goImports`/`jsbundle`-style import side-channels. The `Generate(req, sink) error` signature change lands atomically with the first platform migration; other platforms keep working through a transient `[]*OutputFile`-collecting sink adapter only inside this PR if needed, or each platform migrates in its own PR by toggling between old and new signature via a temporary `Generate2` method.

3. **Migrate remaining platforms** in order: bubbletea, fyne, gtk4, android, none. Each PR collapses that platform's import map and routes file emission through `OpenCodeFile`.

4. **Cutover.** Delete `OutputFile`, `BytesFile`, `TemplateFile`, `RenderTemplates`, `Response`, `Response.Files`. Drop any transient `Generate2`. Update `LangTranslator`: delete `WriteExpr`, `WriteStmt`, `WriteType`, `Eval`. Add `EmitFile`.

5. **Position threading.** Audit translator stmt-level emission to call `w.Mark`. (Go `//line` and JS v3 renderers already live; this step wires them up at the translator level.)

6. **Bundler interaction.** If html bundles JS, the bundler must preserve/merge `//# sourceMappingURL` — track as a follow-up issue once bundled output regresses.

### Plan A status (2026-05-22)

- [x] Sink + MemSink + DirSink (commits `df6d6ec`, `419857e`, `911d2db`)
- [x] ImportSpec + ImportKind (`021a06f`)
- [x] PosEntry + SourceMapResult (`822605e`)
- [x] LangTranslator.RenderHeader + RenderSourceMap stubs on every lang (`da7cebd`, `3ab9c9f`)
- [x] CodeWriter + position tracking + import dedup + maps gating (`35e5ed1`, `31e3e72`, `3cf3c13`)
- [x] Go //line renderer (`7c87c99`)
- [x] JS VLQ encoder + v3 source-map renderer (`7eb28b9`, `a2596e6`)
- [x] Options.maps threaded into Request.Maps (`d668a8a`)
- [x] End-to-end integration tests against real Go + JS translators (`4901599`)
- [ ] Generate(req, sink) signature change — Plan B
- [ ] Per-platform migration through OpenCodeFile — Plan B
- [ ] OutputFile / Response / dead LangTranslator stubs deletion — Plan B
- [ ] Position-marker call sites in EvalStmt / EmitFuncDef — Plan B (rides with platform migration)

## Testing

- `codegen/writer_test.go` — exercises `MemSink` + `CodeWriter` with a fake `LangTranslator`: writes interleaved with imports + marks, asserts header-then-body order and sourcemap sidecar emission.
- Per-platform golden tests: existing snapshot/script tests already validate output; new sink path must produce byte-identical files for current cases.
- `cmd/sngl/testdata/` script tests for `compile`/`build`/`run` keep validating end-to-end.
- Source-map specifics:
  - Go: golden test asserts `//line` directives appear at expected offsets given a fixture.
  - JS: golden test parses sidecar JSON, asserts `mappings` decodes to expected source positions.

## Risks

- **Position-marking discipline.** If translators forget `w.Mark`, source maps become sparse. Mitigation: stmt-level marking is concentrated in `EvalStmt` and `EmitFuncDef` — one place per language, easy to audit.
- **Bundler interaction (JS).** Source-map merging across the bundler may need follow-up. v1 emits unbundled-source maps; bundled-source maps may need a separate pass.
- **Import dedup ordering.** Insertion order is stable but not necessarily "nice". Per-language `RenderHeader` is the sort point — keep all sorting there.
- **Cutover blast radius.** `OutputFile` has ~91 references. Migration plan staggers per-platform to keep PRs reviewable.

## Open questions

None currently. Resolved during brainstorming:

- `Generate` takes a `Sink`; `Response` is deleted. (Approved approach.)
- Kotlin SMAP deferred. (Approved.)
- One spec covers sink + writer + imports + sourcemaps (deeply coupled).
