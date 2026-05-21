# Normalize Method Calls + Unblock #75 Re-enable Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Normalize the two method-call IR patterns (implicit-recv and explicit-recv) into one (explicit-recv) via a new lowering pass, add per-target translation of the synthetic component-self ident, dedupe HTML's function-emission loop, and re-enable issue #75's component-method desugaring.

**Architecture:** New `passNoImplicitRecv` lowering pass scans `*ir.Call`s whose `Func.Receiver` is non-empty but whose `Args` is missing the receiver slot; for component receivers, prepends a synthetic `*ir.Ident{Sym: *ir.Component}` as Args[0]. Each codegen target translates that synthetic ident to its per-instance state expression (`state` for JS, `m` for Go). HTML's `emitScript` dedupes pkg.Funcs ∪ main.Funcs on `*ir.Func` pointer; single naming convention `<Receiver>_<Name>(state, ...)`. Issue #75 desugaring re-enables once these normalizations are in place.

**Tech Stack:** Go. Touchpoints: `internal/lower/{lower,caps,normalize_method_calls}.go`, `codegen/lang/javascript/{ircontext,translate_ir}.go`, `codegen/platform/html/html.go`, `codegen/platform/bubbletea/compiler_ir.go` (already deduped — reference). Driver fixtures already exist in `testdata/test_html_*.sngl`.

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/lower/caps.go` | Modify | Add `NoImplicitRecv` flag + string serialization |
| `internal/lower/normalize_method_calls.go` | Create | New `passNoImplicitRecv` pass |
| `internal/lower/normalize_method_calls_test.go` | Create | Unit tests for the pass |
| `internal/lower/lower.go` | Modify | Register the new pass in the global pass order |
| `codegen/lang/javascript/ircontext.go` | Modify | Translate `*ir.Ident{Sym: *ir.Component}` to `state`; reset the pre-existing `evalTypeMethodCall` to the canonical `<Receiver>_<Method>(args...)` form |
| `codegen/lang/javascript/translate_ir.go` | Modify | Same for the alternate translation path |
| `codegen/platform/html/html.go` | Modify | Opt into `NoImplicitRecv`; dedupe `pkgFuncs`; lift `emitJSFunc` naming to `<Receiver>_<Name>` for desugared methods |
| `codegen/platform/html/html_test.go` | Modify | Update test expectations to match new emission convention |
| `codegen/platform/html/internal/webtest/*` | Modify (if needed) | Browser-test fixtures referring to old names |
| `internal/checker/checker.go` | Modify | Re-enable `registerNestedMethods` call for components |

---

## Task 1: Add `Caps.NoImplicitRecv` flag

**Files:**
- Modify: `internal/lower/caps.go`

- [ ] **Step 1.1: Add the field**

In `internal/lower/caps.go`, find the `Caps` struct. Add a new bool field (alphabetically, near `NoInlineComponents`):

```go
type Caps struct {
    // ... existing fields ...
    NoImplicitRecv     bool // method calls with implicit receiver → explicit Args[0]
    NoInlineComponents bool // user-defined non-recursive components → inlined into main
    // ... rest ...
}
```

- [ ] **Step 1.2: Update `Union`**

In the `Union` method (combines two Caps via OR), add:

```go
NoImplicitRecv: c.NoImplicitRecv || other.NoImplicitRecv,
```

- [ ] **Step 1.3: Update `String`**

In the `String` method (lists enabled caps for debug output), add a stanza alongside `NoInlineComponents`:

```go
if c.NoImplicitRecv {
    parts = append(parts, "NoImplicitRecv")
}
```

Place this in the same order the field appears in the struct (which is alphabetical near NoInlineComponents).

- [ ] **Step 1.4: Update Caps tests**

Run: `go test ./internal/lower/ -count=1 -run TestCaps`
If tests assert exact string output (`caps_test.go:46-68`), they'll break because the alphabetical ordering changes. Update the expected strings to include `NoImplicitRecv` at the proper position.

- [ ] **Step 1.5: Verify build**

Run: `go build ./internal/lower/...`
Expected: clean.

- [ ] **Step 1.6: Commit**

```bash
git add internal/lower/caps.go internal/lower/caps_test.go
git commit -m "lower: add NoImplicitRecv capability flag

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Implement `passNoImplicitRecv`

**Files:**
- Create: `internal/lower/normalize_method_calls.go`

- [ ] **Step 2.1: Write the pass**

Create `internal/lower/normalize_method_calls.go`:

```go
package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoImplicitRecv normalizes method calls so every *ir.Call whose
// Func has a non-empty Receiver carries the receiver value in Args[0].
//
// Two source patterns currently reach IR:
//
//   Pattern A (implicit recv):
//     ir.Call{Func: T.method (with receiver param), Args: []}
//   Pattern B (explicit recv):
//     ir.Call{Func: T.method, Args: [recv, ...]}
//
// Pattern A arises from bare-name component-method refs (T8 elision
// followed by implicitCall) where the source has no receiver expression
// to write. After this pass, every Call is Pattern B; codegen drops its
// "is Args empty?" branching.
//
// For component receivers, the synthesized Args[0] is an *ir.Ident
// whose Sym is the *ir.Component. Each codegen target translates that
// ident to its per-instance state expression (`state` for JS, `m` for Go).
var passNoImplicitRecv = pass{
	name:    "NoImplicitRecv",
	enabled: func(c Caps) bool { return c.NoImplicitRecv },
	apply:   lowerNoImplicitRecv,
}

func lowerNoImplicitRecv(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &noImplicitRecvState{pkg: pkg}
	for _, comp := range pkg.Components {
		st.currentComp = comp
		if err := st.walkStmts(comp.Body); err != nil {
			return err
		}
		for _, fn := range comp.Funcs {
			if err := st.walkStmts(fn.Block); err != nil {
				return err
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				if err := st.walkStmts(t.Handler.Block); err != nil {
					return err
				}
			}
		}
	}
	st.currentComp = nil
	for _, fn := range pkg.Funcs {
		if err := st.walkStmts(fn.Block); err != nil {
			return err
		}
	}
	for _, w := range pkg.Windows {
		if err := st.walkStmts(w.Body); err != nil {
			return err
		}
		for _, fn := range w.Funcs {
			if err := st.walkStmts(fn.Block); err != nil {
				return err
			}
		}
	}
	return nil
}

type noImplicitRecvState struct {
	pkg         *ir.Package
	currentComp *ir.Component
}

func (st *noImplicitRecvState) walkStmts(stmts []ir.Stmt) error {
	for _, s := range stmts {
		if err := st.walkStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (st *noImplicitRecvState) walkStmt(s ir.Stmt) error {
	switch n := s.(type) {
	case *ir.Assign:
		return st.walkExpr(n.Value)
	case *ir.Toggle:
		return st.walkExpr(n.Target)
	case *ir.Return:
		return st.walkExpr(n.Value)
	case *ir.LocalVar:
		return st.walkExpr(n.Init)
	case *ir.If:
		if err := st.walkExpr(n.Cond); err != nil {
			return err
		}
		if err := st.walkStmts(n.Body); err != nil {
			return err
		}
		return st.walkStmts(n.Else)
	case *ir.For:
		if err := st.walkExpr(n.Iter); err != nil {
			return err
		}
		if err := st.walkStmts(n.Body); err != nil {
			return err
		}
		return st.walkStmts(n.Else)
	case *ir.Emit:
		for _, a := range n.Args {
			if err := st.walkExpr(a.Value); err != nil {
				return err
			}
		}
	case *ir.CallStmt:
		return st.walkExpr(n.Call)
	case *ir.NodeInst:
		for _, p := range n.Props {
			if err := st.walkExpr(p.Value); err != nil {
				return err
			}
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				if err := st.walkStmts(h.Func.Block); err != nil {
					return err
				}
			}
		}
		return st.walkStmts(n.Children)
	case *ir.SlotInst:
		return st.walkStmts(n.Children)
	case *ir.ErrorBoundary:
		if err := st.walkStmts(n.Children); err != nil {
			return err
		}
		if n.Handler != nil && n.Handler.Func != nil {
			return st.walkStmts(n.Handler.Func.Block)
		}
	case *ir.PlatformFilter:
		return st.walkStmts(n.Body)
	case *ir.Window:
		if err := st.walkExpr(n.Href); err != nil {
			return err
		}
		if err := st.walkExpr(n.Title); err != nil {
			return err
		}
		if err := st.walkExpr(n.Favicon); err != nil {
			return err
		}
		return st.walkStmts(n.Body)
	case *ir.ContextProvider:
		if err := st.walkExpr(n.Value); err != nil {
			return err
		}
		return st.walkStmts(n.Children)
	}
	return nil
}

func (st *noImplicitRecvState) walkExpr(e ir.Expr) error {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ir.Call:
		return st.walkCall(n)
	case *ir.Binary:
		if err := st.walkExpr(n.Left); err != nil {
			return err
		}
		return st.walkExpr(n.Right)
	case *ir.Unary:
		return st.walkExpr(n.Operand)
	case *ir.Ternary:
		if err := st.walkExpr(n.Cond); err != nil {
			return err
		}
		if err := st.walkExpr(n.Then); err != nil {
			return err
		}
		return st.walkExpr(n.Else)
	case *ir.Conversion:
		return st.walkExpr(n.Operand)
	case *ir.Select:
		return st.walkExpr(n.Operand)
	case *ir.Index:
		if err := st.walkExpr(n.Operand); err != nil {
			return err
		}
		return st.walkExpr(n.Idx)
	case *ir.ListLit:
		for _, el := range n.Elems {
			if err := st.walkExpr(el); err != nil {
				return err
			}
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			if err := st.walkExpr(f.Value); err != nil {
				return err
			}
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			if err := st.walkExpr(en.Key); err != nil {
				return err
			}
			if err := st.walkExpr(en.Value); err != nil {
				return err
			}
		}
	case *ir.Spread:
		return st.walkExpr(n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			return st.walkStmts(n.Func.Block)
		}
	case *ir.Closure:
		if n.Func != nil {
			return st.walkStmts(n.Func.Block)
		}
	}
	return nil
}

func (st *noImplicitRecvState) walkCall(c *ir.Call) error {
	// Recurse into existing args first.
	for _, a := range c.Args {
		if err := st.walkExpr(a.Value); err != nil {
			return err
		}
	}
	if c.Receiver != nil {
		if err := st.walkExpr(c.Receiver); err != nil {
			return err
		}
	}
	if c.Func == nil || c.Func.Receiver == "" {
		return nil
	}
	if len(c.Args) >= len(c.Func.Params) {
		return nil // already Pattern B (explicit-recv)
	}
	// Pattern A: synthesize the receiver.
	if len(c.Func.Params) == 0 {
		return nil // nothing to receive (shouldn't happen for receiver-bearing methods)
	}
	recvType := c.Func.Params[0].Type
	if recvType == nil {
		return nil
	}
	recv, err := st.synthRecv(recvType, c.Func.Receiver)
	if err != nil {
		return err
	}
	c.Args = append([]ir.CallArg{{Value: recv}}, c.Args...)
	return nil
}

// synthRecv returns an expression representing the implicit receiver
// for a method call whose Args[0] was elided by the checker. For
// component receivers, returns an *ir.Ident whose Sym is the
// *ir.Component (codegen translates this to per-target state).
//
// Struct and enum receivers should never reach this path — those
// methods are always called via explicit `.method()` syntax which
// produces Pattern B at the checker. If they do, return an error so
// the regression is visible.
func (st *noImplicitRecvState) synthRecv(recvType *ir.Type, receiverName string) (ir.Expr, error) {
	switch d := recvType.Decl.(type) {
	case *ir.Component:
		return &ir.Ident{
			Name: d.Name,
			Type: recvType,
			Sym:  d,
		}, nil
	case *ir.StructDef:
		return nil, fmt.Errorf("noImplicitRecv: struct method %s.%s called without explicit receiver", d.Name, receiverName)
	case *ir.EnumDef:
		return nil, fmt.Errorf("noImplicitRecv: enum method %s.%s called without explicit receiver", d.Name, receiverName)
	}
	return nil, fmt.Errorf("noImplicitRecv: unsupported receiver type kind %v for %s", recvType.Kind, receiverName)
}
```

- [ ] **Step 2.2: Build**

Run: `go build ./internal/lower/...`
Expected: clean.

- [ ] **Step 2.3: Do not commit yet** — Task 3 adds the test, Task 4 wires the pass into the pipeline.

---

## Task 3: Unit test for `passNoImplicitRecv`

**Files:**
- Create: `internal/lower/normalize_method_calls_test.go`

- [ ] **Step 3.1: Write the test**

Create `internal/lower/normalize_method_calls_test.go`:

```go
package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestNoImplicitRecv_SynthesizesComponentSelf(t *testing.T) {
	main := &ir.Component{Name: "main"}
	thisType := &ir.Type{Kind: ir.TypeComponent, Decl: main}

	// func main.status(this main) => ...
	status := &ir.Func{
		Receiver: "main",
		Name:     "status",
		Params:   []*ir.Param{{Name: "this", Type: thisType}},
	}

	// Call site with Args empty (implicit-recv form).
	call := &ir.Call{
		Func: status,
		Args: nil,
	}
	main.Body = []ir.Stmt{&ir.Return{Value: call}}
	main.Funcs = []*ir.Func{status}

	pkg := &ir.Package{Components: []*ir.Component{main}}

	if err := lowerNoImplicitRecv(pkg, Caps{NoImplicitRecv: true}, Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 1 {
		t.Fatalf("Args after pass: got len=%d, want 1", len(call.Args))
	}
	got, ok := call.Args[0].Value.(*ir.Ident)
	if !ok {
		t.Fatalf("Args[0]: got %T, want *ir.Ident", call.Args[0].Value)
	}
	comp, ok := got.Sym.(*ir.Component)
	if !ok || comp != main {
		t.Errorf("Args[0].Sym: got %v, want *ir.Component{main}", got.Sym)
	}
}

func TestNoImplicitRecv_LeavesExplicitRecvUnchanged(t *testing.T) {
	main := &ir.Component{Name: "main"}
	thisType := &ir.Type{Kind: ir.TypeComponent, Decl: main}
	status := &ir.Func{
		Receiver: "main",
		Name:     "status",
		Params:   []*ir.Param{{Name: "this", Type: thisType}},
	}

	// Call site with explicit recv arg (Pattern B already).
	explicitRecv := &ir.Ident{Name: "self", Sym: main}
	call := &ir.Call{
		Func: status,
		Args: []ir.CallArg{{Value: explicitRecv}},
	}
	main.Body = []ir.Stmt{&ir.Return{Value: call}}
	main.Funcs = []*ir.Func{status}

	pkg := &ir.Package{Components: []*ir.Component{main}}

	if err := lowerNoImplicitRecv(pkg, Caps{NoImplicitRecv: true}, Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 1 {
		t.Errorf("Args length: got %d, want 1 (unchanged)", len(call.Args))
	}
	if call.Args[0].Value != explicitRecv {
		t.Errorf("Args[0].Value: was reassigned; pass should leave Pattern B alone")
	}
}

func TestNoImplicitRecv_IgnoresFreeFunctionCalls(t *testing.T) {
	// func add(a int, b int) int — no receiver.
	add := &ir.Func{
		Receiver: "", // free function
		Name:     "add",
		Params: []*ir.Param{
			{Name: "a", Type: &ir.Type{Kind: ir.TypeInt}},
			{Name: "b", Type: &ir.Type{Kind: ir.TypeInt}},
		},
	}
	call := &ir.Call{
		Func: add,
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Raw: "1"}},
			{Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Raw: "2"}},
		},
	}
	main := &ir.Component{Name: "main", Body: []ir.Stmt{&ir.Return{Value: call}}}
	pkg := &ir.Package{Components: []*ir.Component{main}, Funcs: []*ir.Func{add}}

	if err := lowerNoImplicitRecv(pkg, Caps{NoImplicitRecv: true}, Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 2 {
		t.Errorf("free-function Args length changed: got %d, want 2", len(call.Args))
	}
}
```

- [ ] **Step 3.2: Run tests; expect FAIL on pass-not-registered**

Run: `go test ./internal/lower/ -count=1 -run TestNoImplicitRecv`
Expected: PASS — `lowerNoImplicitRecv` is called directly, not via the pass dispatcher, so it works before Task 4 wires it in.

- [ ] **Step 3.3: Commit Tasks 2+3**

```bash
git add internal/lower/normalize_method_calls.go internal/lower/normalize_method_calls_test.go
git commit -m "lower: passNoImplicitRecv normalizes method calls to explicit-recv

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Register the pass in the pipeline

**Files:**
- Modify: `internal/lower/lower.go`

- [ ] **Step 4.1: Locate the passes slice**

Run: `grep -n "passes = \|passReactivity\|passNoInlineComponents\|passDeclarative" internal/lower/lower.go | head`

The passes slice is around line 44. Order matters: `passNoImplicitRecv` must run AFTER the checker's `registerNestedMethods` (which produces the Pattern A calls) and BEFORE codegen (so codegen only sees Pattern B). The clearest insertion point is right after `passNoInlineComponents`:

- [ ] **Step 4.2: Add to the slice**

```go
var passes = []pass{
    // ... existing entries ...
    passNoInlineComponents,
    passNoImplicitRecv, // NEW — after inlining, before reactivity
    passReactivity,
    // ...
    passDeclarative,
}
```

Order rationale (capture in comment near the slice):
- After `passNoInlineComponents`: inlining substitutes `this` bindings with concrete arg exprs for non-recursive calls, eliminating most Pattern A calls; what remains are calls into surviving components (recursive cycles, reactive-loop targets).
- Before `passReactivity`: the slot-body lowering may inspect Call.Args to track reactive reads through method dispatch.
- Before `passDeclarative`: declarative lowering reads NodeInst props (which may be method calls) and expects the canonical Pattern B shape.

- [ ] **Step 4.3: Update lower.go's comment block (lines 1-50) if it lists pass order**

If the file documents pass order in a top-of-file comment block, append a line for `NoImplicitRecv`. Otherwise skip.

- [ ] **Step 4.4: Build + test**

Run: `go build ./...`
Run: `go test ./internal/lower/ -count=1`
Expected: pass. Existing lower fixtures don't request `NoImplicitRecv`, so behavior is unchanged for them.

- [ ] **Step 4.5: Commit**

```bash
git add internal/lower/lower.go
git commit -m "lower: register passNoImplicitRecv in pipeline

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: JS codegen — translate `*ir.Ident{Sym: *ir.Component}` to `state`

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go`

- [ ] **Step 5.1: Locate `evalIdent`**

Run: `grep -n "func.*evalIdent\|case \*ir.Component" codegen/lang/javascript/ircontext.go | head`

`evalIdent` is around line 270-310. It checks for various ident kinds and emits the corresponding JS expression.

- [ ] **Step 5.2: Add component-self translation**

Inside `evalIdent`, before the generic name-resolution branch, add:

```go
// Component-self ident: synthesized by passNoImplicitRecv as the
// implicit receiver of a desugared component method. The JS emission
// uses `state` for per-instance state of the currently-emitting
// component.
if _, ok := n.Sym.(*ir.Component); ok {
    return "state"
}
```

> Verify the exact position: this branch should run BEFORE any fallthrough that would emit the component's bare name (which would produce `main` as a JS reference — wrong).

- [ ] **Step 5.3: Mirror in `translate_ir.go`**

Run: `grep -n "func translateIRIdent\|case \*ir.Component" codegen/lang/javascript/translate_ir.go | head`

`translateIRIdent` is the alternate JS-emit path used by the static-tree HTML emitter. Add the same branch:

```go
if _, ok := n.Sym.(*ir.Component); ok {
    return "state"
}
```

- [ ] **Step 5.4: Build + run JS tests**

Run: `go build ./codegen/lang/javascript/...`
Run: `go test ./codegen/lang/javascript/... -count=1`
Expected: clean. No existing test exercises the new branch (Pattern A → Pattern B normalization isn't in any current fixture).

- [ ] **Step 5.5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/translate_ir.go
git commit -m "js: translate component-self ident to state expression

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: HTML codegen — opt into `NoImplicitRecv`

**Files:**
- Modify: `codegen/platform/html/html.go:55`

- [ ] **Step 6.1: Update RequiredCaps**

```go
func (g *Generator) RequiredCaps() lower.Caps {
    return lower.Caps{
        NoContext:          true,
        NoReactivity:       true,
        NoAsyncReactive:    true,
        NoStdlibWrappers:   true,
        NoInlineComponents: true,
        NoImplicitRecv:     true, // NEW
    }
}
```

- [ ] **Step 6.2: Update `dump_lowered_list.txt` test baseline**

Run: `grep -rn "NoImplicitRecv\|NoInlineComponents" cmd/sngl/testdata/ | head`

The `cmd/sngl/testdata/dump_lowered_list.txt` golden file lists which caps each platform requests. Append `NoImplicitRecv` alongside `NoInlineComponents` in the HTML-platform sections (both `--lang go` and `--lang none`). Run the test:

```bash
go test ./cmd/sngl/... -count=1 -run TestDumpLoweredList
```

If it fails because of the cap list, accept the diff (manually edit the golden) and re-run.

- [ ] **Step 6.3: Build + run HTML tests**

Run: `go build ./codegen/platform/html/...`
Run: `go test ./codegen/platform/html/... -count=1 -short`
Expected: clean. The new pass runs but doesn't change existing IR shape (no Pattern A calls in current fixtures).

- [ ] **Step 6.4: Commit**

```bash
git add codegen/platform/html/html.go cmd/sngl/testdata/dump_lowered_list.txt
git commit -m "html: opt into NoImplicitRecv

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: HTML codegen — dedupe `pkgFuncs` on `*ir.Func` pointer

**Files:**
- Modify: `codegen/platform/html/html.go` (the `pkgFuncs` function around line 1032)

- [ ] **Step 7.1: Rewrite `pkgFuncs` with a seen-set**

Replace the function:

```go
// pkgFuncs returns user-defined top-level funcs plus main component funcs,
// deduplicated. After passNoInlineComponents + registerNestedMethods,
// component methods land in both pkg.Funcs AND comp.Funcs; without dedupe
// the emitter would double-emit them.
//
// Synthesized funcs (e.g. __renderSlotN from passReactivity) are excluded;
// emitScript routes them through htmlTranslator + WalkLowered separately.
func (g *htmlGen) pkgFuncs() []*ir.Func {
    if g.pkg == nil {
        return nil
    }
    seen := make(map[*ir.Func]struct{})
    var out []*ir.Func
    add := func(f *ir.Func) {
        if f.Synthesized {
            return
        }
        if _, dup := seen[f]; dup {
            return
        }
        seen[f] = struct{}{}
        out = append(out, f)
    }
    for _, f := range g.pkg.Funcs {
        add(f)
    }
    if main := mainIRComponent(g.pkg); main != nil {
        for _, f := range main.Funcs {
            add(f)
        }
    }
    return out
}
```

- [ ] **Step 7.2: Test + commit**

Run: `go test ./codegen/platform/html/... -count=1 -short`
Expected: clean.

```bash
git add codegen/platform/html/html.go
git commit -m "html: dedupe pkgFuncs by pointer

Component methods registered via registerNestedMethods appear in both
pkg.Funcs and comp.Funcs; pointer-identity dedupe prevents the emitter
from writing each one twice.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: HTML codegen — single naming convention for desugared methods

**Files:**
- Modify: `codegen/platform/html/html.go` (the `emitJSFunc` function around line 3760)

- [ ] **Step 8.1: Emit `<Receiver>_<Name>` for receiver-bearing funcs**

The current `emitJSFunc` mangles dotted names via `strings.ReplaceAll(fn.Name, ".", "_")`. For desugared methods (`Receiver="main"`, `Name="status"`), `fn.Name` is just `"status"` — the dotted form lives only in `fn.Receiver`. Update the JS naming:

Find this line in `emitJSFunc` (~line 3782):

```go
// Mangle dotted names for JS: int.sqrt → int_sqrt
jsName := strings.ReplaceAll(fn.Name, ".", "_")
```

Replace with:

```go
// Mangle dotted names for JS: int.sqrt → int_sqrt.
// For desugared methods, the dotted form lives in fn.Receiver.
jsName := strings.ReplaceAll(fn.Name, ".", "_")
if fn.Receiver != "" {
    jsName = fn.Receiver + "_" + fn.Name
}
```

- [ ] **Step 8.2: Same for the computed-emit branch**

Run: `grep -n "function \\$\\|function %s" codegen/platform/html/html.go | head`

Around line 2560 the computed-emit block writes `function $%s()`. For receiver-bearing funcs (desugared methods that happen to qualify as computed), use the `<Receiver>_<Name>(state)` form:

```go
funcs := g.pkgFuncs()
hasComputed := false
for _, fn := range funcs {
    if !codegen.IsComputed(fn) || len(fn.Block) != 1 {
        continue
    }
    ret, ok := fn.Block[0].(*ir.Return)
    if !ok || ret.Value == nil {
        continue
    }
    body := g.exprToJS(ret.Value)
    if fn.Receiver != "" {
        fmt.Fprintf(b, "function %s_%s(state) { return %s; }\n", fn.Receiver, fn.Name, body)
    } else {
        fmt.Fprintf(b, "function $%s() { return %s; }\n", fn.Name, body)
    }
    hasComputed = true
}
```

> Note: ensure `emitJSFunc` and the computed branch don't BOTH emit the same function. The computed branch hits funcs where `IsComputed(fn) && len(fn.Block) == 1`; `emitJSFunc` hits every func in `pkgFuncs()`. For methods that qualify as computed, both would fire. Add a skip in `emitJSFunc`:

```go
func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ir.Func) {
    if codegen.IsComputed(fn) && len(fn.Block) == 1 {
        if _, ok := fn.Block[0].(*ir.Return); ok {
            return // already emitted in the computed branch
        }
    }
    // ... existing body ...
}
```

- [ ] **Step 8.3: Build + test**

Run: `go build ./codegen/platform/html/...`
Run: `go test ./codegen/platform/html/... -count=1 -short`
Expected: clean. Existing tests don't have receiver-bearing funcs in their corpus (component methods aren't desugared yet — that's Task 10).

- [ ] **Step 8.4: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "html: emit desugared methods as <Receiver>_<Name>(state)

Receiver-bearing funcs (post-passNoImplicitRecv normalization, the
receiver is in Args[0]) use the canonical naming convention.
emitJSFunc skips computed funcs already emitted in the dedicated
computed branch to avoid duplicates.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: JS evalTypeMethodCall — emit the canonical form

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` (the `evalTypeMethodCall` function around line 467)

- [ ] **Step 9.1: Replace the user-method branch**

Locate the lookup that uses `jc.Ctx.Pkg.Funcs` (added in earlier work). The current form has an "implicit state" fallback. After `passNoImplicitRecv` runs, every method Call has explicit Args[0], so the fallback is dead code. Simplify:

Find the block:

```go
if jc.Ctx != nil && jc.Ctx.Pkg != nil {
    for _, f := range jc.Ctx.Pkg.Funcs {
        if f.Receiver == receiverName && f.Name == method {
            if len(args) == 0 && jc.Ctx.Component != nil && jc.Ctx.Component.Name == receiverName {
                return receiverName + "_" + method + "(state)"
            }
            return receiverName + "_" + method + "(" + strings.Join(args, ", ") + ")"
        }
    }
}
```

Replace with:

```go
if jc.Ctx != nil && jc.Ctx.Pkg != nil {
    for _, f := range jc.Ctx.Pkg.Funcs {
        if f.Receiver == receiverName && f.Name == method {
            return receiverName + "_" + method + "(" + strings.Join(args, ", ") + ")"
        }
    }
}
```

The `state` injection happens at the IR level now (via the synthesized component-self ident → translated to `state` in Task 5). Removing the branch here keeps the JS code path single-purpose.

- [ ] **Step 9.2: Apply same simplification in `translate_ir.go`**

Run: `grep -n "Ctx.Pkg.Funcs\|f.Receiver == receiverName" codegen/lang/javascript/translate_ir.go`

If the same fallback exists in `translate_ir.go`'s `translateIRTypeMethodCall`, simplify identically.

- [ ] **Step 9.3: Build + test**

Run: `go build ./...`
Run: `go test ./codegen/lang/javascript/... -count=1`
Expected: clean.

- [ ] **Step 9.4: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/translate_ir.go
git commit -m "js: drop empty-args state fallback in evalTypeMethodCall

passNoImplicitRecv now guarantees every method Call has explicit
Args[0]. Codegen drops the conditional that injected \`state\` for
empty-args calls; the synthesized component-self ident in Args[0]
translates to \`state\` via evalIdent's new component branch.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Re-enable issue #75 component-method desugaring

**Files:**
- Modify: `internal/checker/checker.go` (in `registerComponent`, around line 990-1050)

- [ ] **Step 10.1: Locate the closure-only branch**

Run: `grep -n "Component nested funcs keep closure\|nestedFuncs " internal/checker/checker.go`

Find the comment block in `registerComponent` describing why component nested funcs stay as closures. The body has:

```go
case *ast.FuncDef:
    // Component nested funcs keep closure semantics — built directly,
    // stored on the component's Funcs slice, never desugared to a
    // method on a synthetic `this` receiver. Re-attempting the
    // desugaring breaks HTML codegen's reactive prop/binding paths
    // and several testrunner fixtures that bare-reference component
    // vars across method boundaries. Tracked as a follow-up.
    fn := c.buildFunc(s)
    irComp.Funcs = append(irComp.Funcs, fn)
```

- [ ] **Step 10.2: Replace with desugaring**

```go
case *ast.FuncDef:
    nestedFuncs = append(nestedFuncs, s)
```

Add `var nestedFuncs []*ast.FuncDef` at the top of the function (before the `for _, stmt := range comp.Body.Stmts` loop).

After the loop completes, replace the trailing:

```go
c.pkg.Components = append(c.pkg.Components, irComp)
c.symtab.Comps[irComp.Name] = irComp
c.scope.Declare(irComp)
```

with:

```go
c.pkg.Components = append(c.pkg.Components, irComp)
c.symtab.Comps[irComp.Name] = irComp
c.scope.Declare(irComp)

irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nestedFuncs)
```

- [ ] **Step 10.3: Run full suite**

Run: `go test ./... -count=1 -short`
Expected: pass, except for pre-existing testrunner failures unrelated to HTML codegen.

In particular, `TestTodoApp`, `TestFullExample`, `TestFullFixture` in `codegen/platform/html/` should now pass — the lifted `function main_status(state)` is emitted once, the call site is `main_status(state)`, and the names match.

- [ ] **Step 10.4: Investigate any remaining test failures**

If `TestTodoApp` still fails, dump the generated HTML for the todo example:

```bash
go install ./cmd/sngl
cd examples/todo && sngl compile --lang go --platform bubbletea
```

Inspect the generated `model.go` / output for symbol mismatches. Common causes:
- `state` reference inside a Go output (Bubbletea uses `m`, not `state`). Add the same component-self translation for Go (Task 11).
- Test fixtures (`codegen/platform/html/internal/webtest/*` or `html_test.go` expected strings) referencing the old `$status()` form. Update them to `main_status(state)`.

- [ ] **Step 10.5: Commit**

```bash
git add internal/checker/checker.go
git commit -m "checker: re-enable component method desugaring (issue #75)

Now possible because passNoImplicitRecv normalizes every method Call
to explicit-recv form, and the JS codegen translates synthetic
component-self idents to \`state\`. HTML codegen emits desugared
methods uniformly as \`<Receiver>_<Name>(state, ...)\`.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 11: Bubbletea / Fyne Go-target component-self translation

**Files:**
- Modify: `codegen/lang/golang/ircontext.go`

The Go translator follows the same pattern as JS but emits `m` (the Bubbletea Model receiver) instead of `state`.

- [ ] **Step 11.1: Locate `evalIdent` in `ircontext.go`**

Run: `grep -n "func.*evalIdent" codegen/lang/golang/ircontext.go | head`

- [ ] **Step 11.2: Add component-self branch**

Add inside `evalIdent`, before the generic name-resolution fallback:

```go
// Component-self ident: synthesized by passNoImplicitRecv as the
// implicit receiver of a desugared component method. Go emission uses
// `m` for the Bubbletea/Fyne Model receiver.
if _, ok := n.Sym.(*ir.Component); ok {
    return "m"
}
```

- [ ] **Step 11.3: Mirror in `translate_ir.go`**

Run: `grep -n "func translateIRIdent" codegen/lang/golang/translate_ir.go`

Add the same branch.

- [ ] **Step 11.4: Run bubbletea + fyne tests**

Run: `go test ./codegen/platform/bubbletea/... ./codegen/platform/fyne/... -count=1 -short`
Expected: pass. These platforms don't opt into `NoImplicitRecv` yet, but the new branch is harmless — it only fires when the IR has a synthetic component-self ident, which can't happen until the pass runs.

If bubbletea/fyne tests fail, investigate: perhaps an existing ir.Ident with Sym=*ir.Component was previously translated as the component's name (e.g. `main` as a Go identifier). The new branch changes that to `m`. Check whether any existing test expects the old behavior.

- [ ] **Step 11.5: Commit**

```bash
git add codegen/lang/golang/ircontext.go codegen/lang/golang/translate_ir.go
git commit -m "go: translate component-self ident to model receiver

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 12: Full verification

- [ ] **Step 12.1: `go tool verify`**

Run: `go tool verify`
Expected: PASS, except for pre-existing testrunner failures (`test_reactivity_*`, `test_mutation_*` in `codegen/platform/none/testrunner`). These test the interp, not the new codegen path.

- [ ] **Step 12.2: `go fmt ./...`**

Run: `go fmt ./...`
Expected: no diff.

- [ ] **Step 12.3: Install CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 12.4: Smoke-test the todo example**

Run: `cd examples/todo && sngl compile --lang go --platform bubbletea`

Inspect `model.go`:
- No `/* unresolved method main.status */` anywhere.
- `func (m Model) status() string { ... }` OR `func main_status(m Model) string { ... }` defined exactly once.
- Call site uses the matching name.

- [ ] **Step 12.5: Smoke-test the showcase example (if it exists)**

If `examples/showcase/` is in the tree, repeat the compile + inspect for both Go and HTML output.

- [ ] **Step 12.6: Final cleanup commit if any stragglers**

```bash
git add -A
git commit -m "normalize-method-calls: final cleanup"
```

---

## Self-Review

**Spec coverage:**
- §Pattern A description → Task 2 (lowerNoImplicitRecv handles the `len(Args) < len(Params)` case).
- §Pattern B description → Task 2 (skipped when `len(Args) >= len(Params)`).
- §Normalization via lowering → Tasks 1-4 (Caps flag, pass, tests, pipeline wiring).
- §Per-target component-self ident → Tasks 5 (JS) + 11 (Go).
- §HTML dedupe pkg.Funcs vs main.Funcs → Task 7.
- §Single JS emission convention → Tasks 8 + 9.
- §Issue #75 re-enable → Task 10.

**Placeholder scan:** none. Every step has actual code or exact commands.

**Type consistency:**
- `passNoImplicitRecv` defined in Task 2, registered in Task 4 — same name.
- `Caps.NoImplicitRecv` introduced in Task 1, set in Task 6 — same name.
- JS naming convention `<Receiver>_<Name>` used in Tasks 5, 7, 8, 9 — consistent (no underscore-vs-dollar divergence).
- `state` for JS component-self in Tasks 5, 9; `m` for Go component-self in Task 11 — different per target, internally consistent.

**Flagged-for-verification notes:**
- Task 4: pass-order rationale assumes `passNoInlineComponents` runs before `passReactivity` in the existing pipeline. Verify by reading `internal/lower/lower.go`'s passes slice.
- Task 5: the existing `evalIdent` may have early-out paths (e.g. component-namespace handling) that need to come AFTER the component-self branch so this synthetic ident takes precedence.
- Task 8: emit-dedupe relies on `codegen.IsComputed` agreeing between the two emit paths. The check should be identical.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-21-normalize-method-calls.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
