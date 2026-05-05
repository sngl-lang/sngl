# NoLambda — Phase A (Surface + IR Shapes) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land everything `NoLambda` needs *before* the pass body itself: the `ref<T>` type, `&` / `*` unary operators in SNGL surface, the `*ir.Closure` IR expression, and the corresponding parser / formatter / checker / optimizer / `ir.Convert` plumbing. After Phase A the build is green, no behavior changes for any existing program, and Phase B can implement the pass against a stable IR.

**Architecture:** Treat `ref<T>` as a builtin parametric type recognized by the checker (parallel to `list<T>` / `option<T>` — `ast.NamedType` carries it through the parser). Add `UnaryAddr` and `UnaryDeref` to the existing `ast.UnaryOp` enum so `ast.UnaryExpr` covers them with no new AST node. The IR gains one new `TypeKind` (`TypeRef`) and one new expression (`*ir.Closure`); both are inert until Phase B emits them. Auto-deref through `Select` materializes as an explicit `*ir.Unary{UnaryDeref}` so downstream passes never need a special case.

**Tech Stack:** Go 1.24+, existing `ast`/`ir`/`internal/parser`/`internal/checker`/`internal/optimize`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-03-nolambda-design.md`
**Reference plans:** `docs/superpowers/plans/2026-05-02-codegen-lowering-phase2b.md`

---

## File Structure

**Modify:**
- `ast/expr.go` — add `UnaryAddr` and `UnaryDeref` constants to `UnaryOp`.
- `ast/unaryop_string.go` — regenerate via stringer.
- `ir/types.go` — add `TypeRef` Kind, equality, printing, helpers.
- `ir/typekind_string.go` — regenerate via stringer.
- `ir/expr.go` — add `*ir.Closure` expression type.
- `ir/convert.go` — render `*ir.Closure`, `TypeRef`, and the two new unary ops.
- `ir/strip.go` — handle the new shapes during strip if relevant.
- `internal/parser/parse.go` — accept `&` / `*` as unary operators; recognize `ref<T>` via the existing generic-type path.
- `internal/parser/format.go` — print `&x`, `*x`, `ref<T>`.
- `internal/checker/resolve.go` — resolve `ref<T>` to `ir.RefOf(elem)`.
- `internal/checker/expr.go` — type-check `UnaryAddr` / `UnaryDeref` expressions; auto-deref through `Select`.
- `internal/checker/checker.go` — register `ref` in the builtin-type-name list.
- `internal/optimize/fold.go` — early-return for `UnaryDeref` / `UnaryAddr` in const folder.
- `internal/lower/walk.go` — extend `walkPackage` to descend into `*ir.Closure` operands when they appear (forward-compat; Phase B emits them).

**Create:**
- `internal/parser/testdata/ref_type_basic.sngl` — round-trip fixture.
- `internal/parser/testdata/ref_type_in_struct.sngl` — round-trip fixture.
- `internal/parser/testdata/unary_addr_deref.sngl` — round-trip fixture.

---

### Task 1: AST UnaryOp constants

**Files:**
- Modify: `ast/expr.go`
- Modify: `ast/unaryop_string.go`

- [ ] **Step 1: Add the constants**

Edit `ast/expr.go`. Locate the `UnaryOp` const block (around line 77-80) and extend:

```go
const (
    UnaryNot   UnaryOp = iota // !
    UnaryNeg                  // -
    UnaryAddr                 // &
    UnaryDeref                // *
)
```

- [ ] **Step 2: Regenerate stringer**

Run: `cd ast && go generate ./... && cd ..`
Expected: `ast/unaryop_string.go` updated. The new file's `_UnaryOp_name` constant should read `"!-&*"` and the index `[0, 1, 2, 3, 4]`.

- [ ] **Step 3: Compile check**

Run: `go build ./...`
Expected: passes; existing switches over `UnaryOp` may now produce exhaustiveness warnings — that is the next task.

- [ ] **Step 4: Commit**

```bash
git add ast/expr.go ast/unaryop_string.go
git commit -m "ast: add UnaryAddr and UnaryDeref UnaryOps"
```

---

### Task 2: IR TypeRef kind

**Files:**
- Modify: `ir/types.go`
- Modify: `ir/typekind_string.go`

- [ ] **Step 1: Add the kind**

Edit `ir/types.go`. Locate the `TypeKind` const block (the `TypeIPV6` line ends the existing list; add immediately after, around line 36):

```go
const (
    TypeInvalid TypeKind = iota // error sentinel
    TypeDyn                     // unknown/dynamic
    TypeBool
    TypeInt
    TypeFloat
    TypeString
    TypeList      // Elem set
    TypeOption    // Elem set
    TypeStruct    // Decl set
    TypeEnum      // Decl set
    TypeUnit      // Decl set
    TypeFunc      // Sig set
    TypeComponent // Decl set
    TypeColor
    TypeDate
    TypeTime
    TypeDateTime
    TypeDuration
    TypeURL
    TypeEmail
    TypeUUID
    TypeRegex
    TypeBase64
    TypeIPV4
    TypeIPV6
    TypeRef // Elem set — ref<T>, used by NoLambda for mutable captures
    // ...keep any remaining trailing kinds in their existing order...
)
```

(Audit the original ordering before saving; insert `TypeRef` immediately after the last existing kind, keeping `TypeInvalid = 0` stable.)

- [ ] **Step 2: Add helper constructor and equality arm**

In the same file, alongside `ListOf` / `OptionOf`, add:

```go
// RefOf builds a ref<elem> Type.
func RefOf(elem *Type) *Type {
    return &Type{Kind: TypeRef, Elem: elem}
}
```

Locate the type-equality / IsAssignable / IsCompatible code (search for `t.Kind == TypeList` to find the right spot — the same arm typically also handles `TypeOption`). For each such arm, add a `TypeRef` case that requires `t.Elem` and `other.Elem` to match.

- [ ] **Step 3: Regenerate stringer**

Run: `cd ir && go generate ./... && cd ..`
Expected: `ir/typekind_string.go` updated to include `TypeRef`.

- [ ] **Step 4: Compile check**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Commit**

```bash
git add ir/types.go ir/typekind_string.go
git commit -m "ir: add TypeRef Kind plus RefOf constructor"
```

---

### Task 3: IR Closure expression

**Files:**
- Modify: `ir/expr.go`
- Modify: `ir/strip.go` (only if it walks Expr nodes)

- [ ] **Step 1: Add the type and interface methods**

Edit `ir/expr.go`. After the existing `Lambda` definition (around line 162), add:

```go
// Closure is a lifted lambda: a top-level Func plus a captured-state struct
// literal. NoLambda emits Closures in place of every Lambda. Codegen for
// closure-supporting target languages never sees a Closure (their cap is
// off); closure-free targets translate Closure into a (state, fn-ref) pair.
type Closure struct {
    AST   *ast.LambdaExpr // original lambda position; nil for synthesized handler lifts
    Type  *Type           // user-visible TypeFunc — without the synthesized leading state param
    Func  *Func           // lifted top-level Func; first Param is the state struct
    State *StructLit      // captured-state struct construction at this site
}
```

In the same file, extend the existing exprNode/ExprType blocks. Add:

```go
func (*Closure) exprNode()        {}
func (x *Closure) ExprType() *Type { return x.Type }
```

In the `IsConst` switch in the same file, add:

```go
case *Closure:
    return false
