# NoLambda — Phase B (Pass Implementation + Integration) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `NoLambda` stub with a full pass that lifts every `*ir.Lambda` to a top-level `*ir.Func` plus a synthesized captured-state struct, emitting `*ir.Closure` at the original lambda position. Wire the lift routine into `NoDeclarative`'s handler-promote step. Wire `NoReactivity` to resolve mutations through lifted captures.

**Architecture:** Single `lifter` struct owns a package-scoped fresh-name counter and a stack of enclosing-scope frames (for nested closure capture sharing). One `Lift(body, params, ret, src) *ir.Closure` entry point used by both the main pass and `NoDeclarative`. Capture analysis walks the body's IR collecting `(Sym, mutable)` tuples in deterministic order. Lifted Funcs and synthesized structs are appended to `pkg.Funcs` / `pkg.Structs`. A new `pkg.LiftedCaptures` field maps lifted Func → (Sym → field-name) so `NoReactivity` can resolve a mutation site that targets `state.fieldName` back to the underlying captured Var.

**Tech Stack:** Go 1.24+, `ir`/`ast`/`internal/lower` from Phase A.

**Reference spec:** `docs/superpowers/specs/2026-05-03-nolambda-design.md`
**Reference plan:** `docs/superpowers/plans/2026-05-04-nolambda-phase-a.md` (must be merged first).

---

## File Structure

**Create:**
- `internal/lower/testdata/lambda_basic.txtar`
- `internal/lower/testdata/lambda_mutable_capture.txtar`
- `internal/lower/testdata/lambda_nested.txtar`
- `internal/lower/testdata/lambda_no_captures.txtar`
- `internal/lower/testdata/lambda_in_for.txtar`
- `internal/lower/testdata/lambda_handler_lift.txtar`
- `internal/lower/testdata/lambda_with_reactivity.txtar`

**Modify:**
- `ir/ir.go` — add `Package.LiftedCaptures` field.
- `internal/lower/lambda.go` — replace stub with full pass + exported `lifter`.
- `internal/lower/declarative.go` — call `lifter.Lift` from handler-promote step (only the integration point; the bulk of NoDeclarative lives in its own plan, Phase 3d).
- `internal/lower/reactivity.go` — consult `pkg.LiftedCaptures` when classifying assignment targets that go through `state.fieldName`.

---

### Task 1: pkg.LiftedCaptures field

**Files:**
- Modify: `ir/ir.go`

- [ ] **Step 1: Add the field**

Edit `ir/ir.go`. Locate the `Package` struct definition (search `type Package struct`). Add a field:

```go
type Package struct {
    // ...existing fields...

    // LiftedCaptures records, for every lifted closure Func produced by
    // NoLambda, the mapping from each captured Symbol to the synthesized
    // state-struct field name that aliases it. NoReactivity reads this
    // map to resolve `*state.fieldName` mutation sites back to the
    // underlying captured Var. Empty map (not nil) when no lifts have
    // happened.
    LiftedCaptures map[*Func]map[Symbol]string
}
```

- [ ] **Step 2: Initialize the field at package construction**

Find every `&Package{...}` literal in the codebase (likely a small number — checker package builder, parser package builder, test helpers). Initialize:

```go
&Package{
    // ...
    LiftedCaptures: map[*Func]map[Symbol]string{},
}
```

Run: `grep -rn "&ir.Package{" --include='*.go'` to find the construction sites.

- [ ] **Step 3: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 4: Commit**

```bash
git add ir/ir.go internal/checker internal/parser internal/lower
git commit -m "ir: add Package.LiftedCaptures map"
```

(Adjust the `git add` paths to match the actual files touched in Step 2.)

---

### Task 2: Capture analysis

**Files:**
- Modify: `internal/lower/lambda.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lower/lambda_test.go` (alongside the existing `golden_test.go`):

```go
package lower

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/ir"
)

func TestAnalyzeCapturesImmutableRead(t *testing.T) {
    // Lambda body reads outer `n` once; n is read-only inside the body.
    outerN := &ir.Var{Name: "n", Type: ir.TypInt}
    body := []ir.Stmt{
        &ir.Return{Value: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt}},
    }
    caps := analyzeCaptures(body, nil)
    if len(caps) != 1 {
        t.Fatalf("want 1 capture, got %d", len(caps))
    }
    if caps[0].Sym != outerN {
        t.Errorf("captured wrong sym")
    }
    if caps[0].Mutable {
        t.Errorf("read-only capture marked mutable")
    }
}

func TestAnalyzeCapturesMutableWrite(t *testing.T) {
    outerN := &ir.Var{Name: "n", Type: ir.TypInt}
    body := []ir.Stmt{
        &ir.Assign{
            Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
            Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
        },
    }
    caps := analyzeCaptures(body, nil)
    if len(caps) != 1 || !caps[0].Mutable {
        t.Errorf("expected mutable capture; got %+v", caps)
    }
}

func TestAnalyzeCapturesParamNotCaptured(t *testing.T) {
    p := &ir.Param{Name: "x", Type: ir.TypInt}
    body := []ir.Stmt{
        &ir.Return{Value: &ir.Ident{Name: "x", Sym: p, Type: ir.TypInt}},
    }
    caps := analyzeCaptures(body, []*ir.Param{p})
    if len(caps) != 0 {
        t.Errorf("param should not be captured: %+v", caps)
    }
}

func TestAnalyzeCapturesOrderStable(t *testing.T) {
    a := &ir.Var{Name: "a", Type: ir.TypInt}
    b := &ir.Var{Name: "b", Type: ir.TypInt}
    body := []ir.Stmt{
        &ir.Return{Value: &ir.Binary{
            Op:   0, // any
            Left:  &ir.Ident{Name: "a", Sym: a, Type: ir.TypInt},
            Right: &ir.Ident{Name: "b", Sym: b, Type: ir.TypInt},
            Type:  ir.TypInt,
        }},
    }
    caps := analyzeCaptures(body, nil)
    if len(caps) != 2 || caps[0].Sym != a || caps[1].Sym != b {
        t.Errorf("order unstable: %+v", caps)
    }
}
```

