# Reactive Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reactive context primitive to SNGL (top-level `context #name(default)` decl, name-as-visual-node provider, bare-name consumer read) with hidden-prop threading via a new `NoContext` lowering pass; migrate i18n off process-global locale; replace `t.setLocale` with generic `t.setContext`.

**Architecture:** `context #name(default)` and `name(value) { children }` are parsed as ordinary `VisualNode`s (no parser changes needed for surface). The checker recognizes them by name, registers a new `ir.Context` decl in `Package`, and emits `ir.ContextProvider` nodes inside windows/components. A new lowering pass `NoContext` (between `NoLambda` and `NoReactivity`) computes per-context transitive reachability across the call graph, adds synthesized `__ctx_<name>` params to reachable components, rewrites provider visual nodes into hidden-prop assignments on enclosed component-calls, and synthesizes a root-default binding at each window root. Post-pass IR contains zero context-specific nodes — existing reactivity/codegen treats the synthesized props as ordinary reactive props. All platforms set `NoContext: true` for v1.

**Tech Stack:** Go (compiler, runtime); SNGL fixtures with `// ERROR(...)` / `// FOLD(...)` directives; existing `passReactivity` / `passInlineComponents` infrastructure.

**Spec:** [`docs/superpowers/specs/2026-05-16-reactive-context-design.md`](../specs/2026-05-16-reactive-context-design.md)

---

## File Structure

### New files

- `ir/context.go` — `ir.Context` decl type, `ir.ContextProvider` visual-node IR, `ir.ContextRead` expr.
- `internal/lower/context.go` — `passNoContext` lowering pass: reachability, hidden-param synthesis, provider rewrite, root-default injection.
- `internal/lower/context_test.go` — unit tests for the lowering pass (FOLD-style assertions via dump).
- `testdata/context_*.sngl` — fixtures driving parser/checker/lowering behavior.
- `testdata/error_context_*.sngl` — fixtures for negative checker cases.

### Modified files

- `ast/ast.go` — `VisualNode` already exists; no AST shape change. Add doc comment to `Document` clarifying that `context #...` lives in `Visuals`.
- `internal/parser/build.go` — verify `context #ident(args)` parses as a `VisualNode` with ID and one arg; add lexer regression test only if it doesn't already.
- `internal/parser/format.go` — verify round-trip (already handled by generic visual-node formatter; add fixture, no code change unless gap found).
- `internal/checker/checker.go` — extend root-level visual-node switch with a `"context"` case; build `ir.Context` and register in `Package.Contexts`.
- `internal/checker/expr.go` — recognize context-name references in:
  - visual-node position inside windows/components → emit `ir.ContextProvider`;
  - expression position → emit `ir.ContextRead` typed as `T`.
  Reject disallowed positions (assign target, func arg, struct field, list element, local var init).
- `internal/checker/scope.go` — `Scope.Declare` handles `*ir.Context` like other symbols.
- `internal/checker/stdlib.go` — recognize that stdlib i18n functions are implicit readers of the `locale` context (mark on function signatures or via a separate predicate the lowering pass consults).
- `ir/ir.go` — add `Contexts []*Context` field to `Package`.
- `ir/convert.go` — IR→AST round-trip for `Context`, `ContextProvider`, `ContextRead`.
- `internal/lower/caps.go` — add `NoContext bool` flag; add to `Merge` and `String`.
- `internal/lower/lower.go` — register `passNoContext` in `passes` slice. Order: after `passLambda`, before `passReactivity`.
- `codegen/platform/{html,fyne,gtk4,bubbletea,android,none,gtk4}/platform.go` (whichever file declares `Capabilities()`) — set `NoContext: true`.
- `codegen/lang/{golang,kotlin}/testlower.go` — implement `t.setContext` lowering; remove `t.setLocale` stubs.
- `codegen/platform/none/testrunner/runner.go` — interpreter support for context provider scoping + `t.setContext`.
- `lib/i18n.sngl` — add `context #locale(i18n.defaultLocale())`; declare `i18n.defaultLocale()` stdlib func; mark existing i18n.* funcs (or annotate) as locale-context readers.
- `pkg/go/i18n/i18n.go` — drop process-global state; per-call translator with locale-keyed cache.
- `pkg/js/i18n/*.js` — accept locale per call.
- `pkg/kotlin/i18n/*.kt` — drop `I18n.init` injection; per-call locale.
- `codegen/lang/kotlin/*` — remove `MainActivity.onCreate` `I18n.init` injection.
- `internal/lspcore/*` — hover/go-to-def/completion for context names.
- `cmd/sngl/dump.go` — surface contexts in `dump parsed/checked/optimized`.
- Various `testdata/*.sngl` — replace `t.setLocale(...)` with `t.setContext(locale, ...)`.

---

## Phase 1 — Surface: AST recognition + parser/format fixtures

Goal: `context #name(default)` at top level and `name(value) { children }` inside windows parse, format, and round-trip cleanly. No checker logic yet.

### Task 1: Parser round-trip fixture for top-level context decl

**Files:**
- Create: `testdata/context_decl_basic.sngl`
- Test: `internal/parser/format_test.go` (existing — picks up new fixture)

- [ ] **Step 1: Write fixture**

Create `testdata/context_decl_basic.sngl`:

```sngl
context #theme("light")

window #home(title="Home", href="/") {
    text(value="hello")
}
```

- [ ] **Step 2: Run format round-trip**

Run: `go test ./internal/parser/ -run TestFormatRoundTrip -v`
Expected: PASS for the new fixture (parser already handles arbitrary visual-node syntax).

If the existing round-trip test doesn't auto-pick up new fixtures, add the fixture explicitly to its fixture list and re-run.

- [ ] **Step 3: Commit**

```bash
git add testdata/context_decl_basic.sngl
git commit -m "test: context decl parse + format round-trip fixture"
```

### Task 2: Parser fixture for inferred-type defaults

**Files:**
- Create: `testdata/context_decl_inferred_type.sngl`

- [ ] **Step 1: Write fixture**

```sngl
context #s("hello")
context #n(42)
context #b(true)

struct Theme { primary string, density int }
context #theme(Theme{primary: "blue", density: 1})

enum Mode { light, dark }
context #mode(Mode.light)

window #home(title="Home", href="/") {
    text(value="x")
}
```

- [ ] **Step 2: Round-trip**

Run: `go test ./internal/parser/ -run TestFormatRoundTrip -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add testdata/context_decl_inferred_type.sngl
git commit -m "test: context decl with various inferred default types"
```

---

## Phase 2 — IR shape

### Task 3: Define `ir.Context`, `ir.ContextProvider`, `ir.ContextRead`

**Files:**
- Create: `ir/context.go`
- Modify: `ir/ir.go` (add `Contexts []*Context` to `Package`)

- [ ] **Step 1: Write the failing test**

Create `ir/context_test.go`:

```go
package ir

import "testing"

func TestContextDeclSymName(t *testing.T) {
    c := &Context{Name: "theme"}
    if got := c.SymName(); got != "theme" {
        t.Errorf("SymName = %q, want %q", got, "theme")
    }
}

func TestContextReadIsExpr(t *testing.T) {
    var _ Expr = (*ContextRead)(nil)
}

func TestContextProviderIsStmt(t *testing.T) {
    var _ Stmt = (*ContextProvider)(nil)
}

func TestPackageContexts(t *testing.T) {
    p := &Package{}
    p.Contexts = append(p.Contexts, &Context{Name: "theme"})
    if len(p.Contexts) != 1 {
        t.Fatalf("len = %d", len(p.Contexts))
    }
}
```

- [ ] **Step 2: Run, see fail**

Run: `go test ./ir/ -run TestContext -v`
Expected: compile error — `Context`, `ContextRead`, `ContextProvider`, `Package.Contexts` undefined.

- [ ] **Step 3: Implement**

Create `ir/context.go`:

```go
package ir

import "git.duckfam.us/jonathan/sngl/ast"

// Context represents a top-level `context #name(default)` declaration.
// Default is the checked default-value expression; Typ is its inferred
// type. The lowering pass NoContext threads its value through every
// reachable component as a hidden parameter.
type Context struct {
    AST     *ast.VisualNode
    Name    string
    Typ     *Type
    Default Expr
}

