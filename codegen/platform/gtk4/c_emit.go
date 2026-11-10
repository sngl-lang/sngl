package gtk4

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
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
var rtFiles = []string{"sngl_gtk.h", "sngl_gtk.c", "look.c", "scroll.c", "keys.c", "choose.c", "tabs.c", "menu.c", "image.c"}

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
		fe.RequireImport("<string.h>")
	}
	reg, err := g.gir()
	if err != nil {
		return err
	}
	e := &cEmitter{fe: fe, ctx: fe.Context(), pkg: pkg, reg: reg, globals: map[*ir.Var]string{}, nodes: map[string]string{}, appended: map[string]bool{}, invokers: map[string]cInvoker{}, images: map[string]string{}}
	e.ctx.State = e.state
	e.ctx.Types = e.types
	e.ctx.Native = e.native
	e.ctx.Closures = e.closure
	e.canvases(pkg)
	e.markup = collectMarkup(pkg)

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
	e.bodyMade = len(e.order)
	var tests, written []*ir.Func
	for _, fn := range funcs {
		if instanceOwnsFunc(pkg, fn) || e.closureOwner(fn) != nil {
			continue
		}
		if fn.IsTest {
			tests = append(tests, fn)
			continue
		}
		if !hasBody(fn) {
			continue
		}
		// A handler writes its widgets as the body does.
		e.anchor = ""
		fn.Block = codegen.WalkLowered(context.Background(), fn.Block, e)
		written = append(written, fn)
	}
	// A closure whose function no declaration holds -- a lambda lifted in
	// a component the inliner spliced away -- is the file's.
	known := map[*ir.Func]bool{}
	for _, fn := range funcs {
		known[fn] = true
	}
	var stray func(root any)
	stray = func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			cl, ok := n.(*ir.Closure)
			if !ok || cl.Func == nil || known[cl.Func] || clang.Bodiless(cl.Func) || e.closureOwner(cl.Func) != nil {
				return nil
			}
			known[cl.Func] = true
			e.anchor = ""
			cl.Func.Block = codegen.WalkLowered(context.Background(), cl.Func.Block, e)
			written = append(written, cl.Func)
			stray(cl.Func.Block)
			return nil
		})
	}
	stray(body)
	for _, fn := range slices.Clone(written) {
		stray(fn.Block)
	}
	for _, comp := range pkg.Components {
		if isInstanceComponent(comp) {
			e.record(comp)
		}
	}
	for _, fn := range written {
		fe.Func(fn)
	}
	e.draws(nil)
	// The program's state is set apart from the tree its build makes, since
	// a test writes state before it mounts, as Go's does between making the
	// Model and building it.
	var build strings.Builder
	build.WriteString("\nstatic void sngl_init(void) {\n")
	// A test builds the program afresh, whose copies the last one's were
	// not.
	for _, id := range e.copies {
		fmt.Fprintf(&build, "\tif (%s) {\n\t\tg_hash_table_remove_all(%s);\n\t}\n", copiesName(id), copiesName(id))
	}
	for _, v := range vars {
		if v.Init == nil {
			continue
		}
		// A null is the zero of the var's type, which is a struct for an
		// option, a list, a map and a function.
		value := e.ctx.Keep(v.Type, e.ctx.EvalExpr(v.Init))
		if ir.IsNullLiteral(v.Init) {
			value = "(" + e.ctx.TypeName(v.Type) + "){0}"
		}
		fmt.Fprintf(&build, "\t%s = %s;\n", e.globals[v], value)
	}
	build.WriteString("}\n\nstatic void sngl_build(void) {\n")
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
		fmt.Fprintf(&globals, "static %s%s;\n", e.nodes[id], clang.Global(id))
	}
	for _, a := range e.anchors {
		fmt.Fprintf(&globals, "static GtkWidget *%s;\n", a)
	}
	for _, id := range e.copies {
		fmt.Fprintf(&globals, "static GHashTable *%s;\n", copiesName(id))
	}
	fe.Prepend("\n" + globals.String())
	io.WriteString(fe, build.String())
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
	}
	if agent {
		io.WriteString(fe, e.testRoot())
		io.WriteString(fe, e.tests(tests, teardown))
	} else {
		io.WriteString(fe, "\nstatic void sngl_start(void) {\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n}\n")
		io.WriteString(fe, "\nint main(void) {\n\tint status = sngl_app_run(sngl_start);\n"+teardown+"\treturn status;\n}\n")
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
	assets, err := cAssets(req.FileAssets)
	if err != nil {
		return err
	}
	if err := writeRawFile(sink, "sngl_assets.c", assets); err != nil {
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
	// cType is the widget's class, which says what a value the test passes
	// is written into before the signal fires.
	cType string
	// notify is the property a notification signal reports, which a test
	// fires with g_object_notify: emitting one wants the GParamSpec it
	// carries.
	notify string
	// fire is the rt function a test fires an event no signal of the
	// widget's reports through, handed the widget: a canvas's click, which a
	// gesture reports (canvasClick), and a scroll's end arriving, which rt
	// reads off its adjustments.
	fire string
}

// rtFires are the rt functions a test fires an event with that a gtk4rt
// native connects rather than a signal, by the native.
var rtFires = map[string]string{
	"gtk4rt.ScrollOnReachEnd":  "sngl_scroll_reach_end",
	"gtk4rt.ToplevelOnKey":     "sngl_toplevel_key",
	"gtk4rt.TabPageOnShown":    "sngl_tab_page_show",
	"gtk4rt.MenuEntryOnSelect": "sngl_menu_entry_select",
	"gtk4rt.BackOnPress":       "sngl_back_press",
	"gtk4rt.ImageFrameOnClick": "sngl_image_click",
}

// tests writes each test as a function of the agent's, and main serving
// them. A test builds the program where it mounts its component, or first
// where it takes none. The program's state and widgets are the file's, so
// each test starts by tearing down the one before -- what it armed, a
// timer, would otherwise run into this one's state -- and setting the state
// afresh.
func (e *cEmitter) tests(fns []*ir.Func, teardown string) string {
	var b strings.Builder
	b.WriteString("\nstatic void sngl_reset(void) {\n\tstatic bool run;\n\tif (run) {\n\t" + teardown + "\t}\n\trun = true;\n\tsngl_init();\n}\n")
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
		// A test is a turn; one a failure leaves, the agent ends.
		b.WriteString("\tsngl_turn_enter();\n\tsngl_reset();\n")
		if comp == "" {
			b.WriteString("\tsngl_build();\n")
		}
		for _, l := range e.testBlock(fn.Block, tName, comp) {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString("\tsngl_turn_leave();\n}\n")
		table = append(table, fmt.Sprintf("\t{%q, %s},", strings.TrimPrefix(fn.Name, "test"), name))
	}
	b.WriteString("\nstatic const sngl_test sngl_tests[] = {\n" + strings.Join(table, "\n") + "\n};\n")
	b.WriteString(cTestMain)
	return b.String()
}

// cTestMain is the test agent's main. A critical GLib logs is a program
// misusing GTK -- a widget appended that is not one, a pointer gone stale --
// which GTK survives and a test reading the program's own state would not
// notice, so it fails the test running.
const cTestMain = `
static GLogWriterOutput sngl_test_log_writer(GLogLevelFlags level, const GLogField *fields, gsize n, gpointer data) {
	if (level & G_LOG_LEVEL_CRITICAL) {
		for (gsize i = 0; i < n; i++) {
			if (strcmp(fields[i].key, "MESSAGE") == 0 && fields[i].length < 0) {
				sngl_test_fault(fields[i].value);
			}
		}
	}
	return g_log_writer_default(level, fields, n, data);
}

// A snapshot is the window Go's agent draws: what the test shows, in a
// window of 800 by 600.
static bool sngl_test_draw(unsigned char **rgba, int *width, int *height, const char **why) {
	return sngl_capture(sngl_test_root(), 800, 600, rgba, width, height, why);
}

int main(void) {
	g_log_set_writer_func(sngl_test_log_writer, NULL, NULL);
	gtk_init();
	sngl_test_capture = sngl_test_draw;
	return sngl_test_main(sngl_tests, sizeof sngl_tests / sizeof sngl_tests[0]);
}
`

// testRoot writes sngl_test_root, the widget a snapshot shows: in a build
// rooted at a component, the one its body makes and attaches nowhere; in a
// build of the program, none, which shows the window it attached last.
func (e *cEmitter) testRoot() string {
	var tops []string
	if e.pkg.RootDecl() != nil {
		for _, id := range e.order[:e.bodyMade] {
			if !e.appended[id] && !strings.HasSuffix(e.nodes[id], "__inst *") {
				tops = append(tops, id)
			}
		}
	}
	root := "NULL"
	if len(tops) == 1 {
		root = "GTK_WIDGET(" + clang.Global(tops[0]) + ")"
	}
	return "\nstatic GtkWidget *sngl_test_root(void) {\n\treturn " + root + ";\n}\n"
}

// testBlock renders a test's statements as the program's own, the component
// it takes read as the program's state. What only a test does -- an assert, a
// wait, a trigger, its mount -- is a call the native hook answers (testCall)
// wherever the test writes it, a loop's body as well as its own.
func (e *cEmitter) testBlock(stmts []ir.Stmt, tName, comp string) []string {
	e.testT = tName
	defer func() { e.testT = "" }()
	stmts = clang.SetContexts(stmts, func(name string) *ir.Var {
		for v := range e.globals {
			if v.Name == name {
				return v
			}
		}
		return nil
	})
	var lines []string
	for _, s := range stmts {
		lines = append(lines, e.ctx.EvalStmt(e.componentReads(s, comp))...)
	}
	return lines
}

// testCall is n as the test being written does it, or false where n is no
// call only a test makes.
func (e *cEmitter) testCall(ctx *clang.Context, n *ir.Call) (string, bool) {
	if e.testT == "" {
		return "", false
	}
	if ir.IsTestCall(n, ir.TestMountID) {
		return "sngl_build()", true
	}
	if tt, ok := codegen.EventTriggerTarget(n); ok {
		return e.trigger(ctx, tt, n), true
	}
	switch {
	case ir.IsTestCall(n, ir.TestAssertID), ir.IsTestCall(n, ir.TestMustID):
		cond := ctx.EvalExpr(n.Args[1].Value)
		fail, word := "sngl_test_fail", "assert"
		if ir.IsTestCall(n, ir.TestMustID) {
			fail, word = "sngl_test_fail_now", "must"
		}
		return "((" + cond + ") ? (void)0 : " + fail + "(" + clang.Local(e.testT) + ", " + clang.StringLiteral(word+" failed: "+cond) + "))", true
	case ir.IsTestCall(n, ir.TestSnapshotID):
		return "sngl_test_snapshot(" + clang.Local(e.testT) + ", " + ctx.EvalExpr(n.Args[1].Value) + ")", true
	case ir.IsTestCall(n, ir.TestWaitID):
		// The test runs on the loop's own thread, so it runs the loop while
		// it waits, as gtk4rt.PumpFor does for Go.
		pred := n.Args[1].Value
		fnT := ctx.TypeName(pred.ExprType())
		name := fnT + "_wait"
		ctx.Helper(name, "static void "+name+"("+fnT+" p, int64_t ms) {\n\tgint64 end = g_get_monotonic_time() + (gint64)ms * 1000;\n\twhile (!p.fn(p.env) && g_get_monotonic_time() < end) {\n\t\tif (!g_main_context_iteration(NULL, FALSE)) {\n\t\t\tg_usleep(1000);\n\t\t}\n\t}\n}\n")
		return name + "(" + ctx.EvalExpr(pred) + ", " + ctx.EvalExpr(n.Args[2].Value) + ")", true
	}
	if ir.IsTestMethodCall(n) {
		e.fail("a test's t.%s", n.Func.Name)
		return "(void)0", true
	}
	return "", false
}

// componentReads is s with each read of the component a test takes, `c.n`,
// the program's state or widget it names.
func (e *cEmitter) componentReads(s ir.Stmt, comp string) ir.Stmt {
	if comp == "" {
		return s
	}
	s = ir.CloneStmtsSharingDecls([]ir.Stmt{s})[0]
	_ = ir.Rewrite(s, func(n ir.Node) (ir.Node, error) {
		// A trigger names its widget through the component, which is how
		// testCall finds the invoker.
		if call, ok := n.(*ir.Call); ok {
			if _, ok := codegen.EventTriggerTarget(call); ok {
				return n, ir.SkipDir
			}
		}
		// The component itself, handed a method lowering wrote for it as
		// its `this`: its state is the file's, so the method reads none of
		// it through the receiver.
		if id, ok := n.(*ir.Ident); ok && id.Name == comp {
			if _, param := id.Sym.(*ir.Param); param && e.recordOf(id.Type) == nil {
				return &ir.Literal{Type: ir.TypNull, Value: "null"}, ir.SkipDir
			}
		}
		// One copy of a node loops rendered, `c.b[0][1]`: the widget its
		// key holds, null where none was rendered there.
		if ix, ok := n.(*ir.Index); ok {
			if id, path := e.copyRead(ix, comp); id != "" {
				return rtCall("CopyGet", ix.Type, &ir.Literal{Type: ir.TypString, Value: id}, ir.CopyKey(path)), ir.SkipDir
			}
		}
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
		if _, ok := e.nodes[sel.Field]; ok {
			return &ir.Ident{Name: sel.Field, Type: sel.Type}, ir.SkipDir
		}
		// A copy's state, or a component's built at run time, is its
		// record's, which a test reaches through no name of the file's.
		e.fail("a test reading %s.%s, which the program holds in no var or widget of its own", comp, sel.Field)
		return n, ir.SkipDir
	})
	e.unreached(s, comp)
	return s
}

// copyRead is the node and the path of indices ix reads one copy of off the
// component comp, or "" where ix reads none.
func (e *cEmitter) copyRead(ix *ir.Index, comp string) (string, []ir.Expr) {
	var path []ir.Expr
	var x ir.Expr = ix
	for {
		in, ok := x.(*ir.Index)
		if !ok {
			break
		}
		path = append([]ir.Expr{in.Idx}, path...)
		x = in.Operand
	}
	sel, ok := x.(*ir.Select)
	if !ok {
		return "", nil
	}
	if id, ok := sel.Operand.(*ir.Ident); !ok || id.Name != comp || !slices.Contains(e.copies, sel.Field) {
		return "", nil
	}
	return sel.Field, path
}

// unreached refuses what a test reaches other than through the component it
// takes: an alias of it, a helper handed it, a ref it read off it, each of
// which C would name as a value it never declared.
func (e *cEmitter) unreached(s ir.Stmt, comp string) {
	_ = ir.Walk(s, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			if tt, ok := codegen.EventTriggerTarget(x); ok {
				if tt.Recv != comp {
					e.fail("a test's trigger through %s", tt.Recv)
				}
				return ir.SkipDir
			}
			if ir.IsTestCall(x, ir.TestMountID) || ir.IsTestMethodCall(x) {
				return nil
			}
			for _, a := range x.Args {
				// A widget the test read off the component is the file's.
				if id, ok := a.Value.(*ir.Ident); ok {
					if _, held := e.nodes[id.Name]; held {
						continue
					}
				}
				if t := a.Value.ExprType(); t != nil && (t.Kind == ir.TypeComponent || t.Kind == ir.TypeInstance) {
					e.fail("a test handing %s a component", x.Func.Name)
				}
			}
		case *ir.Select:
			if t := x.Operand.ExprType(); t != nil && (t.Kind == ir.TypeComponent || t.Kind == ir.TypeInstance) {
				e.fail("a test reading %s off a component other than the one it takes", x.Field)
			}
		case *ir.LocalVar:
			if t := x.Type; t != nil && (t.Kind == ir.TypeComponent || t.Kind == ir.TypeInstance) {
				e.fail("a test holding a component in %s", x.Name)
			}
		}
		return nil
	})
}

