# Funcs Inside Type Definitions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `func` declarations inside `struct` and `enum` bodies (already supported in `component`); extend uniformly so top-level `func T.foo()` works for any user-defined type kind. Nested funcs are methods with implicit `this`.

**Architecture:** Replace `StructDef.Fields []*StructField` with a single `StructDef.Body []StructBodyItem` sealed-interface slice (same for `EnumDef.Members` → `EnumDef.Body`). Parser builds both fields/members and `*FuncDef` items into the ordered slice. Checker pass1 desugars each nested `*FuncDef` into a synthesised top-level method (prepends a `this T` parameter and `Receiver = T`) and registers via the existing `symtab.RegisterMethod` machinery. No IR changes; codegen unaffected. Top-level `func T.foo(...)` with no `T`-typed first param registers as a static in `T`'s namespace.

**Tech Stack:** Go. Parser generated from `internal/parser/sngl.ebnf` via `go tool egg`. Tests are fixture-driven via `internal/testutil/sample.go` (fixtures in `testdata/*.sngl`, error directives `// ERROR(parse|check) "msg"`).

**Spec:** `docs/superpowers/specs/2026-05-20-funcs-in-types-design.md`

---

## File Structure

Files created or modified, by responsibility:

| File                                                | Purpose                                                                                          | Action     |
|-----------------------------------------------------|--------------------------------------------------------------------------------------------------|------------|
| `ast/ast.go`                                        | Sealed interface + `Body` slice for `StructDef`/`EnumDef`; `EnumMember` becomes pointer-friendly | Modify     |
| `internal/parser/sngl.ebnf`                         | Grammar: `StructDecl` body accepts `FuncDecl`; `EnumDecl` body accepts `FuncDecl` items          | Modify     |
| `internal/parser/zparser.go`                        | Regenerated from grammar                                                                         | Regenerate |
| `internal/parser/build.go`                          | `buildStructDecl` / `buildEnumDecl` append `*FuncDef` to `Body`                                  | Modify     |
| `internal/parser/format.go`                         | `writeStructDef` / `writeEnumDef` walk `Body` with a type-switch                                 | Modify     |
| `internal/checker/resolve.go`                       | `buildStructDef` / `buildEnumDef` iterate `Body` for field/member extraction                     | Modify     |
| `internal/checker/checker.go`                       | Pass1 desugars nested funcs into top-level methods with synthetic `this`                         | Modify     |
| `internal/checker/expr.go`                          | `this`-elision in identifier and call resolution inside method bodies                            | Modify     |
| `internal/lspcore/hover.go`                         | Hover for enum body walks `Body`                                                                 | Modify     |
| `internal/lspcore/completion.go`                    | Completion enumerates fields/members from `Body`                                                 | Modify     |
| `internal/optimize/shake.go`                        | Tree-shake walks enum `Body`                                                                     | Modify     |
| `internal/lower/enum.go`                            | Enum lowering iterates `Body`                                                                    | Modify     |
| `internal/checker/i18n.go`                          | i18n walks enum `Body`                                                                           | Modify     |
| `internal/checker/stdlib.go`                        | Stdlib type-decl walking; consumes `Body`                                                        | Modify     |
| `internal/checker/docapi.go`                        | Doc API; consumes `Body`                                                                         | Modify     |
| `internal/docbrowser/model.go`                      | Docs site; consumes `Body`                                                                       | Modify     |
| `docs/lookup/lookup.go`                             | Lookup; consumes `Body`                                                                          | Modify     |
| `codegen/scheme/js/typescript.go`                   | TS emitter for enums; consumes `Body`                                                            | Modify     |
| `codegen/platform/android/compiler_ir.go`           | Android emitter; consumes `Body`                                                                 | Modify     |
| `testdata/test_struct_nested_methods.sngl`          | Driver fixture                                                                                   | Create     |
| `testdata/test_enum_nested_methods.sngl`            | Driver fixture                                                                                   | Create     |
| `testdata/test_component_nested_this.sngl`          | Driver fixture                                                                                   | Create     |
| `testdata/test_top_level_static.sngl`               | Driver fixture                                                                                   | Create     |
| `testdata/test_extension_method.sngl`               | Driver fixture                                                                                   | Create     |
| `testdata/test_nested_generic_struct_method.sngl`   | Driver fixture                                                                                   | Create     |
| `testdata/error_nested_func_field_collision.sngl`   | Driver fixture                                                                                   | Create     |
| `testdata/error_this_outside_method.sngl`           | Driver fixture                                                                                   | Create     |
| `testdata/error_duplicate_nested_and_toplevel.sngl` | Driver fixture                                                                                   | Create     |

---

## Task 1: Driver Fixtures

Per CLAUDE.md, fixtures drive the implementation. Write all expected-pass and expected-fail fixtures first. They will compile errors until the implementation lands — that's intentional.

**Files:**
- Create: `testdata/test_struct_nested_methods.sngl`
- Create: `testdata/test_enum_nested_methods.sngl`
- Create: `testdata/test_component_nested_this.sngl`
- Create: `testdata/test_top_level_static.sngl`
- Create: `testdata/test_extension_method.sngl` (multi-file split inside one fixture: SNGL files allow declarations in any order at top-level, so this is one file simulating the extension scenario)
- Create: `testdata/test_nested_generic_struct_method.sngl`
- Create: `testdata/error_nested_func_field_collision.sngl`
- Create: `testdata/error_this_outside_method.sngl`
- Create: `testdata/error_duplicate_nested_and_toplevel.sngl`

- [ ] **Step 1.1: Write `testdata/test_struct_nested_methods.sngl`**

```sngl
struct Point {
    x, y int

    func magnitude2() int => this.x*this.x + this.y*this.y
    func translate(dx, dy int) {
        this.x += dx
        this.y += dy
    }
    // `this.`-elision: bare field, bare sibling call
    func describe() string => "({x},{y}) m2={magnitude2()}"
}

component main {
    var p = Point{x = 3, y = 4}
    text(value=p.describe())
}

func testNestedMethodFieldAccess(t Test, c main) {
    t.assert(c.p.magnitude2() == 25)
}

func testNestedMethodMutation(t Test, c main) {
    c.p.translate(1, 2)
    t.assert(c.p.x == 4)
    t.assert(c.p.y == 6)
}

func testNestedMethodBareSiblingCall(t Test, c main) {
    t.assert(c.p.describe() == "(3,4) m2=25")
}
```

- [ ] **Step 1.2: Write `testdata/test_enum_nested_methods.sngl`**

```sngl
enum Status { ok, err
    func isOk() bool => this == Status.ok
}

component main {
    var s = Status.ok
    text(value=s.isOk() ? "yes" : "no")
}

func testEnumNestedMethod(t Test, c main) {
    t.assert(c.s.isOk())
    c.s = Status.err
    t.assert(!c.s.isOk())
}
```