func (c *Context) SymName() string { return c.Name }
func (c *Context) SymType() *Type  { return c.Typ }

// ContextProvider is the IR form of `name(value) { children }` inside a
// window or component body. The lowering pass NoContext rewrites it into
// hidden-prop assignments on every component call reachable inside Body.
type ContextProvider struct {
    AST   *ast.VisualNode
    Ref   *Context
    Value Expr
    Body  []Stmt
}

func (p *ContextProvider) stmtNode() {}
func (p *ContextProvider) StmtPos() *ast.Pos {
    if p.AST != nil {
        return &p.AST.Pos
    }
    return nil
}

// ContextRead is the IR form of a bare reference to a context name in
// expression position. Lowering rewrites these to reads of the
// synthesized hidden parameter `__ctx_<Name>`.
type ContextRead struct {
    AST *ast.IdentExpr
    Ref *Context
    Typ *Type
}

func (r *ContextRead) exprNode()    {}
func (r *ContextRead) Type() *Type  { return r.Typ }
func (r *ContextRead) ExprPos() *ast.Pos {
    if r.AST != nil {
        return &r.AST.Pos
    }
    return nil
}
```

Modify `ir/ir.go` `Package` struct — add field after `Outputs`:

```go
    Outputs    []*Output
    Contexts   []*Context
    Symbols    *SymbolTable
```

- [ ] **Step 4: Run tests, see pass**

Run: `go test ./ir/ -run TestContext -v && go build ./...`
Expected: PASS, builds clean.

- [ ] **Step 5: Commit**

```bash
git add ir/context.go ir/context_test.go ir/ir.go
git commit -m "ir: add Context, ContextProvider, ContextRead types"
```

### Task 4: `ir.Convert` round-trip for context nodes

**Files:**
- Modify: `ir/convert.go`

- [ ] **Step 1: Inspect existing patterns**

Read `ir/convert.go` to find how `Window` and `Timer` are converted to `ast.VisualNode`. Match that pattern.

- [ ] **Step 2: Write the failing test**

Add to `ir/context_test.go`:

```go
func TestConvertContextDecl(t *testing.T) {
    p := &Package{Contexts: []*Context{
        {Name: "theme", Typ: &Type{Kind: TypeString}, Default: &StringLit{Value: "light"}},
    }}
    doc := Convert(p)
    found := false
    for _, v := range doc.Visuals {
        if vn, ok := v.(*ast.VisualNode); ok {
            if ident, ok := vn.Target.(*ast.IdentExpr); ok && ident.Name == "context" && vn.ID == "theme" {
                found = true
                break
            }
        }
    }
    if !found {
        t.Fatalf("Convert did not emit context decl as visual node")
    }
}
```

- [ ] **Step 3: Run, see fail**

Run: `go test ./ir/ -run TestConvertContextDecl -v`
Expected: FAIL.

- [ ] **Step 4: Implement in `ir/convert.go`**

Locate the function that walks `Package` into `*ast.Document` (likely `Convert(*Package) *ast.Document`). After windows/timers/outputs are emitted, walk `Contexts` and emit one `*ast.VisualNode` per context:

```go
for _, c := range p.Contexts {
    vn := &ast.VisualNode{
        Target: &ast.IdentExpr{Name: "context"},
        ID:     c.Name,
        Args:   []*ast.NamedArg{{Value: convertExpr(c.Default)}},
    }
    doc.Visuals = append(doc.Visuals, vn)
}
```

Also handle `ContextProvider` (inside Body statements) and `ContextRead` (inside Expr conversions). Provider:

```go
case *ContextProvider:
    return &ast.VisualNode{
        Target: &ast.IdentExpr{Name: s.Ref.Name},
        Args:   []*ast.NamedArg{{Value: convertExpr(s.Value)}},
        Body:   convertStmts(s.Body),
    }
```

Read:

```go
case *ContextRead:
    return &ast.IdentExpr{Name: e.Ref.Name}
```

- [ ] **Step 5: Run, see pass**

Run: `go test ./ir/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ir/convert.go ir/context_test.go
git commit -m "ir: convert Context/Provider/Read back to AST"
```

---

## Phase 3 — Checker

### Task 5: Recognize top-level `context #name(default)`

**Files:**
- Modify: `internal/checker/checker.go` (root-level visual-node switch)
- Test: `internal/checker/checker_test.go` (extend existing)

- [ ] **Step 1: Write the failing test**

Create `testdata/context_basic.sngl`:

```sngl
context #theme("light")

window #home(title="Home", href="/") {
    text(value="hello")
}
```

Add a checker test (in `internal/checker/checker_test.go` or via the existing fixture-driven runner) that loads `context_basic.sngl` and asserts `pkg.Contexts` has one entry named `theme` with type `string`.

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/checker/ -run TestContextBasic -v`
Expected: FAIL — unhandled root-level `context` node.

- [ ] **Step 3: Implement in `internal/checker/checker.go`**

In the root-level visual-node switch (the function containing `case "window":`), add:

```go
case "context":
    ctx := c.buildContext(vn)
    c.pkg.Contexts = append(c.pkg.Contexts, ctx)
    if ctx.Name != "" {
        c.scope.Declare(ctx)
    }
```

Add `buildContext` (same file or a new `internal/checker/context.go`):

```go
func (c *checker) buildContext(vn *ast.VisualNode) *ir.Context {
    ctx := &ir.Context{AST: vn, Name: vn.ID}
    if vn.ID == "" {
        c.error(vn.Pos, "context decl requires #identifier")
        return ctx
    }
    if len(vn.Args) != 1 || vn.Args[0].Name != "" {
        c.error(vn.Pos, "context decl takes exactly one positional default value")
        return ctx
    }
    if len(vn.Body) != 0 {
        c.error(vn.Pos, "context decl cannot have a body")
        return ctx
    }
    def := c.checkExpr(vn.Args[0].Value, nil)
    if !isConstantExpr(def) {
        c.error(vn.Args[0].Value.Pos(), "context default must be a constant expression")
    }
    ctx.Default = def
    ctx.Typ = def.Type()
    return ctx
}
```

Add `isConstantExpr` if not present — true for literals, pure stdlib func calls, struct/enum literals with constant fields. Reuse any existing const-checking helper (look for `evalConst`, `constEval`, similar).

- [ ] **Step 4: Run, see pass**

Run: `go test ./internal/checker/ -run TestContextBasic -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add testdata/context_basic.sngl internal/checker/context.go internal/checker/checker.go internal/checker/checker_test.go
git commit -m "checker: recognize top-level context #name(default)"
```

### Task 6: Reject `context` decl outside top level

**Files:**
- Create: `testdata/error_context_not_top_level.sngl`

- [ ] **Step 1: Write fixture**

```sngl
window #home(title="Home", href="/") {
    context #theme("light") // ERROR(check) "context decl only permitted at file top level"
    text(value="hi")
}
```

- [ ] **Step 2: Run fixture-driven checker tests**

Run: `go test ./internal/checker/ -v`
Expected: existing root-level switch already errors with `"unexpected root-level visual node"` — but inside a window, the visual-node check for "context" needs to also report. Add to the inside-window visual-node handler (find where `case "timer"` is handled inside windows) a rejection for `"context"`:

```go
case "context":
    c.error(vn.Pos, "context decl only permitted at file top level")
```

Adjust the `ERROR(check)` directive message to match exactly.

- [ ] **Step 3: Commit**

```bash
git add testdata/error_context_not_top_level.sngl internal/checker/expr.go
git commit -m "checker: reject context decl inside windows/components"
```

### Task 7: Reject non-constant default

**Files:**
- Create: `testdata/error_context_non_const_default.sngl`

- [ ] **Step 1: Write fixture**

```sngl
var x = 5

context #c(x) // ERROR(check) "context default must be a constant expression"

