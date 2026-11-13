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
// An environment is made in the turn's arena, as a string is, behind a
// header saying how it is kept and given back (sngl_env): a holder storing a
// closure keeps a copy of its environment and what that captured, and gives
// both back as it drops it. A callback handed to C is kept as it crosses,
// and given back when C releases it (cCallback).

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
	envT := stateT + "__env"
	params := []string{"void *env"}
	args := []string{"((" + envT + " *)env)->caps"}
	for i, p := range sig.Params {
		params = append(params, c.typeName(p.Type)+fmt.Sprintf(" a%d", i))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	ret := c.typeName(sig.Return)
	body := funcName(n.Func) + "(" + strings.Join(args, ", ") + ")"
	if Bodiless(n.Func) {
		// A handler lowering attached where the program wrote none, which
		// no file declares: its thunk does nothing.
		if ret != "void" {
			return c.fail("a closure of %s, which has no body", n.Func.Name)
		}
		body = "(void)env"
	} else if ret != "void" {
		body = "return " + body
	}
	c.file.define(envT, "typedef struct "+envT+" {\n\tsngl_env hdr;\n\t"+stateT+" caps;\n} "+envT+";\n", stateT)
	c.envOps(envT, map[string]*ir.Type{"caps": state.Type}, []string{"caps"})
	c.file.helper(thunk, fmt.Sprintf("static %s %s(%s) {\n\t%s;\n}\n", ret, thunk, strings.Join(params, ", "), body))
	env := c.arenaCopy(envT, "{{"+envT+"__keep, "+envT+"__drop}, "+c.EvalExpr(n.State)+"}")
	return "((" + fnT + "){" + thunk + ", " + env + "})"
}

// envOps defines the keep and drop of envT, an environment whose fields
// named in order, of the types in fields, are what it captured: keep copies
// it out of the arena and keeps each, and drop gives each back and then it.
func (c *Context) envOps(envT string, fields map[string]*ir.Type, order []string) {
	if c.file.helperNames[envT+"__keep"] {
		return
	}
	var keep, drop strings.Builder
	fmt.Fprintf(&keep, "static void *%s__keep(const void *p) {\n\t%s *e = sngl_keep_copy(p, sizeof(%s));\n", envT, envT, envT)
	fmt.Fprintf(&drop, "static void %s__drop(void *p) {\n\t%s *e = p;\n", envT, envT)
	for _, f := range order {
		t := fields[f]
		if t == nil || !owns(t) {
			continue
		}
		fmt.Fprintf(&keep, "\te->%s = %s;\n", f, c.keep(t, "e->"+f))
		fmt.Fprintf(&drop, "\t%s\n", c.drop(t, "e->"+f))
	}
	keep.WriteString("\treturn e;\n}\n")
	drop.WriteString("\tsngl_drop(e);\n}\n")
	c.file.protos = append(c.file.protos, "static void *"+envT+"__keep(const void *p);", "static void "+envT+"__drop(void *p);")
	c.file.helper(envT+"__keep", keep.String())
	c.file.helper(envT+"__drop", drop.String())
}

// heapCopy is v, of the C type t, copied to the heap.
func (c *Context) heapCopy(t, v string) string {
	return "sngl_heap_copy(&(" + t + "[]){" + v + "}, sizeof(" + t + "))"
}

