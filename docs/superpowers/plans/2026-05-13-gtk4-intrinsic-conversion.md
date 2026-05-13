# GTK4 Intrinsic Conversion + IR-Based Translator Implementation Plan (Plan C)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert gtk4 to consume the lowered IR stream the same way Plans B+B.2 converted fyne — AND refactor the `IntrinsicTranslator` interface so platforms produce **`[]ir.Stmt`** rather than language-specific source strings. The translator becomes language-agnostic (knows GTK API names but not Go syntax); a language renderer (today: `golang.GoIRContext.EvalStmt`) renders the IR to source. After this plan, gtk4 platform code knows nothing about Go syntax, and Go language code knows nothing about GTK — both interact only through the canonical IR. Back-port fyne to the new interface in the same plan so the two platforms stay consistent.

**Architecture:** Existing `*ir.Func` already supports native foreign-function refs via `NativePkg`/`NativeName` (rendered by `codegen/lang/golang/ircontext.go:354` as `C.gtk_label_new(...)` when `NativePkg == "C"`). Plan C exploits this: the gtk4 translator emits `ir.Call` structures with `NativeName: "C.gtk_*"` and uses `ir.Conversion`/`ir.Assign` for casts and Model-field writes. The Go renderer handles all syntax. Adding `context.Context` to translator methods now (rather than later) prepares for future cross-cutting concerns (multi-frontend support, per-call diagnostics, scope tracking).

**Tech Stack:** Go, cgo, GTK4 C API, GIR (`codegen/platform/gtk4/gir/`), the existing `*ir.Func.NativePkg/NativeName` mechanism, `context.Context`, the codegen `IntrinsicTranslator` interface from Plan B.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` §2.3 (gtk4 conversion).

**Predecessors:** Plans A, B, B.2 (all on `main`).

**Successor:** Plan D (html conversion using the same IR-based interface) and Plan E (final audit).

---

## Why the architecture change

The user surfaced the following architectural constraint during Plan C drafting:

> All Go-related logic stays in the go language. We'll be supporting other languages with gtk4 (eg: C, Rust, python) using the C-API abstraction at some point in the future. It's also important that Go remains gtk4 agnostic, so it can support other platforms (like Qt and Win32) over a C-API abstraction.

Concretely: Plan B's fyneTranslator emits `m.btn.SetText(...)` strings directly. That's fine because Fyne IS a Go library — no cross-language reuse is possible anyway. But Plan C as originally drafted had gtk4Translator emit `C.gtk_label_new()` strings, which couples gtk4 to Go syntax. A future Rust frontend wanting to use GTK4 couldn't reuse gtk4Translator.

The fix: the translator emits **IR** (`ir.Call` with NativeName, `ir.Assign`, `ir.Conversion`). The language renderer (Go: `gc.EvalStmt/EvalExpr`; future Rust: rust renderer) emits target syntax. Same IR feeds every language.

The `*ir.Func.NativePkg/NativeName` mechanism (used today for things like `fmt.Sprintf` imports) extends cleanly to C functions via `NativePkg: "C", NativeName: "C.gtk_label_new"`. Renderer already handles this case.

---

## File Structure

**Modify (substantially):**
- `codegen/intrinsic_walker.go` — interface signature: `[]string` → `[]ir.Stmt`, expression methods `string` → `ir.Expr`, all methods take `context.Context`.
- `codegen/intrinsic_walker_test.go` — trace stub matches new interface.
- `codegen/platform/fyne/intrinsic_translator.go` — back-port to new interface; methods produce IR shapes.
- `codegen/platform/fyne/intrinsic_translator_test.go` — assertions updated.
- `codegen/platform/fyne/compiler_ir.go` — emission path feeds translator output through `gc.EvalStmt`.
- `codegen/platform/gtk4/gtk4.go` — `Capabilities()` adds `NoDeclarative`.
- `codegen/platform/gtk4/compiler_ir.go` — replace tree walker with `WalkLowered` + IR-emission boundary; emit `__renderSlot<N>` Funcs.
- `codegen/platform/gtk4/view_ir.go` — most of file goes away (parallel pipeline rip).
- `codegen/platform/gtk4/scaffold.go`, `codegen/platform/gtk4/templates/model.go.tmpl` — drop updater fields, `/*REACTIVE_REFRESH*/` slot.

**Create:**
- `codegen/platform/gtk4/intrinsic_translator.go` — `gtk4Translator` implementing the new interface; emits `ir.Stmt`s.
- `codegen/platform/gtk4/intrinsic_translator_test.go` — unit tests.
- `codegen/platform/gtk4/intrinsic_integration_test.go` — reactive-if compile-checked end-to-end.

**Reference (no changes):**
- `codegen/lang/golang/ircontext.go` — `EvalStmt`/`EvalExpr`. We rely on the existing `NativePkg`/`NativeName` rendering path at line 354.
- `internal/lower/` — already complete via Plan A.
- `codegen/platform/gtk4/gir/`, `codegen/platform/gtk4/gtk4.sngl` — GIR loader and stdlib stay.

---

## Phase 0: Interface revision

### Task 1: Add `context.Context` + change leaf returns to `[]ir.Stmt`

**Files:**
- Modify: `codegen/intrinsic_walker.go`
- Modify: `codegen/intrinsic_walker_test.go`

- [ ] **Step 1: Revise the interface**

Replace `IntrinsicTranslator` in `codegen/intrinsic_walker.go`:

```go
package codegen

