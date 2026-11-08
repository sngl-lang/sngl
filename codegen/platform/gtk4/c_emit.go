package gtk4

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
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

// generateC writes req's program as C: built for `sngl test`, its tests and
// the agent running them in place of the application.
func (g *Generator) generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	if pkg.Run != nil {
		return fmt.Errorf("gtk4: @run is not yet written in C")
	}
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "main.c", Source: req.Source, Platform: platformName}, pkg, platformName)
	fe.RequireImport("<gtk/gtk.h>")
	fe.RequireImport("sngl_gtk.h")
	if agent {
		fe.RequireImport(clang.TestHeader)
	}
	e := &cEmitter{fe: fe, ctx: fe.Context(), pkg: pkg, globals: map[*ir.Var]string{}, nodes: map[string]string{}, invokers: map[string]cInvoker{}}
	e.ctx.State = e.state
	e.ctx.Types = e.types
	e.ctx.Native = e.native
	e.ctx.Closures = e.closure

	// What is built is the package body, or the body of the component a
	// test harness isolated, whose state is then the program's.
	stmts, funcs := pkg.Body, pkg.Funcs
	vars := append(append([]*ir.Var(nil), pkg.Consts...), pkg.Vars...)
	if root := pkg.RootDecl(); root != nil {
		stmts = root.Stmts
		vars = append(vars, root.Vars...)
		funcs = append(append([]*ir.Func(nil), funcs...), root.Funcs...)
	}
	for _, v := range vars {
		e.globals[v] = clang.Decl(&v.DeclInfo)
	}
	// Every body is walked before any is written, so that a widget one
	// function makes is known where another reads it.
	body := codegen.WalkLowered(context.Background(), stmts, e)
	var tests, written []*ir.Func
	for _, fn := range funcs {
		if instanceOwnsFunc(pkg, fn) || e.closureOwner(fn) != nil {
			continue
		}
		if fn.IsTest {
			tests = append(tests, fn)
			continue
		}
		if len(fn.Block) == 0 {
			continue
		}
		// A handler writes its widgets as the body does.
		e.anchor = ""
		fn.Block = codegen.WalkLowered(context.Background(), fn.Block, e)
		written = append(written, fn)
	}
	for _, comp := range pkg.Components {
		if isInstanceComponent(comp) {
			e.record(comp)
		}
	}
	for _, fn := range written {
		fe.Func(fn)
	}
	var build strings.Builder
	build.WriteString("\nstatic void sngl_build(void) {\n")
	for _, v := range vars {
		if v.Init == nil {
			continue
		}
		fmt.Fprintf(&build, "\t%s = %s;\n", e.globals[v], e.ctx.EvalExpr(v.Init))
	}
	for _, l := range e.ctx.EvalBlock(body) {
		build.WriteString("\t" + l + "\n")
	}
	build.WriteString("}\n")
	if e.err != nil {
		return e.err
	}
	var globals strings.Builder
	for _, v := range vars {
		fmt.Fprintf(&globals, "static %s %s;\n", e.ctx.TypeName(v.Type), e.globals[v])
	}
	for _, id := range e.order {
		fmt.Fprintf(&globals, "static %s *%s;\n", e.nodes[id], clang.Global(id))
	}
	for _, a := range e.anchors {
		fmt.Fprintf(&globals, "static GtkWidget *%s;\n", a)
	}
	fe.Prepend("\n" + globals.String())
	io.WriteString(fe, build.String())
	if agent {
		io.WriteString(fe, e.tests(tests))
	} else {
		teardown := ""
		if pkg.Teardown != nil {
			teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
		}
		io.WriteString(fe, "\nint main(void) {\n\tint status = sngl_app_run(sngl_build);\n"+teardown+"\treturn status;\n}\n")
	}
	if e.err != nil {
		return e.err
	}
	links := ir.ReachedCLinks(pkg)
	sources, err := clang.Links(e.ctx, links)
	if err != nil {
		return fmt.Errorf("gtk4: %w", err)
	}
	if err := fe.Close(); err != nil {
		return fmt.Errorf("gtk4: %w", err)
	}
	if err := writeRawFile(sink, clang.BuildFile, clang.Manifest(links, "gtk4")); err != nil {
		return err
	}
	for name, data := range sources {
		if err := writeRawFile(sink, name, data); err != nil {
			return err
		}
	}
	// The runtimes are the same for every program, so a golden holds their
	// digest rather than their text.
	runtime := clang.Runtime()
	if agent {
		maps.Copy(runtime, clang.TestRuntime())
	}
	for name, data := range runtime {
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

// cInvoker is how a test fires an event on a widget: the signal the
// program's handler is connected to, emitted on the widget as GTK would.
type cInvoker struct {
	node, signal ir.Expr
	// notify says the signal is a property's notification, which a test
	// fires only by setting the property first.
	notify bool
}

// tests writes each test as a function of the agent's, and main serving
// them. A test builds the program where it mounts its component, or first
// where it takes none; the program's state and widgets are the file's, so a
// build starts it over.
func (e *cEmitter) tests(fns []*ir.Func) string {
	var b strings.Builder
	var table []string
	for _, fn := range fns {
		name := "sngl_" + fn.Name
		tName, comp := "t", codegen.TestComponentParam(fn)
		for _, p := range fn.Params {
			if p.Name != comp {
				tName = p.Name
				break
			}
		}
		fmt.Fprintf(&b, "\nstatic void %s(sngl_t *%s) {\n", name, clang.Local(tName))
		if comp == "" {
			b.WriteString("\tsngl_build();\n")
		}
		for _, l := range e.testBlock(fn.Block, tName, comp) {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString("}\n")
		table = append(table, fmt.Sprintf("\t{%q, %s},", strings.TrimPrefix(fn.Name, "test"), name))
	}
	b.WriteString("\nstatic const sngl_test sngl_tests[] = {\n" + strings.Join(table, "\n") + "\n};\n")
	b.WriteString("\nint main(void) {\n\tgtk_init();\n\treturn sngl_test_main(sngl_tests, sizeof sngl_tests / sizeof sngl_tests[0]);\n}\n")
	return b.String()
}

// testBlock renders a test's statements: its asserts, its triggers and its
// mount as the agent and the widgets take them, and the rest as the program's
// own, the component it takes read as the program's state.
func (e *cEmitter) testBlock(stmts []ir.Stmt, tName, comp string) []string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, e.testStmt(s, tName, comp)...)
	}
	return lines
}

