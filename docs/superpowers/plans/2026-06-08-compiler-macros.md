# Compiler Macros Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `#[alias.name(args)]` macro invocation syntax, a pre/post-check expand pipeline, and the first built-in macro `#[canvas.shape]` via `internal://canvas`.

**Architecture:** Parser wraps attributed declarations in `AttrDecl` nodes. `expand.Pre` (after Parse, before Check) resolves imports via a new `internal/imports` package, dispatches to registered Go handlers top-to-bottom, and replaces `AttrDecl` nodes with the modified inner decl. The checker never sees `AttrDecl`. `expand.Post` (after Check) stubs ready for future behavioral macros.

**Tech Stack:** Go, existing `ast`, `ir`, `internal/parser`, `internal/checker` packages.

---

## File Map

| File | Status | Responsibility |
|------|--------|----------------|
| `ast/ast.go` | Modify | Add `MacroAttr`, `AttrDecl` types |
| `internal/parser/token.go` | Modify | Add `ATTR_OPEN` token constant |
| `internal/parser/lexer.go` | Modify | Lex `#[` → `ATTR_OPEN` |
| `internal/parser/build.go` | Modify | Parse `#[alias.name(args)]` → `AttrDecl` |
| `internal/parser/format.go` | Modify | Format `AttrDecl` back to source |
| `internal/imports/imports.go` | Create | `ImportRef`, `ResolveAliases`, `ParseScheme`, `NamespaceFromPath` |
| `internal/checker/resolve.go` | Modify | Remove `ParseScheme`/`NamespaceFromPath`, import from `internal/imports` |
| `internal/checker/checker.go` | Modify | Use `internal/imports`; add `canvas` to known internal packages; handle `AttrDecl` |
| `internal/expand/registry.go` | Create | `MacroAttr`, `PreHandler`, `PostHandler`, `RegisterPre`, `RegisterPost` |
| `internal/expand/pre.go` | Create | `ExpandPre` — resolve aliases, dispatch handlers, consistency checks |
| `internal/expand/post.go` | Create | `ExpandPost` stub |
| `internal/expand/expand_test.go` | Create | Fixture-driven tests for `ERROR(expand)` |
| `internal/macros/canvas/canvas.go` | Create | `canvas.shape` handler registration |
| `sngl.go` | Modify | Add `ExpandPre`, `ExpandPost` public API functions |
| `testdata/macro_canvas_shape.sngl` | Create | Happy-path fixture for `#[canvas.shape]` |
| `testdata/macro_expand_errors.sngl` | Create | `ERROR(expand)` directive fixtures |

---

## Task 1: AST — add MacroAttr and AttrDecl

**Files:**
- Modify: `ast/ast.go`

- [ ] **Step 1: Write a failing parse test**

In `internal/parser/build_test.go`, add after the `TestParseDisabledDecl` test:

```go
func TestParseAttrDecl(t *testing.T) {
    src := `#[canvas.shape]
component rect() {}`
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse error: %v", err)
    }
    ad, ok := doc.Stmts[0].(*ast.AttrDecl)
    if !ok {
        t.Fatalf("expected AttrDecl, got %T", doc.Stmts[0])
    }
    if len(ad.Attrs) != 1 {
        t.Fatalf("expected 1 attr, got %d", len(ad.Attrs))
    }
    attr := ad.Attrs[0]
    if attr.Alias != "canvas" || attr.Name != "shape" {
        t.Errorf("expected canvas.shape, got %s.%s", attr.Alias, attr.Name)
    }
    if _, ok := ad.Inner.(*ast.ComponentDecl); !ok {
        t.Errorf("expected ComponentDecl inner, got %T", ad.Inner)
    }
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd /home/jonathan/src/git.duckfam.us/jonathan/sngl
go test ./internal/parser/... -run TestParseAttrDecl -v
```

Expected: compile error — `ast.AttrDecl` undefined.

- [ ] **Step 3: Add MacroAttr and AttrDecl to ast/ast.go**

In `ast/ast.go`, after the `DisabledDecl` block (after line 47), insert:

```go
// MacroAttr is a single macro attribute: #[alias.name(args)].
type MacroAttr struct {
	Pos   Pos
	Alias string     // import alias, e.g. "canvas" in #[canvas.shape]
	Name  string     // macro name, e.g. "shape" in #[canvas.shape]
	Args  []Expr     // optional arguments
}

// AttrDecl wraps any declaration prefixed with one or more #[...] attributes.
// The expand pass processes and removes AttrDecl nodes before the checker runs.
type AttrDecl struct {
	Pos   Pos
	Attrs []MacroAttr
	Inner Stmt
}
```

