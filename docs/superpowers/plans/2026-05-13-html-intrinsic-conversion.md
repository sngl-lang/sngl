# HTML Intrinsic Conversion Implementation Plan (Plan D)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the consolidated reactivity contract to html: build an `htmlTranslator` implementing `codegen.IntrinsicTranslator` (produces IR; JavaScript renderer in `codegen/lang/javascript` emits the JS source). Migrate html's reactive-update layer off `g.updates`/`findAffectedUpdaters`/`loweredID` onto `codegen.WalkLowered`. The tree-walking initial-HTML emitter STAYS (static-site path is the whole point of html-platform's existence).

**Architecture:** Unlike fyne/gtk4, html does NOT enable `NoDeclarative` — pages with zero reactive deps must produce zero JS. After Plan D: the initial HTML body (renderStaticNode, renderStaticBox, renderStaticButton, renderStaticInput, etc.) still emits declarative HTML; the JS update layer (today: `g.updates` + `findAffectedUpdaters` registry, post-walk substitution) becomes WalkLowered output. Reactive prop assigns flow through `htmlTranslator.OnPropAssign` → IR Calls → JsIRContext renders to DOM property assignments. `__renderSlot<N>` Funcs from `passReactivity` get emitted as JS functions.

**Tech Stack:** Go, JavaScript (cgo-free), `codegen/lang/javascript/JsIRContext`, the consolidated `IntrinsicTranslator` interface, the static-HTML emitter that survives.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` §2.4 (html conversion).

**Predecessors:** Plans A, B, B.2, C, F (all on `main`). The IR-fragment interface from Plan C and the language-renderer separation from Plan F are the foundation.

**Successor:** Plan E (final audit).

---

## Key differences from fyne/gtk4 (Plans B/B.2/C)

| Concern                         | fyne/gtk4                                                             | html                                                                                    |
|---------------------------------|-----------------------------------------------------------------------|-----------------------------------------------------------------------------------------|
| NoDeclarative                   | ON — whole body lowered                                               | OFF — declarative tree stays                                                            |
| Initial render                  | Build widget tree imperatively at BuildUI time                        | Render static HTML once, embed in document body                                         |
| Reactive updates                | Spliced setter Assigns in handler bodies → translated to setter calls | Spliced setter Assigns in handler bodies → translated to DOM property assignments in JS |
| Zero-reactivity case            | Still produces full Model + BuildUI scaffolding                       | Zero JS — pure static HTML                                                              |
| Target language                 | Go (gc.EvalStmt)                                                      | JavaScript (jc.EvalStmt)                                                                |
| Storage of node refs            | `m.__nN` field on Model                                               | `document.querySelector('[data-sngl-id="__nN"]')` lookup OR cached `const __nN = ...`   |
| Container parent for slot Funcs | `*fyne.Container` / `*C.GtkBox`                                       | DOM `Element` reference                                                                 |
| __slot<N>                       | `[]fyne.CanvasObject` / `[]*C.GtkWidget` Model field                  | Local JS array inside the rendered `<script>` block                                     |

The js-side translator pattern differs slightly: html doesn't have a "Model" — JS state lives in a top-level `let state = {...}` object, and node refs are `const __n0 = document.querySelector(...)`. So qualification is different from fyne's `m.<name>` pattern.

---

## File Structure

**Create:**
- `codegen/platform/html/intrinsic_translator.go` — `htmlTranslator` implementing `codegen.IntrinsicTranslator`. Emits IR fragments; JsIRContext renders JS.
- `codegen/platform/html/intrinsic_translator_test.go` — unit tests.
- `codegen/platform/html/intrinsic_integration_test.go` — reactive-if end-to-end + browser smoke check.

**Modify (substantially):**
- `codegen/platform/html/html.go` — replace `g.updates` registry-and-`findAffectedUpdaters` mechanism with WalkLowered output. `emitScript` calls translator. Retain renderStaticNode et al for initial HTML.

**Modify (small):**
- `codegen/lang/javascript/ircontext.go` — verify `EvalStmt` handles `*ir.If`, `*ir.For`, `*ir.Assign` on Selects, the `Synthesized` ident hook for resolution (mirror the Go renderer's behavior). Add anything missing.
- `codegen/platform/html/ir_helpers.go` — possibly drop `nodeIsReactive` if it's no longer needed once the new path lands.

**Reference (no changes expected):**
- `codegen/platform/html/cdprunner.go`, `internal/`, `i18n*` runtime helpers.

---

## Phase A: JsIRContext readiness

### Task 1: Audit JsIRContext for the same primitives the Go renderer added

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go`
- Test: `codegen/lang/javascript/ircontext_test.go`

