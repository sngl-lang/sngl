# GTK4 Intrinsic Conversion Implementation Plan (Plan C)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the same conversion to gtk4 that Plans B + B.2 applied to fyne: implement a `gtk4Translator` for the `codegen.IntrinsicTranslator` interface, enable `NoDeclarative`, switch BuildUI emission onto `WalkLowered`, resolve the `__root` sentinel, and rip the parallel-reactive pipeline (`localMode`, `lateReactive`/`SNGLREACT` tokens, `updaters`/`FindAffected`/`doRefresh`, `buildReactiveRefresh`, `localWidgets`).

**Architecture:** GTK4's parallel-reactive pipeline mirrors fyne's structurally — same `renderStmt` tree walker, same `lateReactive`/`nodeBindings` token machinery, same `updaters`/`FindAffected` dispatch, same `localMode` for-loop scoping. The conversion shape is identical to Plan B+B.2; only the **emission target** differs (GTK4 C bridge calls via cgo + GIR-derived metadata, rather than Fyne Go methods + a blueprint table).

**Tech Stack:** Go (with cgo for the C bridge), GTK4 C API, the `codegen.IntrinsicTranslator` interface, `codegen.WalkLowered`, GIR (GObject Introspection Repository) files in `codegen/platform/gtk4/gir/`.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` §2.3 (gtk4 conversion).

**Predecessors:** Plan A (lowering foundation), Plan B (fyne intrinsic translator), Plan B.2 (fyne NoDeclarative + cleanup). All on `main`.

**Successor:** Plan D (html conversion) and Plan E (final audit).

---

## Reference: how Plan B+B.2 solved this for fyne

This plan reuses everything from Plan B+B.2 with minimal restating. Engineers should keep these two open in adjacent tabs:

- `docs/superpowers/plans/2026-05-12-fyne-intrinsic-translator.md` — fyne tasks 1-13 (build translator, wire to slot Funcs).
- `docs/superpowers/plans/2026-05-12-fyne-nodeclarative-conversion.md` — fyne tasks 1-26 (cleanup + NoDeclarative + rip pipeline).

Plan A's IR-level pieces (`Synthesized` flag on Ident, `__root` Var synthesis, `lower.RemoveChild` intrinsic, `passReactivity` structural extension) are already in place — gtk4 just needs to consume them. Plan B.2's shared `IntrinsicTranslator` widening (`OnDefault`/`OnSlotReset`/`OnSlotAppend`/`OnIter`/`OnCond` methods, structural recursion in `WalkLowered`) is also in place.

## Key differences from fyne (the gtk4-specific work)

| Concern | Fyne approach | GTK4 approach |
|---|---|---|
| **Widget metadata** | hardcoded `fyneBlueprint` table | GIR-loaded `ClassInfo` from `gir/gir.go` |
| **Constructor emission** | `widget.NewLabel("")` Go call | `C.gtk_label_new(nil)` cgo call |
| **Property setter** | `m.lbl.SetText(v)` method | `C.gtk_label_set_text((*C.GtkLabel)(unsafe.Pointer(m.lbl)), C.CString(v))` |
| **Per-tag setter map** | per-blueprint `bindMeta.Target` | `gtkSetterTable[CType][propName]` (`view_ir.go:1233-1253`) |
| **Container append** | `m.box.Add(m.child)` | `C.gtk_box_append(...)` (`gtkChildAddTable`, `view_ir.go:1302-1305`) |
| **Signal connection** | `m.btn.OnTapped = m.handler` | `C.sngl_connect(m.btn, "clicked", idx)` + `snglCallbacks` array |
| **Field naming** | bare ident on Model | typed `*C.GtkWidget` ref + `(*C.GtkLabel)(unsafe.Pointer(...))` cast at setter |
| **Memory** | Go GC handles it | C.CString must be paired with C.free; gtk4 ignores leaks today |

These are translator-internal concerns. The structural conversion is identical to Plan B+B.2.

---

## File Structure

**Create:**
- `codegen/platform/gtk4/intrinsic_translator.go` — `gtk4Translator` implementing `codegen.IntrinsicTranslator`.
- `codegen/platform/gtk4/intrinsic_translator_test.go` — unit tests.
- `codegen/platform/gtk4/intrinsic_integration_test.go` — reactive-if end-to-end + compile check.

**Modify (substantially):**
- `codegen/platform/gtk4/compiler_ir.go` — replace renderStmt-based BuildUI emission with `WalkLowered + gtk4Translator`; delete `updaters`/`FindAffected`/`doRefresh`/`buildReactiveRefresh` plumbing; emit `__renderSlot<N>` Funcs.
- `codegen/platform/gtk4/view_ir.go` — most of this file goes away. Surviving pieces (GIR-driven widget metadata helpers, signal-trampoline machinery) move into `intrinsic_translator.go` or `compiler_ir.go`.
- `codegen/platform/gtk4/gtk4.go` — `Capabilities()` returns `{NoReactivity: true, NoDeclarative: true}`.
- `codegen/platform/gtk4/scaffold.go` — drop `UpdaterNames`/`AffectedUpdaters`/`doRefresh`-related fields.
- `codegen/platform/gtk4/templates/model.go.tmpl` — drop `/*REACTIVE_REFRESH*/` slot and the `doRefresh` body.

**No changes** to:
- `codegen/platform/gtk4/gir/` — GIR parser stays as widget-metadata source.
- `codegen/platform/gtk4/gtk4.sngl` — stdlib component declarations.
- `codegen/platform/gtk4/templates/callbacks.go.tmpl` — signal trampoline machinery.
- `internal/lower/` — already complete via Plan A.

---

## Phase A: Build `gtk4Translator`

### Task 1: Scaffold `gtk4Translator` with interface compliance

**Files:**
- Create: `codegen/platform/gtk4/intrinsic_translator.go`

Mirror Plan B's Task 1 (fyne scaffold).

- [ ] **Step 1: Create the file**

```go
package gtk4

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// gtk4Translator implements codegen.IntrinsicTranslator for gtk4.
// Emits cgo calls into the GTK4 C API for widget construction,
// property setters, child attachment, and signal connection.
//
// Per-tag widget metadata comes from the GIR registry already
// loaded by the gtk4 platform's Resolve() path; per-setter / per-
// container function names come from the static tables in
// view_ir.go (gtkSetterTable, gtkChildAddTable, gtkGetterTable).
type gtk4Translator struct {
	gc        *golang.GoIRContext
	registry  *gir.TypeRegistry
	fieldSink func(name, cType string)
	idCTypes  map[string]string // synthetic id ("__n0") → GTK C type ("GtkLabel")
}

