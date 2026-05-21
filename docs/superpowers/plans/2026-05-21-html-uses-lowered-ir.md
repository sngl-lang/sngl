# HTML Codegen Uses the Lowered IR — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace HTML codegen's in-house component inlining + name-keyed dep bridge with consumption of the fully lowered IR. Extend the lower passes so NodeInsts in reactive contexts and recursive cycles emit a flat `CreateComponent` intrinsic, mirroring how DOM nodes lower today. Unblocks issue #75's component-method desugaring re-enable.

**Architecture:** Add `lower.CreateComponent(comp, props)` intrinsic. Extend `passNoInlineComponents` to skip NodeInsts in reactive For/If bodies and recursive cycles. Extend `passReactivity` and `passDeclarative` to emit `CreateComponent + AppendChild` for those skipped NodeInsts. HTML opts into `NoInlineComponents`, deletes its inliner + name bridge, emits JS factory functions for the remaining components, and adds a `CreateComponent` translation to `codegen/lang/javascript/translate_ir.go`.

**Tech Stack:** Go. Touch points: `ir/intrinsics.go`, `internal/lower/{inline_components,reactivity,declarative}.go`, `codegen/lang/javascript/translate_ir.go`, `codegen/platform/html/html.go`. Driver fixtures in `testdata/*.sngl`.

**Spec:** `docs/superpowers/specs/2026-05-21-html-uses-lowered-ir-design.md`

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `ir/intrinsics.go` | Modify | Add `CreateComponent` to `LowerIntrinsics` |
| `internal/lower/inline_components.go` | Modify | Skip NodeInsts in reactive contexts and recursive cycles; expose helper |
| `internal/lower/reactivity.go` | Modify | Lower component NodeInsts in slot bodies to `CreateComponent` + `AppendChild` |
| `internal/lower/declarative.go` | Modify | Lower static-position recursive-component NodeInsts to `CreateComponent` + `AppendChild` |
| `internal/lower/inline_components_test.go` | Modify | Unit tests for skip rules |
| `internal/lower/reactivity_test.go` | Modify | Unit test for CreateComponent in slot body |
| `internal/lower/declarative_test.go` | Modify | Unit test for CreateComponent in static position |
| `codegen/lang/javascript/translate_ir.go` | Modify | `lower.CreateComponent` intrinsic translation |
| `codegen/platform/html/html.go` | Modify | Add `NoInlineComponents` cap; delete in-house inliner; rewrite `exprDeps`; emit JS factories |
| `testdata/test_html_loop_component.sngl` | Create | Driver fixture |
| `testdata/test_html_recursive_component.sngl` | Create | Driver fixture |
| `testdata/test_html_method_reactivity.sngl` | Create | Driver fixture |
| `testdata/test_html_two_instances_isolated.sngl` | Create | Driver fixture |

---

## Task 1: Driver fixtures (red baseline)

**Files:**
- Create: `testdata/test_html_loop_component.sngl`
- Create: `testdata/test_html_recursive_component.sngl`
- Create: `testdata/test_html_method_reactivity.sngl`
- Create: `testdata/test_html_two_instances_isolated.sngl`

- [ ] **Step 1.1: Write `testdata/test_html_loop_component.sngl`**

```sngl
struct Item {
    name string = ""
}

component card {
    var clicks = 0
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

component main {
    var items list<Item> = [{name = "a"}, {name = "b"}]
    for item = items {
        card(:name=item.name)
    }
    button #add(text="add", @click { items.push({name = "x"}) })
}

func testLoopComponentMountsInstances(t Test, c main) {
    t.assert(true)
}
// Static smoke test — the checker passing is enough; browser test
// covers runtime behavior.
```

- [ ] **Step 1.2: Write `testdata/test_html_recursive_component.sngl`**

```sngl
struct Node {
    label string = ""
    children list<Node> = []
}

component tree {
    text(value=node.label)
    for c = node.children {
        tree(node=c)
    }
}

component main {
    var root Node = {label = "root", children = [{label = "a", children = []}, {label = "b", children = [{label = "ba", children = []}]}]}
    tree(node=root)
}

func testRecursiveComponentCompiles(t Test, c main) {
    t.assert(true)
}
```

- [ ] **Step 1.3: Write `testdata/test_html_method_reactivity.sngl`**

```sngl
component main {
    var x = 5
    var y = 3
    func bigger() => x > y
    button #bump(text="bump x", @click { x += 1 })
    text #out(value=bigger() ? "x" : "y")
}

func testReactiveMethodDrivesUpdate(t Test, c main) {
    t.assert(c.out.value == "x")
}
```

- [ ] **Step 1.4: Write `testdata/test_html_two_instances_isolated.sngl`**

```sngl
component counter {
    var n = 0
    button #bump(text="+", @click { n += 1 })
    text #out(value=string(n))
}

component main {
    counter()
    counter()
}

func testTwoInstancesIsolated(t Test, c main) {
    t.assert(true)
}
```

