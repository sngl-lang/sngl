package c

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A turn's strings are made in the arena the turn's end resets
// (design/ownership.md, *A C language target*), so a value a holder keeps --
// a var of the package's or a record's, a box, an element of one, a
// closure's environment -- is copied out of it as it is stored: kept, on the
// heap, deep where the value holds others. What the holder held before is
// dropped, which gives it back once the turn ends, since a local of the turn
// may still be reading it. The runtime knows each kept block, so a value
// never kept -- a literal, the zero a var starts at -- is dropped as nothing.

// owns reports whether a value of t holds memory a holder keeps: a string's
// bytes, a list's or a map's elements, a closure's environment, a counted
// box, or a value holding one of those.
func owns(t *ir.Type) bool { return ownsSeen(t, map[ir.Symbol]bool{}, true) }

// visits reports whether a value of t may hold a counted box, which the
// collector of cycles visits: one itself, a closure's environment, or a
// value holding one of those.
func visits(t *ir.Type) bool { return ownsSeen(t, map[ir.Symbol]bool{}, false) }

// ownsSeen is owns, or visits where strs is false, which a string's bytes
// do not answer.
func ownsSeen(t *ir.Type, seen map[ir.Symbol]bool, strs bool) bool {
	t = listed(t)
	if t == nil {
		return false
	}
	if textual(t) {
		return strs
	}
	switch t.Kind {
	case ir.TypeString:
		return strs
	case ir.TypeFunc:
		return true
	case ir.TypeList:
		return strs || ownsSeen(t.Elems[0], seen, strs)
	case ir.TypeMap:
		return strs || ownsSeen(t.Elems[0], seen, strs) || ownsSeen(t.Elems[1], seen, strs)
	case ir.TypeOption:
		return len(t.Elems) == 1 && ownsSeen(t.Elems[0], seen, strs)
	case ir.TypeStruct:
		sd, ok := t.Decl.(*ir.StructDef)
		if ok && sd.Counted {
			return true
		}
		if !ok || sd.RefBox || !isProgramStruct(t) {
			return false
		}
		if seen[sd] {
			return true
		}
		seen[sd] = true
		for _, f := range sd.Fields {
			if ownsSeen(f.Type, seen, strs) {
				return true
			}
		}
	case ir.TypeEnum:
		ed := ir.PayloadEnum(t)
		if ed == nil || seen[ed] {
			return ed != nil
		}
		seen[ed] = true
		for _, m := range ed.Members {
			if ownsSeen(ir.MemberPayload(t, m.Name), seen, strs) {
				return true
			}
		}
	}
	return false
}

// Keep is v, a value of t, as a holder keeps it.
func (c *Context) Keep(t *ir.Type, v string) string { return c.keep(t, v) }

func (c *Context) keep(t *ir.Type, v string) string {
	if isCounted(t) {
		return "((" + c.typeName(t) + ")sngl_counted_retain(" + v + "))"
	}
	if !owns(t) || c.isPointer(t) {
		return v
	}
	if textual(t) || t.Kind == ir.TypeString {
		return "sngl_string_keep(" + v + ")"
	}
	return c.ownHelper(t, "keep") + "(" + v + ")"
}

// drop is the statement giving back what v, a value of t a holder held,
// kept: nothing where it keeps nothing.
func (c *Context) drop(t *ir.Type, v string) string {
	if isCounted(t) {
		return "sngl_counted_release(" + v + ");"
	}
	if !owns(t) || c.isPointer(t) {
		return ""
	}
	if textual(t) || t.Kind == ir.TypeString {
		return "sngl_drop((" + v + ").ptr);"
	}
	return c.ownHelper(t, "drop") + "(" + v + ");"
}

