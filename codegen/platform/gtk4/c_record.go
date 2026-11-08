package gtk4

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// A component built at run time is a record in C, as it is in Go: a struct
// holding its vars and the widgets its body makes, which a constructor
// allocates and builds, each prop's setter and its teardown take, and a
// destroy gives back. Its root widget, what its parent holds, is referenced
// by the record, since a slot removing it to put it back would free it.
//
//	typedef struct s_row__inst { sngl_string __prop_label; …; GtkButton *b; } s_row__inst;
//	static s_row__inst *s_row__new(sngl_string label);
//	static void s_row____set_label(s_row__inst *__self, sngl_string __v);
//	static void s_row__destroy(s_row__inst *__self);
//
// Inside one, what the record holds is read through __self.

// selfName is the parameter a record's functions take it as.
const selfName = "__self"

// cRecord is a record being written: its component, and the widgets its body
// made, by id, in the order made, with those another was appended to.
type cRecord struct {
	comp     *ir.Component
	nodes    map[string]string
	order    []string
	appended map[string]bool
}

func recordType(comp *ir.Component) string { return clang.Decl(&comp.DeclInfo) + "__inst" }

func recordFunc(comp *ir.Component, name string) string {
	return clang.Decl(&comp.DeclInfo) + "_" + name
}

// holds reports whether name is something r holds.
func (r *cRecord) holds(name string) bool {
	if r == nil {
		return false
	}
	if _, ok := r.nodes[name]; ok {
		return true
	}
	return slices.ContainsFunc(r.comp.Vars, func(v *ir.Var) bool { return v.Name == name })
}

