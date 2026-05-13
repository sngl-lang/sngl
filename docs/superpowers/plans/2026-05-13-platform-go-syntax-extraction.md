# Platform Go-Syntax Extraction Implementation Plan (Plan F)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all remaining Go-syntax and Go-cgo-syntax knowledge out of fyne and gtk4 platform packages into the Go language renderer (`codegen/lang/golang`). Platforms produce IR fragments (`*ir.Func`, `*ir.Call`, `*ir.Conversion`, typed `*ir.Type`). The renderer owns: function definition syntax, cgo function-call syntax (`C.foo` prefix), cgo pointer-cast syntax (`(*C.T)(unsafe.Pointer(x))`), type rendering.

**Architecture:** Plan C revised `IntrinsicTranslator` leaf methods to return `[]ir.Stmt`, but the surrounding emitter scaffolding (Func definition emission, BuildUI body, `irBind.Init` string literals, `widgetField.CType` rendered as `*C.GtkLabel`) still bakes Go-cgo strings into platform packages. Plan F finishes the decoupling. After this plan: a future Rust frontend can reuse fyne/gtk4 translators with its own renderer; a future Go-on-Qt platform writes a `qtTranslator` producing IR and reuses the Go renderer.

**Tech Stack:** Go, cgo, the existing `*ir.Func.NativePkg`/`NativeName` mechanism, `*ir.Conversion`, `*ir.Type.Meta` for native-pointer marking.

**Predecessors:** Plans A, B, B.2, C (all on `main`). Plan C's revised IntrinsicTranslator is the foundation; Plan F finishes what C started.

**Successor:** Plan D (html conversion) and Plan E (final audit). After Plan F the Plan D translator pattern is fully clean.

---

## Architectural target

| Concern | Current (post-Plan C) | After Plan F |
|---|---|---|
| Cgo function call `C.gtk_label_new(nil)` | `nativeCall("C.gtk_label_new", nullLit)` — `NativeName = "C.gtk_label_new"` includes the `C.` prefix | `nativeCall("gtk_label_new", nullLit)` — `NativeName = "gtk_label_new"`; renderer prepends `C.` when `NativePkg == "C"` |
| Cgo pointer cast `(*C.GtkLabel)(unsafe.Pointer(x))` | `nativeCall("(*C.GtkLabel)", nativeCall("unsafe.Pointer", x))` — fake function whose Name is Go syntax | `&ir.Conversion{Type: &ir.Type{Kind: TypeNative, Meta: "GtkLabel"}, Operand: x}`; renderer expands to cgo cast pattern |
| Func definition `func (m *Model) name(params) ret { body }` | Emitter builds string directly via `fmt.Fprintf` | Platform constructs `*ir.Func`; renderer's `EmitFuncDef` produces the source lines |
| Var init `container.NewVBox()` / `(*C.GtkBox)(unsafe.Pointer(C.gtk_box_new(...)))` | `irBind.Init string` raw text | `irBind.Init ir.Expr` — renderer evaluates |
| Field type `*widget.Label` / `*C.GtkLabel` | `irBind.GoType string` | `irBind.Type *ir.Type` — renderer formats |
| `m.__root = container.NewVBox()` startup-init | Platform string-formats | `*ir.Assign` constructed in IR; renderer emits |

---

## Architectural primitives (Phase 0)

### Task 1: Type-kind for native foreign types + Conversion rendering

**Files:**
- Modify: `ir/types.go` — add `TypeNative` kind.
- Modify: `codegen/lang/golang/ircontext.go` — `IRTypeToGo` handles native; `evalConversion` recognises native-pointer cast pattern.
- Test: `codegen/lang/golang/ircontext_test.go`.

A "native" type is one defined in the target language's foreign world (cgo C types, Java types via JNI later, etc.). The platform tells the renderer "this is a foreign type called X"; the renderer formats it per its language (Go: `*C.X`; future Rust: `*mut X`).

- [ ] **Step 1: Add TypeNative**

In `ir/types.go`, add to the TypeKind iota:

```go
TypeNative // platform-provided foreign type; Meta carries the platform-specific descriptor
```

Add a constructor helper:

```go
// NativePointerOf returns a type representing a pointer to a foreign
// type named `name`. For Go-cgo: renders as "*C.<name>". The Meta
// field holds the name string so language renderers can read it.
func NativePointerOf(name string) *Type {
    return &Type{Kind: TypeNative, Meta: name}
}
```

