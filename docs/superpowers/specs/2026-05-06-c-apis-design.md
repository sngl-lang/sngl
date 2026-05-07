# C APIs for SNGL — Design Spec

**Goal:** Allow SNGL language plugins to expose C FFI so platforms can be defined in terms of C APIs (native toolkits like GTK, SDL).

**Date:** 2026-05-06

---

## Overview

Add a `c://` import scheme that parses C headers at compile time and exposes their declarations as SNGL-typed functions, structs, and enums. Language translators that support C emit cgo preambles and `C.symbol` calls. The feature follows the existing `go://` scheme and `WASMCompiler` interface patterns exactly.

---

## Architecture

Three new pieces:

```
codegen/scheme/c/          ← new package: C SchemeImporter
  importer.go              ← URI dispatch + modernc.org/cc/v4 parsing
  types.go                 ← C type → ir.Type mapping

codegen/codegen.go         ← add CCompiler interface (parallel to WASMCompiler)

codegen/lang/golang/
  ccompiler.go             ← new file: GoTranslator implements CCompiler
```

Data flow:

```
import "c://path/to/sdl.h"
        │
        ▼
c.Importer.Resolve(uri, dir)
        │  parses header with modernc.org/cc/v4
        │  maps C types → ir.Type
        ▼
ir.NativeImport{Funcs, Structs, Enums, LinkFlags}
        │
        ▼  (existing codegen path)
ir.Func{NativePkg:"C", NativeName:"SDL_Init"}
        │
        ▼
golang.CCompiler.EmitCHeader(imports) → cgo preamble + import "C"
golang translator → C.SDL_Init(...)
```

`CCompiler` is type-asserted on `LangTranslator` at codegen time, same as `WASMCompiler`. Platforms emit `ir.Call` nodes to native funcs unchanged; the language translator handles C-specific emission.

---

## C Scheme Importer

### URI Forms

- `c://path/to/header.h` — path resolved relative to the importing `.sngl` file's directory
- `c://pkg:gtk+-3.0` — runs `pkg-config --cflags gtk+-3.0` to get include paths, then parses the primary header

### Header Parsing

Uses `modernc.org/cc/v4`. Configure a `cc.Config` with system include paths plus any pkg-config paths, call `cc.Translate()`, walk the resulting AST. For `c://path/to/header.h`: only declarations defined in that specific file are extracted (not transitive includes). For `c://pkg:name`: the resolver invokes `pkg-config --cflags` to get include paths, then parses the package's primary header (the one named in pkg-config's `--modversion` output or the conventional top-level include); declarations from transitive includes are extracted only if referenced by a top-level symbol.

### Type Mapping

| C type | SNGL/ir.Type |
|--------|-------------|
| `int`, `long`, `int32_t`, etc. | `ir.Int` |
| `float`, `double` | `ir.Float` |
| `char*`, `const char*` | `ir.String` |
| `bool`, `_Bool` | `ir.Bool` |
| `T*` (nullable / unknown nullability) | `optional<ref<T>>` |
| `T*` with `_Nonnull` annotation | `ref<T>` |
| `void*` | `ref<OpaqueHandle>` (synthesized per-import empty struct) |
| named struct | `ir.StructType` (fields recursively mapped) |
| enum | `ir.EnumType` |
| typedef to above | unwrap and map |
| function pointer | `ir.Unusable` |
| union | `ir.Unusable` |
| `const` qualifier | stripped (SNGL has no const) |

All C pointer params/returns default to `optional<ref<T>>` — conservative, since C headers rarely annotate nullability. `_Nonnull` (Clang extension) maps to `ref<T>`.

`void*` gets a synthesized empty struct named `<ImportName>OpaqueHandle` (e.g. `SDL_OpaqueHandle`) in `ir.NativeImport.Structs` to avoid collisions across imports.

Unmappable symbols (unions, function pointers) set `Func.Unusable = true` with a checker warning — not a hard error, so partial headers work.

### Output

`ir.NativeImport` populated with:
- `Funcs []ir.Func` — each with `NativePkg:"C"`, `NativeName:"symbol_name"`
- `Structs []ir.StructDef` — C structs mapped to SNGL structs
- `Enums []ir.EnumDef` — C enums mapped to SNGL enums
- `LinkFlags []string` — pkg-config `--libs` output, stored for cgo `#cgo` pragma emission

---

## CCompiler Interface

Added to `codegen/codegen.go`:

```go
// CCompiler is an optional capability on LangTranslator.
// Implemented by translators that can emit C FFI call sites.
type CCompiler interface {
    EmitCHeader(imports []*ir.NativeImport) string
}
```

`EmitCHeader` returns the full cgo preamble block (include directives, `#cgo` link flags) plus `import "C"`. Called once per output file before other imports.

---

## golang/cgo Implementation

`codegen/lang/golang/ccompiler.go` — `GoTranslator` implements `CCompiler`:

```go
func (g *GoTranslator) EmitCHeader(imports []*ir.NativeImport) string
// emits:
// /*
// #cgo pkg-config: gtk+-3.0        (from LinkFlags)
// #include "path/to/header.h"
// */
// import "C"
```

**Call emission** — `translateIRNativeCall` already handles `NativePkg`/`NativeName`. For `NativePkg == "C"`, translator emits `C.SDL_Init(...)`. No changes needed to that path.

**Struct/enum emission** — C structs with `NativePkg == "C"` emit `C.SDL_Rect` type references. Enum constants emit `C.SDL_QUIT` etc.

**Integration** — at file generation time in golang codegen, after collecting all native imports, type-assert `CCompiler`; if present, prepend the cgo header block before other `import` groups.

---

## Error Handling

| Condition | Behavior |
|-----------|----------|
| `pkg-config` not found | Hard error: `"pkg-config required for c://pkg: imports — install it or use a direct header path"` |
| Header file not found | Hard error at import resolution (same as missing Go package) |
| Unmappable symbol | `Func.Unusable = true` + checker warning: `"C symbol foo uses unsupported type union — ignored"` |
| `CCompiler` not implemented | Hard error: `"platform X with lang Y does not support C imports"` |

---

## Testing

**`cmd/sngl/testdata/compile_c_import.txt`** — txtar fixture:
- Minimal fake `test.h` with a struct, enum, and two functions
- `.sngl` file importing `c://test.h`, calling a function, using the struct
- Asserts: cgo preamble present, `import "C"` present, `C.test_func(` in output

**`cmd/sngl/testdata/compile_c_pkgconfig_missing.txt`**:
- `.sngl` file importing `c://pkg:nonexistent`
- Asserts hard error message about pkg-config

**`codegen/scheme/c/importer_test.go`** — unit tests:
- Each C primitive type maps correctly
- Struct with nested fields
- Enum with constants
- `void*` → `ref<OpaqueHandle>` synthesized struct
- Nullable pointer → `optional<ref<T>>`
- `_Nonnull` pointer → `ref<T>`
- Unmappable union → `Unusable`

---

## Out of Scope

- Callbacks / function pointers (map to `Unusable`; future work)
- Unions (map to `Unusable`; future work)
- Non-golang language translators implementing `CCompiler`
- Windows (cgo on Windows has additional toolchain requirements; document as Linux/macOS only for now)