At the end of `ast/ast.go`, in the `StmtPos` block, add:

```go
func (a *AttrDecl) StmtPos() *Pos { return &a.Pos }
```

- [ ] **Step 4: Run test to confirm it still fails (parser not yet wired)**

```bash
go test ./internal/parser/... -run TestParseAttrDecl -v
```

Expected: FAIL — `doc.Stmts[0]` is not `*ast.AttrDecl` (parser does not yet produce it).

- [ ] **Step 5: Commit**

```bash
git add ast/ast.go internal/parser/build_test.go
git commit -m "feat(ast): add MacroAttr and AttrDecl for macro attribute syntax"
```

---

## Task 2: Lexer — add ATTR_OPEN token for `#[`

**Files:**
- Modify: `internal/parser/token.go`
- Modify: `internal/parser/lexer.go`

- [ ] **Step 1: Add ATTR_OPEN to token.go**

In `internal/parser/token.go`, find the block of `TokenType` constants. Add `ATTR_OPEN` alongside existing tokens:

```go
ATTR_OPEN TokenType = 0x5A // #[ — start of macro attribute
```

- [ ] **Step 2: Add String() case for ATTR_OPEN**

In `token.go`, find the `String()` method on `TokenType` and add:

```go
case ATTR_OPEN:
    return "ATTR_OPEN"
```

- [ ] **Step 3: Update lexer to emit ATTR_OPEN for `#[`**

In `internal/parser/lexer.go`, find the `#` handler (around line 152):

```go
// Color (#rrggbb / #rrggbbaa) or element reference (#id)
if ch == '#' && l.pos+1 < len(l.input) {
    next := l.peekAt(1)
    if isHexDigit(next) || isIdentStart(next) {
        return l.scanHashToken(startLine, startCol)
    }
}
```

Change to:

```go
// #[ — macro attribute open; #hex — color; #id — element reference
if ch == '#' && l.pos+1 < len(l.input) {
    next := l.peekAt(1)
    if next == '[' {
        l.advance() // #
        l.advance() // [
        return l.tok(ATTR_OPEN, "#[", startLine, startCol)
    }
    if isHexDigit(next) || isIdentStart(next) {
        return l.scanHashToken(startLine, startCol)
    }
}
```

- [ ] **Step 4: Write a lexer token test**

In `internal/parser/build_test.go` (or a new `lexer_test.go` if one exists), add:

```go
func TestLexAttrOpen(t *testing.T) {
    src := `#[canvas.shape]`
    doc, err := Parse("test.sngl", []byte(src))
    // We expect a parse error since AttrDecl is not wired yet,
    // but ILLEGAL token would cause a different error than ATTR_OPEN.
    // Just verify no panic.
    _ = doc
    _ = err
}
```

- [ ] **Step 5: Run existing lexer/parser tests to check no regressions**

```bash
go test ./internal/parser/... -v 2>&1 | tail -20
```

Expected: existing tests pass; `TestParseAttrDecl` still fails (parser not wired).

- [ ] **Step 6: Commit**

```bash
git add internal/parser/token.go internal/parser/lexer.go internal/parser/build_test.go
git commit -m "feat(lexer): add ATTR_OPEN token for #[ macro attribute syntax"
```

---

## Task 3: Parser — parse `#[alias.name(args)]` into AttrDecl

**Files:**
- Modify: `internal/parser/build.go`

- [ ] **Step 1: Locate the top-level statement parsing loop in build.go**

```bash
grep -n "func.*parseDoc\|func.*parseStmt\|func.*topLevel\|doc\.Stmts" internal/parser/build.go | head -20
```

Find where `doc.Stmts` is appended to at the document level.

- [ ] **Step 2: Add parseAttrDecl function to build.go**

At an appropriate location in `build.go`, add:

```go
// parseAttrDecl parses one #[alias.name(args)] attribute.
// Called after the ATTR_OPEN token has been consumed.
func (p *parser) parseAttrDecl(startPos ast.Pos) ast.MacroAttr {
    // expect: alias.name or just name
    alias := p.expectIdent("macro attribute alias")
    var name string
    if p.peek() == DOT {
        p.advance() // consume .
        name = p.expectIdent("macro attribute name")
    } else {
        // bare name without alias — alias stays empty, name = alias value
        name = alias
        alias = ""
    }
    var args []ast.Expr
    if p.peek() == LPAREN {
        p.advance() // (
        for p.peek() != RPAREN && p.peek() != EOF {
            args = append(args, p.parseExpr())
            if p.peek() == COMMA {
                p.advance()
            }
        }
        p.expect(RPAREN, "closing ) in macro attribute")
    }
    p.expect(RBRACKET, "closing ] in macro attribute")
    return ast.MacroAttr{Pos: startPos, Alias: alias, Name: name, Args: args}
}
```

- [ ] **Step 3: Wire AttrDecl into the top-level statement loop**

In the document-level statement parsing loop (where `doc.Stmts` is appended), before the existing statement dispatch, add handling for `ATTR_OPEN`:

Find the section that looks like:
```go
case token.SLASH_DASH: // or similar for DisabledDecl
```

Add before or alongside it:
```go
case ATTR_OPEN:
    attrPos := p.pos()
    p.advance() // consume ATTR_OPEN
    var attrs []ast.MacroAttr
    attrs = append(attrs, p.parseAttrDecl(attrPos))
    // Collect consecutive #[...] attributes
    for p.peek() == ATTR_OPEN {
        pos := p.pos()
        p.advance()
        attrs = append(attrs, p.parseAttrDecl(pos))
    }
    inner := p.parseTopLevelStmt() // parse the attributed declaration
    doc.Stmts = append(doc.Stmts, &ast.AttrDecl{
        Pos:   attrPos,
        Attrs: attrs,
        Inner: inner,
    })
```

Note: the exact method names (`p.pos()`, `p.advance()`, `p.parseTopLevelStmt()`) must match the existing parser conventions. Run `grep -n "func (p \*parser)" internal/parser/build.go | head -30` to find the actual method names.

- [ ] **Step 4: Run the failing test to verify it now passes**

```bash
go test ./internal/parser/... -run TestParseAttrDecl -v
```

Expected: PASS.

- [ ] **Step 5: Run full parser test suite**

```bash
go test ./internal/parser/... -v 2>&1 | grep -E "FAIL|PASS|ok"
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/parser/build.go
git commit -m "feat(parser): parse #[alias.name(args)] into AttrDecl nodes"
```

---

## Task 4: Formatter — round-trip AttrDecl

**Files:**
- Modify: `internal/parser/format.go`

- [ ] **Step 1: Write a formatter round-trip test**

In `internal/parser/build_test.go`, add:

```go
func TestFormatAttrDecl(t *testing.T) {
    src := "#[canvas.shape]\ncomponent rect() {}"
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    got := Format(doc)
    if got != src+"\n" {
        t.Errorf("format round-trip mismatch:\ngot:  %q\nwant: %q", got, src+"\n")
    }
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/parser/... -run TestFormatAttrDecl -v
```

Expected: FAIL — formatter doesn't handle `AttrDecl`.

- [ ] **Step 3: Add AttrDecl case to the formatter's statement dispatch**

In `internal/parser/format.go`, find the `case *ast.DisabledDecl:` block (around line 251). Add alongside it:

```go
case *ast.AttrDecl:
    f.writeAttrDecl(x)
```

Then add the `writeAttrDecl` function near `writeDisabledDecl`:

```go
func (f *formatter) writeAttrDecl(d *ast.AttrDecl) {
    for _, attr := range d.Attrs {
        f.write("#[")
        if attr.Alias != "" {
            f.write(attr.Alias)
            f.write(".")
        }
        f.write(attr.Name)
        if len(attr.Args) > 0 {
            f.write("(")
            for i, arg := range attr.Args {
                if i > 0 {
                    f.write(", ")
                }
                f.write(FormatExpr(arg))
            }
            f.write(")")
        }
        f.write("]")
        f.newline()
    }
    f.writeStmt(d.Inner)
}
```

Note: `f.write`, `f.newline`, `f.writeStmt` must match the existing formatter API. Check with `grep -n "func (f \*formatter)" internal/parser/format.go | head -20`.

- [ ] **Step 4: Run formatter test**

```bash
go test ./internal/parser/... -run TestFormatAttrDecl -v
```

Expected: PASS.