```

- [ ] **Step 2: Audit ir/strip.go**

Open `ir/strip.go`. If it has an `Expr` switch that calls `panic("unhandled")` or similar on unknown shapes, add a `*Closure` case that strips its `State` and recursively descends into `Func.Block` (treat it like a Func body). If `strip.go` does not walk Expr nodes, skip.

- [ ] **Step 3: Compile check**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 4: Commit**

```bash
git add ir/expr.go ir/strip.go
git commit -m "ir: add Closure expression for lifted lambdas"
```

---

### Task 4: ir.Convert renderers

**Files:**
- Modify: `ir/convert.go`

The Convert routine builds `ast.Document` / `ast.Expr` from `*ir.Package`. Each new IR shape needs a renderer.

- [ ] **Step 1: Render TypeRef**

Find the `convertType` (or equivalently named) function in `ir/convert.go`. Add a case before the default fallback:

```go
case ir.TypeRef:
    return &ast.NamedType{
        Name:     "ref",
        TypeArgs: []ast.TypeExpr{convertType(t.Elem)},
    }
```

- [ ] **Step 2: Render UnaryAddr / UnaryDeref**

Find the function that converts `*ir.Unary` to `ast.UnaryExpr`. The existing implementation likely passes `Op` through unchanged — verify the new constants flow through. If the function whitelists ops, extend the whitelist.

- [ ] **Step 3: Render Closure**

Find the function that converts `ir.Expr` to `ast.Expr`. Add a case:

```go
case *ir.Closure:
    // Render as a synthetic call: __closure(funcRef, structLit).
    return &ast.CallExpr{
        Callee: &ast.IdentExpr{Name: "__closure"},
        Args: ast.ArgList{Args: []ast.Arg{
            {Value: &ast.IdentExpr{Name: x.Func.Name}},
            {Value: convertExpr(x.State)},
        }},
    }
