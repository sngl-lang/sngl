package gtk4

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// toplevelTag is gtk4.sngl's `Toplevel`, the primitive `ui.window` is on this
// platform. Its handle is the box its content is appended to, so a child and
// a render slot inside it are ordinary box operations; what makes it a window
// is being attached to the application (ir.AppParent), which gtk4rt answers.
const toplevelTag = "Toplevel"

// emitToplevelCreate builds a Toplevel's box. In wrapped mode gtk4rt keeps
// the window's state beside it; the inline-cgo scaffold has no runtime, so
// the box is all there is and BuildUI puts the first one in a window.
func (t *gtk4Translator) emitToplevelCreate(id string) []ir.Stmt {
	if t.toplevels == nil {
		t.toplevels = map[string]bool{}
	}
	t.toplevels[id] = true
	if t.shared != nil {
		t.shared.toplevels[id] = true
	}
	var ctor ir.Expr = rtCall("ToplevelNew")
	if !t.wrapped {
		ctor = nativeCall("gtk_box_new", &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}, intLit("6"))
	}
	return t.emitConstructorAssign(id, "GtkBox", ctor)
}

// isToplevel reports whether node names a Toplevel this scope or another
// created. The C type alone cannot say -- it is a GtkBox -- so the node's own
// declaration answers where the id is not one this scope built.
func (t *gtk4Translator) isToplevel(bare string) bool {
	if t.toplevels[bare] {
		return true
	}
	if t.shared != nil && t.shared.toplevels[bare] {
		return true
	}
	return false
}

// toplevelPropAssign sets a Toplevel's title or visibility.
func (t *gtk4Translator) toplevelPropAssign(node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	ref := t.qualifyNodeExpr(node)
	// The default: a window that names no title is built with none.
	if lit, ok := value.(*ir.Literal); ok && prop == "title" && lit.Value == "" && t.building {
		return nil
	}
	if !t.wrapped {
		switch prop {
		case "title":
			if t.shared != nil && t.building {
				t.shared.toplevelTitles[codeKey(node)] = value
			}
		}
		return nil
	}
	switch prop {
	case "title":
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ToplevelSetTitle", ref, value)}}
	case "visible":
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ToplevelSetVisible", ref, value)}}
	}
	return nil
}

// codeKey is the id a node expression names, for a map keyed by node.
func codeKey(node ir.Expr) string {
	if id, ok := node.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}

// toplevelAttachHandler connects what the host reports about a Toplevel: a
// change of `visible` it made itself, and the window manager's close, which
// arrives as the first and then as `@closed`.
func (t *gtk4Translator) toplevelAttachHandler(node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	if !t.wrapped {
		// The override subscribes to the host's close whether or not the
		// program did; only a handler with something in it is a refusal.
		if event == "closed" && !t.emptyHandler(handler) {
			t.shared.fail(fmt.Errorf("gtk4: a window's @closed needs the gtk4rt runtime, and this program's generated code calls into cgo directly"))
		}
		return nil
	}
	fn := ""
	switch event {
	case "closed":
		fn = "ToplevelOnClosed"
	case "visible":
		fn = "ToplevelOnVisible"
	default:
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall(fn, t.qualifyNodeExpr(node), handler)}}
}