// trigger fires the event c names on the widget id, as GTK does: its signal
// emitted, which reaches the program's handler through the connection the
// program made.
func (e *cEmitter) trigger(ctx *clang.Context, tt codegen.EventTrigger, c *ir.Call) string {
	id := tt.ID
	inv, ok := e.invokers[id+"."+c.Event]
	if !ok {
		e.fail("a test's %s.%s(), which nothing connects", id, c.Event)
		return "(void)0"
	}
	w := ctx.EvalExpr(inv.node)
	// One copy of a node loops render is the widget its key holds, and a
	// test reaching one not rendered fails.
	if key := tt.Key(); key != nil {
		cType := e.widgetType(id)
		if cType == "" {
			cType = "GtkWidget"
		}
		at := "sngl_copy_at"
		ctx.Helper(at, "static GtkWidget *"+at+"(sngl_t *t, GHashTable *copies, sngl_string key) {\n\tGtkWidget *w = sngl_copy_get(copies, key.ptr);\n\tif (w == NULL) {\n\t\tsngl_test_fail_now(t, sngl_concat(SNGL_STR(\"no copy rendered at \"), key));\n\t}\n\treturn w;\n}\n")
		w = "((" + cType + " *)" + at + "(" + clang.Local(e.testT) + ", " + copiesName(id) + ", " + ctx.EvalExpr(key) + "))"
	}
	if inv.fire != "" && len(c.Args) == 0 {
		return inv.fire + "(GTK_WIDGET(" + w + "))"
	}
	if inv.fire == "sngl_toplevel_key" && len(c.Args) == 2 {
		// A window's key is the press, field by field, and the stop its
		// handler calls: a function of the file's takes the press apart.
		press, stop := c.Args[0].Value, c.Args[1].Value
		fn, env := ctx.Callback(stop.ExprType().Sig, stop)
		pt := ctx.TypeName(press.ExprType())
		name := pt + "_press"
		ctx.Helper(name, "static void "+name+"(GtkWidget *w, "+pt+" p, SnglFn stop, void *ctx) {\n\tsngl_toplevel_key(w, p.key.ptr, p.ctrl, p.alt, p.shift, p.meta, p.editing, stop, ctx, (GDestroyNotify)sngl_release);\n}\n")
		return name + "(GTK_WIDGET(" + w + "), " + ctx.EvalExpr(press) + ", (SnglFn)" + fn + ", " + env + ")"
	}
	signal, prop := ctx.EvalExpr(inv.signal), "NULL"
	fire := "g_signal_emit_by_name((gpointer)(" + w + "), " + signal + ")"
	if inv.notify != "" {
		prop = strconv.Quote(inv.notify)
		fire = "g_object_notify(G_OBJECT(" + w + "), " + prop + ")"
	}
	if len(c.Args) == 0 {
		return fire
	}
	v := ctx.EvalExpr(c.Args[0].Value)
	switch {
	case len(c.Args) > 1:
	case inv.cType == "GtkCheckButton" || inv.cType == "GtkSwitch":
		// A flip is the state written, which fires the signal; state the
		// widget already holds fires nothing, so the test fires it itself.
		return "sngl_flip(GTK_WIDGET(" + w + "), " + v + ", " + signal + ", " + prop + ")"
	case inv.cType == "GtkEntry":
		// An entry's events carry its text, which the handler reads back off
		// the widget: the test puts it there first.
		return "(sngl_editable_set_text_quiet(GTK_WIDGET(" + w + "), (" + v + ").ptr), " + fire + ")"
	case inv.cType == "GtkCalendar":
		// A day is selected unreported, as the user's click selects it, and
		// the test fires the selection; none selects nothing.
		day := v
		if t := c.Args[0].Value.ExprType(); t != nil && t.Kind == ir.TypeOption {
			return "((" + v + ").ok ? sngl_calendar_select_day(GTK_CALENDAR(" + w + "), (" + v + ").value.ptr) : (void)0, " + fire + ")"
		}
		return "(sngl_calendar_select_day(GTK_CALENDAR(" + w + "), (" + day + ").ptr), " + fire + ")"
	case inv.cType == "GtkDropDown":
		// The choice labeled with the value, selected as a click does, which
		// reports itself.
		return "sngl_drop_down_select(GTK_DROP_DOWN(" + w + "), (" + v + ").ptr)"
	}
	e.fail("a test's %s.%s() carrying a value", id, c.Event)
	return "(void)0"
}