import (
	"context"

	"git.duckfam.us/jonathan/sngl/ir"
)

// IntrinsicTranslator is implemented by codegen platforms that consume
// lowered IR. Each method returns a slice of IR statements (rather than
// target-language source), so the platform stays language-agnostic — a
// future C, Rust, or Python frontend can render the same IR using its
// own language renderer.
//
// Expression hooks (OnIter, OnCond) return ir.Expr for the same reason.
//
// ctx carries cross-cutting state (e.g. the current language renderer
// reference, per-call diagnostics, scope info). Implementations should
// treat ctx as opaque — only consume keys they registered themselves.
type IntrinsicTranslator interface {
	OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt
	OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt
	OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt
	OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt
	OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt
	OnIter(ctx context.Context, iter ir.Expr) ir.Expr
	OnCond(ctx context.Context, cond ir.Expr) ir.Expr
	OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt
}
```

Important shape changes:
- All methods take `ctx`.
- Leaf methods return `[]ir.Stmt` (not `[]string`).
- Reference args become `ir.Expr` (not `string`).
- `slot *ir.Var` is the actual Var pointer (lookup from name happens in the walker).
- Expression methods return `ir.Expr` (transformed expression).

- [ ] **Step 2: Revise WalkLowered to return `[]ir.Stmt`**

```go
// WalkLowered iterates the lowered IR statement sequence and dispatches
// each known shape to t. Returns the concatenated emissions in source
// order. ctx is passed through to every translator method; if the
// caller has no context, pass context.Background().
func WalkLowered(ctx context.Context, stmts []ir.Stmt, t IntrinsicTranslator) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, walkOne(ctx, s, t)...)
	}
	return out
}

func walkOne(ctx context.Context, s ir.Stmt, t IntrinsicTranslator) []ir.Stmt {
	switch n := s.(type) {
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
			tag, _ := extractStringLit(call.Args[0].Value)
			return t.OnCreateNode(ctx, n.Name, tag)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			switch {
			case isLowerIntrinsic(n.Call, "AppendChild"):
				return t.OnAppendChild(ctx, n.Call.Args[0].Value, n.Call.Args[1].Value)
			case isLowerIntrinsic(n.Call, "RemoveChild"):
				return t.OnRemoveChild(ctx, n.Call.Args[0].Value, n.Call.Args[1].Value)
			case isLowerIntrinsic(n.Call, "AttachHandler"):
				evt, _ := extractStringLit(n.Call.Args[1].Value)
				return t.OnAttachHandler(ctx, n.Call.Args[0].Value, evt, n.Call.Args[2].Value)
			}
		}
	case *ir.Assign:
		if id, ok := n.Target.(*ir.Ident); ok && id.Synthesized {
			if ll, ok := n.Value.(*ir.ListLit); ok && len(ll.Elems) == 0 {
				if v, ok := id.Sym.(*ir.Var); ok {
					return t.OnSlotReset(ctx, v)
				}
			}
			if call, ok := n.Value.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "ListPush" && len(call.Args) == 2 {
				if v, ok := id.Sym.(*ir.Var); ok {
					return t.OnSlotAppend(ctx, v, call.Args[1].Value)
				}
			}
		}
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(ctx, sel.Operand, sel.Field, n.Value)
			}
		}
	case *ir.For:
		iterExpr := t.OnIter(ctx, n.Iter)
		body := WalkLowered(ctx, n.Body, t)
		// Rebuild the For with the translated iter and body.
		return []ir.Stmt{&ir.For{
			Key:   n.Key,
			Value: n.Value,
			Iter:  iterExpr,
			Body:  body,
			Else:  n.Else,
		}}
	case *ir.If:
		condExpr := t.OnCond(ctx, n.Cond)
		body := WalkLowered(ctx, n.Body, t)
		var elseBody []ir.Stmt
		if len(n.Else) > 0 {
			elseBody = WalkLowered(ctx, n.Else, t)
		}
		return []ir.Stmt{&ir.If{
			Cond: condExpr,
			Body: body,
			Else: elseBody,
		}}
	}
	return t.OnDefault(ctx, s)
}
```

Note: For/If now produce `ir.Stmt` wrappers. The body is recursively translated. Iter and Cond go through translator hooks.

Slot var lookup: `id.Sym.(*ir.Var)` requires the Synthesized Ident to carry `Sym` pointing to the actual `*ir.Var`. Plan A's `synthesizeSlotVar` creates the Var but doesn't always set Sym on the Idents that reference it. Verify by inspecting `internal/lower/reactivity.go` — if Idents don't carry Sym, set it during emission. If that's a non-trivial change, fall back to looking up the slot by name in the surrounding owner's Vars (less clean but functional).

For now, fall back: if `id.Sym` is nil but `id.Synthesized` is true and `id.Name` starts with `__slot`, look up by name. Add a helper:

```go
func resolveSynthVar(id *ir.Ident, owner ?) *ir.Var {
    if v, ok := id.Sym.(*ir.Var); ok {
        return v
    }
    // fallback: name-based lookup. Requires ctx to carry the owner.
    return nil
}
```

For Plan C scope: accept the fallback as a small known limitation. The "ctx carries owner" pattern can be added later.

- [ ] **Step 3: Update walker tests**

In `codegen/intrinsic_walker_test.go`, the `trace` stub now implements the new interface. Methods accept `ctx` and return `[]ir.Stmt`/`ir.Expr`:

```go
type trace struct{ lines []string }

