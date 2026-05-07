# GTK Platform for SNGL — Design Spec

**Goal:** Add a `gtk` platform that compiles SNGL to GTK4 desktop apps via Go+cgo, exercising the `c://` import scheme end-to-end.

**Date:** 2026-05-06

---

## Overview

A new `codegen/platform/gtk/` package registers a `PlatformGenerator` that compiles SNGL to GTK4 Go+cgo code. Widget types are synthesized at checker time by parsing the GTK4 GIR file (`Gtk-4.0.gir`); `gtk.sngl` defines stdlib SNGL components in terms of those GIR-resolved symbols — the same pattern HTML uses with raw tag names. The platform injects a `c://pkg:gtk4` import implicitly; generated files carry a `#include <gtk/gtk.h>` cgo preamble.

---

## Architecture

```
gtk.sngl stdlib           ← SNGL components (button, label, vbox…) built on GIR symbols
        │
GIR resolver              ← parses Gtk-4.0.gir at compiler init; Resolve("GtkButton") → ir.Component
        │
c:// implicit import      ← codegen emits #include <gtk/gtk.h> via pkg-config gtk4; all calls are C.gtk_*
```

**New package `codegen/platform/gtk/`:**

| File | Responsibility |
|------|---------------|
| `gtk.go` | `Generator`: `PlatformIdentifier()="gtk"`, `Resolve()`, `SupportedLangs:["go"]`, `init()` registration |
| `gir/gir.go` | GIR XML parser → `TypeRegistry` (class → props, signals, C constructor name) |
| `gir/gir_test.go` | Unit tests for GIR parsing against inline XML fixture |
| `gtk.sngl` | Stdlib component declarations using GIR-resolved types |
| `compiler_ir.go` | MutationModel emitter (parallel to `codegen/platform/fyne/compiler_ir.go`) |
| `scaffold.go` | `templateData` struct + assembly |
| `templates/model.go.tmpl` | State struct, `BuildUI`, updaters, `gtkPost` helper |
| `templates/main.go.tmpl` | Entrypoint: `runtime.LockOSThread` + `gtk_application_run` |
| `gtk_test.go` | Unit tests for `Resolve` |

`codegen/platform/platforms.go` gains a blank import for `gtk`.

---

## GIR Resolver

GTK4 ships GIR files as part of its dev package (e.g. `libgtk-4-dev` on Debian, `gtk4-devel` on Fedora). The resolver reads `/usr/share/gir-1.0/Gtk-4.0.gir` once at compiler init and builds a `TypeRegistry`.

### GIR XML shape (excerpt)

```xml
<class name="Button" c:type="GtkButton" ...>
  <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
    <parameters><parameter name="label" .../></parameters>
  </constructor>
  <property name="label" writable="1"><type name="utf8" c:type="gchar*"/></property>
  <glib:signal name="clicked"><return-value .../></glib:signal>
</class>
```

### TypeRegistry

```go
// gir/gir.go
type Prop struct {
    Name    string
    IRType  *ir.Type
}

type Signal struct {
    Name       string
    ParamTypes []*ir.Type // callback parameter types
}

type ClassInfo struct {
    CType       string   // "GtkButton"
    Constructor string   // "gtk_button_new_with_label"
    Props       []Prop
    Signals     []Signal
}

type TypeRegistry struct {
    Classes map[string]*ClassInfo // keyed by GIR class name, e.g. "Button"
}

func ParseGIR(path string) (*TypeRegistry, error)
func ParseGIRBytes(data []byte) (*TypeRegistry, error) // for tests
```

### GIR type mapping

| GIR type | SNGL `ir.Type` |
|----------|---------------|
| `utf8`, `gchararray` | `TypeString` |
| `gboolean` | `TypeBool` |
| `gint`, `gint32`, `gint64`, `guint`, `guint32` | `TypeInt` |
| `gdouble`, `gfloat` | `TypeFloat` |
| object/class reference | `TypeDyn{Meta: "unsafe.Pointer"}` |
| unmappable | `TypeDyn{Meta: "unsafe.Pointer"}` + checker warning |

### `Resolve(identifier string) ir.Symbol`

`Generator.Resolve` is called by the checker when it encounters an unknown identifier in platform context. For `"GtkButton"`:

```go
func (g *Generator) Resolve(identifier string) ir.Symbol {
    info, ok := g.registry.Classes[stripGtk(identifier)]
    if !ok {
        return nil
    }
    return girClassToComponent(info)
}
```

Returns `*ir.Component` with `Props` from GIR properties and `Events` (signal params) from GIR signals. Returns `nil` for unknown identifiers — the checker reports a normal "unknown identifier" error.

If the GIR file is missing, `init()` stores the error in `Generator.girErr`. `Generate()` returns that error immediately. `Resolve()` returns `nil` (unknown identifier), causing a checker error that surfaces the original GIR message:
> `"gtk4 GIR file not found — install libgtk-4-dev (or gtk4-devel)"`

---

## `gtk.sngl` Stdlib

Defines SNGL components in terms of GIR-resolved symbols. Same pattern as `html.sngl` using raw HTML tag names.