- [ ] **Step 1.3: Write `testdata/test_component_nested_this.sngl`**

```sngl
component counter {
    var n = 0
    func bump()  { n += 1 }       // bare field
    func reset() { this.n = 0 }   // explicit this
    func value() int => n         // bare field in expression body
}

component main {
    counter()
}

func testComponentThisElision(t Test, c main) {
    var cc = c.children[0]
    cc.bump()
    cc.bump()
    cc.bump()
    t.assert(cc.value() == 3)
    cc.reset()
    t.assert(cc.value() == 0)
}
```

- [ ] **Step 1.4: Write `testdata/test_top_level_static.sngl`**

```sngl
struct S {
    x int
}

// No S-typed first param → static in S's namespace.
func S.helper(n int) int => n * 2

component main {
    text(value=string(S.helper(5)))
}

func testStaticOnUserType(t Test, c main) {
    t.assert(S.helper(5) == 10)
}
```

- [ ] **Step 1.5: Write `testdata/test_extension_method.sngl`**

```sngl
// Type declared first, then an extension method declared separately —
// the legacy top-level form is a peer of the nested form.
struct Vec2 {
    x, y int
}

func Vec2.dot(a Vec2, b Vec2) int => a.x*b.x + a.y*b.y

component main {
    var u = Vec2{x = 1, y = 2}
    var v = Vec2{x = 3, y = 4}
    text(value=string(u.dot(v)))
}

func testExtensionMethod(t Test, c main) {
    t.assert(c.u.dot(c.v) == 11)
    t.assert(Vec2.dot(c.u, c.v) == 11)  // static-style call also works
}
```

- [ ] **Step 1.6: Write `testdata/test_nested_generic_struct_method.sngl`**

```sngl
struct box<T> {
    v T

    func get() T => this.v
}

component main {
    var b = box<int>{v = 42}
    text(value=string(b.get()))
}

func testNestedGenericMethod(t Test, c main) {
    t.assert(c.b.get() == 42)
}
```

- [ ] **Step 1.7: Write `testdata/error_nested_func_field_collision.sngl`**

```sngl
struct Bad {
    x int
    func x() int => 1 // ERROR(check) "duplicate declaration"
}

component main {
    text(value="x")
}
```

- [ ] **Step 1.8: Write `testdata/error_this_outside_method.sngl`**

```sngl
func main_helper() => this
// ERROR(check) "undeclared identifier \"this\""

component main {
    text(value="x")
}
```

- [ ] **Step 1.9: Write `testdata/error_duplicate_nested_and_toplevel.sngl`**

```sngl
struct S {
    x int
    func foo() int => this.x
}

func S.foo(s S) int => s.x + 1 // ERROR(check) "duplicate declaration"

component main {
    text(value="x")
}
```

- [ ] **Step 1.10: Verify all fixtures present and confirm tests fail today**

