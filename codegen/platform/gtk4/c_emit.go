package gtk4

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// A program in C is one file, main.c, beside the C runtime (pkg/c) and
// gtk4's own (lib/platform/gtk4/rt). Its widgets are the ones the cgo option
// declares, which call GTK as C does, and what a cgo build still calls in
// gtk4rt -- a window, a box's append, a look -- is rt's C under the name
// cNatives gives it.
//
// The program's state is file-scope: a package var, and each widget the
// package body makes, is a static of main.c, set by sngl_build when the
// application starts. A program has one instance of its tree, so nothing
// holds them but the file.

// rtFiles are gtk4's runtime sources, written beside main.c.
var rtFiles = []string{"sngl_gtk.h", "sngl_gtk.c", "look.c"}

// generateC writes req's program as C.
func (g *Generator) generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	if pkg.Run != nil {
		return fmt.Errorf("gtk4: @run is not yet written in C")
	}
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "main.c", Source: req.Source, Platform: platformName}, pkg, platformName)
	fe.RequireImport("<gtk/gtk.h>")
	fe.RequireImport("sngl_gtk.h")
	e := &cEmitter{fe: fe, ctx: fe.Context(), pkg: pkg, globals: map[*ir.Var]string{}, nodes: map[string]string{}}
	e.ctx.State = e.state
	e.ctx.Types = e.types
	e.ctx.Native = e.native

	var globals strings.Builder
	vars := append(append([]*ir.Var(nil), pkg.Consts...), pkg.Vars...)
	for _, v := range vars {
		e.globals[v] = clang.Global(v.Name)
	}
	// What the package body builds, rendered first, so that the widgets it
	// makes are known before anything reads one.
	body := codegen.WalkLowered(context.Background(), pkg.Body, e)
	var build strings.Builder
	build.WriteString("\nstatic void sngl_build(void) {\n")
	for _, v := range vars {
		if v.Init == nil {
			continue
		}
		fmt.Fprintf(&build, "\t%s = %s;\n", clang.Global(v.Name), e.ctx.EvalExpr(v.Init))
	}
	for _, l := range e.ctx.EvalBlock(body) {
		build.WriteString("\t" + l + "\n")
	}
	build.WriteString("}\n")
	if e.err != nil {
		return e.err
	}

	for _, v := range vars {
		fmt.Fprintf(&globals, "static %s %s;\n", e.ctx.TypeName(v.Type), clang.Global(v.Name))
	}
	for _, id := range e.order {
		fmt.Fprintf(&globals, "static %s *%s;\n", e.nodes[id], clang.Global(id))
	}
	io.WriteString(fe, "\n"+globals.String())
	for _, fn := range pkg.Funcs {
		if fn.IsTest || len(fn.Block) == 0 {
			continue
		}
		// A handler writes its widgets as the body does.
		fn.Block = codegen.WalkLowered(context.Background(), fn.Block, e)
		fe.Func(fn)
	}
	if e.err != nil {
		return e.err
	}
	io.WriteString(fe, build.String())
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t" + clang.Global(pkg.Teardown.Name) + "();\n"
	}
	io.WriteString(fe, "\nint main(void) {\n\tint status = sngl_app_run(sngl_build);\n"+teardown+"\treturn status;\n}\n")
	if err := fe.Close(); err != nil {
		return fmt.Errorf("gtk4: %w", err)
	}
	if err := writeRawFile(sink, clang.BuildFile, clang.Manifest("gtk4")); err != nil {
		return err
	}
	// The runtimes are the same for every program, so a golden holds their
	// digest rather than their text.
	for name, data := range clang.Runtime() {
		codegen.MarkBoilerplate(sink, name)
		if err := writeRawFile(sink, name, data); err != nil {
			return err
		}
	}
	for _, name := range rtFiles {
		data, err := fs.ReadFile(lib.FS, "platform/gtk4/rt/"+name)
		if err != nil {
			return fmt.Errorf("gtk4: its C runtime: %w", err)
		}
		codegen.MarkBoilerplate(sink, name)
		if err := writeRawFile(sink, name, data); err != nil {
			return err
		}
	}
	return nil
}

// cEmitter translates the lowered tree's node operations into the C calls
// that build it.
type cEmitter struct {
	fe  *clang.FileEmitter
	ctx *clang.Context
	pkg *ir.Package
	// globals are the package's vars, by the static each is.
	globals map[*ir.Var]string
	// nodes are the C type of each widget the body makes, by its id, and
	// order the ids in the order made.
	nodes map[string]string
	order []string
	seq   int
	err   error
}

