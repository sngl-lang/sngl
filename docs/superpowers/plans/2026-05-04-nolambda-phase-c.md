# NoLambda — Phase C (NoRef: ref-elimination via boxing) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `NoRef` lowering cap that eliminates every `ref<T>` shape from the IR by boxing each addressed binding into a synthesized one-field reference-semantic struct. Lets target languages that lack both closures and pointers (e.g. closure-free JS-flavored interpreters) consume lowered IR with `NoLambda + NoRef` enabled.

**Architecture:** Boxing pass runs immediately after `NoLambda` (slot 5.5 in the master pipeline). Pre-walk seeds `pkg.AddressedVars` from every `*ir.Unary{UnaryAddr}` operand. Each addressed Var has its declared type rewritten from `T` to `__ref_T` (a synthesized one-field struct), its initializer wrapped, every read replaced by `var.value`, every write by `var.value = ...`. `&v` becomes plain `v`; `*p` becomes `p.value`; `ref<T>` types become `__ref_T` struct types. Synthesized box struct definitions are deduped by element type (`__ref_int`, `__ref_string`, `__ref_<componentName>`, etc.).

**Tech Stack:** Go 1.24+, `ir`/`ast`/`internal/lower` from Phase A and Phase B.

**Reference spec:** `docs/superpowers/specs/2026-05-03-nolambda-design.md` (section "Companion pass: `NoRef`").
**Reference plans:** `docs/superpowers/plans/2026-05-04-nolambda-phase-a.md`, `2026-05-04-nolambda-phase-b.md` (must be merged first).

---

## File Structure

**Create:**
- `internal/lower/noref.go` — pass entry point, address-set collection, rewrite engine.
- `internal/lower/testdata/noref_basic.txtar` — boxed counter example.
- `internal/lower/testdata/noref_with_lambda.txtar` — composition `NoLambda + NoRef`.
- `internal/lower/testdata/noref_handler_lift.txtar` — composition `NoLambda + NoDeclarative + NoRef`.
- `internal/lower/noref_test.go` — unit tests for the address-set scan and box-name allocation.

**Modify:**
- `internal/lower/caps.go` — add `NoRef bool` field; update `String()`.
- `internal/lower/lower.go` — register `passNoRef` between `passLambda` and `passToggle` (slot 5.5).
- `ir/ir.go` — add `Package.AddressedVars map[*Var]bool` field.
- `internal/lower/lambda.go` — populate `pkg.AddressedVars` whenever `lifter.Lift` emits `&v`.

---

### Task 1: Caps + pass registration

**Files:**
- Modify: `internal/lower/caps.go`
- Modify: `internal/lower/lower.go`
- Create: `internal/lower/noref.go` (stub only)

- [ ] **Step 1: Add the cap field**

Edit `internal/lower/caps.go`. In the `Caps` struct, add:

```go
type Caps struct {
    // ...existing fields...
    NoRef bool // ref<T> → __ref_T struct boxing; & → identity; * → .value
}
```

In the `Merge` method, add the field-wise OR for `NoRef`. In `String()`, add the entry so it sorts alongside the other flags.

- [ ] **Step 2: Stub the pass**

Create `internal/lower/noref.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passNoRef = pass{
    name:    "NoRef",
    enabled: func(c Caps) bool { return c.NoRef },
    apply:   lowerNoRef,
}

// lowerNoRef boxes every addressed binding into a synthesized one-field
// reference-semantic struct. After this pass no *ir.TypeRef remains.
//
// Phase C — body lands in subsequent tasks.
func lowerNoRef(pkg *ir.Package) error {
    return nil
}
```

- [ ] **Step 3: Register in pipeline**

Edit `internal/lower/lower.go`. Insert `passNoRef` between `passLambda` and `passToggle`:

```go
var passes = []pass{
    passUnit,
    passEnum,
    passTernary,
    passComputed,
    passLambda,
    passNoRef,    // slot 5.5
    passToggle,
    passReactivity,
    passTimer,
    passDeclarative,
}
```