func (t *trace) OnCreateNode(_ context.Context, id, tag string) []ir.Stmt {
	t.add("create %s %s", id, tag); return nil
}
func (t *trace) OnAppendChild(_ context.Context, p, c ir.Expr) []ir.Stmt {
	t.add("append %s %s", identName(p), identName(c)); return nil
}
func (t *trace) OnRemoveChild(_ context.Context, p, c ir.Expr) []ir.Stmt {
	t.add("remove %s %s", identName(p), identName(c)); return nil
}
func (t *trace) OnAttachHandler(_ context.Context, n ir.Expr, e string, h ir.Expr) []ir.Stmt {
	t.add("attach %s %s %s", identName(n), e, identName(h)); return nil
}
func (t *trace) OnPropAssign(_ context.Context, n ir.Expr, p string, v ir.Expr) []ir.Stmt {
	t.add("prop %s %s", identName(n), p); return nil
}
func (t *trace) OnSlotReset(_ context.Context, s *ir.Var) []ir.Stmt {
	t.add("reset %s", s.Name); return nil
}
func (t *trace) OnSlotAppend(_ context.Context, s *ir.Var, c ir.Expr) []ir.Stmt {
	t.add("append-slot %s %s", s.Name, identName(c)); return nil
}
func (t *trace) OnIter(_ context.Context, e ir.Expr) ir.Expr { return e }
func (t *trace) OnCond(_ context.Context, e ir.Expr) ir.Expr { return e }
func (t *trace) OnDefault(_ context.Context, s ir.Stmt) []ir.Stmt {
	t.add("default %T", s); return nil
}
func (t *trace) add(f string, args ...any) { t.lines = append(t.lines, fmt.Sprintf(f, args...)) }
```

Update existing tests to pass `context.Background()` to `WalkLowered`. Existing tests built `*ir.Ident` directly for refs; that still works since the new interface accepts `ir.Expr`.

Some tests passed a Synthesized Ident WITHOUT `Sym: <Var>`. Those will fail the slot tests because the new walker requires `id.Sym` to be a `*ir.Var` for slot recognition. Build the Vars properly in test fixtures or relax the walker's strictness.

For TestWalkLowered_SlotReset and TestWalkLowered_SlotAppend — construct an `*ir.Var` and set `id.Sym` on the Ident.

- [ ] **Step 4: Run**

```bash
go test ./codegen/...
```

Expected: walker tests pass. Existing platform tests (fyne) will break because fyne hasn't been ported yet — that's expected.

- [ ] **Step 5: Commit**

```bash
git add codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go
git commit -m "codegen: IntrinsicTranslator returns []ir.Stmt + takes context.Context

Translator methods now produce IR fragments rather than language-
specific source strings. The Go renderer (gc.EvalStmt) renders the
IR to source. Decouples platforms from language syntax: a future C/
Rust/Python frontend can reuse a translator by writing its own
renderer.

WalkLowered returns []ir.Stmt; the platform emitter feeds the
result through gc.EvalStmt at the source-emission boundary.

Fyne tests break here intentionally; Task 2 ports fyne.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 1: Back-port fyne to the new interface

### Task 2: Rewrite fyneTranslator methods to produce IR

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Modify: `codegen/platform/fyne/intrinsic_translator_test.go`

Each fyneTranslator method now returns `[]ir.Stmt`. Concretely:

- `OnCreateNode(ctx, id, tag)` returns a single `ir.Assign` setting `m.<id>` to a call expression matching the blueprint constructor.
- `OnAppendChild(ctx, parent, child)` returns a `*ir.CallStmt` for `<parent>.Add(<child>)`.
- `OnPropAssign(ctx, node, prop, value)` returns a `*ir.CallStmt` for the setter call (e.g. `m.__n0.SetText(fmt.Sprint(value))`).

Each "method call" target is rendered via `ir.Select` (operand is the receiver, field is the method name). The synthesized `*ir.Func` for the setter carries no Intrinsic flag; gc.EvalExpr already handles the method-call shape.

- [ ] **Step 1: Build helper constructors**

Add to `codegen/platform/fyne/intrinsic_translator.go`:

```go
import (
	"context"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// modelFieldRef returns an *ir.Ident for `m.<name>` — used for
// synthesized Model fields like __n0, __slot0.
func modelFieldRef(name string) *ir.Ident {
	return &ir.Ident{Name: name, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
}

// fyneMethodCall builds a Call ir.Expr for `receiver.Method(args...)`.
// gc.EvalExpr renders this as Go method-call syntax. methodFunc is a
// synthesized *ir.Func with Name=method; gc walks Receiver.Field.
func fyneMethodCall(receiver ir.Expr, method string, args []ir.Expr, retType *ir.Type) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     retType,
		Receiver: receiver,
		Func:     &ir.Func{Name: method},
		Args:     callArgs,
	}
}

// goFmtSprintCall wraps an expression in fmt.Sprint(...). Used by
// blueprint transforms (e.g. text's value prop).
func goFmtSprintCall(arg ir.Expr) *ir.Call {
	return &ir.Call{
		Type:    ir.TypString,
		Func:    &ir.Func{Name: "Sprint", NativePkg: "fmt", NativeName: "fmt.Sprint"},
		Args:    []ir.CallArg{{Value: arg}},
	}
}
```

