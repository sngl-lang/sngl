package c

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen/irwalk"
	"duckfam.us/sngl/ir"
)

// C has no closure, so a SNGL function value is a struct of two words: the
// function, which takes the closure's environment first, and the
// environment, a heap copy of what the closure captured. Its type is one per
// signature, `sngl_fn_<params>__<result>`, defined the first time a value of
// it is written. A call through one passes the environment back:
// `f.fn(f.env, x)`.
//
// passLambda lifted every lambda a program wrote to a function taking its
// captures as a struct (ir.Closure), so a closure here is that function
// behind a thunk unpacking the environment. A lambda the passes never saw --
// one a protocol's template writes, spliced after them -- is lifted here
// (Lambda), capturing what it reads of the scope it is written in.
//
// An environment is never freed: a closure is held by what it is handed to,
// which on gtk4 is a widget's signal for the life of the widget. A callback
// handed to C is freed when C releases it (cCallback).

// fnType is the C struct a value of the function type t is, defined in the
// file.
func (c *Context) fnType(t *ir.Type) string {
	sig := t.Sig
	name := "sngl_fn_" + sigToken(sig)
	params := []string{"void *env"}
	for _, p := range sig.Params {
		params = append(params, c.typeName(p.Type))
	}
	ret := c.typeName(sig.Return)
	// A function pointer's parameters may be types only declared.
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\t%s (*fn)(%s);\n\tvoid *env;\n} %s;\n", name, ret, strings.Join(params, ", "), name))
	return name
}

// sigToken names a signature by the C types it takes and gives: the name of
// its function value's type and of the dispatcher C calls it through.
func sigToken(sig *ir.FuncSig) string {
	var parts []string
	for _, p := range sig.Params {
		parts = append(parts, typeToken(p.Type))
	}
	if len(parts) == 0 {
		parts = []string{"void"}
	}
	ret := "void"
	if sig.Return != nil && sig.Return.Kind != ir.TypeVoid {
		ret = typeToken(sig.Return)
	}
	return strings.Join(parts, "_") + "__" + ret
}

func typeToken(t *ir.Type) string {
	if ct := ir.CTypeOf(t); ct != nil && !isProgramStruct(t) {
		return strings.ReplaceAll(ct.Name, " ", "_") + strings.Repeat("_p", ct.Ptr)
	}
	switch t.Kind {
	case ir.TypeFunc:
		return "fn_" + sigToken(t.Sig) + "_"
	case ir.TypeStruct:
		if sd, ok := t.Decl.(*ir.StructDef); ok {
			return mangle(sd.Name)
		}
	case ir.TypeNative:
		if ref, ok := t.Meta.(ir.NativeTypeRef); ok {
			return mangle(strings.TrimPrefix(ref.Name, "C.")) + "_p"
		}
	}
	return mangle(t.String())
}