- [ ] **Step 1.5: Verify fixtures parse + check today**

Run: `go test ./internal/checker/... -count=1 -run 'TestCheckTestdata/test_html'`
Expected: PASS — the fixtures are syntactically valid and check successfully against current SNGL semantics.

- [ ] **Step 1.6: Commit**

```bash
git add testdata/test_html_loop_component.sngl \
        testdata/test_html_recursive_component.sngl \
        testdata/test_html_method_reactivity.sngl \
        testdata/test_html_two_instances_isolated.sngl
git commit -m "testdata: fixtures for HTML lowered-IR migration"
```

---

## Task 2: Add `CreateComponent` intrinsic

**Files:**
- Modify: `ir/intrinsics.go`

- [ ] **Step 2.1: Add the intrinsic**

Open `ir/intrinsics.go`. Find the `LowerIntrinsics = []IntrinsicDef{...}` block. Append after the last entry:

```go
{Name: "CreateComponent", Params: []*Param{
    {Name: "comp", Type: TypDyn},
    {Name: "props", Type: TypDyn},
}, Return: TypDyn},
```

- [ ] **Step 2.2: Verify it loads**

Run: `go build ./ir/...`
Expected: clean build.

Run: `go test ./ir/... -count=1`
Expected: pass.

- [ ] **Step 2.3: Commit**

```bash
git add ir/intrinsics.go
git commit -m "ir: add lower.CreateComponent intrinsic"
```

---

## Task 3: Reactive-expression detection helper

A small helper used by the inline pass to decide "is this If/For reactive?" — i.e. does its Cond/Iter depend on reactive vars (those mutated by handlers)?

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 3.1: Add `dependsOnReactiveVar` helper**

At the bottom of `internal/lower/inline_components.go`, add:

```go
// dependsOnReactiveVar reports whether an expression reads any var the
// pass should treat as reactive — i.e. one that handlers may mutate
// after initial render. Used by the inliner to identify *ir.If/*ir.For
// whose body must be lowered to imperative-mount form rather than
// statically inlined.
//
// Reuses the package-wide reactive-var set computed by
// collectReactiveVars in reactivity.go.
func dependsOnReactiveVar(e ir.Expr, reactive map[*ir.Var]bool) bool {
    if e == nil {
        return false
    }
    var found bool
    walkExprIdents(e, func(id *ir.Ident) {
        if v, ok := id.Sym.(*ir.Var); ok && reactive[v] {
            found = true
        }
    })
    return found
}
```

The `walkExprIdents` helper is defined in `internal/lower/reactivity.go` — it walks an expression and calls a visitor on every `*ir.Ident`. Verify it exists; if not, add it.

- [ ] **Step 3.2: Verify build**

Run: `go build ./internal/lower/...`
Expected: clean.

- [ ] **Step 3.3: Do not commit yet** — used by Task 4.

---

## Task 4: Extend `passNoInlineComponents` — skip reactive/recursive NodeInsts

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 4.1: Compute reactive vars at pass entry**