- [ ] **Step 5: Run full parser suite**

```bash
go test ./internal/parser/... 2>&1 | grep -E "FAIL|ok"
```

- [ ] **Step 6: Commit**

```bash
git add internal/parser/format.go internal/parser/build_test.go
git commit -m "feat(format): round-trip AttrDecl nodes to #[alias.name(args)] syntax"
```

---

## Task 5: internal/imports — extract import resolution helpers

**Files:**
- Create: `internal/imports/imports.go`
- Modify: `internal/checker/resolve.go`

- [ ] **Step 1: Create internal/imports/imports.go**

```bash
mkdir -p internal/imports
```

Create `internal/imports/imports.go`:

```go
// Package imports provides shared import-path parsing and lightweight
// alias resolution used by both the checker and the expand pass.
package imports

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ImportRef holds the scheme and URI for a resolved import path.
type ImportRef struct {
	Scheme string // e.g. "internal", "go", "platform"
	URI    string // e.g. "canvas", "pkg/path"
}

// ParseScheme extracts the scheme and URI from an import path.
// Returns ("go", "pkg/path") for "go://pkg/path", or ("", path) for directory imports.
func ParseScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}

// NamespaceFromPath derives a namespace alias from an import path.
// Uses the last path segment: "widgets/counter" → "counter".
func NamespaceFromPath(path string) string {
	_, uri := ParseScheme(path)
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// ResolveAliases scans import declarations in docs and returns a map of
// alias → ImportRef. No IR building; scheme and URI only. Used by the
// expand pass to resolve #[alias.name] macro attributes before type-checking.
func ResolveAliases(docs []*ast.Document) map[string]ImportRef {
	out := make(map[string]ImportRef)
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok {
				continue
			}
			path := imp.Path
			if imp.Replace != "" {
				path = imp.Replace
			}
			scheme, uri := ParseScheme(path)
			alias := imp.Alias
			if alias == "" {
				alias = NamespaceFromPath(imp.Path)
			}
			out[alias] = ImportRef{Scheme: scheme, URI: uri}
		}
	}
	return out
}
```

- [ ] **Step 2: Write a test for ResolveAliases**

Create `internal/imports/imports_test.go`:

```go
package imports_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
)

func TestResolveAliases(t *testing.T) {
	docs := []*ast.Document{
		{Stmts: []ast.Stmt{
			&ast.Import{Path: "internal://canvas"},
			&ast.Import{Path: "internal://storage", Alias: "store"},
			&ast.Import{Path: "go://mypkg/widget"},
		}},
	}
	got := imports.ResolveAliases(docs)

	if got["canvas"].Scheme != "internal" || got["canvas"].URI != "canvas" {
		t.Errorf("canvas: got %+v", got["canvas"])
	}
	if got["store"].Scheme != "internal" || got["store"].URI != "storage" {
		t.Errorf("store: got %+v", got["store"])
	}
	if got["widget"].Scheme != "go" || got["widget"].URI != "mypkg/widget" {
		t.Errorf("widget: got %+v", got["widget"])
	}
}
```

- [ ] **Step 3: Run imports tests**

```bash
go test ./internal/imports/... -v
```

Expected: PASS.

- [ ] **Step 4: Update checker/resolve.go to use internal/imports**

In `internal/checker/resolve.go`, replace the `ParseScheme` and `NamespaceFromPath` function bodies with forwarding calls to the `imports` package:

```go
import "git.duckfam.us/jonathan/sngl/internal/imports"

// ParseScheme extracts the scheme and URI from an import path.
func ParseScheme(path string) (scheme, uri string) {
	return imports.ParseScheme(path)
}

// NamespaceFromPath derives a namespace alias from an import path.
func NamespaceFromPath(path string) string {
	return imports.NamespaceFromPath(path)
}
```

Keep the function signatures identical so callers in the checker package are unaffected.

- [ ] **Step 5: Verify no regressions**

```bash
go build ./... && go test ./internal/checker/... 2>&1 | grep -E "FAIL|ok"
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/imports/ internal/checker/resolve.go
git commit -m "feat(imports): extract ParseScheme/NamespaceFromPath + add ResolveAliases"
```

---

## Task 6: Checker — add canvas to known internal packages; handle AttrDecl

**Files:**
- Modify: `internal/checker/checker.go`

- [ ] **Step 1: Add canvas to the internal package switch**