window #home(title="Home", href="/") {
    text(value="x")
}
```

- [ ] **Step 2: Run checker tests**

Run: `go test ./internal/checker/ -v`
Expected: PASS (logic already in `buildContext` from Task 5).

- [ ] **Step 3: Commit**

```bash
git add testdata/error_context_non_const_default.sngl
git commit -m "test: context default must be constant"
```

### Task 8: Provider node — recognize context name in visual-node position

**Files:**
- Modify: `internal/checker/expr.go` (visual-node resolution inside windows/components)

- [ ] **Step 1: Write fixture**

Create `testdata/context_provider_basic.sngl`:

```sngl
context #theme("light")

component Inner {
    text(value="static")
}

window #home(title="Home", href="/") {
    theme("dark") {
        Inner()
    }
}
```

Add a checker assertion: after type-check, the window's body contains a single `*ir.ContextProvider` whose `Ref.Name == "theme"`, `Value` is the string "dark", and `Body` contains one component call to `Inner`.

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/checker/ -run TestContextProviderBasic -v`
Expected: FAIL — currently the checker would either error or treat as unknown component.

- [ ] **Step 3: Implement**

In `internal/checker/expr.go`, in the function that resolves visual-node targets inside a window/component body: when the target identifier resolves (via `c.scope.Lookup`) to an `*ir.Context`, emit `*ir.ContextProvider` instead of treating as a component call:

```go
if ctx, ok := sym.(*ir.Context); ok {
    if len(vn.Args) != 1 || vn.Args[0].Name != "" {
        c.error(vn.Pos, "context provider takes exactly one positional value")
        return nil
    }
    val := c.checkExpr(vn.Args[0].Value, ctx.Typ)
    body := c.checkBody(vn.Body)
    return &ir.ContextProvider{AST: vn, Ref: ctx, Value: val, Body: body}
}
```

Value expression's type must be assignable to `ctx.Typ` — reuse existing coerce helper.

- [ ] **Step 4: Run, see pass**

Run: `go test ./internal/checker/ -run TestContextProvider -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add testdata/context_provider_basic.sngl internal/checker/expr.go
git commit -m "checker: emit ContextProvider for name-as-visual-node"
```

### Task 9: Consumer read — bare ident resolves to ContextRead in expression position

**Files:**
- Modify: `internal/checker/expr.go` (ident resolution in expression position)

- [ ] **Step 1: Write fixture**

Create `testdata/context_consumer_basic.sngl`:

```sngl
context #theme("light")

component Toolbar {
    text(value=theme)
}

window #home(title="Home", href="/") {
    theme("dark") {
        Toolbar()
    }
}
```

Add a checker assertion: `Toolbar.Body` contains a `text(value=…)` whose value is `*ir.ContextRead{Ref.Name: "theme", Typ.Kind: TypeString}`.

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/checker/ -run TestContextConsumer -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In ident-expression checking: when an identifier resolves to `*ir.Context`, emit `*ir.ContextRead`:

```go
if ctx, ok := sym.(*ir.Context); ok {
    return &ir.ContextRead{AST: e, Ref: ctx, Typ: ctx.Typ}
}
```

- [ ] **Step 4: Run, see pass**

Run: `go test ./internal/checker/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add testdata/context_consumer_basic.sngl internal/checker/expr.go
git commit -m "checker: emit ContextRead for ident referring to context"
```

### Task 10: Reject context in disallowed positions

**Files:**
- Create:
  - `testdata/error_context_as_func_arg.sngl`
  - `testdata/error_context_assign.sngl`
  - `testdata/error_context_as_struct_field.sngl`
  - `testdata/error_context_as_list_elem.sngl`
- Modify: `internal/checker/expr.go` — add position-aware rejection

- [ ] **Step 1: Write fixtures**

`error_context_as_func_arg.sngl`:

```sngl
context #theme("light")

func show(t string) => t

window #home(title="Home", href="/") {
    text(value=show(theme)) // ERROR(check) "context value cannot be passed as function argument"
}
```

`error_context_assign.sngl`:

```sngl
context #theme("light")

window #home(title="Home", href="/") {
    text(value="x", @click {
        theme = "dark" // ERROR(check) "context cannot be assigned"
    })
}
```

`error_context_as_struct_field.sngl`:

```sngl
context #theme("light")

struct Holder { t string }

window #home(title="Home", href="/") {
    text(value=Holder{t: theme}.t) // permitted: ContextRead in field init reads as T (string), that's fine
}
```

Actually the *read* is permitted — what's rejected is using the context **name itself** as a non-read reference. Drop this fixture; `theme` here is a normal read returning T.

Replace with `error_context_in_var_init.sngl`:

```sngl
context #theme("light")

window #home(title="Home", href="/") {
    var t = theme // ERROR(check) "context cannot be captured into a local var"
    text(value=t)
}
```

Rationale: capturing into a local var would freeze the value at init time and miss reactive updates. Force users to read at use site.

- [ ] **Step 2: Implement position-aware rejection**

Add a checker concept: an "expression context" tag passed down. Reject `ContextRead` emission when the parent context is:
- assign target (LHS of `=`)
- argument to a function call (not a component call's prop)
- right-hand side of a top-level `var =` or `const =` (where the var is component- or window-scoped local state)

For the assign case, the LHS check is at the assign statement level: if `e.LHS` resolves to an `*ir.Context`, error.

For func-arg case, when checking call args (`CallExpr`), inspect each arg's checked Expr; if any is `*ir.ContextRead` and the call target is not a component invocation (i.e. it's a `Func`), error. This is the most defensible boundary — component calls *do* pass values through, but Go-style funcs would capture stale.

Actually simpler & more orthogonal: any read at all is fine syntactically. We only need to reject **non-read positions**:
1. Assign LHS where target is a Context decl.
2. `var x = theme` / `const x = theme` where RHS is a `*ir.ContextRead` *and* the declaration site is local/component-scoped state (would lose reactivity).

Drop the func-arg restriction — it's enforced by typing: `ContextRead` has type T, so it just passes T to the function. That's fine; the function call evaluates eagerly each render. Remove the func-arg fixture.

Update fixtures: keep `error_context_assign.sngl` and `error_context_in_var_init.sngl`.

- [ ] **Step 3: Run**

Run: `go test ./internal/checker/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add testdata/error_context_assign.sngl testdata/error_context_in_var_init.sngl internal/checker/expr.go
git commit -m "checker: reject context assign + var-init capture"
```

### Task 11: Nested provider shadowing — checker test

**Files:**
- Create: `testdata/context_nested_shadowing.sngl`

- [ ] **Step 1: Write fixture**

```sngl
context #theme("outer")

component Show {
    text(value=theme)
}

window #home(title="Home", href="/") {
    theme("outer-val") {
        theme("inner-val") {
            Show() // expect: "inner-val"
        }
        Show()     // expect: "outer-val"
    }
    Show()         // expect: "outer" (default)
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/checker/ -v`
Expected: PASS (this fixture is structural; lowering tests will assert the shadowing semantics).

- [ ] **Step 3: Commit**

```bash
git add testdata/context_nested_shadowing.sngl
git commit -m "test: nested provider shadowing fixture"
```

---

## Phase 4 — Caps + lowering pass scaffold

### Task 12: Add `NoContext` to `Caps`

**Files:**
- Modify: `internal/lower/caps.go`
- Modify: `internal/lower/caps_test.go`

- [ ] **Step 1: Failing test**

Add to `internal/lower/caps_test.go`:

```go
func TestCapsNoContextString(t *testing.T) {
    c := Caps{NoContext: true}
    if !strings.Contains(c.String(), "NoContext") {
        t.Errorf("Caps{NoContext:true}.String() = %q, missing NoContext", c.String())
    }
}
```

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/lower/ -run TestCapsNoContext -v`
Expected: FAIL — `Caps.NoContext` undefined.

- [ ] **Step 3: Edit `internal/lower/caps.go`**

Add field, Merge, String entry. Place `NoContext` between `NoLambda` and `NoReactivity` in `String()` ordering (matches pass-execution order).

```go
type Caps struct {
    // ... existing fields ...
    NoContext bool // context decls → hidden-prop threading via NoContext pass
    // ... rest ...
}

// in Merge:
NoContext: c.NoContext || other.NoContext,