- [ ] **Step 2: Rewrite OnCreateNode**

```go
func (t *fyneTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	bp, ok := t.blueprints[tag]
	if !ok || bp.Constructor == nil || bp.Constructor.GoType == "" {
		return nil
	}
	t.fieldSink(id, bp.Constructor.GoType)
	t.idTags[id] = tag

	// Build the constructor call:
	//   m.<id> = <ctor>(<zeroArgs>)
	// Constructor is a NativePkg-flagged Func so gc.EvalExpr emits the
	// fully-qualified Go name (e.g. "widget.NewLabel").
	ctor := &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{
			NativePkg:  bp.Constructor.GoFn, // dummy; gc only checks for non-empty NativePkg
			NativeName: bp.Constructor.GoFn,
		},
		Args: zeroArgsCallArgs(bp.Constructor.ZeroArgs),
	}

	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  ctor,
	}}
}

// zeroArgsCallArgs parses a blueprint's ZeroArgs string into ir.CallArg
// values. For minimal Plan C scope, supports empty (no args), a single
// `""` (empty-string arg for text/label), and `"", nil` (button).
func zeroArgsCallArgs(zeroArgs string) []ir.CallArg {
	switch zeroArgs {
	case "":
		return nil
	case `""`:
		return []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: ""}}}
	case `"", nil`:
		return []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Raw: ""}},
			{Value: &ir.Literal{Type: ir.TypNull}},
		}
	}
	return nil
}
```

Note: this is a transitional implementation. The blueprint's `ZeroArgs` is currently a string designed for direct emission. A cleaner end-state is for blueprints to carry typed zero-args (a `[]ir.Expr` or similar). Plan C accepts the parser for now and lists it as a follow-up.

- [ ] **Step 3: Rewrite OnAppendChild / OnRemoveChild**

```go
func (t *fyneTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	return []ir.Stmt{&ir.CallStmt{Call: fyneMethodCall(parent, "Add", []ir.Expr{child}, ir.TypVoid)}}
}

func (t *fyneTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	return []ir.Stmt{&ir.CallStmt{Call: fyneMethodCall(parent, "Remove", []ir.Expr{child}, ir.TypVoid)}}
}

// qualifyParentExpr converts a slot-function `parent` ident param into
// the type-asserted `container` local (matching emitIRSlotFunc's prologue).
// Plan B.2 Task 6 simplified this to a typed parameter; this preserves
// that.
func (t *fyneTranslator) qualifyParentExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok && id.Name == "parent" {
		return &ir.Ident{Name: "container", Type: ir.TypDyn}
	}
	return e
}

// qualifyChildExpr prefixes a Synthesized widget-ref ident with "m." so
// it resolves through the Model receiver. The Synthesized flag (set by
// passReactivity / passDeclarative) carries this signal.
func (t *fyneTranslator) qualifyChildExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok && id.Synthesized {
		return &ir.Select{Operand: &ir.Ident{Name: "m"}, Field: id.Name, Type: id.Type}
	}
	return e
}
```

Hmm, `&ir.Select{Operand: &ir.Ident{Name: "m"}, Field: id.Name}` — that's the IR shape for `m.<name>`. gc.EvalExpr renders it correctly.

But the current Go code path for `id.Synthesized` is already in gc.EvalExpr (added in Plan B.2 Task 9). So we don't actually need to wrap in Select — passing the Synthesized Ident directly through to gc.EvalExpr already renders as `m.<name>`. Simplify:

```go
func (t *fyneTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent = t.qualifyParentExpr(parent)
	// child is already a Synthesized Ident; gc.EvalExpr renders it as m.<name>.
	return []ir.Stmt{&ir.CallStmt{Call: fyneMethodCall(parent, "Add", []ir.Expr{child}, ir.TypVoid)}}
}
```

Verify by reading the existing EvalExpr path. If it renders Synthesized Idents correctly, drop `qualifyChildExpr`.

- [ ] **Step 4: Rewrite OnAttachHandler**

```go
func (t *fyneTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	bareID := identBareName(node)
	tag, ok := t.idTags[bareID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	var target string
	for _, b := range bp.Bindings {
		if b.Kind == bindEvent && b.Prop == event {
			target = b.Target
			break
		}
	}
	if target == "" {
		return nil
	}
	// target is like ".OnTapped"; strip the leading dot for the field name.
	fieldName := strings.TrimPrefix(target, ".")

	// Emit: m.<id>.<field> = handler
	receiver := node
	if id, ok := receiver.(*ir.Ident); ok && id.Synthesized {
		// already qualified through gc.EvalExpr; leave as-is.
	}

	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{
			Operand: receiver,
			Field:   fieldName,
			Type:    ir.TypDyn,
		},
		Op:    ast.AssignSet,
		Value: handler,
	}}
}

// identBareName returns the unqualified name of an Ident, stripping
// any "m." prefix that came pre-qualified.
func identBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return strings.TrimPrefix(id.Name, "m.")
	}
	return ""
}
```

- [ ] **Step 5: Rewrite OnPropAssign**