func (e *cEmitter) fail(format string, args ...any) []ir.Stmt {
	if e.err == nil {
		e.err = fmt.Errorf("gtk4: "+format+" is not yet written in C", args...)
	}
	return nil
}

// state renders a package var, and a widget the body made, as its static.
func (e *cEmitter) state(n *ir.Ident) (string, bool) {
	if v, ok := n.Sym.(*ir.Var); ok {
		if s, ok := e.globals[v]; ok {
			return s, true
		}
	}
	if _, ok := e.nodes[n.Name]; ok {
		return clang.Global(n.Name), true
	}
	return "", false
}

// types names a widget as the GObject pointer it is: its class's where it is
// one of GIR's, and GtkWidget's for any node else.
func (e *cEmitter) types(t *ir.Type) (string, bool) {
	if t.Kind != ir.TypeComponent {
		return "", false
	}
	comp, _ := t.Decl.(*ir.Component)
	if cType := widgetCType(comp); cType != "" {
		return cType + " *", true
	}
	return "GtkWidget *", true
}

// node is the widget id names, as the static it is.
func (e *cEmitter) node(x ir.Expr) (string, bool) {
	id, ok := x.(*ir.Ident)
	if !ok {
		return "", false
	}
	if _, ok := e.nodes[id.Name]; !ok {
		return "", false
	}
	return clang.Global(id.Name), true
}

func (e *cEmitter) OnMakeNode(ctx context.Context, id, tag string, made ir.Expr) []ir.Stmt {
	cType := tag
	if _, ok := e.registryClass(tag); !ok {
		// A primitive of gtk4.sngl's own is a GtkWidget: a toplevel's box.
		cType = "GtkWidget"
	}
	if _, dup := e.nodes[id]; !dup {
		e.order = append(e.order, id)
	}
	e.nodes[id] = cType
	v := &ir.Var{Name: id, Type: ir.NativePointerOf(cType)}
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: id, Sym: v, Type: v.Type},
		Op:     ast.AssignSet,
		Value:  &ir.Conversion{Type: v.Type, Operand: made},
	}}
}

// registryClass reports whether tag names one of GIR's classes.
func (e *cEmitter) registryClass(tag string) (string, bool) {
	return tag, strings.HasPrefix(tag, "Gtk")
}

func (e *cEmitter) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	return e.fail("a %s made without a declared create", tag)
}

func (e *cEmitter) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	if prop == lookPropName {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("SetLook", ir.TypVoid, node, value)}}
	}
	call, ok := value.(*ir.Call)
	if !ok {
		return e.fail("setting %s from %T", prop, value)
	}
	return []ir.Stmt{&ir.CallStmt{Call: call}}
}

func (e *cEmitter) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if ir.IsAppParent(parent) {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppAttach", ir.TypVoid, child)}}
	}
	return e.fail("appending to %s without its class's append", codegen.IdentBareName(parent))
}

func (e *cEmitter) OnAppendThrough(ctx context.Context, parent, child, call ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: call.(*ir.Call)}}
}

func (e *cEmitter) OnRemoveThrough(ctx context.Context, parent, child, call ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: call.(*ir.Call)}}
}

// OnConnectThrough is the declared connect, its handler parameter bound to
// the handler: a local, which a template's lambda reads as its capture
// rather than building the closure again at each event.
func (e *cEmitter) OnConnectThrough(ctx context.Context, node ir.Expr, event string, handler, call ir.Expr) []ir.Stmt {
	// A handler lowering attaches where the program wrote none is a
	// function with no body: there is nothing to run, so nothing connects.
	if unwritten(handler) {
		return nil
	}
	e.seq++
	name := fmt.Sprintf("__handler%d", e.seq)
	v := &ir.Var{Name: name, Type: handler.ExprType()}
	ref := &ir.Ident{Name: name, Sym: v, Type: v.Type}
	c, ok := ir.CloneExprSharingDecls(call).(*ir.Call)
	if !ok {
		return e.fail("a connect making %T", call)
	}
	_ = ir.Rewrite(c, func(n ir.Node) (ir.Node, error) {
		if id, ok := n.(*ir.Ident); ok {
			if _, ok := id.Sym.(*ir.Param); ok {
				return ref, ir.SkipDir
			}
		}
		return n, nil
	})
	return []ir.Stmt{
		&ir.LocalVar{Name: name, Type: v.Type, Init: handler, Sym: v},
		&ir.CallStmt{Call: c},
	}
}