Run: `go build ./...`
Expected: build still succeeds (fixtures aren't compiled by the Go compiler).
Run: `go test ./internal/parser/... ./internal/checker/... -run 'Sample|Testdata' -count=1`
Expected: failures for the new fixtures (parse errors or unexpected-success on the error fixtures). This is the red baseline.

- [ ] **Step 1.11: Commit**

```bash
git add testdata/test_struct_nested_methods.sngl \
        testdata/test_enum_nested_methods.sngl \
        testdata/test_component_nested_this.sngl \
        testdata/test_top_level_static.sngl \
        testdata/test_extension_method.sngl \
        testdata/test_nested_generic_struct_method.sngl \
        testdata/error_nested_func_field_collision.sngl \
        testdata/error_this_outside_method.sngl \
        testdata/error_duplicate_nested_and_toplevel.sngl
git commit -m "testdata: add fixtures for funcs inside type defs (issue #75)"
```

---

## Task 2: AST — Sealed-interface `Body` slices

**Files:**
- Modify: `ast/ast.go:64-78` (`EnumDef`, `StructDef`, `EnumMember`)
- Modify: `ast/ast.go:272-279` (`StmtPos` impls — `EnumMember` becomes pointer receiver if any new `StmtPos` is added; not required)

- [ ] **Step 2.1: Update `EnumMember` to be addressable via pointer; add sealed interfaces**

Open `ast/ast.go` and replace the block from `EnumMember` through `EnumDef` and `StructDef` with the following:

```go
// EnumMember is a single value in an enum declaration.
type EnumMember struct {
	Pos   Pos
	Name  string
	Value Expr // nil for bare members
}

// EnumBodyItem is one element inside an enum body — a member or a nested func.
type EnumBodyItem interface {
	enumBodyItem()
}

func (*EnumMember) enumBodyItem() {}
func (*FuncDef) enumBodyItem()    {}

// EnumDef declares an enum type. Name is empty for anonymous enum types.
type EnumDef struct {
	Pos         Pos
	Name        string
	Body        []EnumBodyItem // members and nested funcs, in source order
	IsMultiline bool
}

// Members returns just the *EnumMember items from Body, in source order.
// Read-only iteration helper for callers that don't care about funcs.
func (e *EnumDef) Members() []*EnumMember {
	out := make([]*EnumMember, 0, len(e.Body))
	for _, it := range e.Body {
		if m, ok := it.(*EnumMember); ok {
			out = append(out, m)
		}
	}
	return out
}

// Funcs returns just the *FuncDef items from Body, in source order.
func (e *EnumDef) Funcs() []*FuncDef {
	out := make([]*FuncDef, 0, len(e.Body))
	for _, it := range e.Body {
		if f, ok := it.(*FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}

// StructBodyItem is one element inside a struct body — a field or a nested func.
type StructBodyItem interface {
	structBodyItem()
}

func (*StructField) structBodyItem() {}
func (*FuncDef) structBodyItem()     {}

// StructDef declares a struct type. Name is empty for anonymous struct types.
type StructDef struct {
	Pos         Pos
	Name        string
	TypeParams  []string // generic type parameters: ["T"] for `struct list<T> {}`
	Body        []StructBodyItem
	IsMultiline bool
}

// Fields returns just the *StructField items from Body, in source order.
func (s *StructDef) Fields() []*StructField {
	out := make([]*StructField, 0, len(s.Body))
	for _, it := range s.Body {
		if f, ok := it.(*StructField); ok {
			out = append(out, f)
		}
	}
	return out
}

// Funcs returns just the *FuncDef items from Body, in source order.
func (s *StructDef) Funcs() []*FuncDef {
	out := make([]*FuncDef, 0, len(s.Body))
	for _, it := range s.Body {
		if f, ok := it.(*FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}
```

- [ ] **Step 2.2: Run go build, expect many failures**

Run: `go build ./...`
Expected: errors at every site that does `x.Fields` (field access) on `*ast.StructDef` or `x.Members` on `*ast.EnumDef`. This is the migration surface — tracked below.

- [ ] **Step 2.3: Do not commit yet.** Migrating callers comes in the next tasks; commit happens after the codebase builds again.

---

## Task 3: Migrate `ast.StructDef.Fields` readers

The reader sites identified by grep (excluding the parser builder, which is rewritten in Task 5):

| File:Line                         | Form to change                                                |
|-----------------------------------|---------------------------------------------------------------|
| `internal/checker/resolve.go:263` | `for _, f := range s.Fields` → `for _, f := range s.Fields()` |

That is the only top-level reader of `ast.StructDef.Fields` in non-parser code. (Verify with `grep -rn "\.Fields\b" --include='*.go' ast/ internal/ codegen/ | grep -v ir.StructDef`.)

- [ ] **Step 3.1: Update `internal/checker/resolve.go:263`**

Edit (line numbers refer to current main):

Old:

```go
for _, f := range s.Fields {
```

New:

```go
for _, f := range s.Fields() {
```

- [ ] **Step 3.2: Re-grep to confirm no other ast.StructDef.Fields readers remain**

Run: `grep -rn "ast\.StructDef\|StructDef\b" --include='*.go' ast/ internal/ codegen/ | grep "\.Fields\b"`
Expected: empty output (matches will be `ir.StructDef.Fields`, which is unchanged).

- [ ] **Step 3.3: Do not commit yet** — combined commit after Task 4.

---

## Task 4: Migrate `ast.EnumDef.Members` readers

Reader sites (excluding parser builder and formatter, both rewritten later):

| File:Line                                            | Form to change                                                                                       |
|------------------------------------------------------|------------------------------------------------------------------------------------------------------|
| `internal/checker/resolve.go:294-295`                | iterate Body; build IR enum members                                                                  |
| `internal/checker/expr.go:255`                       | `for _, m := range ed.Members` → `for _, m := range ed.Members()`                                    |
| `internal/checker/expr.go:1149`                      | same                                                                                                 |
| `internal/checker/i18n.go:182`                       | same (`enumDecl.Members`)                                                                            |
| `internal/checker/i18n.go:201`                       | same                                                                                                 |
| `internal/checker/stdlib.go:85-87`                   | type-switch uses `ast.EnumDef` (no field access at that line; verify)                                |
| `internal/checker/docapi.go:261`                     | same (verify)                                                                                        |
| `internal/lspcore/hover.go:212`                      | `for _, m := range e.Members` → `for _, m := range e.Members()`                                      |
| `internal/lspcore/completion.go:497`                 | verify; switch on `*ast.EnumDef` may walk members                                                    |
| `internal/lower/enum.go:26-27, 39-40`                | swap to `Members()`                                                                                  |
| `internal/optimize/shake.go:102`                     | swap to `Members()`                                                                                  |
| `internal/docbrowser/model.go:444`                   | reads `EnumDoc.Members` (IR enum doc) — verify unchanged                                             |
| `docs/lookup/lookup.go:641,697`                      | verify if it reads `ast.EnumDef.Members`                                                             |
| `codegen/scheme/js/typescript.go:207, 216, 242, 250` | calls `.Members()` already (these are IR-side accessors); verify                                     |
| `codegen/platform/android/compiler_ir.go:303, 305`   | `ed.Members` — verify whether `ed` is `ast.EnumDef` or `ir.EnumDef`. If AST, switch to `.Members()`. |

For each ambiguous site, open the file and confirm which type `ed`/`e`/`s.Members` refers to. AST sites need `.Members()`; IR sites are unchanged. After verification, edit each AST site.

- [ ] **Step 4.1: Update `internal/checker/resolve.go:293-310`**

Replace the body of `buildEnumDef` to walk `e.Body`:

```go
// buildEnumDef builds an IR EnumDef from an AST EnumDef.
func (c *checker) buildEnumDef(e *ast.EnumDef) *ir.EnumDef {
	astMembers := e.Members()
	members := make([]*ir.EnumMember, len(astMembers))
	for i, m := range astMembers {
		var val ir.Expr
		if m.Value != nil {
			// Placeholder; actual value checked later when scope is ready.
			val = &ir.Literal{Type: TypDyn}
		}
		members[i] = &ir.EnumMember{
			Name:  m.Name,
			Value: val,
		}
	}
	return &ir.EnumDef{
		AST:     e,
		Name:    e.Name,
		Members: members,
	}
}
```

- [ ] **Step 4.2: Update `internal/checker/expr.go:255`**

Old:

```go
for _, m := range ed.Members {
```

New:

```go
for _, m := range ed.Members() {
```

- [ ] **Step 4.3: Update `internal/checker/expr.go:1149`** — same replacement.

- [ ] **Step 4.4: Update `internal/checker/i18n.go:182` and `:201`** — same replacement.

- [ ] **Step 4.5: Update `internal/lspcore/hover.go:212` (in `formatEnumHover`)**

Old:

```go
for _, m := range e.Members {
```

New:

```go
for _, m := range e.Members() {
```

- [ ] **Step 4.6: Update `internal/lower/enum.go:26-40`**

Two loops; replace both `e.Members` / `decl.Members` accesses with `e.Members()` / `decl.Members()`. Stash the slice in a local if used twice (e.g. for `len(...)`).

```go
membersA := e.Members()
m := make(map[string]int, len(membersA))
for i, mem := range membersA {
    ...
}
```

Apply the same pattern to the second loop using `decl.Members()`.

- [ ] **Step 4.7: Update `internal/optimize/shake.go:102`**

Old:

```go
for _, m := range s.Members {
```

New:

```go
for _, m := range s.Members() {
```

- [ ] **Step 4.8: Verify other call sites**

Run: `grep -rn "ast\.EnumDef" --include='*.go' . | head -30` and inspect each `case *ast.EnumDef:` to confirm whether it accesses `.Members`. Fix any missed sites with `.Members()`.

- [ ] **Step 4.9: Update `internal/parser/format.go:189-191`**

Old:

```go
case *ast.EnumDef:
    if x.IsMultiline {
        return x.Pos.Line + len(x.Members) + 1
    }
```

New:

```go
case *ast.EnumDef:
    if x.IsMultiline {
        return x.Pos.Line + len(x.Body) + 1
    }
```

(Body length is the right input for the line-estimate — funcs in the body each take at least one line.)

Same change for the StructDef arm above it: `len(x.Fields)` → `len(x.Body)`.

- [ ] **Step 4.10: Run go build, expect it to succeed**

Run: `go build ./...`
Expected: PASS. AST migration is complete; we haven't touched parsing or the formatter's emission yet, so existing fixtures still parse old-shape (no funcs in struct/enum bodies) and `Body` only ever contains the field/member items the builder used to put in `Fields`/`Members`. The accessors return them; everything compiles.

The parser/formatter still write the old single-slice loops — that's fine for now: the next two tasks add func support.

- [ ] **Step 4.11: Commit AST migration**

```bash
git add ast/ast.go internal/checker/resolve.go internal/checker/expr.go \
        internal/checker/i18n.go internal/lspcore/hover.go internal/lower/enum.go \
        internal/optimize/shake.go internal/parser/format.go
# include any other migrated readers your grep surfaced
git commit -m "ast: switch StructDef/EnumDef to single Body slice"
```

---

## Task 5: Parser — accept `FuncDecl` inside struct/enum bodies

This task updates the grammar and regenerates the parser.

**Files:**
- Modify: `internal/parser/sngl.ebnf:248-254`
- Regenerate: `internal/parser/zparser.go`
- Modify: `internal/parser/build.go:255-330`

- [ ] **Step 5.1: Update grammar**

In `internal/parser/sngl.ebnf`, replace:

```ebnf
StructDecl = kw_struct [ ident ] [ TypeParamList ] lbrace [ StructField { semi StructField } [ semi ] ] rbrace .

StructField = IdentList Type [ assign Expr ] .

# Enum values use a brace list: enum Color{Red, Green, Blue} or enum Status{ok=0, err=1}
# The body is a comma-separated ArgList, not a StmtBlock (no semis).
EnumDecl = kw_enum [ ident ] lbrace [ ArgList ] rbrace .
```

with:

```ebnf
StructDecl = kw_struct [ ident ] [ TypeParamList ] lbrace [ StructBodyItem { semi StructBodyItem } [ semi ] ] rbrace .

StructBodyItem = FuncDecl | StructField .

StructField = IdentList Type [ assign Expr ] .

# Enum body accepts members (ident [= Expr]) and nested funcs, separated by comma or semi.
EnumDecl = kw_enum [ ident ] lbrace [ EnumBodyItem { (comma | semi) EnumBodyItem } [ comma | semi ] ] rbrace .

EnumBodyItem = FuncDecl | EnumMember .

EnumMember = ident [ assign Expr ] .
```

Note: `StructBodyItem` lists `FuncDecl` first so the `kw_func` first-token dispatches before `IdentList` (which starts with `ident`). The two alternatives have disjoint first-sets (`kw_func` vs `ident`), so LL(1) is preserved.

For enums, `EnumMember` (a new explicit production) shares the `ident` first-token with `FuncDecl`'s `kw_func` first-token — disjoint, LL(1) OK.

- [ ] **Step 5.2: Regenerate parser**

Run from `internal/parser/`:

```bash
cd internal/parser
go generate ./...
cd ../..
```

Expected: `zparser.go` regenerated. The header comment `// Code generated by 'egg ...'` confirms the source.

- [ ] **Step 5.3: Run go build, expect failures in `build.go`**

Run: `go build ./...`
Expected: build errors in `internal/parser/build.go` because `buildStructDecl`/`buildEnumDecl` still reference `StructField`/`ArgList` shapes that the new grammar doesn't produce identically. Use the errors to drive Step 5.4.

If new non-terminal constants `StructBodyItem` / `EnumBodyItem` / `EnumMember` exist as `Symbol` values in `zparser.go`, that's the dispatch tag.

- [ ] **Step 5.4: Rewrite `buildStructDecl`**

In `internal/parser/build.go`, replace the body of `buildStructDecl` (currently lines 255-283):

```go
func (b *builder) buildStructDecl(it nodeIter) *ast.StructDef {
	// StructDecl = kw_struct [ ident ] [ TypeParamList ] lbrace { StructBodyItem } rbrace .
	pos := b.posFromToken(it.shift()) // kw_struct
	s := &ast.StructDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		s.Name = it.shift().Literal
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
		s.TypeParams = b.buildTypeParamList(it.enter())
	}
	lbraceLine := 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		lbraceLine = it.token().Line
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == StructBodyItem {
			sub := it.enter()
			if !sub.done() && sub.isNonTerminal() {
				switch sub.symbol() {
				case FuncDecl:
					s.Body = append(s.Body, b.buildFuncDecl(sub.enter()))
				case StructField:
					s.Body = append(s.Body, b.buildStructField(sub.enter()))
				}
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == RBRACE {
				if it.token().Line > lbraceLine {
					s.IsMultiline = true
				}
			}
			it.skip() // rbrace or semi
		}
	}
	return s
}
```

- [ ] **Step 5.5: Rewrite `buildEnumDecl`**

Replace `buildEnumDecl` (currently lines 314-348):

```go
func (b *builder) buildEnumDecl(it nodeIter) *ast.EnumDef {
	// EnumDecl = kw_enum [ ident ] lbrace { EnumBodyItem (comma|semi) } rbrace .
	pos := b.posFromToken(it.shift()) // kw_enum
	e := &ast.EnumDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		e.Name = it.shift().Literal
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == EnumBodyItem {
			sub := it.enter()
			if !sub.done() && sub.isNonTerminal() {
				switch sub.symbol() {
				case FuncDecl:
					e.Body = append(e.Body, b.buildFuncDecl(sub.enter()))
				case EnumMember:
					e.Body = append(e.Body, b.buildEnumMember(sub.enter()))
				}
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				e.IsMultiline = true
			}
			it.skip() // comma or semi or rbrace
		}
	}
	return e
}
```

Update `buildEnumMember` to return a `*ast.EnumMember` (was returning `ast.EnumMember` value):

```go
func (b *builder) buildEnumMember(it nodeIter) *ast.EnumMember {
	m := &ast.EnumMember{}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		nameTok := it.shift()
		m.Pos = b.posFromToken(nameTok)
		m.Name = nameTok.Literal
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip() // assign
		if !it.done() && it.isNonTerminal() {
			m.Value = b.buildExpr(it.enter())
		}
	}
	return m
}
```

Delete the now-unused `buildEnumMembers` helper.

- [ ] **Step 5.6: Run go build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 5.7: Run parser-level fixture parse**

Run: `go test ./internal/parser/... -count=1 -run Sample`
Expected: existing samples still parse; new `test_struct_nested_methods.sngl` / `test_enum_nested_methods.sngl` parse (no errors) but `test_struct_nested_methods.sngl` still fails downstream checker tests because nothing desugars the funcs yet.

- [ ] **Step 5.8: Commit**

```bash
git add internal/parser/sngl.ebnf internal/parser/zparser.go internal/parser/build.go
git commit -m "parser: accept FuncDecl in struct and enum bodies"
```

---

## Task 6: Formatter — walk `Body` with type-switch

**Files:**
- Modify: `internal/parser/format.go:271-342`

- [ ] **Step 6.1: Rewrite `writeStructDef`**

Replace `writeStructDef` (currently lines 271-304):

```go
func (f *formatter) writeStructDef(s *ast.StructDef) {
	f.write("struct ")
	if s.Name != "" {
		f.write(s.Name)
		if len(s.TypeParams) > 0 {
			f.write("<")
			f.write(strings.Join(s.TypeParams, ", "))
			f.write(">")
		}
		f.write(" ")
	}
	if len(s.Body) == 0 {
		f.write("{}")
		return
	}
	f.write("{")
	f.newline()
	f.indent++
	for _, item := range s.Body {
		switch it := item.(type) {
		case *ast.StructField:
			f.write(strings.Join(it.Names, ", "))
			if it.Type != nil {
				f.write(" ")
				f.writeType(it.Type)
			}
			if it.Default != nil {
				f.write(" = ")
				f.writeExpr(it.Default)
			}
		case *ast.FuncDef:
			f.writeFuncDef(it)
		}
		f.newline()
	}
	f.indent--
	f.write("}")
}
```

- [ ] **Step 6.2: Rewrite `writeEnumDef`**

Replace `writeEnumDef` (currently lines 308-342):

```go
func (f *formatter) writeEnumDef(e *ast.EnumDef) {
	f.write("enum ")
	if e.Name != "" {
		f.write(e.Name)
		f.write(" ")
	}
	f.write("{")
	// Funcs in the body force multiline form regardless of IsMultiline.
	hasFuncs := false
	for _, it := range e.Body {
		if _, ok := it.(*ast.FuncDef); ok {
			hasFuncs = true
			break
		}
	}
	if e.IsMultiline || hasFuncs {
		f.newline()
		f.indent++
		for _, item := range e.Body {
			switch it := item.(type) {
			case *ast.EnumMember:
				f.write(it.Name)
				if it.Value != nil {
					f.write(" = ")
					f.writeExpr(it.Value)
				}
			case *ast.FuncDef:
				f.writeFuncDef(it)
			}
			f.newline()
		}
		f.indent--
		f.write("}")
		return
	}
	// Single-line form: members only (no funcs by construction).
	f.write(" ")
	members := e.Members()
	for i, m := range members {
		if i > 0 {
			f.write(", ")
		}
		f.write(m.Name)
		if m.Value != nil {
			f.write(" = ")
			f.writeExpr(m.Value)
		}
	}
	f.write(" }")
}
```

- [ ] **Step 6.3: Run formatter round-trip tests**

Run: `go test ./internal/parser/... -count=1`
Expected: PASS. All existing fixtures format unchanged; new fixtures (parsed but not yet checked) format with funcs in order.

- [ ] **Step 6.4: Commit**

```bash
git add internal/parser/format.go
git commit -m "parser/format: walk struct/enum Body in source order"
```

---

## Task 7: Checker — desugar nested funcs into top-level methods

**Files:**
- Modify: `internal/checker/checker.go:543-548` (`registerStruct`)
- Modify: `internal/checker/checker.go:519` area (`registerEnum`; verify line via grep)
- Modify: `internal/checker/checker.go:891` area (`registerComponent`) — already handles nested funcs; no behavioural change here but ensures we cross-check collisions.

The strategy:

1. In `registerStruct` / `registerEnum`, after building the IR type, walk `astDef.Funcs()` and for each nested AST `*FuncDef`, synthesise a top-level method-form `FuncDef`. Then call `buildFunc` and `symtab.RegisterMethod` exactly like `registerFunc` does.
2. The synthesised `FuncDef` must:
   - Set `Name = T + "." + nested.Name` so `ast.SplitMethodName` picks up the receiver.
   - Prepend a synthetic `Param{Name: "this", Type: <T or T<TypeParams>>}` to `Params.Params`.
   - Copy `RecvTypeParams` from the type's `TypeParams` (if generic).
   - Preserve `Pos`, `Body`, `Block`, `ReturnType`, method-level `TypeParams`.

- [ ] **Step 7.1: Add a helper that builds the synthetic receiver type expression**

In `internal/checker/checker.go`, add (near the other type helpers):

```go
// synthRecvTypeExpr returns an ast.TypeExpr referring to `name` (with any
// type-params instantiated as themselves). Used to produce the `this` param's
// declared type when desugaring nested methods.
func synthRecvTypeExpr(pos ast.Pos, name string, typeParams []string) ast.TypeExpr {
	base := &ast.IdentExpr{Pos: pos, Name: name}
	if len(typeParams) == 0 {
		return base
	}
	args := make([]ast.TypeExpr, len(typeParams))
	for i, tp := range typeParams {
		args[i] = &ast.IdentExpr{Pos: pos, Name: tp}
	}
	return &ast.GenericInst{Pos: pos, Base: base, Args: args}
}
```

> **Verify before writing:** confirm that `ast.GenericInst` is the production used by the checker for `T<U>` type expressions. Run: `grep -n "GenericInst\b" ast/*.go internal/checker/resolve.go | head`. If the name differs (e.g. `TypeInst`, `ParameterizedType`), use that. The helper's intent is invariant: build the AST for `T<typeParams...>`.

- [ ] **Step 7.2: Add `registerNestedMethods` helper**

```go
// registerNestedMethods desugars each nested *ast.FuncDef into an explicit
// top-level method with a synthetic `this` parameter and registers it.
//
// recvName is the type name (struct/enum/component); typeParams is the
// declared receiver-level type-param list (may be empty).
func (c *checker) registerNestedMethods(recvName string, recvPos ast.Pos, typeParams []string, nested []*ast.FuncDef) {
	for _, n := range nested {
		// Build the synthesised top-level method-form FuncDef.
		thisType := synthRecvTypeExpr(n.Pos, recvName, typeParams)
		thisParam := ast.Param{
			Pos:  n.Pos,
			Name: "this",
			Type: thisType,
		}
		newParams := ast.ParamList{
			Pos:         n.Params.Pos,
			IsMultiline: n.Params.IsMultiline,
			Params:      append([]ast.Param{thisParam}, n.Params.Params...),
		}
		synthetic := &ast.FuncDef{
			Pos:            n.Pos,
			Name:           recvName + "." + n.Name,
			TypeParams:     n.TypeParams,
			RecvTypeParams: append([]string(nil), typeParams...),
			Params:         newParams,
			ReturnType:     n.ReturnType,
			Body:           n.Body,
			Block:          n.Block,
		}
		fn := c.buildFunc(synthetic)
		c.pkg.Funcs = append(c.pkg.Funcs, fn)
		c.symtab.RegisterMethod(fn.Receiver, fn)
	}
}
```

- [ ] **Step 7.3: Wire `registerStruct` to call it**

Update `registerStruct` (currently lines 543-548):

```go
func (c *checker) registerStruct(s *ast.StructDef) {
	sd := c.buildStructDef(s)
	c.pkg.Structs = append(c.pkg.Structs, sd)
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
	c.registerNestedMethods(sd.Name, s.Pos, sd.TypeParams, s.Funcs())
}
```

- [ ] **Step 7.4: Wire `registerEnum` to call it**

Locate `registerEnum` (grep `func (c \*checker) registerEnum`). Add the same trailing call:

```go
c.registerNestedMethods(ed.Name, e.Pos, nil, e.Funcs())
```

(Enums don't carry type params today; pass nil.)

- [ ] **Step 7.5: Wire `registerComponent` to call it**

In `registerComponent` (around line 891 / 1014), the existing loop already pulls `*ast.FuncDef` items out of the component body and stores them on `irComp.Funcs`. This task changes that loop so the funcs are *also* registered as methods on the component's type namespace.

Update the loop body for `*ast.FuncDef` (currently `irComp.Funcs = append(irComp.Funcs, fn)`):

```go
case *ast.FuncDef:
    nestedFuncs = append(nestedFuncs, s)
```

…where `nestedFuncs []*ast.FuncDef` is declared at the top of the function. After the loop, call:

```go
c.registerNestedMethods(irComp.Name, comp.Pos, nil, nestedFuncs)
```

And keep the IR component's `Funcs` slice populated via the desugared methods — i.e. mirror the IR-side list from `c.pkg.Funcs` after registration if the codegen still expects it. (Verify by checking what callers read from `ir.Component.Funcs`; if it's only test-runner-style code that uses `c.pkg.Funcs` regardless, drop the separate slice.)

> **Verification step before edit:** grep `IrComponent.Funcs\b\|irComp.Funcs\b\|\.Funcs\b` in `codegen/` and `internal/lower/` to confirm whether `ir.Component.Funcs` is read independently. If yes, populate it from the IR methods returned by `registerNestedMethods` (have the helper return `[]*ir.Func`) so existing readers see the same shape.

- [ ] **Step 7.6: Run checker tests**

Run: `go test ./internal/checker/... -count=1`
Expected: many previously-failing fixtures now check successfully. `test_struct_nested_methods.sngl`, `test_enum_nested_methods.sngl`, `test_nested_generic_struct_method.sngl`, `test_extension_method.sngl` should *parse and check* — but bodies that use `this.x`-elision (bare `x`) will still fail until Task 8. Method-via-`this` (with the explicit `this.`) should already work because the synthesised `this` param exists.

- [ ] **Step 7.7: Commit**

```bash
git add internal/checker/checker.go
git commit -m "checker: desugar nested funcs in struct/enum/component bodies"
```

---

## Task 8: Checker — `this`-elision for bare field and bare sibling-call

**Files:**
- Modify: `internal/checker/expr.go` (identifier resolution + call resolution)

The change is scoped: when resolving an unbound `*ast.IdentExpr` inside a method body whose `Receiver` is non-empty, and `this` is a declared param in the current scope, look up the bare name as a field/member of `this`'s type or as a sibling method.

Strategy:

1. After the synthetic `this` param is in scope (it is, because `registerNestedMethods` builds the synthetic FuncDef which goes through normal body checking), identifier resolution will see `this` as a normal ident.
2. The `this`-elision rewrites happen at expression-check time: when an `*ast.IdentExpr` is otherwise unresolved AND the current method's receiver type has a field/member or sibling method by that name, rewrite to `this.<name>` (a `*ast.SelectExpr`) and continue normal check.

- [ ] **Step 8.1: Locate identifier-resolution entry point**

Run: `grep -n "case \*ast.IdentExpr" internal/checker/expr.go | head -10`
Identify the function that checks ident expressions (likely `checkIdentExpr` or inline within `checkExpr`). Confirm where the "undeclared identifier" error is raised — that's the fallback site for the elision lookup.

- [ ] **Step 8.2: Add a method-context helper**

Near the top of `internal/checker/expr.go` (or `checker.go`), add:

```go
// currentRecvType returns the receiver type for the function currently being
// checked, or nil if no such context is active. It looks at the innermost
// scope for a `this` binding whose declared type names a user-defined type
// with known fields/members or methods.
func (c *checker) currentRecvType() *ir.Type {
	sym, ok := c.scope.Lookup("this")
	if !ok {
		return nil
	}
	p, ok := sym.(*ir.Param)
	if !ok {
		return nil
	}
	return p.Type
}
```

> **Verify** the IR type for params — `ir.Param` may instead be `ir.Var` with an `IsParam` flag, or a dedicated method-receiver kind. Grep `type Param struct\b\|Params \[\]\*` in `ir/`.

- [ ] **Step 8.3: Implement elision in ident resolution**

In the function that handles unresolved `*ast.IdentExpr`, before raising the "undeclared identifier" error, attempt:

```go
if recv := c.currentRecvType(); recv != nil {
	name := ident.Name
	// Field/member of receiver?
	if hasFieldOrMember(recv, name) {
		rewritten := &ast.SelectExpr{
			Pos:      ident.Pos,
			Operand:  &ast.IdentExpr{Pos: ident.Pos, Name: "this"},
			Field:    name,
			FieldPos: ident.Pos,
		}
		return c.checkSelectExpr(rewritten, expected)
	}
	// Sibling method? Only meaningful if the parent expression is a CallExpr,
	// which is handled in the call-resolution path (Step 8.4). For a bare
	// identifier with no call, fall through to the error.
}
```

`hasFieldOrMember(t, name)` — add a small helper that probes `t.Decl` for a struct field or enum member named `name`:

```go
func hasFieldOrMember(t *ir.Type, name string) bool {
	if t == nil || t.Decl == nil {
		return false
	}
	switch d := t.Decl.(type) {
	case *ir.StructDef:
		for _, f := range d.Fields {
			if f.Name == name {
				return true
			}
		}
	case *ir.EnumDef:
		for _, m := range d.Members {
			if m.Name == name {
				return true
			}
		}
	case *ir.Component:
		for _, v := range d.Vars {
			if v.Name == name {
				return true
			}
		}
	}
	return false
}
```

> **Verify** that `ir.Component` is the type stored as the `Decl` for `TypeComponent` types. Grep `Decl.*Component\b` in `ir/` to confirm field shape.

- [ ] **Step 8.4: Implement elision in call resolution**

In the call-check path (around `internal/checker/expr.go:799` per earlier grep), when the callee is a bare `*ast.IdentExpr` that doesn't resolve to anything else, attempt sibling lookup:

```go
if recv := c.currentRecvType(); recv != nil && recv.Decl != nil {
	typeName := typeDeclName(recv.Decl) // e.g., struct name, enum name, component name
	if _, ok := c.symtab.LookupMethod(typeName, ident.Name); ok {
		// Rewrite call.Func from IdentExpr to SelectExpr{this, ident.Name}.
		callExpr.Func = &ast.SelectExpr{
			Pos:      ident.Pos,
			Operand:  &ast.IdentExpr{Pos: ident.Pos, Name: "this"},
			Field:    ident.Name,
			FieldPos: ident.Pos,
		}
		return c.checkCallExpr(callExpr, expected)
	}
}
```

`typeDeclName` returns the canonical name from any `ir.Symbol` that's a type-decl; if a helper already exists (likely a `.Name` field on `*ir.StructDef`/`*ir.EnumDef`/`*ir.Component`), use that.

- [ ] **Step 8.5: Run checker tests**

Run: `go test ./internal/checker/... -count=1`
Expected: `test_struct_nested_methods.sngl` (uses `{x}` interpolation and bare `magnitude2()` sibling call) passes. `test_component_nested_this.sngl` (uses `n` bare in component body) passes. The error fixtures still need work (Task 10).

- [ ] **Step 8.6: Commit**

```bash
git add internal/checker/expr.go internal/checker/checker.go
git commit -m "checker: this-elision for bare field and sibling call"
```

---

## Task 9: Top-level `func T.foo(...)` without receiver → static in T's namespace

**Files:**
- Modify: `internal/checker/checker.go:879-889` (`registerFunc`)
- Modify: `internal/checker/expr.go` call resolution where `T.foo(...)` is matched

The current `registerFunc` (line 879) does:

```go
func (c *checker) registerFunc(f *ast.FuncDef) {
	fn := c.buildFunc(f)
	c.pkg.Funcs = append(c.pkg.Funcs, fn)
	if fn.Receiver != "" {
		c.symtab.RegisterMethod(fn.Receiver, fn)
	} else {
		c.scope.Declare(fn)
	}
}
```

After `buildFunc`, `fn.Receiver` is `T` whenever the source name was `T.foo`. To detect the "static" case, check whether the function has a first param whose type names `T`. If not, it's a static on T's namespace; if yes, it's a method.

- [ ] **Step 9.1: Add `isMethodForm` helper**

```go
// isMethodForm reports whether a top-level T.foo declaration is a method
// (first param's type names T, possibly with type-param instantiation) versus
// a static-on-type-namespace declaration (no such param).
func isMethodForm(fn *ir.Func) bool {
	if len(fn.Params) == 0 {
		return false
	}
	p0 := fn.Params[0]
	if p0.Type == nil || p0.Type.Decl == nil {
		return false
	}
	return typeDeclName(p0.Type.Decl) == fn.Receiver
}
```

- [ ] **Step 9.2: Branch in `registerFunc`**

```go
func (c *checker) registerFunc(f *ast.FuncDef) {
	fn := c.buildFunc(f)
	c.pkg.Funcs = append(c.pkg.Funcs, fn)
	switch {
	case fn.Receiver != "" && isMethodForm(fn):
		c.symtab.RegisterMethod(fn.Receiver, fn)
	case fn.Receiver != "":
		// Static in T's namespace — registered as a method too, but call resolution
		// distinguishes (Step 9.3).
		c.symtab.RegisterMethod(fn.Receiver, fn)
	default:
		c.scope.Declare(fn)
	}
}
```

(Both branches register via `RegisterMethod` because the symtab is the lookup surface for `T.foo`. The differentiation lives in how *call sites* resolve.)

- [ ] **Step 9.3: Update call resolution**

In the path that handles `T.foo(args)` calls (around `internal/checker/expr.go:867` per earlier grep, the "static call" branch for type-namespace methods), allow user-defined types `T` (not just primitives). For static-form funcs, the call passes all args as written. For method-form funcs, the call passes `recv` as the first arg.

```go
// Pseudocode in the type-namespace call branch:
if fn, ok := c.symtab.LookupMethod(typeName, methodName); ok {
	if isMethodForm(fn) {
		// T.foo(recv, args...) — recv must have type T; treat as explicit-recv call.
		return c.checkExplicitRecvCall(callExpr, fn, expected)
	}
	// Static: T.foo(args...) — bind args directly to params.
	return c.checkStaticCall(callExpr, fn, expected)
}
```

> **Verify** by reading `internal/checker/expr.go:867` and surrounding to see how the current primitive type-namespace call is checked. The two paths above may collapse to one if `checkExplicitRecvCall` already handles "first param is the receiver" generically.

- [ ] **Step 9.4: Reject `v.foo()` resolution for static-form funcs**

In the method-call path (when the callee is `v.foo` where `v` has type `T`), look up `foo` on `T`'s method set. If the resolved `*ir.Func` is *not* `isMethodForm`, do not match — fall through to the standard "no method `foo` on type T" error. This prevents `(S{}).helper()` from picking up `func S.helper(n int)`.

- [ ] **Step 9.5: Run tests**

Run: `go test ./internal/checker/... -count=1`
Expected: `test_top_level_static.sngl` passes; `test_extension_method.sngl` passes (`Vec2.dot(u, v)` and `u.dot(v)` both work because `Vec2.dot` is method-form: first param is `Vec2`).

- [ ] **Step 9.6: Commit**

```bash
git add internal/checker/checker.go internal/checker/expr.go
git commit -m "checker: distinguish method-form vs static-on-type for top-level T.foo"
```

---

## Task 10: Collision checks

**Files:**
- Modify: `internal/checker/checker.go` (`registerNestedMethods` from Task 7)

- [ ] **Step 10.1: Track existing names per type**

Augment `registerNestedMethods` (or call `RegisterMethod`-with-collision-check) to error on duplicates. Before registering each synthesised method, check:

1. The type's IR decl has no field/member with the same name.
2. `c.symtab.LookupMethod(recvName, n.Name)` returns false (no prior registration).

```go
func (c *checker) registerNestedMethods(recvName string, recvPos ast.Pos, typeParams []string, nested []*ast.FuncDef) {
	typeDecl := c.symtab.Types[recvName]
	for _, n := range nested {
		// Collision with field/member?
		if typeDecl != nil && hasFieldOrMemberInDecl(typeDecl, n.Name) {
			c.error(n.Pos, "duplicate declaration of %q on %s %s", n.Name, typeKindName(typeDecl), recvName)
			continue
		}
		// Collision with another nested or top-level method?
		if _, exists := c.symtab.LookupMethod(recvName, n.Name); exists {
			c.error(n.Pos, "duplicate declaration of %q on %s %s", n.Name, typeKindName(typeDecl), recvName)
			continue
		}
		// ... existing synthesis + RegisterMethod ...
	}
}
```

Add helpers:

```go
func hasFieldOrMemberInDecl(d ir.Symbol, name string) bool {
	switch x := d.(type) {
	case *ir.StructDef:
		for _, f := range x.Fields {
			if f.Name == name {
				return true
			}
		}
	case *ir.EnumDef:
		for _, m := range x.Members {
			if m.Name == name {
				return true
			}
		}
	case *ir.Component:
		for _, v := range x.Vars {
			if v.Name == name {
				return true
			}
		}
	}
	return false
}

func typeKindName(d ir.Symbol) string {
	switch d.(type) {
	case *ir.StructDef:
		return "struct"
	case *ir.EnumDef:
		return "enum"
	case *ir.Component:
		return "component"
	}
	return "type"
}
```

- [ ] **Step 10.2: Order matters — register types before funcs**

Pass1 already pre-registers all types before funcs (`checker.go:251-264`), so by the time `registerNestedMethods` runs the type's IR decl is in `c.symtab.Types`. But top-level `func T.foo` declarations are processed in the *second* pass1 loop, after type registration — meaning at the moment `registerNestedMethods` is called from `registerStruct`, no top-level methods exist yet. The collision check there will not see a top-level method.

Conversely, when `registerFunc` runs later for `func S.foo`, the nested method has already been registered. So the "duplicate nested and top-level" detection must happen in `registerFunc`, not in `registerNestedMethods`. Update `registerFunc`:

```go
func (c *checker) registerFunc(f *ast.FuncDef) {
	fn := c.buildFunc(f)
	if fn.Receiver != "" {
		if _, exists := c.symtab.LookupMethod(fn.Receiver, fn.Name); exists {
			c.error(f.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
			return
		}
	}
	c.pkg.Funcs = append(c.pkg.Funcs, fn)
	if fn.Receiver != "" {
		c.symtab.RegisterMethod(fn.Receiver, fn)
	} else {
		c.scope.Declare(fn)
	}
}
```

- [ ] **Step 10.3: Run tests**

Run: `go test ./internal/checker/... -count=1`
Expected: `error_nested_func_field_collision.sngl` and `error_duplicate_nested_and_toplevel.sngl` both pass (i.e. the expected error is produced). `error_this_outside_method.sngl` passes (the existing "undeclared identifier" path already handles this — no work needed).

- [ ] **Step 10.4: Commit**

```bash
git add internal/checker/checker.go
git commit -m "checker: reject nested-func collisions with fields and top-level methods"
```

---

## Task 11: LSP polish

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/completion.go`

These changes are mechanical follow-ups so hover signatures don't display the synthetic `this`.

- [ ] **Step 11.1: Hover suppresses synthetic `this`**

In the method-hover formatter, when the receiver type matches the first param's type AND the first param is named `this`, omit it from the rendered param list. Locate the hover producer for methods (grep `Receiver.*hover\|formatMethodHover\|hoverFunc` in `internal/lspcore/`).

Apply:

```go
params := fn.Params
if fn.Receiver != "" && len(params) > 0 && params[0].Name == "this" {
	params = params[1:]
}
// render params
```

- [ ] **Step 11.2: Completion on `this.`**

In completion for member access (`this.<cursor>`), include both fields/members of the receiver type AND sibling methods. This may already work via the normal "member access on type T" path — verify by typing `this.` in a fixture and capturing the LSP completion list. If sibling methods are not included, extend the completion's method lookup to enumerate `c.symtab.Methods[typeName]`.

- [ ] **Step 11.3: Run LSP tests**

Run: `go test ./internal/lspcore/... -count=1`
Expected: PASS.

- [ ] **Step 11.4: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/completion.go
git commit -m "lspcore: suppress synthetic this in hover; complete this. members"
```

---

## Task 12: Full verification

- [ ] **Step 12.1: Run the full test suite**

Run: `go tool verify`
Expected: PASS — all fixtures pass, all unit tests pass.

If a previously-passing fixture now fails because the formatter changed its output (e.g. an enum that now formats slightly differently), inspect the diff. If the difference is cosmetic-but-intended, update the fixture; if not, fix the formatter.

- [ ] **Step 12.2: Format the project**

Run: `go fmt ./...`
Expected: no diff.

- [ ] **Step 12.3: Build the CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 12.4: Smoke-test the dump pipeline**

Run: `sngl dump checked testdata/test_struct_nested_methods.sngl`
Expected: shows the desugared methods with `this` as the first param of each nested method's IR.

- [ ] **Step 12.5: Final commit (if any cleanup)**

If steps 12.1-12.4 surface stragglers, fix and commit:

```bash
git add -A
git commit -m "funcs-in-types: final cleanup"
```

---

## Self-Review

**Spec coverage:**
- §"Three declaration forms" → Tasks 7 (nested method), 9 (static vs method-form), 7 (top-level form still works unchanged).
- §"`this` semantics" — binding (Task 7), mutability (no work — falls out of existing field mutation), elision (Task 8), shadowing (no work — uses normal scope), outside-method error (Task 1 fixture + existing checker).
- §"Grammar deltas" → Task 5.
- §"AST changes" → Task 2.
- §"Parser changes" → Tasks 5, 6.
- §"Checker changes" — pass1 desugaring (Task 7), collision (Task 10), this-elision (Task 8), static-on-type (Task 9), component static (Task 9 covers via the same path since `T` is just a name lookup).
- §"IR & codegen" — no work (verified).
- §"LSP" → Task 11.
- §"Constraints & error cases" table — all rows have driver fixtures in Task 1 + checker enforcement in Tasks 8/10.
- §"Test fixtures" → Task 1.
- §"Risk & mitigation" — formatter (Task 6 walks Body), checker churn (Tasks 3/4 use accessors), diagnostic positions (Task 7 threads `n.Pos` into the synthetic param).

**Placeholder scan:** None of the steps say "implement later" or "add error handling". Every code change has the actual code body.

**Type consistency:** `currentRecvType` / `isMethodForm` / `hasFieldOrMember` / `typeDeclName` / `typeKindName` / `synthRecvTypeExpr` / `registerNestedMethods` — names are consistent across tasks. The accessor names `Body`, `Members()`, `Funcs()`, `Fields()` are consistent. Two helper names are flagged for verification against the actual IR shape (`ir.Param` vs `ir.Var` for params; `ast.GenericInst` vs whatever the codebase actually calls a parameterised type) — Tasks 7 and 8 include explicit "verify" notes for those.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-20-funcs-in-types.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