```go
func (t *fyneTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bareID := identBareName(node)
	tag, ok := t.idTags[bareID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	var target, transform string
	for _, b := range bp.Bindings {
		if (b.Kind == bindReactive || b.Kind == bindInit) && b.Prop == prop {
			target = b.Target
			transform = b.Transform
			break
		}
	}
	if target == "" {
		return nil
	}
	methodName := strings.TrimPrefix(target, ".")
	if transform != "" {
		// Wrap value in transform call (e.g. fmt.Sprint).
		value = &ir.Call{
			Type: ir.TypString,
			Func: &ir.Func{NativePkg: transform, NativeName: transform},
			Args: []ir.CallArg{{Value: value}},
		}
	}
	return []ir.Stmt{&ir.CallStmt{Call: fyneMethodCall(node, methodName, []ir.Expr{value}, ir.TypVoid)}}
}
```

- [ ] **Step 6: Rewrite OnSlotReset / OnSlotAppend / OnIter / OnCond / OnDefault**

```go
func (t *fyneTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	// m.__slotN = nil
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}}
}

func (t *fyneTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	// m.__slotN = append(m.__slotN, child)
	slotRef := modelFieldRef(slot.Name)
	appendCall := &ir.Call{
		Type: slot.Type,
		Func: &ir.Func{Name: "append", NativePkg: "builtin", NativeName: "append"},
		Args: []ir.CallArg{
			{Value: slotRef},
			{Value: child},
		},
	}
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendCall,
	}}
}

func (t *fyneTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	// Synthesized Idents resolve as m.<name> via gc.EvalExpr's existing
	// path. Plain Idents stay bare.
	return iter
}

func (t *fyneTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *fyneTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
```

Note: `OnDefault` no longer calls `gc.EvalStmt`. The translator returns the stmt as-is; the platform emitter calls `gc.EvalStmt` over the full collected stream at the source-emission boundary.

- [ ] **Step 7: Update emitIRSlotFunc to feed through gc.EvalStmt**

In `codegen/platform/fyne/compiler_ir.go`'s `emitIRSlotFunc`:

```go
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField) {
	fmt.Fprintf(b, "func (m *Model) %s(container *fyne.Container) {\n", fn.Name)

	tr := newFyneTranslator(gc, platformBlueprints(), func(name, goType string) {
		*widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
	})

	ctx := context.Background()
	body := codegen.WalkLowered(ctx, fn.Block, tr)
	for _, stmt := range body {
		for _, line := range gc.EvalStmt(stmt) {
			b.WriteString("\t")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("}\n\n")
}
```

Same for BuildUI emission (when the body walks through WalkLowered there too).

- [ ] **Step 8: Update fyne unit tests**

The translator tests in `codegen/platform/fyne/intrinsic_translator_test.go` now need to:
- Pass `context.Background()` to translator methods.
- Assert against `[]ir.Stmt` returns OR feed through `gc.EvalStmt` to get strings.

The simpler path: feed through `gc.EvalStmt`. Tests stay shape-comparable. Update each test:

```go
func TestFyneTranslator_OnCreateNode_Text(t *testing.T) {
	gc := stubGC()
	var fields []string
	tr := newFyneTranslator(gc, platformBlueprints(), func(name, goType string) {
		fields = append(fields, name+" "+goType)
	})
	stmts := tr.OnCreateNode(context.Background(), "__n0", "text")
	var lines []string
	for _, s := range stmts {
		lines = append(lines, gc.EvalStmt(s)...)
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "m.__n0 = widget.NewLabel(") {
		t.Errorf("expected widget.NewLabel; got: %s", got)
	}
	// ... etc
}
```

Apply this pattern to every test.

- [ ] **Step 9: Run tests + integration test + verify**

```bash
go test ./codegen/platform/fyne/...
```

Expected: all unit tests pass. Integration test may need snippet adjustments if the emitted Go's exact form shifted (e.g. extra parens around a converted method call).

```bash
go tool verify 2>&1 | grep -E "^(---|not ok)" | tail -5
```

Expected: same baseline or fewer failures than post-Plan-B.2 state.

- [ ] **Step 10: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: back-port translator to []ir.Stmt + context.Context interface

fyneTranslator methods now produce IR fragments rather than Go source.
emitIRSlotFunc feeds the collected stmts through gc.EvalStmt at the
emission boundary. Decouples fyne from Go syntax — same translator
could be reused by a future C/Rust frontend that brings its own
language renderer.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2: Build gtk4Translator

### Task 3: Scaffold gtk4Translator with interface compliance

**Files:**
- Create: `codegen/platform/gtk4/intrinsic_translator.go`

Mirror Plan B Task 1's scaffolding. All methods return `nil` initially.

```go
package gtk4

import (
	"context"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

type gtk4Translator struct {
	gc        *golang.GoIRContext
	fieldSink func(name, cType string)
	idCTypes  map[string]string // id ("__n0") → GTK C type ("GtkLabel")
	topLevel  []string
}

func newGtk4Translator(gc *golang.GoIRContext, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{gc: gc, fieldSink: fieldSink, idCTypes: map[string]string{}}
}

var _ codegen.IntrinsicTranslator = (*gtk4Translator)(nil)

// All methods return nil; subsequent tasks fill in.
func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt { return nil }
func (t *gtk4Translator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt { return nil }
func (t *gtk4Translator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt { return nil }
func (t *gtk4Translator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt { return nil }
func (t *gtk4Translator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt { return nil }
func (t *gtk4Translator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt { return nil }
func (t *gtk4Translator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt { return nil }
func (t *gtk4Translator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr { return iter }
func (t *gtk4Translator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr { return cond }
func (t *gtk4Translator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt { return []ir.Stmt{stmt} }
```