// record writes comp's record: the struct, the constructor, its functions
// and the destroy.
func (e *cEmitter) record(comp *ir.Component) {
	r := &cRecord{comp: comp, nodes: map[string]string{}, appended: map[string]bool{}}
	prev, prevSelf := e.scope, e.ctx.Self
	e.scope = r
	e.ctx.Self = recordType(comp) + " *" + selfName
	defer func() { e.scope, e.ctx.Self = prev, prevSelf }()

	typ := recordType(comp)
	body := codegen.WalkLowered(context.Background(), comp.Stmts, e)
	// What the body made is the candidates for its root; what its functions
	// make, walked next, are fields too, which the struct must hold before
	// anything is written.
	made := slices.Clone(r.order)
	var funcs []*ir.Func
	for _, fn := range e.closureFuncs(comp) {
		e.anchor = ""
		fn.Block = codegen.WalkLowered(context.Background(), fn.Block, e)
		funcs = append(funcs, fn)
	}
	for _, fn := range comp.Funcs {
		if fn == nil || !hasBody(fn) {
			continue
		}
		e.anchor = ""
		fn.Block = codegen.WalkLowered(context.Background(), fn.Block, e)
		funcs = append(funcs, fn)
	}
	var ctor strings.Builder
	var params []string
	for _, p := range comp.Props {
		params = append(params, e.ctx.TypeName(p.Type)+" "+clang.Local(p.Name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	sig := fmt.Sprintf("%s *%s(%s)", typ, recordFunc(comp, "_new"), strings.Join(params, ", "))
	fmt.Fprintf(&ctor, "\nstatic %s {\n\t%s *%s = calloc(1, sizeof(%s));\n", sig, typ, selfName, typ)
	ctx := e.ctx
	for _, p := range comp.Props {
		ctx = ctx.WithLocal(p.Name)
	}
	for _, v := range comp.Vars {
		if v.Init != nil {
			fmt.Fprintf(&ctor, "\t%s->%s = %s;\n", selfName, clang.Local(v.Name), ctx.EvalExpr(v.Init))
		}
	}
	for _, l := range ctx.EvalBlock(body) {
		ctor.WriteString("\t" + l + "\n")
	}
	// The root: what the body left unattached, boxed where it left several.
	var tops []string
	for _, id := range made {
		if !r.appended[id] {
			tops = append(tops, id)
		}
	}
	root := selfName + "->__root"
	switch len(tops) {
	case 0:
		fmt.Fprintf(&ctor, "\t%s = gtk_label_new(NULL);\n\tgtk_widget_set_visible(%s, FALSE);\n", root, root)
	case 1:
		fmt.Fprintf(&ctor, "\t%s = GTK_WIDGET(%s->%s);\n", root, selfName, clang.Local(tops[0]))
	default:
		fmt.Fprintf(&ctor, "\t%s = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);\n", root)
		for _, id := range tops {
			fmt.Fprintf(&ctor, "\tgtk_box_append(GTK_BOX(%s), GTK_WIDGET(%s->%s));\n", root, selfName, clang.Local(id))
		}
	}
	fmt.Fprintf(&ctor, "\tg_object_ref_sink(%s);\n\treturn %s;\n}\n", root, selfName)

	// The struct, once every field is known.
	var fields, deps []string
	hasRoot := false
	for _, v := range comp.Vars {
		t := e.ctx.TypeName(v.Type)
		if v.Name == instanceRootVar {
			t, hasRoot = "GtkWidget *", true
		}
		deps = append(deps, t)
		fields = append(fields, "\t"+t+" "+clang.Local(v.Name)+";")
	}
	if !hasRoot {
		fields = append(fields, "\tGtkWidget *__root;")
	}
	for _, id := range r.order {
		fields = append(fields, "\t"+r.nodes[id]+clang.Local(id)+";")
	}
	e.ctx.Define(typ, "typedef struct "+typ+" {\n"+strings.Join(fields, "\n")+"\n} "+typ+";\n", deps...)
	e.ctx.Include("stdlib.h", true)

	teardown := ""
	for _, fn := range funcs {
		// A function the component declares names it as its receiver,
		// first: that parameter is the record.
		if takesRecord(fn, comp) {
			fn.Params[0].Name = selfName
			e.fe.FuncAs(fn, recordFunc(comp, fn.Name), "")
		} else {
			e.fe.FuncAs(fn, recordFunc(comp, fn.Name), typ+" *"+selfName)
		}
		if fn.Name == lower.TeardownFunc && slices.Contains(comp.Funcs, fn) {
			teardown = "\t" + recordFunc(comp, fn.Name) + "(" + selfName + ");\n"
		}
	}
	var destroy strings.Builder
	io := e.fe
	fmt.Fprintf(io, "%s", ctor.String())
	fmt.Fprintf(&destroy, "\nstatic void %s(%s *%s) {\n%s\tg_object_unref(%s);\n\tfree(%s);\n}\n", recordFunc(comp, "_destroy"), typ, selfName, teardown, root, selfName)
	fmt.Fprintf(io, "%s", destroy.String())
	e.fe.Prototype(fmt.Sprintf("static %s;", sig))
	e.fe.Prototype(fmt.Sprintf("static void %s(%s *%s);", recordFunc(comp, "_destroy"), typ, selfName))
}

// recordOf is the component a value of t is a record of, where it is one.
func recordOf(t *ir.Type) *ir.Component {
	if t == nil || t.Kind != ir.TypeComponent && t.Kind != ir.TypeInstance {
		return nil
	}
	comp, _ := t.Decl.(*ir.Component)
	if !isInstanceComponent(comp) {
		return nil
	}
	return comp
}

func (e *cEmitter) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	comp := golang.CreateComponentTarget(call)
	if comp == nil || !isInstanceComponent(comp) || len(call.Args) != 2 {
		return e.fail("building %s at run time", id)
	}
	lit, _ := call.Args[1].Value.(*ir.StructLit)
	var args []ir.Expr
	for _, p := range comp.Props {
		var val ir.Expr
		if lit != nil {
			for _, f := range lit.Fields {
				if f.Name == p.Name {
					val = f.Value
				}
			}
		}
		if val == nil {
			val = p.Default
		}
		if val == nil {
			val = ir.ZeroExpr(p.Type)
		}
		args = append(args, val)
	}
	// Held as a widget is: a setter or a teardown after the function that
	// built it reaches it.
	t := &ir.Type{Kind: ir.TypeInstance, Decl: comp}
	v := &ir.Var{Name: id, Type: t}
	e.hold(id, recordType(comp)+" *")
	return []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: id, Sym: v, Type: t}, Op: ast.AssignSet, Value: recordCall(comp, "_new", t, args...)}}
}

func (e *cEmitter) OnComponentRoot(ctx context.Context, id string, inst, key ir.Expr) []ir.Stmt {
	t := ir.NativePointerOf("GtkWidget")
	v := &ir.Var{Name: id, Type: t}
	return []ir.Stmt{&ir.LocalVar{Name: id, Type: t, Sym: v, Init: &ir.Select{Operand: inst, Field: "__root", Type: t}}}
}