// in String, after NoLambda branch:
if c.NoContext {
    parts = append(parts, "NoContext")
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/lower/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/caps.go internal/lower/caps_test.go
git commit -m "lower: add NoContext cap"
```

### Task 13: Register `passNoContext` skeleton

**Files:**
- Create: `internal/lower/context.go`
- Modify: `internal/lower/lower.go` (insert in passes slice)

- [ ] **Step 1: Write the failing test**

Create `internal/lower/context_test.go`:

```go
package lower

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/ir"
)

func TestNoContextNoop(t *testing.T) {
    pkg := &ir.Package{}
    if err := passNoContext.apply(pkg, Caps{NoContext: true}, Options{}); err != nil {
        t.Fatalf("apply: %v", err)
    }
}

func TestPassNoContextRegistered(t *testing.T) {
    for _, name := range PassNames() {
        if name == "NoContext" {
            return
        }
    }
    t.Fatalf("NoContext not in PassNames(): %v", PassNames())
}
```

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/lower/ -run TestNoContext -v`
Expected: FAIL — `passNoContext` undefined.

- [ ] **Step 3: Implement skeleton**

Create `internal/lower/context.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passNoContext = pass{
    name:    "NoContext",
    enabled: func(c Caps) bool { return c.NoContext },
    apply:   applyNoContext,
}

// applyNoContext rewrites all context decls + providers + reads into
// hidden-prop threading. See spec §Pipeline → Lowering.
func applyNoContext(pkg *ir.Package, _ Caps, _ Options) error {
    if len(pkg.Contexts) == 0 {
        return nil
    }
    // 1. Reachability per context.
    reach := computeReachability(pkg)

    // 2. Synthesize __ctx_<name> param on each Reach component.
    addHiddenParams(pkg, reach)

    // 3. Rewrite ContextReads to reads of the hidden param.
    rewriteReads(pkg, reach)

    // 4. Lower ContextProviders to per-component-call hidden-prop assignments.
    lowerProviders(pkg, reach)

    // 5. Inject root defaults at each window.
    injectRootDefaults(pkg)

    // 6. Clear pkg.Contexts (no longer needed; round-trip should not re-emit decls).
    pkg.Contexts = nil
    return nil
}

func computeReachability(pkg *ir.Package) map[*ir.Context]map[*ir.Component]bool {
    panic("TODO Task 14")
}
func addHiddenParams(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    panic("TODO Task 15")
}
func rewriteReads(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    panic("TODO Task 16")
}
func lowerProviders(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    panic("TODO Task 17")
}
func injectRootDefaults(pkg *ir.Package) {
    panic("TODO Task 18")
}
```

Modify `internal/lower/lower.go` passes slice — insert after `passLambda`, before `passInlinePure`/`passReactivity`:

```go
var passes = []pass{
    passPlatformExtensionBody,
    passUnit,
    passEnum,
    passTernary,
    passAsyncReactive,
    passComputed,
    passLambda,
    passNoListLambdas,
    passToggle,
    passNoContext,        // NEW — must run before InlinePure (provider rewrite needs un-inlined component shape) and Reactivity (hidden params must be visible as reactive deps).
    passInlinePure,
    passNoInlineComponents,
    passReactivity,
    passTimer,
    passDeclarative,
    passNoRef,
}
```

- [ ] **Step 4: Run, see register pass but apply panic**

Run: `go test ./internal/lower/ -run TestPassNoContextRegistered -v`
Expected: PASS.

Run: `go test ./internal/lower/ -run TestNoContextNoop -v`
Expected: PASS (no contexts → early return).

- [ ] **Step 5: Commit**

```bash
git add internal/lower/context.go internal/lower/context_test.go internal/lower/lower.go
git commit -m "lower: register NoContext pass skeleton"
```

### Task 14: Implement `computeReachability`

**Files:**
- Modify: `internal/lower/context.go`
- Test: `internal/lower/context_test.go`

Reachability rule (restated from spec): a component `C` is in `Reach(ctx)` iff its body transitively (a) contains an `ir.ContextRead{Ref: ctx}`, or (b) contains a call to a component `D ∈ Reach(ctx)` where the call site is not enclosed by an `ir.ContextProvider{Ref: ctx}` shadowing it.

Algorithm: fixpoint over the call graph. Per-context boolean per-component. Walk component bodies; if any read/call places it in reach, mark; iterate until stable.

- [ ] **Step 1: Failing test**

Add to `internal/lower/context_test.go`:

```go
func TestReachabilityDirectRead(t *testing.T) {
    // Build IR with a context and a component that reads it. Assert
    // the component is in Reach(ctx).
    pkg, ctx, comp := mustParseAndCheck(t, `
context #theme("light")

component A {
    text(value=theme)
}

window #home(title="x", href="/") {
    A()
}
`)
    reach := computeReachability(pkg)
    if !reach[ctx][comp("A")] {
        t.Fatalf("A not marked as reaching theme")
    }
}

func TestReachabilityTransitive(t *testing.T) {
    pkg, ctx, comp := mustParseAndCheck(t, `
context #theme("light")

component Leaf { text(value=theme) }
component Mid  { Leaf() }
component Top  { Mid() }

window #home(title="x", href="/") { Top() }
`)
    reach := computeReachability(pkg)
    for _, n := range []string{"Leaf", "Mid", "Top"} {
        if !reach[ctx][comp(n)] {
            t.Errorf("%s not in Reach(theme)", n)
        }
    }
}

func TestReachabilityShadowingBlocks(t *testing.T) {
    pkg, ctx, comp := mustParseAndCheck(t, `
context #theme("light")

component Leaf { text(value=theme) }
component Mid  {
    theme("override") { Leaf() }
}
component Top  { Mid() }

window #home(title="x", href="/") { Top() }
`)
    reach := computeReachability(pkg)
    if !reach[ctx][comp("Leaf")] {
        t.Errorf("Leaf must be in Reach (it reads theme)")
    }
    // Mid contains a shadowed call to Leaf, so Mid does NOT need to forward theme.
    if reach[ctx][comp("Mid")] {
        t.Errorf("Mid should not be in Reach — its Leaf call is shadowed")
    }
    if reach[ctx][comp("Top")] {
        t.Errorf("Top should not be in Reach — Mid does not need it")
    }
}
```

Helper `mustParseAndCheck` (add to a `helpers_test.go` in `internal/lower`): runs parser + checker on a string source, returns `*ir.Package`, the named context, and a closure `comp(name) *ir.Component`.

- [ ] **Step 2: Run, see fail**

Run: `go test ./internal/lower/ -run TestReachability -v`
Expected: FAIL — function panics with "TODO".

- [ ] **Step 3: Implement**

```go
func computeReachability(pkg *ir.Package) map[*ir.Context]map[*ir.Component]bool {
    reach := make(map[*ir.Context]map[*ir.Component]bool, len(pkg.Contexts))
    for _, ctx := range pkg.Contexts {
        reach[ctx] = make(map[*ir.Component]bool)
    }
    // Step 1: direct readers — components whose body contains ContextRead.
    for _, comp := range pkg.Components {
        directly := componentDirectContextReads(comp)
        for ctx := range directly {
            reach[ctx][comp] = true
        }
    }
    // Step 2: fixpoint propagation through call graph, respecting shadowing.
    changed := true
    for changed {
        changed = false
        for _, caller := range pkg.Components {
            calls := componentComponentCalls(caller) // []componentCall{Callee, ShadowedCtxs}
            for _, call := range calls {
                for ctx := range reach {
                    if call.shadowed[ctx] {
                        continue
                    }
                    if reach[ctx][call.callee] && !reach[ctx][caller] {
                        reach[ctx][caller] = true
                        changed = true
                    }
                }
            }
        }
    }
    return reach
}

type componentCall struct {
    callee   *ir.Component
    shadowed map[*ir.Context]bool
}

// componentDirectContextReads walks comp's body for ContextRead.
func componentDirectContextReads(comp *ir.Component) map[*ir.Context]bool {
    out := map[*ir.Context]bool{}
    ir.WalkStmts(comp.Body, func(s ir.Stmt) {
        ir.WalkExprs(s, func(e ir.Expr) {
            if r, ok := e.(*ir.ContextRead); ok {
                out[r.Ref] = true
            }
        })
    })
    return out
}

// componentComponentCalls walks body, returning every component call with
// the set of contexts that are shadowed at that call site (any enclosing
// ContextProvider).
func componentComponentCalls(comp *ir.Component) []componentCall {
    var out []componentCall
    var walk func(stmts []ir.Stmt, shadow map[*ir.Context]bool)
    walk = func(stmts []ir.Stmt, shadow map[*ir.Context]bool) {
        for _, s := range stmts {
            switch n := s.(type) {
            case *ir.ContextProvider:
                inner := copyShadow(shadow)
                inner[n.Ref] = true
                walk(n.Body, inner)
            case *ir.ComponentCall:
                out = append(out, componentCall{callee: n.Callee, shadowed: copyShadow(shadow)})
            case *ir.If:
                walk(n.Then, shadow)
                walk(n.Else, shadow)
            case *ir.For:
                walk(n.Body, shadow)
            // other stmt types with nested stmts: recurse similarly
            }
        }
    }
    walk(comp.Body, map[*ir.Context]bool{})
    return out
}

func copyShadow(m map[*ir.Context]bool) map[*ir.Context]bool {
    out := make(map[*ir.Context]bool, len(m))
    for k, v := range m {
        out[k] = v
    }
    return out
}
```