In `internal/checker/checker.go`, find the `switch uri {` block inside `registerImport` (around line 339):

```go
switch uri {
case "stdlib":
    irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.Intrinsics)
case "alert":
    // ...
```

Add:

```go
case "canvas":
    // Macro-only package: provides no IR symbols at runtime.
    // The expand pass handles #[canvas.*] attributes before type-checking.
    irImport.Pkg = &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
```

- [ ] **Step 2: Add AttrDecl handling in checker pass1**

In `internal/checker/checker.go`, find the pass1 statement dispatch (the `switch stmt.(type)` block that processes top-level statements). Find where `*ast.DisabledDecl` is handled:

```go
case *ast.DisabledDecl:
    // disabled — skip
```

Add alongside it:

```go
case *ast.AttrDecl:
    // Attributes not resolved by expand.Pre (e.g. non-internal imports, future
    // user-defined macros) pass through as AttrDecl. Check the inner declaration.
    c.checkTopLevelStmt(s.Inner)
```

Note: the exact dispatch method name must match the existing code. Use `grep -n "DisabledDecl\|pass1\|topLevel" internal/checker/checker.go | head -20` to find the actual location.

- [ ] **Step 3: Add AttrDecl handling in checker pass2**

In the pass2 statement dispatch (if one exists), add the same pattern for `*ast.AttrDecl`:

```go
case *ast.AttrDecl:
    c.checkTopLevelStmtPass2(s.Inner)
```

- [ ] **Step 4: Verify checker tests still pass**

```bash
go test ./internal/checker/... 2>&1 | grep -E "FAIL|ok"
```

Expected: all pass; `import "internal://canvas"` no longer produces "unknown internal package" error.

- [ ] **Step 5: Commit**

```bash
git add internal/checker/checker.go
git commit -m "feat(checker): add canvas macro-only package; handle AttrDecl transparently"
```

---

## Task 7: internal/expand — registry, pre-check pass, post-check stub

**Files:**
- Create: `internal/expand/registry.go`
- Create: `internal/expand/pre.go`
- Create: `internal/expand/post.go`
- Create: `internal/expand/expand_test.go`

- [ ] **Step 1: Create registry.go**

```bash
mkdir -p internal/expand
```

Create `internal/expand/registry.go`:

```go
// Package expand implements the compiler macro expansion pipeline.
// Pre-check expansion (ExpandPre) runs after parsing, before type-checking.
// Post-check expansion (ExpandPost) runs after type-checking, before lowering.
package expand

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// MacroAttr is a resolved macro attribute ready for handler dispatch.
type MacroAttr struct {
	Pos   ast.Pos
	Alias string     // import alias
	Name  string     // macro name
	Args  []ast.Expr // optional arguments
}

// PreHandler transforms an AST declaration before type-checking.
// Returns the (possibly modified) declaration, or an error.
type PreHandler func(attr MacroAttr, decl ast.Stmt) (ast.Stmt, error)

// PostHandler transforms an IR symbol after type-checking.
// Returns the (possibly modified) symbol, or an error.
type PostHandler func(attr MacroAttr, sym ir.Symbol) (ir.Symbol, error)

var (
	mu       sync.RWMutex
	preRegs  = map[string]map[string]PreHandler{}  // uri → name → handler
	postRegs = map[string]map[string]PostHandler{} // uri → name → handler
)

// RegisterPre registers a pre-check macro handler for the given internal URI and name.
// Call from init() in macro implementation packages.
func RegisterPre(internalURI, name string, h PreHandler) {
	mu.Lock()
	defer mu.Unlock()
	if preRegs[internalURI] == nil {
		preRegs[internalURI] = map[string]PreHandler{}
	}
	preRegs[internalURI][name] = h
}

// RegisterPost registers a post-check macro handler for the given internal URI and name.
// Call from init() in macro implementation packages.
func RegisterPost(internalURI, name string, h PostHandler) {
	mu.Lock()
	defer mu.Unlock()
	if postRegs[internalURI] == nil {
		postRegs[internalURI] = map[string]PostHandler{}
	}
	postRegs[internalURI][name] = h
}

func lookupPre(uri, name string) (PreHandler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	if m, ok := preRegs[uri]; ok {
		h, ok := m[name]
		return h, ok
	}
	return nil, false
}
```

- [ ] **Step 2: Create pre.go**

