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
		if c.file.building[name] {
			c.file.reentered[name] = true
		}
		return name
	}
	if c.file.building == nil {
		c.file.building, c.file.reentered = map[string]bool{}, map[string]bool{}
	}
	c.file.building[name] = true
	defer delete(c.file.building, name)
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
	case t.Kind == ir.TypeList, t.Kind == ir.TypeMap:
		var body string
		switch op {
		case "keep":
			body = c.copyBody(t)
			if shares(t) {
				body = "\tif (!" + shareCall(t, "v") + ") {\n" + indent(body) + "\t}\n"
			}
		case "drop":
			body = c.elemsBody(t, op)
			for _, buf := range buffers(t) {
				body += "\tsngl_drop(v." + buf + ");\n"
			}
			if shares(t) {
				body = "\tif (!" + letGoCall(t, "v") + ") {\n" + indent(body) + "\t}\n"
			}
		default:
			body = c.elemsBody(t, op)
		}
		b.WriteString(body)
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
	body := b.String()
	if c.file.reentered[name] {
		// A recursive type's value nests as deep as it was built, which
		// the runtime bounds rather than the C stack (sngl_deeper).
		body = strings.Replace(body, " {\n", " {\n\tsngl_deeper();\n", 1) + "\tsngl_shallower();\n"
	}
	if op == "keep" {
		body += "\treturn v;\n"
	}
	c.file.include("string.h", true)
	c.file.setHelper(name, body+"}\n")
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

// shares reports whether every holder of a list or a map of t shares its
// buffer rather than copying it (sngl_share): one whose elements may hold a
// counted block is copied, since the collector of cycles would visit it
// once for each holder.
func shares(t *ir.Type) bool {
	t = listed(t)
	return t != nil && (t.Kind == ir.TypeList || t.Kind == ir.TypeMap) && !visits(t)
}

// buffers is the fields of a list's or a map's value holding its elements.
func buffers(t *ir.Type) []string {
	if listed(t).Kind == ir.TypeMap {
		return []string{"keys", "vals"}
	}
	return []string{"data"}
}

// shareCall is whether a holder took v's buffers as they are, counting one
// more holder of each.
func shareCall(t *ir.Type, v string) string {
	if listed(t).Kind == ir.TypeMap {
		return "sngl_share_map(" + v + ".keys, " + v + ".vals, " + v + ".len)"
	}
	return "sngl_share(" + v + ".data, " + v + ".len)"
}

// letGoCall is whether a holder let go of v's buffers while another still
// holds them, leaving their elements to it.
func letGoCall(t *ir.Type, v string) string {
	if listed(t).Kind == ir.TypeMap {
		return "sngl_let_go_shared_map(" + v + ".keys, " + v + ".vals)"
	}
	return "sngl_let_go_shared(" + v + ".data)"
}

// elemsBody is the loop keeping, dropping or visiting each element of v, a
// list or a map of t: nothing where its elements hold nothing.
func (c *Context) elemsBody(t *ir.Type, op string) string {
	t = listed(t)
	holds := owns
	if op == "visit" {
		holds = visits
	}
	fields := []string{"v.data[i]"}
	if t.Kind == ir.TypeMap {
		fields = []string{"v.keys[i]", "v.vals[i]"}
	}
	var b strings.Builder
	for k, f := range fields {
		et := t.Elems[k]
		if !holds(et) {
			continue
		}
		switch op {
		case "keep":
			fmt.Fprintf(&b, "\t\t%s = %s;\n", f, c.keep(et, f))
		case "drop":
			fmt.Fprintf(&b, "\t\t%s\n", c.drop(et, f))
		default:
			fmt.Fprintf(&b, "\t\t%s\n", c.visit(et, f, "fn"))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\tfor (int64_t i = 0; i < v.len; i++) {\n" + b.String() + "\t}\n"
}

// copyBody is v, a list or a map of t, copied into buffers of its own,
// kept, each element kept, its length recorded; a map's keys with an index
// of their own (mapReindex).
func (c *Context) copyBody(t *ir.Type) string {
	t = listed(t)
	var b strings.Builder
	for k, buf := range buffers(t) {
		et := c.typeName(t.Elems[k])
		size := fmt.Sprintf("sizeof(%s) * (size_t)(v.len + 1)", et)
		if t.Kind == ir.TypeMap && buf == "keys" {
			size = c.mapKeysSize(t) + "(v.len)"
		}
		fmt.Fprintf(&b, "\t%s *%s = sngl_keep_alloc(%s);\n\tif (v.len > 0) {\n\t\tmemcpy(%s, v.%s, sizeof(%s) * (size_t)v.len);\n\t}\n\tv.%s = %s;\n\tsngl_kept_len(%s, v.len);\n", et, buf, size, buf, buf, et, buf, buf, buf)
	}
	b.WriteString("\tv.cap = v.len;\n")
	b.WriteString(c.elemsBody(t, "keep"))
	if t.Kind == ir.TypeMap {
		b.WriteString("\t" + c.mapReindex(t) + "(&v);\n")
	}
	return b.String()
}

// unshare is the function a holder's list or map of t is written through
// in place by: copied first where another holder shares its buffer, so the
// write is its own. Nothing where t is never shared.
func (c *Context) unshare(t *ir.Type) string {
	if !shares(t) {
		return ""
	}
	t = listed(t)
	tn := c.typeName(t)
	name := tn + "__unshare"
	if !c.file.helperNames[name] {
		c.file.helper(name, "")
		c.file.protos = append(c.file.protos, "static void "+name+"("+tn+" *l);")
		c.file.include("string.h", true)
		c.file.setHelper(name, "static void "+name+"("+tn+" *l) {\n\tif (!sngl_shared(l->"+buffers(t)[0]+")) {\n\t\treturn;\n\t}\n\t"+tn+" v = *l;\n\t"+letGoCall(t, "v")+";\n"+c.copyBody(t)+"\t*l = v;\n}\n")
	}
	return name
}