Run: `go test ./internal/lower/ -run TestAnalyzeCaptures -v`
Expected: FAIL (`analyzeCaptures` undefined).

- [ ] **Step 2: Implement analyzeCaptures**

Edit `internal/lower/lambda.go`. Replace the file's body (keeping the existing pass var) with:

```go
package lower

import (
    "git.duckfam.us/jonathan/sngl/ast"
    "git.duckfam.us/jonathan/sngl/ir"
)

var passLambda = pass{
    name:    "NoLambda",
    enabled: func(c Caps) bool { return c.NoLambda },
    apply:   lowerLambda,
}

// capture is one captured outer-scope binding referenced by a lambda body.
type capture struct {
    Sym     ir.Symbol // the outer Var or Param being captured
    Mutable bool      // true if the body assigns to Sym
}

// analyzeCaptures walks body collecting every Ident.Sym that resolves to a
// *Var or *Param not declared inside body's params. Mutability is set when
// the body contains an Assign whose Target chain bottoms out at the same Sym.
// Returned slice is in first-occurrence order (stable across runs).
func analyzeCaptures(body []ir.Stmt, params []*ir.Param) []capture {
    paramSet := make(map[ir.Symbol]bool, len(params))
    for _, p := range params {
        paramSet[p] = true
    }

    seen := make(map[ir.Symbol]int) // sym → index in result
    var caps []capture

    addRead := func(sym ir.Symbol) {
        if sym == nil || paramSet[sym] {
            return
        }
        switch sym.(type) {
        case *ir.Var, *ir.Param:
            // OK
        default:
            return
        }
        if _, ok := seen[sym]; ok {
            return
        }
        seen[sym] = len(caps)
        caps = append(caps, capture{Sym: sym})
    }

    markMutable := func(sym ir.Symbol) {
        if sym == nil {
            return
        }
        addRead(sym)
        if i, ok := seen[sym]; ok {
            caps[i].Mutable = true
        }
    }

    var walkExpr func(ir.Expr)
    var walkStmt func(ir.Stmt)
    var walkStmts func([]ir.Stmt)

    walkExpr = func(e ir.Expr) {
        switch x := e.(type) {
        case nil:
            return
        case *ir.Ident:
            addRead(x.Sym)
        case *ir.Binary:
            walkExpr(x.Left)
            walkExpr(x.Right)
        case *ir.Unary:
            walkExpr(x.Operand)
        case *ir.Ternary:
            walkExpr(x.Cond)
            walkExpr(x.Then)
            walkExpr(x.Else)
        case *ir.Call:
            walkExpr(x.Receiver)
            for i := range x.Args {
                walkExpr(x.Args[i].Value)
            }
        case *ir.Conversion:
            walkExpr(x.Operand)
        case *ir.Select:
            walkExpr(x.Operand)
        case *ir.Index:
            walkExpr(x.Operand)
            walkExpr(x.Idx)
        case *ir.ListLit:
            for _, el := range x.Elems {
                walkExpr(el)
            }
        case *ir.StructLit:
            for i := range x.Fields {
                walkExpr(x.Fields[i].Value)
            }
        case *ir.Spread:
            walkExpr(x.Operand)
        case *ir.Lambda:
            // Nested lambdas: their own captures are the responsibility of
            // their own Lift call. Do not descend (the outer lambda's
            // captures are only the Idents directly visible at this level).
            return
        case *ir.Closure:
            // Already-lifted; do not descend.
            return
        }
    }

    rootSym := func(e ir.Expr) ir.Symbol {
        for {
            switch x := e.(type) {
            case *ir.Ident:
                return x.Sym
            case *ir.Select:
                e = x.Operand
            case *ir.Index:
                e = x.Operand
            case *ir.Unary:
                if x.Op == ast.UnaryDeref {
                    e = x.Operand
                    continue
                }
                return nil
            default:
                return nil
            }
        }
    }

    walkStmt = func(s ir.Stmt) {
        switch n := s.(type) {
        case *ir.Assign:
            markMutable(rootSym(n.Target))
            walkExpr(n.Target)
            walkExpr(n.Value)
        case *ir.LocalVar:
            walkExpr(n.Init)
        case *ir.Return:
            walkExpr(n.Value)
        case *ir.If:
            walkExpr(n.Cond)
            walkStmts(n.Body)
            walkStmts(n.Else)
        case *ir.For:
            walkExpr(n.Iter)
            walkStmts(n.Body)
            walkStmts(n.Else)
        case *ir.PlatformFilter:
            walkStmts(n.Body)
        case *ir.NodeInst:
            for i := range n.Props {
                walkExpr(n.Props[i].Value)
            }
            walkExpr(n.Key)
            walkExpr(n.Ref)
            walkStmts(n.Children)
            for i := range n.Handlers {
                if n.Handlers[i].Func != nil {
                    walkStmts(n.Handlers[i].Func.Block)
                }
            }
        case *ir.SlotInst:
            walkStmts(n.Children)
        case *ir.ErrorBoundary:
            walkStmts(n.Children)
            if n.Handler != nil && n.Handler.Func != nil {
                walkStmts(n.Handler.Func.Block)
            }
        case *ir.Emit:
            for i := range n.Args {
                walkExpr(n.Args[i].Value)
            }
        case *ir.CallStmt:
            if n.Call != nil {
                walkExpr(n.Call)
            }
        case *ir.Toggle:
            markMutable(rootSym(n.Target))
            walkExpr(n.Target)
        case *ir.Window:
            walkExpr(n.Href)
            walkExpr(n.Title)
            walkExpr(n.Favicon)
            walkStmts(n.Body)
        }
    }

    walkStmts = func(stmts []ir.Stmt) {
        for _, s := range stmts {
            walkStmt(s)
        }
    }

    walkStmts(body)
    return caps
}

func lowerLambda(pkg *ir.Package) error { return nil } // placeholder, Task 4 replaces
```