func (e *cEmitter) testStmt(s ir.Stmt, tName, comp string) []string {
	if _, _, ok := codegen.TestMount(s); ok {
		return []string{"sngl_build();"}
	}
	if cs, ok := s.(*ir.CallStmt); ok {
		if tt, ok := codegen.EventTriggerTarget(cs.Call); ok {
			return e.trigger(tt.ID, cs.Call)
		}
	}
	s = e.componentReads(s, comp)
	switch n := s.(type) {
	case *ir.CallStmt:
		c := n.Call
		switch {
		case ir.IsTestCall(c, ir.TestAssertID), ir.IsTestCall(c, ir.TestMustID):
			cond := e.ctx.EvalExpr(c.Args[1].Value)
			fail, word := "sngl_test_fail", "assert"
			if ir.IsTestCall(c, ir.TestMustID) {
				fail, word = "sngl_test_fail_now", "must"
			}
			return []string{"if (!(" + cond + ")) {", "\t" + fail + "(" + clang.Local(tName) + ", " + clang.StringLiteral(word+" failed: "+cond) + ");", "}"}
		}
		if ir.IsTestMethodCall(c) {
			e.fail("a test's t.%s", c.Func.Name)
			return nil
		}
	case *ir.If:
		lines := []string{"if (" + e.ctx.EvalExpr(n.Cond) + ") {"}
		for _, l := range e.testBlock(n.Body, tName, comp) {
			lines = append(lines, "\t"+l)
		}
		if len(n.Else) > 0 {
			lines = append(lines, "} else {")
			for _, l := range e.testBlock(n.Else, tName, comp) {
				lines = append(lines, "\t"+l)
			}
		}
		return append(lines, "}")
	}
	return e.ctx.EvalStmt(s)
}