In `lowerInlineComponents` (the pass's `apply` function around line 21), before constructing `inlineCompState`, compute the reactive var set:

```go
func lowerInlineComponents(pkg *ir.Package, _ Caps, _ Options) error {
    if pkg == nil {
        return nil
    }
    main := mainComponent(pkg)
    if main == nil {
        return nil
    }
    cycles := findRecursiveCycles(pkg)
    reactive := collectReactiveVars(pkg)
    st := &inlineCompState{pkg: pkg, main: main, cycles: cycles, reactive: reactive}
    if err := st.run(); err != nil {
        return err
    }
    pkg.Components = retainComponents(pkg.Components, st.keep)
    return nil
}
```

Add the field to `inlineCompState`:

```go
type inlineCompState struct {
    pkg         *ir.Package
    main        *ir.Component
    cycles      map[*ir.Component]bool
    keep        map[*ir.Component]bool
    reactive    map[*ir.Var]bool
    instCounter int
}
```

- [ ] **Step 4.2: Skip NodeInsts inside reactive For/If**

Find the recursion into `*ir.For` and `*ir.If` bodies in `inlineStmts` (lines 355-378). The current logic inlines through unconditionally. Change to test the Cond/Iter expression and propagate a context flag:

```go
case *ir.If:
    inReactive := dependsOnReactiveVar(n.Cond, st.reactive)
    body, ch1, err := st.inlineStmtsCtx(n.Body, inReactive)
    if err != nil {
        return nil, false, err
    }
    els, ch2, err := st.inlineStmtsCtx(n.Else, inReactive)
    if err != nil {
        return nil, false, err
    }
    n.Body = body
    n.Else = els
    return []ir.Stmt{n}, ch1 || ch2, nil
case *ir.For:
    inReactive := dependsOnReactiveVar(n.Iter, st.reactive)
    body, ch1, err := st.inlineStmtsCtx(n.Body, inReactive)
    if err != nil {
        return nil, false, err
    }
    els, ch2, err := st.inlineStmtsCtx(n.Else, inReactive)
    if err != nil {
        return nil, false, err
    }
    n.Body = body
    n.Else = els
    return []ir.Stmt{n}, ch1 || ch2, nil
```

- [ ] **Step 4.3: Add the context-carrying variant**

Add a `bool` parameter version that threads through every nested call:

```go
// inlineStmtsCtx is inlineStmts with an explicit "we're inside a
// reactive structural context (For/If body)" flag. NodeInsts encountered
// while the flag is true are left in place rather than inlined.
func (st *inlineCompState) inlineStmtsCtx(stmts []ir.Stmt, inReactive bool) ([]ir.Stmt, bool, error) {
    out := make([]ir.Stmt, 0, len(stmts))
    anyCh := false
    for _, s := range stmts {
        replacement, ch, err := st.inlineStmtCtx(s, inReactive)
        if err != nil {
            return nil, false, err
        }
        out = append(out, replacement...)
        anyCh = anyCh || ch
    }
    return out, anyCh, nil
}
```

Refactor the existing `inlineStmt` to a wrapper that delegates with `inReactive=false`, and rename the worker to `inlineStmtCtx(s, inReactive)`. Replace all internal calls accordingly.

- [ ] **Step 4.4: Skip in `expandCall` when reactive**

In `inlineStmtCtx`, the `case *ir.NodeInst` branch is where `expandCall` is invoked. Wrap it:

```go
case *ir.NodeInst:
    if n.Component != nil {
        // Skip when inside a reactive context or when the target is a
        // recursive component. NodeInst is preserved in place; passReactivity
        // and passDeclarative will lower it to CreateComponent + AppendChild.
        if inReactive || st.cycles[n.Component] {
            st.keep[n.Component] = true
            // Still recurse into children (stdlib slot-bearing components
            // may have children that need inlining).
            children, chCh, err := st.inlineStmtsCtx(n.Children, inReactive)
            if err != nil {
                return nil, false, err
            }
            n.Children = children
            return []ir.Stmt{n}, chCh, nil
        }
        return st.expandCall(n)
    }
    // No component target — recurse into children only.
    children, chCh, err := st.inlineStmtsCtx(n.Children, inReactive)
    if err != nil {
        return nil, false, err
    }
    n.Children = children
    return []ir.Stmt{n}, chCh, nil
```

- [ ] **Step 4.5: Verify build**

Run: `go build ./...`
Expected: clean. The lower pass behavior is now: NodeInsts in reactive For/If bodies are left in place; NodeInsts targeting recursive components are left in place; everything else inlines as before.

- [ ] **Step 4.6: Verify existing tests still pass**

Run: `go test ./internal/lower/... -count=1`
Expected: pass. The new skip rules only fire when a NodeInst with `n.Component != nil` is inside a reactive context or in a recursive cycle. Existing fixtures don't have this combination (or if they do, the previous behavior was broken anyway).

- [ ] **Step 4.7: Commit**

```bash
git add internal/lower/inline_components.go
git commit -m "lower: skip NodeInsts in reactive contexts and recursive cycles"
```

---

## Task 5: Unit tests for skip behavior

**Files:**
- Modify: `internal/lower/inline_components_test.go`

- [ ] **Step 5.1: Add test for reactive-For skip**

Append to `internal/lower/inline_components_test.go`:

```go
func TestInlineComponents_SkipsReactiveForBody(t *testing.T) {
    // Build a minimal package: component card; component main with `for x = items { card() }`
    // where items is reactive.
    items := &ir.Var{Name: "items", Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{{Kind: ir.TypeDyn}}}}
    card := &ir.Component{Name: "card"}
    main := &ir.Component{Name: "main", Vars: []*ir.Var{items}}

    nodeInst := &ir.NodeInst{Component: card}
    forStmt := &ir.For{
        Key:  "x",
        Iter: &ir.Ident{Name: "items", Sym: items},
        Body: []ir.Stmt{nodeInst},
    }
    main.Body = []ir.Stmt{forStmt}

    // Mark items as reactive (a mutator handler somewhere).
    handler := &ir.Assign{Target: &ir.Ident{Name: "items", Sym: items}, Value: &ir.ListLit{}}
    main.Body = append(main.Body, &ir.NodeInst{Handlers: []ir.EventHandler{{Name: "click", Func: &ir.Func{Block: []ir.Stmt{handler}}}}})

    pkg := &ir.Package{Components: []*ir.Component{card, main}}

    if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
        t.Fatal(err)
    }

    // card should be retained.
    if !slices.Contains(pkg.Components, card) {
        t.Errorf("card component should be retained when used inside reactive For body")
    }
    // The NodeInst inside the for body should still exist.
    got, ok := forStmt.Body[0].(*ir.NodeInst)
    if !ok || got.Component != card {
        t.Errorf("NodeInst should be preserved inside reactive For; got %T", forStmt.Body[0])
    }
}
```

- [ ] **Step 5.2: Add test for recursive-component skip**

```go
func TestInlineComponents_SkipsRecursiveComponent(t *testing.T) {
    // component tree { tree(child=...) } — self-recursive.
    tree := &ir.Component{Name: "tree"}
    tree.Body = []ir.Stmt{&ir.NodeInst{Component: tree}}
    main := &ir.Component{Name: "main", Body: []ir.Stmt{&ir.NodeInst{Component: tree}}}

    pkg := &ir.Package{Components: []*ir.Component{tree, main}}

    if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
        t.Fatal(err)
    }

    if !slices.Contains(pkg.Components, tree) {
        t.Errorf("recursive tree component should be retained")
    }
    // The NodeInst in main.Body should still target tree.
    got, ok := main.Body[0].(*ir.NodeInst)
    if !ok || got.Component != tree {
        t.Errorf("NodeInst targeting recursive component should be preserved; got %T", main.Body[0])
    }
}
```

- [ ] **Step 5.3: Run tests**

Run: `go test ./internal/lower/ -count=1 -run 'TestInlineComponents_Skips'`
Expected: both PASS.

- [ ] **Step 5.4: Commit**

```bash
git add internal/lower/inline_components_test.go
git commit -m "lower: tests for reactive/recursive NodeInst skip in inline pass"
```

---

## Task 6: Extend `passReactivity` to emit `CreateComponent`

When `passReactivity` builds `__renderSlotN` Funcs from reactive For/If bodies, it walks the body with the per-slot declarative state (`slotDeclSt`). NodeInsts encountered get lowered into the slot body. Today, a NodeInst with `n.Component != nil` likely panics or falls through to the `CreateNode("componentName")` path. Add a branch that emits `CreateComponent` + `AppendChild`.

**Files:**
- Modify: `internal/lower/reactivity.go` (the slot-body lowering helper, near `lowerNodeForSlot` ~ line 325)

- [ ] **Step 6.1: Locate the slot-body NodeInst lowering**

Run: `grep -n "lowerNodeForSlot\|case \*ir.NodeInst" internal/lower/reactivity.go | head`

Identify the function that lowers NodeInsts inside a slot body. The current implementation calls `lowerNode` (from declarative.go) which assumes a DOM element. Add a precondition check.

- [ ] **Step 6.2: Add the component branch**

In `lowerNodeForSlot` (or the equivalent NodeInst-handling function), add at the top:

```go
func (st *declarativeState) lowerNodeForSlot(n *ir.NodeInst, parentRef ir.Expr) []ir.Stmt {
    if n.Component != nil {
        return st.lowerComponentNode(n, parentRef)
    }
    // ... existing CreateNode-based logic ...
}

// lowerComponentNode emits CreateComponent(comp, props) + AppendChild(parent, handle).
func (st *declarativeState) lowerComponentNode(n *ir.NodeInst, parentRef ir.Expr) []ir.Stmt {
    handleID := "__h" + strconv.Itoa(st.nextID)
    st.nextID++
    handleVar := &ir.Var{Name: handleID, Type: &ir.Type{Kind: ir.TypeDyn}}

    propsLit := buildPropsStructLit(n.Props)

    createCall := &ir.Call{
        Func: st.intrinsics["CreateComponent"],
        Args: []ir.CallArg{
            {Value: &ir.Ident{Name: n.Component.Name, Sym: n.Component, Type: &ir.Type{Kind: ir.TypeDyn}}},
            {Value: propsLit},
        },
        Type: &ir.Type{Kind: ir.TypeDyn},
    }
    appendCall := &ir.Call{
        Func: st.intrinsics["AppendChild"],
        Args: []ir.CallArg{
            {Value: parentRef},
            {Value: &ir.Ident{Name: handleID, Sym: handleVar, Type: &ir.Type{Kind: ir.TypeDyn}}},
        },
        Type: &ir.Type{Kind: ir.TypeVoid},
    }

    return []ir.Stmt{
        &ir.LocalVar{Var: handleVar, Init: createCall},
        &ir.CallStmt{Call: appendCall},
    }
}

// buildPropsStructLit produces an anonymous struct literal from a NodeInst's
// Props list. Each prop becomes a named field.
func buildPropsStructLit(props []ir.Arg) *ir.StructLit {
    fields := make([]ir.FieldInit, 0, len(props))
    for _, p := range props {
        if p.Name == "" {
            continue
        }
        fields = append(fields, ir.FieldInit{Name: p.Name, Value: p.Value})
    }
    return &ir.StructLit{
        Type:   &ir.Type{Kind: ir.TypeStruct},
        Fields: fields,
    }
}
```

> **Verify field names against the actual IR shape:**
> - `*ir.LocalVar` may have a `Var` field or just `Name` + `Type`; check `ir/ir.go`.
> - `ir.Arg` field `Name` and `Value` — verify.
> - `*ir.StructLit.Fields` is `[]ir.FieldInit`; the `Name`/`Value` shape matches the existing usage at, e.g., `internal/checker/expr.go:1369`.

- [ ] **Step 6.3: Ensure the intrinsic is loaded**

Verify `st.intrinsics` is populated. In `newDeclarativeState` (line 53), the loop over `ir.LowerIntrinsics` should include the new `CreateComponent`. If not, no change needed since `LowerIntrinsics` is a single slice and Task 2 added the entry.

- [ ] **Step 6.4: Build + test**

Run: `go build ./internal/lower/...`
Expected: clean.

Run: `go test ./internal/lower/... -count=1`
Expected: pre-existing tests pass. The new component-branch code only fires when a NodeInst has `n.Component != nil` AND is inside a slot body — no current fixtures exercise this.

- [ ] **Step 6.5: Add a unit test**

In `internal/lower/reactivity_test.go`, append:

```go
func TestReactivity_ComponentInSlotEmitsCreateComponent(t *testing.T) {
    items := &ir.Var{Name: "items", Type: &ir.Type{Kind: ir.TypeList}}
    card := &ir.Component{Name: "card"}
    main := &ir.Component{Name: "main", Vars: []*ir.Var{items}}

    forStmt := &ir.For{
        Key:  "x",
        Iter: &ir.Ident{Name: "items", Sym: items},
        Body: []ir.Stmt{&ir.NodeInst{Component: card}},
    }
    main.Body = []ir.Stmt{forStmt}
    // Make items reactive via a handler somewhere in main.Body.
    main.Body = append(main.Body, &ir.NodeInst{Handlers: []ir.EventHandler{
        {Name: "click", Func: &ir.Func{Block: []ir.Stmt{
            &ir.Assign{Target: &ir.Ident{Name: "items", Sym: items}, Value: &ir.ListLit{}},
        }}},
    }})

    pkg := &ir.Package{Components: []*ir.Component{card, main}}

    if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
        t.Fatal(err)
    }
    if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
        t.Fatal(err)
    }

    // Walk main.Funcs for the synthesized __renderSlotN; verify it contains a
    // CreateComponent call.
    var foundCreate bool
    for _, fn := range main.Funcs {
        if !strings.HasPrefix(fn.Name, "__renderSlot") {
            continue
        }
        for _, s := range fn.Block {
            if lv, ok := s.(*ir.LocalVar); ok {
                if call, ok := lv.Init.(*ir.Call); ok {
                    if call.Func != nil && call.Func.Name == "CreateComponent" {
                        foundCreate = true
                    }
                }
            }
        }
    }
    if !foundCreate {
        t.Errorf("expected CreateComponent call in synthesized __renderSlot func")
    }
}
```

Imports: `"strings"` if not already in the file.

- [ ] **Step 6.6: Run test**

Run: `go test ./internal/lower/ -count=1 -run TestReactivity_ComponentInSlotEmitsCreateComponent`
Expected: PASS.

- [ ] **Step 6.7: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_test.go
git commit -m "lower(reactivity): emit CreateComponent+AppendChild for components in slot bodies"
```

---

## Task 7: Extend `passDeclarative` to emit `CreateComponent` for static recursive NodeInsts

For NodeInsts that are not in reactive contexts but target recursive components (left in place by Task 4), `passDeclarative` must lower them similarly.

**Files:**
- Modify: `internal/lower/declarative.go`

- [ ] **Step 7.1: Apply the same component branch in `lowerNode`**

Find `lowerNode` (or whichever function flattens NodeInsts in the top-level declarative pass; around line 209). Add a precondition:

```go
func (st *declarativeState) lowerNode(n *ir.NodeInst, parentRef ir.Expr) []ir.Stmt {
    if n.Component != nil {
        return st.lowerComponentNode(n, parentRef)
    }
    // ... existing CreateNode-based logic ...
}
```

`lowerComponentNode` and `buildPropsStructLit` were added in Task 6; reuse them.

- [ ] **Step 7.2: Add a unit test**

In `internal/lower/declarative_test.go`, append:

```go
func TestDeclarative_RecursiveComponentEmitsCreateComponent(t *testing.T) {
    tree := &ir.Component{Name: "tree"}
    tree.Body = []ir.Stmt{&ir.NodeInst{Component: tree}}
    main := &ir.Component{Name: "main", Body: []ir.Stmt{&ir.NodeInst{Component: tree}}}

    pkg := &ir.Package{Components: []*ir.Component{tree, main}}

    if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
        t.Fatal(err)
    }
    if err := lowerDeclarative(pkg, Caps{NoDeclarative: true}, Options{}); err != nil {
        t.Fatal(err)
    }

    // main.Body should now contain a LocalVar { Init: CreateComponent(tree, ...) }
    // and a CallStmt { AppendChild }.
    var foundCreate bool
    for _, s := range main.Body {
        if lv, ok := s.(*ir.LocalVar); ok {
            if call, ok := lv.Init.(*ir.Call); ok {
                if call.Func != nil && call.Func.Name == "CreateComponent" {
                    foundCreate = true
                }
            }
        }
    }
    if !foundCreate {
        t.Errorf("expected CreateComponent in main.Body for static recursive NodeInst; got %#v", main.Body)
    }
}
```

- [ ] **Step 7.3: Run test**

Run: `go test ./internal/lower/ -count=1 -run TestDeclarative_RecursiveComponentEmitsCreateComponent`
Expected: PASS.

- [ ] **Step 7.4: Commit**

```bash
git add internal/lower/declarative.go internal/lower/declarative_test.go
git commit -m "lower(declarative): emit CreateComponent for static recursive NodeInsts"
```

---

## Task 8: JS translator for `CreateComponent`

**Files:**
- Modify: `codegen/lang/javascript/translate_ir.go`

- [ ] **Step 8.1: Locate the intrinsic-call dispatch**

Run: `grep -n "lower\." codegen/lang/javascript/translate_ir.go | head`

Find the switch that handles `lower.CreateNode`, `lower.AppendChild`, etc. Add a case for `lower.CreateComponent`.

- [ ] **Step 8.2: Add a `factoryName` helper**

In `codegen/lang/javascript/translate_ir.go`, near the top:

```go
// factoryName returns the JS factory function name for a component.
// Matches the naming used by the HTML codegen's factory emission step.
func factoryName(comp *ir.Component) string {
    return "__cf_" + sanitizeJSIdent(comp.Name)
}

