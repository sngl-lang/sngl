package gtk4

import (
	"fmt"
	"regexp"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"

	"git.duckfam.us/jonathan/sngl/ir"
)

// This file implements "wrapped mode" gtk4 codegen: instead of emitting inline
// cgo (`C.gtk_*` calls behind an `import "C"` preamble in every generated
// program), it emits calls to the shared pkg/go/gtk4rt runtime over an opaque
// gtk4rt.Handle. The GTK cgo bindings then compile once (in gtk4rt) and Go's
// build cache reuses them, instead of recompiling the gtk.h preamble per
// program.
//
// Wrapped mode covers only the bounded stdlib widget surface. The translator
// and scaffolding emit wrapped calls for the mapped subset and fall through to
// the legacy cgo emission for anything else; emitIR then scans the output and,
// if any residual `C.` remains, discards the wrapped attempt and re-emits in
// cgo mode. So an unmapped widget cleanly degrades a whole program to the
// legacy path rather than producing half-wrapped (uncompilable) code.

// gtk4rtPkg is the import path of the shared GTK4 runtime. Emitting an
// ir.Func with this Foreign.Path auto-registers the import via the golang IR
// context (see evalNamespaceCall).
const gtk4rtPkg = "git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"

// gtk4rtHandleType is the Go type wrapped-mode code uses for every widget
// field/local, replacing the per-widget *C.GtkX pointer types.
const gtk4rtHandleType = "gtk4rt.Handle"

// rtCall builds a `gtk4rt.<name>(args...)` call. The non-"C" Foreign.Path makes
// evalNamespaceCall register the gtk4rt import and emit the Foreign.Name verbatim.
func rtCall(name string, args ...ir.Expr) *ir.Call {
	ca := make([]ir.CallArg, len(args))
	for i, a := range args {
		ca[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "gtk4rt"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: gtk4rtPkg, Name: "gtk4rt." + name}},
		Args:     ca,
	}
}

// rtConst references an exported gtk4rt identifier (e.g. a constant). The
// gtk4rt import is registered by the rtCall(s) the same file always emits.
func rtConst(name string) *ir.Ident {
	return &ir.Ident{Name: "gtk4rt." + name, Type: ir.TypDyn}
}

func intLit(v string) *ir.Literal { return &ir.Literal{Type: ir.TypInt, Value: v} }

// gtk4rtHandleIRType is an ir.Type that renders as the bare gtk4rt.Handle.
func gtk4rtHandleIRType() *ir.Type { return ir.NativeGoNamed(gtk4rtHandleType) }

// slotParentType is the IR type of a __renderSlot's `parent` param:
// gtk4rt.Handle in wrapped mode, else *C.GtkBox.
func slotParentType(wrapped bool) *ir.Type {
	if wrapped {
		return gtk4rtHandleIRType()
	}
	return ir.NativePointerOf("GtkBox")
}

// widgetFieldGoType returns the Go type for a Model widget field: gtk4rt.Handle
// in wrapped mode, else the per-widget cgo pointer type.
func widgetFieldGoType(cType string, wrapped bool) string {
	// A component instance's handle is a generated Go struct, not a GTK class,
	// so it arrives already spelled as Go and is not a widget in either mode.
	if strings.HasPrefix(cType, "*") {
		return cType
	}
	if wrapped {
		return gtk4rtHandleType
	}
	return "*C." + cType
}