// visit is the statement handing fn, a sngl_visit, each counted box v, a
// value of t a holder holds, holds: nothing where it can hold none.
func (c *Context) visit(t *ir.Type, v, fn string) string {
	switch {
	case isCounted(t):
		return "if ((" + v + ") != NULL) {\n\t\t" + fn + "(" + v + ");\n\t}"
	case !visits(t) || c.isPointer(t):
		return ""
	case listed(t).Kind == ir.TypeFunc:
		return "sngl_env_visit((" + v + ").env, " + fn + ");"
	}
	return c.ownHelper(t, "visit") + "(" + v + ", " + fn + ");"
}

// ownHelper is the function the file defines to keep, drop or visit a value
// of t,
// declared ahead, since a recursive type's reaches it again.
func (c *Context) ownHelper(t *ir.Type, op string) string {
	t = listed(t)
	tn := c.typeName(t)
	name := tn + "__" + op
	if c.file.helperNames[name] {
		return name
	}
	sig := fmt.Sprintf("static %s %s(%s v)", tn, name, tn)
	switch op {
	case "drop":
		sig = fmt.Sprintf("static void %s(%s v)", name, tn)
	case "visit":
		sig = fmt.Sprintf("static void %s(%s v, sngl_visit fn)", name, tn)
	}
	c.file.helper(name, "")
	c.file.protos = append(c.file.protos, sig+";")
	var b strings.Builder
	b.WriteString(sig + " {\n")
	holds := owns
	if op == "visit" {
		holds = visits
	}
	each := func(ft *ir.Type, f string) {
		if !holds(ft) {
			return
		}
		switch op {
		case "keep":
			fmt.Fprintf(&b, "\t%s = %s;\n", f, c.keep(ft, f))
		case "drop":
			fmt.Fprintf(&b, "\t%s\n", c.drop(ft, f))
		default:
			fmt.Fprintf(&b, "\t%s\n", c.visit(ft, f, "fn"))
		}
	}
	switch {
	case t.Kind == ir.TypeFunc:
		// The environment knows what it captured (sngl_env).
		switch op {
		case "keep":
			b.WriteString("\tv.env = sngl_env_keep(v.env);\n")
		case "drop":
			b.WriteString("\tsngl_env_drop(v.env);\n")
		default:
			b.WriteString("\tsngl_env_visit(v.env, fn);\n")
		}
	case t.Kind == ir.TypeList:
		el := c.typeName(t.Elems[0])
		if op == "keep" {
			fmt.Fprintf(&b, "\t%s *data = sngl_keep_alloc(sizeof(%s) * (size_t)(v.len + 1));\n\tif (v.len > 0) {\n\t\tmemcpy(data, v.data, sizeof(%s) * (size_t)v.len);\n\t}\n\tv.data = data;\n\tv.cap = v.len;\n", el, el, el)
		}
		if holds(t.Elems[0]) {
			b.WriteString("\tfor (int64_t i = 0; i < v.len; i++) {\n")
			each(t.Elems[0], "v.data[i]")
			b.WriteString("\t}\n")
		}
		if op == "drop" {
			b.WriteString("\tsngl_drop(v.data);\n")
		}
	case t.Kind == ir.TypeMap:
		kt, vt := c.typeName(t.Elems[0]), c.typeName(t.Elems[1])
		if op == "keep" {
			fmt.Fprintf(&b, "\t%s *keys = sngl_keep_alloc(sizeof(%s) * (size_t)(v.len + 1));\n\t%s *vals = sngl_keep_alloc(sizeof(%s) * (size_t)(v.len + 1));\n\tif (v.len > 0) {\n\t\tmemcpy(keys, v.keys, sizeof(%s) * (size_t)v.len);\n\t\tmemcpy(vals, v.vals, sizeof(%s) * (size_t)v.len);\n\t}\n\tv.keys = keys;\n\tv.vals = vals;\n\tv.cap = v.len;\n", kt, kt, vt, vt, kt, vt)
		}
		if holds(t.Elems[0]) || holds(t.Elems[1]) {
			b.WriteString("\tfor (int64_t i = 0; i < v.len; i++) {\n")
			each(t.Elems[0], "v.keys[i]")
			each(t.Elems[1], "v.vals[i]")
			b.WriteString("\t}\n")
		}
		if op == "drop" {
			b.WriteString("\tsngl_drop(v.keys);\n\tsngl_drop(v.vals);\n")
		}
	case t.Kind == ir.TypeOption:
		b.WriteString("\tif (v.ok) {\n")
		each(t.Elems[0], "v.value")
		b.WriteString("\t}\n")
	case ir.PayloadEnum(t) != nil:
		b.WriteString("\tswitch (v.tag) {\n")
		for i, m := range ir.PayloadEnum(t).Members {
			if pt := ir.MemberPayload(t, m.Name); holds(pt) {
				fmt.Fprintf(&b, "\tcase %d:\n", i)
				each(pt, "v."+Local(m.Name))
				b.WriteString("\t\tbreak;\n")
			}
		}
		b.WriteString("\t}\n")
	case t.Kind == ir.TypeStruct:
		for _, f := range t.Decl.(*ir.StructDef).Fields {
			each(f.Type, "v."+Local(f.Name))
		}
	}
	if op == "keep" {
		b.WriteString("\treturn v;\n")
	}
	b.WriteString("}\n")
	c.file.include("string.h", true)
	c.file.setHelper(name, b.String())
	return name
}