```
component button(label string, onClick () => void) {
    GtkButton(label=label, clicked=onClick) {}
}

component label(text string) {
    GtkLabel(label=text) {}
}

component entry(value string, onChange (string) => void) {
    GtkEntry(text=value, changed=onChange) {}
}

component vbox(spacing int = 6) {
    GtkBox(orientation=GTK_ORIENTATION_VERTICAL, spacing=spacing) {
        children...
    }
}

component hbox(spacing int = 6) {
    GtkBox(orientation=GTK_ORIENTATION_HORIZONTAL, spacing=spacing) {
        children...
    }
}

component checkbox(label string, checked bool, onChange (bool) => void) {
    GtkCheckButton(label=label, active=checked, toggled=onChange) {}
}

component image(src string) {
    GtkImage(file=src) {}
}

component scroll() {
    GtkScrolledWindow() { children... }
}

component window(title string, width int = 800, height int = 600) {
    GtkApplicationWindow(title=title, defaultWidth=width, defaultHeight=height) {
        children...
    }
}
```

---

## Code Generation

### MutationModel

Same approach as Fyne: `BuildMutationModel` + `EmitFromMutation`. The `compilation` struct holds per-request state:

```go
type compilation struct {
    ctx      *codegen.CodegenCtx
    info     *irAnalysis
    cfg      Config
    lang     codegen.LangTranslator
}
```

`Config` supports `package string` (default `"main"`) and `main bool` (default `true` — emit `main.go` with entrypoint).

### `model.go` template

The cgo preamble is injected via `CCompiler.EmitCHeader` (same as Fyne). The platform synthesizes a single implicit `ir.NativeImport` with `ImportPath:"c://pkg:gtk4"` and `LinkFlags` from `pkg-config --libs gtk4`. This import is passed to `EmitCHeader` rather than requiring user code to declare it.

```go
// model.go (generated)
package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>
*/
import "C"

import (
    "runtime"
    "unsafe"
)

type Model struct {
    window  *C.GtkApplicationWindow
    btnOk   *C.GtkButton
    lblName *C.GtkLabel
    // ... bound state fields ...
    count   int
}

func New() *Model { return &Model{...} }

func (m *Model) BuildUI(app *C.GtkApplication) {
    win := C.gtk_application_window_new(app)
    m.window = (*C.GtkApplicationWindow)(unsafe.Pointer(win))
    // ... widget construction + signal connections ...
    C.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(m.window)), topWidget)
}

func gtkPost(fn func()) {
    f := fn
    C.sngl_gtk_idle_add(unsafe.Pointer(&f))
}

func (m *Model) updateLblName() {
    C.gtk_label_set_text(m.lblName, C.CString(m.name))
}
```

A small C helper `sngl_gtk_idle_add` is emitted inline in the cgo preamble, using `g_idle_add` with a Go trampoline exported as `snglIdleCallback`:

```c
extern void snglIdleCallback(void* fn);
static gboolean sngl_idle_trampoline(gpointer data) {
    snglIdleCallback(data);
    return G_SOURCE_REMOVE;
}
static void sngl_gtk_idle_add(void* fn) {
    g_idle_add(sngl_idle_trampoline, fn);
}
```

The corresponding Go export:
```go
//export snglIdleCallback
func snglIdleCallback(ptr unsafe.Pointer) {
    fn := *(*func())(ptr)
    fn()
}
```

### `main.go` template

Emitted when `Options{main:true}` (the default for top-level compilation):

```go
//export snglActivate
func snglActivate(app *C.GtkApplication, _ C.gpointer) {
    m := New()
    m.BuildUI(app)
    C.gtk_widget_show((*C.GtkWidget)(unsafe.Pointer(m.window)))
}

func main() {
    runtime.LockOSThread()
    app := C.gtk_application_new(C.CString("com.example.app"),
        C.G_APPLICATION_DEFAULT_FLAGS)
    C.g_signal_connect_data(app, C.CString("activate"),
        C.GCallback(C.snglActivate), nil, nil, 0)
    C.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil)
}
```

`//export` callbacks are placed in `callbacks.go` (separate file — cgo requirement).

---

## Error Handling

| Condition | Behavior |
|-----------|----------|
| GIR file missing at compile time | Hard error: `"gtk4 GIR file not found — install libgtk-4-dev (or gtk4-devel)"` |
| GIR class unknown via `Resolve` | Returns `nil` → checker reports normal unknown-identifier error |
| GIR property type unmappable | `TypeDyn{Meta:"unsafe.Pointer"}` + checker warning |
| Non-go lang requested | Hard error: `"gtk: only lang=go is supported"` |
| `CCompiler` not implemented by lang | Hard error: `"platform gtk with lang %T does not support C imports"` |

---

## Testing

**`codegen/platform/gtk/gir/gir_test.go`** — unit tests against inline GIR XML:
- `GtkButton` resolves with `label` prop (TypeString) and `clicked` signal
- `GtkLabel` resolves with `label` prop
- Unknown class returns nil
- Unmappable GIR type falls back to `TypeDyn`

**`codegen/platform/gtk/gtk_test.go`** — `Resolve` integration:
- `Resolve("GtkButton")` returns `*ir.Component` with expected props/events
- `Resolve("UnknownWidget")` returns nil

**`cmd/sngl/testdata/compile_gtk_button.txt`** — txtar end-to-end:
- `.sngl` file with a `button` + `label`, bound state
- Asserts: `C.gtk_button_new_with_label(` in output, `import "C"` present, `snglActivate` in `main.go`

---

## Out of Scope

- Non-Go language support (GTK+cgo is Go-only for now)
- Windows (GTK on Windows requires MSYS2; document as Linux/macOS only)
- CSS theming / `GtkCssProvider`
- `GtkListView` / `GtkColumnView` (complex model-based widgets; future work)
- Drag-and-drop, accessibility APIs
- GIR-derived method calls beyond constructor + signals (future: reactive property setters)