Update the comment block above `passes` to document the new slot.

- [ ] **Step 4: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Tests pass with cap off**

Run: `go test ./internal/lower/`
Expected: every existing fixture still passes (NoRef cap defaults to false; the stub is a no-op).

- [ ] **Step 6: Commit**

```bash
git add internal/lower/caps.go internal/lower/lower.go internal/lower/noref.go
git commit -m "lower: register NoRef cap and pass stub"
```

---

### Task 2: pkg.AddressedVars field

**Files:**
- Modify: `ir/ir.go`
- Modify: `internal/lower/lambda.go`

- [ ] **Step 1: Add the field**

Edit `ir/ir.go`. In the `Package` struct, add:

```go
// AddressedVars records every Var whose address is taken anywhere in the
// package — by user code via `&v`, or by NoLambda's lifter when emitting
// mutable-capture init. NoRef reads this set to decide which Vars to box.
AddressedVars map[*Var]bool
```

- [ ] **Step 2: Initialize at construction**

Find every `&ir.Package{...}` literal and add `AddressedVars: map[*ir.Var]bool{}`.

- [ ] **Step 3: Have lifter record addresses**

Edit `internal/lower/lambda.go`. In `Lift`, where the State StructLit's mutable-capture init builds an `&Ident{outerN}` shape (the `c.Mutable` branch), record the address:

```go
if c.Mutable {
    if v, ok := c.Sym.(*ir.Var); ok {
        if l.pkg.AddressedVars == nil {
            l.pkg.AddressedVars = map[*ir.Var]bool{}
        }
        l.pkg.AddressedVars[v] = true
    }
    fieldValue = &ir.Unary{
        Op:      ast.UnaryAddr,
        Operand: ident,
        Type:    ir.RefOf(c.Sym.SymType()),
    }
}
```