Create `internal/expand/pre.go`:

```go
package expand

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ExpandPre runs pre-check macro expansion on docs, replacing AttrDecl nodes
// with the transformed inner declarations. Returns diagnostics for any errors.
// Modifies docs in place.
func ExpandPre(docs []*ast.Document) []ir.Diagnostic {
	aliases := imports.ResolveAliases(docs)
	var diags []ir.Diagnostic

	for _, doc := range docs {
		var newStmts []ast.Stmt
		for _, stmt := range doc.Stmts {
			ad, ok := stmt.(*ast.AttrDecl)
			if !ok {
				newStmts = append(newStmts, stmt)
				continue
			}
			result, attrDiags := applyAttrs(ad, aliases)
			diags = append(diags, attrDiags...)
			newStmts = append(newStmts, result)
		}
		doc.Stmts = newStmts
	}
	return diags
}

// applyAttrs applies macro attributes to a declaration top-to-bottom.
// Returns the transformed declaration and any diagnostics.
func applyAttrs(ad *ast.AttrDecl, aliases map[string]imports.ImportRef) (ast.Stmt, []ir.Diagnostic) {
	decl := ad.Inner
	var diags []ir.Diagnostic

	for _, attr := range ad.Attrs {
		ref, aliasKnown := aliases[attr.Alias]

		if !aliasKnown {
			diags = append(diags, ir.Diagnostic{
				Pos:     attr.Pos,
				Message: fmt.Sprintf("unresolved import alias %q in macro attribute", attr.Alias),
			})
			continue
		}

		if ref.Scheme != "internal" {
			// Non-internal import: user-defined macros (future). Silently skip.
			continue
		}

		handler, found := lookupPre(ref.URI, attr.Name)
		if !found {
			diags = append(diags, ir.Diagnostic{
				Pos:     attr.Pos,
				Message: fmt.Sprintf("unknown macro %q in package %q", attr.Name, ref.URI),
			})
			continue
		}

		mattr := MacroAttr{Pos: attr.Pos, Alias: attr.Alias, Name: attr.Name, Args: attr.Args}
		result, err := handler(mattr, decl)
		if err != nil {
			diags = append(diags, ir.Diagnostic{
				Pos:     attr.Pos,
				Message: err.Error(),
			})
			continue
		}

		// Consistency check: declaration kind must not change.
		if fmt.Sprintf("%T", result) != fmt.Sprintf("%T", decl) {
			diags = append(diags, ir.Diagnostic{
				Pos:     attr.Pos,
				Message: fmt.Sprintf("macro %s.%s changed declaration kind from %T to %T", attr.Alias, attr.Name, decl, result),
			})
			continue
		}

		decl = result
	}

	return decl, diags
}
```

- [ ] **Step 3: Create post.go stub**

Create `internal/expand/post.go`:

```go
package expand

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// ExpandPost runs post-check macro expansion on pkg.
// Currently a no-op; post-check handlers registered via RegisterPost will be
// dispatched here when implemented.
func ExpandPost(pkg *ir.Package) []ir.Diagnostic {
	return nil
}
```

- [ ] **Step 4: Write expand tests**

Create `internal/expand/expand_test.go`:

```go
package expand_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func parseDoc(t *testing.T, src string) *ast.Document {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func TestExpandPre_UnknownAlias(t *testing.T) {
	doc := parseDoc(t, `#[bogus.thing]
component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown alias, got none")
	}
	if got := diags[0].Message; got == "" {
		t.Error("expected non-empty diagnostic message")
	}
}

func TestExpandPre_NoAttrs_NoOp(t *testing.T) {
	doc := parseDoc(t, `component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
	if _, ok := doc.Stmts[0].(*ast.ComponentDecl); !ok {
		t.Errorf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
}

func TestExpandPre_KnownInternal_UnknownName(t *testing.T) {
	// internal://canvas is registered but "bogusname" is not a registered macro.
	doc := parseDoc(t, `import "internal://canvas"
#[canvas.bogusname]
component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown macro name, got none")
	}
}
```

- [ ] **Step 5: Run expand tests**

```bash
go test ./internal/expand/... -v
```

Expected: PASS (the unknown-alias test needs `internal://canvas` not yet registered; adjust if needed).

- [ ] **Step 6: Commit**

```bash
git add internal/expand/
git commit -m "feat(expand): registry, ExpandPre dispatch, ExpandPost stub"
```