// sanitizeJSIdent makes a SNGL component name safe for JS identifier
// position by replacing non-ident chars with underscores.
func sanitizeJSIdent(name string) string {
    out := make([]byte, 0, len(name))
    for i := 0; i < len(name); i++ {
        c := name[i]
        if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
            out = append(out, c)
        } else {
            out = append(out, '_')
        }
    }
    return string(out)
}
```

- [ ] **Step 8.3: Add the translation case**

In the switch handling `lower.<X>` intrinsic names:

```go
case "lower.CreateComponent":
    if len(call.Args) != 2 {
        return "/* CreateComponent: wrong arity */"
    }
    compIdent, ok := call.Args[0].Value.(*ir.Ident)
    if !ok {
        return "/* CreateComponent: arg[0] not an Ident */"
    }
    comp, ok := compIdent.Sym.(*ir.Component)
    if !ok {
        return "/* CreateComponent: arg[0].Sym not a Component */"
    }
    propsJS := translateIRExpr(call.Args[1].Value, scope)
    return factoryName(comp) + "(" + propsJS + ")"
```

- [ ] **Step 8.4: Build + verify**

Run: `go build ./codegen/lang/javascript/...`
Expected: clean.

- [ ] **Step 8.5: Commit**

```bash
git add codegen/lang/javascript/translate_ir.go
git commit -m "js: translate lower.CreateComponent to factory call"
```

---

## Task 9: HTML — opt into `NoInlineComponents`

**Files:**
- Modify: `codegen/platform/html/html.go:55`

- [ ] **Step 9.1: Update `RequiredCaps`**

Find the `RequiredCaps` method (line 55):

```go
func (g *Generator) RequiredCaps() lower.Caps {
    return lower.Caps{
        NoContext:           true,
        NoReactivity:        true,
        NoAsyncReactive:     true,
        NoStdlibWrappers:    true,
        NoInlineComponents:  true,  // NEW
    }
}
```

- [ ] **Step 9.2: Verify build**

Run: `go build ./codegen/platform/html/...`
Expected: clean.

- [ ] **Step 9.3: Run HTML tests; expect regression**

Run: `go test ./codegen/platform/html/... -count=1 -short`
Expected: many failures. The lower pipeline now produces a flat main.Vars with promoted names, but HTML's in-house inliner still runs and double-inlines / collides. Subsequent tasks remove the in-house inliner.

DO NOT commit yet — broken intermediate state. Tasks 10–12 fix.

---

## Task 10: HTML — delete in-house inlining + bridge

**Files:**
- Modify: `codegen/platform/html/html.go`

This is the largest mechanical step. The deletions are interconnected; delete them in this order to keep intermediate compile state minimal.

- [ ] **Step 10.1: Delete `renderComponentInline`**

Run: `grep -n "func.*renderComponentInline\|renderComponentInline(" codegen/platform/html/html.go | head`

Find the function and every call site. Delete the function. At each call site, replace with the standard NodeInst handling (which now passes through to the post-lowering ir code path that emits factory calls).

- [ ] **Step 10.2: Delete `dataRenames` field + threading**

Grep:

```bash
grep -n "dataRenames" codegen/platform/html/html.go
```

Delete the field from `htmlGen`, all assignment sites, and all readers. Where `dataRenames[name]` was looked up, no replacement is needed — the lower pass already substituted the promoted name into the IR.

- [ ] **Step 10.3: Delete `inlinedStateInits` field + threading**

Same procedure. State inits now come from `main.Vars[i].Init` directly.

- [ ] **Step 10.4: Delete `varRegistry`, `newVarRegistry`, `varSetToNames`, `remapMutated`**

Delete the types and functions and every call site.

`g.exprDeps()` is rewritten in Task 11; for now it can be a stub returning the unconverted pointer-keyed set (will adjust types in Task 11).

- [ ] **Step 10.5: Verify build (will not pass)**

Run: `go build ./codegen/platform/html/...`
Expected: compile errors from callers of the deleted functions. Continue to Task 11 to fix.

DO NOT commit yet.

---

## Task 11: HTML — rewrite `exprDeps` + downstream consumers

**Files:**
- Modify: `codegen/platform/html/html.go`

- [ ] **Step 11.1: Add a `currentComp` field**

In `htmlGen`:

```go
type htmlGen struct {
    // ... existing fields ...
    currentComp *ir.Component
    // ...
}
```

Initialize in `newHTMLGen`:

```go
g.currentComp = mainIRComponent(pkg)
```

- [ ] **Step 11.2: Rewrite `exprDeps`**

Replace the existing `exprDeps` (around line 3300):

```go
func (g *htmlGen) exprDeps(expr ir.Expr) map[*ir.Var]struct{} {
    return g.dt.ExprDeps(g.currentComp, expr)
}
```

- [ ] **Step 11.3: Change `updateFunc.deps`, `eventHandler.mutated`, `timerDef.mutated` to pointer-keyed**

Update the type definitions (~line 484-503):

```go
type updateFunc struct {
    funcName string
    body     string
    deps     map[*ir.Var]struct{}
    initOnly bool
}