// componentReads is s with each read of the component a test takes, `c.n`,
// the program's state or widget it names.
func (e *cEmitter) componentReads(s ir.Stmt, comp string) ir.Stmt {
	if comp == "" {
		return s
	}
	s = ir.CloneStmtsSharingDecls([]ir.Stmt{s})[0]
	_ = ir.Rewrite(s, func(n ir.Node) (ir.Node, error) {
		sel, ok := n.(*ir.Select)
		if !ok {
			return n, nil
		}
		id, ok := sel.Operand.(*ir.Ident)
		if !ok || id.Name != comp {
			return n, nil
		}
		for v := range e.globals {
			if v.Name != sel.Field {
				continue
			}
			ref := &ir.Ident{Name: v.Name, Sym: v, Type: v.Type}
			// State a closure writes is the box NoRef made of it.
			if sd, ok := v.Type.Decl.(*ir.StructDef); ok && v.Type.Kind == ir.TypeStruct && sd.RefBox {
				return &ir.Select{Operand: ref, Field: "value", Type: sd.Fields[0].Type}, ir.SkipDir
			}
			return ref, ir.SkipDir
		}
		return &ir.Ident{Name: sel.Field, Type: sel.Type}, ir.SkipDir
	})
	return s
}

// trigger fires the event c names on the widget id, as GTK does: its signal
// emitted, which reaches the program's handler through the connection the
// program made.
func (e *cEmitter) trigger(id string, c *ir.Call) []string {
	inv, ok := e.invokers[id+"."+c.Event]
	if !ok {
		e.fail("a test's %s.%s(), which nothing connects", id, c.Event)
		return nil
	}
	if inv.notify || len(c.Args) > 0 {
		e.fail("a test's %s.%s() carrying a value", id, c.Event)
		return nil
	}
	return []string{"g_signal_emit_by_name((gpointer)(" + e.ctx.EvalExpr(inv.node) + "), " + e.ctx.EvalExpr(inv.signal) + ");"}
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
	// invokers are how a test fires each event a handler is connected to,
	// by "<id>.<event>" as the test names it.
	invokers map[string]cInvoker
	// anchor is the anchor of the slot the function being walked renders,
	// once it has reset the slot, and anchors every slot's.
	anchor  string
	anchors []string
	// scope is the record being written, where one is.
	scope *cRecord
	// recordClosures are each record's closures (closureFuncs).
	recordClosures map[*ir.Component][]*ir.Func
	seq            int
	err            error
}

func (e *cEmitter) fail(format string, args ...any) []ir.Stmt {
	if e.err == nil {
		e.err = fmt.Errorf("gtk4: "+format+" is not yet written in C", args...)
	}
	return nil
}