// Closure is a lifted lambda as a function value: its thunk, and its
// captures copied to the heap.
func (c *Context) Closure(n *ir.Closure) string {
	if c.Closures != nil {
		if out, ok := c.Closures(c, n); ok {
			return out
		}
	}
	fnT := c.fnType(n.Type)
	state := n.Func.Params[0]
	stateT := c.typeName(state.Type)
	thunk := funcName(n.Func) + "__fn"
	sig := n.Type.Sig
	params := []string{"void *env"}
	args := []string{"*(" + stateT + " *)env"}
	for i, p := range sig.Params {
		params = append(params, c.typeName(p.Type)+fmt.Sprintf(" a%d", i))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	ret := c.typeName(sig.Return)
	body := funcName(n.Func) + "(" + strings.Join(args, ", ") + ")"
	if ret != "void" {
		body = "return " + body
	}
	c.file.helper(thunk, fmt.Sprintf("static %s %s(%s) {\n\t%s;\n}\n", ret, thunk, strings.Join(params, ", "), body))
	env := c.heapCopy(stateT, c.EvalExpr(n.State))
	return "((" + fnT + "){" + thunk + ", " + env + "})"
}

// heapCopy is v, of the C type t, copied to the heap.
func (c *Context) heapCopy(t, v string) string {
	return "sngl_heap_copy(&(" + t + "[]){" + v + "}, sizeof(" + t + "))"
}

// funcValue is fn, a declared function, as a function value: a thunk that
// drops the environment, which is NULL.
func (c *Context) funcValue(fn *ir.Func, t *ir.Type) string {
	fnT := c.fnType(t)
	thunk := funcName(fn) + "__fn"
	var params, args []string
	params = append(params, "void *env")
	for i, p := range fn.Params {
		params = append(params, c.typeName(p.Type)+fmt.Sprintf(" a%d", i))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	ret := c.typeName(fn.Return)
	body := funcName(fn) + "(" + strings.Join(args, ", ") + ")"
	if ret != "void" {
		body = "return " + body
	}
	c.file.helper(thunk, fmt.Sprintf("static %s %s(%s) {\n\t%s;\n}\n", ret, thunk, strings.Join(params, ", "), body))
	return "((" + fnT + "){" + thunk + ", NULL})"
}

// Lambda lifts a lambda the passes never lifted to a function of the file,
// its environment what its body reads of the locals in scope, copied: a
// template's lambda reads only what was spliced into it, and writes nothing
// of its scope's.
func (c *Context) Lambda(n *ir.Lambda) string {
	if n.Type == nil || n.Type.Sig == nil {
		n = &ir.Lambda{AST: n.AST, Func: n.Func, Type: ir.FuncOf(n.Func.Params, n.Func.Return)}
	}
	fnT := c.fnType(n.Type)
	id := c.file.next()
	name := fmt.Sprintf("sngl_lambda%d", id)
	envT := name + "_env"
	own := map[string]bool{}
	for _, p := range n.Func.Params {
		own[p.Name] = true
	}
	var caps []string
	seen := map[string]bool{}
	ir.Walk(n.Func.Block, func(node ir.Node) error {
		switch x := node.(type) {
		case *ir.LocalVar:
			own[x.Name] = true
		case *ir.Ident:
			if c.locals[x.Name] && !own[x.Name] && !seen[x.Name] {
				seen[x.Name] = true
				caps = append(caps, x.Name)
			}
		}
		return nil
	})
	var fields, inits []string
	inner := &Context{Ctx: c.Ctx, State: c.State, Native: c.Native, Types: c.Types, Closures: c.Closures, file: c.file, locals: map[string]bool{}, ret: n.Type.Sig.Return}
	var deps []string
	for _, name := range caps {
		t := c.localType(n.Func.Block, name)
		deps = append(deps, c.typeName(t))
		fields = append(fields, "\t"+c.typeName(t)+" "+Local(name)+";")
		inits = append(inits, "."+Local(name)+" = "+Local(name))
		inner.locals[name] = true
	}
	// The record the code is written in, which the body reads what it holds
	// through.
	var self string
	if c.Self != "" {
		i := strings.LastIndexAny(c.Self, " *")
		self = c.Self[i+1:]
		fields = append(fields, "\t"+c.Self+";")
		inits = append(inits, "."+self+" = "+self)
		inner.Self = c.Self
	}
	if len(fields) == 0 {
		fields = []string{"\tchar unused;"}
	}
	c.file.define(envT, "typedef struct "+envT+" {\n"+strings.Join(fields, "\n")+"\n} "+envT+";\n", deps...)
	params := []string{"void *env_"}
	for _, p := range n.Func.Params {
		params = append(params, c.typeName(p.Type)+" "+Local(p.Name))
		inner = inner.WithLocal(p.Name)
	}
	ret := c.typeName(n.Type.Sig.Return)
	sig := fmt.Sprintf("static %s %s(%s)", ret, name, strings.Join(params, ", "))
	c.file.protos = append(c.file.protos, sig+";")
	var b strings.Builder
	b.WriteString(sig + " {\n")
	if len(caps) > 0 || self != "" {
		fmt.Fprintf(&b, "\t%s *env = env_;\n", envT)
		for _, name := range caps {
			fmt.Fprintf(&b, "\t%s %s = env->%s;\n", c.typeName(c.localType(n.Func.Block, name)), Local(name), Local(name))
		}
		if self != "" {
			fmt.Fprintf(&b, "\t%s = env->%s;\n", c.Self, self)
		}
	}
	for _, l := range irwalk.EvalBlock(inner, n.Func.Block) {
		b.WriteString("\t" + l + "\n")
	}
	b.WriteString("}\n")
	c.file.lifted = append(c.file.lifted, b.String())
	env := "NULL"
	if len(caps) > 0 || self != "" {
		env = c.heapCopy(envT, "{"+strings.Join(inits, ", ")+"}")
	}
	return "((" + fnT + "){" + name + ", " + env + "})"
}

// localType is the type of what block reads as the local name.
func (c *Context) localType(block []ir.Stmt, name string) *ir.Type {
	var t *ir.Type
	ir.Walk(block, func(node ir.Node) error {
		if id, ok := node.(*ir.Ident); ok && id.Name == name && t == nil {
			t = id.Type
		}
		return nil
	})
	return t
}

// callValue is a call of fn, a function value: its function, handed its
// environment.
func (c *Context) callValue(fn string, args []string) string {
	return "(" + fn + ").fn(" + strings.Join(append([]string{"(" + fn + ").env"}, args...), ", ") + ")"
}

// cCallback is the C function pointer, context and release a function value
// crosses to C as: a dispatcher per C signature, which finds the closure
// through the context, the closure copied to the heap as the context, and a
// release that frees it. sig is the callback's type as the C function
// declares it.
func (c *Context) cCallback(sig *ir.FuncSig, value string, valueType *ir.Type) (fn, ctx string) {
	fnT := c.fnType(valueType)
	name := "sngl_dispatch_" + sigToken(sig)
	var params, args []string
	for i, p := range sig.Params {
		params = append(params, c.typeName(p.Type)+fmt.Sprintf(" a%d", i))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	params = append(params, "void *ctx")
	ret := c.typeName(sig.Return)
	call := "f->fn(" + strings.Join(append([]string{"f->env"}, args...), ", ") + ")"
	if ret != "void" {
		call = "return " + call
	}
	c.file.helper(name, fmt.Sprintf("static %s %s(%s) {\n\t%s *f = ctx;\n\t%s;\n}\n", ret, name, strings.Join(params, ", "), fnT, call))
	c.file.helper("sngl_release", "static void sngl_release(void *ctx) {\n\tfree(ctx);\n}\n")
	c.file.include("stdlib.h", true)
	return name, c.heapCopy(fnT, value)
}