If `ir.WalkStmts`/`ir.WalkExprs` don't exist, write small local versions; or use `ir/treewalk.go` helpers if available. Verify the actual `ir.ComponentCall` IR shape — adjust the type assertion if it's `*ir.CallExpr` with a component target instead.

- [ ] **Step 4: Run, see pass**

Run: `go test ./internal/lower/ -run TestReachability -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/context.go internal/lower/context_test.go internal/lower/helpers_test.go
git commit -m "lower: NoContext reachability with shadowing"
```

### Task 15: Implement `addHiddenParams`

**Files:**
- Modify: `internal/lower/context.go`

For each `(ctx, comp)` in reach: add a synthetic `*ir.Var` parameter `__ctx_<ctx.Name>` of type `ctx.Typ` to `comp.Params`. Mark it synthetic to suppress LSP visibility.

- [ ] **Step 1: Failing test**

```go
func TestAddHiddenParams(t *testing.T) {
    pkg, ctx, comp := mustParseAndCheck(t, `
context #theme("light")
component A { text(value=theme) }
window #home(title="x", href="/") { A() }
`)
    reach := computeReachability(pkg)
    addHiddenParams(pkg, reach)
    a := comp("A")
    found := false
    for _, p := range a.Params {
        if p.Name == "__ctx_theme" && p.Type.Kind == ir.TypeString {
            found = true
        }
    }
    if !found {
        t.Fatalf("A.Params missing __ctx_theme: %v", a.Params)
    }
    _ = ctx
}
```

- [ ] **Step 2: Run, see fail**, then **Step 3: Implement**

```go
func addHiddenParams(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    for _, ctx := range pkg.Contexts {
        for _, comp := range pkg.Components {
            if !reach[ctx][comp] {
                continue
            }
            paramName := "__ctx_" + ctx.Name
            comp.Params = append(comp.Params, &ir.Var{
                Name: paramName,
                Typ:  ctx.Typ,
                Kind: ir.VarKindParam, // adjust if Kind field is different
                Synthetic: true,       // add field if absent
            })
        }
    }
}
```

If `ir.Var.Synthetic` doesn't exist, add it as a bool field on Var with a comment. Update any IR-walking pass that filters params if needed.

- [ ] **Step 4: Run, see pass**, then **Step 5: Commit**

```bash
git add internal/lower/context.go internal/lower/context_test.go ir/ir.go
git commit -m "lower: NoContext adds hidden __ctx_* params to Reach components"
```

### Task 16: Implement `rewriteReads`

Rewrite every `*ir.ContextRead{Ref: ctx}` inside a component in `Reach(ctx)` to a `*ir.VarRef{Var: __ctx_<ctx.Name>}` (or equivalent IR for a param read).

- [ ] **Step 1: Failing test**

```go
func TestRewriteReads(t *testing.T) {
    pkg, _, comp := mustParseAndCheck(t, `
context #theme("light")
component A { text(value=theme) }
window #home(title="x", href="/") { A() }
`)
    reach := computeReachability(pkg)
    addHiddenParams(pkg, reach)
    rewriteReads(pkg, reach)
    // Walk A.Body; expect no ContextRead remains; expect a VarRef to __ctx_theme.
    seenCtx := false
    seenVar := false
    ir.WalkExprs(comp("A").Body, func(e ir.Expr) {
        if _, ok := e.(*ir.ContextRead); ok {
            seenCtx = true
        }
        if v, ok := e.(*ir.VarRef); ok && v.Var.Name == "__ctx_theme" {
            seenVar = true
        }
    })
    if seenCtx {
        t.Errorf("ContextRead survived rewrite")
    }
    if !seenVar {
        t.Errorf("no VarRef to __ctx_theme produced")
    }
}
```

- [ ] **Step 2: Implement**

```go
func rewriteReads(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    // Pre-build param lookup: comp → ctx → *Var
    paramOf := map[*ir.Component]map[*ir.Context]*ir.Var{}
    for _, comp := range pkg.Components {
        paramOf[comp] = map[*ir.Context]*ir.Var{}
        for _, ctx := range pkg.Contexts {
            if !reach[ctx][comp] {
                continue
            }
            for _, p := range comp.Params {
                if p.Name == "__ctx_"+ctx.Name {
                    paramOf[comp][ctx] = p
                }
            }
        }
    }
    for _, comp := range pkg.Components {
        ir.RewriteExprsInStmts(comp.Body, func(e ir.Expr) ir.Expr {
            r, ok := e.(*ir.ContextRead)
            if !ok {
                return e
            }
            p, ok := paramOf[comp][r.Ref]
            if !ok {
                // Should not happen: reachability should have placed comp in Reach.
                return e
            }
            return &ir.VarRef{Var: p, Typ: r.Typ}
        })
    }
    // Also rewrite reads inside Window.Body using each window's default-context bindings (see Task 18).
}
```

If `ir.RewriteExprsInStmts` doesn't exist, write it (mirror `WalkExprs`).

- [ ] **Step 3: Run pass**, then **Step 4: Commit**

```bash
git add internal/lower/context.go ir/treewalk.go
git commit -m "lower: NoContext rewrites ContextRead to hidden param read"
```

### Task 17: Implement `lowerProviders`

For each `*ir.ContextProvider{Ref: ctx, Value: v, Body: stmts}`: replace in place with `stmts`, but for every `*ir.ComponentCall` inside `stmts` whose callee is in `Reach(ctx)`, splice an additional named arg `__ctx_<ctx.Name> = v`.

Nested providers compose: the innermost provider's value wins for calls inside its block; outer-block sibling calls see the outer.

- [ ] **Step 1: Failing test** — adapt `TestReachabilityShadowingBlocks`, after running all four steps, assert specific argument values on each component call.

- [ ] **Step 2: Implement**

```go
func lowerProviders(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    for _, comp := range pkg.Components {
        comp.Body = lowerProvidersInStmts(comp.Body, map[*ir.Context]ir.Expr{}, reach)
    }
    for _, w := range pkg.Windows {
        w.Body = lowerProvidersInStmts(w.Body, map[*ir.Context]ir.Expr{}, reach)
    }
}

func lowerProvidersInStmts(
    stmts []ir.Stmt,
    active map[*ir.Context]ir.Expr,
    reach map[*ir.Context]map[*ir.Component]bool,
) []ir.Stmt {
    out := make([]ir.Stmt, 0, len(stmts))
    for _, s := range stmts {
        switch n := s.(type) {
        case *ir.ContextProvider:
            inner := copyExprMap(active)
            inner[n.Ref] = n.Value
            lowered := lowerProvidersInStmts(n.Body, inner, reach)
            out = append(out, lowered...)
        case *ir.ComponentCall:
            for ctx, val := range active {
                if !reach[ctx][n.Callee] {
                    continue
                }
                n.Args = append(n.Args, &ir.NamedArg{Name: "__ctx_" + ctx.Name, Value: val})
            }
            out = append(out, n)
        case *ir.If:
            n.Then = lowerProvidersInStmts(n.Then, active, reach)
            n.Else = lowerProvidersInStmts(n.Else, active, reach)
            out = append(out, n)
        case *ir.For:
            n.Body = lowerProvidersInStmts(n.Body, active, reach)
            out = append(out, n)
        default:
            out = append(out, n)
        }
    }
    return out
}

func copyExprMap(m map[*ir.Context]ir.Expr) map[*ir.Context]ir.Expr {
    out := make(map[*ir.Context]ir.Expr, len(m))
    for k, v := range m {
        out[k] = v
    }
    return out
}
```