// newGtk4Translator constructs a translator. registry is the GIR-
// loaded widget metadata; fieldSink receives (widget-name, C-type)
// pairs so the enclosing emitter can declare the field on Model.
func newGtk4Translator(gc *golang.GoIRContext, registry *gir.TypeRegistry, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{
		gc:        gc,
		registry:  registry,
		fieldSink: fieldSink,
		idCTypes:  map[string]string{},
	}
}

// Compile-time interface check.
var _ codegen.IntrinsicTranslator = (*gtk4Translator)(nil)

func (t *gtk4Translator) OnCreateNode(id, tag string) []string         { return nil }
func (t *gtk4Translator) OnAppendChild(parent, child string) []string  { return nil }
func (t *gtk4Translator) OnRemoveChild(parent, child string) []string  { return nil }
func (t *gtk4Translator) OnAttachHandler(n, e, h string) []string      { return nil }
func (t *gtk4Translator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) []string {
	return nil
}
func (t *gtk4Translator) OnDefault(stmt ir.Stmt) []string        { return t.gc.EvalStmt(stmt) }
func (t *gtk4Translator) OnSlotReset(slotID string) []string     { return nil }
func (t *gtk4Translator) OnSlotAppend(slotID, child string) []string { return nil }
func (t *gtk4Translator) OnIter(iter ir.Expr) string             { return t.gc.EvalExpr(iter) }
func (t *gtk4Translator) OnCond(cond ir.Expr) string             { return t.gc.EvalExpr(cond) }
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./codegen/platform/gtk4/...`
Expected: clean (interface compliance enforced by the `_ = (*gtk4Translator)(nil)` line).

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/gtk4/intrinsic_translator.go
git commit -m "gtk4: scaffold gtk4Translator (IntrinsicTranslator impl)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `OnCreateNode` via GIR lookup + tag→CType resolution

**Goal:** map a SNGL tag (e.g. `text`, `button`) to a GTK C type (`GtkLabel`, `GtkButton`), emit `m.<id> = (*C.GtkWidget)(unsafe.Pointer(C.gtk_<lowered>_new(args...)))`, register the field on Model.

**Background:** SNGL tag → GTK CType mapping is currently scattered across `renderStdlibComponent` (`view_ir.go:828+`) and `renderStdlibInline`. The cleanest move: build a `tagToCType` map alongside the existing `gtkSetterTable`, populated by reading the stdlib's `gtk4.sngl` once at init.

Pragmatic alternative: hardcode the common ones in a small switch (mirror fyne's `zeroArgsFor` Plan-B simplicity), since the gtk4 stdlib component list is bounded.

- [ ] **Step 1: Write the failing test**

Create `codegen/platform/gtk4/intrinsic_translator_test.go`:

```go
package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
)

func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(nil)
}

func stubRegistry(t *testing.T) *gir.TypeRegistry {
	t.Helper()
	reg, err := loadGirRegistry()
	if err != nil {
		t.Skipf("GIR not available: %v", err)
	}
	return reg
}

func TestGtk4Translator_OnCreateNode_Text(t *testing.T) {
	reg := stubRegistry(t)
	var fields []string
	tr := newGtk4Translator(stubGC(), reg, func(name, cType string) {
		fields = append(fields, name+" "+cType)
	})
	got := strings.Join(tr.OnCreateNode("__n0", "text"), "\n")
	if !strings.Contains(got, "C.gtk_label_new") {
		t.Errorf("expected C.gtk_label_new in emission; got: %s", got)
	}
	want := "__n0 GtkLabel"
	found := false
	for _, f := range fields {
		if f == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field registration %q; got: %v", want, fields)
	}
}