```

This renders as `__closure(__lambda0, __lambda0_caps{...})` in the formatted SNGL — debug-readable, not parseable as user code (the parser does not need to accept `__closure(...)`; the renderer is a one-way debug view).

- [ ] **Step 4: Compile + smoke run**

Run: `go build ./...`
Expected: passes.

Run: `go test ./ir/... -run Convert`
Expected: passes; if there is a fuzz test, run a brief seed: `go test ./ir/... -run Convert -fuzz=. -fuzztime=10s` (skip if no fuzz target exists).

- [ ] **Step 5: Commit**

```bash
git add ir/convert.go
git commit -m "ir: render TypeRef, Closure, UnaryAddr/Deref via ir.Convert"
```

---

### Task 5: Parser — `&` and `*` unary

**Files:**
- Modify: `internal/parser/parse.go`

- [ ] **Step 1: Locate the unary-expression parse rule**

Search for `UnaryNot` and `UnaryNeg` in `internal/parser/parse.go`. The unary parser is typically a function like `parseUnary` that switches on the leading token.

- [ ] **Step 2: Extend the switch**

Wherever `case '!'` and `case '-'` produce `UnaryNot` / `UnaryNeg`, add:

```go
case '&':
    p.next()
    operand := p.parseUnary()
    return &ast.UnaryExpr{Pos: pos, Op: ast.UnaryAddr, Operand: operand}
case '*':
    p.next()
    operand := p.parseUnary()
    return &ast.UnaryExpr{Pos: pos, Op: ast.UnaryDeref, Operand: operand}
