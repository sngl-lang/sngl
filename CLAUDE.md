# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

**Public API** is in `sngl.go`: `Parse`, `Format`, `FormatNode`, `Check`, `Optimize`. Keep this file intact as the stable surface.

### Codegen Plugin System

Languages and platforms register via `init()` and are looked up by name at runtime:

- **`codegen/codegen.go`** — defines `LangTranslator` and `PlatformGenerator` interfaces
- **`codegen/registry.go`** — thread-safe registration (`RegisterLang`, `RegisterPlatform`)
- **`codegen/lang/`** — language translators (js, golang), each registers in `init()`
- **`codegen/platform/`** — platform generators (html, bubbletea, none), each registers in `init()`
- **`codegen/lang/languages.go`** and **`codegen/platform/platforms.go`** — blank-import all implementations; `cmd/sngl/main.go` imports these to trigger registration

`PlatformGenerator` optionally implements `TestRunner`, `PreviewStyler`, or `Snapshotter` interfaces (checked via type assertion).

### Platform Details

- **html** — generates single `index.html` with inline JS; supports `js` lang only. Browser testing via CDP (go-rod) is gated behind `//go:build !js` so WASM playground builds exclude it. A `testing_js.go` stub satisfies the interface for WASM.
- **bubbletea** — generates Go TUI code (`model.go`); supports `go` lang only.
- **none** — no codegen; provides an interpreter-based test runner for headless test execution.

### Key Internal Packages

- **`internal/snglparser/`** — lexer, recursive-descent parser, formatter for `.sngl` syntax
- **`internal/checker/`** — two-pass type checker using CEL (pass1: register declarations, pass2: validate expressions)
- **`internal/optimize/`** — constant folding, dead code elimination with platform/language awareness
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics)

### AST

- **`ast/ast.go`** — top-level types: `Document`, `Component`, `App`, `VisualNode`, `TestDef`, `Data`, `Computed`, `Param`, `Struct`, `Enum`
- **`ast/snglexpr.go`** — expression nodes: `BinaryExpr`, `UnaryExpr`, `CallExpr`, `MethodExpr`, `SelectExpr`, `IndexExpr`, `TernaryExpr`, `LiteralExpr`, `IdentExpr`, `ListExpr`, `StructExpr`
- **`ast/expr.go`** — `Expr` wrapper bridging literal values and SNGL expression nodes

### Test Infrastructure

- `testdata/` at project root contains `.sngl` fixture files (e.g., `test_arithmetic.sngl`, `component_simple.sngl`, `error_*.sngl`)
- `cmd/sngl/testdata/` contains CLI golden test files (`txtar` format)
- Test runners resolve testdata via relative paths from their package directory
- Error directive comments in test files (e.g., `// error:test "substring"`) drive expected-failure assertions via `internal/testutil`
