package gtk4

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// toplevelTag is gtk4.sngl's `Toplevel`, the primitive `ui.window` is on this
// platform. Its handle is the box its content is appended to, so a child and
// a render slot inside it are ordinary box operations; what makes it a window
// is being attached to the application (ir.AppParent), which gtk4rt answers.
const toplevelTag = "Toplevel"

// emitToplevelCreate builds a Toplevel's box through the create gtk4.sngl
// declares, gtk4rt's in either mode: the inline-cgo scaffold has no runtime
// loop, so BuildUI puts the first one in the window it makes.
func (t *gtk4Translator) emitToplevelCreate(id string, made ir.Expr) []ir.Stmt {
	if made == nil {
		panic("gtk4: a Toplevel reached the translator without the create gtk4.sngl declares")
	}
	if t.toplevels == nil {
		t.toplevels = map[string]bool{}
	}
	t.toplevels[id] = true
	if t.shared != nil {
		t.shared.toplevels[id] = true
	}
	return t.emitConstructorAssign(id, "GtkBox", made)
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

// untitled reports whether a Toplevel's write is the empty title it is built
// with by default, which a window that names none is built without.
func (t *gtk4Translator) untitled(prop string, value ir.Expr) bool {
	if prop != "title" || !t.building {
		return false
	}
	if c, ok := value.(*ir.Call); ok && len(c.Args) == 2 {
		value = c.Args[1].Value
	}
	lit, ok := value.(*ir.Literal)
	return ok && lit.Value == ""
}

// codeKey is the id a node expression names, for a map keyed by node.
func codeKey(node ir.Expr) string {
	if id, ok := node.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}

// refuseCgoClosed refuses a window's `@closed` in an inline-cgo build, whose
// scaffold has no runtime to report the window manager's close. The override
// subscribes to it whether or not the program did; only a handler with
// something in it is a refusal.
func (t *gtk4Translator) refuseCgoClosed(event string, handler ir.Expr) {
	if !t.wrapped && event == "closed" && !t.emptyHandler(handler) {
		t.shared.fail(fmt.Errorf("gtk4: a window's @closed needs the gtk4rt runtime, and this program's generated code calls into cgo directly"))
	}
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
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppAttach", ir.TypVoid, t.qualifyNodeExpr(child))}}
}

func (t *gtk4Translator) appDetach(child ir.Expr) []ir.Stmt {
	if !t.wrapped {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppDetach", ir.TypVoid, t.qualifyNodeExpr(child))}}
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
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 || !ir.IsAppParent(cs.Call.Args[0].Value) {
		return nil, false
	}
	if !t.wrapped {
		t.shared.fail(fmt.Errorf("gtk4: a window under an `if` or a `for` that reads state needs the gtk4rt runtime, and this program's generated code calls into cgo directly"))
		return nil, true
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: rtConst("App", gtk4rtHandleIRType())}}
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
func emitGTK4AppMain(b *strings.Builder, pkg *ir.Package, open string) {
	b.WriteString("\nfunc main() {\n")
	b.WriteString(open)
	b.WriteString("\tgtk4rt.Init()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	golang.EmitAppStart(b, pkg, "gtk4rt.Args()", func(show bool) string {
		return fmt.Sprintf("gtk4rt.RunWindows(%t)", show)
	}, "gtk4rt.RefuseRunAgain")
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
		if fn == nil || (fn.Lowered.Event == "visible" && tops[fn.Lowered.Node]) {
			continue
		}
		if !reached[fn] && fn.Lowered.Event == "" {
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