---

## Task 8: sngl.go — add ExpandPre and ExpandPost to public API

**Files:**
- Modify: `sngl.go`

- [ ] **Step 1: Add ExpandPre and ExpandPost to sngl.go**

In `sngl.go`, after the `Parse` function, add:

```go
// ExpandPre runs pre-check macro expansion on docs. Call after Parse and before
// Check. Modifies docs in place. Returns diagnostics for any expansion errors.
func ExpandPre(docs []*ast.Document) []ir.Diagnostic {
	return expand.ExpandPre(docs)
}

// ExpandPost runs post-check macro expansion on pkg. Call after Check and before
// Lower. Currently a no-op; reserved for future behavioral macros.
func ExpandPost(pkg *ir.Package) []ir.Diagnostic {
	return expand.ExpandPost(pkg)
}
```

Add `"git.duckfam.us/jonathan/sngl/internal/expand"` to the imports block in `sngl.go`.

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add sngl.go
git commit -m "feat(sngl): expose ExpandPre and ExpandPost in public API"
```

---

## Task 9: internal/macros/canvas — canvas.shape handler

**Files:**
- Create: `internal/macros/canvas/canvas.go`

- [ ] **Step 1: Write failing tests for canvas.shape**

Create `testdata/macro_canvas_shape.sngl`:

```sngl
import "internal://canvas"

#[canvas.shape]
component rect(x int, y int, w int, h int) {}

#[canvas.shape]
component circle(cx int, cy int, r int) {}
```

Create `testdata/macro_canvas_errors.sngl`:

```sngl
import "internal://canvas"

// ERROR(expand) "shape macro requires a component declaration"
#[canvas.shape]
var bad = 5

// ERROR(expand) "shape components do not support event declarations"
#[canvas.shape]
component badShape(@click ClickEvent) {}
```

- [ ] **Step 2: Create the canvas macro package**

```bash
mkdir -p internal/macros/canvas
```

Create `internal/macros/canvas/canvas.go`:

```go
// Package canvas registers compiler macros for the internal://canvas package.
package canvas

import (
	"errors"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	expand.RegisterPre("canvas", "shape", shapeHandler)
}

// shapeHandler validates that a component declaration is suitable for use as a
// canvas shape: no event handlers. Sets IsShape=true on the component.
func shapeHandler(attr expand.MacroAttr, decl ast.Stmt) (ast.Stmt, error) {
	comp, ok := decl.(*ast.ComponentDecl)
	if !ok {
		return decl, errors.New("shape macro requires a component declaration")
	}
	for _, p := range comp.Props.Props {
		if _, isEvent := p.(ast.EventDecl); isEvent {
			return decl, errors.New("shape components do not support event declarations")
		}
	}
	comp.IsShape = true
	return comp, nil
}
```

- [ ] **Step 3: Add IsShape field to ast.ComponentDecl**

In `ast/ast.go`, find `ComponentDecl`:

```go
type ComponentDecl struct {
    Pos          Pos
    Name         string
    Props        PropList
    HasParens    bool
    ChildrenType TypeExpr
    Body         StmtBlock
}
```

Add `IsShape bool`:

```go
type ComponentDecl struct {
    Pos          Pos
    Name         string
    Props        PropList
    HasParens    bool
    ChildrenType TypeExpr
    Body         StmtBlock
    IsShape      bool // set by #[canvas.shape] macro expansion
}
```

- [ ] **Step 4: Wire the canvas macro package into the expand test**

In `internal/expand/expand_test.go`, add a blank import of the canvas package so `init()` runs:

```go
import (
    _ "git.duckfam.us/jonathan/sngl/internal/macros/canvas"
    // ... existing imports
)
```

Update `TestExpandPre_KnownInternal_UnknownName` to use a name that is genuinely unregistered (not "shape"):

```go
func TestExpandPre_KnownInternal_UnknownName(t *testing.T) {
    doc := parseDoc(t, `import "internal://canvas"
#[canvas.notarealname]
component foo() {}`)
    diags := expand.ExpandPre([]*ast.Document{doc})
    if len(diags) == 0 {
        t.Fatal("expected diagnostic for unknown macro name, got none")
    }
}
```

Add a happy-path test:

```go
func TestExpandPre_CanvasShape_ValidComponent(t *testing.T) {
    doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
component rect(x int, y int) {}`)
    diags := expand.ExpandPre([]*ast.Document{doc})
    if len(diags) != 0 {
        t.Fatalf("expected no diagnostics, got: %v", diags)
    }
    comp, ok := doc.Stmts[1].(*ast.ComponentDecl)
    if !ok {
        t.Fatalf("expected ComponentDecl after expand, got %T", doc.Stmts[1])
    }
    if !comp.IsShape {
        t.Error("expected IsShape=true after #[canvas.shape] expansion")
    }
}

func TestExpandPre_CanvasShape_RejectsVar(t *testing.T) {
    doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
var bad = 5`)
    diags := expand.ExpandPre([]*ast.Document{doc})
    if len(diags) == 0 {
        t.Fatal("expected diagnostic rejecting var")
    }
    if diags[0].Message != "shape macro requires a component declaration" {
        t.Errorf("unexpected message: %q", diags[0].Message)
    }
}