- [ ] **Step 2: Test for IRTypeToGo**

Append to `codegen/lang/golang/ircontext_test.go`:

```go
func TestIRTypeToGo_NativePointer(t *testing.T) {
    typ := ir.NativePointerOf("GtkLabel")
    got := IRTypeToGo(typ)
    want := "*C.GtkLabel"
    if got != want {
        t.Errorf("IRTypeToGo(NativePointer GtkLabel) = %q; want %q", got, want)
    }
}
```

Run: `go test ./codegen/lang/golang/ -run TestIRTypeToGo_NativePointer -v`
Expected: FAIL (`unhandled ir.TypeKind`).

- [ ] **Step 3: Implement TypeNative in IRTypeToGo**

In `codegen/lang/golang/ircontext.go`, find the `IRTypeToGo` function. Add a case before the default panic:

```go
case ir.TypeNative:
    if name, ok := t.Meta.(string); ok && name != "" {
        return "*C." + name
    }
    return "unsafe.Pointer"
```

Run the test — PASSES.

- [ ] **Step 4: Test for Conversion rendering native pointer cast**

Append to `ircontext_test.go`:

```go
func TestEvalConversion_NativePointerCast(t *testing.T) {
    gc := newMinimalIRCtx()
    operand := &ir.Ident{Name: "raw"}
    conv := &ir.Conversion{
        Type:    ir.NativePointerOf("GtkLabel"),
        Operand: operand,
    }
    got := gc.EvalExpr(conv)
    want := "(*C.GtkLabel)(unsafe.Pointer(raw))"
    if got != want {
        t.Errorf("EvalExpr cgo cast = %q; want %q", got, want)
    }
}
```

Run: `go test ./codegen/lang/golang/ -run TestEvalConversion_NativePointerCast -v`
Expected: FAIL (current path emits `*C.GtkLabel(raw)` without the unsafe.Pointer wrapper).

- [ ] **Step 5: Extend evalConversion for native pointer types**

In `evalConversion`, after the existing null-to-func check, add:

```go
if n.Type != nil && n.Type.Kind == ir.TypeNative {
    goType := IRTypeToGo(n.Type)
    operand := gc.EvalExpr(n.Operand)
    return "(" + goType + ")(unsafe.Pointer(" + operand + "))"
}
```

Run the test — PASSES.

- [ ] **Step 6: Run full lang/golang tests**