func TestGtk4Translator_OnCreateNode_UnknownTag(t *testing.T) {
	reg := stubRegistry(t)
	tr := newGtk4Translator(stubGC(), reg, func(_, _ string) {})
	got := tr.OnCreateNode("__n0", "wibble")
	if len(got) != 0 {
		t.Errorf("expected empty emission for unknown tag; got: %v", got)
	}
}
```

The test uses `loadGirRegistry()` which needs to be a helper exposing the gtk4 package's existing GIR lookup. Find where `view_ir.go` does `vc.registry.Classes[...]` and trace back to who builds `registry` — that's the loader you expose:

```go
// In gtk4.go or a new helper, add a package-level helper:
func loadGirRegistry() (*gir.TypeRegistry, error) {
	// adapt to whatever the existing gtk4 GIR loader does
	return loadOrParseGir() // or whatever the actual function is named
}
```

If the existing loader is keyed off an instance method, expose a package-level wrapper.

- [ ] **Step 2: Run test, expect failure**

Run: `go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnCreateNode -v`
Expected: FAIL (stubs return nil).

- [ ] **Step 3: Implement OnCreateNode**

Add the tag→CType mapping:

```go
// gtk4TagToCType returns the GTK C type for a SNGL stdlib component tag.
// Returns "" for unknown tags (e.g. user-defined components).
func gtk4TagToCType(tag string) string {
	switch tag {
	case "text", "label":
		return "GtkLabel"
	case "button":
		return "GtkButton"
	case "input", "entry":
		return "GtkEntry"
	case "vbox":
		return "GtkBox" // orientation set via constructor
	case "hbox":
		return "GtkBox"
	case "checkbox", "switch":
		return "GtkCheckButton"
	case "scroll":
		return "GtkScrolledWindow"
	}
	return ""
}

// gtk4Constructor returns the C constructor call for a tag. Uses
// per-tag knowledge for tags that take orientation or default args.
func gtk4Constructor(tag string) string {
	switch tag {
	case "vbox":
		return "C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 6)"
	case "hbox":
		return "C.gtk_box_new(C.GTK_ORIENTATION_HORIZONTAL, 6)"
	case "text", "label":
		return "C.gtk_label_new(nil)"
	case "button":
		return `C.gtk_button_new_with_label(C.CString(""))`
	case "input", "entry":
		return "C.gtk_entry_new()"
	case "checkbox":
		return "C.gtk_check_button_new()"
	case "scroll":
		return "C.gtk_scrolled_window_new()"
	}
	return ""
}