// widgetType is the class of the widget id names, in the record being
// written or the file.
func (e *cEmitter) widgetType(id string) string {
	cType := e.nodes[id]
	if r := e.scope; r != nil {
		if t, held := r.nodes[id]; held {
			cType = t
		}
	}
	return strings.TrimSuffix(cType, " *")
}

// cEmitter translates the lowered tree's node operations into the C calls
// that build it.
type cEmitter struct {
	fe  *clang.FileEmitter
	ctx *clang.Context
	pkg *ir.Package
	reg *gir.TypeRegistry
	// globals are the package's vars, by the static each is.
	globals map[*ir.Var]string
	// nodes are the C type of each widget the body makes, by its id, and
	// order the ids in the order made.
	nodes map[string]string
	order []string
	// appended are the widgets of the file's something is attached to,
	// and bodyMade how many of order the body made, which a build rooted at
	// a component holds its root among (testRoot).
	appended map[string]bool
	bodyMade int
	// invokers are how a test fires each event a handler is connected to,
	// by "<id>.<event>" as the test names it.
	invokers map[string]cInvoker
	// anchor is the anchor of the slot the function being walked renders,
	// once it has reset the slot, and anchors every slot's.
	anchor string
	// forget is the call the slot render being walked starts with that lets
	// go of each widget of the file's it makes, and of each copy: what the
	// render no longer builds is not rendered, though GTK may hold it a
	// while yet.
	forget  *ir.Call
	anchors []string
	// scope is the record being written, where one is.
	scope *cRecord
	// recordClosures are each record's closures (closureFuncs).
	recordClosures map[*ir.Component][]*ir.Func
	// canvasByID and canvasByNode are the program's drawings (canvases).
	canvasByID   map[string]*codegen.Canvas
	canvasByNode map[*ir.NodeInst]*codegen.Canvas
	// images are the tag of each image primitive the body makes, by its id.
	images map[string]string
	// markup is the program's flows and the spans in each, whose words a
	// flow's label is set to as Pango markup.
	markup *markupTrees
	// copies are the ids of the nodes loops render that a test reaches one
	// copy of, each held by its key (copiesName).
	copies []string
	// testT is the name of the test's sngl_t while a test is written
	// (testBlock), and "" otherwise.
	testT string
	seq   int
	err   error
}