Run: `go test ./codegen/lang/golang/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ir/types.go codegen/lang/golang/ircontext.go codegen/lang/golang/ircontext_test.go
git commit -m "ir+lang/go: add TypeNative + cgo pointer-cast Conversion rendering

ir.Type gains TypeNative kind with Meta carrying the foreign type
name. Go renderer formats as *C.<name> and renders ir.Conversion to
a TypeNative as the (*C.Type)(unsafe.Pointer(x)) cgo cast pattern.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Cgo function-call rendering — strip `C.` prefix from NativeName

**Files:**
- Modify: `codegen/lang/golang/ircontext.go` — `evalNamespaceCall` prepends `C.` when `NativePkg == "C"`.
- Test: `codegen/lang/golang/ircontext_test.go`.

Today `*ir.Func{NativePkg:"C", NativeName:"C.gtk_label_new"}` renders as `C.gtk_label_new(...)`. The platform pre-baked the `C.` prefix into NativeName — Go-syntax leak. After this task: `NativeName: "gtk_label_new"` (bare C identifier); renderer adds the `C.` prefix.

- [ ] **Step 1: Test**

Append to `ircontext_test.go`:

```go
func TestEvalCall_CgoNativePrefix(t *testing.T) {
    gc := newMinimalIRCtx()
    call := &ir.Call{
        Receiver: &ir.Ident{Name: "C"}, // synthetic namespace receiver
        Func:     &ir.Func{NativePkg: "C", NativeName: "gtk_label_new"},
        Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypNull}}},
    }
    got := gc.EvalExpr(call)
    want := "C.gtk_label_new(nil)"
    if got != want {
        t.Errorf("EvalExpr cgo call = %q; want %q", got, want)
    }
}
```

Run: `go test ./codegen/lang/golang/ -run TestEvalCall_CgoNativePrefix -v`
Expected: FAIL (current renders `gtk_label_new(nil)` because NativeName is bare).

- [ ] **Step 2: Update evalNamespaceCall**

In `codegen/lang/golang/ircontext.go`, find `evalNamespaceCall`. Current code at ~line 354:

```go
if n.Func.NativePkg != "" {
    return n.Func.NativeName + "(" + strings.Join(args, ", ") + ")"
}
```

Replace with:

```go
if n.Func.NativePkg != "" {
    name := n.Func.NativeName
    // Cgo C-API call: NativeName carries the bare C identifier
    // (e.g. "gtk_label_new"); the renderer adds the cgo-side "C."
    // prefix. Backwards-compat: if NativeName already starts with
    // "C." (legacy callers), leave untouched.
    if n.Func.NativePkg == "C" && !strings.HasPrefix(name, "C.") {
        name = "C." + name
    }
    return name + "(" + strings.Join(args, ", ") + ")"
}
```

Run the test — PASSES.

- [ ] **Step 3: Verify existing tests still pass (backwards-compat)**

Run: `go test ./codegen/lang/golang/...`
Expected: PASS — existing tests (like `TestEvalCall_NamedFunc`) that use NativePkg/NativeName with already-prefixed names still emit correctly via the `HasPrefix("C.")` guard.

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/golang/ircontext.go codegen/lang/golang/ircontext_test.go
git commit -m "lang/go: prepend C. prefix when NativePkg == 'C'

Cgo function calls now use NativeName as the bare C identifier.
Renderer adds the 'C.' prefix. Older callers that already include
'C.' in NativeName still work via the HasPrefix guard.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Func-definition emission via the Go renderer

**Files:**
- Modify: `codegen/lang/golang/ircontext.go` — add `EmitFuncDef(fn *ir.Func) []string`.
- Test: `codegen/lang/golang/ircontext_test.go`.

Today every platform builds `func (recv *T) name(params) ret { body }` headers as raw strings. Move this into `golang.GoIRContext.EmitFuncDef`. Body lines come from `EvalStmt` over `fn.Block`; signature lines come from `fn.Params`/`fn.Return`; receiver from `fn.Receiver` (existing field, today used for type-attached method dispatch).

- [ ] **Step 1: Test**

Append to `ircontext_test.go`:

```go
func TestEmitFuncDef_PlainFunc(t *testing.T) {
    gc := newMinimalIRCtx()
    fn := &ir.Func{
        Name:   "greet",
        Params: []*ir.Param{{Name: "name", Type: ir.TypString}},
        Return: ir.TypString,
        Block: []ir.Stmt{
            &ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}},
        },
    }
    got := strings.Join(gc.EmitFuncDef(fn), "\n")
    want := "func greet(name string) string {\n\treturn \"hi\"\n}"
    if got != want {
        t.Errorf("EmitFuncDef plain func mismatch:\ngot:\n%s\nwant:\n%s", got, want)
    }
}

func TestEmitFuncDef_ModelMethod(t *testing.T) {
    gc := newMinimalIRCtx()
    fn := &ir.Func{
        Name:     "Click",
        Receiver: "Model",
        Params:   nil,
        Return:   ir.TypVoid,
        Block:    []ir.Stmt{},
    }
    got := strings.Join(gc.EmitFuncDef(fn), "\n")
    want := "func (m *Model) Click() {\n}"
    if got != want {
        t.Errorf("EmitFuncDef Model method mismatch:\ngot:\n%s\nwant:\n%s", got, want)
    }
}
```

Run: `go test ./codegen/lang/golang/ -run TestEmitFuncDef -v`
Expected: FAIL (function undefined).

- [ ] **Step 2: Implement EmitFuncDef**

In `codegen/lang/golang/ircontext.go`, append:

```go
// EmitFuncDef renders a complete Go function definition from an *ir.Func.
// Includes the receiver clause (for Model methods), param list, return
// type, and body. Body statements flow through EvalStmt — Synthesized
// idents resolve to m.<name>, native funcs to C.<NativeName>, etc.
//
// Returns the source as a slice of lines (each line WITHOUT trailing
// newline). The caller joins with "\n".
func (gc *GoIRContext) EmitFuncDef(fn *ir.Func) []string {
    var lines []string

    // Signature
    params := make([]string, len(fn.Params))
    for i, p := range fn.Params {
        params[i] = p.Name + " " + IRTypeToGo(p.Type)
    }
    retType := ""
    if fn.Return != nil && fn.Return.Kind != ir.TypeVoid && fn.Return.Kind != ir.TypeDyn {
        retType = " " + IRTypeToGo(fn.Return)
    }

    sig := "func "
    if fn.Receiver != "" {
        sig += "(m *" + fn.Receiver + ") "
    }
    sig += fn.Name + "(" + strings.Join(params, ", ") + ")" + retType + " {"
    lines = append(lines, sig)

    // Body — push local scope for each param so EvalStmt resolves
    // them as plain idents rather than going through Sym-lookup.
    bodyGC := gc
    for _, p := range fn.Params {
        bodyGC = bodyGC.WithLocal(p.Name)
    }
    for _, stmt := range fn.Block {
        for _, line := range bodyGC.EvalStmt(stmt) {
            lines = append(lines, "\t"+line)
        }
    }

    lines = append(lines, "}")
    return lines
}
```

Run the tests — PASS.

- [ ] **Step 3: Full lang/golang tests**

Run: `go test ./codegen/lang/golang/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/golang/ircontext.go codegen/lang/golang/ircontext_test.go
git commit -m "lang/go: EmitFuncDef renders Go func from *ir.Func