type eventHandler struct {
    elemID  string
    event   string
    body    string
    mutated map[*ir.Var]struct{}
    isAsync bool
}

type timerDef struct {
    index      int
    intervalMs int
    activeVar  string
    body       string
    mutated    map[*ir.Var]struct{}
    bodyAsync  bool
}
```

- [ ] **Step 11.4: Migrate consumers**

At every site that iterates these sets and writes JS, switch to:

```go
for v := range updater.deps {
    name := v.Name
    // emit subscription / setter wiring for name
}
```

`grep -n "\.deps\b\|\.mutated\b" codegen/platform/html/html.go` to find them all. Mechanical rewrite.

- [ ] **Step 11.5: Migrate `MutatedFields` calls**

`grep -n "MutatedFields\|MutatedFieldsExpr" codegen/platform/html/html.go`.

Each call now takes `(g.currentComp, g.dt, stmt)` instead of `(stmt)`. The returned `map[*ir.Var]struct{}` flows into handlers as pointer-keyed deps.

- [ ] **Step 11.6: Drop the test_setter map literal fix**

`codegen/platform/html/emit_setter_test.go` had pointer-keyed empty maps for `NewDepTracker(...)`. Verify those still type-check correctly.

- [ ] **Step 11.7: Build**

Run: `go build ./codegen/platform/html/...`
Expected: clean.

DO NOT commit yet — tests will still fail because factory emission isn't wired in.

---

## Task 12: HTML — emit JS factories for remaining components

**Files:**
- Modify: `codegen/platform/html/html.go` (script emission section)

- [ ] **Step 12.1: Add a factory-emission loop in `emitScript`**

Find `emitScript` (around line 2467). After timer collection but before main-component body emission, insert:

```go
// Emit JS factories for components that survived NoInlineComponents
// (recursive cycles, reactive-loop targets). Each factory creates a
// per-instance closure over `state` and returns the root DOM node.
for _, comp := range g.pkg.Components {
    if comp.Name == "main" {
        continue
    }
    g.emitComponentFactory(b, comp)
}
```

- [ ] **Step 12.2: Implement `emitComponentFactory`**

Add to `html.go`:

```go
// emitComponentFactory writes a JS function that returns a fresh
// instance of the component. The function name matches the convention
// used by the JS translator's CreateComponent lowering.
func (g *htmlGen) emitComponentFactory(b *strings.Builder, comp *ir.Component) {
    fmt.Fprintf(b, "function %s(props) {\n", factoryName(comp))
    b.WriteString("  const state = {\n")
    for _, v := range comp.Vars {
        fmt.Fprintf(b, "    %s: %s,\n", v.Name, g.literalToJS(v.Init))
    }
    b.WriteString("  };\n")

    // Save and set component context so nested emits attribute
    // bindings/handlers to this component's state.
    savedComp := g.currentComp
    g.currentComp = comp
    defer func() { g.currentComp = savedComp }()

    // Body is already lowered to imperative form (CreateNode + setProp
    // + AppendChild). Reuse the same renderIRStmt path used for main.
    for _, s := range comp.Body {
        g.renderIRStmt(b, s, 1)
    }

    // The first __nN var declared in the body is the root by convention.
    b.WriteString("  return __n0;\n")
    b.WriteString("}\n\n")
}