- [ ] **Step 1: Create the file**, build, commit.

```bash
go build ./codegen/platform/gtk4/...
git add codegen/platform/gtk4/intrinsic_translator.go
git commit -m "gtk4: scaffold gtk4Translator (IntrinsicTranslator impl)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: gtk4Translator.OnCreateNode emits cgo call via IR

**Files:**
- Modify: `codegen/platform/gtk4/intrinsic_translator.go`
- Create: `codegen/platform/gtk4/intrinsic_translator_test.go`

Each method that previously emitted `C.gtk_<fn>(...)` strings now emits `*ir.Call` with `NativePkg: "C"` and `NativeName: "C.gtk_<fn>"`. gc.EvalExpr renders these correctly per `codegen/lang/golang/ircontext.go:354`.

- [ ] **Step 1: Add tag/constructor helpers**

```go
// gtk4TagToCType maps a SNGL stdlib tag to its GTK C type.
func gtk4TagToCType(tag string) string {
	switch tag {
	case "text", "label":   return "GtkLabel"
	case "button":          return "GtkButton"
	case "input", "entry":  return "GtkEntry"
	case "vbox", "hbox":    return "GtkBox"
	case "checkbox":        return "GtkCheckButton"
	case "scroll":          return "GtkScrolledWindow"
	}
	return ""
}

