# Fyne Intrinsic Translator Implementation Plan (Plan B, phases 1-2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a `fyneIntrinsicTranslator` that consumes the lowered IR stream (`lower.CreateNode`/`AppendChild`/`RemoveChild`/`AttachHandler` calls + element-ref prop Assigns), and wire it into fyne's codegen so the `__renderSlot<N>` Funcs synthesized by Plan A's `passReactivity` get emitted as real Go methods instead of being skipped.

**Architecture:** Plan A landed a shared `codegen/intrinsic_walker.go` defining `IntrinsicTranslator` and `WalkLowered`. This plan implements that interface for fyne (Phase 1), then plumbs it through `emitIRFyneFunc` so reactive `if`/`for` actually re-renders via the new pipeline (Phase 2). The existing tree-walking `view_ir.go` stays in place for non-reactive content — that broader rewrite is **Plan B.2**, not this plan. Per-tag widget dispatch reuses fyne's existing blueprint table (`fyne.sngl` + `blueprint.go`).

**Tech Stack:** Go, fyne v2 API, fyne blueprint records from `codegen/platform/fyne/blueprint.go`, `codegen/intrinsic_walker.go` interface, `codegen/lang/golang/ircontext.go` for expression evaluation.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` §2.2 (fyne conversion).

**Predecessor:** `docs/superpowers/plans/2026-05-12-reactivity-lowering-foundation.md` (Plan A — already merged to main).

**Successor:** Plan B.2 will enable `NoDeclarative` for fyne and rewrite the main BuildUI emission onto the same intrinsic-walker pipeline. After Plan B.2, the legacy `view_ir.go` / `addUpdater` / `FindAffected` machinery comes out.

---

## Background: what fyne sees after Plan A

Source:

```sngl
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
```

After `passReactivity` lowering, the component's `Funcs` contains a synthesized `__renderSlot0(parent dyn) void`:

```sngl
func __renderSlot0(parent dyn) void {
    for __entry = __slot0 {
        lower.RemoveChild(#parent, __entry)
    }
    __slot0 = []
    if visible {
        var __n0 text = lower.CreateNode("text")
        #__n0.value = "hi"
        lower.AppendChild(#parent, #__n0)
        __slot0 = stdlib.ListPush(__slot0, #__n0)
    }
}
```

And the component body now contains `__renderSlot0(#__root)` calls (at source position + spliced after every `visible` mutation).

Plan A added a guard in `codegen/platform/fyne/compiler_ir.go` that skips `fn.Synthesized` funcs, so today fyne never emits `__renderSlot0`. The reactive `if` therefore doesn't update at runtime via the new pipeline. (The old `updateIf0` updater still runs in parallel because Plan A didn't touch `view_ir.go` — so visually the app keeps working, but through two systems.)

This plan makes fyne emit `__renderSlot0` and the supporting `__slot0` var, validating the intrinsic-walker pipeline on a real platform. Stripping the parallel updater machinery is **Plan B.2**.

---

## File Structure

**Create:**

- `codegen/platform/fyne/intrinsic_translator.go` — `fyneTranslator` struct implementing `codegen.IntrinsicTranslator`; emits Go source for each lowered IR shape.
- `codegen/platform/fyne/intrinsic_translator_test.go` — unit tests exercising each translator method.
- `codegen/platform/fyne/intrinsic_integration_test.go` — end-to-end test: compile a reactive-if SNGL fixture and assert the emitted Go contains the expected `__renderSlot0` method and call sites.

**Modify:**

- `codegen/platform/fyne/compiler_ir.go` — replace the `if fn.Synthesized { continue }` skip in the Func-emission loop with a dispatch that routes `__renderSlot*` Funcs through `fyneTranslator` for their bodies. Also stop suppressing `__slot*` Vars (let them through to the Model struct as `[]fyne.CanvasObject` fields).
- `codegen/lang/golang/ircontext.go` — extend `EvalStmt` to handle `*ir.If` (currently panics on it). Required so `__renderSlot*` bodies (which contain `if cond { ... }` directly) can be emitted via the same `gc.EvalStmt` path used for other statements.

**No structural changes** to: `view_ir.go`, `blueprint.go`, `scaffold.go`, the template — those stay as-is for non-reactive content. They come out in Plan B.2.

---

## Phase 1: Build `fyneIntrinsicTranslator`

### Task 1: Scaffolding — empty translator that compiles

**Files:**
- Create: `codegen/platform/fyne/intrinsic_translator.go`

- [ ] **Step 1: Write the file**

Create `codegen/platform/fyne/intrinsic_translator.go`:

```go
package fyne

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// fyneTranslator implements codegen.IntrinsicTranslator for fyne. It
// emits Go source that mutates an enclosing *Model receiver — the
// generated functions all sit on Model so widget refs (e.g. `m.n0`)
// resolve as struct fields.
//
// One translator instance is constructed per __renderSlot<N> Func
// emission; widget-field registrations performed during that emission
// flow back into the enclosing *compilation via fieldSink.
type fyneTranslator struct {
	gc         *golang.GoIRContext
	blueprints map[string]*fyneBlueprint
	fieldSink  func(name, goType string)
}

// newFyneTranslator constructs a translator. `blueprints` is the
// platform-wide blueprint table loaded once in init(); `fieldSink`
// receives every (widget-name, go-type) pair so the enclosing emitter
// can declare the field on Model.
func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string)) *fyneTranslator {
	return &fyneTranslator{gc: gc, blueprints: blueprints, fieldSink: fieldSink}
}

// Compile-time interface check.
var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)

func (t *fyneTranslator) OnCreateNode(id, tag string) string        { return "" }
func (t *fyneTranslator) OnAppendChild(parent, child string) string { return "" }
func (t *fyneTranslator) OnRemoveChild(parent, child string) string { return "" }
func (t *fyneTranslator) OnAttachHandler(node, event, h string) string {
	return ""
}
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string {
	return ""
}

// dummy use of strings so the import isn't unused before later tasks
// flesh out the method bodies. Removed by Task 2.
var _ = strings.Builder{}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./codegen/platform/fyne/...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go
git commit -m "fyne: scaffold fyneTranslator (IntrinsicTranslator impl)

Empty struct + interface compliance, no method bodies yet. Bodies
implemented in subsequent tasks per the plan.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `OnCreateNode` — emit widget constructor via blueprint lookup

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Test: `codegen/platform/fyne/intrinsic_translator_test.go` (create)

Background: fyne already maps SNGL tags (`text`, `button`, `vbox`, etc.) to Fyne constructors via the blueprint system in `blueprint.go` + `fyne.sngl`. Each blueprint has a `Constructor.GoFn` (e.g. `widget.NewLabel`), `Constructor.GoType` (e.g. `*widget.Label`), and an args template. `OnCreateNode` needs to emit `m.<id> = widget.NewLabel("")` (or analog) and register `<id>` as a Model field of the right type.

For Plan B's narrow scope (only consuming `__renderSlot*` bodies), the only constructor arguments we'll see at this layer are tags from `lower.CreateNode("tag")`. Prop values come through later via `OnPropAssign`. So the constructor call uses **zero-arg defaults** here, relying on the downstream `OnPropAssign` calls to set initial values.

- [ ] **Step 1: Write the failing test**

Create `codegen/platform/fyne/intrinsic_translator_test.go`:

```go
package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
)

// stubGC builds a GoIRContext suitable for translator unit tests —
// no real package context, just enough machinery to emit Go strings.
func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(nil)
}

func TestFyneTranslator_OnCreateNode_Text(t *testing.T) {
	var fields []string
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(name, goType string) {
		fields = append(fields, name+" "+goType)
	})
	got := tr.OnCreateNode("__n0", "text")
	if !strings.Contains(got, "m.__n0 = widget.NewLabel(") {
		t.Errorf("expected widget.NewLabel constructor; got: %s", got)
	}
	want := "__n0 *widget.Label"
	if len(fields) != 1 || fields[0] != want {
		t.Errorf("expected field registration %q; got: %v", want, fields)
	}
}