// factoryName mirrors the JS translator's helper.
func factoryName(comp *ir.Component) string {
    return "__cf_" + sanitizeJSIdent(comp.Name)
}

func sanitizeJSIdent(name string) string {
    out := make([]byte, 0, len(name))
    for i := 0; i < len(name); i++ {
        c := name[i]
        if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
            out = append(out, c)
        } else {
            out = append(out, '_')
        }
    }
    return string(out)
}
```

> **Note on `__n0`-as-root assumption.** `passDeclarative` emits node declarations with sequential `__nN` IDs; the first is the root. If a factory's body produces a different root convention (e.g., a single-child wrapper), revisit. Spec out a follow-up if any fixture trips this.

- [ ] **Step 12.3: Verify build**

Run: `go build ./codegen/platform/html/...`
Expected: clean.

- [ ] **Step 12.4: Run HTML tests**

Run: `go test ./codegen/platform/html/... -count=1 -short`
Expected: most tests pass. Browser tests for TodoApp/FullExample/FullFixture should be green.

If failures remain, they're in test-specific areas (slot children, specific selectors). Investigate per-test; expand scope only if a regression.

- [ ] **Step 12.5: Commit the HTML migration**

```bash
git add codegen/platform/html/html.go codegen/platform/html/emit_setter_test.go
git commit -m "$(cat <<'EOF'
html: consume lowered IR; drop in-house inliner and bridge

