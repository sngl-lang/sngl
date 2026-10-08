package fyne

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// toplevelTag is fyne.sngl's `Toplevel`, the primitive `ui.window` is on this
// platform: a fynelayout.Toplevel, which is a box its content is added to and
// a window once the node is attached to the application (ir.AppParent).
const toplevelTag = "Toplevel"

// toplevelSpec is the Toplevel's construction, written here rather than as a
// Spec record because its type is this platform's runtime and not a Fyne
// widget. Everything downstream is the ordinary path: the box takes children
// through Add, `title` and `visible` have setters, and the host's reports are
// callback fields.
func toplevelSpec(props map[string]ir.Expr) *fyneSpec {
	return &fyneSpec{
		New:       fyneNative{Path: fyneLayoutImportPath, Name: "NewToplevel"},
		GoType:    fyneNative{Path: fyneLayoutImportPath, Name: "*Toplevel"},
		Add:       "Add",
		CtorProps: props,
		Setters:   map[string]string{"title": "SetTitle", "visible": "SetVisible"},
		Currents:  map[string]string{},
		Handlers: map[string]fyneHandler{
			"closed": {Field: "OnClosed", Signature: "func()"},
			// Named for the parameter the binding's write-back handler is
			// given (passPropBindings), which is what its body reads.
			"visible": {Field: "OnVisible", Signature: "func(__visible bool)", Param: "__visible"},
		},
		toplevel: true,
	}
}

// appSlotArg is the argument a render slot is handed for its container: the
// application at the root of the package body, and a Toplevel's box inside
// one, since a slot renders into a *fyne.Container.
func (t *fyneTranslator) appSlotArg(cs *ir.CallStmt) *ir.CallStmt {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	arg := cs.Call.Args[0].Value
	var v ir.Expr
	switch {
	case ir.IsAppParent(arg):
		v = &ir.Ident{Name: fyneNative{Path: fyneLayoutImportPath, Name: "App"}.qualify(t.gc), Type: ir.TypDyn}
	default:
		sp := t.specs[codegen.IdentBareName(arg)]
		if sp == nil || !sp.toplevel {
			return nil
		}
		v = &ir.Select{Operand: t.qualifyParentExpr(arg), Field: "Container", Type: ir.TypDyn}
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: v}}
	return &ir.CallStmt{Call: &call}
}

// appAttach is a node at the root of the package body joining the
// application: a Toplevel's window is created and, once the loop runs, put on
// screen.
func (t *fyneTranslator) appAttach(child ir.Expr) []ir.Stmt {
	if id, ok := child.(*ir.Ident); ok {
		t.appChildren = append(t.appChildren, id.Name)
	}
	return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.AppAttach", fyneLayoutImportPath,
		[]ir.Expr{t.qualifyChildExpr(child)}, ir.TypVoid)}}
}

// emitIRBuildUIApp is BuildUI for a program whose root is the application: the
// tree is the package body, every Toplevel in it attached as it is built, and
// what BuildUI returns -- what a test or a snapshot takes -- is the first
// one's content.
func emitIRBuildUIApp(b *strings.Builder, info *irAnalysis, buildBuf *strings.Builder, first string, gc *golang.GoIRContext) {
	ret := "widget.NewLabel(\"\")"
	if first != "" {
		// The content box, which is what a harness puts in a window of its
		// own: the Toplevel around it is the program's window.
		ret = gc.EvalExpr(elementRef(first)) + ".Container"
	} else {
		gc.RequireImport("fyne.io/fyne/v2/widget")
	}
	b.WriteString("// BuildUI creates the widget tree and returns the first window's content.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")
	fmt.Fprintf(b, "\tif m.__built {\n\t\treturn %s\n\t}\n", ret)
	b.WriteString("\tm.__built = true\n")
	b.WriteString(buildBuf.String())
	if info.NeedsToast && first != "" {
		gc.RequireImport("fyne.io/fyne/v2/widget")
		gc.RequireImport("fyne.io/fyne/v2/container")
		b.WriteString("\tm.toastLabel = widget.NewLabel(\"\")\n")
		b.WriteString("\tm.toastBox = container.NewVBox(m.toastLabel)\n")
		b.WriteString("\tm.toastBox.Hide()\n")
		fmt.Fprintf(b, "\t%s.Add(m.toastBox)\n", ret)
	}
	fmt.Fprintf(b, "\treturn %s\n", ret)
	b.WriteString("}\n\n")
}

// emitIRAppMain is the entry point of a program whose root is the
// application. The tree is built first -- the first settle included -- and
// fynelayout.RunWindows then creates the windows attached to it. `run` is what
// puts them on screen: a program that wrote `@run` and never called it runs
// the loop with none shown.
func emitIRAppMain(b *strings.Builder, pkg *ir.Package) {
	b.WriteString("func main() {\n")
	b.WriteString("\ta := app.New()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.BuildUI()\n")
	if pkg.RemoteSettle != nil {
		// Through DoAndWait because a settle arrives on the fetch's goroutine
		// and Fyne's widgets belong to the main one.
		fmt.Fprintf(b, "\tremote.Default.OnSettle(func() { fyne.DoAndWait(m.%s) })\n", pkg.RemoteSettle.Name)
	}
	switch {
	case pkg.Run == nil:
		b.WriteString("\tfynelayout.RunWindows(a, true)\n")
	case len(pkg.Run.Block) == 0:
		// An empty handler is emitted as no function at all, and never calls
		// run.
		b.WriteString("\tfynelayout.RunWindows(a, false)\n")
	default:
		b.WriteString("\tran := false\n")
		fmt.Fprintf(b, "\t%s(os.Args[1:], func() {\n", golang.ModelCallee(pkg, pkg.Run, "m"))
		b.WriteString("\t\tran = true\n")
		b.WriteString("\t\tfynelayout.RunWindows(a, true)\n")
		b.WriteString("\t})\n")
		b.WriteString("\tif !ran {\n")
		b.WriteString("\t\tfynelayout.RunWindows(a, false)\n")
		b.WriteString("\t}\n")
	}
	if pkg.Teardown != nil {
		fmt.Fprintf(b, "\tm.%s()\n", pkg.Teardown.Name)
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}