```

(Adjust to match the existing function's local conventions — token-type vs rune, helper names, etc.)

- [ ] **Step 3: Confirm `*` does not collide with multiplication**

The existing parser already distinguishes prefix `-` (UnaryNeg) from binary `-`. `*` follows the same pattern: only when no left operand is present. Search for `BinaryMul` to confirm the binary-`*` branch is in `parseMulDiv` (or similar) and the prefix branch is in `parseUnary`. Verify visually; no code change needed if so.

- [ ] **Step 4: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Commit**

```bash
git add internal/parser/parse.go
git commit -m "parser: accept & and * as prefix unary ops"
```

---

### Task 6: Parser — `ref<T>` type expression

**Files:**
- Modify: `internal/parser/parse.go`

- [ ] **Step 1: Verify generic-type parsing covers `ref<T>` already**

Search for `list<` or `parseTypeExpr` / `parseType`. Generic types like `list<int>` parse into `ast.NamedType{Name: "list", TypeArgs: [...]}`. Because the grammar is name-based, `ref<int>` should parse identically with no parser change required.

- [ ] **Step 2: Add a parser test fixture**

Create `internal/parser/testdata/ref_type_basic.sngl`:

```
struct StateBag {
    counter: ref<int>
    label: ref<string>
}
```

Run the existing parser round-trip test on this file. Find the test function (search for `TestParseFormat` or `TestRoundTrip` in `internal/parser/`). Add the fixture to its inputs if the table is hand-listed; if it walks `testdata/` automatically, the fixture is picked up.

- [ ] **Step 3: Run the fixture**

Run: `go test ./internal/parser/ -run TestParse -v`
Expected: pass; the round-trip prints `ref<int>` / `ref<string>` correctly.

- [ ] **Step 4: Commit**

```bash
git add internal/parser/testdata/ref_type_basic.sngl
git commit -m "parser: round-trip fixture for ref<T> type"
```

---

### Task 7: Formatter — print `&`, `*`, `ref<T>`

**Files:**
- Modify: `internal/parser/format.go`

- [ ] **Step 1: Verify `ref<T>` already prints**

`ast.NamedType` formatting prints `Name<arg, arg, ...>` for any name with TypeArgs — so `ref<int>` falls out of existing logic. Read the function that handles `NamedType` in `format.go` to confirm. No change needed.

- [ ] **Step 2: Verify `&` / `*` already print**

`ast.UnaryExpr` formatting reads `op.String()` which now returns `"&"` / `"*"` (Task 1's stringer regen). Read the unary-printing function to confirm; the existing `UnaryNot` / `UnaryNeg` path covers the new ops with no branch.

- [ ] **Step 3: Add the round-trip fixture**

Create `internal/parser/testdata/unary_addr_deref.sngl`:

```
component main {
    var x: int = 0
    var p: ref<int> = &x
    func read() -> int => *p
}
```

- [ ] **Step 4: Run round-trip**

Run: `go test ./internal/parser/ -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/parser/testdata/unary_addr_deref.sngl
git commit -m "parser: round-trip fixture for & and * unary ops"
```

---

### Task 8: Checker — `ref<T>` resolution

**Files:**
- Modify: `internal/checker/resolve.go`
- Modify: `internal/checker/checker.go`

- [ ] **Step 1: Add `ref` to generic-builtins switch**

Edit `internal/checker/resolve.go`. Locate the switch that handles `"list"` and `"option"` (around line 99). Add:

```go
case "ref":
    if len(t.TypeArgs) == 0 {
        c.error(t.Pos, "ref requires a type argument, e.g. ref<int>")
        return ir.RefOf(TypDyn)
    }
    return ir.RefOf(c.resolveType(t.TypeArgs[0]))
```

- [ ] **Step 2: Add `ref` to the builtin-type-name list**

Edit `internal/checker/checker.go`. Find the case-list at line 662 (`case "int", "float", "string", "bool", "list", "color":`). Add `"ref"`:

```go
case "int", "float", "string", "bool", "list", "color", "ref":
```

- [ ] **Step 3: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 4: Commit**

```bash
git add internal/checker/resolve.go internal/checker/checker.go
git commit -m "checker: resolve ref<T> as ir.TypeRef"
```

---

### Task 9: Checker — type-check `&` and `*`

**Files:**
- Modify: `internal/checker/expr.go`

- [ ] **Step 1: Locate the unary-checker switch**

Search `internal/checker/expr.go` for `UnaryNot`. The function is typically `inferUnary` or similar. It reads the Operand's checked type, validates, and returns the result Expr.

- [ ] **Step 2: Add UnaryAddr arm**

Add a case mirroring the existing arms:

```go
case ast.UnaryAddr:
    operandIR := c.checkExpr(x.Operand)
    if !c.isAddressable(operandIR) {
        c.error(*x.ExprPos(), "cannot take address of non-lvalue expression")
        return &ir.Unary{AST: x, Type: ir.RefOf(exprType(operandIR)), Op: ast.UnaryAddr, Operand: operandIR}
    }
    return &ir.Unary{AST: x, Type: ir.RefOf(exprType(operandIR)), Op: ast.UnaryAddr, Operand: operandIR}
```

Helper:

```go
// isAddressable reports whether e is an lvalue suitable as the operand of `&`.
// Per the NoLambda spec: Idents resolving to *Var or *Param, Selects bottoming
// out at one of those, and Index against an addressable list operand.
func (c *checker) isAddressable(e ir.Expr) bool {
    switch x := e.(type) {
    case *ir.Ident:
        switch x.Sym.(type) {
        case *ir.Var, *ir.Param:
            return true
        }
        return false
    case *ir.Select:
        return c.isAddressable(x.Operand)
    case *ir.Index:
        return c.isAddressable(x.Operand)
    }
    return false
}
```

- [ ] **Step 3: Add UnaryDeref arm**

```go
case ast.UnaryDeref:
    operandIR := c.checkExpr(x.Operand)
    operandType := exprType(operandIR)
    if operandType.Kind != ir.TypeRef {
        c.error(*x.ExprPos(), "cannot dereference non-ref type %s", operandType)
        return &ir.Unary{AST: x, Type: ir.TypDyn, Op: ast.UnaryDeref, Operand: operandIR}
    }
    return &ir.Unary{AST: x, Type: operandType.Elem, Op: ast.UnaryDeref, Operand: operandIR}