Receiver-aware. Adds Model method support via fn.Receiver. Body
lines pushed through EvalStmt with local-scope additions for each
param. Platform emitters can now construct *ir.Func + call
EmitFuncDef instead of building func headers as raw strings.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## fyne back-port (Phase 1)

### Task 4: irBind.Init becomes ir.Expr; init values constructed via IR

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go` — `irBind` field rename + analyzeIR construction.
- Modify: `codegen/platform/fyne/scaffold.go` — template-data passing.
- Modify: `codegen/platform/fyne/templates/model.go.tmpl` — uses rendered init.

Plan B.2 introduced `irBind.Init string` as raw Go syntax. Move to typed IR.

- [ ] **Step 1: Refactor irBind**

In `codegen/platform/fyne/compiler_ir.go`, change the irBind struct:

```go
type irBind struct {
    name        string
    goType      string
    init        ir.Expr // nil → zero value rendered at template time
    noAccessors bool
}
```

(Was `init string`.)

- [ ] **Step 2: Update analyzeIR**

In analyzeIR's `if v.Synthesized` branch, construct IR for the init:

```go
if v.Name == "__root" {
    // container.NewVBox() as ir.Call
    initCall := &ir.Call{
        Type: ir.TypDyn,
        Func: &ir.Func{NativePkg: "container", NativeName: "container.NewVBox"},
    }
    info.binds = append(info.binds, irBind{
        name:        v.Name,
        goType:      "*fyne.Container",
        init:        initCall,
        noAccessors: true,
    })
    continue
}
// __slot<N> = nil
info.binds = append(info.binds, irBind{
    name:        v.Name,
    goType:      "[]fyne.CanvasObject",
    init:        &ir.Literal{Type: ir.TypNull},
    noAccessors: true,
})
```

For the non-synthesized branch, the existing `initVal := irVarInit(v, varGC)` returns a string. Change `irVarInit` to return `ir.Expr` (it likely already evaluates an IR expression internally — adapt the return).

Read `irVarInit` first. If it returns a string by composing `gc.EvalExpr(v.Init)`, change it to return `v.Init` directly (the IR expr).

- [ ] **Step 3: Render init at template time**

The template uses `{{.Init}}` to splice the init string. Change the binding data type:

In `scaffold.go`, the bindData struct:

```go
type bindData struct {
    Name        string
    GoType      string
    Init        string // rendered Go syntax at template-build time
    NoAccessors bool
}
```

In `newIRTemplateData` (or wherever bindData is built), render each irBind.init at template-data-build time:

```go
for _, b := range info.binds {
    initStr := "nil"
    if b.init != nil {
        initStr = gc.EvalExpr(b.init)
    }
    td.Binds = append(td.Binds, bindData{
        Name: b.name, GoType: b.goType, Init: initStr, NoAccessors: b.noAccessors,
    })
}
```

(Adapt to actual code shape.)

- [ ] **Step 4: Run fyne tests**

```bash
go test ./codegen/platform/fyne/...
```

Expected: PASS — emitted Go unchanged because the rendered string is the same.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/scaffold.go
git commit -m "fyne: irBind.init becomes ir.Expr; rendered via gc.EvalExpr

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: fyne Func emission uses EmitFuncDef

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go` — `emitIRFyneFunc`, `emitIRSlotFunc`, `emitIRPromotedHandler`, `renderIRComponentMethod` all construct *ir.Func and call gc.EmitFuncDef.