// state renders a package var, and a widget the body made, as its static.
func (e *cEmitter) state(n *ir.Ident) (string, bool) {
	if e.scope.holds(n.Name) {
		return selfName + "->" + clang.Local(n.Name), true
	}
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
	if t.Kind != ir.TypeComponent && t.Kind != ir.TypeInstance {
		return "", false
	}
	comp, _ := t.Decl.(*ir.Component)
	if cType := widgetCType(comp); strings.HasPrefix(cType, "Gtk") {
		return cType + " *", true
	}
	if comp := recordOf(t); comp != nil {
		return recordType(comp) + " *", true
	}
	// gtk4.sngl's own primitives and any node else are a GtkWidget.
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
	if r := e.scope; r != nil {
		if _, dup := r.nodes[id]; !dup {
			r.order = append(r.order, id)
		}
		r.nodes[id] = cType
	} else {
		if _, dup := e.nodes[id]; !dup {
			e.order = append(e.order, id)
		}
		e.nodes[id] = cType
	}
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

// OnAppendThrough is the class's append, or where a slot's render puts a
// child in the box it renders into, a place before the slot's anchor.
func (e *cEmitter) OnAppendThrough(ctx context.Context, parent, child, call ir.Expr) []ir.Stmt {
	if id, ok := child.(*ir.Ident); ok && e.scope != nil {
		e.scope.appended[id.Name] = true
	}
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" && e.anchor != "" {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("InsertBefore", ir.TypVoid, parent, child, anchorName(e.anchor))}}
	}
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
	if id, ok := node.(*ir.Ident); ok {
		if i := slices.IndexFunc(c.Func.Params, func(p *ir.Param) bool { return p.Name == "signal" }); i >= 0 && i < len(c.Args) {
			e.invokers[id.Name+"."+codegen.TriggerEventName(handler, event)] = cInvoker{
				node:   node,
				signal: c.Args[i].Value,
				notify: strings.HasPrefix(event, "notify"),
			}
		}
	}
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

// OnSlotReset starts a slot's render over: its list of widgets emptied, and
// its anchor made in the box it renders into where it is not there already.
func (e *cEmitter) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	e.anchor = clang.Global(slot.Name) + "__anchor"
	if !slices.Contains(e.anchors, e.anchor) {
		e.anchors = append(e.anchors, e.anchor)
	}
	ref := &ir.Ident{Name: slot.Name, Sym: slot, Type: slot.Type}
	return []ir.Stmt{
		&ir.Assign{Target: ref, Op: ast.AssignSet, Value: &ir.ListLit{Type: slot.Type}},
		&ir.CallStmt{Call: rtCall("SlotAnchor", ir.TypVoid, &ir.Ident{Name: "parent", Type: ir.NativePointerOf("GtkWidget")}, anchorName(e.anchor))},
	}
}

// OnSlotAppend records a widget the slot rendered, which its next render
// removes.
func (e *cEmitter) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	ref := &ir.Ident{Name: slot.Name, Sym: slot, Type: slot.Type}
	push := &ir.Func{Name: "push", Intrinsic: "list.push"}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: push, Type: ir.TypVoid, Args: []ir.CallArg{
		{Value: ref},
		{Value: &ir.Conversion{Type: slot.Type.Elems[0], Operand: child}},
	}}}}
}

func (e *cEmitter) OnIter(ctx context.Context, iter ir.Expr) ir.Expr { return iter }
func (e *cEmitter) OnCond(ctx context.Context, cond ir.Expr) ir.Expr { return cond }
func (e *cEmitter) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}

// cNatives are the gtk4rt calls a cgo build still makes, as rt's C writes
// each: its name, or what it writes in its place given its arguments.
func (e *cEmitter) native(c *clang.Context, n *ir.Call) (string, bool) {
	if n.Func == nil {
		return "", false
	}
	if n.Func.Foreign.Path == recordPath {
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = c.EvalExpr(a.Value)
		}
		return n.Func.Name + "(" + strings.Join(args, ", ") + ")", true
	}
	// A function of the record being written is handed it.
	if r := e.scope; r != nil && slices.Contains(r.comp.Funcs, n.Func) {
		args := []string{selfName}
		for _, a := range n.Args {
			args = append(args, c.EvalExpr(a.Value))
		}
		return recordFunc(r.comp, n.Func.Name) + "(" + strings.Join(args, ", ") + ")", true
	}
	if n.Func.Foreign.Path != gtk4rtPkg {
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
	case "BoxRemove":
		return "gtk_box_remove(GTK_BOX(" + arg(0) + "), " + widget(1) + ")", true
	case "SlotAnchor":
		a := anchorOf(n.Args[1].Value)
		return a + " = sngl_slot_anchor(" + widget(0) + ", " + a + ")", true
	case "InsertBefore":
		return "sngl_box_insert_before(" + widget(0) + ", " + anchorOf(n.Args[2].Value) + ", " + widget(1) + ")", true
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

// anchorName carries a slot's anchor, the static it is, in the IR a walk
// writes, which is rendered after every walk.
func anchorName(name string) ir.Expr { return &ir.Literal{Type: ir.TypString, Value: name} }

func anchorOf(e ir.Expr) string {
	lit, _ := e.(*ir.Literal)
	return lit.Value
}