```

- [ ] **Step 4: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Add a positive checker test**

Create or extend a test in `internal/checker/expr_test.go` (or wherever existing checker tests live) with a snippet:

```go
func TestCheckRefAndDeref(t *testing.T) {
    src := `
component main {
    var x: int = 0
    var p: ref<int> = &x
    var y: int = *p
}`
    pkg := mustCheckPackage(t, src)
    if len(pkg.Diagnostics) != 0 {
        t.Fatalf("unexpected diagnostics: %v", pkg.Diagnostics)
    }
}
```

(Match the helper-call shape used elsewhere in the file — e.g. `parseAndCheck`, `checkSource`, etc.)

- [ ] **Step 6: Add an error-case test**

```go
func TestCheckAddrOfNonLvalue(t *testing.T) {
    src := `
component main {
    var p: ref<int> = &(1 + 2)
}`
    pkg := mustCheckPackage(t, src)
    requireErrorContains(t, pkg.Diagnostics, "non-lvalue")
}
```

- [ ] **Step 7: Run**

Run: `go test ./internal/checker/ -v`
Expected: both new tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/checker/expr.go internal/checker/expr_test.go
git commit -m "checker: type-check & and * unary ops"
```

---

### Task 10: Checker — auto-deref through `Select`

**Files:**
- Modify: `internal/checker/expr.go`

- [ ] **Step 1: Locate `inferSelect` (or equivalent)**

Search for the function that type-checks `*ast.SelectExpr`. The current logic resolves `operand.field` against the operand's type.

- [ ] **Step 2: Insert auto-deref**

Before resolving the field against the operand's type, check whether the operand is `ref<T>`. If so, wrap the operand in an explicit `*ir.Unary{UnaryDeref}` and continue resolution against `T`:

```go
operandIR := c.checkExpr(x.Operand)
operandType := exprType(operandIR)
if operandType != nil && operandType.Kind == ir.TypeRef {
    // Auto-deref through Select. The IR carries an explicit Unary{Deref}
    // so downstream passes never need a special case.
    operandIR = &ir.Unary{
        AST:     nil, // synthesized — no source position
        Type:    operandType.Elem,
        Op:      ast.UnaryDeref,
        Operand: operandIR,
    }
    operandType = operandType.Elem
}
// ...continue with the existing field-resolution logic against operandType...
```

- [ ] **Step 3: Add a positive test**

```go
func TestCheckAutoDerefSelect(t *testing.T) {
    src := `
struct Node { value: int }
component main {
    var n: Node = Node{value: 0}
    var p: ref<Node> = &n
    var v: int = p.value      // auto-deref: p.value is n.value
    func bump() { p.value = p.value + 1 }
}`
    pkg := mustCheckPackage(t, src)
    if len(pkg.Diagnostics) != 0 {
        t.Fatalf("unexpected diagnostics: %v", pkg.Diagnostics)
    }
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/checker/ -v -run TestCheckAutoDerefSelect`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/checker/expr.go internal/checker/expr_test.go
git commit -m "checker: auto-deref ref<T> through Select"
```

---

### Task 11: Optimizer — don't fold across `Deref` / `Addr`

**Files:**
- Modify: `internal/optimize/fold.go`

- [ ] **Step 1: Find the unary-fold case**

Search for `UnaryNot` in `internal/optimize/fold.go` — the const-folding switch over `*ir.Unary`.

- [ ] **Step 2: Add early-return for the new ops**

Before the existing arms:

```go
case ast.UnaryAddr, ast.UnaryDeref:
    // Reference operations are never const — the underlying storage may
    // be mutated through aliases. Leave the expression untouched.
    return e