func (e *cEmitter) fail(format string, args ...any) []ir.Stmt {
	if e.err == nil {
		e.err = fmt.Errorf("gtk4: "+format+" is not yet written in C", args...)
	}
	return nil
}

// state renders a package var and a record's, by the declaration, and a
// widget or record the code holds, by its name where no local takes it.
func (e *cEmitter) state(n *ir.Ident, local bool) (string, bool) {
	// The application, as a slot at the package's root takes its parent:
	// the runtime's box calls answer for it, as gtk4rt's do for App.
	if ir.IsAppParent(n) {
		return "SNGL_APP", true
	}
	// A component handed as a method's `this` is the record being written.
	if comp, ok := n.Sym.(*ir.Component); ok && e.scope != nil && e.scope.comp == comp {
		return selfName, true
	}
	if v, ok := n.Sym.(*ir.Var); ok {
		if s, ok := e.globals[v]; ok {
			return s, true
		}
		if r := e.scope; r != nil && slices.Contains(r.comp.Vars, v) {
			return selfName + "->" + clang.Local(n.Name), true
		}
	}
	if local {
		return "", false
	}
	if r := e.scope; r != nil && r.holds(n.Name) {
		return selfName + "->" + clang.Local(n.Name), true
	}
	if _, ok := e.nodes[n.Name]; ok {
		return clang.Global(n.Name), true
	}
	return "", false
}

// types names a widget as the GObject pointer it is: its class's where it is
// one of GIR's, and GtkWidget's for any node else.
func (e *cEmitter) types(t *ir.Type) (string, bool) {
	// A timer's schedule, which rt's sngl_every arms, and a picture.
	if sd, ok := t.Decl.(*ir.StructDef); ok && t.Kind == ir.TypeStruct && sd.Foreign.Path == gtk4rtPkg {
		switch sd.Foreign.Name {
		case "*gtk4rt.Schedule":
			return "SnglSchedule *", true
		case "*gtk4rt.Picture":
			return "SnglPicture *", true
		}
	}
	// A component's family parameter nothing bound, as a timer in a loop
	// leaves it: the node its body renders, which is none.
	if t.Kind == ir.TypeTypeParam {
		for _, comp := range e.pkg.Components {
			if comp.FamilyParam != nil && comp.FamilyParam.ParamName == t.ParamName {
				return "GtkWidget *", true
			}
		}
		return "", false
	}
	if t.Kind != ir.TypeComponent && t.Kind != ir.TypeInstance {
		return "", false
	}
	comp, _ := t.Decl.(*ir.Component)
	if cType := widgetCType(comp); strings.HasPrefix(cType, "Gtk") {
		return cType + " *", true
	}
	if comp := e.recordOf(t); comp != nil {
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
	e.hold(id, cType+" *")
	v := &ir.Var{Name: id, Type: ir.NativePointerOf(cType)}
	ref := &ir.Ident{Name: id, Sym: v, Type: v.Type}
	out := []ir.Stmt{&ir.Assign{
		Target: ref,
		Op:     ast.AssignSet,
		Value:  &ir.Conversion{Type: v.Type, Operand: made},
	}}
	// A widget the file holds reads NULL once a branch took it away and
	// GTK let it go, which is what a test's null test asks.
	if e.scope == nil {
		out = []ir.Stmt{&ir.CallStmt{Call: rtCall("Hold", ir.TypVoid, ref, &ir.Conversion{Type: v.Type, Operand: made})}}
		if e.anchor != "" && e.forget != nil {
			e.forget.Args = append(e.forget.Args, ir.CallArg{Value: ref})
		}
	}
	if imagePrimitive(tag) {
		e.images[id] = tag
	}
	if tag == flowTag {
		out = append(out, e.flowMarkup(id, ref)...)
	}
	// A drawn source's picture, painted once as it is made: its shapes are
	// a canvas's drawing, which the file's draw function paints.
	if cv := e.canvasByID[id]; tag == imageDrawingTag && cv != nil {
		out = append(out, &ir.CallStmt{Call: rtCall("ImagePaint", ir.TypVoid, ref, &ir.Literal{Type: ir.TypString, Value: id})})
	}
	return out
}

// imagePaint is the native hook's ImagePaint: the drawn source the literal
// names painted by its drawing's function, at the drawing's size.
func (e *cEmitter) imagePaint(id ir.Expr, w string) string {
	lit, _ := id.(*ir.Literal)
	if lit == nil || e.canvasByID[lit.Value] == nil {
		e.fail("painting a drawing no canvas is found for")
		return "(void)0"
	}
	cv := e.canvasByID[lit.Value]
	data := "NULL"
	if canvasOwner(cv) != nil {
		data = selfName
	}
	return fmt.Sprintf("sngl_image_paint(GTK_WIDGET(%s), %d, %d, %s, %s)", w, cv.Width, cv.Height, drawName(cv), data)
}

// hold makes id, a widget or a record the code builds, something the
// record being written holds, or the file where none is, as the C type
// cType: what a function after the one that made it reads.
func (e *cEmitter) hold(id, cType string) {
	if r := e.scope; r != nil {
		if _, dup := r.nodes[id]; !dup {
			r.order = append(r.order, id)
		}
		r.nodes[id] = cType
		return
	}
	if _, dup := e.nodes[id]; !dup {
		e.order = append(e.order, id)
	}
	e.nodes[id] = cType
}

// registryClass reports whether tag names one of GIR's classes.
func (e *cEmitter) registryClass(tag string) (string, bool) {
	return tag, strings.HasPrefix(tag, "Gtk")
}

func (e *cEmitter) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	if tag == "canvas" {
		return e.canvasCreate(id)
	}
	// A span is no widget: its words are its flow's markup.
	if tag == spanTag {
		return nil
	}
	return e.fail("a %s made without a declared create", tag)
}