Adjust IR type names (`ComponentCall`, `NamedArg`, etc.) to match actual IR. If component calls are represented as `*ir.VisualNodeInst` or similar, route through that type.

- [ ] **Step 3: Test, commit**

```bash
git add internal/lower/context.go internal/lower/context_test.go
git commit -m "lower: NoContext rewrites providers to per-call hidden args"
```

### Task 18: Implement `injectRootDefaults`

At each window's root, prepend a `*ir.ContextProvider` *virtual* scope — equivalent to running `lowerProvidersInStmts` with `active = {ctx: ctx.Default for ctx in pkg.Contexts}` already populated. Simplest: run `lowerProvidersInStmts(w.Body, defaults, reach)` instead of empty initial active map.

Refactor Task 17's `lowerProviders` to accept an initial active map.

- [ ] **Step 1: Failing test**

```go
func TestRootDefaultInjected(t *testing.T) {
    pkg, _, _ := mustParseAndCheck(t, `
context #theme("default-val")
component A { text(value=theme) }
window #home(title="x", href="/") { A() }
`)
    if err := applyNoContext(pkg, Caps{NoContext: true}, Options{}); err != nil {
        t.Fatalf(err.Error())
    }
    // Window body's A() call should now have __ctx_theme = "default-val"
    win := pkg.Windows[0]
    found := false
    ir.WalkStmts(win.Body, func(s ir.Stmt) {
        if cc, ok := s.(*ir.ComponentCall); ok && cc.Callee.Name == "A" {
            for _, a := range cc.Args {
                if a.Name == "__ctx_theme" {
                    if lit, ok := a.Value.(*ir.StringLit); ok && lit.Value == "default-val" {
                        found = true
                    }
                }
            }
        }
    })
    if !found {
        t.Fatalf("root-default not injected on A() call")
    }
}
```

- [ ] **Step 2: Implement**

```go
func injectRootDefaults(pkg *ir.Package) {
    // No-op — Task 18 changes `lowerProviders` to seed initial active map
    // from defaults per-window. See updated lowerProviders below.
}
```

Refactor:

```go
func lowerProviders(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
    defaults := map[*ir.Context]ir.Expr{}
    for _, ctx := range pkg.Contexts {
        defaults[ctx] = ctx.Default
    }
    for _, w := range pkg.Windows {
        w.Body = lowerProvidersInStmts(w.Body, copyExprMap(defaults), reach)
    }
    // Component bodies: no defaults, because non-window-entry components are
    // only reached via a window's call chain — defaults flow in from there.
    for _, comp := range pkg.Components {
        comp.Body = lowerProvidersInStmts(comp.Body, map[*ir.Context]ir.Expr{}, reach)
    }
}
```

Remove the stub `injectRootDefaults` and remove its call from `applyNoContext`.

- [ ] **Step 3: Test, commit**

Run: `go test ./internal/lower/ -run TestRootDefault -v`
Expected: PASS.

```bash
git add internal/lower/context.go internal/lower/context_test.go
git commit -m "lower: NoContext seeds root defaults per window"
```

### Task 19: End-to-end FOLD fixture for lowering

**Files:**
- Create: `testdata/context_lower_hidden_prop.sngl`

- [ ] **Step 1: Write fixture with FOLD directives**

```sngl
context #theme("light")

component Toolbar {
    text(value=theme)
}

window #home(title="Home", href="/") {
    theme("dark") {
        Toolbar()
    }
}

// FOLD(NoContext) component Toolbar(__ctx_theme string)
// FOLD(NoContext) Toolbar(__ctx_theme="dark")
```

(Adjust `FOLD` directive syntax to match what `internal/testutil/sample.go` expects.)

- [ ] **Step 2: Run sample-driven tests**

Run: `go test ./... -run TestSampleFixtures -v`
Expected: PASS for this fixture.

- [ ] **Step 3: Commit**

```bash
git add testdata/context_lower_hidden_prop.sngl
git commit -m "test: FOLD fixture for NoContext lowering"
```

### Task 20: Reactive-value lowering fixture

**Files:**
- Create: `testdata/context_reactive_value.sngl`

- [ ] **Step 1: Write fixture**

```sngl
context #theme("light")

component Toolbar {
    text(value=theme)
}

window #home(title="Home", href="/") {
    var dark = false
    button(text="toggle", @click { dark = !dark })
    theme(dark ? "dark" : "light") {
        Toolbar()
    }
}
```

Add FOLD assertions that confirm the synthesized Toolbar call's `__ctx_theme` arg is the ternary expression (post-NoTernary it will be a temp ref) and that `passReactivity` adds an updater for the Toolbar call whose deps include `dark`.

- [ ] **Step 2: Run, commit**

```bash
git add testdata/context_reactive_value.sngl
git commit -m "test: NoContext + reactivity wires reactive provider values"
```

### Task 21: For/If lowering fixtures

**Files:**
- Create:
  - `testdata/context_for_loop_consumer.sngl`
  - `testdata/context_if_branch_consumer.sngl`

- [ ] **Step 1: Write `context_for_loop_consumer.sngl`**

```sngl
context #theme("light")

component Row {
    text(value=theme)
}

window #home(title="Home", href="/") {
    theme("dark") {
        for i = [1, 2, 3] {
            Row()
        }
    }
}
```

- [ ] **Step 2: Write `context_if_branch_consumer.sngl`**

```sngl
context #theme("light")

component A { text(value=theme) }
component B { text(value="static") }

window #home(title="Home", href="/") {
    var flag = true
    theme("dark") {
        if flag {
            A()
        } else {
            B()
        }
    }
}
```

- [ ] **Step 3: Run, commit**

```bash
git add testdata/context_for_loop_consumer.sngl testdata/context_if_branch_consumer.sngl
git commit -m "test: NoContext threads through for/if bodies"
```

### Task 22: Nested shadowing end-to-end

**Files:**
- Update: `testdata/context_nested_shadowing.sngl` (add FOLD)

- [ ] **Step 1: Add FOLDs to the fixture from Task 11**

After the nested provider example, add directives confirming each `Show()` call carries the expected `__ctx_theme` argument value.

- [ ] **Step 2: Run, commit**

```bash
git add testdata/context_nested_shadowing.sngl
git commit -m "test: nested shadowing end-to-end FOLD"
```

---

## Phase 5 — Per-platform wiring

### Task 23: Turn `NoContext` on for every platform

**Files:**
- Modify: `codegen/platform/html/html.go` (or its Capabilities() site)
- Modify: `codegen/platform/fyne/fyne.go`
- Modify: `codegen/platform/gtk4/gtk4.go`
- Modify: `codegen/platform/bubbletea/bubbletea.go`
- Modify: `codegen/platform/android/android.go`
- Modify: `codegen/platform/none/none.go`

- [ ] **Step 1: Find each `Capabilities()` method**

Run: `grep -rn "Capabilities()" codegen/platform/`

- [ ] **Step 2: Add `NoContext: true` to each return value**

- [ ] **Step 3: Run the full test suite**