// holder reports whether target, what an assignment writes, is held past the
// turn: a var of the package's or of a record's, a box, or a part of one.
// A local or a parameter is the turn's.
func (c *Context) holder(target ir.Expr) bool {
	for {
		switch x := target.(type) {
		case *ir.Ident:
			v, ok := x.Sym.(*ir.Var)
			if !ok || c.refs[x.Name] {
				return false
			}
			if c.isPackageVar(v) {
				return true
			}
			if c.locals[x.Name] {
				return false
			}
			if c.State != nil {
				_, held := c.State(x, false)
				return held
			}
			return false
		case *ir.Select:
			if isBox(x.Operand.ExprType()) || x.Field == "value" && x.Operand.ExprType() != nil && x.Operand.ExprType().Kind == ir.TypeRef {
				return true
			}
			target = x.Operand
		case *ir.Index:
			target = x.Operand
		case *ir.Unary:
			return x.Op == ast.UnaryDeref
		default:
			return false
		}
	}
}

// store is target written with value, as a holder keeps it: the value kept,
// and what it held before dropped. The value is kept before the target's
// address is taken: evaluating it may grow the list or map the target is
// in, which moves its elements.
func (c *Context) store(t *ir.Type, target, value string) string {
	n := c.file.next()
	slot, kept := fmt.Sprintf("__slot%d", n), fmt.Sprintf("__kept%d", n)
	tn := c.typeName(t)
	return "{\n\t" + tn + " " + kept + " = " + c.keep(t, value) + ";\n\t" + tn + " *" + slot + " = &(" + target + ");\n\t" + c.drop(t, "*"+slot) + "\n\t*" + slot + " = " + kept + ";\n}"
}

// frees is the statements giving back self, a record's box its destroy is
// handed (ir.Func.Frees): what each field kept, a counted box let go of,
// then the record. Each is
// dropped, so freed as the turn ends, which a handler still running reads
// it until.
func (c *Context) frees(self *ir.Param) []string {
	sd, ok := self.Type.Decl.(*ir.StructDef)
	if !ok || !sd.RefBox {
		return []string{c.fail("giving back %s, which is no box", self.Type)}
	}
	ref := &ir.Ident{Name: self.Name, Sym: self, Type: self.Type, Synthesized: true}
	var out []string
	for _, f := range sd.Fields {
		if d := c.drop(f.Type, c.EvalExpr(&ir.Select{Operand: ref, Field: f.Name, Type: f.Type})); d != "" {
			out = append(out, d)
		}
	}
	return append(out, "sngl_drop("+c.EvalExpr(ref)+");")
}

// isCounted reports whether t is a box every holder of it shares, counting
// its holders (ir.StructDef.Counted): a holder retains it as it keeps it and
// releases it as it drops it, and the last to let go gives it back.
func isCounted(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd.Counted
}