func (e *cEmitter) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	// A span's prop is read off the tree into its flow's markup, which is
	// set again from the same expression: the whole of a flow's reactivity.
	if flow := e.markup.ownerOf[codegen.IdentBareName(node)]; flow != "" {
		return e.flowMarkup(flow, &ir.Ident{Name: flow, IsElementRef: true, Type: ir.NativePointerOf("GtkLabel")})
	}
	if _, ok := e.markup.spans[codegen.IdentBareName(node)]; ok {
		return nil
	}
	if prop == lookPropName {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("SetLook", ir.TypVoid, node, value)}}
	}
	// A canvas's own props, which its drawing was sized and scaled by when
	// canvasCreate made it.
	if e.canvasByID[codegen.IdentBareName(node)] != nil && setterOf(node, prop) == nil {
		return nil
	}
	// The key of a copy loops rendered, which the node is held by for a
	// test reaching that copy.
	if prop == ir.CopyProp {
		id := codegen.IdentBareName(node)
		if !slices.Contains(e.copies, id) {
			e.copies = append(e.copies, id)
		}
		if e.scope != nil && !slices.Contains(e.scope.copies, id) {
			e.scope.copies = append(e.scope.copies, id)
		}
		// The render builds every copy again, so the table of them starts
		// over with it.
		if e.scope == nil && e.anchor != "" && e.forget != nil && !e.forgets(id) {
			e.forget.Args = append(e.forget.Args, ir.CallArg{Value: &ir.Literal{Type: ir.TypString, Value: id}})
		}
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("CopyPut", ir.TypVoid, &ir.Literal{Type: ir.TypString, Value: id}, value, node)}}
	}
	// What a source says it shows rather than what it shows, and a
	// drawing's size, which its paint read off the drawing.
	switch e.images[codegen.IdentBareName(node)] {
	case imageURLTag:
		if prop == ir.ImageSourceURL {
			return nil
		}
	case imageDrawingTag:
		return nil
	}
	call, ok := value.(*ir.Call)
	if !ok {
		return e.fail("setting %s from %T", prop, value)
	}
	return []ir.Stmt{&ir.CallStmt{Call: call}}
}

// markAppended notes child attached in the record being written, so it is
// not a candidate for its root: a component instance by its own id, since
// what is attached is its root's binding (ir.InstanceRootSuffix).
func (e *cEmitter) markAppended(child ir.Expr) {
	id, ok := child.(*ir.Ident)
	if !ok {
		return
	}
	appended := e.appended
	if e.scope != nil {
		appended = e.scope.appended
	}
	appended[id.Name] = true
	appended[strings.TrimSuffix(id.Name, ir.InstanceRootSuffix)] = true
}

func (e *cEmitter) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if _, ok := e.markup.spans[codegen.IdentBareName(child)]; ok {
		return nil
	}
	if ir.IsAppParent(parent) {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("AppAttach", ir.TypVoid, child)}}
	}
	e.markAppended(child)
	// A slot's entry into the box it renders into, as OnRemoveChild takes
	// it out.
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" && e.anchor != "" {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("InsertBefore", ir.TypVoid, parent, child, anchorName(e.anchor))}}
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("BoxAppend", ir.TypVoid, parent, child)}}
}

// OnAppendThrough is the class's append, or where a slot's render puts a
// child in the box it renders into, a place before the slot's anchor.
func (e *cEmitter) OnAppendThrough(ctx context.Context, parent, child, call ir.Expr) []ir.Stmt {
	if _, ok := e.markup.spans[codegen.IdentBareName(child)]; ok {
		return nil
	}
	e.markAppended(child)
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
	id, _ := node.(*ir.Ident)
	// A gtk4rt native connects what no signal reports -- a scroll's end
	// arriving -- on a widget of its own, whatever class the node is held as.
	viaRt := false
	if c, ok := call.(*ir.Call); ok && c.Func != nil {
		viaRt = c.Func.Foreign.Path == gtk4rtPkg
	}
	if id != nil && !viaRt && !e.connects(id.Name, event) {
		// An event the node's class has no signal for -- a component's own,
		// on the box its instance renders as -- reaches its handler as a
		// call, and nothing connects.
		return nil
	}
	// A signal hands its handler the widget it fired on; a native hands it
	// the values it declares, a window's visibility.
	if id != nil && !viaRt {
		handler = handlerOn(handler, node)
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
	if id != nil {
		if i := slices.IndexFunc(c.Func.Params, func(p *ir.Param) bool { return p.Name == "signal" }); i >= 0 && i < len(c.Args) {
			cType := e.widgetType(id.Name)
			prop, _ := strings.CutPrefix(notifySignal(cType, event), "notify::")
			e.invokers[id.Name+"."+codegen.TriggerEventName(handler, event)] = cInvoker{
				node:   node,
				signal: c.Args[i].Value,
				cType:  cType,
				notify: prop,
			}
		} else if fire := rtFires[c.Func.Foreign.Name]; fire != "" {
			e.invokers[id.Name+"."+codegen.TriggerEventName(handler, event)] = cInvoker{node: node, fire: fire}
		}
	}
	return []ir.Stmt{
		&ir.LocalVar{Name: name, Type: v.Type, Init: handler, Sym: v},
		&ir.CallStmt{Call: c},
	}
}

func (e *cEmitter) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	if e.canvasByID[codegen.IdentBareName(node)] != nil && event == "click" {
		return e.canvasClick(node, handler)
	}
	return e.fail("@%s without its class's connect", event)
}