Current emitters string-format `func (m *Model) Name(params) ret { ... body ... }`. Replace.

- [ ] **Step 1: Rewrite emitIRFyneFunc**

Replace its body with:

```go
func emitIRFyneFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
    if len(fn.Block) == 0 {
        return
    }
    // Set Receiver to "Model" so EmitFuncDef emits the method form.
    // Preserve original (it's likely empty for user funcs we promote to methods).
    receiver := fn.Receiver
    if receiver == "" {
        receiver = "Model"
    }
    fnCopy := *fn // shallow copy to override Receiver without mutating shared
    fnCopy.Receiver = receiver

    for _, line := range gc.EmitFuncDef(&fnCopy) {
        b.WriteString(line)
        b.WriteByte('\n')
    }
    b.WriteByte('\n')
}
```

- [ ] **Step 2: Rewrite emitIRSlotFunc**

```go
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField) {
    tr := newFyneTranslator(gc, platformBlueprints(), func(name, goType string) {
        *widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
    })

    // Walk the body through the translator → IR stmts.
    bodyStmts := codegen.WalkLowered(context.Background(), fn.Block, tr)

    // Build an *ir.Func with Receiver=Model so EmitFuncDef produces
    // `func (m *Model) <name>(<param>) { <body> }`.
    synthesized := &ir.Func{
        Name:     fn.Name,
        Receiver: "Model",
        Params:   fn.Params, // slot func has one param: container *fyne.Container
        Return:   ir.TypVoid,
        Block:    bodyStmts,
    }
    for _, line := range gc.EmitFuncDef(synthesized) {
        b.WriteString(line)
        b.WriteByte('\n')
    }
    b.WriteByte('\n')
}
```

For the `container *fyne.Container` parameter type: fyne needs to express "*fyne.Container" as an *ir.Type the renderer translates. Add a fyne-specific native type:

```go
// fyneContainerType is the *ir.Type fyne uses for slot-func params.
var fyneContainerType = &ir.Type{Kind: ir.TypeNative, Meta: "fyne.Container"}
```

But wait — `ir.TypeNative + Meta` was defined to render as `*C.<name>` in Plan F Task 1. fyne wants `*fyne.Container`, not `*C.fyne.Container`. The renderer needs to distinguish "cgo C type" from "Go package type."

Option A: extend the `Meta` field to be a struct discriminating cgo vs Go-package:

```go
type NativeTypeRef struct {
    CgoC bool   // true → *C.<Name>; false → *<Name> with Go-style qualified name
    Name string // "GtkLabel" or "fyne.Container"
}
```

Adapt `IRTypeToGo`:

```go
case ir.TypeNative:
    if ref, ok := t.Meta.(NativeTypeRef); ok {
        if ref.CgoC {
            return "*C." + ref.Name
        }
        return "*" + ref.Name
    }
    if name, ok := t.Meta.(string); ok && name != "" {
        return "*C." + name  // legacy default
    }
    return "unsafe.Pointer"
```

Same in `evalConversion` — the cgo cast pattern only applies when `ref.CgoC`. For Go-package types, conversion is just `Type(x)` like normal.

- [ ] **Step 3: Backfill the NativeTypeRef refactor**

Refactor Task 1's `NativePointerOf` helper to use the new ref:

```go
func NativePointerOf(name string) *Type {
    return &Type{Kind: TypeNative, Meta: NativeTypeRef{CgoC: true, Name: name}}
}

// NativeGoPointerOf returns a pointer to a Go-package type
// (e.g. *fyne.Container, *widget.Label). Cast pattern is plain
// type-conversion (not cgo's unsafe.Pointer trick).
func NativeGoPointerOf(name string) *Type {
    return &Type{Kind: TypeNative, Meta: NativeTypeRef{CgoC: false, Name: name}}
}
```

Update Task 1's tests to match the new shape (re-run them after the helper change to confirm).

- [ ] **Step 4: Rewrite emitIRPromotedHandler**

Similar pattern — construct an *ir.Func with the translated body, call gc.EmitFuncDef. The signature/bindParam stripping logic that lives inline today moves before the WalkLowered call (it transforms the IR Block).

- [ ] **Step 5: Run fyne tests + integration test**

```bash
go test ./codegen/platform/fyne/...
```

Expected: PASS. Integration test's compile-check confirms emitted Go is identical (up to whitespace).