func (e *cEmitter) OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	comp := recordOf(inst.ExprType())
	if comp == nil {
		return e.fail("updating a %s at run time", inst.ExprType())
	}
	setter := "__set_" + prop
	if !slices.ContainsFunc(comp.Funcs, func(f *ir.Func) bool { return f.Name == setter }) {
		return e.fail("%s's prop %s, which it has no setter for", comp.Name, prop)
	}
	return []ir.Stmt{&ir.CallStmt{Call: recordCall(comp, setter, ir.TypVoid, inst, value)}}
}

func (e *cEmitter) OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt {
	comp := recordOf(inst.ExprType())
	if comp == nil {
		return e.fail("destroying a %s", inst.ExprType())
	}
	return []ir.Stmt{&ir.CallStmt{Call: recordCall(comp, "_destroy", ir.TypVoid, inst)}}
}

// recordCall is a call of comp's record function name, which the native hook
// spells.
func recordCall(comp *ir.Component, name string, ret *ir.Type, args ...ir.Expr) *ir.Call {
	ca := make([]ir.CallArg, len(args))
	for i, a := range args {
		ca[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{Type: ret, Func: &ir.Func{Name: recordFunc(comp, name), Path: recordPath}, Args: ca}
}

// recordPath marks a call recordCall makes, which the native hook writes as
// the call of the C function it names.
const recordPath = "sngl:record"

// closureFuncs are the functions lowering lifted out of comp's body and its
// functions, and out of those in turn: the record's closures.
func (e *cEmitter) closureFuncs(comp *ir.Component) []*ir.Func {
	if fns, ok := e.recordClosures[comp]; ok {
		return fns
	}
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	var visit func(root any)
	visit = func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			if cl, ok := n.(*ir.Closure); ok && cl.Func != nil && !seen[cl.Func] {
				seen[cl.Func] = true
				out = append(out, cl.Func)
				visit(cl.Func.Block)
			}
			return nil
		})
	}
	visit(comp.Stmts)
	for _, fn := range comp.Funcs {
		visit(fn.Block)
	}
	if e.recordClosures == nil {
		e.recordClosures = map[*ir.Component][]*ir.Func{}
	}
	e.recordClosures[comp] = out
	return out
}

// closureOwner is the record comp whose closure fn is, nil where it is none's.
func (e *cEmitter) closureOwner(fn *ir.Func) *ir.Component {
	for _, comp := range e.pkg.Components {
		if isInstanceComponent(comp) && slices.Contains(e.closureFuncs(comp), fn) {
			return comp
		}
	}
	return nil
}

// closure writes a closure made in a record: its environment is the
// captures lowering gave it and the record, which its function, written as
// one of the record's, reads the rest through.
func (e *cEmitter) closure(c *clang.Context, n *ir.Closure) (string, bool) {
	comp := e.closureOwner(n.Func)
	if comp == nil {
		return "", false
	}
	if e.scope == nil || e.scope.comp != comp {
		e.fail("a closure of %s's made outside it", comp.Name)
		return "0", true
	}
	fnT := c.FnType(n.Type)
	name := recordFunc(comp, n.Func.Name)
	stateT := c.TypeName(n.Func.Params[0].Type)
	envT := name + "__env"
	c.Define(envT, "typedef struct "+envT+" {\n\t"+stateT+" state;\n\t"+recordType(comp)+" *self;\n} "+envT+";\n", stateT, recordType(comp))
	params := []string{"void *env"}
	args := []string{"((" + envT + " *)env)->self", "((" + envT + " *)env)->state"}
	for i, p := range n.Type.Sig.Params {
		params = append(params, fmt.Sprintf("%s a%d", c.TypeName(p.Type), i))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	ret := c.TypeName(n.Type.Sig.Return)
	body := name + "(" + strings.Join(args, ", ") + ")"
	if ret != "void" {
		body = "return " + body
	}
	c.Helper(name+"__fn", fmt.Sprintf("static %s %s__fn(%s) {\n\t%s;\n}\n", ret, name, strings.Join(params, ", "), body))
	env := "sngl_heap_copy(&(" + envT + "[]){{" + c.EvalExpr(n.State) + ", " + selfName + "}}, sizeof(" + envT + "))"
	return "((" + fnT + "){" + name + "__fn, " + env + "})", true
}

// takesRecord reports whether fn, a function of comp's, names comp as its
// first parameter, the receiver a method has: that parameter is the record.
func takesRecord(fn *ir.Func, comp *ir.Component) bool {
	return len(fn.Params) > 0 && fn.Params[0].Type != nil && fn.Params[0].Type.Decl == ir.Symbol(comp)
}