func (e *cEmitter) OnDetachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	return e.fail("taking @%s off", event)
}

// OnRemoveChild takes a slot's entry out of the box it rendered into, which
// is a GtkBox wherever no class declared the remove: a slot renders into
// the box its body made or the record's root.
func (e *cEmitter) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("BoxRemove", ir.TypVoid, parent, child)}}
}

// OnSlotReset starts a slot's render over: its list of widgets emptied, and
// its anchor made in the box it renders into where it is not there already.
func (e *cEmitter) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	// A record's is its own, as each instance renders the slot into a box
	// of its own.
	if r := e.scope; r != nil {
		field := clang.Local(slot.Name) + "__anchor"
		e.anchor = selfName + "->" + field
		if !slices.Contains(r.anchors, field) {
			r.anchors = append(r.anchors, field)
		}
	} else {
		e.anchor = clang.Global(slot.Name) + "__anchor"
		if !slices.Contains(e.anchors, e.anchor) {
			e.anchors = append(e.anchors, e.anchor)
		}
	}
	ref := &ir.Ident{Name: slot.Name, Sym: slot, Type: slot.Type}
	e.forget = rtCall("Forget", ir.TypVoid)
	return []ir.Stmt{
		&ir.CallStmt{Call: e.forget},
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
	if n, ok := stmt.(*ir.CanvasRedrawStmt); ok {
		return e.canvasRedraw(n)
	}
	if e.scope != nil && e.scope.rootRenders[stmt] {
		return append(e.topsInto(ctx, stmt.(*ir.CallStmt).Call.Args[0].Value), stmt)
	}
	return []ir.Stmt{stmt}
}

