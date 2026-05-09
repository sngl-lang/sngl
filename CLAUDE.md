# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Reviewing changes

**Start with `testdata/`.** Every language feature in SNGL has at least one fixture in `testdata/*.sngl` that exercises it. Reading those fixtures is the fastest way to understand what a change is meant to do and to spot gaps. When proposing a feature, write the fixture first and let the test framework drive the implementation. Fixtures use directives like `// ERROR(check) "msg"` and `// FOLD(...)` to assert behavior at specific compiler phases — see `internal/testutil/sample.go` for the framework.

## Function syntax

Two valid forms — no third:

- `func name(params) [Type] { ... }` — block-bodied function (return type is optional only when there's no return value)
- `func name(params) => expr` — expression-bodied function

`func name(params) -> Type` is **not valid syntax** (despite occasional appearances in old docs/specs). The arrow `->` is reserved for func *type* expressions only, and even that usage is being phased out.

## Build & Test Commands

```bash
go install ./cmd/sngl          # build CLI (preferred over go build)
go build ./...                 # verify all packages compile
go tool verify                 # full test suite with coverage
go test ./path/to/pkg/...      # test a single package tree
go test -run TestName ./pkg/   # run a single test
go fmt .                       # format Go (run from package dir)
go tool docsgen                # build docs site to _site/
```

WASM build (used by docsgen for playground):

```bash
GOOS=js GOARCH=wasm go build ./internal/playground
```

## Architecture

SNGL is a UI language that compiles to multiple platforms. The pipeline:

```
.sngl source → Parser → AST → Checker → Optimizer → Platform+Lang Codegen → Output
```

**Public API** is in `sngl.go`: `Parse`, `Format`, `FormatTo`, `FormatExpr`, `Check`, `Convert`. Keep this file intact as the stable surface.

### Codegen Plugin System

Languages and platforms register via `init()` and are looked up by name at runtime:

- **`codegen/codegen.go`** — defines `LangTranslator` and `PlatformGenerator` interfaces
- **`codegen/registry.go`** — thread-safe registration (`RegisterLang`, `RegisterPlatform`)
- **`codegen/lang/`** — language translators (golang, javascript, kotlin), each registers in `init()`
- **`codegen/platform/`** — platform generators (android, bubbletea, fyne, html, none), each registers in `init()`
- **`codegen/lang/languages.go`** and **`codegen/platform/platforms.go`** — blank-import all implementations; `cmd/sngl/main.go` imports these to trigger registration

`PlatformGenerator` optionally implements `TestRunner`, `PreviewStyler`, or `Snapshotter` interfaces (checked via type assertion).

### Codegen Models

Platforms choose between two intermediate representations based on their rendering approach:

- **MutationModel** (`codegen/model.go`) — emit a static tree once, then generate targeted `Updater` functions to patch when state changes. Used by HTML and Fyne. Interfaces: `MutationModelEmitter`.
- **RenderModel** (`codegen/model.go`) — re-render the full view from state on every change; framework handles diffing. Used by BubbleTea and Android/Compose. Interfaces: `RenderModelEmitter`.

Both start from `codegen.AnalyzeCommon(doc)` which extracts model fields, computed deps, functions, structs, and timers into a platform-independent `CommonAnalysis`.

### Platform Details

- **html** — two modes selected by `--lang`:
  - `--lang none` (default): static site — one `index.html` per window with inline JS.
  - any language whose translator implements `codegen.HTTPCompiler` (today: `--lang go`): route mode. html collects windows into `HTTPRoute`s and delegates code gen (mux syntax for dynamic paths, server entry, `main()`/ListenAndServe) to the language via `CompileHTTP`. The platform carries no language- or framework-specific logic. POST actions are emitted only for handlers that transitively call functions imported from the target language (e.g. `go://` funcs under `--lang go`); other handlers stay pure client-side JS. Static mode errors the build if any window has a dynamic href.
  Browser testing via CDP (go-rod) is gated behind `//go:build !js` so WASM playground builds exclude it. A `testing_js.go` stub satisfies the interface for WASM.
- **bubbletea** — generates Go TUI code (`model.go`); supports `golang` lang only.
- **fyne** — generates Go desktop code; supports `golang` lang only.
- **android** — generates Android app code; supports `kotlin` and `golang`.
- **none** — no codegen; provides an interpreter-based test runner for headless test execution.

### Key Internal Packages

- **`internal/parser/`** — lexer, recursive-descent parser, formatter for `.sngl` syntax
- **`internal/checker/`** — two-pass type checker using CEL (pass1: register declarations, pass2: validate expressions)
- **`internal/optimize/`** — constant folding, dead code elimination with platform/language awareness
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics)

### Stdlib

Stdlib source lives in `lib/*.sngl` and is embedded via `//go:embed` in `lib/lib.go` (exported as `lib.FS`). `internal/checker/stdlib.go` reads from that FS and parses the files at startup, returning components, functions, structs, units, and style properties. The checker prepends stdlib functions/structs to user definitions (user can override). Platform-specific component implementations are injected via `PkgSource` overrides keyed by platform name.

Notable stdlib packages:

- **`i18n`** — translatable strings via `$"..."` syntax, lowered to `i18n.tr(template, args)`. Supports ICU MessageFormat: plurals (`{n, plural, =0{...} one{...} other{...}}`), selects (`{x, select, key{...} other{...}}`). Manifest-backed translation; runtime locale from `LC_ALL`/`LC_MESSAGES`/`LANG`. Runtimes live in `pkg/{go,js,kotlin}/i18n/`. Direct formatters: `i18n.numberInt`, `i18n.numberFloat`, `i18n.date`, `i18n.time`, `i18n.datetime`, `i18n.select`.

### Runtime packages for generated code

SNGL ships per-target-language runtime packages under `pkg/<lang>/<name>/`. These contain Go/Kotlin/JS code that generated programs import. Examples:

- `pkg/go/i18n/` — Go runtime backing the `i18n` SNGL stdlib package (manifest loader, ICU formatter, date/number formatters wrapping `golang.org/x/text` and `github.com/goodsign/monday`).
- `pkg/js/i18n/` — JavaScript runtime: ICU template parser plus `Intl.NumberFormat`/`DateTimeFormat`/`PluralRules`. Manifest inlined as `globalThis.__SNGL_I18N_MANIFEST__` by the html platform.
- `pkg/kotlin/i18n/` — Kotlin/Android runtime: ICU parser plus `android.icu.text.*`. Manifest loaded from `assets/i18n.manifest.json` via `I18n.init(context)` in `Application.onCreate` (currently injected into `MainActivity.onCreate` since the scaffold has no custom Application).

The `lib/` directory holds **SNGL stdlib declarations** (language-agnostic `.sngl` files embedded into the compiler). The `pkg/` directory holds **runtime implementations** (per-target-language packages emitted into generated code's import graph).

When adding a new stdlib package that needs runtime support:
1. Declare the SNGL surface in `lib/<name>.sngl`.
2. For each target language that needs runtime support, create `pkg/<lang>/<name>/`.
3. The codegen for that language emits `import "git.duckfam.us/jonathan/sngl/pkg/<lang>/<name>"` and translates stdlib calls to that package's API.

### Built-in Generic Types

- **`map<K, V>`** — generic map type. Literal syntax `{k = v}` (disambiguated from struct literals by expected-type context). Methods: `length`, `keys`, `values`, `contains`, `get`. Codegen: Go → `map[K]V`, JS → `Map`, Kotlin → `Map<K,V>`.
- **`iter<T>`** — opaque generic iterator type. `list<T>` and `map<K, V>` implicitly convert to `iter<T>` (list elements; map yields key-value pairs). For-loops bind elements via `for x = iter`; map iteration uses two variables `for k, v = m`. No methods, no fields.

Stdlib collection types support generic methods: `func list<T>.filter(f func(T) bool) list<T>`, `func list<T>.map<U>(f func(T) U) list<U>`, `func map<K, V>.keys() list<K>`, etc. The receiver's type parameters are bound at the call site from the operand's concrete type (e.g. `xs : list<int>` binds `T=int`). Method-level type parameters (the `<U>` after the method name) are inferred from the call's actual argument types — typically from a lambda's return type.

### AST

- **`ast/ast.go`** — top-level declarations and structural types: `Document`, `ComponentDecl`, `VisualNode`, `FuncDef`, `VarDecl`, `ConstDecl`, `StructDef`, `EnumDef`, `UnitDef`, `Param`, `Import`, `IfStmt`, `ForStmt`, `PlatformStmt`
- **`ast/expr.go`** — expression nodes and statements: `BinaryExpr`, `UnaryExpr`, `CallExpr`, `SelectExpr`, `IndexExpr`, `TernaryExpr`, `LiteralExpr`, `IdentExpr`, `ListExpr`, `StructExpr`, `LambdaExpr`, `InterpolationExpr`, plus `AssignStmt`, `EmitStmt`, `StmtBlock`, etc.

### Test Infrastructure

- `testdata/` at project root contains `.sngl` fixture files (e.g., `test_arithmetic.sngl`, `component_simple.sngl`, `error_*.sngl`)
- `cmd/sngl/testdata/` contains CLI golden test files (`txtar` format)
- Test runners resolve testdata via relative paths from their package directory
- Error directive comments in test files (e.g., `// ERROR(check) "invalid color literal"` — phase is `parse`, `check`, etc.) drive expected-failure assertions via `internal/testutil`

**Txtar script tests** (`cmd/sngl/script_test.go`): each `.txt` file is a txtar archive with script commands at top and embedded files below `-- filename --` markers. The `sngl` command runs in-process. Use `stdout`, `stderr`, `exists`, and `!` for assertions.

### Debugging

Structured logging via `slog` at three levels controlled by CLI flags:

- `-v, --verbose` — Info: phase timing, file discovery, external commands run
- `--debug` — Debug: const folding, dead code elimination, import resolution
- `-q, --quiet` — Error only

Dump commands inspect each compiler phase:

```bash
sngl dump parsed [file|dir]                              # after parse + merge
sngl dump checked [file|dir]                             # after type check
sngl dump optimized --lang js --platform html [file|dir] # after optimization
sngl dump analysis --lang js --platform html [file|dir]  # CommonAnalysis as JSON
```