If output formats shift, update string snippet assertions to match canonical form.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go ir/types.go codegen/lang/golang/ircontext.go codegen/lang/golang/ircontext_test.go
git commit -m "fyne: Func emission via gc.EmitFuncDef + NativeTypeRef

All four fyne emitters (FyneFunc, SlotFunc, PromotedHandler,
ComponentMethod) construct *ir.Func and call gc.EmitFuncDef
rather than string-formatting headers. ir.NativeTypeRef
discriminates cgo C types (*C.X) from Go-package types (*pkg.X).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: BuildUI emission uses IR for top-level appends

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`.

Currently single-window BuildUI emits `m.__root.Add(m.__nN)` strings inline. Refactor: collect top-level refs, construct ir.CallStmts for the appends, render via gc.EvalStmt.

- [ ] **Step 1: Restructure single-window emission**

Replace the inline `fmt.Fprintf(&buildBuf, "...m.__root.Add(m.%s)\n", ref)` with:

```go
// Build IR for top-level appends.
rootRef := &ir.Ident{Name: "__root", IsElementRef: true, Synthesized: true}
for _, ref := range tr.topLevel {
    childRef := &ir.Ident{Name: ref, IsElementRef: true, Synthesized: true}
    appendCall := &ir.Call{
        Type:     ir.TypVoid,
        Receiver: rootRef,
        Func:     &ir.Func{Name: "Add"},
        Args:     []ir.CallArg{{Value: childRef}},
    }
    stmt := &ir.CallStmt{Call: appendCall}
    for _, line := range gc.EvalStmt(stmt) {
        fmt.Fprintf(&buildBuf, "\t%s\n", line)
    }
}
```

The `Synthesized` flag makes both refs render as `m.__root`/`m.__nN`. gc.EvalExpr for method-style Call (Receiver + Func.Name) emits `m.__root.Add(m.__nN)` — same output.

- [ ] **Step 2: Test + commit**

```bash
go test ./codegen/platform/fyne/...
git add codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: BuildUI top-level appends emitted via IR + gc.EvalStmt

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## gtk4 cleanup (Phase 2)

### Task 7: Rebase gtk4 cgo calls — drop pre-rendered C. prefix

**Files:**
- Modify: `codegen/platform/gtk4/intrinsic_translator.go` — `nativeCall` shorthand uses bare names; `cgoCast` uses ir.Conversion.