// cNatives are the gtk4rt calls a cgo build still makes, as rt's C writes
// each: its name, or what it writes in its place given its arguments.
func (e *cEmitter) native(c *clang.Context, n *ir.Call) (string, bool) {
	if out, ok := e.testCall(c, n); ok {
		return out, true
	}
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
		var args []string
		for i, a := range n.Args {
			// The component as the receiver a method call names is the
			// record, which __self is.
			if id, ok := a.Value.(*ir.Ident); ok && i == 0 && id.Sym == ir.Symbol(r.comp) {
				args = append(args, selfName)
				continue
			}
			args = append(args, c.EvalExpr(a.Value))
		}
		if !takesRecord(n.Func, r.comp) {
			args = append([]string{selfName}, args...)
		}
		return recordFunc(r.comp, n.Func.Name) + "(" + strings.Join(args, ", ") + ")", true
	}
	// A function of a component no record is made of -- the root a test
	// mounts, which reaches its functions as methods -- reads the file's
	// state, and is handed no component, which C has no value for.
	if comp := e.funcOwner(n.Func); comp != nil && !isInstanceComponent(comp) && takesRecord(n.Func, comp) && len(n.Args) > 0 {
		args := []string{"NULL"}
		for _, a := range n.Args[1:] {
			args = append(args, c.EvalExpr(a.Value))
		}
		return clang.FuncName(n.Func) + "(" + strings.Join(args, ", ") + ")", true
	}
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	widget := func(i int) string { return "GTK_WIDGET(" + arg(i) + ")" }
	switch n.Func.Intrinsic {
	case "gtk4.checkActive":
		return "gtk_check_button_get_active(GTK_CHECK_BUTTON(" + arg(0) + "))", true
	case "gtk4.switchActive":
		return "gtk_switch_get_active(GTK_SWITCH(" + arg(0) + "))", true
	case "gtk4.entryText":
		return "sngl_string_copy(gtk_editable_get_text(GTK_EDITABLE(" + arg(0) + ")))", true
	case "gtk4.calendarDate":
		// A calendar always has a day selected, which rt writes as a date's
		// text.
		opt := c.TypeName(n.Type)
		fn := "sngl_gtk_calendar_date"
		c.Helper(fn, "static "+opt+" "+fn+"(GtkWidget *w) {\n\tchar day[11];\n\tsngl_calendar_date(GTK_CALENDAR(w), day);\n\treturn ("+opt+"){.ok = true, .value = sngl_string_copy(day)};\n}\n")
		return fn + "(" + widget(0) + ")", true
	case "gtk4.dropDownLabel":
		return "sngl_string_copy(sngl_drop_down_label(GTK_DROP_DOWN(" + arg(0) + ")))", true
	}
	if n.Func.Foreign.Path != gtk4rtPkg {
		return "", false
	}
	name := strings.TrimPrefix(n.Func.Foreign.Name, "gtk4rt.")
	switch name {
	case "EditableSetTextQuiet":
		return "sngl_editable_set_text_quiet(" + widget(0) + ", (" + arg(1) + ").ptr)", true
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
		// rt hears a key as C has it, which an adapter hands the handler as
		// the program's: the name a string, and rt's stop a closure.
		sig := n.Func.Params[1].Type.Sig
		fn, ctx := c.Callback(sig, n.Args[1].Value)
		adapter := fn + "_key"
		c.Helper(adapter, fmt.Sprintf("static void %s(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, SnglFn stop, void *stop_ctx, void *ctx) {\n\t%s(sngl_string_copy(key), ctrl, alt, shift, meta, editing, (%s){stop, stop_ctx}, ctx);\n}\n",
			adapter, fn, c.TypeName(sig.Params[6].Type)))
		return "sngl_toplevel_on_key(" + widget(0) + ", " + adapter + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "BoxAppend":
		return "sngl_box_append(" + widget(0) + ", " + widget(1) + ")", true
	case "BoxRemove":
		return "sngl_box_remove(" + widget(0) + ", " + widget(1) + ")", true
	case "SlotAnchor":
		a := anchorOf(n.Args[1].Value)
		return "sngl_slot_anchor(" + widget(0) + ", &" + a + ")", true
	case "InsertBefore":
		return "sngl_box_insert_before(" + widget(0) + ", " + anchorOf(n.Args[2].Value) + ", " + widget(1) + ")", true
	case "AppAttach":
		return "sngl_app_attach(" + widget(0) + ")", true
	case "CanvasNew":
		return e.canvasNew(n.Args[0].Value), true
	case "CanvasOnClick":
		fn, ctx := c.Callback(n.Args[1].Value.ExprType().Sig, n.Args[1].Value)
		return "sngl_canvas_on_click(" + widget(0) + ", (SnglFn)" + fn + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "ScrollNew":
		return "sngl_scroll_new()", true
	case "ScrolledWindowSetChild":
		return "sngl_scroll_set_child(" + widget(0) + ", " + widget(1) + ")", true
	case "ScrollSetDirection":
		return "sngl_scroll_set_direction(" + widget(0) + ", (" + arg(1) + ").ptr)", true
	case "ScrollOnReachEnd":
		fn, ctx := c.Callback(n.Func.Params[1].Type.Sig, n.Args[1].Value)
		return "sngl_scroll_on_reach_end(" + widget(0) + ", (SnglFn)" + fn + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "QueueDraw":
		return "gtk_widget_queue_draw(" + widget(0) + ")", true
	case "TabsNew", "TabPageNew", "MenuEntryNew", "BackNew", "ImageFrameNew", "ImageSourceNew":
		return map[string]string{
			"TabsNew":        "sngl_tabs_new()",
			"TabPageNew":     "sngl_tab_page_new()",
			"MenuEntryNew":   "sngl_menu_entry_new()",
			"BackNew":        "sngl_back_new()",
			"ImageFrameNew":  "sngl_image_new()",
			"ImageSourceNew": "sngl_image_source_new()",
		}[name], true
	case "TabsSetSelected":
		return "sngl_tabs_set_selected(" + widget(0) + ", (int)(" + arg(1) + "))", true
	case "TabPageSetTitle", "MenuEntrySetLabel", "ImageFrameSetFit":
		set := map[string]string{"TabPageSetTitle": "sngl_tab_page_set_title", "MenuEntrySetLabel": "sngl_menu_entry_set_label", "ImageFrameSetFit": "sngl_image_set_fit"}[name]
		return set + "(" + widget(0) + ", (" + arg(1) + ").ptr)", true
	case "TabPageSetDisabled", "MenuEntrySetDisabled", "MenuEntrySetChecked":
		set := map[string]string{"TabPageSetDisabled": "sngl_tab_page_set_disabled", "MenuEntrySetDisabled": "sngl_menu_entry_set_disabled", "MenuEntrySetChecked": "sngl_menu_entry_set_checked"}[name]
		return set + "(" + widget(0) + ", " + arg(1) + ")", true
	case "TabPageOnShown", "MenuEntryOnSelect", "BackOnPress", "ImageFrameOnClick", "ImageFrameOnLoad":
		on := map[string]string{"TabPageOnShown": "sngl_tab_page_on_shown", "MenuEntryOnSelect": "sngl_menu_entry_on_select", "BackOnPress": "sngl_back_on_press", "ImageFrameOnClick": "sngl_image_on_click", "ImageFrameOnLoad": "sngl_image_on_load"}[name]
		fn, ctx := c.Callback(n.Func.Params[1].Type.Sig, n.Args[1].Value)
		return on + "(" + widget(0) + ", (SnglFn)" + fn + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "ImageFrameOnError":
		// rt reports the message as C has it, which an adapter hands the
		// handler as the program's string.
		fn, ctx := c.Callback(n.Func.Params[1].Type.Sig, n.Args[1].Value)
		adapter := fn + "_str"
		c.Helper(adapter, fmt.Sprintf("static void %s(const char *s, void *ctx) {\n\t%s(sngl_string_copy(s), ctx);\n}\n", adapter, fn))
		return "sngl_image_on_error(" + widget(0) + ", " + adapter + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "ImageSourceSetPicture":
		return "sngl_image_source_set_picture(" + widget(0) + ", " + arg(1) + ")", true
	case "LoadPicture":
		return "sngl_picture_load((" + arg(0) + ").ptr)", true
	case "RefPicture":
		return "sngl_picture_ref(" + arg(0) + ")", true
	case "UnrefPicture":
		return "sngl_picture_unref(" + arg(0) + ")", true
	case "CairoPaintPicture":
		return "sngl_picture_paint(" + arg(0) + ", " + arg(1) + ", " + arg(2) + ", " + arg(3) + ", " + arg(4) + ", " + arg(5) + ")", true
	case "ImagePaint":
		return e.imagePaint(n.Args[1].Value, arg(0)), true
	case "Forget":
		if len(n.Args) == 0 {
			return "(void)0", true
		}
		var held []string
		for i, a := range n.Args {
			if lit, ok := a.Value.(*ir.Literal); ok {
				copies := copiesName(lit.Value)
				held = append(held, "("+copies+" ? g_hash_table_remove_all("+copies+") : (void)0)")
				continue
			}
			held = append(held, "sngl_hold((GtkWidget **)&"+arg(i)+", NULL)")
		}
		return "(" + strings.Join(held, ", ") + ")", true
	case "Hold":
		return "sngl_hold((GtkWidget **)&" + arg(0) + ", GTK_WIDGET(" + arg(1) + "))", true
	case "FlowNew":
		return "sngl_flow_new()", true
	case "LabelSetMarkup":
		return "gtk_label_set_markup(GTK_LABEL(" + arg(0) + "), (" + arg(1) + ").ptr)", true
	case "When":
		return "((" + arg(0) + ") ? (" + arg(1) + ") : SNGL_STR(\"\"))", true
	case "Escape":
		c.Helper("sngl_markup_escape", "static sngl_string sngl_markup_escape(sngl_string s) {\n\tgchar *escaped = g_markup_escape_text(s.ptr, (gssize)s.len);\n\tsngl_string out = sngl_string_copy(escaped);\n\tg_free(escaped);\n\treturn out;\n}\n")
		return "sngl_markup_escape(" + arg(0) + ")", true
	case "Foreground":
		t := c.TypeName(n.Args[0].Value.ExprType())
		fn := t + "_foreground"
		c.Helper(fn, "static sngl_string "+fn+"("+t+" c) {\n\tif (c.a == 0) {\n\t\treturn SNGL_STR(\"\");\n\t}\n\tchar buf[32];\n\tg_snprintf(buf, sizeof buf, \" foreground=\\\"#%02X%02X%02X\\\"\", (unsigned)c.r, (unsigned)c.g, (unsigned)c.b);\n\treturn sngl_string_copy(buf);\n}\n")
		return fn + "(" + arg(0) + ")", true
	case "CopyGet":
		lit, _ := n.Args[0].Value.(*ir.Literal)
		return "((" + c.TypeName(n.Type) + ")sngl_copy_get(" + copiesName(lit.Value) + ", (" + arg(1) + ").ptr))", true
	case "CopyPut":
		lit, _ := n.Args[0].Value.(*ir.Literal)
		return "sngl_copy_put(&" + copiesName(lit.Value) + ", (" + arg(1) + ").ptr, " + widget(2) + ")", true
	case "CalendarSelectDay":
		return "sngl_calendar_select(GTK_CALENDAR(" + arg(0) + "), (int)(" + arg(1) + "), (int)(" + arg(2) + "), (int)(" + arg(3) + "))", true
	case "DropDownNew":
		return "gtk_drop_down_new(NULL, NULL)", true
	case "DropDownOffer":
		return "sngl_drop_down_offer(GTK_DROP_DOWN(" + arg(0) + "), (" + arg(1) + ").ptr, (" + arg(2) + ").ptr)", true
	case "DropDownLabel":
		return "sngl_string_copy(sngl_drop_down_label(GTK_DROP_DOWN(" + arg(0) + ")))", true
	case "DropDownSelect":
		return "sngl_drop_down_select(GTK_DROP_DOWN(" + arg(0) + "), (" + arg(1) + ").ptr)", true
	case "Every":
		fn, ctx := c.Callback(n.Func.Params[1].Type.Sig, n.Args[1].Value)
		return "sngl_every((int)(" + arg(0) + "), (SnglFn)" + fn + ", " + ctx + ", (GDestroyNotify)sngl_release)", true
	case "Schedule.Cancel":
		return "sngl_schedule_cancel(" + arg(0) + ")", true
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

// connects reports whether the GLib connect of event reaches the widget id
// names: its GIR class has the signal, or it is a property's notification.
// A widget gtk4.sngl makes of its own, and a name no widget holds, connect
// through their own protocols.
func (e *cEmitter) connects(id, event string) bool {
	cType := e.widgetType(id)
	if !strings.HasPrefix(cType, "Gtk") || e.reg == nil {
		return true
	}
	if strings.HasPrefix(event, "notify::") || notifySignal(cType, event) != "" {
		return true
	}
	info := e.reg.ByCType[cType]
	if info == nil {
		return true
	}
	for _, sig := range info.Signals {
		if sig.Name == event {
			return true
		}
	}
	_, found := girSignal(info, event)
	return found
}

// handlerOn is a handler declaring the widget it fires on, `@toggled(b)`,
// as the one the connect's template calls with nothing: what it reads of
// its parameter it reads of node.
func handlerOn(handler, node ir.Expr) ir.Expr {
	switch h := handler.(type) {
	case *ir.Lambda:
		if h.Func == nil || len(h.Func.Params) == 0 {
			return h
		}
		fn := *h.Func
		fn.Block = selfIsNode(fn.Block, fn.Params, node)
		fn.Params = nil
		return &ir.Lambda{AST: h.AST, Func: &fn, Type: ir.FuncOf(nil, fn.Return)}
	case *ir.Closure:
		if h.Func == nil || len(h.Func.Params) < 2 {
			return h
		}
		h.Func.Block = selfIsNode(h.Func.Block, h.Func.Params[1:], node)
		h.Func.Params = h.Func.Params[:1]
		cp := *h
		cp.Type = ir.FuncOf(nil, h.Func.Return)
		return &cp
	}
	return handler
}

// hasBody reports whether fn is written with a body, an empty one included:
// what has none is a declaration -- a native, or a handler lowering attaches
// where the program wrote none.
func hasBody(fn *ir.Func) bool {
	return len(fn.Block) > 0 || fn.AST != nil && (fn.AST.Body != nil || fn.AST.Block.IsDefined())
}

// funcOwner is the component fn is a function of, or nil.
func (e *cEmitter) funcOwner(fn *ir.Func) *ir.Component {
	if e.pkg == nil || fn == nil {
		return nil
	}
	for _, comp := range e.pkg.Components {
		if comp != nil && slices.Contains(comp.Funcs, fn) {
			return comp
		}
	}
	return nil
}

// cAssets is sngl_assets.c, the `file:` assets the build copied, carried in
// the program as rt's sngl_assets: each by the path the build gave it, which
// an image's URL names.
func cAssets(assets []codegen.FileAsset) ([]byte, error) {
	var b strings.Builder
	b.WriteString("/* Code generated by sngl. DO NOT EDIT. */\n#include \"sngl_gtk.h\"\n")
	var table []string
	for i, fa := range assets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				return nil, err
			}
			data = d
		}
		fmt.Fprintf(&b, "\nstatic const unsigned char sngl_asset%d[] = {", i)
		for j, c := range data {
			if j%16 == 0 {
				b.WriteString("\n\t")
			} else {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "0x%02x,", c)
		}
		b.WriteString("\n};\n")
		table = append(table, fmt.Sprintf("\t{%s, sngl_asset%d, %d},\n", strconv.Quote(filepath.ToSlash(fa.OutPath)), i, len(data)))
	}
	b.WriteString("\nconst SnglAsset sngl_assets[] = {\n" + strings.Join(table, "") + "\t{NULL, NULL, 0},\n};\n")
	return []byte(b.String()), nil
}

// forgets reports whether the slot render being walked lets go of the copies
// of id already.
func (e *cEmitter) forgets(id string) bool {
	for _, a := range e.forget.Args {
		if lit, ok := a.Value.(*ir.Literal); ok && lit.Value == id {
			return true
		}
	}
	return false
}

// copiesName is the table the copies of the node id names are held in.
func copiesName(id string) string { return clang.Global(id) + "__copies" }

// flowMarkup sets the label flow is to the markup its spans make, through the
// set gtk4.sngl declares for it.
func (e *cEmitter) flowMarkup(flow string, node ir.Expr) []ir.Stmt {
	set := e.markup.setMarkup[flow]
	if set == nil {
		return e.fail("a flow with no set of its markup")
	}
	return []ir.Stmt{&ir.CallStmt{Call: boundCall(set, node, e.markup.markupExpr(flow, func(err error) {
		if e.err == nil {
			e.err = err
		}
	}))}}
}