func (t *gtk4Translator) OnCreateNode(id, tag string) []string {
	cType := gtk4TagToCType(tag)
	if cType == "" {
		return nil
	}
	ctor := gtk4Constructor(tag)
	if ctor == "" {
		return nil
	}
	t.fieldSink(id, cType)
	t.idCTypes[id] = cType
	return []string{
		"m." + id + " = (*C." + cType + ")(unsafe.Pointer(" + ctor + "))",
	}
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnCreateNode -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnCreateNode emits cgo constructor + Model field

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `OnAppendChild` / `OnRemoveChild` via container-type dispatch

**Background:** GTK4 container append is per-type — `gtk_box_append`, `gtk_scrolled_window_set_child`, `gtk_window_set_child`, etc. The existing `gtkChildAddTable` (`view_ir.go:1302-1305`) covers the basics. For Plan C scope: reuse that table, fall back to a runtime no-op + comment for unsupported containers.

- [ ] **Step 1: Write the failing test**

```go
func TestGtk4Translator_OnAppendChild_Box(t *testing.T) {
	reg := stubRegistry(t)
	tr := newGtk4Translator(stubGC(), reg, func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "vbox")
	_ = tr.OnCreateNode("__n1", "text")
	got := strings.Join(tr.OnAppendChild("m.__n0", "m.__n1"), "\n")
	if !strings.Contains(got, "C.gtk_box_append") {
		t.Errorf("expected C.gtk_box_append; got: %s", got)
	}
}

func TestGtk4Translator_OnRemoveChild_Box(t *testing.T) {
	reg := stubRegistry(t)
	tr := newGtk4Translator(stubGC(), reg, func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "vbox")
	_ = tr.OnCreateNode("__n1", "text")
	got := strings.Join(tr.OnRemoveChild("m.__n0", "m.__n1"), "\n")
	if !strings.Contains(got, "C.gtk_box_remove") {
		t.Errorf("expected C.gtk_box_remove; got: %s", got)
	}
}
```

The tests use `"m.__n0"` (pre-qualified) — that matches Plan B.2's contract where the dispatch hands the translator the qualified name.

- [ ] **Step 2: Run, expect failure**

```bash
go test ./codegen/platform/gtk4/ -run "TestGtk4Translator_On(Append|Remove)Child" -v
```

- [ ] **Step 3: Implement**

```go
// gtk4ChildAppendFn returns the C function name for adding a child to
// the given parent CType. "" when no append path is known (would
// be a translator error).
func gtk4ChildAppendFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "C.gtk_box_append"
	case "GtkScrolledWindow":
		return "C.gtk_scrolled_window_set_child"
	case "GtkWindow", "GtkApplicationWindow":
		return "C.gtk_window_set_child"
	}
	return ""
}

func gtk4ChildRemoveFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "C.gtk_box_remove"
	}
	return ""
}

// parentCType extracts the C type from a qualified Model field name
// like "m.__n0". Returns "" for params like "container" (the slot
// function param — see Plan B.2 Task 6 for the equivalent fyne path).
func (t *gtk4Translator) parentCType(qualifiedName string) string {
	bare := strings.TrimPrefix(qualifiedName, "m.")
	return t.idCTypes[bare]
}

func (t *gtk4Translator) OnAppendChild(parent, child string) []string {
	cType := t.parentCType(parent)
	if cType == "" {
		// Slot-function `container` param — assume GtkBox (the slot's
		// runtime parent is typed as *C.GtkBox in emitIRSlotFunc, mirroring
		// fyne's *fyne.Container assumption).
		cType = "GtkBox"
	}
	fn := gtk4ChildAppendFn(cType)
	if fn == "" {
		return nil
	}
	// Cast both args to GtkWidget*.
	parentArg := "(*C.GtkWidget)(unsafe.Pointer(" + parent + "))"
	childArg := "(*C.GtkWidget)(unsafe.Pointer(" + child + "))"
	if cType == "GtkBox" {
		// gtk_box_append takes (*C.GtkBox, *C.GtkWidget)
		parentArg = "(*C.GtkBox)(unsafe.Pointer(" + parent + "))"
	}
	return []string{fn + "(" + parentArg + ", " + childArg + ")"}
}

func (t *gtk4Translator) OnRemoveChild(parent, child string) []string {
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	fn := gtk4ChildRemoveFn(cType)
	if fn == "" {
		return nil
	}
	parentArg := "(*C.GtkBox)(unsafe.Pointer(" + parent + "))"
	childArg := "(*C.GtkWidget)(unsafe.Pointer(" + child + "))"
	return []string{fn + "(" + parentArg + ", " + childArg + ")"}
}
```

Add `import "strings"` if not present.

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_On -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnAppendChild + OnRemoveChild emit container-typed cgo calls

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `OnPropAssign` via gtkSetterTable

**Background:** the static `gtkSetterTable[CType][propName]` in `view_ir.go:1233-1253` maps GIR property names to C setter functions. The translator's `OnPropAssign` looks up the node's CType, finds the setter, and emits the call with type-coercion for the value.

This task assumes `gtkSetterTable` remains accessible from `intrinsic_translator.go`. Either keep it in view_ir.go (cross-file access) or move it to a new `dispatch_tables.go` co-located file.

- [ ] **Step 1: Write the failing test**

```go
func TestGtk4Translator_OnPropAssign_LabelText(t *testing.T) {
	reg := stubRegistry(t)
	tr := newGtk4Translator(stubGC(), reg, func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "text")

	val := &ir.Literal{Type: ir.TypString, Raw: "hi"}
	got := strings.Join(tr.OnPropAssign("__n0", "value", val), "\n")
	if !strings.Contains(got, "C.gtk_label_set_text") {
		t.Errorf("expected gtk_label_set_text; got: %s", got)
	}
	if !strings.Contains(got, `C.CString`) {
		t.Errorf("expected C.CString conversion; got: %s", got)
	}
}
```

Note: SNGL's `text` component exposes `value` as the user-facing prop name (per `gtk4.sngl`); the GIR property is `label` on GtkLabel. There's a mapping somewhere — read `gtk4.sngl` and `view_ir.go` to find where SNGL `value` → GIR `label` is resolved. The translator must do the same.

If the mapping is held in `gtkSetterTable` keyed on `value` directly (it might be, depending on how view_ir.go consumes it), use it as-is. Otherwise add a `sngl-prop → gir-prop` translation step in OnPropAssign.

- [ ] **Step 2: Run, expect failure**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnPropAssign -v
```

- [ ] **Step 3: Implement**

```go
func (t *gtk4Translator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) []string {
	cType, ok := t.idCTypes[nodeID]
	if !ok {
		return nil
	}
	setter := gtkSetter(cType, prop)
	if setter == "" {
		return nil
	}
	val := t.gc.EvalExpr(valueExpr)
	// String props go through C.CString.
	// Other types (int, bool) need their own conversions; gtkSetterTable
	// declares the value IR type — adapt the conversion accordingly.
	valArg := "C.CString(" + val + ")"
	cast := "(*C." + cType + ")(unsafe.Pointer(m." + nodeID + "))"
	return []string{"C." + setter + "(" + cast + ", " + valArg + ")"}
}
```

(`gtkSetter` is the existing helper at `view_ir.go:1255-1260` — reuse it.)

Note: this is a minimal first-cut that only handles string props. Plan C Task 11 (the "make integration test compile" task) extends it for ints/bools as needed.

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnPropAssign -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnPropAssign emits cgo setter via gtkSetterTable

Strings only at this stage; other types added on demand.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: `OnAttachHandler` via signal-trampoline

**Background:** gtk4 wires handlers through `C.sngl_connect(widget, signal, callbackIndex)` + a `snglCallbacks` array. Connection is per-(widget, signal) — see `connectSignal` (`view_ir.go:745-801`). The translator's `OnAttachHandler` mirrors that path.

There's no per-event signature in gtk4 the way fyne has `bindings` with `BindParam` — gtk4 hardcodes a few signal mappings (e.g. button click → `"clicked"` signal, entry input → `"changed"` signal).

- [ ] **Step 1: Write the failing test**

```go
func TestGtk4Translator_OnAttachHandler_ButtonClick(t *testing.T) {
	reg := stubRegistry(t)
	tr := newGtk4Translator(stubGC(), reg, func(_, _ string) {})
	_ = tr.OnCreateNode("__n0", "button")
	got := strings.Join(tr.OnAttachHandler("m.__n0", "click", "m.handleClick"), "\n")
	if !strings.Contains(got, `C.sngl_connect`) {
		t.Errorf("expected sngl_connect; got: %s", got)
	}
	if !strings.Contains(got, `"clicked"`) {
		t.Errorf("expected GTK signal name 'clicked'; got: %s", got)
	}
}
```

- [ ] **Step 2: Run, expect failure**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnAttachHandler -v
```

- [ ] **Step 3: Implement**

```go
// gtk4SignalFor maps a SNGL event name to the GTK signal name.
func gtk4SignalFor(cType, event string) string {
	switch cType {
	case "GtkButton":
		if event == "click" {
			return "clicked"
		}
	case "GtkEntry":
		if event == "input" || event == "change" {
			return "changed"
		}
	case "GtkCheckButton":
		if event == "change" {
			return "toggled"
		}
	}
	return ""
}

func (t *gtk4Translator) OnAttachHandler(node, event, handlerRef string) []string {
	bare := strings.TrimPrefix(node, "m.")
	cType := t.idCTypes[bare]
	signal := gtk4SignalFor(cType, event)
	if signal == "" {
		return nil
	}
	widget := "(*C.GtkWidget)(unsafe.Pointer(" + node + "))"
	// Callback registration:
	//   idx := len(snglCallbacks); snglCallbacks = append(...)
	//   C.sngl_connect(widget, C.CString("clicked"), C.int(idx))
	return []string{
		"snglCallbacks = append(snglCallbacks, " + handlerRef + ")",
		"C.sngl_connect(" + widget + ", C.CString(" + `"` + signal + `"` + "), C.int(len(snglCallbacks)-1))",
	}
}
```

Note: this is structurally simpler than the existing `connectSignal` path (which has entry-value PreFire logic for `@input(e)` with two-way bind). Plan C accepts the simplification — entry two-way binding is a separate concern that surfaces in Task 11 if it breaks.

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnAttachHandler -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnAttachHandler emits sngl_connect signal wiring

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `OnSlotReset` / `OnSlotAppend` — slot accumulator translation

**Background:** Plan A's `__slot<N>` Var is a `list<dyn>` of widget refs. fyne emits it as `[]fyne.CanvasObject` and uses `append`/`nil` (Plan B.2 Tasks 8, 11). gtk4 needs the equivalent for `*C.GtkWidget`.

- [ ] **Step 1: Write the failing test**

```go
func TestGtk4Translator_OnSlotReset(t *testing.T) {
	tr := newGtk4Translator(stubGC(), nil, func(_, _ string) {})
	got := strings.Join(tr.OnSlotReset("__slot0"), "\n")
	if got != "m.__slot0 = nil" {
		t.Errorf("expected 'm.__slot0 = nil'; got: %s", got)
	}
}

func TestGtk4Translator_OnSlotAppend(t *testing.T) {
	tr := newGtk4Translator(stubGC(), nil, func(_, _ string) {})
	got := strings.Join(tr.OnSlotAppend("__slot0", "__n0"), "\n")
	want := "m.__slot0 = append(m.__slot0, (*C.GtkWidget)(unsafe.Pointer(m.__n0)))"
	if got != want {
		t.Errorf("slot append mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
```

- [ ] **Step 2: Run, expect failure**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnSlot -v
```

- [ ] **Step 3: Implement**

```go
func (t *gtk4Translator) OnSlotReset(slotID string) []string {
	return []string{"m." + slotID + " = nil"}
}

func (t *gtk4Translator) OnSlotAppend(slotID, childID string) []string {
	return []string{
		"m." + slotID + " = append(m." + slotID + ", (*C.GtkWidget)(unsafe.Pointer(m." + childID + ")))",
	}
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnSlot -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnSlotReset / OnSlotAppend translate slot accumulator ops

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: `OnIter` slot-aware iter expression

Mirror Plan B.2 Task 4's fyne implementation: synthesized Idents resolve through Model receiver.

- [ ] **Step 1: Replace stub**

```go
func (t *gtk4Translator) OnIter(iter ir.Expr) string {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return "m." + id.Name
	}
	return t.gc.EvalExpr(iter)
}
```

- [ ] **Step 2: Test**

```go
func TestGtk4Translator_OnIter_SlotIter(t *testing.T) {
	tr := newGtk4Translator(stubGC(), nil, func(_, _ string) {})
	got := tr.OnIter(&ir.Ident{Name: "__slot0", Synthesized: true})
	if got != "m.__slot0" {
		t.Errorf("expected 'm.__slot0'; got: %s", got)
	}
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./codegen/platform/gtk4/ -run TestGtk4Translator_OnIter -v
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: OnIter qualifies synthesized slot refs as Model fields

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase B: Wire into the gtk4 emitter — slot Funcs only

### Task 8: Emit `__renderSlot<N>` Funcs via `WalkLowered`

Mirror Plan B Tasks 7-11 (fyne's slot-Func emission path), with adaptations for gtk4's cgo:

- Slot Func signature: `func (m *Model) __renderSlot0(container *C.GtkBox)`
- Type assertion: dropped (typed param like Plan B.2 Task 6).
- Use `codegen.WalkLowered(fn.Block, tr)` directly.

**Files:**
- Modify: `codegen/platform/gtk4/compiler_ir.go` — replace any `Synthesized` skip with a real emission path.

Search for `Synthesized` in `compiler_ir.go` — if it's not there yet (gtk4 doesn't have Plan B's tactical skip-guard because gtk4 hasn't been touched yet), the slot Funcs ARE being emitted but their bodies go through the legacy renderStmt path. Investigate the actual current state.

- [ ] **Step 1: Identify the current emission path for synthesized Funcs**

Run: `grep -n "fn.Synthesized\|allFuncs\|__renderSlot" codegen/platform/gtk4/compiler_ir.go`

If there's a Func-emission loop similar to fyne's old `for _, fn := range allFuncs`, locate it.

- [ ] **Step 2: Add `emitIRSlotFunc` analogous to fyne's**

Add to `compiler_ir.go`:

```go
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]gtkWidgetField, registry *gir.TypeRegistry) {
	fmt.Fprintf(b, "func (m *Model) %s(container *C.GtkBox) {\n", fn.Name)

	tr := newGtk4Translator(gc, registry, func(name, cType string) {
		*widgetFields = append(*widgetFields, gtkWidgetField{Name: name, CType: cType})
	})

	body := codegen.WalkLowered(fn.Block, tr)
	for _, l := range body {
		b.WriteString("\t")
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("}\n\n")
}
```

If `gtkWidgetField` doesn't exist (gtk4 uses a different field-tracking struct), find the equivalent and use it.

- [ ] **Step 3: Wire it into the Funcs loop**

In the existing Funcs-emission loop in `compiler_ir.go`, add:

```go
if fn.Synthesized {
    emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields, registry)
    continue
}
// ... existing path for non-synthesized funcs
```

If there's no existing Funcs loop because gtk4 doesn't yet emit user functions, you'll need to add the iteration. Check `compiler_ir.go:298-307` (Phase 1b component pre-render) — that's a related path.

- [ ] **Step 4: Emit `__slot<N>` and `__root` Vars**

Same as Plan B.2 Tasks 8-9 for fyne, with adjustments for gtk4 types:
- `__slot<N>` → `[]*C.GtkWidget`
- `__root` → `*C.GtkBox`

In gtk4's analog of `analyzeIR` (find it in `compiler_ir.go`), find where binds are accumulated. Add a Synthesized-var branch:

```go
if v.Synthesized {
    if v.Name == "__root" {
        // Emit as *C.GtkBox field. BuildUI initializes via
        // C.gtk_box_new() and reassigns Children.
        binds = append(binds, gtkBind{
            Name: v.Name, GoType: "*C.GtkBox",
            Init: "(*C.GtkBox)(unsafe.Pointer(C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 6)))",
        })
        continue
    }
    binds = append(binds, gtkBind{
        Name: v.Name, GoType: "[]*C.GtkWidget", Init: "nil",
    })
    continue
}
```

Adapt to gtk4's actual bind-tracking struct.

- [ ] **Step 5: Run, expect partial integration**

```bash
go test ./codegen/platform/gtk4/...
go build ./...
```

Expected: some test breakage. Document categorically.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/gtk4/compiler_ir.go
git commit -m "gtk4: emit __renderSlot<N> Funcs via WalkLowered + gtk4Translator

Mirrors Plan B Task 7 for fyne. Synthesized Funcs route through the
intrinsic translator; __slot<N> and __root vars emit as typed Model
fields.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Integration test for reactive `if` on gtk4

Mirror Plan B Task 12 (fyne integration test). Same SNGL fixture, same parse/check/lower/generate flow, assertions adapted for gtk4's emitted Go.

- [ ] **Step 1: Create the test**

Create `codegen/platform/gtk4/intrinsic_integration_test.go`:

```go
package gtk4