func (e *cEmitter) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	return e.fail("@%s without its class's connect", event)
}

func (e *cEmitter) OnDetachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	return e.fail("taking @%s off", event)
}

func (e *cEmitter) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return e.fail("removing a child without its class's remove")
}

func (e *cEmitter) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	return e.fail("a component built at run time")
}

func (e *cEmitter) OnComponentRoot(ctx context.Context, id string, inst, key ir.Expr) []ir.Stmt {
	return e.fail("a component built at run time")
}

func (e *cEmitter) OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	return e.fail("a component built at run time")
}

func (e *cEmitter) OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt {
	return e.fail("a component built at run time")
}

func (e *cEmitter) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return e.fail("a reactive slot")
}

func (e *cEmitter) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	return e.fail("a reactive slot")
}

func (e *cEmitter) OnIter(ctx context.Context, iter ir.Expr) ir.Expr { return iter }
func (e *cEmitter) OnCond(ctx context.Context, cond ir.Expr) ir.Expr { return cond }
func (e *cEmitter) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}

// cNatives are the gtk4rt calls a cgo build still makes, as rt's C writes
// each: its name, or what it writes in its place given its arguments.
func (e *cEmitter) native(c *clang.Context, n *ir.Call) (string, bool) {
	if n.Func == nil || n.Func.Foreign.Path != gtk4rtPkg {
		return "", false
	}
	name := strings.TrimPrefix(n.Func.Foreign.Name, "gtk4rt.")
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	widget := func(i int) string { return "GTK_WIDGET(" + arg(i) + ")" }
	switch name {
	case "ToplevelNew":
		return "sngl_toplevel_new()", true
	case "ToplevelSetTitle":
		return "sngl_toplevel_set_title(" + widget(0) + ", (" + arg(1) + ").ptr)", true
	case "ToplevelSetVisible":
		return "sngl_toplevel_set_visible(" + widget(0) + ", " + arg(1) + ")", true
	case "ToplevelOnClosed", "ToplevelOnVisible":
		h := n.Args[1].Value
		fn, ctx := c.Callback(n.Func.Params[1].Type.Sig, h)
		cast, set := "SnglFn", "sngl_toplevel_on_closed"
		if name == "ToplevelOnVisible" {
			cast, set = "SnglBoolFn", "sngl_toplevel_on_visible"
		}
		return set + "(" + widget(0) + ", (" + cast + ")" + fn + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "ToplevelOnKey":
		e.fail("a window's @key")
		return "(void)0", true
	case "BoxAppend":
		return "gtk_box_append(GTK_BOX(" + arg(0) + "), " + widget(1) + ")", true
	case "AppAttach":
		return "sngl_app_attach(" + widget(0) + ")", true
	case "SetLook":
		return e.setLook(c, widget(0), n.Args[1].Value), true
	}
	e.fail("gtk4rt.%s", name)
	return "(void)0", true
}

// unwritten reports whether h is a handler the program did not write: the
// bodyless function lowering attaches in its place.
func unwritten(h ir.Expr) bool {
	id, ok := h.(*ir.Ident)
	if !ok {
		return false
	}
	fn, ok := id.Sym.(*ir.Func)
	return ok && len(fn.Block) == 0
}

// setLook is gtk4rt.SetLook in C: rt's sngl_set_look given each field of
// the look, which a helper reads once.
func (e *cEmitter) setLook(c *clang.Context, w string, look ir.Expr) string {
	t := c.TypeName(look.ExprType())
	c.Helper("sngl_look_apply", "static void sngl_look_apply(GtkWidget *w, "+t+` l) {
	sngl_set_look(w, l.css.ptr, (int)(l.width + 0.5), (int)(l.height + 0.5), l.grow,
		(int)l.cross, (int)l.textAlign, (int)l.lines, (int)l.wrap, l.hidden, l.cursor.ptr,
		(int)l.justify, l.spaced, (int)l.spacing, l.grid, (int)l.columns);
}
`)
	return "sngl_look_apply(" + w + ", " + c.EvalExpr(look) + ")"
}
