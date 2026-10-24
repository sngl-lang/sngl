package gtk4

import (
	"fmt"
	"regexp"
	"strings"

	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/ir"
)

// This file implements "wrapped mode" gtk4 codegen: instead of emitting inline
// cgo (`C.gtk_*` calls behind an `import "C"` preamble in every generated
// program), it emits calls to the shared pkg/go/gtk4rt runtime over an opaque
// gtk4rt.Handle. The GTK cgo bindings then compile once (in gtk4rt) and Go's
// build cache reuses them, instead of recompiling the gtk.h preamble per
// program.
//
// The declared protocol calls gtk4rt for every GIR class (declgen), so what
// leaves a `C.` is the program's own: a canvas, or a #[cnative] it calls.
// emitIR scans the output and, if any remains, discards the wrapped attempt
// and re-emits in cgo mode, so a program is never half-wrapped.

// gtk4rtPkg is the import path of the shared GTK4 runtime. Emitting an
// ir.Func with this Foreign.Path auto-registers the import via the golang IR
// context (see evalNamespaceCall).
const gtk4rtPkg = "duckfam.us/sngl/pkg/go/gtk4rt"

// gtk4rtHandleType is the Go type wrapped-mode code uses for every widget
// field/local, replacing the per-widget *C.GtkX pointer types.
const gtk4rtHandleType = "gtk4rt.Handle"

// rtCall builds a `gtk4rt.<name>(args...)` call. The non-"C" Foreign.Path makes
// evalNamespaceCall register the gtk4rt import and emit the Foreign.Name verbatim.
func rtCall(name string, ret *ir.Type, args ...ir.Expr) *ir.Call {
	ca := make([]ir.CallArg, len(args))
	for i, a := range args {
		ca[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     ret,
		Receiver: &ir.Ident{Name: "gtk4rt"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: gtk4rtPkg, Name: "gtk4rt." + name}},
		Args:     ca,
	}
}

// rtConst references an exported gtk4rt identifier (e.g. a constant). The
// gtk4rt import is registered by the rtCall(s) the same file always emits.
func rtConst(name string, t *ir.Type) *ir.Ident {
	return &ir.Ident{Name: "gtk4rt." + name, Type: t}
}

// rtOrientation is gtk4rt's Orientation, the type of its two constants.
func rtOrientation() *ir.Type { return ir.NativeGoNamed("gtk4rt.Orientation") }

// cOrientationVertical is the cgo constant a vertical box is built with.
func cOrientationVertical() *ir.Ident {
	return &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.NativeGoNamed("C.GtkOrientation")}
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
	// The copies of a widget (copiesField) are a map of what one is.
	if rest, ok := strings.CutPrefix(cType, copyMapType("")); ok {
		return copyMapType(widgetFieldGoType(rest, wrapped))
	}
	if rest, ok := strings.CutPrefix(cType, "[]"); ok {
		return "[]" + widgetFieldGoType(rest, wrapped)
	}
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

// rtSetterKind is the Go type a gtk4rt setter takes its value as.
type rtSetterKind int

const (
	rtStr rtSetterKind = iota
	rtBool
	rtInt
	rtOrient
)

// rtSetterTable maps a GTK setter to the gtk4rt function wrapping it, which
// the set declgen writes calls in its place (writeSets).
var rtSetterTable = map[string]struct {
	fn   string
	kind rtSetterKind
}{
	"gtk_label_set_label":            {"LabelSetLabel", rtStr},
	"gtk_button_set_label":           {"ButtonSetLabel", rtStr},
	"gtk_check_button_set_label":     {"CheckButtonSetLabel", rtStr},
	"gtk_check_button_set_active":    {"CheckButtonSetActive", rtBool},
	"gtk_editable_set_text":          {"EditableSetText", rtStr},
	"gtk_image_set_from_file":        {"ImageSetFromFile", rtStr},
	"gtk_box_set_spacing":            {"BoxSetSpacing", rtInt},
	"gtk_orientable_set_orientation": {"OrientableSetOrientation", rtOrient},
}

// rtChildWrappers are the gtk4rt wrappers that add or remove a child, keyed by the
// GTK function each one calls. The key comes from the introspection data
// (gir.ChildAdder), so this says only which of those calls this runtime package
// wraps -- a fact about pkg/go/gtk4rt, not about GTK. A C type switch said both
// at once, and said the second one wrong: it knew three parents where the cgo
// path knew seven, so a frame or a popover in wrapped mode fell through to raw
// cgo in a file that compiles without cgo.
var rtChildWrappers = map[string]string{
	"gtk_box_append":                "BoxAppend",
	"gtk_box_remove":                "BoxRemove",
	"gtk_window_set_child":          "WindowSetChild",
	"gtk_scrolled_window_set_child": "ScrolledWindowSetChild",
	"gtk_frame_set_child":           "FrameSetChild",
}

// rtChildAppendCall is the gtk4rt call that puts child into parent, or takes
// it out, through the GTK function adder names: gtk4rt's wrapper of it where
// it has one, ChildCall naming it otherwise.
func rtChildAppendCall(adder gir.ChildAdder, parent, child ir.Expr) ir.Stmt {
	if fn, ok := rtChildWrappers[adder.Func]; ok {
		return &ir.CallStmt{Call: rtCall(fn, ir.TypVoid, parent, child)}
	}
	return &ir.CallStmt{Call: rtCall("ChildCall", ir.TypVoid, &ir.Literal{Type: ir.TypString, Value: adder.Func}, parent, child)}
}