// appAttach is a node at the root of the package body joining the
// application: a Toplevel's window is created and, once the loop runs, put on
// screen.
func (t *gtk4Translator) appAttach(child ir.Expr) []ir.Stmt {
	t.dropTopLevel(child)
	if id, ok := child.(*ir.Ident); ok {
		t.appChildren = append(t.appChildren, id.Name)
	}
	if !t.wrapped {
		if len(t.appChildren) > 1 {
			t.shared.fail(fmt.Errorf("gtk4: a program with more than one window needs the gtk4rt runtime, and this program's generated code calls into cgo directly"))
		}
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppAttach", t.qualifyNodeExpr(child))}}
}

func (t *gtk4Translator) appDetach(child ir.Expr) []ir.Stmt {
	if !t.wrapped {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppDetach", t.qualifyNodeExpr(child))}}
}

// dropTopLevel takes child off the list of refs buildWidgetTree parents into
// a root box, since a parent has taken it.
func (t *gtk4Translator) dropTopLevel(child ir.Expr) {
	id, ok := child.(*ir.Ident)
	if !ok {
		return
	}
	for i, name := range t.topLevel {
		if name == id.Name {
			t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
			return
		}
	}
}

// appSlotCall is a render slot at the root of the package body, handed the
// application as its parent: gtk4rt.App, which the box calls it makes answer
// for the application.
func (t *gtk4Translator) appSlotCall(cs *ir.CallStmt) ([]ir.Stmt, bool) {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 || !ir.IsAppParent(cs.Call.Args[0].Value) {
		return nil, false
	}
	if !t.wrapped {
		t.shared.fail(fmt.Errorf("gtk4: a window under an `if` or a `for` that reads state needs the gtk4rt runtime, and this program's generated code calls into cgo directly"))
		return nil, true
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: rtConst("App")}}
	return []ir.Stmt{&ir.CallStmt{Call: &call}}, true
}

// emitBuildUIApp is buildWidgetTree and BuildUI for a program whose root is
// the application: the tree is the package body, every Toplevel in it
// attached as it is built, and BuildUI -- what a snapshot and a test take --
// shows the first one's content in a window of app's.
func emitBuildUIApp(b *strings.Builder, buildBuf *strings.Builder, first string, fields map[string]bool) {
	mref := func(ref string) string {
		if !fields[ref] {
			return ref
		}
		return "m." + ref
	}
	b.WriteString("func (m *Model) buildWidgetTree() {\n")
	b.WriteString("\tif m.__built {\n\t\treturn\n\t}\n")
	b.WriteString("\tm.__built = true\n")
	b.WriteString(buildBuf.String())
	b.WriteString("}\n\n")
	b.WriteString("// BuildUI constructs the widget tree and returns a window showing the first\n")
	b.WriteString("// window's content.\n")
	b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	if first == "" {
		b.WriteString("\treturn gtk4rt.ApplicationWindowNew(app)\n")
	} else {
		fmt.Fprintf(b, "\treturn gtk4rt.ToplevelWindow(app, %s)\n", mref(first))
	}
	b.WriteString("}\n\n")
}

// emitGTK4AppMain is the entry point of a program whose root is the
// application. The tree is built first -- the first settle included -- and
// gtk4rt.RunWindows then creates the toplevels attached to it. `run` is what
// puts them on screen: a program that wrote `@run` and never called it runs
// the loop with none shown.
func emitGTK4AppMain(b *strings.Builder, run string, hasRun bool, teardown *ir.Func) {
	b.WriteString("\nfunc main() {\n")
	b.WriteString("\tgtk4rt.Init()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	switch {
	case !hasRun:
		b.WriteString("\tgtk4rt.RunWindows(true)\n")
	case run == "":
		// An empty handler is emitted as no function at all, and never calls
		// run.
		b.WriteString("\tgtk4rt.RunWindows(false)\n")
	default:
		b.WriteString("\tran := false\n")
		fmt.Fprintf(b, "\t%s(gtk4rt.Args(), func() {\n", run)
		b.WriteString("\t\tran = true\n")
		b.WriteString("\t\tgtk4rt.RunWindows(true)\n")
		b.WriteString("\t})\n")
		b.WriteString("\tif !ran {\n")
		b.WriteString("\t\tgtk4rt.RunWindows(false)\n")
		b.WriteString("\t}\n")
	}
	if teardown != nil {
		fmt.Fprintf(b, "\tm.%s()\n", teardown.Name)
	}
	b.WriteString("}\n")
}

// appendWidgetFieldOnce adds f unless a field of its name is declared.
func appendWidgetFieldOnce(fields []widgetField, f widgetField) []widgetField {
	for _, x := range fields {
		if x.name == f.name {
			return fields
		}
	}
	return append(fields, f)
}

// emptyHandler reports whether handler names a function with nothing in it.
func (t *gtk4Translator) emptyHandler(handler ir.Expr) bool {
	var fn *ir.Func
	switch h := handler.(type) {
	case *ir.Lambda:
		if h.Func != nil {
			fn = h.Func
		}
	case *ir.Ident:
		if f, ok := h.Sym.(*ir.Func); ok {
			fn = f
		} else if t.pkg != nil {
			for _, f := range t.pkg.Funcs {
				if f.Name == h.Name {
					fn = f
				}
			}
		}
	}
	return fn != nil && len(fn.Block) == 0
}

// refuseCgoVisibleWrites is the inline-cgo scaffold's one refusal about
// `visible`: it builds the one window it can and shows it, so a program that
// shows or hides one while it runs -- `details.close()` -- needs the runtime.
// The window's own write-back is exempt: it runs only when the host reports,
// which this scaffold never does.
func refuseCgoVisibleWrites(pkg *ir.Package, tops map[string]bool) error {
	// Only what runs: a window's `open` and `close` are cloned where it is
	// spliced whether or not anything calls them.
	reached := map[*ir.Func]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			reached[x.Func] = true
		case *ir.Ident:
			if f, ok := x.Sym.(*ir.Func); ok {
				reached[f] = true
			}
		}
		return nil
	})
	for _, fn := range pkg.Funcs {
		if fn == nil || (fn.LoweredFromEvent == "visible" && tops[fn.LoweredFromNode]) {
			continue
		}
		if !reached[fn] && fn.LoweredFromEvent == "" {
			continue
		}
		var found bool
		_ = ir.WalkStmts(fn.Block, func(s ir.Stmt) error {
			a, ok := s.(*ir.Assign)
			if !ok {
				return nil
			}
			sel, ok := a.Target.(*ir.Select)
			if ok && sel.Field == "visible" && tops[codeKey(sel.Operand)] {
				found = true
				return ir.SkipAll
			}
			return nil
		})
		if found {
			return fmt.Errorf("gtk4: a window shown or hidden while the program runs needs the gtk4rt runtime, and this program's generated code calls into cgo directly")
		}
	}
	return nil
}