// gtk4Constructor returns the cgo Call ir.Expr for a tag's
// constructor. The Func's NativeName is the cgo path
// (e.g. "C.gtk_label_new") so gc.EvalExpr emits it directly.
func gtk4Constructor(tag string) *ir.Call {
	mk := func(name string, args ...ir.Expr) *ir.Call {
		callArgs := make([]ir.CallArg, len(args))
		for i, a := range args { callArgs[i] = ir.CallArg{Value: a} }
		return &ir.Call{
			Type: ir.TypDyn,
			Func: &ir.Func{NativePkg: "C", NativeName: name},
			Args: callArgs,
		}
	}
	nullLit := &ir.Literal{Type: ir.TypNull}
	emptyStr := &ir.Literal{Type: ir.TypString, Raw: ""}
	switch tag {
	case "text", "label":
		return mk("C.gtk_label_new", nullLit)
	case "button":
		// gtk_button_new_with_label("") via C.CString wrapper
		cStringCall := mk("C.CString", emptyStr)
		return mk("C.gtk_button_new_with_label", cStringCall)
	case "input", "entry":
		return mk("C.gtk_entry_new")
	case "vbox":
		orient := &ir.Literal{Type: ir.TypDyn, Raw: "C.GTK_ORIENTATION_VERTICAL"}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return mk("C.gtk_box_new", orient, spacing)
	case "hbox":
		orient := &ir.Literal{Type: ir.TypDyn, Raw: "C.GTK_ORIENTATION_HORIZONTAL"}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return mk("C.gtk_box_new", orient, spacing)
	case "checkbox":
		return mk("C.gtk_check_button_new")
	case "scroll":
		return mk("C.gtk_scrolled_window_new")
	}
	return nil
}
```

Note the `&ir.Literal{Type: ir.TypDyn, Raw: "C.GTK_ORIENTATION_VERTICAL"}` for the orientation enum — this is a slight abuse since the Raw isn't a Go literal value but a Go identifier. gc.EvalExpr for `*ir.Literal` with TypDyn currently emits Raw verbatim — VERIFY this. If it doesn't, use a different shape (e.g. an `ir.Ident` with the C constant as Name).

- [ ] **Step 2: Implement OnCreateNode**

```go
func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	cType := gtk4TagToCType(tag)
	if cType == "" { return nil }
	ctor := gtk4Constructor(tag)
	if ctor == nil { return nil }

	t.fieldSink(id, cType)
	t.idCTypes[id] = cType
	t.topLevel = append(t.topLevel, id)

	// m.<id> = (*C.<cType>)(unsafe.Pointer(ctor()))
	// Build the cast: outer Call is `(*C.<cType>)(...)`, inner is
	// `unsafe.Pointer(...)`. Represent as nested NativeName Calls.
	unsafePtr := &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{NativePkg: "unsafe", NativeName: "unsafe.Pointer"},
		Args: []ir.CallArg{{Value: ctor}},
	}
	castCall := &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{NativePkg: "C", NativeName: "(*C." + cType + ")"},
		Args: []ir.CallArg{{Value: unsafePtr}},
	}
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  castCall,
	}}
}
```

The `(*C.GtkLabel)` cast is modeled as a "function" with that NativeName. gc.EvalExpr will emit `(*C.GtkLabel)(unsafe.Pointer(C.gtk_label_new(nil)))` — which is what we want.

This is a slight abuse of NativeName (it's not really a function name), but it produces correct cgo source. The cleaner long-term solution is a dedicated `ir.Conversion` rendering for native types; flag as follow-up.

- [ ] **Step 3: Write a test**

```go
func TestGtk4Translator_OnCreateNode_Text(t *testing.T) {
	gc := stubGC()
	var fields []string
	tr := newGtk4Translator(gc, func(name, cType string) {
		fields = append(fields, name+" "+cType)
	})
	stmts := tr.OnCreateNode(context.Background(), "__n0", "text")
	if len(stmts) != 1 { t.Fatalf("expected 1 stmt, got %d", len(stmts)) }
	var lines []string
	for _, s := range stmts {
		lines = append(lines, gc.EvalStmt(s)...)
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "C.gtk_label_new") {
		t.Errorf("expected C.gtk_label_new; got: %s", got)
	}
	if !strings.Contains(got, "unsafe.Pointer") {
		t.Errorf("expected unsafe.Pointer cast; got: %s", got)
	}
	if len(fields) != 1 || fields[0] != "__n0 GtkLabel" {
		t.Errorf("expected field __n0 GtkLabel; got: %v", fields)
	}
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/...
git add codegen/platform/gtk4/
git commit -m "gtk4: OnCreateNode emits cgo cast + constructor as ir.Stmt

Translator produces ir.Assign whose value is a nested chain of Calls
(outer = type cast, middle = unsafe.Pointer, inner = constructor)
all flagged with NativePkg='C'. gc.EvalExpr renders the cgo source.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Tasks 5-9: Implement remaining gtk4Translator methods

These follow the same pattern: build IR shapes, gc.EvalExpr renders. One commit each, with a small TDD test.

- [ ] **Task 5**: `OnAppendChild` / `OnRemoveChild` — emit `&ir.CallStmt{Call: <C.gtk_box_append(...)>}` with cgo casts on parent/child. Pull container-type from `t.idCTypes[bareID]` for dispatch (gtk_box vs gtk_window etc.).

- [ ] **Task 6**: `OnAttachHandler` — emit two stmts:
  1. `snglCallbacks = append(snglCallbacks, handler)` as an `ir.Assign`.
  2. `C.sngl_connect(widget, "<signal>", idx)` as a `*ir.CallStmt`.

  The "idx" arg is `len(snglCallbacks) - 1`. Build as an arithmetic ir.BinaryExpr:
  ```go
  &ir.Binary{Op: ast.BinaryMinus, Left: lenCall, Right: oneIntLit}
  ```

- [ ] **Task 7**: `OnPropAssign` — emit `C.gtk_label_set_text((*C.GtkLabel)(unsafe.Pointer(m.__n0)), C.CString(value))` as a CallStmt. The setter table (`gtkSetterTable` at `view_ir.go:1233`) is consulted for the C function name; the value gets wrapped in `C.CString(...)` for string props.

- [ ] **Task 8**: `OnSlotReset` / `OnSlotAppend` — same as fyne pattern but the slot field is `[]*C.GtkWidget`. `OnSlotAppend` casts the child to `(*C.GtkWidget)(unsafe.Pointer(...))` before appending.

- [ ] **Task 9**: `OnIter` / `OnCond` — return the expression unchanged. gc.EvalExpr handles Synthesized Idents.

Each task: failing test, implementation, passing test, commit. Same structure as Plan B Tasks 2-5.

---

## Phase 3: Wire gtk4 emitter to consume slot Funcs

### Task 10: Emit `__renderSlot<N>` Funcs via WalkLowered

**Files:**
- Modify: `codegen/platform/gtk4/compiler_ir.go`

Add `emitIRSlotFunc` analogous to fyne's. The slot Func's container param is `*C.GtkBox`:

```go
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]gtk4WidgetField) {
	fmt.Fprintf(b, "func (m *Model) %s(container *C.GtkBox) {\n", fn.Name)

	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, gtk4WidgetField{Name: name, CType: cType})
	})

	body := codegen.WalkLowered(context.Background(), fn.Block, tr)
	for _, stmt := range body {
		for _, line := range gc.EvalStmt(stmt) {
			b.WriteString("\t")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("}\n\n")
}
```

Wire into the existing Funcs loop:

```go
if fn.Synthesized {
    emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields)
    continue
}
// ... existing path
```

Emit `__slot<N>` and `__root` Vars on Model:

```go
if v.Synthesized {
    if v.Name == "__root" {
        binds = append(binds, gtk4Bind{
            Name: v.Name, GoType: "*C.GtkBox",
            Init: "(*C.GtkBox)(unsafe.Pointer(C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 6)))",
            NoAccessors: true,
        })
        continue
    }
    binds = append(binds, gtk4Bind{
        Name: v.Name, GoType: "[]*C.GtkWidget", Init: "nil", NoAccessors: true,
    })
    continue
}
```

Adapt struct/field names to gtk4's actual scaffold types. Run tests; commit.

### Task 11: Integration test for reactive `if` on gtk4

Mirror Plan B Task 12. Same SNGL fixture (reactive `if visible { text(...) }`), assert emitted Go contains the expected cgo snippets (`C.gtk_label_new`, `C.gtk_label_set_text`, `C.gtk_box_append`, `m.__renderSlot0`, `m.__slot0 = nil`, etc.), and **add the go-build compile check** mirroring Plan B.2 Task 7.

The compile-check needs cgo enabled and GTK4 headers/libraries available on the host. If those aren't present, `t.Skip` the compile portion but keep the snippet assertions.

---

## Phase 4: Enable NoDeclarative + BuildUI rewrite

### Task 12: Enable NoDeclarative

Mirror Plan B.2 Task 12. Breakage is expected.

```go
func (g *Generator) Capabilities() lower.Caps {
    return lower.Caps{NoReactivity: true, NoDeclarative: true}
}
```

### Task 13: Rewrite BuildUI via WalkLowered

Mirror Plan B.2 Task 13. Replace `vc.renderStmt(bodyStmts, "content")` with:

```go
tr := newGtk4Translator(gc, func(name, cType string) {
    widgetFields = append(widgetFields, gtk4WidgetField{Name: name, CType: cType})
})
body := codegen.WalkLowered(context.Background(), bodyStmts, tr)
for _, stmt := range body {
    for _, line := range gc.EvalStmt(stmt) {
        fmt.Fprintf(&buildBuf, "\t%s\n", line)
    }
}
// Top-level refs append to m.__root.
for _, ref := range tr.topLevel {
    fmt.Fprintf(&buildBuf, "\tC.gtk_box_append((*C.GtkBox)(unsafe.Pointer(m.__root)), (*C.GtkWidget)(unsafe.Pointer(m.%s)))\n", ref)
}
```

Update BuildUI's return:

```go
fmt.Fprintf(b, "\treturn (*C.GtkWidget)(unsafe.Pointer(m.__root))\n")
```

Iterate on test breakage. Most likely categories (per Plan B.2 Phase D):
- Untranslated setter mappings (extend gtkSetterTable or add per-tag prop translations in OnPropAssign).
- Missing signal mappings (extend gtk4SignalFor).
- Two-way entry binding (if it appears in fixtures, restore the PreFire pattern as a Plan-C-specific handler emission shim, mirroring Plan B.2's `LoweredFromTag`/`LoweredFromEvent` fix in commit `36b87e3`).

Commit each fix as you go.

---

## Phase 5: Rip parallel pipeline

### Tasks 14-18: Iterative removal

Mirror Plan B.2 Phase E. Each task deletes a cluster; commit per cluster.

- [ ] **Task 14**: Delete `renderConditional` / `renderIf`-with-info-tracking / `renderFor` from `view_ir.go`.
- [ ] **Task 15**: Delete `widgetUpdater` / `updaters` slice / `addUpdater` / `emitUpdaters` / `codegen.FindAffected` call sites. Remove the `info parameter`-driven branching in `renderIf`/`renderFor` if they survive.
- [ ] **Task 16**: Delete `lateReactive` / `recordLateReactive` / `resolveReactiveTokens` / `nodeBindings` / `recordNodeBinding` / `recordWidgetBinding`. Remove `/*SNGLREACT:i*/` and `/*REACTIVE_REFRESH*/` substitutions.
- [ ] **Task 17**: Delete `localMode` / `localWidgets` / `localCount` / the localMode branches in `allocWidget`. Remove `emitIfRefreshMethods` / `emitForRefreshMethods`.
- [ ] **Task 18**: Drop `UpdaterNames` / `AffectedUpdaters` / `doRefresh` template arms from `scaffold.go` and `templates/model.go.tmpl`.

Run `go test ./codegen/platform/gtk4/...` plus `go build ./...` after each. Commit per cluster.

---

## Phase 6: Verify + handoff

### Task 19: `go tool verify` baseline check

Same shape as Plan B.2 Task 25. Document residual breakage (probably more `cmd/sngl` script tests asserting old gtk4 output formats — same fallout pattern as Plan B.2's six failing cases).

### Task 20: Spec handoff annotation

Update `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`'s migration-order block to include Plan C. Note that Plans D (html) and E (audit) inherit the IR-based interface from Plan C — they don't need to re-do the back-port.

---

## What's NOT in Plan C

- **CType representation as a first-class ir.Type kind.** Plan C uses the NativeName backdoor for cgo type casts (`(*C.GtkLabel)` as a synthetic NativeName). Cleaner long-term: add `ir.Type{Kind: TypeNative, Native: "*C.GtkLabel"}` and have gc.EvalExpr render Conversion-to-Native as the cgo cast pattern. Defer to a follow-up cleanup plan.
- **Blueprint typed-ZeroArgs.** The `Constructor.ZeroArgs string` Plan B.2 introduced is parsed-then-re-stringified by `zeroArgsCallArgs`. A typed `Constructor.ZeroArgs []ir.Expr` would be cleaner. Defer.
- **Memory management for `C.CString(...)`.** GTK4 today leaks. Fixing this isn't a Plan C concern; the IR-based pattern makes it easier when it lands (wrap each CString call in a deferred free emission).
- **Multi-window support.** If gtk4's emission has per-window methods that don't fit the WalkLowered pattern, defer to a follow-up. Plan C handles single-window first.
- **Entry two-way binding** (the `@input(e) { name = e.value }` → setter-back-write pattern). gtk4's `connectSignal` had this at `view_ir.go:1011-1018`. If tests show breakage, restore the pattern as a per-event handler-emission shim (mirroring Plan B.2's `LoweredFromTag`/`LoweredFromEvent` fix in commit `36b87e3`). Not pre-planned; surfaces in Task 13's iterative debugging.

These are explicit deferrals; raise as scope decisions if any block.