// rtEventGetterExpr is the wrapped-mode analogue of cgoEventGetterExpr: it
// reads a widget's bound value through gtk4rt rather than cgo. Returns nil when
// the widget's read-back isn't wrapped (→ fallback).
func rtEventGetterExpr(cType string, widgetRef ir.Expr) ir.Expr {
	switch cType {
	case "GtkEntry":
		return rtCall("EditableGetText", ir.TypString, widgetRef)
	case "GtkCheckButton":
		return rtCall("CheckButtonGetActive", ir.TypBool, widgetRef)
	case "GtkSwitch":
		return rtCall("SwitchGetActive", ir.TypBool, widgetRef)
	case "GtkCalendar":
		return rtCall("CalendarGetDate", ir.NativeGoPointerOf("time.Time"), widgetRef)
	case "GtkDropDown":
		return rtCall("DropDownLabel", ir.TypString, widgetRef)
	}
	return nil
}

// emitBuildUIWrapped is the wrapped-mode analogue of emitBuildUI: it emits the
// buildWidgetTree + BuildUI scaffolding using gtk4rt over gtk4rt.Handle. The
// widget-tree body (buildBuf) was already emitted in wrapped mode by the
// translator, so only the surrounding scaffolding is produced here.
func emitBuildUIWrapped(b *strings.Builder, buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string, title string, fields map[string]bool) {
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
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		b.WriteString("func (m *Model) buildWidgetTree() {}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
		b.WriteString("\twin := gtk4rt.ApplicationWindowNew(app)\n")
		b.WriteString("\treturn win\n")
		b.WriteString("}\n\n")
		return
	}
	// Window-class passthrough: sole top-level is a window widget.
	if len(topLevelRefs) == 1 && isWindowClass(topLevelCType[topLevelRefs[0]]) {
		b.WriteString("func (m *Model) buildWidgetTree() {\n")
		b.WriteString(buildBuf.String())
		b.WriteString("}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
		b.WriteString("\tm.buildWidgetTree()\n")
		fmt.Fprintf(b, "\treturn %s\n", mref(topLevelRefs[0]))
		b.WriteString("}\n\n")
		return
	}
	// General case: wrap top-level children in a synthetic __root box.
	b.WriteString("func (m *Model) buildWidgetTree() {\n")
	b.WriteString("\tif m.__root != nil {\n\t\treturn\n\t}\n")
	b.WriteString("\tm.__root = gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)\n")
	b.WriteString(buildBuf.String())
	for _, ref := range topLevelRefs {
		fmt.Fprintf(b, "\tgtk4rt.BoxAppend(m.__root, %s)\n", mref(ref))
	}
	b.WriteString("}\n\n")
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	b.WriteString("\twin := gtk4rt.ApplicationWindowNew(app)\n")
	b.WriteString("\tgtk4rt.WindowSetDefaultSize(win, 480, 640)\n")
	if title != "" {
		fmt.Fprintf(b, "\tgtk4rt.WindowSetTitle(win, %s)\n", title)
	}
	b.WriteString("\tgtk4rt.WindowSetChild(win, m.__root)\n")
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

	"duckfam.us/sngl/pkg/go/gtk4rt"
	"duckfam.us/sngl/pkg/go/testagent"
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

	"duckfam.us/sngl/pkg/go/gtk4rt"
	"duckfam.us/sngl/pkg/go/testagent"
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

// emitGTK4RunMainWrapped is the entry point for a program that wrote `@run`.
// The tree is built first -- its first settle included -- so a write the
// handler makes is an ordinary update of widgets that exist; `run` then
// presents the window and runs the loop, and a handler that never calls it
// gets the loop with the window hidden.
func emitGTK4RunMainWrapped(b *strings.Builder, pkg *ir.Package) {
	b.WriteString("\nfunc main() {\n")
	b.WriteString("\tgtk4rt.Init()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	golang.EmitAppStart(b, pkg, "gtk4rt.Args()", func(show bool) string {
		if show {
			return "gtk4rt.Run(m.BuildUI)"
		}
		return "gtk4rt.RunHidden(m.BuildUI)"
	}, "gtk4rt.RefuseRunAgain")
	b.WriteString("}\n")
}

// emitGTK4MainWrapped emits the wrapped-mode program entry point. All GTK
// bootstrapping lives in gtk4rt.Run, so the generated main is cgo-free. The
// Model is made on activate, after GTK is up, so the teardown asks whether
// one was.
func emitGTK4MainWrapped(b *strings.Builder, teardown *ir.Func) {
	b.WriteString("\nfunc main() {\n")
	if teardown == nil {
		b.WriteString("\tgtk4rt.Run(func(app gtk4rt.Handle) gtk4rt.Handle {\n")
		b.WriteString("\t\treturn New().BuildUI(app)\n")
		b.WriteString("\t})\n")
		b.WriteString("}\n")
		return
	}
	b.WriteString("\tvar m *Model\n")
	b.WriteString("\tgtk4rt.Run(func(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\t\tm = New()\n")
	b.WriteString("\t\treturn m.BuildUI(app)\n")
	b.WriteString("\t})\n")
	b.WriteString("\tif m != nil {\n")
	fmt.Fprintf(b, "\t\tm.%s()\n", teardown.Name)
	b.WriteString("\t}\n")
	b.WriteString("}\n")
}