// cgoRefRE matches a residual cgo reference: a `C.`-qualified identifier (e.g.
// `C.gtk_box_new`, `*C.GtkWidget`) or any of the inline-cgo helper symbols that
// only exist in the legacy callbacks.go (gtkPost, snglCallbacks, ...). Any of
// these in a wrapped-mode emission means the wrapping didn't fully cover the
// program, so it must fall back to the inline-cgo path.
var cgoRefRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])(C\.[A-Za-z_]|gtkPost|snglCallbacks|snglDrawFuncs|snglGoDispatch|sngl_[a-z])`)

// bytesUseCgo reports whether src still depends on the inline-cgo machinery.
func bytesUseCgo(src []byte) bool { return cgoRefRE.Match(src) }

// rtCtorForCType returns the gtk4rt constructor call for a freshly created
// widget of the given C type, and ok=false if the widget is outside the
// wrapped surface. Widgets take typed-zero initial args here; any user-set
// props are applied afterwards by OnPropAssign, mirroring the cgo path.
func rtCtorForCType(cType string) (ir.Expr, bool) {
	switch cType {
	case "GtkBox":
		return rtCall("BoxNew", rtConst("OrientationHorizontal"), intLit("0")), true
	case "GtkLabel":
		return rtCall("LabelNew", &ir.Literal{Type: ir.TypString, Value: ""}), true
	case "GtkButton":
		return rtCall("ButtonNew"), true
	case "GtkCheckButton":
		return rtCall("CheckButtonNew"), true
	case "GtkEntry":
		return rtCall("EntryNew"), true
	case "GtkImage":
		return rtCall("ImageNew"), true
	case "GtkScrolledWindow":
		return rtCall("ScrolledWindowNew"), true
	case "GtkFrame":
		return rtCall("FrameNew"), true
	}
	return nil, false
}

// rtSetterKind classifies how a C setter's value argument is adapted for the
// wrapped API (which takes Go-native types rather than cgo-coerced ones).
type rtSetterKind int

const (
	rtStr rtSetterKind = iota
	rtBool
	rtInt
	rtOrient
)

// rtSetterTable maps a resolved GTK setter name to its gtk4rt wrapper and the
// value-argument kind. Setters absent here trigger fallback.
var rtSetterTable = map[string]struct {
	fn   string
	kind rtSetterKind
}{
	"gtk_label_set_text":             {"LabelSetText", rtStr},
	"gtk_button_set_label":           {"ButtonSetLabel", rtStr},
	"gtk_check_button_set_label":     {"CheckButtonSetLabel", rtStr},
	"gtk_check_button_set_active":    {"CheckButtonSetActive", rtBool},
	"gtk_editable_set_text":          {"EditableSetText", rtStr},
	"gtk_image_set_from_file":        {"ImageSetFromFile", rtStr},
	"gtk_box_set_spacing":            {"BoxSetSpacing", rtInt},
	"gtk_orientable_set_orientation": {"OrientableSetOrientation", rtOrient},
}

// rtSetterValue adapts an OnPropAssign value expression to the wrapped setter's
// Go-native argument. String/bool/int values pass through unchanged (the golang
// IR context renders them as Go string/bool/int); orientation maps a string
// literal to the gtk4rt constant. ok=false → not adaptable, fall back.
func rtSetterValue(kind rtSetterKind, value ir.Expr) (ir.Expr, bool) {
	switch kind {
	case rtStr, rtBool, rtInt:
		return value, true
	case rtOrient:
		if lit, ok := value.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
			return rtOrientationConst(lit.Value)
		}
	}
	return nil, false
}

// rtChildAppendCall returns the gtk4rt container call for adding child to a
// parent of the given C type, and ok=false if unmapped.
// rtChildAppendWrappers are the gtk4rt wrappers that add a child, keyed by the
// GTK function each one calls. The key comes from the introspection data
// (gir.ChildAdder), so this says only which of those calls this runtime package
// wraps -- a fact about pkg/go/gtk4rt, not about GTK. A C type switch said both
// at once, and said the second one wrong: it knew three parents where the cgo
// path knew seven, so a frame or a popover in wrapped mode fell through to raw
// cgo in a file that compiles without cgo.
var rtChildAppendWrappers = map[string]string{
	"gtk_box_append":                "BoxAppend",
	"gtk_window_set_child":          "WindowSetChild",
	"gtk_scrolled_window_set_child": "ScrolledWindowSetChild",
	"gtk_frame_set_child":           "FrameSetChild",
}

func rtChildAppendCall(adder gir.ChildAdder, parent, child ir.Expr) (ir.Stmt, bool) {
	fn, ok := rtChildAppendWrappers[adder.Func]
	if !ok {
		return nil, false
	}
	return &ir.CallStmt{Call: rtCall(fn, parent, child)}, true
}

// rtChildRemoveCall returns the gtk4rt call for removing child from a parent of
// the given C type, and ok=false if unmapped.
func rtChildRemoveCall(parentCType string, parent, child ir.Expr) (ir.Stmt, bool) {
	if parentCType == "GtkBox" {
		return &ir.CallStmt{Call: rtCall("BoxRemove", parent, child)}, true
	}
	return nil, false
}

// rtEventGetterExpr is the wrapped-mode analogue of gtk4EventGetterExpr: it
// reads a widget's bound value through gtk4rt rather than cgo. Returns nil when
// the widget's read-back isn't wrapped (→ fallback).
func rtEventGetterExpr(cType string, widgetRef ir.Expr) ir.Expr {
	switch cType {
	case "GtkEntry":
		return rtCall("EditableGetText", widgetRef)
	case "GtkCheckButton":
		return rtCall("CheckButtonGetActive", widgetRef)
	}
	return nil
}

// rtOrientationConst maps a SNGL orientation string literal to the gtk4rt
// constant ident, and ok=false for anything unrecognised.
func rtOrientationConst(value string) (ir.Expr, bool) {
	switch value {
	case "vertical":
		return rtConst("OrientationVertical"), true
	case "horizontal":
		return rtConst("OrientationHorizontal"), true
	}
	return nil, false
}

// emitBuildUIWrapped is the wrapped-mode analogue of emitBuildUI: it emits the
// buildWidgetTree + BuildUI scaffolding using gtk4rt over gtk4rt.Handle. The
// widget-tree bodies were already emitted in wrapped mode by the translator,
// so only the surrounding scaffolding is produced here.
func emitBuildUIWrapped(b *strings.Builder, trees []windowTree, entry int, gc *golang.GoIRContext, fields map[string]bool) {
	// A ref the translator did not put in the Model is a local in
	// buildWidgetTree; see buildRef, which answers the same question for the
	// inline-cgo path.
	mref := func(ref string) string {
		if !fields[ref] {
			return ref
		}
		return "m." + ref
	}

	// Empty component: BuildUI just creates a window.
	if len(trees) == 1 && trees[0].empty() {
		b.WriteString("func (m *Model) buildWidgetTree() {}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
		b.WriteString("\treturn gtk4rt.ApplicationWindowNew(app)\n")
		b.WriteString("}\n\n")
		return
	}
	if len(trees) == 1 && trees[0].passthrough() {
		b.WriteString("func (m *Model) buildWidgetTree() {\n")
		b.WriteString(trees[0].build.String())
		b.WriteString("}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
		b.WriteString("\tm.buildWidgetTree()\n")
		fmt.Fprintf(b, "\treturn %s\n", mref(trees[0].tops[0]))
		b.WriteString("}\n\n")
		return
	}
	b.WriteString("func (m *Model) buildWidgetTree() {\n")
	fmt.Fprintf(b, "\tif m.%s != nil {\n\t\treturn\n\t}\n", trees[0].root)
	for _, t := range trees {
		fmt.Fprintf(b, "\tm.%s = gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)\n", t.root)
		b.WriteString(t.build.String())
		for _, ref := range t.tops {
			fmt.Fprintf(b, "\tgtk4rt.BoxAppend(m.%s, %s)\n", t.root, mref(ref))
		}
	}
	b.WriteString("}\n\n")
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	for _, i := range otherWindowsFirst(len(trees), entry) {
		t := trees[i]
		name := windowVar(i, entry)
		fmt.Fprintf(b, "\t%s := gtk4rt.ApplicationWindowNew(app)\n", name)
		fmt.Fprintf(b, "\tgtk4rt.WindowSetDefaultSize(%s, 480, 640)\n", name)
		if title := windowTitleGo(t.win, gc); title != "" {
			fmt.Fprintf(b, "\tgtk4rt.WindowSetTitle(%s, %s)\n", name, title)
		}
		fmt.Fprintf(b, "\tgtk4rt.WindowSetChild(%s, m.%s)\n", name, t.root)
		if i != entry {
			fmt.Fprintf(b, "\tgtk4rt.WindowPresent(%s)\n", name)
		}
	}
	b.WriteString("\treturn win\n")
	b.WriteString("}\n\n")
}

// agentMainBytes returns the agent-mode agent_main.go. In wrapped mode it is
// cgo-free (gtk_init → gtk4rt.Init); otherwise it returns the caller's cgo
// source unchanged.
func agentMainBytes(pkg string, wrapped bool, cgoSrc []byte) []byte {
	if !wrapped {
		return cgoSrc
	}
	return []byte(`package ` + pkg + `

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