// arenaCopy is v, of the C type t, copied to the turn's arena.
func (c *Context) arenaCopy(t, v string) string {
	return "sngl_alloc_copy(&(" + t + "[]){" + v + "}, sizeof(" + t + "))"
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
// its environment what its body reads of the locals in scope. A template's
// lambda reads only what was spliced into it, which is copied. A test's
// lambda -- a stop a key's handler calls, a wait's predicate -- may write a
// local of the test's, which it reaches through the local's address: each
// runs while the test does, which is the local's whole life.
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
	seen, written := map[string]bool{}, map[string]bool{}
	ir.Walk(n.Func.Block, func(node ir.Node) error {
		switch x := node.(type) {
		case *ir.LocalVar:
			own[x.Name] = true
		case *ir.For:
			own[x.Key] = true
			own[x.Value] = true
		case *ir.Lambda:
			for _, p := range x.Func.Params {
				own[p.Name] = true
			}
		case *ir.Assign:
			if id, ok := x.Target.(*ir.Ident); ok {
				written[id.Name] = true
			}
		case *ir.Toggle:
			if id, ok := x.Target.(*ir.Ident); ok {
				written[id.Name] = true
			}
		case *ir.Ident:
			if c.captures(x) && !own[x.Name] && !seen[x.Name] {
				seen[x.Name] = true
				caps = append(caps, x.Name)
			}
		}
		return nil
	})
	fields, inits := []string{"\tsngl_env hdr;"}, []string{".hdr = {" + envT + "__keep, " + envT + "__drop}"}
	kept := map[string]*ir.Type{}
	var order []string
	inner := &Context{Ctx: c.Ctx, State: c.State, Native: c.Native, Types: c.Types, Closures: c.Closures, file: c.file, locals: map[string]bool{}, refs: map[string]bool{}, ret: n.Type.Sig.Return}
	var deps []string
	for _, name := range caps {
		t := c.localType(n.Func.Block, name)
		deps = append(deps, c.typeName(t))
		ref, addr := "", ""
		if written[name] {
			ref, addr = "*", "&"
			inner.refs[name] = true
		}
		fields = append(fields, "\t"+c.typeName(t)+" "+ref+Local(name)+";")
		capture := c.Ident(&ir.Ident{Name: name, Sym: c.localSym(n.Func.Block, name), Type: t})
		if addr == "" {
			kept[Local(name)] = t
			order = append(order, Local(name))
		}
		inits = append(inits, "."+Local(name)+" = "+addr+capture)
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
	c.file.define(envT, "typedef struct "+envT+" {\n"+strings.Join(fields, "\n")+"\n} "+envT+";\n", deps...)
	if len(caps) > 0 || self != "" {
		c.envOps(envT, kept, order)
	}
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
			ref := ""
			if inner.refs[name] {
				ref = "*"
			}
			fmt.Fprintf(&b, "\t%s %s%s = env->%s;\n", c.typeName(c.localType(n.Func.Block, name)), ref, Local(name), Local(name))
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
		env = c.arenaCopy(envT, "{"+strings.Join(inits, ", ")+"}")
	}
	return "((" + fnT + "){" + name + ", " + env + "})"
}

// captures reports whether a lambda reading id reads it from the scope it is
// made in: a parameter or loop variable, or a var neither the package nor
// the platform holds.
func (c *Context) captures(id *ir.Ident) bool {
	if c.locals[id.Name] {
		return true
	}
	switch sym := id.Sym.(type) {
	case *ir.Param, *ir.LoopVar:
		return true
	case *ir.Var:
		if c.isPackageVar(sym) {
			return false
		}
		if c.State != nil {
			if _, held := c.State(id, false); held {
				return false
			}
		}
		return true
	}
	return false
}

// localSym is the declaration block reads as the local name.
func (c *Context) localSym(block []ir.Stmt, name string) ir.Symbol {
	var sym ir.Symbol
	ir.Walk(block, func(node ir.Node) error {
		if id, ok := node.(*ir.Ident); ok && id.Name == name && sym == nil {
			sym = id.Sym
		}
		return nil
	})
	return sym
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
	// What C calls back is a turn of the program's, or part of the one
	// running where the program's own call made C call back, which gives
	// back the arena it used either way.
	call := "f->fn(" + strings.Join(append([]string{"f->env"}, args...), ", ") + ");\n\tsngl_call_leave(m)"
	if ret != "void" {
		call = ret + " r = " + call + ";\n\treturn r"
	}
	c.file.helper(name, fmt.Sprintf("static %s %s(%s) {\n\t%s *f = ctx;\n\tsngl_mark m = sngl_call_enter();\n\t%s;\n}\n", ret, name, strings.Join(params, ", "), fnT, call))
	c.file.helper("sngl_release", "static void sngl_release(void *ctx) {\n\tsngl_closure_release(ctx);\n}\n")
	// The closure is kept as it crosses: C holds it past the turn that
	// made it, until it releases it.
	return name, c.heapCopy(fnT, c.keep(valueType, value))
}

// Bodiless reports whether fn is a function with no body and nothing else
// behind it -- a handler lowering attached where the program wrote none --
// which no file declares.
func Bodiless(fn *ir.Func) bool {
	if len(fn.Block) > 0 || fn.Foreign.Path != "" || fn.Intrinsic != "" {
		return false
	}
	return fn.AST == nil || fn.AST.Body == nil && !fn.AST.Block.IsDefined()
}