- [ ] **Step 3: Run the test**

Run: `go test ./internal/lower/ -run TestAnalyzeCaptures -v`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/lambda.go internal/lower/lambda_test.go
git commit -m "lower: implement NoLambda capture analysis"
```

---

### Task 3: Lifter struct and Lift routine

**Files:**
- Modify: `internal/lower/lambda.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/lower/lambda_test.go`:

```go
func TestLifterBasicReadOnlyCapture(t *testing.T) {
    pkg := &ir.Package{
        LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
    }
    outerN := &ir.Var{Name: "n", Type: ir.TypInt}
    pkg.Vars = []*ir.Var{outerN}

    body := []ir.Stmt{
        &ir.Return{Value: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt}},
    }
    l := &lifter{pkg: pkg}
    cl := l.Lift(body, nil, ir.TypInt, nil)

    if cl == nil || cl.Func == nil || cl.State == nil {
        t.Fatalf("Lift returned bad shape: %+v", cl)
    }
    if len(cl.Func.Params) != 1 {
        t.Errorf("lifted Func should have one (state) param; got %d", len(cl.Func.Params))
    }
    if cl.State.Def == nil || len(cl.State.Def.Fields) != 1 {
        t.Errorf("state struct should have one field; got %+v", cl.State.Def)
    }
    field := cl.State.Def.Fields[0]
    if field.Type.Kind != ir.TypeInt {
        t.Errorf("read-only capture field should be int, got %v", field.Type.Kind)
    }
    if len(pkg.Funcs) != 1 || pkg.Funcs[0] != cl.Func {
        t.Errorf("lifted Func not appended to pkg.Funcs")
    }
    if len(pkg.Structs) != 1 || pkg.Structs[0] != cl.State.Def {
        t.Errorf("state struct not appended to pkg.Structs")
    }
    capMap := pkg.LiftedCaptures[cl.Func]
    if capMap[outerN] != "n" {
        t.Errorf("LiftedCaptures missing entry for outerN")
    }
}

func TestLifterMutableCaptureUsesRefType(t *testing.T) {
    pkg := &ir.Package{
        LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
    }
    outerN := &ir.Var{Name: "n", Type: ir.TypInt}
    pkg.Vars = []*ir.Var{outerN}

    body := []ir.Stmt{
        &ir.Assign{
            Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
            Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
        },
    }
    l := &lifter{pkg: pkg}
    cl := l.Lift(body, nil, nil, nil)

    field := cl.State.Def.Fields[0]
    if field.Type.Kind != ir.TypeRef {
        t.Errorf("mutable capture field should be ref<int>, got %v", field.Type.Kind)
    }
    if field.Type.Elem == nil || field.Type.Elem.Kind != ir.TypeInt {
        t.Errorf("ref elem should be int, got %v", field.Type.Elem)
    }
    // State init field value must be Unary{UnaryAddr, Ident{outerN}}.
    init := cl.State.Fields[0].Value
    u, ok := init.(*ir.Unary)
    if !ok || u.Op != ast.UnaryAddr {
        t.Errorf("mutable capture init should be &outerN; got %T", init)
    }
}

func TestLifterFreshNameCollision(t *testing.T) {
    // pkg already has a struct named __lambda0_caps; lifter must skip past it.
    pkg := &ir.Package{
        LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
        Structs: []*ir.StructDef{
            {Name: "__lambda0_caps"},
        },
    }
    body := []ir.Stmt{}
    l := &lifter{pkg: pkg}
    cl := l.Lift(body, nil, nil, nil)
    if cl.State.Def.Name == "__lambda0_caps" {
        t.Errorf("lifter did not skip existing name")
    }
}
```

Run: `go test ./internal/lower/ -run TestLifter -v`
Expected: FAIL (`lifter` undefined).

- [ ] **Step 2: Implement the lifter**

Replace the placeholder `lowerLambda` and add the lifter in `internal/lower/lambda.go`:

```go
// lifter owns NoLambda's package-scoped state. The same lifter is shared
// between the main pass and NoDeclarative's handler-promote step.
type lifter struct {
    pkg     *ir.Package
    counter int
    // enclosing tracks ancestor lift frames so a nested lambda capturing
    // the same Sym as its enclosing lambda re-uses the outer's ref-typed
    // state field instead of taking a fresh address.
    enclosing []scopeFrame
}

type scopeFrame struct {
    // captureField maps a captured outer Symbol to the AST expression that
    // accesses it from this frame's lifted body — typically Select{Ident{state}, fieldName}.
    captureField map[ir.Symbol]ir.Expr
}