Run: `go tool verify`
Expected: PASS (modulo failures in i18n / test-harness fixtures that Phases 6-7 address).

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/
git commit -m "codegen: NoContext on for all platforms (v1)"
```

### Task 24: Verify each platform handles synthesized hidden props

For each platform, the existing prop-handling code path should accept the synthesized `__ctx_*` args without special-casing. Most likely no code change needed. Add a smoke fixture per platform if not already covered.

- [ ] **Step 1: Run platform-specific snapshot/golden tests**

Run: `go test ./codegen/platform/html/... ./codegen/platform/fyne/... ./codegen/platform/bubbletea/... ./codegen/platform/android/... ./codegen/platform/gtk4/... ./codegen/platform/none/... -v`

- [ ] **Step 2: Triage any failures**

If a platform's codegen rejects an `__ctx_` param (e.g. because of a naming policy or because primitive widgets receive it), debug and patch the platform's prop walker. Document in a comment the exception.

- [ ] **Step 3: Add per-platform golden test** (if absent)

For each platform, create a minimal `testdata/_platform_<name>/context_smoke.sngl` (or wherever the snapshot tests live) that uses a context and asserts the generated output compiles. Verify with the snapshotter.

- [ ] **Step 4: Commit per-platform fixes**

```bash
git add ...
git commit -m "codegen(<platform>): accept __ctx_* synthesized hidden args"
```

---

## Phase 6 — Test harness: `t.setContext`

### Task 25: Interpreter (`none/testrunner`) support

**Files:**
- Modify: `codegen/platform/none/testrunner/runner.go`
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 1: Failing test**

Create `testdata/context_test_setContext_none.sngl`:

```sngl
context #locale("en-US")

component Greeting {
    text(value=locale)
}

test "locale override" {
    t.setContext(locale, "es-MX")
    t.mount(Greeting())
    t.assertText("es-MX")
}
```

Run: `go test ./codegen/platform/none/testrunner/ -v`
Expected: FAIL — `setContext` not recognized.

- [ ] **Step 2: Implement in `testing_t.go`**

Add a `setContext(name, value)` method to the test-T value the interpreter exposes. Store overrides in a map on the T struct. On the next `mount`, wrap the rendered subject in synthetic providers for each override entry.

- [ ] **Step 3: Run, see pass, commit**

```bash
git add codegen/platform/none/testrunner/ testdata/context_test_setContext_none.sngl
git commit -m "testrunner(none): support t.setContext"
```

### Task 26: Go-lowered tests (`testlower.go` for golang)

**Files:**
- Modify: `codegen/lang/golang/testlower.go`

- [ ] **Step 1: Inspect existing `Test.*` lowering switch**

Find the switch at line ~50 of `testlower.go`. Identify the `setLocale` TODO.

- [ ] **Step 2: Remove `setLocale` TODO; add `setContext` lowering**

```go
case "setContext":
    // t.setContext(ctxRef, valueExpr) — lower to a write of a per-test
    // var that the mount synthesizer threads as that context's value.
    if len(c.Args) != 2 {
        return []string{fmt.Sprintf("// ERROR: t.setContext takes 2 args, got %d", len(c.Args))}
    }
    ctxName := identArgName(c.Args[0])
    valExpr := lowerExpr(c.Args[1])
    return []string{fmt.Sprintf("__test_ctx_%s = %s", ctxName, valExpr)}
```

Also delete the generic `setLocale` stub case (the `// TODO: lower t.<name>` default branch now only fires for genuinely-unimplemented methods).

The `__test_ctx_<name>` vars are declared at test-fn scope by the test runner's mount synthesizer (see Task 27).

- [ ] **Step 3: Wire mount synthesizer to emit __test_ctx_<name> + provider wrap**

In the same file, find `mount` lowering. Before emitting the mount call, emit declarations for every context the test references and wrap the mounted subject in `theme(__test_ctx_theme) { ... }` synthesized providers.

- [ ] **Step 4: Failing test fixture**

`testdata/context_test_setContext_go.sngl` for fyne/bubbletea (uses Go-lowered runner). Mirror the `none` fixture from Task 25.

- [ ] **Step 5: Run, commit**

```bash
git add codegen/lang/golang/testlower.go testdata/context_test_setContext_go.sngl
git commit -m "testlower(go): lower t.setContext for fyne/bubbletea runners"
```

### Task 27: Kotlin testlower

**Files:**
- Modify: `codegen/lang/kotlin/testlower.go`

- [ ] **Step 1-5:** Mirror Task 26 — same logic, Kotlin syntax for the emitted vars and provider wrap.

- [ ] **Step 6: Commit**

```bash
git add codegen/lang/kotlin/testlower.go testdata/context_test_setContext_android.sngl
git commit -m "testlower(kotlin): lower t.setContext for android runner"
```

---

## Phase 7 — i18n migration

### Task 28: Add `i18n.defaultLocale()` stdlib func

**Files:**
- Modify: `lib/i18n.sngl`

- [ ] **Step 1: Edit `lib/i18n.sngl`**

Add at top of file:

```sngl
// Returns the process-startup locale string. Reads $LC_ALL, then
// $LC_MESSAGES, then $LANG, falling back to "en-US".
func i18n.defaultLocale() string {}

// Stdlib context used by all i18n.* formatters. Overridable per-subtree
// via `locale("xx-YY") { ... }` provider.
context #locale(i18n.defaultLocale())
```

- [ ] **Step 2: Implement runtime side per language**

In `pkg/go/i18n/i18n.go`, add:

```go
// DefaultLocale resolves the process-startup locale from env, with fallback.
func DefaultLocale() string {
    for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
        if v := os.Getenv(k); v != "" {
            return strings.SplitN(v, ".", 2)[0]
        }
    }
    return "en-US"
}
```

Mirror in `pkg/js/i18n` (read from `navigator.language`-style fallback or a runtime injection) and `pkg/kotlin/i18n` (read `Locale.getDefault()` BCP-47 tag).

- [ ] **Step 3: Run checker tests**

Run: `go test ./internal/checker/ -v`
Expected: PASS — the stdlib parses correctly and `locale` is registered as a context.

- [ ] **Step 4: Commit**

```bash
git add lib/i18n.sngl pkg/go/i18n/i18n.go pkg/js/i18n/ pkg/kotlin/i18n/
git commit -m "stdlib(i18n): add defaultLocale() + locale context decl"
```

### Task 29: Mark i18n.* funcs as locale-context readers

**Files:**
- Modify: `internal/checker/stdlib.go` (or wherever stdlib funcs are loaded)

- [ ] **Step 1: Decide marker mechanism**

Option A — annotation on `*ir.Func`: add a `ReadsContexts []*Context` field, set during stdlib parse for the i18n functions.

Option B — predicate in `NoContext` lowering: hardcode the list of i18n.* function names.

Pick A (declarative, extensible). Define the marker.

- [ ] **Step 2: Set marker for i18n.tr, i18n.format, i18n.numberInt, i18n.numberFloat, i18n.date, i18n.time, i18n.datetime, i18n.select, i18n.plural, i18n.selectordinal**

In the stdlib loader, after parsing `lib/i18n.sngl`, locate each of these `*ir.Func` and append the `locale` `*ir.Context` to `ReadsContexts`.

- [ ] **Step 3: Teach `NoContext` to honor `ReadsContexts`**

In `computeReachability`: a component that contains a call to a `*ir.Func` whose `ReadsContexts` lists `ctx` is treated as a direct reader of `ctx`.

In `lowerProviders`: at each call site to such a func, splice an additional named arg `locale=<active value or default>` into the call's args.

In `addHiddenParams`: extend each i18n.* func's params with the corresponding `__ctx_locale` (or just pass it as the function's last positional/named arg without renaming — they're called via the generated language's bindings).

Actually for stdlib funcs that compile to a target-language runtime call, the cleanest path is to thread the locale as an extra arg on the call site. The lowering pass writes `i18n.tr(...args..., locale=<active>)`; per-language codegen renders that as `i18n.NewTranslator(locale).Tr(...)` (Go), the JS equivalent, the Kotlin equivalent.

- [ ] **Step 4: Failing fixture**

`testdata/context_i18n_locale_override.sngl`:

```sngl
component Page {
    text(value=$"Hello")
}

window #home(title="x", href="/") {
    locale("es-MX") {
        Page()
    }
    Page()
}

// FOLD(NoContext) i18n.tr("Hello", {...}, locale="es-MX")  // inside the override
// FOLD(NoContext) i18n.tr("Hello", {...}, locale=i18n.defaultLocale())  // outside
```

- [ ] **Step 5: Run, commit**

```bash
git add internal/checker/stdlib.go internal/lower/context.go ir/ir.go testdata/context_i18n_locale_override.sngl
git commit -m "lower: thread locale through i18n.* call sites"
```