func TestExpandPre_CanvasShape_RejectsEventDecl(t *testing.T) {
    doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
component bad(@click ClickEvent) {}`)
    diags := expand.ExpandPre([]*ast.Document{doc})
    if len(diags) == 0 {
        t.Fatal("expected diagnostic rejecting event declaration")
    }
    if diags[0].Message != "shape components do not support event declarations" {
        t.Errorf("unexpected message: %q", diags[0].Message)
    }
}
```

- [ ] **Step 5: Run canvas macro tests**

```bash
go test ./internal/expand/... ./internal/macros/... -v
```

Expected: all pass.

- [ ] **Step 6: Run full test suite**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok"
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/macros/canvas/ internal/expand/expand_test.go ast/ast.go testdata/macro_canvas_shape.sngl testdata/macro_canvas_errors.sngl
git commit -m "feat(macros/canvas): canvas.shape pre-check handler; IsShape field on ComponentDecl"
```

---

## Task 10: Wire expand into CLI and testdata fixture runner

**Files:**
- Modify: `cmd/sngl/main.go` (or wherever Parse → Check is called)
- Modify: `internal/testutil/sample.go`

- [ ] **Step 1: Blank-import canvas macro package in CLI**

In `cmd/sngl/main.go`, add a blank import so `canvas.init()` registers the handler:

```go
import (
    // existing imports ...
    _ "git.duckfam.us/jonathan/sngl/internal/macros/canvas"
)
```

- [ ] **Step 3: Find where Parse → Check is called in the CLI**

```bash
grep -rn "sngl\.Check\|checker\.Check\|\.Check(" cmd/sngl/ --include="*.go" | head -10
```

- [ ] **Step 4: Insert ExpandPre between Parse and Check in the CLI compile path**

In the CLI compile path (the function that runs Parse then Check), insert:

```go
// After parse, before check:
if expandDiags := sngl.ExpandPre([]*ast.Document{doc}); len(expandDiags) > 0 {
    // format and report expand diagnostics the same way check diagnostics are reported
    for _, d := range expandDiags {
        // use existing diagnostic reporting mechanism
    }
}
```

Use the existing diagnostic reporting pattern — run `grep -n "diag\|Diagnostic" cmd/sngl/main.go | head -20` to find it.

- [ ] **Step 5: Wire ExpandPre into the testutil sample runner**

In `internal/testutil/sample.go`, find where `Check` is called on a sample (around line 175+). Before the `Check` call, add:

```go
expandDiags := sngl.ExpandPre([]*ast.Document{doc})
if s.ExpectsError("expand") {
    testutil.AssertDiagnostics(t, expandDiags, s.PhaseErrors("expand"), "expand")
    if len(expandDiags) > 0 {
        return // stop pipeline if expand errored
    }
}
```

Note: `sngl` must be imported in `sample.go`. Check if it already is; if not, add `"git.duckfam.us/jonathan/sngl"` to imports.

- [ ] **Step 6: Run testdata fixture tests**

```bash
go test ./... -run TestSample 2>&1 | grep -E "FAIL|PASS|ok"
```

Expected: all pass, including the new `macro_canvas_errors.sngl` fixture.

- [ ] **Step 7: Final full build and test**

```bash
go build ./... && go test ./... 2>&1 | grep -E "FAIL|ok"
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add cmd/sngl/ internal/testutil/sample.go
git commit -m "feat: wire ExpandPre into CLI compile path and testdata fixture runner"
```