// Lift converts a closure-shaped (body, params, return) into a top-level Func
// + caps StructDef and returns the *ir.Closure that replaces the original
// lambda expression.
func (l *lifter) Lift(body []ir.Stmt, params []*ir.Param, ret *ir.Type, src *ast.LambdaExpr) *ir.Closure {
    caps := analyzeCaptures(body, params)

    // Synthesize names.
    capsName, funcName := l.freshNames()

    // Build the state struct definition.
    capsDef := &ir.StructDef{Name: capsName}
    for _, c := range caps {
        fieldType := c.Sym.SymType()
        if c.Mutable {
            fieldType = ir.RefOf(fieldType)
        }
        capsDef.Fields = append(capsDef.Fields, &ir.StructField{
            Name: c.Sym.SymName(),
            Type: fieldType,
        })
    }
    l.pkg.Structs = append(l.pkg.Structs, capsDef)

    capsType := &ir.Type{Kind: ir.TypeStruct, Decl: capsDef}

    // Synthesize the state Param.
    stateParam := &ir.Param{Name: "state", Type: capsType}

    // Synthesize the lifted Func.
    lifted := &ir.Func{
        Name:   funcName,
        Params: append([]*ir.Param{stateParam}, params...),
        Return: ret,
    }

    // Build the rewrite map: captured Sym → expression accessing it inside
    // the lifted body. State is value-typed; mutable fields are ref<T>.
    captureField := make(map[ir.Symbol]ir.Expr, len(caps))
    for _, c := range caps {
        sel := &ir.Select{
            Operand: &ir.Ident{Name: "state", Sym: stateParam, Type: capsType},
            Field:   c.Sym.SymName(),
            Type:    c.Sym.SymType(),
        }
        var access ir.Expr = sel
        if c.Mutable {
            // Field type is ref<T>; reads/writes go through Unary{Deref}.
            sel.Type = ir.RefOf(c.Sym.SymType())
            access = &ir.Unary{
                Op:      ast.UnaryDeref,
                Operand: sel,
                Type:    c.Sym.SymType(),
            }
        }
        captureField[c.Sym] = access
    }

    // Push frame and rewrite body.
    l.enclosing = append(l.enclosing, scopeFrame{captureField: captureField})
    lifted.Block = l.rewriteStmts(body, captureField)
    l.enclosing = l.enclosing[:len(l.enclosing)-1]

    l.pkg.Funcs = append(l.pkg.Funcs, lifted)

    // Build the State StructLit at the original lambda position.
    state := &ir.StructLit{Type: capsType, Def: capsDef}
    for _, c := range caps {
        var fieldValue ir.Expr
        // If an enclosing frame already aliases this Sym, re-use its access
        // expression (already a ref<T> read or value access). Otherwise
        // synthesize a fresh Ident or & based on mutability.
        if outer := l.outerCaptureAccess(c.Sym); outer != nil {
            fieldValue = outer
        } else {
            ident := &ir.Ident{Name: c.Sym.SymName(), Sym: c.Sym, Type: c.Sym.SymType()}
            if c.Mutable {
                fieldValue = &ir.Unary{
                    Op:      ast.UnaryAddr,
                    Operand: ident,
                    Type:    ir.RefOf(c.Sym.SymType()),
                }
            } else {
                fieldValue = ident
            }
        }
        state.Fields = append(state.Fields, ir.FieldInit{
            Name:  c.Sym.SymName(),
            Value: fieldValue,
        })
    }

    // Record capture names for NoReactivity.
    capMap := make(map[ir.Symbol]string, len(caps))
    for _, c := range caps {
        capMap[c.Sym] = c.Sym.SymName()
    }
    if l.pkg.LiftedCaptures == nil {
        l.pkg.LiftedCaptures = map[*ir.Func]map[ir.Symbol]string{}
    }
    l.pkg.LiftedCaptures[lifted] = capMap

    // User-visible signature: original params + return only.
    userSig := &ir.FuncSig{Params: params, Return: ret}

    return &ir.Closure{
        AST:   src,
        Type:  &ir.Type{Kind: ir.TypeFunc, Sig: userSig},
        Func:  lifted,
        State: state,
    }
}

// outerCaptureAccess returns the access expression an enclosing frame
// already exposes for sym, or nil if no enclosing frame captures sym.
func (l *lifter) outerCaptureAccess(sym ir.Symbol) ir.Expr {
    for i := len(l.enclosing) - 1; i >= 0; i-- {
        if access, ok := l.enclosing[i].captureField[sym]; ok {
            return access
        }
    }
    return nil
}

// freshNames returns (capsName, funcName) skipping past any existing names
// in pkg.Structs / pkg.Funcs.
func (l *lifter) freshNames() (string, string) {
    for {
        capsName := "__lambda" + itoa(l.counter) + "_caps"
        funcName := "__lambda" + itoa(l.counter)
        l.counter++
        if !l.nameExists(capsName) && !l.nameExists(funcName) {
            return capsName, funcName
        }
    }
}

func (l *lifter) nameExists(name string) bool {
    for _, s := range l.pkg.Structs {
        if s.Name == name {
            return true
        }
    }
    for _, f := range l.pkg.Funcs {
        if f.Name == name {
            return true
        }
    }
    return false
}