### Task 30: Per-language runtime: per-call translator

**Files:**
- Modify: `pkg/go/i18n/i18n.go`
- Modify: `pkg/js/i18n/*.js`
- Modify: `pkg/kotlin/i18n/*.kt`

- [ ] **Step 1: Go — locale-keyed translator cache**

```go
var translators sync.Map // map[string]*Translator

func translatorFor(locale string) *Translator {
    if v, ok := translators.Load(locale); ok {
        return v.(*Translator)
    }
    t := NewTranslator(manifest, locale)
    actual, _ := translators.LoadOrStore(locale, t)
    return actual.(*Translator)
}

func Tr(key, inlined string, args map[string]any, locale string) string {
    return translatorFor(locale).Tr(key, inlined, args)
}
// Mirror NumberInt, NumberFloat, Date, Time, DateTime, Plural, etc.
```

Remove any process-global Translator state.

- [ ] **Step 2: JS — per-call locale arg**

In `pkg/js/i18n/*.js`, rewrite formatters to take `locale` as the final arg. Drop module-level "current locale" state.

- [ ] **Step 3: Kotlin — drop `I18n.init(context)` + read manifest lazily**

In `pkg/kotlin/i18n/I18n.kt`:
- Manifest loaded on first call (synchronized once).
- Each formatter takes locale string, constructs `android.icu.text.MessageFormat`/`NumberFormat`/`DateFormat` with the BCP-47 tag.

In `codegen/lang/kotlin/` — find the `I18n.init(context)` injection site in `MainActivity.onCreate` and remove it.

- [ ] **Step 4: Run all runtime tests**

Run: `go test ./pkg/go/i18n/... -v`
Expected: PASS.

For JS/Kotlin, run the existing test commands for those packages.

- [ ] **Step 5: Run end-to-end i18n tests**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/go/i18n/ pkg/js/i18n/ pkg/kotlin/i18n/ codegen/lang/kotlin/
git commit -m "runtime(i18n): per-call locale; drop process-global state"
```

### Task 31: Migrate test fixtures off `t.setLocale`

**Files:**
- Modify: every `testdata/**/*.sngl` containing `t.setLocale(`

- [ ] **Step 1: Find them**

Run: `grep -rln "t\.setLocale\|setLocale" testdata/ codegen/`

- [ ] **Step 2: Replace each `t.setLocale("xx-YY")` with `t.setContext(locale, "xx-YY")`**

Use a single sed pass or manual edits, depending on volume.

- [ ] **Step 3: Run full suite**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add testdata/ codegen/
git commit -m "test: migrate t.setLocale → t.setContext(locale, ...)"
```

### Task 32: Remove `t.setLocale` stubs

**Files:**
- Modify: `codegen/lang/golang/testlower.go`
- Modify: `codegen/lang/kotlin/testlower.go`

- [ ] **Step 1: Delete the `setLocale` stub branches and any associated TODO comments**

Verify with `grep`. The `Test.*` switches should no longer have a `case "setLocale"` and the default-branch TODO message should reflect that `setContext` is the supported path.

- [ ] **Step 2: Run**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add codegen/lang/
git commit -m "testlower: remove t.setLocale stubs (replaced by t.setContext)"
```

---

## Phase 8 — LSP + dump

### Task 33: LSP hover / goto-def / completion for context names

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/definition.go`
- Modify: `internal/lspcore/completion.go`

- [ ] **Step 1: Add hover handling**

When the cursor's symbol resolves to `*ir.Context`: render `context #<name>: <type> = <default-expr-snippet>`.

- [ ] **Step 2: Goto-def**

Already works if context decls populate the scope/symbol table via `c.scope.Declare(ctx)` from Task 5. Add an integration test.

- [ ] **Step 3: Completion**

In completion proposal builder: in visual-node position (block context), include in-scope contexts as candidates. In expression position, include them as identifier candidates with hint `(context)`.

- [ ] **Step 4: Tests**

Add `internal/lspcore/lsp_context_test.go` with three small tests (hover, goto-def, completion) exercising a `context #theme(...)` decl.

- [ ] **Step 5: Run, commit**

```bash
git add internal/lspcore/
git commit -m "lsp: hover / goto-def / completion for context names"
```

### Task 34: `sngl dump` surfaces contexts

**Files:**
- Modify: `cmd/sngl/dump.go` (or wherever `dump parsed/checked/optimized` are wired)

- [ ] **Step 1: Add context emission**

In each dump mode, emit context decls (round-trip through `ir.Convert` already produces them as visual nodes for the parsed/checked stages). For `dump analysis`, include `Reach(ctx)` per context.

- [ ] **Step 2: Add `sngl dump` golden test for context fixture**

Use the existing txtar harness in `cmd/sngl/script_test.go`.

- [ ] **Step 3: Commit**

```bash
git add cmd/sngl/dump.go cmd/sngl/testdata/dump_context.txt
git commit -m "dump: surface context decls + analysis reachability"
```

---

## Phase 9 — Documentation + final sweep

### Task 35: Mark spec status shipped

**Files:**
- Modify: `docs/superpowers/specs/2026-05-16-reactive-context-design.md`

- [ ] **Step 1: Change Status field**

`**Status:** Shipped`

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-16-reactive-context-design.md
git commit -m "docs(specs): mark reactive-context as shipped"
```

### Task 36: Update CLAUDE.md / Sngl docs if needed

- [ ] **Step 1: Scan `CLAUDE.md`, `docs/`, and `website.sngl` (showcase tour)**

If any file references the old singleton-locale story or `t.setLocale`, update.

- [ ] **Step 2: Add context to the showcase if appropriate**

Per the design-spec testdata table, `#theme` is the canonical user-code example. Consider adding a small showcase page.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md docs/ website.sngl
git commit -m "docs: mention context primitive in showcase + project guide"
```

### Task 37: Final verification

- [ ] **Step 1: Run full test suite**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 2: Build CLI**

Run: `go install ./cmd/sngl && sngl --version`

- [ ] **Step 3: WASM build**

Run: `GOOS=js GOARCH=wasm go build ./internal/playground`
Expected: builds clean.

- [ ] **Step 4: Smoke-run the docs site**

Run: `go tool docsgen`
Expected: completes without errors.

If anything fails, fix root cause, re-run.

---

## Self-Review Notes

**Spec coverage check:**
- §Surface: Tasks 1, 2, 5, 8, 9 (decl + provider + consumer parse/check). ✓
- §Semantics → Reachability: Task 14. ✓
- §Semantics → Root default: Task 18. ✓
- §Semantics → Nested provider lowering: Tasks 17, 22. ✓
- §Semantics → Reactivity: Task 20 (passes through existing reactivity). ✓
- §Pipeline → Parser: Tasks 1, 2 (no new AST node — visual-node reuses existing surface). ✓
- §Pipeline → AST: documented in plan; nothing to add (Document.Visuals holds context decls). ✓
- §Pipeline → Checker: Tasks 5, 8, 9, 10, 6, 7. ✓
- §Pipeline → IR & ir.Convert: Tasks 3, 4. ✓
- §Pipeline → Lowering: Tasks 12-18. ✓
- §Pipeline → Capability flag: Tasks 12, 23. ✓
- §Pipeline → Formatter: Tasks 1, 2 (round-trip). ✓
- §Pipeline → LSP: Task 33. ✓
- §Pipeline → Dump: Task 34. ✓
- §Per-platform lowering: Tasks 23, 24. ✓
- §Test harness: Tasks 25, 26, 27. ✓
- §i18n migration: Tasks 28, 29, 30, 31, 32. ✓
- §Testdata fixtures: Tasks 1, 2, 5-11, 19-22, 25, 29. ✓

**Placeholder scan:** No "TBD"/"TODO"/etc. in plan text. Stubs in skeleton code (Task 13's `panic("TODO Task N")`) are intentional and resolved by named follow-up tasks.

**Type consistency:** Verified `__ctx_<name>` hidden-param naming used uniformly across Tasks 15-22, 26, 29. `Reach` map shape consistent across Tasks 14-18. `ReadsContexts` field on `*ir.Func` used consistently in Task 29.