import (
	"bytes"
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
	resp, err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("generate error: %s", resp.Error)
	}
	var buf bytes.Buffer
	if _, err := resp.Files[0].WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, snippet := range []string{
		"func (m *Model) __renderSlot0(container *C.GtkBox)",
		"for _, __entry := range m.__slot0",
		"C.gtk_box_remove",
		"m.__slot0 = nil",
		"if m.visible",
		"m.__n0 = (*C.GtkLabel)",
		"C.gtk_label_new",
		"C.gtk_label_set_text",
		"C.gtk_box_append",
		"m.__slot0 = append(m.__slot0",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted Go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

	for _, leak := range []string{
		"lower.CreateNode",
		"lower.AppendChild",
		"lower.RemoveChild",
		"stdlib.ListPush",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("untranslated intrinsic %q leaked into emitted Go", leak)
		}
	}
}
```

- [ ] **Step 2: Run**

```bash
go test ./codegen/platform/gtk4/ -run TestIntegration -v
```

Expected: probably FAILS on several snippets — Task 8 wired the path but the integration may surface edge cases (signal names, prop name mappings, container types). Investigate each failure: which snippet is missing means which Task 2-7 emission is off. Fix and re-run.

- [ ] **Step 3: Commit (once all snippets match)**

```bash
git add codegen/platform/gtk4/intrinsic_integration_test.go
git commit -m "gtk4: integration test for reactive-if renderSlot emission

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase C: Enable `NoDeclarative` + BuildUI rewrite