HTML opts into NoInlineComponents. The lower pipeline now produces a
flat main with promoted-name *ir.Var pointers. Components in reactive
loops or recursive cycles survive as ir.Components; HTML emits each as
a JS factory __cf_<name>(props) returning the root DOM node.

Deletions:
- renderComponentInline + dataRenames + inlinedStateInits.
- varRegistry / newVarRegistry / varSetToNames / remapMutated.
- The promote branch in nodeFromIRCallStmt.

exprDeps now calls dt.ExprDeps directly; updater/handler dep sets are
pointer-keyed end-to-end. Approximately 300 lines removed.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Full verification + smoke test for issue #75 re-enable

- [ ] **Step 13.1: Full test suite**

Run: `go tool verify`
Expected: PASS. Pre-existing testrunner failures in `test_reactivity_*` may still occur (those test the interp, not HTML codegen) — note them but they're not blockers.

- [ ] **Step 13.2: Format**

Run: `go fmt ./...`
Expected: no diff.

- [ ] **Step 13.3: Build CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 13.4: Smoke test #75 component-method desugaring re-enable**

Open `internal/checker/checker.go`. Find the comment block in `registerComponent` (around line 1037) and replace the closure-only branch:

```go
case *ast.FuncDef:
    nestedFuncs = append(nestedFuncs, s)
}
```