func TestFyneTranslator_OnCreateNode_UnknownTag(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnCreateNode("__n0", "wibble")
	if got != "" {
		t.Errorf("expected empty emission for unknown tag; got: %s", got)
	}
}
```

This test uses `platformBlueprints()` which doesn't exist yet — it's a small helper we'll add in Step 3. The package-level `blueprintByName` map is initialized in `blueprint.go:loadBlueprints` and is available globally to fyne package code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnCreateNode -v`
Expected: FAIL — `platformBlueprints` undefined and `OnCreateNode` returns "".

- [ ] **Step 3: Add `platformBlueprints()` helper**

In `codegen/platform/fyne/intrinsic_translator.go` (or wherever the package's blueprint loading lives — check `blueprint.go`), expose a getter:

```go
// platformBlueprints returns the blueprint table loaded at init().
// Used by intrinsic translators to look up widget constructors and
// binding records by component tag.
func platformBlueprints() map[string]*fyneBlueprint {
	return blueprintByName
}
```

If `blueprintByName` doesn't exist by that name, grep `blueprint.go` for the actual identifier and use the right one. (The Explore agent's report mentions "blueprintByName map".)

- [ ] **Step 4: Implement `OnCreateNode`**

Replace the stub `OnCreateNode` in `codegen/platform/fyne/intrinsic_translator.go`:

```go
func (t *fyneTranslator) OnCreateNode(id, tag string) string {
	bp, ok := t.blueprints[tag]
	if !ok {
		// Unknown tag — emit nothing rather than guess. Caller's
		// upstream lowering should not produce CreateNode for tags fyne
		// doesn't know; if this fires in practice it's a Plan A/B gap.
		return ""
	}
	goType := bp.Constructor.GoType
	if goType == "" {
		return ""
	}
	t.fieldSink(id, goType)
	// Emit `m.<id> = <ctor>(<zero-arg defaults>)`. We don't pass any
	// prop values here — those come through OnPropAssign as separate
	// stmts. Most fyne constructors tolerate zero / empty args.
	return "m." + id + " = " + bp.Constructor.GoFn + "()\n"
}
```

Note on zero-args: some fyne constructors require non-empty args (e.g. `widget.NewSelect(options, onChanged)`). For Plan B's narrow scope (`__renderSlot*` bodies only created `text` widgets in our golden tests), single-arg constructors like `widget.NewLabel("")` are the realistic case. If a test fixture reveals a constructor that errors on zero args, extend this method to pass `bp.Constructor.DefaultArgs` if such a field exists on the blueprint — but only in a follow-up commit, not preemptively.

Actually `widget.NewLabel()` requires one string arg. Make the emission pass `""` for any constructor that takes positional args. Look up how blueprints currently emit args; if there's a pattern like `bp.Constructor.Args`, just emit the same default args used at init time.

Read `blueprint.go` and `view_ir.go:renderFromBlueprint` lines 537-690 to see how constructor args get filled in normally. The slot-time emission can't reuse that path verbatim (it depends on `n *ir.NodeInst` which we don't have), so emit a minimal zero-value invocation. For `text` specifically that's `widget.NewLabel("")`.

Simplest concrete rule: for the `text` tag, emit `widget.NewLabel("")`. For everything else in Plan B, emit `bp.Constructor.GoFn + "()"` and let tests tell us where to add specific overrides.

Concrete update:

```go
func (t *fyneTranslator) OnCreateNode(id, tag string) string {
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	goType := bp.Constructor.GoType
	if goType == "" {
		return ""
	}
	t.fieldSink(id, goType)
	args := zeroArgsFor(tag)
	return "m." + id + " = " + bp.Constructor.GoFn + "(" + args + ")\n"
}

// zeroArgsFor returns the constructor-arg string used when a slot-time
// CreateNode emits a widget; the slot's subsequent OnPropAssign calls
// fill the real values.
func zeroArgsFor(tag string) string {
	switch tag {
	case "text", "label":
		return `""`
	case "button":
		return `"", nil`
	default:
		return ""
	}
}
```

(Plan B mainly exercises `text`; other tags are placeholders that Plan B.2 will need to revisit when it widens scope.)

- [ ] **Step 5: Run tests**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnCreateNode -v`
Expected: BOTH tests PASS.

- [ ] **Step 6: Run all fyne tests**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: OnCreateNode emits widget constructor via blueprint lookup

Look up the tag in the platform-wide blueprint table, emit the matching
constructor call, register the widget field with the enclosing
emitter. Plan B only exercises 'text'; other tags get zero-arg
constructors that Plan B.2 will revisit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `OnAppendChild` / `OnRemoveChild`

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Modify: `codegen/platform/fyne/intrinsic_translator_test.go`

Background: in Fyne, container children are managed via the container's `Objects` slice plus `Refresh()`. For a `*fyne.Container`:
- Add: `m.parent.Add(m.child)` (Fyne provides this as a method).
- Remove: `m.parent.Remove(m.child)`.

For the slot case specifically, the `parent` reference is a `fyne.CanvasObject` (received as the parameter `parent` in `__renderSlot<N>(parent fyne.CanvasObject)`). We need to assert it's a `*fyne.Container` to call `.Add()` / `.Remove()`.

Pragmatic plan: emit a type assertion at the start of each `__renderSlot<N>` body (Task 7 does this — the slot-Func emission wraps the body with `c := parent.(*fyne.Container)`). `OnAppendChild` and `OnRemoveChild` then emit `c.Add(m.child)` / `c.Remove(m.child)`.

Alternative: detect when `parent` is a `fyne.Container` already and skip the assertion. For simplicity, always assert.

- [ ] **Step 1: Write the failing tests**

Append to `codegen/platform/fyne/intrinsic_translator_test.go`:

```go
func TestFyneTranslator_OnAppendChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnAppendChild("parent", "__n0")
	want := "parent.Add(m.__n0)\n"
	if got != want {
		t.Errorf("OnAppendChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnRemoveChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnRemoveChild("parent", "__entry")
	want := "parent.Remove(__entry)\n"
	if got != want {
		t.Errorf("OnRemoveChild: got %q, want %q", got, want)
	}
}
```

Note: `parent` is the slot Func's local parameter (a `*fyne.Container` after Task 7's assertion), so no `m.` prefix. `__n0` is a Model widget field — prefix `m.`. `__entry` is a teardown-loop iterator variable — bare name, no prefix.

The translator needs to know which names are Model fields vs locals. Simplest rule: emit `m.<name>` if the name starts with `__n` (Plan A's synthetic widget IDs use that prefix); otherwise emit the bare name.

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run "TestFyneTranslator_On(Append|Remove)Child" -v`
Expected: FAIL — empty emissions.

- [ ] **Step 3: Implement the methods**

In `codegen/platform/fyne/intrinsic_translator.go`, replace the stubs:

```go
func (t *fyneTranslator) OnAppendChild(parent, child string) string {
	return parent + ".Add(" + modelRef(child) + ")\n"
}

func (t *fyneTranslator) OnRemoveChild(parent, child string) string {
	return parent + ".Remove(" + modelRef(child) + ")\n"
}

// modelRef qualifies a node id with "m." when it's a Plan A synthetic
// widget ref (`__nN`). Loop-local and parameter refs stay bare.
func modelRef(name string) string {
	if strings.HasPrefix(name, "__n") {
		return "m." + name
	}
	return name
}
```

(The `strings` import already exists from Task 1.)

- [ ] **Step 4: Run tests**

Run: `go test ./codegen/platform/fyne/ -run "TestFyneTranslator_On(Append|Remove)Child" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: OnAppendChild / OnRemoveChild emit Container.Add/.Remove

The slot's runtime parent is a *fyne.Container (asserted at the
__renderSlot<N> body's entry — see Task 7). Append and Remove are
plain method calls. modelRef() prefixes Plan A's __nN widget refs
with the Model receiver; other names (loop locals, parent param)
stay bare.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `OnAttachHandler`

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Modify: `codegen/platform/fyne/intrinsic_translator_test.go`

Background: `lower.AttachHandler(#node, "event", handlerRef)` wires an event. In fyne, event handlers are assigned to widget-specific fields, e.g. `*widget.Button.OnTapped = func() { ... }`. The blueprint binding records carry these per-event targets (`Event{prop:"click", target:".OnTapped", signature:"func()"}`).

For Plan B's narrow scope: the slot-Func body almost never contains `AttachHandler` calls — handlers attach at top-level (outside slots) where the existing `view_ir.go` walker handles them. So `OnAttachHandler` is mostly defensive coverage. Still, implement it concretely: look up the event in the blueprint's bindings, emit the assignment.

But there's a wrinkle: `OnAttachHandler` receives `node` (an id like `__n0`) but doesn't know its tag — so it can't look up the blueprint binding. The translator needs a side-table mapping `id → tag` populated by `OnCreateNode`. Add it.

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestFyneTranslator_OnAttachHandler_ButtonClick(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	// Simulate: tr.OnCreateNode("__n0", "button") registered earlier.
	_ = tr.OnCreateNode("__n0", "button")
	got := tr.OnAttachHandler("__n0", "click", "m.handleClick")
	if !strings.Contains(got, "m.__n0.OnTapped = m.handleClick") {
		t.Errorf("expected OnTapped assignment; got: %s", got)
	}
}

func TestFyneTranslator_OnAttachHandler_UnknownEvent(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "button")
	got := tr.OnAttachHandler("__n0", "wibble", "m.h")
	if got != "" {
		t.Errorf("expected empty emission for unknown event; got: %s", got)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnAttachHandler -v`
Expected: FAIL.

- [ ] **Step 3: Add id→tag side table and implement OnAttachHandler**

Update the struct and `OnCreateNode` in `codegen/platform/fyne/intrinsic_translator.go`:

```go
type fyneTranslator struct {
	gc         *golang.GoIRContext
	blueprints map[string]*fyneBlueprint
	fieldSink  func(name, goType string)
	idTags     map[string]string // id ("__n0") → tag ("text")
}

func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		blueprints: blueprints,
		fieldSink:  fieldSink,
		idTags:     map[string]string{},
	}
}

func (t *fyneTranslator) OnCreateNode(id, tag string) string {
	// ... existing body unchanged, plus:
	t.idTags[id] = tag
	// ... existing return ...
}
```

(Add the `t.idTags[id] = tag` line just before `return` in the existing body. Don't rewrite the whole method.)

Then `OnAttachHandler`:

```go
func (t *fyneTranslator) OnAttachHandler(node, event, handlerRef string) string {
	tag, ok := t.idTags[node]
	if !ok {
		return ""
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	target := ""
	for _, b := range bp.Bindings {
		if b.Kind == bindEvent && b.Prop == event {
			target = b.Target
			break
		}
	}
	if target == "" {
		return ""
	}
	return modelRef(node) + target + " = " + handlerRef + "\n"
}
```

Note: `bindEvent` and `Binding.Kind` — read `blueprint.go` to confirm the enum / field names. The grep output in the investigation report mentioned `Bindings` containing `Init/Reactive/Event` records; the field names may be `Kind`, `Prop`, `Target`. If they don't match, adapt while preserving the lookup semantics.

- [ ] **Step 4: Run tests**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnAttachHandler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: OnAttachHandler looks up event target in blueprint bindings

Translator now maintains an id→tag side table populated by
OnCreateNode, so OnAttachHandler can find the right blueprint
binding for the event and emit the matching widget field assignment
(e.g. m.__n0.OnTapped = m.handleClick).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: `OnPropAssign`

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Modify: `codegen/platform/fyne/intrinsic_translator_test.go`

Background: an `Assign{Target: Select{Operand: IsElementRef Ident "__n0", Field: "value"}, Value: <expr>}` becomes a setter call. For `text`'s `value` prop, the blueprint binding declares `Reactive{prop:"value", target:".SetText", transform:"fmt.Sprint"}`. So emission is:

```go
m.__n0.SetText(fmt.Sprint(<value-expr-translated-by-GoIRContext>))
```

The value expression's Go translation comes via `gc.EvalExpr(valueExpr)` — `golang.GoIRContext` is fyne's existing language translator. So this method's job is: look up the binding, run `gc.EvalExpr` on the value, wrap with transform if present, emit.

- [ ] **Step 1: Write the failing test**

```go
func TestFyneTranslator_OnPropAssign_TextValue(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "text")

	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	got := tr.OnPropAssign("__n0", "value", val)
	want := `m.__n0.SetText(fmt.Sprint("hi"))` + "\n"
	if got != want {
		t.Errorf("OnPropAssign mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFyneTranslator_OnPropAssign_UnknownProp(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "text")
	val := &ir.Literal{Type: ir.TypString, Raw: "x"}
	got := tr.OnPropAssign("__n0", "wibble", val)
	if got != "" {
		t.Errorf("expected empty emission for unknown prop; got %q", got)
	}
}
```

Add `"git.duckfam.us/jonathan/sngl/ir"` to imports if not already present.

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnPropAssign -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string {
	tag, ok := t.idTags[nodeID]
	if !ok {
		return ""
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	var binding *bindMeta
	for i := range bp.Bindings {
		b := &bp.Bindings[i]
		if (b.Kind == bindReactive || b.Kind == bindInit) && b.Prop == prop {
			binding = b
			break
		}
	}
	if binding == nil {
		return ""
	}
	val := t.gc.EvalExpr(valueExpr)
	if binding.Transform != "" {
		val = binding.Transform + "(" + val + ")"
	}
	return modelRef(nodeID) + binding.Target + "(" + val + ")\n"
}
```

Same caveat: `bindMeta`, `bindReactive`, `bindInit`, `Kind`, `Prop`, `Target`, `Transform` are the field names per the investigation report. If they differ, adapt.

- [ ] **Step 4: Run tests**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator_OnPropAssign -v`
Expected: PASS.

- [ ] **Step 5: Run full translator test suite**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator -v`
Expected: ALL PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: OnPropAssign emits setter call via blueprint binding

Look up the prop's Reactive/Init binding in the blueprint, translate
the value expression through GoIRContext, wrap with the transform
(e.g. fmt.Sprint), emit the setter call on the widget field.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2: Wire emitIRFyneFunc to consume `__renderSlot*` bodies

### Task 6: Add `*ir.If` handling to `golang.GoIRContext.EvalStmt`

**Files:**
- Modify: `codegen/lang/golang/ircontext.go`
- Modify: `codegen/lang/golang/ircontext_test.go` (or create if absent)

Background: today `gc.EvalStmt(s)` panics on `*ir.If`. `__renderSlot*` bodies contain plain `if cond { ... }` stmts directly. Fyne's emitter needs `EvalStmt` to translate them. Standard Go if-then-else.

- [ ] **Step 1: Write the failing test**

Check whether `ircontext_test.go` exists. If not, create it with a minimal helper:

```go
package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEvalStmt_If(t *testing.T) {
	gc := NewIRContext(nil)
	stmt := &ir.If{
		Cond: &ir.Literal{Type: ir.TypBool, Raw: "true"},
		Body: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "x"},
				Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
			},
		},
	}
	lines := gc.EvalStmt(stmt)
	joined := strings.Join(lines, "\n")
	if !strings.HasPrefix(joined, "if true {") {
		t.Errorf("expected 'if true {' prefix, got: %s", joined)
	}
	if !strings.Contains(joined, "x = 1") {
		t.Errorf("expected body 'x = 1', got: %s", joined)
	}
	if !strings.HasSuffix(joined, "}") {
		t.Errorf("expected closing '}', got: %s", joined)
	}
}

func TestEvalStmt_IfElse(t *testing.T) {
	gc := NewIRContext(nil)
	stmt := &ir.If{
		Cond: &ir.Literal{Type: ir.TypBool, Raw: "true"},
		Body: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Raw: "1"}}},
		Else: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Raw: "2"}}},
	}
	joined := strings.Join(gc.EvalStmt(stmt), "\n")
	if !strings.Contains(joined, "} else {") {
		t.Errorf("expected '} else {' separator, got: %s", joined)
	}
	if !strings.Contains(joined, "x = 2") {
		t.Errorf("expected else body 'x = 2', got: %s", joined)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/lang/golang/ -run TestEvalStmt_If -v`
Expected: FAIL — panic on unhandled `*ir.If`.

- [ ] **Step 3: Implement**

In `codegen/lang/golang/ircontext.go`, find `EvalStmt`'s switch (around line 107). Add a case for `*ir.If` before the default panic:

```go
case *ir.If:
    return gc.evalIf(n)
```

Then add `evalIf` as a method below (alongside `evalFor`):

```go
// evalIf emits a Go if-then-else. Else may be empty.
func (gc *GoIRContext) evalIf(n *ir.If) []string {
	cond := gc.EvalExpr(n.Cond)
	var lines []string
	lines = append(lines, "if "+cond+" {")
	for _, s := range n.Body {
		for _, l := range gc.EvalStmt(s) {
			lines = append(lines, "\t"+l)
		}
	}
	if len(n.Else) > 0 {
		lines = append(lines, "} else {")
		for _, s := range n.Else {
			for _, l := range gc.EvalStmt(s) {
				lines = append(lines, "\t"+l)
			}
		}
	}
	lines = append(lines, "}")
	return lines
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./codegen/lang/golang/ -run TestEvalStmt -v`
Expected: BOTH new tests pass; existing tests still pass.

- [ ] **Step 5: Run full golang lang tests**

Run: `go test ./codegen/lang/golang/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/lang/golang/ircontext.go codegen/lang/golang/ircontext_test.go
git commit -m "lang/golang: handle *ir.If in EvalStmt

Plan A's __renderSlot<N> bodies contain plain if-then-else stmts.
fyne emits those bodies through gc.EvalStmt; until now any *ir.If
hit the default panic. Adds straightforward Go if-then-else
translation.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Dispatch `__renderSlot*` Funcs through fyneTranslator

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`

This is the integration point. Currently `compiler_ir.go` has (around line 320):

```go
for _, fn := range allFuncs {
	if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
		continue
	}
	// Synthesized by passReactivity; fyne does not yet consume these (Plan B).
	if fn.Synthesized {
		continue
	}
	emitIRFyneFunc(&funcBuf, fn, gc)
}
```

Replace with:

```go
for _, fn := range allFuncs {
	if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
		continue
	}
	if fn.Synthesized {
		emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields)
		continue
	}
	emitIRFyneFunc(&funcBuf, fn, gc)
}
```

Then add `emitIRSlotFunc`:

```go
// emitIRSlotFunc emits a passReactivity-synthesized __renderSlot<N>
// Func as a Model method. The body is a mix of plain Go statements
// (For teardown, Assign reset, If gate) and lower.* intrinsic calls.
// Each stmt goes through fyneStmtDispatch, which routes intrinsics
// through fyneTranslator and falls back to gc.EvalStmt for everything
// else.
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField) {
	// Slot Funcs take a single `parent dyn` parameter at the IR level;
	// fyne emits it as `parent fyne.CanvasObject` and asserts the
	// container type at function entry so subsequent .Add/.Remove
	// calls compile.
	fmt.Fprintf(b, "func (m *Model) %s(parent fyne.CanvasObject) {\n", fn.Name)
	b.WriteString("\tcontainer, _ := parent.(*fyne.Container)\n")
	b.WriteString("\tif container == nil { return }\n")

	tr := newFyneTranslator(gc, platformBlueprints(), func(name, goType string) {
		*widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
	})

	for _, stmt := range fn.Block {
		lines := fyneStmtDispatch(stmt, tr, gc)
		for _, l := range lines {
			b.WriteString("\t")
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	b.WriteString("}\n\n")
}

// fyneStmtDispatch routes one statement either through the intrinsic
// translator (for lower.* calls, element-ref Assigns, CreateNode
// LocalVars) or through the language's generic Go translator.
func fyneStmtDispatch(s ir.Stmt, tr *fyneTranslator, gc *golang.GoIRContext) []string {
	switch n := s.(type) {
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
			tag, _ := lowerStringArg(call, 0)
			emission := tr.OnCreateNode(n.Name, tag)
			return splitLines(emission)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			switch {
			case isLowerIntrinsic(n.Call, "AppendChild"):
				p, c := lowerIdentArg(n.Call, 0), lowerIdentArg(n.Call, 1)
				// AppendChild's parent is always our `container` local
				// (because slot Funcs receive a CanvasObject, asserted
				// above). Override the raw arg name.
				return splitLines(tr.OnAppendChild("container", c))
				_ = p
			case isLowerIntrinsic(n.Call, "RemoveChild"):
				_, c := lowerIdentArg(n.Call, 0), lowerIdentArg(n.Call, 1)
				return splitLines(tr.OnRemoveChild("container", c))
			case isLowerIntrinsic(n.Call, "AttachHandler"):
				node := lowerIdentArg(n.Call, 0)
				evt, _ := lowerStringArg(n.Call, 1)
				h := lowerIdentArg(n.Call, 2)
				return splitLines(tr.OnAttachHandler(node, evt, h))
			}
		}
	case *ir.Assign:
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return splitLines(tr.OnPropAssign(id.Name, sel.Field, n.Value))
			}
		}
	}
	return gc.EvalStmt(s)
}

// Small helpers — keep close to the dispatch logic since they're only
// used here.
func isLowerIntrinsic(call *ir.Call, name string) bool {
	return call != nil && call.Func != nil && call.Func.Intrinsic == name
}

func lowerStringArg(call *ir.Call, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	if l, ok := call.Args[i].Value.(*ir.Literal); ok && l.Type == ir.TypString {
		return l.Raw, true
	}
	return "", false
}

func lowerIdentArg(call *ir.Call, i int) string {
	if i >= len(call.Args) {
		return ""
	}
	if id, ok := call.Args[i].Value.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
```

Note: the AppendChild override (`"container"` instead of the raw parent arg) is the contract we set up at slot entry. The raw arg's name in the IR is `parent` (the Func param), but our Go-side asserted `container := parent.(*fyne.Container)` and we want subsequent code to use `container`. Keep this special case isolated; it's not a generic intrinsic-walker concern.

- [ ] **Step 1: Apply the edits**

Edit `codegen/platform/fyne/compiler_ir.go` as described. Make sure `"git.duckfam.us/jonathan/sngl/ir"` import is present (it already is).

- [ ] **Step 2: Build**

Run: `go build ./codegen/platform/fyne/...`
Expected: clean (if not, address compile errors before continuing).

- [ ] **Step 3: Run fyne unit tests**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: dispatch __renderSlot<N> Funcs through fyneTranslator

The synthesized renderSlot Funcs from passReactivity now emit as
Model methods. Each stmt routes either through fyneTranslator
(lower.* intrinsics, element-ref Assigns) or through the language's
generic Go translator (For loop, Assign on locals, etc.).

Removes the fn.Synthesized skip — synthesized Funcs now have a real
emission path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Emit `__slot<N>` Vars as `[]fyne.CanvasObject` Model fields

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`

Plan A's guard at `codegen/platform/fyne/compiler_ir.go` around line 110 currently skips `v.Synthesized` Vars. Now that we emit `__renderSlot<N>` Funcs, those funcs reference `__slot<N>` — which has to exist on the Model.

The IR type for `__slot<N>` is `list<dyn>`. Fyne should emit it as `[]fyne.CanvasObject` so the loop teardown (`for __entry := range m.__slot0 { container.Remove(__entry) }`) type-checks.

- [ ] **Step 1: Find the synthesized-var skip**

Open `codegen/platform/fyne/compiler_ir.go` near line 110 (the var-collection loop) and locate:

```go
// Synthesized by passReactivity; fyne does not yet consume these (Plan B).
if v.Synthesized {
	continue
}
```

- [ ] **Step 2: Replace the skip with type override**

```go
if v.Synthesized {
	// Plan A's __slot<N> list<dyn> vars hold widget refs at runtime.
	// Emit as []fyne.CanvasObject so the renderSlot teardown loop
	// (range over the slice, container.Remove each entry) compiles.
	info.binds = append(info.binds, irBind{
		name:   v.Name,
		goType: "[]fyne.CanvasObject",
		init:   "nil",
	})
	continue
}
```

- [ ] **Step 3: Build + run fyne tests**

Run: `go build ./codegen/platform/fyne/... && go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: emit __slot<N> Vars as []fyne.CanvasObject fields

renderSlot bodies range over m.__slotN and call container.Remove
on each entry, so the field must be a slice of CanvasObjects.
Replaces the Synthesized-Var skip with a type-overridden bind.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Translate `__slotN = stdlib.ListPush(__slotN, #__nN)` to Go append

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go` (or a small new helper)

Background: inside renderSlot Func bodies, slot-tracking assigns look like:

```
__slot0 = stdlib.ListPush(__slot0, #__n0)
```

When `gc.EvalStmt` translates that today, it emits something like `__slot0 = stdlib.ListPush(__slot0, m.__n0)` — but `ListPush` isn't a real Go function. The codebase emits stdlib intrinsics through some translator path (see `golang/helpers.go:274` per the earlier grep). For fyne the slot field is `m.__slot0` and the correct Go is:

```go
m.__slot0 = append(m.__slot0, m.__n0)
```

The cleanest fix is in `fyneStmtDispatch`: detect this exact shape and emit the `append`. Otherwise we'd be threading list-append translation through a lang-translator hook.

- [ ] **Step 1: Write a small unit test**

Append to `codegen/platform/fyne/intrinsic_translator_test.go`:

```go
func TestFyneStmtDispatch_SlotListPush(t *testing.T) {
	listPush := &ir.Func{Name: "ListPush", Intrinsic: "ListPush"}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0"},
		Value: &ir.Call{
			Receiver: &ir.Ident{Name: "stdlib"},
			Func:     listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0"}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	want := "m.__slot0 = append(m.__slot0, m.__n0)"
	if got != want {
		t.Errorf("slot ListPush dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestFyneStmtDispatch_SlotListPush -v`
Expected: FAIL (the dispatch's default path produces something else).

- [ ] **Step 3: Special-case in fyneStmtDispatch**

In `compiler_ir.go`, find `fyneStmtDispatch`'s `*ir.Assign` case. Add the slot-push detection BEFORE the existing element-ref-Select branch:

```go
case *ir.Assign:
    if isSlotListPush(n) {
        target := "m." + n.Target.(*ir.Ident).Name
        call := n.Value.(*ir.Call)
        elem := lowerIdentArg(call, 1)
        return []string{target + " = append(" + target + ", " + modelRef(elem) + ")"}
    }
    if sel, ok := n.Target.(*ir.Select); ok {
        if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
            return splitLines(tr.OnPropAssign(id.Name, sel.Field, n.Value))
        }
    }
```

And the helper:

```go
// isSlotListPush detects the slot-tracking pattern
// `__slotN = stdlib.ListPush(__slotN, #__nN)` emitted by
// passReactivity's __renderSlot<N> generators.
func isSlotListPush(a *ir.Assign) bool {
	id, ok := a.Target.(*ir.Ident)
	if !ok || !strings.HasPrefix(id.Name, "__slot") {
		return false
	}
	call, ok := a.Value.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Intrinsic != "ListPush" {
		return false
	}
	if len(call.Args) != 2 {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./codegen/platform/fyne/ -run TestFyneStmtDispatch_SlotListPush -v`
Expected: PASS.

- [ ] **Step 5: Run full fyne tests**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: translate slot ListPush assignment to Go append

Inside __renderSlot bodies, passReactivity emits
'__slotN = stdlib.ListPush(__slotN, #__nN)' to track created widget
refs. fyneStmtDispatch now special-cases this shape and emits the
plain 'm.__slotN = append(m.__slotN, m.__nN)' form so subsequent
teardown loops work against a real Go slice.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Translate `__slotN = []` reset to Go nil-assignment

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`

Inside renderSlot bodies, after teardown:

```
__slot0 = []
```

`gc.EvalStmt` would emit `__slot0 = []dyn{}` or similar — but our field is `[]fyne.CanvasObject`, and the cleanest reset is `m.__slot0 = nil`.

- [ ] **Step 1: Write the test**

Append:

```go
func TestFyneStmtDispatch_SlotReset(t *testing.T) {
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0"},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	want := "m.__slot0 = nil"
	if got != want {
		t.Errorf("slot reset dispatch:\ngot:  %q\nwant: %q", got, want)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestFyneStmtDispatch_SlotReset -v`
Expected: FAIL.

- [ ] **Step 3: Special-case reset**

In `fyneStmtDispatch`, extend the `*ir.Assign` case:

```go
case *ir.Assign:
    if isSlotReset(n) {
        return []string{"m." + n.Target.(*ir.Ident).Name + " = nil"}
    }
    if isSlotListPush(n) {
        // ... existing ...
```

Helper:

```go
// isSlotReset detects '__slotN = []' emitted by passReactivity at
// the head of every __renderSlot<N> body before re-rendering.
func isSlotReset(a *ir.Assign) bool {
	id, ok := a.Target.(*ir.Ident)
	if !ok || !strings.HasPrefix(id.Name, "__slot") {
		return false
	}
	ll, ok := a.Value.(*ir.ListLit)
	if !ok {
		return false
	}
	return len(ll.Elems) == 0
}
```

- [ ] **Step 4: Run the test + full fyne suite**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: translate slot reset '__slotN = []' to nil assignment

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Translate the teardown For loop's iterator ref

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`

Inside renderSlot:

```
for __entry = __slot0 {
    lower.RemoveChild(#parent, __entry)
}
```

`gc.EvalStmt` handles `*ir.For` via `evalFor` (which exists today). It would emit something like:

```go
for _, __entry := range m.__slot0 {
	container.Remove(__entry)
}
```

(assuming `m.__slot0` resolves correctly via the slot Var being on Model, which Task 8 set up).

`evalFor` builds `range <Iter>`. The `Iter` is `&ir.Ident{Name: "__slot0"}`. We need `gc.EvalExpr` to render that as `m.__slot0`. Today's GoIRContext.EvalExpr handles Ident by looking up the symbol context — if no symbol resolution gives it Model membership, it'll emit bare `__slot0` which won't compile.

The simplest fix: in `fyneStmtDispatch`, detect `*ir.For` whose `Iter` is an `Ident` referring to a slot name, and rewrite the Iter to a `Select{m, __slotN}` before delegating to `gc.EvalStmt`. Or: handle the for loop body inline.

Try the rewrite path:

- [ ] **Step 1: Test**

Append:

```go
func TestFyneStmtDispatch_SlotTeardownFor(t *testing.T) {
	removeChild := &ir.Func{Name: "RemoveChild", Intrinsic: "RemoveChild"}
	stmt := &ir.For{
		Key:  "__entry",
		Iter: &ir.Ident{Name: "__slot0", Type: ir.ListOf(ir.TypDyn)},
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Receiver: &ir.Ident{Name: "lower"},
				Func:     removeChild,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: "parent", IsElementRef: true}},
					{Value: &ir.Ident{Name: "__entry"}},
				},
			}},
		},
	}
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	lines := fyneStmtDispatch(stmt, tr, stubGC())
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "for _, __entry := range m.__slot0") {
		t.Errorf("expected range over m.__slot0; got: %s", got)
	}
	if !strings.Contains(got, "container.Remove(__entry)") {
		t.Errorf("expected container.Remove(__entry); got: %s", got)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestFyneStmtDispatch_SlotTeardownFor -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `fyneStmtDispatch`, intercept the `*ir.For` case for slot-iterator loops:

```go
case *ir.For:
    // Slot teardown: `for __entry = __slot0 { ... }` iterates a Model
    // field. Recurse into the body via the intrinsic dispatcher so the
    // RemoveChild call routes through tr; rewrite the Iter ident so the
    // emitted Go ranges over m.__slot0.
    if id, ok := n.Iter.(*ir.Ident); ok && strings.HasPrefix(id.Name, "__slot") {
        var bodyLines []string
        for _, s := range n.Body {
            for _, l := range fyneStmtDispatch(s, tr, gc) {
                bodyLines = append(bodyLines, "\t"+l)
            }
        }
        lines := []string{"for _, " + n.Key + " := range m." + id.Name + " {"}
        lines = append(lines, bodyLines...)
        lines = append(lines, "}")
        return lines
    }
```

Put this case BEFORE the default `return gc.EvalStmt(s)` fallthrough so generic for-loops still delegate.

- [ ] **Step 4: Run the test + full suite**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: slot teardown For ranges over m.__slot<N>

Detect 'for __entry = __slotN { ... }' in renderSlot bodies, rewrite
the Iter to range over the Model field, and recurse the body through
fyneStmtDispatch so nested intrinsics (lower.RemoveChild) route
correctly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Integration test — compile a reactive-if SNGL fixture

**Files:**
- Create: `codegen/platform/fyne/intrinsic_integration_test.go`

End-to-end check: take the SNGL fixture we know exercises reactive `if`, run the full fyne pipeline, assert the emitted Go contains the expected method/call sites.

- [ ] **Step 1: Write the test**

```go
package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIntegration_ReactiveIfEmitsRenderSlot(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	if err := lower.Lower(pkg, lower.Caps{NoReactivity: true}, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	resp, err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("generate error: %s", resp.Error)
	}
	if len(resp.Files) == 0 {
		t.Fatal("no files emitted")
	}
	out := string(resp.Files[0].Data())

	for _, snippet := range []string{
		"func (m *Model) __renderSlot0(parent fyne.CanvasObject)",
		"container, _ := parent.(*fyne.Container)",
		"for _, __entry := range m.__slot0",
		"container.Remove(__entry)",
		"m.__slot0 = nil",
		"if visible",
		"m.__n0 = widget.NewLabel",
		"m.__n0.SetText(fmt.Sprint(",
		"container.Add(m.__n0)",
		"m.__slot0 = append(m.__slot0, m.__n0)",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted Go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}
}
```

Adapt `d.Severity == 0` to the real error severity constant — check existing checker tests for the right symbol.

- [ ] **Step 2: Run**

Run: `go test ./codegen/platform/fyne/ -run TestIntegration_ReactiveIfEmitsRenderSlot -v`
Expected: PASS. If any snippet is missing, the message identifies the gap — investigate which Task's emission is off.

- [ ] **Step 3: Run the full fyne suite**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 4: Run `go tool verify`**

Run: `go tool verify 2>&1 | tail -10`
Expected: still 33/109 baseline failures, no new ones. If new fyne failures appear, fix before moving on.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/intrinsic_integration_test.go
git commit -m "fyne: integration test for reactive-if renderSlot emission

End-to-end: parse a SNGL component with reactive if, run NoReactivity
lowering, compile via fyne, assert the emitted Go contains the
expected __renderSlot0 method, container assertion, teardown loop,
slot reset, body widget creation, prop setter, and slot tracking.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: Plan handoff annotation

**Files:**
- Modify: `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`

- [ ] **Step 1: Update the migration-order note in the spec**

Find the existing annotation:

```
> **Plan A** (`docs/superpowers/plans/2026-05-12-reactivity-lowering-foundation.md`)
> lands steps 1–3. Plans B/C/D cover per-platform conversion (fyne, gtk4,
> html); Plan E covers the final audit.
```

Replace with:

```
> **Plan A** (`docs/superpowers/plans/2026-05-12-reactivity-lowering-foundation.md`)
> lands steps 1–3. **Plan B** (`docs/superpowers/plans/2026-05-12-fyne-intrinsic-translator.md`)
> lands the fyne intrinsic translator + slot-Func consumption (validates
> the pipeline on fyne; old tree walker still in place). **Plan B.2**
> enables NoDeclarative on fyne, switches BuildUI to walk lowered IR,
> and rips the parallel updater pipeline. Plans C/D cover gtk4 / html.
> Plan E is the final audit.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md
git commit -m "docs(spec): annotate Plan B scope on reactivity lowering design

Plan B (this plan) lands the fyne intrinsic translator and wires
slot-Func consumption. Plan B.2 will follow with the bigger
NoDeclarative-on-fyne rewrite.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Testing summary

Plan B's verification at close:

- `go test ./codegen/platform/fyne/...` — all green, including the new translator unit tests + integration test.
- `go test ./codegen/lang/golang/...` — green; new `*ir.If` handling exercised.
- `go tool verify` — at most the pre-session baseline failures (33/109 from existing bubbletea/fyne/gtk4 reactivity issues). No new failures.
- `FuzzLoweredDocument` (from Plan A) still passes; Plan B doesn't touch lowering.
- A reactive `if` in a SNGL component compiled with `--platform fyne` produces working Go that toggles a widget via the new pipeline.

## What's NOT in Plan B (deferred to Plan B.2)

- The `lateReactive` / `recordLateReactive` / `resolveReactiveTokens` machinery in `view_ir.go` stays in place. It coexists with the new translator; Plan B.2 deletes it once `WalkLowered` covers all reactive paths.
- The `updaters` / `addUpdater` / `FindAffected` / `updateIf` / `updateFor` parallel pipeline stays. Plan B.2 rips it after `NoDeclarative` is enabled and the main BuildUI emission moves to the walker.
- `scaffold.go`'s `UpdaterNames`, `AffectedUpdaters`, `doRefresh` template slots stay. Plan B.2 removes the now-dead template arms.
- `view_ir.go`'s `renderConditional`, `renderFor`, `localMode` stay. Plan B.2 removes them along with the rest of the tree walker.
- Enabling `NoDeclarative` in `Capabilities()`. Plan B.2 turns it on (and absorbs the resulting fan-out of test breakage as the main BuildUI emission switches paths).

After Plan B, fyne's reactive `if` and `for` run through TWO pipelines simultaneously — the old `updateIf<N>` / `updateFor<N>` updater calls and the new `__renderSlot<N>` Func calls. Both fire on the same dep mutation. The visual outcome is correct (just emits the same widget mutations twice). Plan B.2 makes this exactly-once.