func itoa(n int) string {
    return strconv.Itoa(n)
}
```

Add the import `"strconv"`.

- [ ] **Step 3: Implement rewriteStmts / rewriteExpr**

Append to `internal/lower/lambda.go`:

```go
// rewriteStmts replaces every Ident.Sym appearing in stmts that is in
// captureField with the field's access expression. It does not descend into
// nested *ir.Lambda nodes — those will be lifted by their own Lift call,
// which establishes its own frame and inherits the current frame via
// l.enclosing.
func (l *lifter) rewriteStmts(stmts []ir.Stmt, captureField map[ir.Symbol]ir.Expr) []ir.Stmt {
    for _, s := range stmts {
        l.rewriteStmt(s, captureField)
    }
    return stmts
}

func (l *lifter) rewriteStmt(s ir.Stmt, captureField map[ir.Symbol]ir.Expr) {
    switch n := s.(type) {
    case *ir.Assign:
        n.Target = l.rewriteExpr(n.Target, captureField)
        n.Value = l.rewriteExpr(n.Value, captureField)
    case *ir.LocalVar:
        n.Init = l.rewriteExpr(n.Init, captureField)
    case *ir.Return:
        n.Value = l.rewriteExpr(n.Value, captureField)
    case *ir.If:
        n.Cond = l.rewriteExpr(n.Cond, captureField)
        l.rewriteStmts(n.Body, captureField)
        l.rewriteStmts(n.Else, captureField)
    case *ir.For:
        n.Iter = l.rewriteExpr(n.Iter, captureField)
        l.rewriteStmts(n.Body, captureField)
        l.rewriteStmts(n.Else, captureField)
    case *ir.PlatformFilter:
        l.rewriteStmts(n.Body, captureField)
    case *ir.Emit:
        for i := range n.Args {
            n.Args[i].Value = l.rewriteExpr(n.Args[i].Value, captureField)
        }
    case *ir.CallStmt:
        if n.Call != nil {
            n.Call = l.rewriteExpr(n.Call, captureField).(*ir.Call)
        }
    case *ir.Toggle:
        n.Target = l.rewriteExpr(n.Target, captureField)
    case *ir.NodeInst:
        for i := range n.Props {
            n.Props[i].Value = l.rewriteExpr(n.Props[i].Value, captureField)
        }
        n.Key = l.rewriteExpr(n.Key, captureField)
        n.Ref = l.rewriteExpr(n.Ref, captureField)
        l.rewriteStmts(n.Children, captureField)
        for i := range n.Handlers {
            if n.Handlers[i].Func != nil {
                l.rewriteStmts(n.Handlers[i].Func.Block, captureField)
            }
        }
    case *ir.SlotInst:
        l.rewriteStmts(n.Children, captureField)
    case *ir.ErrorBoundary:
        l.rewriteStmts(n.Children, captureField)
        if n.Handler != nil && n.Handler.Func != nil {
            l.rewriteStmts(n.Handler.Func.Block, captureField)
        }
    }
}

func (l *lifter) rewriteExpr(e ir.Expr, captureField map[ir.Symbol]ir.Expr) ir.Expr {
    switch x := e.(type) {
    case nil:
        return nil
    case *ir.Ident:
        if x.Sym != nil {
            if access, ok := captureField[x.Sym]; ok {
                return cloneExpr(access)
            }
        }
        return x
    case *ir.Binary:
        x.Left = l.rewriteExpr(x.Left, captureField)
        x.Right = l.rewriteExpr(x.Right, captureField)
        return x
    case *ir.Unary:
        x.Operand = l.rewriteExpr(x.Operand, captureField)
        return x
    case *ir.Ternary:
        x.Cond = l.rewriteExpr(x.Cond, captureField)
        x.Then = l.rewriteExpr(x.Then, captureField)
        x.Else = l.rewriteExpr(x.Else, captureField)
        return x
    case *ir.Call:
        x.Receiver = l.rewriteExpr(x.Receiver, captureField)
        for i := range x.Args {
            x.Args[i].Value = l.rewriteExpr(x.Args[i].Value, captureField)
        }
        return x
    case *ir.Conversion:
        x.Operand = l.rewriteExpr(x.Operand, captureField)
        return x
    case *ir.Select:
        x.Operand = l.rewriteExpr(x.Operand, captureField)
        return x
    case *ir.Index:
        x.Operand = l.rewriteExpr(x.Operand, captureField)
        x.Idx = l.rewriteExpr(x.Idx, captureField)
        return x
    case *ir.ListLit:
        for i := range x.Elems {
            x.Elems[i] = l.rewriteExpr(x.Elems[i], captureField)
        }
        return x
    case *ir.StructLit:
        for i := range x.Fields {
            x.Fields[i].Value = l.rewriteExpr(x.Fields[i].Value, captureField)
        }
        return x
    case *ir.Spread:
        x.Operand = l.rewriteExpr(x.Operand, captureField)
        return x
    case *ir.Lambda:
        // Nested lambda inside this body. The outer pass's walker will
        // reach it as part of normal traversal (after the outer Lift
        // returns) and call Lift on it. Do not rewrite into the lambda
        // body here — that would conflate scope frames.
        return x
    case *ir.Closure:
        // Already lifted; do not descend.
        return x
    }
    return e
}