(*Param* captures don't have a corresponding Var; they cannot be addressed across function boundaries in current SNGL semantics, so the `*ir.Var` check is correct.)

- [ ] **Step 4: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 5: Confirm existing NoLambda goldens still pass**

Run: `go test ./internal/lower/ -run TestLowerGolden`
Expected: pass — the `AddressedVars` map is populated but no consumer reads it yet.

- [ ] **Step 6: Commit**

```bash
git add ir/ir.go internal/lower/lambda.go
git commit -m "ir: track AddressedVars; NoLambda lifter populates it"
```

---

### Task 3: Pre-walk to seed AddressedVars

**Files:**
- Modify: `internal/lower/noref.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lower/noref_test.go`:

```go
package lower

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/ast"
    "git.duckfam.us/jonathan/sngl/ir"
)

func TestSeedAddressedVarsFromUnaryAddr(t *testing.T) {
    pkg := &ir.Package{
        AddressedVars: map[*ir.Var]bool{},
    }
    n := &ir.Var{Name: "n", Type: ir.TypInt}
    pkg.Vars = []*ir.Var{n}

    handler := &ir.Func{
        Block: []ir.Stmt{
            &ir.LocalVar{
                Name: "p",
                Type: ir.RefOf(ir.TypInt),
                Init: &ir.Unary{
                    Op:      ast.UnaryAddr,
                    Operand: &ir.Ident{Name: "n", Sym: n, Type: ir.TypInt},
                    Type:    ir.RefOf(ir.TypInt),
                },
            },
        },
    }
    pkg.Funcs = []*ir.Func{handler}

    seedAddressedVars(pkg)

    if !pkg.AddressedVars[n] {
        t.Errorf("n should be marked addressed")
    }
}
```

Run: `go test ./internal/lower/ -run TestSeedAddressedVars -v`
Expected: FAIL (`seedAddressedVars` undefined).

- [ ] **Step 2: Implement the seed walker**

Edit `internal/lower/noref.go`. Add:

```go
import (
    "git.duckfam.us/jonathan/sngl/ast"
)

// seedAddressedVars walks pkg recording every *ir.Var whose address is
// taken by *ir.Unary{UnaryAddr}. Idempotent — entries set by NoLambda's
// lifter persist; this pass adds any Vars addressed by hand-written code.
func seedAddressedVars(pkg *ir.Package) {
    if pkg.AddressedVars == nil {
        pkg.AddressedVars = map[*ir.Var]bool{}
    }
    walkPackage(pkg, walkFuncs{
        expr: func(e ir.Expr) ir.Expr {
            seedAddressedVarsInExpr(e, pkg.AddressedVars)
            return e
        },
        stmts: func(stmts []ir.Stmt) []ir.Stmt {
            for _, s := range stmts {
                seedAddressedVarsInStmt(s, pkg.AddressedVars)
            }
            return stmts
        },
    })
}

func seedAddressedVarsInExpr(e ir.Expr, set map[*ir.Var]bool) {
    if e == nil {
        return
    }
    switch x := e.(type) {
    case *ir.Unary:
        if x.Op == ast.UnaryAddr {
            if leaf := addressLeafVar(x.Operand); leaf != nil {
                set[leaf] = true
            }
        }
        seedAddressedVarsInExpr(x.Operand, set)
    case *ir.Binary:
        seedAddressedVarsInExpr(x.Left, set)
        seedAddressedVarsInExpr(x.Right, set)
    case *ir.Ternary:
        seedAddressedVarsInExpr(x.Cond, set)
        seedAddressedVarsInExpr(x.Then, set)
        seedAddressedVarsInExpr(x.Else, set)
    case *ir.Call:
        seedAddressedVarsInExpr(x.Receiver, set)
        for i := range x.Args {
            seedAddressedVarsInExpr(x.Args[i].Value, set)
        }
    case *ir.Conversion:
        seedAddressedVarsInExpr(x.Operand, set)
    case *ir.Select:
        seedAddressedVarsInExpr(x.Operand, set)
    case *ir.Index:
        seedAddressedVarsInExpr(x.Operand, set)
        seedAddressedVarsInExpr(x.Idx, set)
    case *ir.ListLit:
        for _, el := range x.Elems {
            seedAddressedVarsInExpr(el, set)
        }
    case *ir.StructLit:
        for i := range x.Fields {
            seedAddressedVarsInExpr(x.Fields[i].Value, set)
        }
    case *ir.Spread:
        seedAddressedVarsInExpr(x.Operand, set)
    case *ir.Closure:
        if x.State != nil {
            for i := range x.State.Fields {
                seedAddressedVarsInExpr(x.State.Fields[i].Value, set)
            }
        }
        if x.Func != nil {
            for _, s := range x.Func.Block {
                seedAddressedVarsInStmt(s, set)
            }
        }
    }
}

func seedAddressedVarsInStmt(s ir.Stmt, set map[*ir.Var]bool) {
    switch n := s.(type) {
    case *ir.Assign:
        seedAddressedVarsInExpr(n.Target, set)
        seedAddressedVarsInExpr(n.Value, set)
    case *ir.LocalVar:
        seedAddressedVarsInExpr(n.Init, set)
    case *ir.Return:
        seedAddressedVarsInExpr(n.Value, set)
    case *ir.If:
        seedAddressedVarsInExpr(n.Cond, set)
        for _, t := range n.Body {
            seedAddressedVarsInStmt(t, set)
        }
        for _, t := range n.Else {
            seedAddressedVarsInStmt(t, set)
        }
    case *ir.For:
        seedAddressedVarsInExpr(n.Iter, set)
        for _, t := range n.Body {
            seedAddressedVarsInStmt(t, set)
        }
        for _, t := range n.Else {
            seedAddressedVarsInStmt(t, set)
        }
    case *ir.PlatformFilter:
        for _, t := range n.Body {
            seedAddressedVarsInStmt(t, set)
        }
    case *ir.NodeInst:
        for i := range n.Props {
            seedAddressedVarsInExpr(n.Props[i].Value, set)
        }
        seedAddressedVarsInExpr(n.Key, set)
        seedAddressedVarsInExpr(n.Ref, set)
        for _, t := range n.Children {
            seedAddressedVarsInStmt(t, set)
        }
        for i := range n.Handlers {
            if n.Handlers[i].Func != nil {
                for _, t := range n.Handlers[i].Func.Block {
                    seedAddressedVarsInStmt(t, set)
                }
            }
        }
    case *ir.SlotInst:
        for _, t := range n.Children {
            seedAddressedVarsInStmt(t, set)
        }
    case *ir.ErrorBoundary:
        for _, t := range n.Children {
            seedAddressedVarsInStmt(t, set)
        }
        if n.Handler != nil && n.Handler.Func != nil {
            for _, t := range n.Handler.Func.Block {
                seedAddressedVarsInStmt(t, set)
            }
        }
    case *ir.Emit:
        for i := range n.Args {
            seedAddressedVarsInExpr(n.Args[i].Value, set)
        }
    case *ir.CallStmt:
        if n.Call != nil {
            seedAddressedVarsInExpr(n.Call, set)
        }
    case *ir.Toggle:
        seedAddressedVarsInExpr(n.Target, set)
    }
}

// addressLeafVar peels Select/Index off operand and returns the leaf *ir.Var
// (or nil if the chain doesn't terminate at one).
func addressLeafVar(e ir.Expr) *ir.Var {
    for {
        switch x := e.(type) {
        case *ir.Ident:
            if v, ok := x.Sym.(*ir.Var); ok {
                return v
            }
            return nil
        case *ir.Select:
            e = x.Operand
        case *ir.Index:
            e = x.Operand
        default:
            return nil
        }
    }
}
```

- [ ] **Step 3: Run the test**

Run: `go test ./internal/lower/ -run TestSeedAddressedVars -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/noref.go internal/lower/noref_test.go
git commit -m "lower: NoRef seedAddressedVars walker"
```

---

### Task 4: Box-struct synthesis

**Files:**
- Modify: `internal/lower/noref.go`

- [ ] **Step 1: Implement deterministic name + struct allocation**

Append to `noref.go`:

```go
// boxRegistry deduplicates synthesized box structs by element-type identity.
// Element types are compared via canonical type-name strings.
type boxRegistry struct {
    pkg     *ir.Package
    byElem  map[string]*ir.StructDef // elem-typename → box StructDef
}

func newBoxRegistry(pkg *ir.Package) *boxRegistry {
    return &boxRegistry{pkg: pkg, byElem: map[string]*ir.StructDef{}}
}

// boxFor returns the synthesized one-field struct definition that boxes elem.
// Reuses an existing __ref_<elem> if one was already synthesized this pass.
func (r *boxRegistry) boxFor(elem *ir.Type) *ir.StructDef {
    key := canonicalTypeName(elem)
    if def, ok := r.byElem[key]; ok {
        return def
    }
    def := &ir.StructDef{
        Name: "__ref_" + key,
        Fields: []*ir.StructField{
            {Name: "value", Type: elem},
        },
    }
    r.byElem[key] = def
    r.pkg.Structs = append(r.pkg.Structs, def)
    return def
}

// canonicalTypeName produces a stable, identifier-safe name for elem suitable
// as a suffix of __ref_. Collisions are avoided by structural-name encoding.
func canonicalTypeName(t *ir.Type) string {
    if t == nil {
        return "dyn"
    }
    switch t.Kind {
    case ir.TypeInt:
        return "int"
    case ir.TypeFloat:
        return "float"
    case ir.TypeString:
        return "string"
    case ir.TypeBool:
        return "bool"
    case ir.TypeStruct:
        if t.Decl != nil {
            return t.Decl.Name
        }
        return "anon_struct"
    case ir.TypeComponent:
        if t.Decl != nil {
            if c, ok := t.Decl.(interface{ SymName() string }); ok {
                return c.SymName()
            }
        }
        return "component"
    case ir.TypeList:
        return "list_" + canonicalTypeName(t.Elem)
    case ir.TypeOption:
        return "option_" + canonicalTypeName(t.Elem)
    case ir.TypeFunc:
        return "func"
    }
    return t.Kind.String()
}
```

- [ ] **Step 2: Add a unit test**

Append to `noref_test.go`:

```go
func TestBoxRegistryDedupesByElemType(t *testing.T) {
    pkg := &ir.Package{}
    r := newBoxRegistry(pkg)

    a := r.boxFor(ir.TypInt)
    b := r.boxFor(ir.TypInt)
    if a != b {
        t.Errorf("same elem type should share box def")
    }

    c := r.boxFor(ir.TypString)
    if c == a {
        t.Errorf("different elem types should not share box def")
    }

    if a.Name != "__ref_int" || c.Name != "__ref_string" {
        t.Errorf("wrong canonical names: %q %q", a.Name, c.Name)
    }

    if len(pkg.Structs) != 2 {
        t.Errorf("expected 2 box structs in pkg, got %d", len(pkg.Structs))
    }
}
```

- [ ] **Step 3: Run**

Run: `go test ./internal/lower/ -run TestBoxRegistry -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/noref.go internal/lower/noref_test.go
git commit -m "lower: NoRef box-struct registry with elem-type dedup"
```

---

### Task 5: Rewrite engine

**Files:**
- Modify: `internal/lower/noref.go`

- [ ] **Step 1: Replace lowerNoRef body**

In `noref.go`:

```go
func lowerNoRef(pkg *ir.Package) error {
    if pkg == nil {
        return nil
    }
    seedAddressedVars(pkg)
    if len(pkg.AddressedVars) == 0 {
        // Even with no addressed Vars, ref<T> can still appear from hand-written
        // code (e.g. caps fields produced by lifters whose target Var is gone).
        // Fall through to the type-rewrite walker.
    }

    reg := newBoxRegistry(pkg)
    rw := &refRewriter{pkg: pkg, reg: reg}

    // Step 1: rewrite addressed Var declarations and their initializers.
    for _, v := range collectAllVars(pkg) {
        if !pkg.AddressedVars[v] {
            continue
        }
        elem := v.Type
        boxDef := reg.boxFor(elem)
        boxType := &ir.Type{Kind: ir.TypeStruct, Decl: boxDef}
        oldInit := v.Init
        v.Type = boxType
        v.Init = &ir.StructLit{
            Type: boxType,
            Def:  boxDef,
            Fields: []ir.FieldInit{
                {Name: "value", Value: orNullLiteral(oldInit, elem)},
            },
        }
    }

    // Step 2: rewrite every Expr and Stmt — reads, writes, & and *, ref<T> types.
    walkPackage(pkg, walkFuncs{
        expr:  func(e ir.Expr) ir.Expr { return rw.rewriteExpr(e) },
        stmts: func(stmts []ir.Stmt) []ir.Stmt { rw.rewriteStmts(stmts); return stmts },
    })

    // Step 3: rewrite ref<T> appearances in struct fields and func params.
    for _, s := range pkg.Structs {
        for _, f := range s.Fields {
            f.Type = rw.rewriteType(f.Type)
        }
    }
    for _, f := range pkg.Funcs {
        for _, p := range f.Params {
            p.Type = rw.rewriteType(p.Type)
        }
        f.Return = rw.rewriteType(f.Return)
    }
    for _, comp := range pkg.Components {
        for _, f := range comp.Funcs {
            for _, p := range f.Params {
                p.Type = rw.rewriteType(p.Type)
            }
            f.Return = rw.rewriteType(f.Return)
        }
    }
    return nil
}

// orNullLiteral returns init if non-nil, else a null literal of elem's type.
func orNullLiteral(init ir.Expr, elem *ir.Type) ir.Expr {
    if init != nil {
        return init
    }
    return &ir.Literal{Type: elem, Raw: "null"}
}

// collectAllVars returns every *ir.Var declared in pkg (top-level, components,
// windows). Local vars inside func bodies are handled by the rewriteStmt walker.
func collectAllVars(pkg *ir.Package) []*ir.Var {
    var out []*ir.Var
    out = append(out, pkg.Vars...)
    for _, c := range pkg.Components {
        out = append(out, c.Vars...)
    }
    for _, w := range pkg.Windows {
        out = append(out, w.Vars...)
    }
    return out
}
```

- [ ] **Step 2: Implement refRewriter**

Append:

```go
type refRewriter struct {
    pkg *ir.Package
    reg *boxRegistry
}

// rewriteType replaces ref<T> with the corresponding __ref_T struct type.
// Recurses into Elem so nested types (list<ref<int>>) are handled.
func (r *refRewriter) rewriteType(t *ir.Type) *ir.Type {
    if t == nil {
        return nil
    }
    if t.Kind == ir.TypeRef {
        boxDef := r.reg.boxFor(r.rewriteType(t.Elem))
        return &ir.Type{Kind: ir.TypeStruct, Decl: boxDef}
    }
    if t.Elem != nil {
        return &ir.Type{Kind: t.Kind, Elem: r.rewriteType(t.Elem), Sig: t.Sig, Decl: t.Decl, Meta: t.Meta}
    }
    return t
}

func (r *refRewriter) rewriteExpr(e ir.Expr) ir.Expr {
    if e == nil {
        return nil
    }
    switch x := e.(type) {
    case *ir.Unary:
        switch x.Op {
        case ast.UnaryAddr:
            // &v becomes plain v (the box is the reference).
            return r.rewriteExpr(x.Operand)
        case ast.UnaryDeref:
            // *p becomes p.value.
            inner := r.rewriteExpr(x.Operand)
            return &ir.Select{
                Operand: inner,
                Field:   "value",
                Type:    x.Type,
            }
        }
        x.Operand = r.rewriteExpr(x.Operand)
        return x
    case *ir.Ident:
        if v, ok := x.Sym.(*ir.Var); ok && r.pkg.AddressedVars[v] {
            // Reads of an addressed Var go through .value.
            return &ir.Select{
                Operand: x,
                Field:   "value",
                Type:    x.Type, // original elem type; the Ident's Type now points at the box, but the read result is elem
            }
        }
        return x
    case *ir.Binary:
        x.Left = r.rewriteExpr(x.Left)
        x.Right = r.rewriteExpr(x.Right)
        return x
    case *ir.Ternary:
        x.Cond = r.rewriteExpr(x.Cond)
        x.Then = r.rewriteExpr(x.Then)
        x.Else = r.rewriteExpr(x.Else)
        return x
    case *ir.Call:
        x.Receiver = r.rewriteExpr(x.Receiver)
        for i := range x.Args {
            x.Args[i].Value = r.rewriteExpr(x.Args[i].Value)
        }
        return x
    case *ir.Conversion:
        x.Operand = r.rewriteExpr(x.Operand)
        return x
    case *ir.Select:
        x.Operand = r.rewriteExpr(x.Operand)
        return x
    case *ir.Index:
        x.Operand = r.rewriteExpr(x.Operand)
        x.Idx = r.rewriteExpr(x.Idx)
        return x
    case *ir.ListLit:
        for i := range x.Elems {
            x.Elems[i] = r.rewriteExpr(x.Elems[i])
        }
        return x
    case *ir.StructLit:
        for i := range x.Fields {
            x.Fields[i].Value = r.rewriteExpr(x.Fields[i].Value)
        }
        if x.Type != nil && x.Type.Kind == ir.TypeRef {
            x.Type = r.rewriteType(x.Type)
        }
        return x
    case *ir.Spread:
        x.Operand = r.rewriteExpr(x.Operand)
        return x
    case *ir.Closure:
        if x.State != nil {
            for i := range x.State.Fields {
                x.State.Fields[i].Value = r.rewriteExpr(x.State.Fields[i].Value)
            }
            if x.State.Type != nil {
                x.State.Type = r.rewriteType(x.State.Type)
            }
        }
        if x.Func != nil {
            for _, p := range x.Func.Params {
                p.Type = r.rewriteType(p.Type)
            }
            x.Func.Return = r.rewriteType(x.Func.Return)
            r.rewriteStmts(x.Func.Block)
        }
        if x.Type != nil {
            x.Type = r.rewriteType(x.Type)
        }
        return x
    }
    return e
}

func (r *refRewriter) rewriteStmts(stmts []ir.Stmt) {
    for _, s := range stmts {
        r.rewriteStmt(s)
    }
}

func (r *refRewriter) rewriteStmt(s ir.Stmt) {
    switch n := s.(type) {
    case *ir.Assign:
        n.Target = r.rewriteAssignTarget(n.Target)
        n.Value = r.rewriteExpr(n.Value)
    case *ir.LocalVar:
        n.Init = r.rewriteExpr(n.Init)
        n.Type = r.rewriteType(n.Type)
    case *ir.Return:
        n.Value = r.rewriteExpr(n.Value)
    case *ir.If:
        n.Cond = r.rewriteExpr(n.Cond)
        r.rewriteStmts(n.Body)
        r.rewriteStmts(n.Else)
    case *ir.For:
        n.Iter = r.rewriteExpr(n.Iter)
        n.ElemType = r.rewriteType(n.ElemType)
        r.rewriteStmts(n.Body)
        r.rewriteStmts(n.Else)
    case *ir.PlatformFilter:
        r.rewriteStmts(n.Body)
    case *ir.NodeInst:
        for i := range n.Props {
            n.Props[i].Value = r.rewriteExpr(n.Props[i].Value)
        }
        n.Key = r.rewriteExpr(n.Key)
        n.Ref = r.rewriteExpr(n.Ref)
        r.rewriteStmts(n.Children)
        for i := range n.Handlers {
            if n.Handlers[i].Func != nil {
                r.rewriteStmts(n.Handlers[i].Func.Block)
            }
        }
    case *ir.SlotInst:
        r.rewriteStmts(n.Children)
    case *ir.ErrorBoundary:
        r.rewriteStmts(n.Children)
        if n.Handler != nil && n.Handler.Func != nil {
            r.rewriteStmts(n.Handler.Func.Block)
        }
    case *ir.Emit:
        for i := range n.Args {
            n.Args[i].Value = r.rewriteExpr(n.Args[i].Value)
        }
    case *ir.CallStmt:
        if n.Call != nil {
            n.Call = r.rewriteExpr(n.Call).(*ir.Call)
        }
    case *ir.Toggle:
        n.Target = r.rewriteAssignTarget(n.Target)
    }
}

// rewriteAssignTarget handles the LHS of an assignment. Idents to addressed
// Vars become Select{ident, "value"}; *p becomes p.value; otherwise normal
// rewriteExpr applies.
func (r *refRewriter) rewriteAssignTarget(t ir.Expr) ir.Expr {
    return r.rewriteExpr(t)
}
```

- [ ] **Step 3: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 4: Run all existing tests with cap off**

Run: `go test ./internal/lower/`
Expected: passes — NoRef is off for all goldens.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/noref.go
git commit -m "lower: NoRef rewrite engine — boxing, & elimination, * → .value"
```

---

### Task 6: Basic NoRef golden

**Files:**
- Create: `internal/lower/testdata/noref_basic.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoRef
-- input.sngl --
component main {
    var n: int = 0
    var p: ref<int> = &n
    func get() -> int => *p
    func set(x: int) { *p = x }
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- New struct `__ref_int { value: int }`.
- `var n: __ref_int = __ref_int{value: 0}`.
- `var p: __ref_int = n` (the `&n` collapses to plain `n`; the box itself is the reference).
- `func get() => p.value` (the `*p` becomes `.value`).
- `func set(x) { p.value = x }`.

- [ ] **Step 4: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/noref_basic.txtar
git commit -m "lower: NoRef golden — basic ref<T> elimination"
```

---

### Task 7: Composition with NoLambda

**Files:**
- Create: `internal/lower/testdata/noref_with_lambda.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoLambda, NoRef
-- input.sngl --
component main {
    var count: int = 0
    var inc: func() = () => { count = count + 1 }
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- New struct `__ref_int { value: int }`.
- New struct `__lambda0_caps { count: __ref_int }` — the field type, originally `ref<int>` after NoLambda, is now `__ref_int`.
- Lifted body: `state.count.value = state.count.value + 1` (no `*` deref, no `ref<T>`).
- Component's `count` is now `__ref_int{value: 0}`.
- StructLit init: `__lambda0_caps{count: count}` — the original `&count` is now plain `count` (the box itself is the ref).
- The `inc` var holds `__closure(__lambda0, __lambda0_caps{count: count})`.

- [ ] **Step 4: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/noref_with_lambda.txtar
git commit -m "lower: NoRef golden — composition with NoLambda"
```

---

### Task 8: Composition with handler lift

**Files:**
- Create: `internal/lower/testdata/noref_handler_lift.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoLambda, NoDeclarative, NoRef
-- input.sngl --
component counter {
    var count: int = 0
    button(@click {
        count = count + 1
    })
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- Component body uses `lower.createNode("button")`, `lower.attachHandler(...)`.
- `count` is `__ref_int{value: 0}`.
- The handler-lift produced `__lambda0_caps{count: __ref_int}`.
- Lifted body has `state.count.value = state.count.value + 1`.
- `lower.attachHandler(...)` third arg is `__closure(__lambda0, __lambda0_caps{count: count})`.

- [ ] **Step 4: Run full suite**

Run: `go tool verify`
Expected: full suite passes.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/noref_handler_lift.txtar
git commit -m "lower: NoRef golden — composition with NoLambda + NoDeclarative"
```

---

### Task 9: Final verification + tag

**Files:** none

- [ ] **Step 1: Verify post-pass invariant**

Add to `noref.go`'s `lowerNoRef` (just before the final `return nil`):

```go
if err := assertNoRefSurvives(pkg); err != nil {
    return err
}
```

Add the helper:

```go
func assertNoRefSurvives(pkg *ir.Package) error {
    var found bool
    var check func(t *ir.Type)
    check = func(t *ir.Type) {
        if t == nil {
            return
        }
        if t.Kind == ir.TypeRef {
            found = true
            return
        }
        check(t.Elem)
    }
    walkPackage(pkg, walkFuncs{
        expr: func(e ir.Expr) ir.Expr {
            if e != nil {
                check(e.ExprType())
            }
            return e
        },
    })
    for _, s := range pkg.Structs {
        for _, f := range s.Fields {
            check(f.Type)
        }
    }
    for _, f := range pkg.Funcs {
        for _, p := range f.Params {
            check(p.Type)
        }
        check(f.Return)
    }
    if found {
        return fmt.Errorf("lower: NoRef invariant violated — TypeRef survived the pass")
    }
    return nil
}
```

Add `import "fmt"`.

- [ ] **Step 2: Run final verify**

Run: `go tool verify`
Expected: pass.

- [ ] **Step 3: Commit invariant**

```bash
git add internal/lower/noref.go
git commit -m "lower: NoRef post-pass invariant — no TypeRef survives"
```

- [ ] **Step 4: Tag**

```bash
git tag -m "Phase C complete: NoRef boxing pass eliminates ref<T>" nolambda-phase-c
```

(Skip / coordinate with user if local-tag policy says otherwise.)

---

## Phase C complete

NoLambda's `ref<T>` output is now optionally lowerable to plain box-struct accesses. Targets without first-class refs that also lack closures (closure-free interpreters, sandboxed embeddings) opt into `NoLambda + NoRef` and consume IR with no `TypeRef` and no `*ir.Lambda` remaining.