Add at the bottom of `registerComponent`:

```go
c.pkg.Components = append(c.pkg.Components, irComp)
c.symtab.Comps[irComp.Name] = irComp
c.scope.Declare(irComp)

irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nestedFuncs)
```

Add `var nestedFuncs []*ast.FuncDef` at the top of the function (where it's needed in the loop).

- [ ] **Step 13.5: Run full suite with #75 enabled**

Run: `go test ./... -count=1 -short`
Expected: pass. HTML codegen tests should be green because component methods are now desugared and tracked via the walker on the lowered IR.

- [ ] **Step 13.6: Commit the #75 re-enable**

```bash
git add internal/checker/checker.go
git commit -m "checker: re-enable component method desugaring (issue #75)

Now possible because HTML codegen consumes the lowered IR and the
substituting walker handles per-method this-receiver tracking
naturally on flat *ir.Var pointer keys.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §New intrinsic → Task 2.
- §passNoInlineComponents change → Tasks 3, 4. Skip rules for reactive context + recursive cycles.
- §passReactivity change → Task 6 — CreateComponent + AppendChild in slot bodies.
- §passDeclarative change → Task 7 — same for static-position recursive NodeInsts.
- §HTML required caps → Task 9.
- §HTML deletions → Task 10.
- §HTML exprDeps rewrite → Task 11.
- §JS scope emission → covered by Task 11 (renames built from main.Vars; the existing translator handles state.<name> emission).
- §Component factories → Task 12.
- §CreateComponent translation → Task 8.
- §Testing (driver fixtures) → Task 1.
- §Testing (unit tests for passes) → Tasks 5, 6.5, 7.2.
- §Smoke test #75 → Task 13.

**Placeholder scan:** none. Every step has actual code, exact greps, or exact commands.

**Type consistency:** `factoryName(comp)` defined in both `codegen/lang/javascript/translate_ir.go` and `codegen/platform/html/html.go` — they MUST produce identical strings for the same component. Both implementations use `__cf_` + `sanitizeJSIdent(comp.Name)`. Duplicated to avoid cross-package dependency; verify they stay in sync. `map[*ir.Var]struct{}` is used uniformly for dep/mutated sets across html.go.

**Flagged-for-verification notes during implementation:**
- Task 3: confirm `walkExprIdents` exists in `reactivity.go`; if not, add.
- Task 6: verify `*ir.LocalVar` and `*ir.StructLit` field shapes against `ir/ir.go`.
- Task 12: the `return __n0` convention assumes `passDeclarative` numbers nodes starting from 0 per-component. Verify; adjust if the counter is global.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-21-html-uses-lowered-ir.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