var gtkInit sync.Once

var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }

func newTestComponent() *Model {
	gtkInit.Do(func() { gtk4rt.Init() })
	m := New()
	m.buildWidgetTree()
	return m
}

func main() { testagent.Main() }
`)
}

// agentSnapshotBytes returns the agent-mode snapshot.go. In wrapped mode the
// snapshot goes through gtk4rt (cgo-free); otherwise the caller's cgo source.
func agentSnapshotBytes(pkg string, wrapped bool, cgoSrc []byte) []byte {
	if !wrapped {
		return cgoSrc
	}
	return []byte(`package ` + pkg + `

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

func currentTestModel() *Model { return currentModel }

func snapshotBytesGtk(m *Model) (string, []byte, error) {
	if m == nil {
		return "", nil, fmt.Errorf("no current model registered")
	}
	data, err := gtk4rt.SnapshotModelBytes(func(app gtk4rt.Handle) gtk4rt.Handle {
		return m.BuildUI(app)
	}, 800, 600)
	if err != nil {
		return "", nil, err
	}
	return "image/png", data, nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytesGtk(currentTestModel())
	})
}
`)
}

// emitGTK4MainWrapped emits the wrapped-mode program entry point. All GTK
// bootstrapping lives in gtk4rt.Run, so the generated main is cgo-free.
func emitGTK4MainWrapped(b *strings.Builder) {
	b.WriteString("\nfunc main() {\n")
	b.WriteString("\tgtk4rt.Run(func(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\t\treturn New().BuildUI(app)\n")
	b.WriteString("\t})\n")
	b.WriteString("}\n")
}