// cloneExpr returns a deep-enough copy of e so multiple insertions of an
// access expression do not alias mutations across sites. Only the spine
// matters; leaf Symbols are shared by design.
func cloneExpr(e ir.Expr) ir.Expr {
    switch x := e.(type) {
    case *ir.Ident:
        cp := *x
        return &cp
    case *ir.Select:
        cp := *x
        cp.Operand = cloneExpr(x.Operand)
        return &cp
    case *ir.Unary:
        cp := *x
        cp.Operand = cloneExpr(x.Operand)
        return &cp
    }
    return e
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/lower/ -run TestLifter -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/lambda.go internal/lower/lambda_test.go
git commit -m "lower: implement lifter.Lift for NoLambda"
```

---

### Task 4: NoLambda pass apply

**Files:**
- Modify: `internal/lower/lambda.go`

- [ ] **Step 1: Replace lowerLambda**

In `internal/lower/lambda.go`, replace the placeholder `lowerLambda` with:

```go
func lowerLambda(pkg *ir.Package) error {
    if pkg == nil {
        return nil
    }
    if pkg.LiftedCaptures == nil {
        pkg.LiftedCaptures = map[*ir.Func]map[ir.Symbol]string{}
    }

    l := &lifter{pkg: pkg}

    rewrite := func(e ir.Expr) ir.Expr {
        return liftLambdas(e, l)
    }

    walkPackage(pkg, walkFuncs{
        expr: rewrite,
        stmts: func(stmts []ir.Stmt) []ir.Stmt {
            return liftLambdasInStmts(stmts, l)
        },
    })

    if err := assertNoLambdaSurvives(pkg); err != nil {
        return err
    }
    return nil
}

// liftLambdas replaces every *ir.Lambda found in e (recursively) with a
// *ir.Closure produced by l.Lift.
func liftLambdas(e ir.Expr, l *lifter) ir.Expr {
    switch x := e.(type) {
    case nil:
        return nil
    case *ir.Lambda:
        // Recurse into the lambda's body first so nested lambdas are
        // lifted in inner-most order; their captures may reach outer
        // bindings via l.enclosing once Lift establishes its frame.
        x.Func.Block = liftLambdasInStmts(x.Func.Block, l)
        return l.Lift(x.Func.Block, x.Func.Params, x.Func.Return, x.AST)
    case *ir.Binary:
        x.Left = liftLambdas(x.Left, l)
        x.Right = liftLambdas(x.Right, l)
        return x
    case *ir.Unary:
        x.Operand = liftLambdas(x.Operand, l)
        return x
    case *ir.Ternary:
        x.Cond = liftLambdas(x.Cond, l)
        x.Then = liftLambdas(x.Then, l)
        x.Else = liftLambdas(x.Else, l)
        return x
    case *ir.Call:
        x.Receiver = liftLambdas(x.Receiver, l)
        for i := range x.Args {
            x.Args[i].Value = liftLambdas(x.Args[i].Value, l)
        }
        return x
    case *ir.Conversion:
        x.Operand = liftLambdas(x.Operand, l)
        return x
    case *ir.Select:
        x.Operand = liftLambdas(x.Operand, l)
        return x
    case *ir.Index:
        x.Operand = liftLambdas(x.Operand, l)
        x.Idx = liftLambdas(x.Idx, l)
        return x
    case *ir.ListLit:
        for i := range x.Elems {
            x.Elems[i] = liftLambdas(x.Elems[i], l)
        }
        return x
    case *ir.StructLit:
        for i := range x.Fields {
            x.Fields[i].Value = liftLambdas(x.Fields[i].Value, l)
        }
        return x
    case *ir.Spread:
        x.Operand = liftLambdas(x.Operand, l)
        return x
    }
    return e
}

func liftLambdasInStmts(stmts []ir.Stmt, l *lifter) []ir.Stmt {
    for _, s := range stmts {
        liftLambdasInStmt(s, l)
    }
    return stmts
}

func liftLambdasInStmt(s ir.Stmt, l *lifter) {
    switch n := s.(type) {
    case *ir.Assign:
        n.Target = liftLambdas(n.Target, l)
        n.Value = liftLambdas(n.Value, l)
    case *ir.LocalVar:
        n.Init = liftLambdas(n.Init, l)
    case *ir.Return:
        n.Value = liftLambdas(n.Value, l)
    case *ir.If:
        n.Cond = liftLambdas(n.Cond, l)
        liftLambdasInStmts(n.Body, l)
        liftLambdasInStmts(n.Else, l)
    case *ir.For:
        n.Iter = liftLambdas(n.Iter, l)
        liftLambdasInStmts(n.Body, l)
        liftLambdasInStmts(n.Else, l)
    case *ir.PlatformFilter:
        liftLambdasInStmts(n.Body, l)
    case *ir.Emit:
        for i := range n.Args {
            n.Args[i].Value = liftLambdas(n.Args[i].Value, l)
        }
    case *ir.CallStmt:
        if n.Call != nil {
            n.Call = liftLambdas(n.Call, l).(*ir.Call)
        }
    case *ir.Toggle:
        n.Target = liftLambdas(n.Target, l)
    case *ir.NodeInst:
        for i := range n.Props {
            n.Props[i].Value = liftLambdas(n.Props[i].Value, l)
        }
        n.Key = liftLambdas(n.Key, l)
        n.Ref = liftLambdas(n.Ref, l)
        liftLambdasInStmts(n.Children, l)
        for i := range n.Handlers {
            if n.Handlers[i].Func != nil {
                liftLambdasInStmts(n.Handlers[i].Func.Block, l)
            }
        }
    case *ir.SlotInst:
        liftLambdasInStmts(n.Children, l)
    case *ir.ErrorBoundary:
        liftLambdasInStmts(n.Children, l)
        if n.Handler != nil && n.Handler.Func != nil {
            liftLambdasInStmts(n.Handler.Func.Block, l)
        }
    }
}

// assertNoLambdaSurvives walks pkg verifying that no *ir.Lambda remains.
// Cheap (one extra walk); failure here means the pass missed a position.
func assertNoLambdaSurvives(pkg *ir.Package) error {
    var found bool
    walkPackage(pkg, walkFuncs{
        expr: func(e ir.Expr) ir.Expr {
            if _, ok := e.(*ir.Lambda); ok {
                found = true
            }
            return e
        },
    })
    if found {
        return fmt.Errorf("lower: NoLambda invariant violated — *ir.Lambda survived the pass")
    }
    return nil
}
```

Add import `"fmt"` if not already present.

- [ ] **Step 2: Compile**

Run: `go build ./...`
Expected: passes.

- [ ] **Step 3: Run unit tests**

Run: `go test ./internal/lower/ -v`
Expected: all existing tests pass; lifter unit tests still pass.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/lambda.go
git commit -m "lower: wire NoLambda pass apply with post-pass invariant"
```

---

### Task 5: Per-pass goldens (basic, no captures)

**Files:**
- Create: `internal/lower/testdata/lambda_no_captures.txtar`
- Create: `internal/lower/testdata/lambda_basic.txtar`

- [ ] **Step 1: Write the no-captures fixture**

Create `internal/lower/testdata/lambda_no_captures.txtar`:

```
caps: NoLambda
-- input.sngl --
component main {
    var doubler: func(int) -> int = x => x * 2
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected output**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`
Expected: `expected.sngl` populated.

- [ ] **Step 3: Inspect**

Read the generated `expected.sngl`. Verify it contains:

- A struct decl named `__lambda0_caps` with no fields.
- A top-level func `__lambda0(state: __lambda0_caps, x: int) -> int` whose body returns `x * 2`.
- The component's var initializer is `__closure(__lambda0, __lambda0_caps{})`.

- [ ] **Step 4: Write the basic-capture fixture**

Create `internal/lower/testdata/lambda_basic.txtar`:

```
caps: NoLambda
-- input.sngl --
component main {
    var k: int = 5
    var addK: func(int) -> int = x => x + k
}
-- expected.sngl --
```

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 5: Inspect**

Verify:
- `__lambda0_caps` has one field `k: int` (immutable capture, no ref).
- Lifted body returns `x + state.k`.
- Closure init is `__closure(__lambda0, __lambda0_caps{k: k})`.

- [ ] **Step 6: Run final goldens to ensure stability**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass with no diffs.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/testdata/lambda_no_captures.txtar internal/lower/testdata/lambda_basic.txtar
git commit -m "lower: NoLambda goldens — basic and no-captures cases"
```

---

### Task 6: Mutable-capture golden

**Files:**
- Create: `internal/lower/testdata/lambda_mutable_capture.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoLambda
-- input.sngl --
component main {
    var n: int = 0
    var inc: func() = () => { n = n + 1 }
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- Field `n: ref<int>` (note the `ref<>`).
- Lifted body has `*state.n = *state.n + 1` (explicit deref).
- Closure init has `n: &n`.

- [ ] **Step 4: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/lambda_mutable_capture.txtar
git commit -m "lower: NoLambda golden — mutable capture uses ref<T>"
```

---

### Task 7: Nested-closure golden

**Files:**
- Create: `internal/lower/testdata/lambda_nested.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoLambda
-- input.sngl --
component main {
    var n: int = 0
    var outer: func() -> func() = () => {
        return () => { n = n + 1 }
    }
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- Both inner and outer lifts produce `__lambda0` and `__lambda1` (or similar).
- Outer's caps struct field for `n` is `ref<int>` (because the inner mutates n, the outer must propagate that ref).
- Inner's caps init expression for `n` re-uses the outer's `state.n` (already a `ref<int>`), NOT `&state.n` — verifies the enclosing-frame lookup.

- [ ] **Step 4: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/lambda_nested.txtar
git commit -m "lower: NoLambda golden — nested closures share ref"
```

---

### Task 8: Lambda-in-for golden

**Files:**
- Create: `internal/lower/testdata/lambda_in_for.txtar`

- [ ] **Step 1: Write fixture**

```
caps: NoLambda
-- input.sngl --
component main {
    var sum: int = 0
    func run() {
        for x in [1, 2, 3] {
            var f: func() = () => { sum = sum + x }
            f()
        }
    }
}
-- expected.sngl --
```

- [ ] **Step 2: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 3: Inspect**

Verify:
- The lifted Func captures both `sum` (mutable) and `x` (the for-loop variable).
- Loop var x is captured by **value** (LoopVar is a Param-like Sym, not a Var; mutations in the lambda body do not write back to the loop variable).
- Each iteration constructs a fresh `__lambda0_caps` with the current x snapshot.

- [ ] **Step 4: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/lambda_in_for.txtar
git commit -m "lower: NoLambda golden — lambda inside for-loop"
```

---

### Task 9: NoReactivity integration

**Files:**
- Modify: `internal/lower/reactivity.go`

The dataflow analysis classifies each `*ir.Assign` site by its target. After NoLambda runs (step 5, before NoReactivity at step 7), some Assigns in lifted bodies have a target like `*state.n` — i.e., `Unary{Deref, Select{Ident{state}, "n"}}`. The underlying mutated Var is the captured `n`, recorded in `pkg.LiftedCaptures[liftedFunc][nSym] = "n"`.

- [ ] **Step 1: Locate the assignment-target classifier**

Search `internal/lower/reactivity.go` for where it inspects `*ir.Assign.Target` to determine which Var was mutated. The function likely walks down through `Select` chains to find the leaf `*ir.Ident`.

- [ ] **Step 2: Add lifted-capture awareness**

When the target's leaf `*ir.Ident.Sym` resolves to a `*ir.Param` whose containing Func has an entry in `pkg.LiftedCaptures`, AND the access path matches `*state.fieldName` (or `state.fieldName` for read-only — though read-only captures cannot be mutation targets, so only the deref form), look up the original captured Sym via:

```go
// inside the classifier:
if target is *ir.Unary{Op: UnaryDeref, Operand: *ir.Select{Operand: *ir.Ident{Sym: stateParam}, Field: fieldName}} {
    for liftedFunc, capMap := range pkg.LiftedCaptures {
        if liftedFunc.Params[0] == stateParam {
            for sym, name := range capMap {
                if name == fieldName {
                    return sym // the underlying captured Var
                }
            }
        }
    }
}
```

(Adjust to match the existing classifier's API and helper conventions.)

- [ ] **Step 3: Write the composition golden**

Create `internal/lower/testdata/lambda_with_reactivity.txtar`:

```
caps: NoLambda, NoReactivity
-- input.sngl --
component counter {
    var count: int = 0
    var inc: func() = () => { count = count + 1 }
    text(value="Count: " + string(count))
}
-- expected.sngl --
```

- [ ] **Step 4: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 5: Inspect**

Verify the generated output has, inside the lifted `__lambda0` body, BOTH the original assignment (`*state.count = *state.count + 1`) AND the NoReactivity-injected updater (`__nN.value = "Count: " + string(*state.count)` — where the updater also reads through state because it lives in the lifted body).

- [ ] **Step 6: Run goldens**

Run: `go test ./internal/lower/ -run TestLowerGolden -v`
Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/testdata/lambda_with_reactivity.txtar
git commit -m "lower: NoReactivity resolves mutations through lifted captures"
```

---

### Task 10: NoDeclarative handler-promote integration

**Files:**
- Modify: `internal/lower/declarative.go`

Per the spec: when NoDeclarative promotes an `EventHandler.Func` block to a top-level Func, free Idents resolving to component-scoped Vars must be lifted via the same `lifter.Lift` routine.

- [ ] **Step 1: Locate the handler-promote step**

Search `internal/lower/declarative.go` for the code that turns `NodeInst.Handlers[i]` into a top-level Func. It likely synthesizes a name and appends to `pkg.Funcs`.

- [ ] **Step 2: Replace the direct synthesis with a Lift call**

Where the handler's Func is currently appended verbatim, instead:

```go
// Before:
//   pkg.Funcs = append(pkg.Funcs, handler.Func)
//   attachCall := lower.attachHandler(__nM, "<event>", handler.Func)

// After:
l := &lifter{pkg: pkg}
closure := l.Lift(handler.Func.Block, handler.Func.Params, handler.Func.Return, nil)
attachCall := /* lower.attachHandler(__nM, "<event>", closure) */
```

The `lifter` instance should be a single one threaded through NoDeclarative's apply, not a fresh one per handler — keeping its `counter` monotonic across all lifts in this pass. Plumb through the pass's local state.

- [ ] **Step 3: Write the handler-lift composition golden**

Create `internal/lower/testdata/lambda_handler_lift.txtar`:

```
caps: NoLambda, NoDeclarative
-- input.sngl --
component counter {
    var count: int = 0
    button(@click {
        count = count + 1
    })
}
-- expected.sngl --
```

- [ ] **Step 4: Generate expected**

Run: `go test ./internal/lower/ -run TestLowerGolden -update`

- [ ] **Step 5: Inspect**

Verify:
- A struct decl `__lambda0_caps { count: ref<int> }`.
- A top-level func `__lambda0(state: __lambda0_caps)` whose body has `*state.count = *state.count + 1`.
- The component body is fully imperative: `lower.createNode("button")`, `lower.attachHandler(__n0, "click", __closure(__lambda0, __lambda0_caps{count: &count}))`.

- [ ] **Step 6: Run goldens + full verify**

Run: `go test ./internal/lower/ -v`
Expected: pass.

Run: `go tool verify`
Expected: full suite passes.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/declarative.go internal/lower/testdata/lambda_handler_lift.txtar
git commit -m "lower: NoDeclarative lifts promoted handlers via shared lifter"
```

---

### Task 11: Final verification + tag

**Files:** none

- [ ] **Step 1: Run full suite**

Run: `go tool verify`
Expected: pass.

- [ ] **Step 2: Run dump-lowered regression**

For every `examples/*.sngl`, run `sngl dump lowered --format sngl --list` against the registered platform with NoLambda enabled (currently no platform turns it on; use a manual test invocation that sets caps explicitly via the `--list` form once the imperative platform lands). For now, smoke-run on each fixture:

```bash
for f in examples/*.sngl; do
    go run ./cmd/sngl dump lowered "$f" --lang js --platform html >/dev/null
done
```

Expected: all examples dump cleanly.

- [ ] **Step 3: Tag**

```bash
git tag -m "Phase B complete: NoLambda pass + lifter + NoReactivity/NoDeclarative integration" nolambda-phase-b
```

(Skip / coordinate with user if local-tag policy says otherwise.)

---

## NoLambda complete

- All 7 spec-listed goldens land.
- Phase 3d's handler-promote integration is wired.
- C codegen target (master-spec Phase 7) consumes lowered IR with `NoLambda + NoReactivity + NoDeclarative` all on.

Closes gitlab issue #40.