Plan F added to golang.GoIRContext:
- Native-pointer Conversion rendering (skip — html doesn't have native pointer types; pure JS).
- C. prefix on NativePkg=="C" calls (skip — html doesn't emit C calls).
- `EmitFuncDef` for function definitions.
- `Synthesized` Ident shortcut to `m.<name>` (Go-specific; html uses `state.<name>`).

For html, the analogous needs are:
- `EmitFuncDef` for JS function definitions.
- A `Synthesized` Ident hook that resolves to the right JS scope (`state.<name>` for state vars, `__nN` bare for synthesized widget refs since JS has no Model receiver).

- [ ] **Step 1: Audit existing JsIRContext**

```bash
grep -n "Synthesized\|EmitFunc\|IRTypeToJS\|case \*ir.If\|case \*ir.For\|case \*ir.Assign" codegen/lang/javascript/ircontext.go | head
```

Identify gaps. Most likely missing: `EmitFuncDef`, and a Synthesized handling path in `evalIdent`.

- [ ] **Step 2: Add `EmitFuncDef`**

Append:

```go
// EmitFuncDef renders a complete JavaScript function definition from
// an *ir.Func. JS has no method receivers; the Func is emitted as a
// top-level function or a property on a closure-managed object. For
// html's use case, the function is emitted inside the bootstrap
// <script> block as a top-level binding:
//
//	function <name>(params...) { body... }
//
// Returns lines without trailing newlines; caller joins with "\n".
func (jc *JsIRContext) EmitFuncDef(fn *ir.Func) []string {
	var lines []string
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name
	}
	lines = append(lines, "function "+fn.Name+"("+strings.Join(params, ", ")+") {")
	bodyJC := jc
	for _, p := range fn.Params {
		bodyJC = bodyJC.WithLocal(p.Name)
	}
	for _, stmt := range fn.Block {
		for _, line := range bodyJC.EvalStmt(stmt) {
			lines = append(lines, "\t"+line)
		}
	}
	lines = append(lines, "}")
	return lines
}
```

If `WithLocal` doesn't exist on JsIRContext, check what the existing identifier-scope mechanism is. If there's no per-call local scope, params are top-level visible by syntactic position — `function foo(a, b) { ... a ... }` works without scope plumbing.

- [ ] **Step 3: Test**

Create or append to `codegen/lang/javascript/ircontext_test.go`:

```go
func TestEmitFuncDef_PlainFunc(t *testing.T) {
	jc := NewIRContext(nil)
	fn := &ir.Func{
		Name:   "greet",
		Params: []*ir.Param{{Name: "name", Type: ir.TypString}},
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}},
		},
	}
	got := strings.Join(jc.EmitFuncDef(fn), "\n")
	want := "function greet(name) {\n\treturn \"hi\";\n}"
	if got != want {
		t.Errorf("EmitFuncDef mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
```

Run: `go test ./codegen/lang/javascript/ -run TestEmitFuncDef -v`
Adapt the `want` string to whatever EvalStmt actually emits for `Return` (with or without trailing semicolon).

- [ ] **Step 4: Synthesized ident resolution**

Find `evalIdent` in `codegen/lang/javascript/ircontext.go`. Add early branch:

```go
func (jc *JsIRContext) evalIdent(n *ir.Ident) string {
	// Synthesized refs from lowering passes:
	//   __nN widget refs → bare const at <script> scope.
	//   __slotN slot accumulators → bare const at <script> scope.
	//   __root sentinel → bare const at <script> scope.
	//   state vars → `state.<name>` (no Sym set — fall through to
	//     state-object resolution below).
	if n.Synthesized {
		return n.Name
	}
	// ... existing logic
}
```

If the existing logic already routes state vars through `state.<name>` correctly, the Synthesized check just bypasses it. Run existing tests to confirm no regression.

- [ ] **Step 5: Run lang/javascript tests**

```bash
go test ./codegen/lang/javascript/...
```

Commit:

```bash
git add codegen/lang/javascript/
git commit -m "lang/js: EmitFuncDef + Synthesized ident shortcut

Mirrors Plan F's gc.EmitFuncDef for the JS renderer. Synthesized
idents (lowering-pass-emitted refs to __nN/__slotN/__root) render
as bare identifiers; non-synthesized state-var refs continue
through the existing state-object resolution.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase B: Build htmlTranslator

### Task 2: Scaffold

**Files:**
- Create: `codegen/platform/html/intrinsic_translator.go`

```go
package html

import (
	"context"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

// htmlTranslator implements codegen.IntrinsicTranslator for html.
// Produces ir.Stmt fragments that the JS renderer (jc.EvalStmt)
// emits as JavaScript inside the page's bootstrap <script> block.
//
// Unlike fyne/gtk4: no Model receiver. Node refs are JS top-level
// consts initialized via document.querySelector(...). State vars
// live on a `state` object: `state.<name>`.
type htmlTranslator struct {
	jc       *javascript.JsIRContext
	idTags   map[string]string // id ("__n0") → SNGL tag ("text")
	topLevel []string          // ids not yet AppendChild'd
}

func newHTMLTranslator(jc *javascript.JsIRContext) *htmlTranslator {
	return &htmlTranslator{jc: jc, idTags: map[string]string{}}
}

var _ codegen.IntrinsicTranslator = (*htmlTranslator)(nil)

// Stubs: fill in across subsequent tasks.
func (t *htmlTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt { return nil }
func (t *htmlTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return nil
}
func (t *htmlTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return nil
}
func (t *htmlTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	return nil
}
func (t *htmlTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	return nil
}
func (t *htmlTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt { return nil }
func (t *htmlTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	return nil
}
func (t *htmlTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr { return iter }
func (t *htmlTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr { return cond }
func (t *htmlTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
```

Build + commit:

```bash
go build ./codegen/platform/html/...
git add codegen/platform/html/intrinsic_translator.go
git commit -m "html: scaffold htmlTranslator (IntrinsicTranslator impl)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: OnCreateNode — DOM lookup via data-sngl-id

When html lowering produces `lower.CreateNode("text")` inside a `__renderSlot<N>` body, the JS needs to create the actual DOM element (slot bodies create+attach at runtime).

For SLOT-BODY emission, `OnCreateNode` builds the JS equivalent of:

```js
const __n0 = document.createElement("span");
```

(Tag mapping: SNGL tag "text" → HTML "span"; "button" → "button"; "input" → "input"; "vbox" → "div" with flex styles; "hbox" → "div" with flex; etc.)

```go
// htmlTagToDOM maps a SNGL tag to the equivalent HTML element name.
func htmlTagToDOM(tag string) string {
	switch tag {
	case "text", "label":
		return "span"
	case "button":
		return "button"
	case "input":
		return "input"
	case "checkbox":
		return "input" // type="checkbox" set via OnPropAssign
	case "vbox", "hbox":
		return "div" // flex direction set via OnPropAssign
	case "scroll":
		return "div" // overflow set via OnPropAssign
	case "link":
		return "a"
	case "image":
		return "img"
	}
	return ""
}

func (t *htmlTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	domTag := htmlTagToDOM(tag)
	if domTag == "" {
		return nil
	}
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)

	// const <id> = document.createElement("<domTag>");
	createCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "document"},
		Func:     &ir.Func{Name: "createElement"},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: domTag}}},
	}
	return []ir.Stmt{&ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: createCall,
	}}
}
```

Test:

```go
package html

import (
	"context"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

func stubJsCtx() *javascript.JsIRContext {
	return javascript.NewIRContext(nil)
}

func renderStmts(jc *javascript.JsIRContext, stmts []ir.Stmt) string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, jc.EvalStmt(s)...)
	}
	return strings.Join(lines, "\n")
}

func TestHTMLTranslator_OnCreateNode_Text(t *testing.T) {
	jc := stubJsCtx()
	tr := newHTMLTranslator(jc)
	got := renderStmts(jc, tr.OnCreateNode(context.Background(), "__n0", "text"))
	if !strings.Contains(got, `document.createElement("span")`) {
		t.Errorf("expected createElement('span'); got: %s", got)
	}
	if !strings.Contains(got, "__n0") {
		t.Errorf("expected __n0 binding; got: %s", got)
	}
}
```

Commit.

---

### Task 4: OnAppendChild / OnRemoveChild

```go
func (t *htmlTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	// Track: child is no longer top-level.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "appendChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "removeChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}
```

Tests:

```go
func TestHTMLTranslator_OnAppendChild(t *testing.T) {
	jc := stubJsCtx()
	tr := newHTMLTranslator(jc)
	parent := &ir.Ident{Name: "__n0", Synthesized: true}
	child := &ir.Ident{Name: "__n1", Synthesized: true}
	got := renderStmts(jc, tr.OnAppendChild(context.Background(), parent, child))
	if !strings.Contains(got, "__n0.appendChild(__n1)") {
		t.Errorf("expected __n0.appendChild(__n1); got: %s", got)
	}
}

func TestHTMLTranslator_OnRemoveChild(t *testing.T) {
	jc := stubJsCtx()
	tr := newHTMLTranslator(jc)
	parent := &ir.Ident{Name: "__root", Synthesized: true}
	child := &ir.Ident{Name: "__entry", Synthesized: true}
	got := renderStmts(jc, tr.OnRemoveChild(context.Background(), parent, child))
	if !strings.Contains(got, "__root.removeChild(__entry)") {
		t.Errorf("expected __root.removeChild(__entry); got: %s", got)
	}
}
```

Commit.

---

### Task 5: OnPropAssign — DOM property assignment

For html, SNGL props map to DOM properties or attributes. The tag-prop pair determines which:

| SNGL prop         | DOM property |
|-------------------|--------------|
| text.value        | textContent  |
| input.value       | value        |
| input.placeholder | placeholder  |
| button.text       | textContent  |
| checkbox.checked  | checked      |
| any.style.X       | style.X      |
| any.class         | className    |
| any.disabled      | disabled     |

```go
// htmlPropSetter returns the DOM property/attribute name for a SNGL
// prop on a tag. Empty if unknown.
func htmlPropSetter(tag, prop string) string {
	switch tag {
	case "text", "label":
		if prop == "value" {
			return "textContent"
		}
	case "button":
		if prop == "text" {
			return "textContent"
		}
		if prop == "disabled" {
			return "disabled"
		}
	case "input":
		switch prop {
		case "value":
			return "value"
		case "placeholder":
			return "placeholder"
		case "disabled":
			return "disabled"
		case "type":
			return "type"
		}
	case "checkbox":
		if prop == "checked" {
			return "checked"
		}
	}
	return ""
}

func (t *htmlTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bareID := identBareName(node)
	tag := t.idTags[bareID]
	setter := htmlPropSetter(tag, prop)
	if setter == "" {
		// Fall back to setAttribute for unknown props.
		return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: node,
			Func:     &ir.Func{Name: "setAttribute"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: prop}},
				{Value: value},
			},
		}}}
	}
	// node.<setter> = value
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{Operand: node, Field: setter, Type: ir.TypDyn},
		Op:     ast.AssignSet,
		Value:  value,
	}}
}

func identBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}
```

Test:

```go
func TestHTMLTranslator_OnPropAssign_TextValue(t *testing.T) {
	jc := stubJsCtx()
	tr := newHTMLTranslator(jc)
	_ = tr.OnCreateNode(context.Background(), "__n0", "text")
	node := &ir.Ident{Name: "__n0", Synthesized: true}
	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	got := renderStmts(jc, tr.OnPropAssign(context.Background(), node, "value", val))
	if !strings.Contains(got, `__n0.textContent = "hi"`) {
		t.Errorf("expected __n0.textContent = \"hi\"; got: %s", got)
	}
}
```

Commit.

---

### Task 6: OnAttachHandler — addEventListener

For html, SNGL events map to DOM event names:

| SNGL event | DOM event |
|------------|-----------|
| click      | click     |
| input      | input     |
| change     | change    |
| submit     | submit    |

```go
func htmlEventName(event string) string {
	switch event {
	case "click", "input", "change", "submit":
		return event
	}
	return ""
}

func (t *htmlTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	domEvent := htmlEventName(event)
	if domEvent == "" {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: node,
		Func:     &ir.Func{Name: "addEventListener"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Raw: domEvent}},
			{Value: handler},
		},
	}}}
}
```

Test + commit.

---

### Task 7: OnSlotReset / OnSlotAppend

For html, slot accumulators are top-level JS arrays:

```js
let __slot0 = [];
// reset:
__slot0 = [];
// append:
__slot0.push(__n0);
```

Slot Vars themselves need to be emitted at the top of the rendered <script> block. Plan A's `passReactivity` creates the `ir.Var{Name: "__slot0", Type: ListOf(TypDyn)}` on the Component. The html emitter renders these vars as `let __slot0 = [];` at script entry — Task 9 handles that.

```go
func (t *htmlTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	// __slotN = []
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: slot.Name, Synthesized: true},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: slot.Type, Elems: nil},
	}}
}

func (t *htmlTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	// __slotN.push(child)
	slotRef := &ir.Ident{Name: slot.Name, Synthesized: true}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: slotRef,
		Func:     &ir.Func{Name: "push"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}
```

Test + commit.

---

### Task 8: OnIter / OnCond / OnDefault

```go
func (t *htmlTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	// JS for-of iterates iterable directly; Synthesized refs resolve
	// to bare identifiers via jc.evalIdent's Synthesized shortcut.
	return iter
}

func (t *htmlTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *htmlTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
```

Run full translator tests + commit.

---

## Phase C: Wire htmlTranslator into the html emitter

### Task 9: Emit __slotN, __root, and __renderSlotN inside the bootstrap <script>

`html.go`'s `emitScript` (around line 2366) writes the bootstrap JS. Currently it emits state init, element refs, `g.updates` calls, etc. Augment it to also emit the slot accumulators and slot Funcs synthesized by `passReactivity`.

- [ ] **Step 1: Iterate synthesized Vars + Funcs**

```go
// Emit slot accumulators:
for _, v := range pkg.Vars {
	if v.Synthesized {
		b.WriteString("let " + v.Name + " = ")
		if v.Init != nil {
			b.WriteString(jc.EvalExpr(v.Init))
		} else {
			b.WriteString("null")
		}
		b.WriteString(";\n")
	}
}
// (Same for each Component.Vars where v.Synthesized; depends on
// component-walking pattern in the existing emitScript.)

// Emit slot Funcs:
for _, fn := range pkg.Components[0].Funcs {
	if fn.Synthesized {
		tr := newHTMLTranslator(jc)
		// Translate the body via WalkLowered:
		body := codegen.WalkLowered(context.Background(), fn.Block, tr)
		synthesized := &ir.Func{
			Name:   fn.Name,
			Params: fn.Params,
			Block:  body,
		}
		for _, line := range jc.EmitFuncDef(synthesized) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
}
```

Adapt to the actual html.go structure — find the equivalent of where the Phase 1 BuildUI happens. May need to iterate `pkg.Components` AND `pkg.Windows` if the html platform handles multi-window.

- [ ] **Step 2: Wire renderSlot Func call sites**

Plan A emits `CallStmt __renderSlot0(__root)` at source position AND after mutations. The handler-body splice gets the call inlined when the surrounding handler emission walks the IR (today via `g.updates` substitution; after Plan D via the same translator).

For the initial-source-position call: it sits in the component body. Today the static-HTML emitter walks the body and ignores non-NodeInst stmts. The new behavior: when the static walker encounters a `CallStmt __renderSlotN(...)`, emit a JS call at the bootstrap script's end:

```js
// Initial render of reactive slots (after DOM is in place):
__renderSlot0(__root);
```

Translate via the htmlTranslator's OnDefault or a special-case branch in the new emit-script path. Or just emit the JS directly:

```go
// In emitScript, after slot Funcs are emitted, walk component body
// for top-level CallStmts to synthesized __renderSlot*:
for _, s := range pkg.Components[0].Body {
	if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && strings.HasPrefix(cs.Call.Func.Name, "__renderSlot") {
		b.WriteString(jc.EvalStmt(cs)[0]) // single-line call
		b.WriteByte('\n')
	}
}
```

(Adapt as needed.)

- [ ] **Step 3: Run html tests**

```bash
go test ./codegen/platform/html/...
```

Some tests will fail because the new emission path coexists with the legacy `g.updates` registry — they emit duplicate code. That's a temporary state. Tasks 10-13 unwind the legacy.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/html/html.go codegen/platform/html/intrinsic_translator.go
git commit -m "html: emit __slotN vars + __renderSlotN funcs via htmlTranslator

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Route handler bodies through htmlTranslator

**Status (2026-05-13):** SUPERSEDED. Tasks 10–13 are rewritten as a 5-phase plan in `docs/superpowers/plans/2026-05-13-html-gupdates-rip.md`. A second-pass investigation confirmed the original sizing underestimated the work by ~10x: `g.updates` is load-bearing for initial-sync seeds, structural updaters not in `passReactivity` (for-loop list functions, if/else, class-list, attr), `MutationModel` optimization (dead-code elim + dispatch sharing), and scope/rename plumbing in `g.scope`/`g.dataRenames` not yet visible to `JsIRContext`. The follow-up plan splits the rip into 5 independently-shippable phases.

The Task 9 path (synthesized __slotN + __renderSlotN through htmlTranslator + WalkLowered + JsIRContext) is live and exercised by the new `intrinsic_integration_test.go`. Reactive `if` and reactive `for` route through the new translator path end-to-end. The remaining legacy registry continues to drive non-structural reactive updates (textContent patches, input.value patches) and the initial-sync init-only updater calls.

Recommended next step for whoever picks this up: rewrite the four fixtures to assert the *new* output shape that handler-body translator routing should produce, then incrementally migrate `addClickHandler` / `addInputHandler` / `addChangeHandler` / `emitSetter` to consume `WalkLowered` output via `htmlTranslator`. The replacement of `$u__nN_text()` for initial sync needs a parallel mechanism (e.g. emit an `init()` function that calls the same translator-emitted property writes once at script load).

---

### Task 10 (original, deferred): Route handler bodies through htmlTranslator

Currently handler bodies in html are emitted via `g.exprToJS` + raw string concat (search for `addEventListener` and handler-body emission in html.go). Each spliced reactive Assign in a handler body needs to flow through OnPropAssign.

- [ ] **Step 1: Find handler-body emission**

```bash
grep -n "addClickHandler\|addInputHandler\|emitClickHandler\|handler.Func\|h.Func.Block" codegen/platform/html/html.go | head
```

- [ ] **Step 2: Replace body-stmt loop with WalkLowered**

For each handler emission site, change:

```go
for _, stmt := range h.Func.Block {
	// old: g.exprToJS + raw conv
}
```

to:

```go
tr := newHTMLTranslator(jc)
body := codegen.WalkLowered(context.Background(), h.Func.Block, tr)
for _, stmt := range body {
	for _, line := range jc.EvalStmt(stmt) {
		b.WriteString(line + ";\n")
	}
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./codegen/platform/html/...
git add codegen/platform/html/html.go
git commit -m "html: handler bodies routed through htmlTranslator + WalkLowered

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Migrate state-setter splice updates to translator

When a `Set<Var>` setter is emitted for `state.foo = v`, the legacy code calls `findAffectedUpdaters(...)` and invokes the matched `$u_*` functions. After Plan D, the spliced Assigns are inline in the handler body (from Plan A's NoReactivity); the setter just sets the state, doesn't need to dispatch.

- [ ] **Step 1: Locate setter emission**

```bash
grep -n "\\$set_\|emitSetter\|findAffected" codegen/platform/html/html.go | head
```

- [ ] **Step 2: Remove findAffected calls from setter emission**

Setters become simple:

```go
function $set_foo(v) {
    state.foo = v;
}
```

(No more `$u_n0_value()` etc. dispatch — those calls now live inline in handler bodies via the spliced Assigns.)

- [ ] **Step 3: Test + commit**

```bash
go test ./codegen/platform/html/...
git commit -m "html: $set_<var> setters drop findAffected dispatch

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Drop g.updates registry

After Tasks 9-11, `g.updates` is unused. Delete the field, `addTextUpdater`, `findAffectedUpdaters`, `loweredID`, and the legacy `$u_*` function emission.

```bash
grep -n "g\\.updates\\|updateFunc\\|findAffectedUpdaters\\|loweredID\\|\\$u_" codegen/platform/html/html.go | head -20
```

Delete each. Test + commit.

---

### Task 13: Drop nodeIsReactive

```bash
grep -n "nodeIsReactive\|IRIsReactive" codegen/platform/html/
```

If anything still uses it, decide whether to keep (e.g. for renderStaticInput's value-attr decision) or remove. Most uses should be obsolete after the translator path.

Test + commit.

---

## Phase D: Integration + verify

### Task 14: Integration test for reactive if on html

Create `codegen/platform/html/intrinsic_integration_test.go`. Mirror Plan B Task 12's structure: parse a reactive-if fixture, run lowering with `{NoReactivity, NoAsyncReactive}`, generate via html, assert JS snippets:

```go
for _, snippet := range []string{
	"function __renderSlot0",
	"let __slot0 = []",
	"__slot0.forEach", // teardown
	"__slot0 = []",    // reset
	"if (state.visible)",
	`document.createElement("span")`,
	".textContent = ",
	`__root.appendChild(`,
	`__slot0.push(`,
	`__renderSlot0(__root)`, // initial + spliced
} {
	if !strings.Contains(out, snippet) {
		t.Errorf("emitted JS missing snippet %q\n--- generated ---\n%s", snippet, out)
	}
}

for _, leak := range []string{
	"lower.CreateNode",
	"lower.AppendChild",
	"lower.RemoveChild",
	"stdlib.ListPush",
} {
	if strings.Contains(out, leak) {
		t.Errorf("untranslated intrinsic %q leaked into emitted JS", leak)
	}
}
```

Run. Iterate on snippet mismatches.

### Task 15: Reproduce hello-i18n on html

```bash
go install ./cmd/sngl
cd /tmp && rm -rf hello-html && mkdir hello-html && cp -r /home/jonathan/src/git.duckfam.us/jonathan/sngl/examples/hello-i18n/* hello-html/
cd hello-html && sngl compile --platform=html --out=out app.sngl
# Inspect out/index.html
```

Check: page loads, reactive vars trigger DOM updates on @click handlers.

If browser smoke check needed, use `chromium --headless --disable-gpu --dump-dom out/index.html`.

### Task 16: Verify

```bash
go test ./codegen/...
go tool verify 2>&1 | grep -E "^---|not ok" | head
```

### Task 17: Spec annotation

Update `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` with Plan D note.

---

## What's NOT in Plan D

- **HTTP route mode** (when `--lang go`): the http-route path uses a different code path that this plan doesn't touch. Server-side state remains Go-rendered.
- **`renderStaticInput`'s two-way-binding inference**: removed by the original session-start fix (commit `18208e8`). Not re-introduced.
- **JS bundling / minification**: existing `htmlminify.go` / `jsbundle.go` paths unchanged.
- **CDP browser tests**: existing `cdprunner.go` continues to drive end-to-end browser tests; verifies the emitted JS works in a real browser.
- **Memory management** for runtime references: html relies on browser GC.