### Task 10: Enable `NoDeclarative` in `Capabilities()`

Mirror Plan B.2 Task 12. THIS COMMIT BREAKS EXISTING gtk4 TESTS by design — subsequent tasks fix them.

- [ ] **Step 1: Update Capabilities**

In `codegen/platform/gtk4/gtk4.go`:

```go
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{NoReactivity: true, NoDeclarative: true}
}
```

- [ ] **Step 2: Run gtk4 tests; categorize failures**

```bash
go test ./codegen/platform/gtk4/... 2>&1 | tail -40
```

Document the failure pattern.

- [ ] **Step 3: Commit (failing tests are the tripwire)**

```bash
git add codegen/platform/gtk4/gtk4.go
git commit -m "gtk4: enable NoDeclarative in Capabilities()

Component bodies now lower to flat intrinsic-call sequences.
renderStmt tree walker is stale; subsequent tasks rewrite BuildUI
onto WalkLowered. Tests break in this commit by design.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Rewrite BuildUI body via `WalkLowered`

Mirror Plan B.2 Task 13. Replace the `vc.renderStmt(bodyStmts, "content")` walk with a `WalkLowered` pass. Collect top-level widget refs and append them to `m.__root`.

- [ ] **Step 1: Locate Phase 1 BuildUI emission**

In `compiler_ir.go:226-329` (the `emitIR` orchestrator), find where the tree walk happens. Likely around `compiler_ir.go:250` (`vc.renderStmt(bodyStmts, "content")`).

- [ ] **Step 2: Replace with WalkLowered**

```go
// Phase 1: walk the lowered body via gtk4Translator.
tr := newGtk4Translator(gc, registry, func(name, cType string) {
    widgetFields = append(widgetFields, gtkWidgetField{Name: name, CType: cType})
})
body := codegen.WalkLowered(bodyStmts, tr)
for _, l := range body {
    fmt.Fprintf(&buildBuf, "\t%s\n", l)
}
// Top-level refs (those not AppendChild'd anywhere) attach to m.__root.
for _, ref := range tr.topLevel {
    fmt.Fprintf(&buildBuf, "\tC.gtk_box_append((*C.GtkBox)(unsafe.Pointer(m.__root)), (*C.GtkWidget)(unsafe.Pointer(m.%s)))\n", ref)
}
```

The `tr.topLevel` field needs to exist on `gtk4Translator` — mirror Plan B.2 Task 14's fyne pattern: track creates minus appends.

- [ ] **Step 3: Add `topLevel` tracking to gtk4Translator**

In `intrinsic_translator.go`:

```go
type gtk4Translator struct {
    // ... existing ...
    topLevel []string // ordered list of refs not yet AppendChild'd
}
```

Update `OnCreateNode` to `t.topLevel = append(t.topLevel, id)`.

Update `OnAppendChild` to remove `child`'s bare name from `t.topLevel`:

```go
func (t *gtk4Translator) OnAppendChild(parent, child string) []string {
    bareChild := strings.TrimPrefix(child, "m.")
    for i, name := range t.topLevel {
        if name == bareChild {
            t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
            break
        }
    }
    // ... rest of existing emission
}
```

- [ ] **Step 4: Update BuildUI return**

The existing emission likely returns `content` (the walker's result var) or wraps in a window. With WalkLowered, the new pattern is to return `m.__root`:

```go
// At end of emitIRBuildUI (find it in compiler_ir.go):
fmt.Fprintf(b, "\treturn (*C.GtkWidget)(unsafe.Pointer(m.__root))\n")
```

If gtk4's BuildUI signature returns `unsafe.Pointer` or something else, adapt.

- [ ] **Step 5: Iterate on test breakage**

```bash
go test ./codegen/platform/gtk4/...
go build ./...
go test ./codegen/platform/gtk4/ -run TestIntegration -v
```

Fix specific failures as they surface. Most likely categories:
- Missing setter mappings in `gtkSetterTable` (e.g. button's `text` → `label` prop).
- Missing signal mappings in `gtk4SignalFor`.
- Untranslated `lower.*` intrinsics (translator stub missing case).

Adapt the translator per finding; commit each fix.

- [ ] **Step 6: Final commit**

```bash
git add codegen/platform/gtk4/
git commit -m "gtk4: rewrite BuildUI body via WalkLowered + gtk4Translator