The cleanest gtk4 emission: `*ir.Call` with `NativePkg: "C"` and `NativeName: "gtk_label_new"` (bare). Renderer adds `C.` (per Task 2). Pointer casts use `*ir.Conversion` with `Type: NativePointerOf("GtkLabel")` (per Task 1's NativeTypeRef).

- [ ] **Step 1: Strip "C." from all NativeName literals**

In `intrinsic_translator.go`, find every `nativeCall("C.<name>", ...)` and change to `nativeCall("<name>", ...)`. Examples:

- `nativeCall("C.gtk_label_new", ...)` → `nativeCall("gtk_label_new", ...)`
- `nativeCall("C.gtk_box_append", ...)` → `nativeCall("gtk_box_append", ...)`
- etc.

In the `nativeFunc` helper, the constructor:

```go
func nativeFunc(nativeName string) *ir.Func {
    // NativeName is the bare C identifier; renderer prepends "C." via
    // NativePkg dispatch.
    return &ir.Func{NativePkg: "C", NativeName: nativeName}
}
```

- [ ] **Step 2: Replace cgoCast with ir.Conversion**

Replace:

```go
func cgoCast(typeName string, expr ir.Expr) ir.Expr {
    unsafePtr := nativeCall("unsafe.Pointer", expr)
    return nativeCall("(*C."+typeName+")", unsafePtr)
}
```

With:

```go
// cgoCast wraps an expression in a cgo pointer cast:
//   (*C.<typeName>)(unsafe.Pointer(expr))
// Implemented as an ir.Conversion whose target type is a Native
// pointer; the Go renderer (lang/golang) recognises the shape and
// emits the cgo cast pattern.
func cgoCast(typeName string, expr ir.Expr) ir.Expr {
    return &ir.Conversion{
        Type:    ir.NativePointerOf(typeName),
        Operand: expr,
    }
}
```

- [ ] **Step 3: Strip Go-cgo bits from the GtkApplicationWindow constructor**

Find the line:

```go
ctor = nativeCall("C.gtk_application_window_new", &ir.Ident{Name: "app", Type: ir.TypDyn})
```

Change to:

```go
ctor = nativeCall("gtk_application_window_new", &ir.Ident{Name: "app", Type: ir.TypDyn})
```

- [ ] **Step 4: Run all gtk4 + integration tests**

```bash
go test ./codegen/platform/gtk4/...
```

Expected: PASS. Emitted source unchanged (renderer now adds C. prefix; cgo cast emits same pattern from Conversion).

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/intrinsic_translator.go
git commit -m "gtk4: drop pre-rendered C. prefix; cgoCast uses ir.Conversion

NativeName carries bare C identifiers; the Go renderer adds 'C.'.
Pointer casts emit via ir.Conversion to a NativePointer type —
renderer formats as (*C.X)(unsafe.Pointer(y)).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: gtk4 Func emission uses EmitFuncDef

**Files:**
- Modify: `codegen/platform/gtk4/compiler_ir.go` — `emitIRSlotFunc`, `emitIRPromotedHandler`, `emitGTK4Func`, and BuildUI body.

Mirror Task 5's fyne refactor for gtk4. The slot-func param `container *C.GtkBox` becomes `&ir.Param{Name: "container", Type: ir.NativePointerOf("GtkBox")}`. The renderer emits `*C.GtkBox` via IRTypeToGo's TypeNative branch.

- [ ] **Step 1: emitIRSlotFunc rewrite**

```go
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package) {
    tr := newGtk4Translator(gc, pkg, func(name, cType string) {
        *widgetFields = append(*widgetFields, widgetField{Name: name, CType: cType})
    })
    bodyStmts := codegen.WalkLowered(context.Background(), fn.Block, tr)

    synthesized := &ir.Func{
        Name:     fn.Name,
        Receiver: "Model",
        Params: []*ir.Param{
            {Name: "container", Type: ir.NativePointerOf("GtkBox")},
        },
        Return: ir.TypVoid,
        Block:  bodyStmts,
    }
    for _, line := range gc.EmitFuncDef(synthesized) {
        b.WriteString(line)
        b.WriteByte('\n')
    }
    b.WriteByte('\n')
}
```

- [ ] **Step 2: emitIRPromotedHandler rewrite**

Same pattern: build *ir.Func with proper Receiver/Params/Return; call EmitFuncDef. The bind-param strip and getter substitution logic transforms fn.Block BEFORE constructing the synthesized Func.

The current getter string `C.GoString(C.gtk_editable_get_text((*C.GtkEditable)(unsafe.Pointer(m.X))))` becomes an IR Call tree:

```go
// C.GoString(C.gtk_editable_get_text((*C.GtkEditable)(unsafe.Pointer(m.<id>))))
widgetRef := &ir.Ident{Name: nodeID, IsElementRef: true, Synthesized: true}
cast := &ir.Conversion{Type: ir.NativePointerOf("GtkEditable"), Operand: widgetRef}
getText := &ir.Call{
    Type: ir.TypDyn,
    Func: nativeFunc("gtk_editable_get_text"),
    Args: []ir.CallArg{{Value: cast}},
}
goString := &ir.Call{
    Type: ir.TypString,
    Func: nativeFunc("GoString"),
    Args: []ir.CallArg{{Value: getText}},
}
```

(`nativeFunc("GoString")` uses `NativePkg:"C", NativeName:"GoString"` — renderer emits `C.GoString(...)`.)

This is the bindParam-replacement value used in `m.<var> = <getter>` — construct as ir.Assign.

- [ ] **Step 3: emitGTK4Func rewrite — use EmitFuncDef**

```go
func emitGTK4Func(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
    if len(fn.Block) == 0 {
        return
    }
    fnCopy := *fn
    if fnCopy.Receiver == "" {
        fnCopy.Receiver = "Model"
    }
    for _, line := range gc.EmitFuncDef(&fnCopy) {
        b.WriteString(line)
        b.WriteByte('\n')
    }
    b.WriteByte('\n')
}
```

- [ ] **Step 4: Run gtk4 tests + integration**

```bash
go test ./codegen/platform/gtk4/...
```

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/compiler_ir.go
git commit -m "gtk4: Func emission via gc.EmitFuncDef + IR-based getters

Slot funcs, promoted handlers, user funcs all construct *ir.Func and
call gc.EmitFuncDef. Bind-param getter expressions (e.g. for entry
input) are IR Call trees with cgo Conversions; renderer emits the
cgo cast + GoString pattern.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: gtk4 bindings/widgetFields use ir.Type

**Files:**
- Modify: `codegen/platform/gtk4/compiler_ir.go` — `widgetField`/`irBind` types.
- Modify: `codegen/platform/gtk4/scaffold.go` — template-data renders types via IRTypeToGo.

Today `widgetField.CType` is a bare string ("GtkLabel") that the template formats as `*C.GtkLabel`. Move the formatting into the renderer.

- [ ] **Step 1: Change widgetField shape**

```go
type widgetField struct {
    Name string
    Type *ir.Type // typed; renderer formats per language
}
```

When fyneTranslator/gtk4Translator's fieldSink is called, pass `ir.NativePointerOf(cType)` instead of the bare string.

- [ ] **Step 2: scaffold rendering**

In scaffold.go, when building template-data bindData, render the type:

```go
Type: golang.IRTypeToGo(w.Type),
```

- [ ] **Step 3: Same for irBind.GoType**

Change `goType string` → `Type *ir.Type`. Render at template-build time.

- [ ] **Step 4: Test + commit**

```bash
go test ./codegen/platform/...
git add codegen/platform/gtk4/
git commit -m "gtk4: widgetField/irBind types are *ir.Type; renderer formats

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

(fyne gets the same treatment in this commit or a sibling — Task 5 should already have moved fyne's irBind.GoType; verify and align.)

---

### Task 10: gtk4 BuildUI body via IR for the gtk_box_append top-level fan-out

Mirror Task 6: top-level widgets append to `m.__root` as ir.CallStmt to `C.gtk_box_append`. Construct the IR call; render via gc.EvalStmt.

- [ ] **Step 1: Replace the inline fmt.Fprintf**

```go
rootRef := &ir.Ident{Name: "__root", IsElementRef: true, Synthesized: true}
for _, ref := range tr.topLevel {
    childRef := &ir.Ident{Name: ref, IsElementRef: true, Synthesized: true}
    appendCall := &ir.Call{
        Type:     ir.TypVoid,
        Func:     nativeFunc("gtk_box_append"),
        Args: []ir.CallArg{
            {Value: &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: rootRef}},
            {Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: childRef}},
        },
    }
    stmt := &ir.CallStmt{Call: appendCall}
    for _, line := range gc.EvalStmt(stmt) {
        fmt.Fprintf(&buildBuf, "\t%s\n", line)
    }
}
```

(`nativeFunc` in gtk4 returns `&ir.Func{NativePkg:"C", NativeName:"gtk_box_append"}`; renderer adds `C.` prefix per Task 2.)

- [ ] **Step 2: Same for the window-class passthrough branch**

The `C.gtk_window_set_child` call → construct as IR. Same shape.

- [ ] **Step 3: Test + commit**

```bash
go test ./codegen/platform/gtk4/...
git add codegen/platform/gtk4/compiler_ir.go
git commit -m "gtk4: BuildUI fan-out emitted via IR