```

- [ ] **Step 3: Add a regression test**

Create `internal/optimize/fold_ref_test.go` (or extend the existing fold_test.go):

```go
func TestFoldDoesNotCollapseDerefOfAddrOfConst(t *testing.T) {
    src := `
component main {
    const k: int = 5
    var p: ref<int> = &k
    var v: int = *p
}`
    pkg := optimizePackage(t, src)
    // *p should remain a *ir.Unary, NOT be replaced by Literal{5}.
    requireExprShape(t, pkg, "main.v.Init", "*ir.Unary")
}
```

(Helper names match whatever this package already uses; if the test framework around `optimize` differs, mirror the existing pattern in `fold_test.go`.)

- [ ] **Step 4: Run**

Run: `go test ./internal/optimize/ -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/optimize/fold.go internal/optimize/fold_ref_test.go
git commit -m "optimize: do not fold across UnaryDeref / UnaryAddr"
```

---

### Task 12: Lower walker — descend into Closure

**Files:**
- Modify: `internal/lower/walk.go`

- [ ] **Step 1: Locate the expression walker dispatcher**

Open `internal/lower/walk.go`. Currently `walkPackage` only invokes `fns.expr` at leaf positions; per-pass implementations have their own descent. No central Expr walker exists.

This Phase A change is forward-compat only: when Phase B emits `*ir.Closure`, downstream passes (NoToggle, NoReactivity, NoTimer, NoDeclarative) need to see inside `Closure.State` (a StructLit) and `Closure.Func.Block` (a Stmt slice) when their walk reaches a Closure node.

The cheapest forward-compat fix: extend the per-pass `transformExpr` switches in `ternary.go` and `computed.go` (the two passes that recurse into expressions today) with a `*ir.Closure` arm that descends into `x.State` and treats `x.Func.Block` as a stmt slice.

- [ ] **Step 2: Add a `*ir.Closure` arm in `internal/lower/ternary.go` `transformExpr`**

In `transformExpr`, after the existing arms, add:

```go
case *ir.Closure:
    var pre []ir.Stmt
    if x.State != nil {
        for i := range x.State.Fields {
            if x.State.Fields[i].Value != nil {
                p, v := st.transformExpr(x.State.Fields[i].Value)
                pre = append(pre, p...)
                x.State.Fields[i].Value = v
            }
        }
    }
    if x.Func != nil {
        x.Func.Block = st.transformBlock(x.Func.Block)
    }
    return pre, x
```

- [ ] **Step 3: Add the same arm in `internal/lower/computed.go` `inlineComputedExpr`**

```go
case *ir.Closure:
    if x.State != nil {
        for i := range x.State.Fields {
            if x.State.Fields[i].Value != nil {
                x.State.Fields[i].Value = inlineComputedExpr(x.State.Fields[i].Value, bodies)
            }
        }
    }
    if x.Func != nil {
        x.Func.Block = inlineComputedStmts(x.Func.Block, rewrite)
    }
    return x
```

- [ ] **Step 4: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Run lower tests**

Run: `go test ./internal/lower/...`
Expected: passes — Closure is unreachable today, so no behavior change.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/ternary.go internal/lower/computed.go
git commit -m "lower: forward-compat Closure descent in ternary/computed walkers"
```

---

### Task 13: Full verify

**Files:** none

- [ ] **Step 1: Run full test suite**

Run: `go tool verify`
Expected: full pass. No existing fixtures should change behavior — Phase A only adds shapes that don't appear in shipping IR.

- [ ] **Step 2: Smoke-test dump on an existing example**

Run: `go install ./cmd/sngl && sngl dump optimized examples/counter.sngl --lang js --platform html`
Expected: identical output to before Phase A (TypeRef / Closure / new unary ops are not present in any existing program).

- [ ] **Step 3: Tag completion**

```bash
git tag -m "Phase A complete: surface + IR shapes for NoLambda" nolambda-phase-a
```

(Skip if local-tag policy says otherwise — confirm with user before pushing.)

---

## Phase A done. Phase B (the pass body, lifter, integration) is in `2026-05-04-nolambda-phase-b.md`.