Replaces the Phase 1 renderStmt tree-walk with a flat WalkLowered
pass over the lowered body. Top-level widget collection appends
to m.__root before returning. Mirrors Plan B.2 Tasks 13-14 for fyne.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase D: Rip the parallel pipeline

### Tasks 12-15: Iterative removal

Mirror Plan B.2 Phase E. Each task deletes a cluster; run tests after each.

- [ ] **Task 12**: Delete `renderConditional`/`renderIf`-with-info-tracking/`renderFor` from `view_ir.go`. Run tests, fix consumers.

- [ ] **Task 13**: Delete `updaters` slice, `addUpdater`, `widgetUpdater`, `emitUpdaters`, the `codegen.FindAffected` call sites. Update `compiler_ir.go` accordingly.

- [ ] **Task 14**: Delete `lateReactive`, `recordLateReactive`, `resolveReactiveTokens`, `nodeBindings`, `recordNodeBinding`, `/*SNGLREACT:i*/` and `/*REACTIVE_REFRESH*/` template slot resolution. Remove `buildReactiveRefresh`.

- [ ] **Task 15**: Delete `localMode`, `localWidgets`, `localCount`, the localMode-conditional branches in `allocWidget`/`recordWidgetBinding`. Remove `emitIfRefreshMethods` and `emitForRefreshMethods` (they emit the legacy refresh dispatch).