Top-level widget appends + window-set-child no longer use raw
fmt.Fprintf with cgo syntax; constructed as ir.Call with
NativePkg='C' and ir.Conversion casts. Renderer emits the final
source.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Verify (Phase 3)

### Task 11: Audit — grep for remaining Go syntax in platform packages

```bash
grep -nE 'fmt\.Fprintf.*"func \(|fmt\.Fprintf.*"\*C\.|"\(\*C\." ' codegen/platform/fyne/ codegen/platform/gtk4/ | grep -v _test.go
```

Expected: empty or only template-string occurrences in non-emitter helpers. Each remaining hit is a regression.

If anything remains, fix it.

### Task 12: Reproduce the user's run command + confirm

```bash
go install ./cmd/sngl
go tool sngl run examples/hello-i18n --platform fyne --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"
go tool sngl run examples/hello-i18n --platform gtk4 --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"
```

Expected: both compile and run. (Need GTK4 headers installed for gtk4 path; if missing, just confirm `go build` succeeds.)

### Task 13: Final verify + commit

```bash
go tool verify 2>&1 | grep -E "^---|not ok" | head -10
```

Compare to pre-Plan-F baseline.

### Task 14: Spec handoff annotation

Update `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` to note Plan F.

---

## What's NOT in Plan F

- **Plan D (html)** — held until Plan F lands. html translator will be built directly on the cleaned interface.
- **Multi-platform Rust/C/Python frontend bring-up** — Plan F enables this but doesn't deliver any specific frontend.
- **Memory management for C.CString** — gtk4 today leaks. Out of scope; tracked separately.