After each: `go test ./codegen/platform/gtk4/...` plus `go build ./...`. Commit each independently.

### Task 16: Drop scaffold/template updater fields

- [ ] **Step 1: Update scaffold.go**

Remove `UpdaterNames`, `AffectedUpdaters`, related fields from the template-data struct.

- [ ] **Step 2: Update template**

In `codegen/platform/gtk4/templates/model.go.tmpl`, remove the `doRefresh()` body and the `/*REACTIVE_REFRESH*/` slot.

- [ ] **Step 3: Test + commit**

```bash
go test ./codegen/platform/gtk4/...
git add codegen/platform/gtk4/scaffold.go codegen/platform/gtk4/templates/model.go.tmpl
git commit -m "gtk4: remove scaffold updater fields + reactive-refresh template slot

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase E: Verify + handoff

### Task 17: Final verify

- [ ] **Step 1: Run `go tool verify`**

```bash
go tool verify 2>&1 | tail -10
```

Capture the new failure count. Compare to baseline (33/109 at Plan A close, modified by Plan B.2's 6 cmd/sngl script-test failures). Expected: similar count or fewer.

- [ ] **Step 2: Run the integration test compile-check**

If the gtk4 integration test from Task 9 includes a `go build` check (mirror Plan B.2 Task 7), confirm it passes.

If it doesn't yet have a compile check, add one now (same pattern as Plan B.2 Task 7).

- [ ] **Step 3: Document residual issues**

For any gtk4 test that's broken because it asserts old-pipeline output formats (e.g. `m.lbl0` vs `m.__n0`), list them. These follow the same fallout pattern as Plan B.2's 6 cmd/sngl test breakages and need rewriting in a follow-up.

- [ ] **Step 4: Commit verification notes**

```bash
git add docs/superpowers/plans/2026-05-13-gtk4-intrinsic-conversion.md
git commit -m "gtk4: Plan C verification close-out notes

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 18: Spec handoff annotation

- [ ] **Step 1: Update the spec migration-order note**

In `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`, find the existing Plan A/B/B.2 annotation block. Update to include Plan C:

```
> **Plan C** (`docs/superpowers/plans/2026-05-13-gtk4-intrinsic-conversion.md`)
> applies the same conversion to gtk4: gtk4Translator implementing
> IntrinsicTranslator, NoDeclarative enabled, parallel pipeline ripped.
> Plan D covers html. Plan E is the final audit.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md
git commit -m "docs(spec): annotate Plan C scope on reactivity lowering design

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## What's NOT in Plan C

- **Entry two-way binding** (`@input(e) { name = e.value }` with synchronous setter back-write). The existing `connectSignal` had this logic at `view_ir.go:1011-1018`. Plan C drops it; if gtk4 tests show breakage on entry widgets, restore the pattern in `OnAttachHandler` as a per-event-binding special case. Same risk as Plan B.2 had with button bindings (and same workaround: `ir.Func.LoweredFromTag/LoweredFromEvent` introduced in Plan B.2's fixup commit `36b87e3`).

- **Stdlib component inlining** (`renderStdlibInline` at `view_ir.go:1086-1116`). Plan A's `passDeclarative` already inlines these via flattening. If gtk4 stdlib components depend on `propScope` substitution that doesn't flatten correctly, investigate in Task 11 and either extend passDeclarative or add a Plan C-specific shim.

- **Test invokers** (`emitEventInvokers` at `compiler_ir.go:485-507`). Test runners use these to fire signals from Go test code. They depend on `gtkEventInvoker` metadata from `connectSignal`. With the translator-based path, that metadata needs to flow from `OnAttachHandler` registrations into a side table the emitter consults for test code. Plan C: keep `emitEventInvokers`, populate the metadata from translator side-effects. Concrete shape emerges in Task 11.

- **Multi-window support**. gtk4's current emission has per-window paths (`emitWindowsCgoInit`, etc.). If Plan C's Task 11 only handles single-window cleanly, multi-window can be a follow-up.

These are explicit deferrals; raise as scope decisions if they block.
