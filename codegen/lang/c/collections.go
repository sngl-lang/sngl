package c

import (
	"duckfam.us/sngl/codegen/irwalk"
	"fmt"
	"regexp"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A list is a struct per element type, `sngl_list_<elem>`: its elements,
// how many there are and how many fit. It is passed by value and shares its
// elements as a Go slice does, so a copy SNGL's value semantics need is the
// one lowering writes out (ir.ValueCopyIntrinsic). Each operation is a
// function defined for the element type the first time the program uses it.
//
// A list made is the turn's, its elements in the arena as a string's bytes
// are, and a holder keeps a copy (keep.go). A holder's own list grows in
// kept memory, giving back what it outgrew; so does a holder's map.
//
// An option is a struct per value type, `sngl_opt_<value>`: whether it holds
// one, and the value.

// listed is t, or for an iter[T] the list[T] C holds it as. A sequence a
// loop walks where it is written is counted and never built (countedHead);
// one held or passed is its elements, as a list where an iter is read is the
// list (Conversion), so either is walked the same.
func listed(t *ir.Type) *ir.Type {
	if t != nil && t.Kind == ir.TypeIter && len(t.Elems) == 1 {
		return &ir.Type{Kind: ir.TypeList, Elems: t.Elems}
	}
	return t
}

// listType names list[elem]'s C struct, defined in the file.
func (c *Context) listType(t *ir.Type) string {
	elem := t.Elems[0]
	et := c.typeName(elem)
	name := "sngl_list_" + typeToken(elem)
	// Its elements are pointed to, so the element type need only be declared.
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\t%s *data;\n\tint64_t len;\n\tint64_t cap;\n} %s;\n", name, et, name))
	return name
}

// optType names option[value]'s C struct, defined in the file.
func (c *Context) optType(t *ir.Type) string {
	elem := t.Elems[0]
	et := c.typeName(elem)
	// An option of what C holds by pointer -- a widget, a record, a C
	// object -- is the pointer, NULL where it holds none.
	if strings.HasSuffix(et, "*") || cHandle(elem) {
		return et
	}
	name := "sngl_opt_" + typeToken(elem)
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\tbool ok;\n\t%s value;\n} %s;\n", name, et, name), et)
	return name
}

// cHandle reports whether t is a C type its declaration says is a typedef
// of a pointer (`#[c.native(…, pointer)]`), as GLib's gpointer is: an option
// of it is the pointer, NULL where it holds none.
func cHandle(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd.Foreign.Path == ir.CPath && sd.Foreign.CPointer
}

// listOp is the function op of list type t, defined from its body the first
// time it is named. The body reads the list type as L and the element type
// as T.
func (c *Context) listOp(t *ir.Type, op, sig, body string) string {
	l := c.listType(t)
	el := c.typeName(t.Elems[0])
	name := l + "_" + op
	text := placeholder.ReplaceAllStringFunc("static "+sig+" {\n"+body+"}\n", func(w string) string {
		return map[string]string{"L": l, "T": el, "NAME": name}[w]
	})
	c.file.include("stdlib.h", true)
	c.file.include("string.h", true)
	c.file.helper(name, text)
	return name
}

// placeholder is a word a listOp body stands for a type or its own name by.
var placeholder = regexp.MustCompile(`\b(L|T|NAME)\b`)

// listOf is the list of type t holding elems.
func (c *Context) listOf(t *ir.Type, elems []string) string {
	l := c.listType(t)
	if len(elems) == 0 {
		return "((" + l + "){0})"
	}
	of := c.listOp(t, "of", "L NAME(int64_t n, T const *xs)", `	L l = {sngl_alloc(sizeof(T) * (size_t)n), n, n};
	memcpy(l.data, xs, sizeof(T) * (size_t)n);
	return l;
`)
	return of + "(" + fmt.Sprint(len(elems)) + ", (" + c.typeName(t.Elems[0]) + "[]){" + strings.Join(elems, ", ") + "})"
}

func (c *Context) ListLit(n *ir.ListLit, elems []string) string {
	t := listed(n.Type)
	if t == nil || t.Kind != ir.TypeList {
		return c.fail("a list literal of %s", n.Type)
	}
	// A spread joins the list it reads to what is written around it, each
	// run of elements a list of its own.
	var parts, run []string
	for i, e := range n.Elems {
		sp, ok := e.(*ir.Spread)
		if !ok {
			run = append(run, elems[i])
			continue
		}
		if st := sp.Operand.ExprType(); st == nil || st.Kind != ir.TypeList || !st.Elems[0].Equal(t.Elems[0]) {
			return c.fail("a spread of %s into %s", st, n.Type)
		}
		if len(run) > 0 {
			parts = append(parts, c.listOf(t, run))
			run = nil
		}
		parts = append(parts, elems[i])
	}
	if len(parts) == 0 {
		return c.listOf(t, elems)
	}
	if len(run) > 0 {
		parts = append(parts, c.listOf(t, run))
	}
	cat := c.listOp(t, "cat", "L NAME(L a, L b)", `	L l = {sngl_alloc(sizeof(T) * (size_t)(a.len + b.len + 1)), a.len + b.len, a.len + b.len};
	if (a.len > 0) {
		memcpy(l.data, a.data, sizeof(T) * (size_t)a.len);
	}
	if (b.len > 0) {
		memcpy(l.data + a.len, b.data, sizeof(T) * (size_t)b.len);
	}
	return l;
`)
	// A spread alone is a copy, which a list written into must be.
	out := cat + "(" + parts[0] + ", " + c.listOf(t, nil) + ")"
	if len(parts) > 1 {
		out = parts[0]
		for _, p := range parts[1:] {
			out = cat + "(" + out + ", " + p + ")"
		}
	}
	return out
}

// listGrow is the function making room for n more elements in a list of t:
// in the arena, or for a holder's list, kept, giving back what it outgrew.
func (c *Context) listGrow(t *ir.Type, kept bool) string {
	op, alloc, drop := "grow", "sngl_alloc", ""
	if kept {
		op, alloc, drop = "grow_kept", "sngl_keep_alloc", "\tsngl_drop(l->data);\n"
	}
	return c.listOp(t, op, "void NAME(L *l, int64_t n)", `	if (l->len + n <= l->cap) {
		return;
	}
	int64_t cap = l->cap * 2;
	if (cap < l->len + n) {
		cap = l->len + n;
	}
	if (cap < 4) {
		cap = 4;
	}
	T *data = `+alloc+`(sizeof(T) * (size_t)cap);
	if (l->len > 0) {
		memcpy(data, l->data, sizeof(T) * (size_t)l->len);
	}
`+drop+`	l->data = data;
	l->cap = cap;
`)
}

// kept is the suffix naming the variant of an operation writing a list or a
// map recv a holder holds.
func (c *Context) kept(recv ir.Expr) (string, bool) {
	if c.holder(recv) {
		return "_kept", true
	}
	return "", false
}

// listAt is the element of list xs of type t at i, which stops the program
// past the end (sngl_panic) as every target does: no handler catches it.
func (c *Context) listAt(t *ir.Type, xs, i string) string {
	at := c.listOp(t, "at", "T *NAME(L l, int64_t i)", `	if (i < 0 || i >= l.len) {
		sngl_panic("index out of range");
	}
	return &l.data[i];
`)
	return "(*" + at + "(" + xs + ", " + i + "))"
}

func (c *Context) Index(n *ir.Index, operand, idx string) string {
	t := n.Operand.ExprType()
	if t != nil && t.Kind == ir.TypeMap {
		return c.mapIndex(t, operand, idx)
	}
	if t == nil || t.Kind != ir.TypeList {
		return c.fail("indexing a %s", t)
	}
	return c.listAt(t, operand, idx)
}

// MutTargetIndex is an element written, through the pointer listAt gives.
// A key a holder's map takes is kept with it.
func (c *Context) MutTargetIndex(n *ir.Index, operand, idx string) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
		if c.holder(n.Operand) {
			return "(*" + c.mapSlotKept(t) + "(&(" + operand + "), " + idx + "))"
		}
		return "(*" + c.mapSlot(t) + "(&(" + operand + "), " + idx + "))"
	}
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeList && c.holder(n.Operand) {
		if u := c.unshare(t); u != "" {
			at := c.listOp(t, "at_kept", "T *NAME(L *l, int64_t i)", "\t"+u+"(l);\n\tif (i < 0 || i >= l->len) {\n\t\tsngl_panic(\"index out of range\");\n\t}\n\treturn &l->data[i];\n")
			return "(*" + at + "(&(" + operand + "), " + idx + "))"
		}
	}
	return c.Index(n, operand, idx)
}

// seqIntrinsic renders a sngl:seq sequence held or passed rather than walked
// where it is written: the list of what it counts (listed).
func (c *Context) seqIntrinsic(n *ir.Call) (string, bool) {
	var start, end, by string
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	switch {
	case n.Func.Intrinsic == "seq.count" && len(n.Args) == 1:
		start, end, by = "0", arg(0), "1"
	case n.Func.Intrinsic == "seq.range" && len(n.Args) == 2:
		start, end, by = arg(0), arg(1), "1"
	case n.Func.Intrinsic == "seq.step" && len(n.Args) == 3:
		start, end, by = arg(0), arg(1), arg(2)
	default:
		return "", false
	}
	// A step of 0 counts nothing, as it does everywhere else.
	seq := c.listOp(listed(n.Type), "seq", "L NAME(int64_t start, int64_t end, int64_t by)", `	int64_t n = 0;
	if (by > 0 && end > start) {
		n = (end - start + by - 1) / by;
	} else if (by < 0 && start > end) {
		n = (start - end - by - 1) / -by;
	}
	L l = {sngl_alloc(sizeof(T) * (size_t)(n + 1)), n, n};
	for (int64_t i = 0; i < n; i++) {
		l.data[i] = start + i * by;
	}
	return l;
`)
	return seq + "(" + start + ", " + end + ", " + by + ")", true
}

// listIntrinsic renders a call of one of sngl:builtin's list methods, the
// list its first argument.
func (c *Context) listIntrinsic(n *ir.Call) (string, bool) {
	id := n.Func.Intrinsic
	if !strings.HasPrefix(id, "list.") || len(n.Args) == 0 {
		return "", false
	}
	t := n.Args[0].Value.ExprType()
	if t != nil && t.Kind == ir.TypeRef {
		t = t.Elems[0]
	}
	if t == nil || t.Kind != ir.TypeList {
		return c.fail("%s of a %s", id, t), true
	}
	recv := n.Args[0].Value
	if u, ok := recv.(*ir.Unary); ok && u.Op == ast.UnaryAddr {
		recv = u.Operand
	}
	xs := c.EvalExpr(recv)
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	// A holder's list written in place is reached as a write is, so each
	// list it is in is its own first (unshare), and copies its buffer where
	// another holder shares it, recording the length it leaves.
	_, held := c.kept(recv)
	before, after := "", ""
	if held {
		xs = irwalk.EvalMutTarget(c, recv)
		if u := c.unshare(t); u != "" {
			before, after = "\t"+u+"(l);\n", "\tsngl_kept_len(l->data, l->len);\n"
		}
	}
	switch id {
	case "list.length":
		return "(" + xs + ").len", true
	case "list.push":
		suffix, held := c.kept(recv)
		grow := c.listGrow(t, held)
		push := c.listOp(t, "push"+suffix, "void NAME(L *l, T v)", before+"\t"+grow+"(l, 1);\n\tl->data[l->len++] = v;\n"+after)
		return push + "(&(" + xs + "), " + c.keptArg(t, recv, arg(1)) + ")", true
	case "list.clear":
		// What a holder's list held, it gives back.
		if held {
			drop := ""
			if owns(t.Elems[0]) {
				drop = "\tfor (int64_t i = 0; i < l->len; i++) {\n\t\t" + c.drop(t.Elems[0], "l->data[i]") + "\n\t}\n"
			}
			clear := c.listOp(t, "clear_kept", "void NAME(L *l)", before+drop+"\tl->len = 0;\n"+after)
			return clear + "(&(" + xs + "))", true
		}
		return "((" + xs + ").len = 0)", true
	case "list.remove":
		suffix, held := c.kept(recv)
		drop := ""
		if held && owns(t.Elems[0]) {
			drop = "\t" + c.drop(t.Elems[0], "l->data[i]") + "\n"
		}
		rm := c.listOp(t, "remove"+suffix, "void NAME(L *l, int64_t i)", `	if (i < 0 || i >= l->len) {
		return;
	}
`+before+drop+`	memmove(&l->data[i], &l->data[i + 1], sizeof(T) * (size_t)(l->len - i - 1));
	l->len--;
`+after)
		return rm + "(&(" + xs + "), " + arg(1) + ")", true
	case "list.insert":
		suffix, held := c.kept(recv)
		grow := c.listGrow(t, held)
		// The holder's copy, kept as the argument was, goes back where the
		// index is out of range and nothing takes it.
		drop := ""
		if held && owns(t.Elems[0]) {
			drop = "\t\t" + c.drop(t.Elems[0], "v") + "\n"
		}
		ins := c.listOp(t, "insert"+suffix, "void NAME(L *l, int64_t i, T v)", `	if (i < 0 || i > l->len) {
`+drop+`		return;
	}
`+before+`	`+grow+`(l, 1);
	memmove(&l->data[i + 1], &l->data[i], sizeof(T) * (size_t)(l->len - i));
	l->data[i] = v;
	l->len++;
`+after)
		return ins + "(&(" + xs + "), " + arg(1) + ", " + c.keptArg(t, recv, arg(2)) + ")", true
	case "list.reverse":
		rev := c.listOp(t, "reverse", "L NAME(L l)", `	L out = {sngl_alloc(sizeof(T) * (size_t)(l.len + 1)), l.len, l.len};
	for (int64_t i = 0; i < l.len; i++) {
		out.data[i] = l.data[l.len - 1 - i];
	}
	return out;
`)
		return rev + "(" + xs + ")", true
	case "list.slice":
		sl := c.listOp(t, "slice", "L NAME(L l, int64_t start, int64_t end)", `	if (start < 0) {
		start = 0;
	}
	if (end > l.len) {
		end = l.len;
	}
	if (end < start) {
		end = start;
	}
	L out = {sngl_alloc(sizeof(T) * (size_t)(end - start + 1)), end - start, end - start};
	if (end > start) {
		memcpy(out.data, &l.data[start], sizeof(T) * (size_t)(end - start));
	}
	return out;
`)
		return sl + "(" + xs + ", " + arg(1) + ", " + arg(2) + ")", true
	case "list.indexOf":
		eq := c.equal(t.Elems[0], "l.data[i]", "v")
		idx := c.listOp(t, "index_of", "int64_t NAME(L l, T v)", "\tfor (int64_t i = 0; i < l.len; i++) {\n\t\tif ("+bare(eq)+") {\n\t\t\treturn i;\n\t\t}\n\t}\n\treturn -1;\n")
		return idx + "(" + xs + ", " + arg(1) + ")", true
	case "list.join":
		join := c.listOp(t, "join", "sngl_string NAME(L l, sngl_string sep)", `	sngl_string out = SNGL_STR("");
	for (int64_t i = 0; i < l.len; i++) {
		if (i > 0) {
			out = sngl_concat(out, sep);
		}
		out = sngl_concat(out, l.data[i]);
	}
	return out;
`)
		return join + "(" + xs + ", " + arg(1) + ")", true
	case "list.filter":
		fnT := c.fnType(n.Args[1].Value.ExprType())
		grow := c.listGrow(t, false)
		f := c.listOp(t, "filter", "L NAME(L l, "+fnT+" f)", `	L out = {0};
	for (int64_t i = 0; i < l.len; i++) {
		if (f.fn(f.env, l.data[i])) {
			`+grow+`(&out, 1);
			out.data[out.len++] = l.data[i];
		}
	}
	return out;
`)
		return f + "(" + xs + ", " + arg(1) + ")", true
	case "list.map":
		ft := n.Args[1].Value.ExprType()
		if ft == nil || ft.Kind != ir.TypeFunc {
			return c.fail("list.map of a %s", ft), true
		}
		out := ir.ListOf(ft.Sig.Return)
		ol := c.listType(out)
		fnT := c.fnType(ft)
		name := c.listType(t) + "_map_" + typeToken(ft.Sig.Return)
		c.file.helper(name, fmt.Sprintf(`static %s %s(%s l, %s f) {
	%s out = {sngl_alloc(sizeof(%s) * (size_t)(l.len + 1)), l.len, l.len};
	for (int64_t i = 0; i < l.len; i++) {
		out.data[i] = f.fn(f.env, l.data[i]);
	}
	return out;
}
`, ol, name, c.listType(t), fnT, ol, c.typeName(ft.Sig.Return)))
		return name + "(" + xs + ", " + arg(1) + ")", true
	case "list.sort", "list.sortBy":
		less := c.less(t.Elems[0], "x", "out[j - 1]")
		sig := "void NAME(L *l)"
		call := ")"
		if id == "list.sortBy" {
			fnT := c.fnType(n.Args[1].Value.ExprType())
			sig = "void NAME(L *l, " + fnT + " before)"
			less = "before.fn(before.env, x, out[j - 1])"
			call = ", " + arg(1) + ")"
		}
		// An insertion sort: stable, as both promise.
		name := strings.TrimPrefix(id, "list.")
		if before != "" {
			name += "_kept"
		}
		sort := c.listOp(t, name, sig, before+`	T *out = l->data;
	for (int64_t i = 1; i < l->len; i++) {
		T x = out[i];
		int64_t j = i;
		while (j > 0 && `+less+`) {
			out[j] = out[j - 1];
			j--;
		}
		out[j] = x;
	}
`)
		return sort + "(&(" + xs + ")" + call, true
	}
	return c.fail("the intrinsic %s", id), true
}

// less is whether a orders before b, two values of the ordered type t.
func (c *Context) less(t *ir.Type, a, b string) string {
	if t.Kind == ir.TypeString {
		return "sngl_string_cmp(" + a + ", " + b + ") < 0"
	}
	return "(" + a + " < " + b + ")"
}

// listLoop is the head of a loop over a list: the list held once, and each
// element bound as the loop's variable, beside its index where the loop
// names two.
func (c *Context) listLoop(n *ir.For) string {
	iter := n.Iter
	if conv, ok := iter.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeIter {
		iter = conv.Operand
	}
	t := listed(iter.ExprType())
	if t == nil || t.Kind != ir.TypeList {
		return c.fail("a loop over a %s", t) + "; {"
	}
	l := c.listType(t)
	v := fmt.Sprintf("__loop%d", c.file.next())
	head := "for (struct { " + l + " l; int64_t i; } " + v + " = {" + c.EvalExpr(iter) + ", 0}; " + v + ".i < " + v + ".l.len; " + v + ".i++) {"
	elem := c.typeName(t.Elems[0])
	switch {
	case n.IterKind == ir.IterIndexed && n.Value != "":
		head += " int64_t " + Local(n.Key) + " = " + v + ".i; " + elem + " " + Local(n.Value) + " = " + v + ".l.data[" + v + ".i];"
	case n.Key != "":
		head += " " + elem + " " + Local(n.Key) + " = " + v + ".l.data[" + v + ".i];"
	}
	return head
}

// option renders the conversions between an option and its value, and
// reports whether n was one.
func (c *Context) option(n *ir.Conversion) (string, bool) {
	switch {
	case ir.IsOptionUnwrap(n):
		if c.ptrOption(n.Operand.ExprType()) {
			return c.EvalExpr(n.Operand), true
		}
		return "(" + c.EvalExpr(n.Operand) + ").value", true
	case ir.IsOptionWrap(n):
		if c.ptrOption(n.Type) {
			return c.EvalExpr(n.Operand), true
		}
		return "((" + c.optType(n.Type) + "){true, " + c.EvalExpr(n.Operand) + "})", true
	}
	if lit, ok := n.Operand.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull && n.Type != nil {
		if c.ptrOption(n.Type) {
			return "NULL", true
		}
		switch n.Type.Kind {
		case ir.TypeOption, ir.TypeList, ir.TypeMap, ir.TypeFunc:
			return "((" + c.typeName(n.Type) + "){0})", true
		}
	}
	return "", false
}

// optionCompare is a comparison with null, which asks whether an option holds
// a value, of two options, or of an option with a value, and reports whether
// n was one.
func (c *Context) optionCompare(n *ir.Binary, left, right string) (string, bool) {
	if n.Op != ast.BinEq && n.Op != ast.BinNeq {
		return "", false
	}
	not := ""
	if n.Op == ast.BinEq {
		not = "!"
	}
	lt, rt := n.Left.ExprType(), n.Right.ExprType()
	switch {
	case ir.IsNullLiteral(n.Right) && c.ptrOption(lt):
		return "(" + left + " " + n.Op.String() + " NULL)", true
	case ir.IsNullLiteral(n.Left) && c.ptrOption(rt):
		return "(" + right + " " + n.Op.String() + " NULL)", true
	case c.ptrOption(lt) || c.ptrOption(rt):
		return "(" + left + " " + n.Op.String() + " " + right + ")", true
	case ir.IsNullLiteral(n.Right) && lt != nil && lt.Kind == ir.TypeOption:
		return not + "(" + left + ").ok", true
	case ir.IsNullLiteral(n.Left) && rt != nil && rt.Kind == ir.TypeOption:
		return not + "(" + right + ").ok", true
	case lt != nil && lt.Kind == ir.TypeOption && rt != nil && rt.Kind == ir.TypeOption:
		eq := c.equal(lt, left, right)
		if n.Op == ast.BinNeq {
			return "!" + eq, true
		}
		return eq, true
	case lt != nil && lt.Kind == ir.TypeOption && rt != nil && rt.Kind != ir.TypeOption:
		eq := "((" + left + ").ok && " + c.equal(lt.Elems[0], "("+left+").value", right) + ")"
		if n.Op == ast.BinNeq {
			return "!" + eq, true
		}
		return eq, true
	case rt != nil && rt.Kind == ir.TypeOption && lt != nil && lt.Kind != ir.TypeOption:
		eq := "((" + right + ").ok && " + c.equal(rt.Elems[0], left, "("+right+").value") + ")"
		if n.Op == ast.BinNeq {
			return "!" + eq, true
		}
		return eq, true
	}
	return "", false
}

// copyValue is v, a value of t, copied so that nothing it holds is shared
// with v's: what lowering writes where SNGL's value semantics need one
// (ir.ValueCopyIntrinsic). A list's elements are copied to a buffer of its
// own, each copied in turn where it shares anything, and a struct field by
// field, through a function the file defines per type.
func (c *Context) copyValue(t *ir.Type, v string) string {
	if t == nil || ir.SharesNothing(t) {
		return v
	}
	switch t.Kind {
	case ir.TypeList:
		elem := c.copyValue(t.Elems[0], "l.data[i]")
		return c.listOp(t, "copy", "L NAME(L l)", `	L out = {sngl_alloc(sizeof(T) * (size_t)(l.len + 1)), l.len, l.len};
	for (int64_t i = 0; i < l.len; i++) {
		out.data[i] = `+elem+`;
	}
	return out;
`) + "(" + v + ")"
	case ir.TypeMap:
		kc, vc := c.copyValue(t.Elems[0], "m.keys[i]"), c.copyValue(t.Elems[1], "m.vals[i]")
		size, reindex := c.mapKeysSize(t), c.mapReindex(t)
		return c.mapOp(t, "copy", "M NAME(M m)", `	M out = {sngl_alloc(`+size+`(m.len)), sngl_alloc(sizeof(V) * (size_t)(m.len + 1)), m.len, m.len, NULL};
	for (int64_t i = 0; i < m.len; i++) {
		out.keys[i] = `+kc+`;
		out.vals[i] = `+vc+`;
	}
	`+reindex+`(&out);
	return out;
`) + "(" + v + ")"
	case ir.TypeEnum:
		if ed := ir.PayloadEnum(t); ed != nil {
			return c.enumCopy(t, ed, v)
		}
		return v
	case ir.TypeOption:
		if c.ptrOption(t) {
			return v
		}
		ot := c.optType(t)
		name := ot + "_copy"
		c.file.helper(name, "static "+ot+" "+name+"("+ot+" o) {\n\tif (o.ok) {\n\t\to.value = "+c.copyValue(t.Elems[0], "o.value")+";\n\t}\n\treturn o;\n}\n")
		return name + "(" + v + ")"
	case ir.TypeStruct:
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok || sd.RefBox || !isProgramStruct(t) {
			return v
		}
		st := c.typeName(t)
		name := st + "__copy"
		if !c.file.helperNames[name] {
			// Its fields' copies are defined after the slot it holds, which
			// a recursive struct needs before them: it is declared ahead.
			c.file.helper(name, "")
			c.file.protos = append(c.file.protos, fmt.Sprintf("static %s %s(%s s);", st, name, st))
			var b strings.Builder
			fmt.Fprintf(&b, "static %s %s(%s s) {\n", st, name, st)
			for _, f := range sd.Fields {
				if !ir.SharesNothing(f.Type) {
					fmt.Fprintf(&b, "\ts.%s = %s;\n", Local(f.Name), c.copyValue(f.Type, "s."+Local(f.Name)))
				}
			}
			b.WriteString("\treturn s;\n}\n")
			c.file.setHelper(name, b.String())
		}
		return name + "(" + v + ")"
	}
	return c.fail("copying a %s", t)
}

// A map is a struct per key and value type, `sngl_map_<key>__<value>`: its
// keys and values side by side in the order first set, found by the keys'
// equality. A loop walks it in that order, where Go's walks it in none.
//
// Past eight keys a map finds one through an index (idx): an open-addressed
// table of positions in keys, probed from the key's hash (hash), which agrees
// with the keys' equality. It is the tail of the keys' own block, so it is
// allocated, kept, shared, dropped and given back with the keys, wherever
// they are; whatever makes a keys block makes its index (mapReindex), and an
// append in place adds to it. A copy of the struct -- a loop's, a value
// before lowering copies it -- shares the block, and finds in it what its own
// length covers: a position past it is another's append, which the probe
// goes past, as it goes past a position whose key is not the one sought.
// Where idx is NULL the keys are walked.

// mapType names map[k, v]'s C struct, defined in the file.
func (c *Context) mapType(t *ir.Type) string {
	kt, vt := c.typeName(t.Elems[0]), c.typeName(t.Elems[1])
	name := "sngl_map_" + typeToken(t.Elems[0]) + "__" + typeToken(t.Elems[1])
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\t%s *keys;\n\t%s *vals;\n\tint64_t len;\n\tint64_t cap;\n\tuint32_t *idx;\n} %s;\n", name, kt, vt, name))
	return name
}

// mapOp is the function op of map type t, as listOp is a list's: its body
// reads the map type as M, the key type as K and the value type as V.
func (c *Context) mapOp(t *ir.Type, op, sig, body string) string {
	m := c.mapType(t)
	kt, vt := c.typeName(t.Elems[0]), c.typeName(t.Elems[1])
	name := m + "_" + op
	text := mapPlaceholder.ReplaceAllStringFunc("static "+sig+" {\n"+body+"}\n", func(w string) string {
		return map[string]string{"M": m, "K": kt, "V": vt, "NAME": name}[w]
	})
	c.file.include("stdlib.h", true)
	c.file.include("string.h", true)
	c.file.helper(name, text)
	return name
}

var mapPlaceholder = regexp.MustCompile(`\b(M|K|V|NAME)\b`)

// mapFind is the function finding a key's place in a map of t, -1 where it
// has none: through its index, or walking its keys where it has none. A
// slot holds a position plus one, and 0 where it is empty.
func (c *Context) mapFind(t *ir.Type) string {
	c.mapIndexRuntime()
	eq := c.equal(t.Elems[0], "m.keys[i]", "k")
	at := c.equal(t.Elems[0], "m.keys[p - 1]", "k")
	return c.mapOp(t, "find", "int64_t NAME(M m, K k)", `	if (m.idx == NULL) {
		for (int64_t i = 0; i < m.len; i++) {
			if (`+bare(eq)+`) {
				return i;
			}
		}
		return -1;
	}
	uint64_t mask = m.idx[-2];
	for (uint64_t s = `+c.hash(t.Elems[0], "k")+` & mask;; s = (s + 1) & mask) {
		uint32_t p = m.idx[s];
		if (p == 0) {
			return -1;
		}
		if ((int64_t)p <= m.len && `+at+`) {
			return (int64_t)p - 1;
		}
	}
`)
}

// mapKeysSize is the function answering how many bytes a keys block of a map
// of t holds for cap keys: the keys, and past eight of them the index's
// mask, count and slots.
func (c *Context) mapKeysSize(t *ir.Type) string {
	c.mapIndexRuntime()
	return c.mapOp(t, "keys_size", "size_t NAME(int64_t cap)", `	size_t n = sngl_mapidx_slots(cap);
	return sngl_mapidx_at(sizeof(K), cap) + (n == 0 ? 0 : sizeof(uint32_t) * (2 + n));
`)
}

// mapReindex is the function making the index of a map of t whose keys block
// is new, mapKeysSize(cap) long: every position its length covers, or none
// where cap is eight or fewer.
func (c *Context) mapReindex(t *ir.Type) string {
	put := c.mapIndexPut(t)
	return c.mapOp(t, "reindex", "void NAME(M *m)", `	size_t n = sngl_mapidx_slots(m->cap);
	if (n == 0) {
		m->idx = NULL;
		return;
	}
	uint32_t *h = (uint32_t *)(void *)((char *)m->keys + sngl_mapidx_at(sizeof(K), m->cap));
	memset(h, 0, sizeof(uint32_t) * (2 + n));
	h[0] = (uint32_t)(n - 1);
	m->idx = h + 2;
	for (int64_t i = 0; i < m->len; i++) {
		`+put+`(m, i);
	}
`)
}

// mapIndexPut is the function adding position i of a map of t to its index.
// The index holds at most half its slots, so a probe always ends: one it
// would fill past that -- only where two copies of a struct appended to one
// block -- is let go, and the map walks its keys until its keys move.
func (c *Context) mapIndexPut(t *ir.Type) string {
	return c.mapOp(t, "index_put", "void NAME(M *m, int64_t i)", `	uint64_t mask = m->idx[-2];
	if (((uint64_t)m->idx[-1] + 1) * 2 > mask + 1) {
		m->idx = NULL;
		return;
	}
	m->idx[-1]++;
	for (uint64_t s = `+c.hash(t.Elems[0], "m->keys[i]")+` & mask;; s = (s + 1) & mask) {
		if (m->idx[s] == 0) {
			m->idx[s] = (uint32_t)(i + 1);
			return;
		}
	}
`)
}

// mapIndexRuntime defines what every map's index uses: how many slots one
// of cap keys has, where it starts in the keys block, and the hashes.
func (c *Context) mapIndexRuntime() {
	if c.file.helperNames["sngl_mapidx_mix"] {
		return
	}
	c.file.include("stdint.h", true)
	c.file.include("string.h", true)
	c.file.helper("sngl_mapidx_mix", `static uint64_t sngl_mapidx_mix(uint64_t h) {
	h ^= h >> 33;
	h *= UINT64_C(0xff51afd7ed558ccd);
	h ^= h >> 33;
	h *= UINT64_C(0xc4ceb9fe1a85ec53);
	h ^= h >> 33;
	return h;
}
`)
	c.file.helper("sngl_mapidx_bytes", `static uint64_t sngl_mapidx_bytes(const void *p, size_t n) {
	const unsigned char *b = p;
	uint64_t h = UINT64_C(14695981039346656037);
	for (size_t i = 0; i < n; i++) {
		h ^= b[i];
		h *= UINT64_C(1099511628211);
	}
	return sngl_mapidx_mix(h);
}
`)
	// A value C compares with == -- a pointer, a handle -- is its bits,
	// which fit a word.
	c.file.helper("sngl_mapidx_word", `static uint64_t sngl_mapidx_word(const void *p, size_t n) {
	if (n > sizeof(uint64_t)) {
		return sngl_mapidx_bytes(p, n);
	}
	uint64_t x = 0;
	memcpy(&x, p, n);
	return sngl_mapidx_mix(x);
}
`)
	c.file.helper("sngl_mapidx_join", `static uint64_t sngl_mapidx_join(uint64_t h, uint64_t x) {
	return sngl_mapidx_mix(h ^ (x + UINT64_C(0x9e3779b97f4a7c15) + (h << 6) + (h >> 2)));
}
`)
	// Both zeros are one key, as == says; a NaN is equal to nothing, so
	// any hash serves it.
	c.file.helper("sngl_mapidx_float", `static uint64_t sngl_mapidx_float(double x) {
	if (x == 0) {
		return 0;
	}
	return sngl_mapidx_bytes(&x, sizeof x);
}
`)
	c.file.helper("sngl_mapidx_slots", `static size_t sngl_mapidx_slots(int64_t cap) {
	if (cap <= 8) {
		return 0;
	}
	size_t n = 32;
	while (n < (size_t)cap * 2) {
		n *= 2;
	}
	return n;
}
`)
	c.file.helper("sngl_mapidx_at", `static size_t sngl_mapidx_at(size_t key, int64_t cap) {
	return (key * (size_t)(cap + 1) + 7) & ~(size_t)7;
}
`)
}

// hash is the hash of v, an lvalue of type t, as a uint64_t: equal where
// equal says two values are, read the way it reads them -- a string by its
// bytes, a struct by its fields, a list by its elements, an option by
// whether it holds one and what, an enum by its member and payload, a float
// with its two zeros alike -- and anything C compares with == by its bits.
func (c *Context) hash(t *ir.Type, v string) string {
	c.mapIndexRuntime()
	str := func(s string) string { return "sngl_mapidx_bytes((" + s + ").ptr, (" + s + ").len)" }
	if textual(t) {
		return str(v)
	}
	if ed := ir.PayloadEnum(t); ed != nil {
		return c.hashHelper(t, v, func() string {
			var arms []string
			for i, m := range ed.Members {
				if pt := ir.MemberPayload(t, m.Name); pt != nil {
					arms = append(arms, "\tcase "+fmt.Sprint(i)+":\n\t\treturn sngl_mapidx_join(h, "+c.hash(pt, "a."+Local(m.Name))+");\n")
				}
			}
			return "\tuint64_t h = sngl_mapidx_mix((uint64_t)a.tag);\n\tswitch (a.tag) {\n" + strings.Join(arms, "") + "\t}\n\treturn h;\n"
		})
	}
	switch t.Kind {
	case ir.TypeString:
		return str(v)
	case ir.TypeStruct:
		if !isProgramStruct(t) || isBox(t) {
			break
		}
		sd := t.Decl.(*ir.StructDef)
		return c.hashHelper(t, v, func() string {
			var b strings.Builder
			b.WriteString("\tuint64_t h = 0;\n")
			for _, f := range sd.Fields {
				b.WriteString("\th = sngl_mapidx_join(h, " + c.hash(f.Type, "a."+Local(f.Name)) + ");\n")
			}
			b.WriteString("\treturn h;\n")
			return b.String()
		})
	case ir.TypeList:
		return c.hashHelper(t, v, func() string {
			return "\tuint64_t h = sngl_mapidx_mix((uint64_t)a.len);\n\tfor (int64_t i = 0; i < a.len; i++) {\n\t\th = sngl_mapidx_join(h, " + c.hash(t.Elems[0], "a.data[i]") + ");\n\t}\n\treturn h;\n"
		})
	case ir.TypeOption:
		if c.ptrOption(t) {
			break
		}
		return c.hashHelper(t, v, func() string {
			return "\treturn a.ok ? sngl_mapidx_join(1, " + c.hash(t.Elems[0], "a.value") + ") : 0;\n"
		})
	case ir.TypeFloat:
		return "sngl_mapidx_float(" + v + ")"
	case ir.TypeUnit:
		if !multiBase(t) {
			return "sngl_mapidx_float(" + v + ")"
		}
	case ir.TypeInt, ir.TypeBool:
		return "sngl_mapidx_mix((uint64_t)(" + v + "))"
	case ir.TypeEnum:
		return "sngl_mapidx_mix((uint64_t)(" + v + "))"
	}
	return "sngl_mapidx_word(&(" + v + "), sizeof(" + v + "))"
}

// hashHelper is a call of the function the file defines to hash a value of
// t, as eqHelper is equal's: declared ahead, since a recursive type's body
// reaches it again.
func (c *Context) hashHelper(t *ir.Type, v string, body func() string) string {
	name := c.typeName(t)
	fn := name + "__hash"
	call := fn + "(" + v + ")"
	if c.file.helperNames[fn] {
		return call
	}
	sig := "static uint64_t " + fn + "(" + name + " a)"
	c.file.helper(fn, "")
	c.file.protos = append(c.file.protos, sig+";")
	c.file.setHelper(fn, sig+" {\n"+body()+"}\n")
	return call
}

// mapSlot is the function answering where a key's value is in a map of t,
// a place made for it, holding V's zero, where it has none.
func (c *Context) mapSlot(t *ir.Type) string { return c.mapSlotAs(t, "slot", "k") }

// mapSlotKept is mapSlot for a holder's map, which keeps a key it takes and
// grows in kept memory.
func (c *Context) mapSlotKept(t *ir.Type) string {
	return c.mapSlotAs(t, "slot_kept", c.keep(t.Elems[0], "k"))
}

func (c *Context) mapSlotAs(t *ir.Type, op, key string) string {
	find := c.mapFind(t)
	alloc, drop, before, after := "sngl_alloc", "", "", ""
	if op == "slot_kept" {
		alloc, drop = "sngl_keep_alloc", "\t\tsngl_drop(m->keys);\n\t\tsngl_drop(m->vals);\n"
		if u := c.unshare(t); u != "" {
			before, after = "\t"+u+"(m);\n", "\tsngl_kept_len(m->keys, m->len);\n\tsngl_kept_len(m->vals, m->len);\n"
		}
	}
	size, reindex, put := c.mapKeysSize(t), c.mapReindex(t), c.mapIndexPut(t)
	return c.mapOp(t, op, "V *NAME(M *m, K k)", before+`	int64_t i = `+find+`(*m, k);
	if (i >= 0) {
		return &m->vals[i];
	}
	if (m->len == m->cap) {
		int64_t cap = m->cap < 4 ? 4 : m->cap * 2;
		K *keys = `+alloc+`(`+size+`(cap));
		V *vals = `+alloc+`(sizeof(V) * (size_t)cap);
		if (m->len > 0) {
			memcpy(keys, m->keys, sizeof(K) * (size_t)m->len);
			memcpy(vals, m->vals, sizeof(V) * (size_t)m->len);
		}
`+drop+`		m->keys = keys;
		m->vals = vals;
		m->cap = cap;
		`+reindex+`(m);
	}
	m->keys[m->len] = `+key+`;
	memset(&m->vals[m->len], 0, sizeof(V));
	m->len++;
	if (m->idx != NULL) {
		`+put+`(m, m->len - 1);
	}
`+after+`	return &m->vals[m->len - 1];
`)
}

func (c *Context) MapLit(n *ir.MapLitIR, keys, vals []string) string {
	if n.Type == nil || n.Type.Kind != ir.TypeMap {
		return c.fail("a map literal of %s", n.Type)
	}
	m := c.mapType(n.Type)
	if len(keys) == 0 {
		return "((" + m + "){0})"
	}
	slot := c.mapSlot(n.Type)
	kt, vt := c.typeName(n.Type.Elems[0]), c.typeName(n.Type.Elems[1])
	of := c.mapOp(n.Type, "of", "M NAME(int64_t n, K const *ks, V const *vs)", "\tM m = {0};\n\tfor (int64_t i = 0; i < n; i++) {\n\t\t*"+slot+"(&m, ks[i]) = vs[i];\n\t}\n\treturn m;\n")
	return of + "(" + fmt.Sprint(len(keys)) + ", (" + kt + "[]){" + strings.Join(keys, ", ") + "}, (" + vt + "[]){" + strings.Join(vals, ", ") + "})"
}

// mapIndex is m[k] read, V's zero where m holds no k, as Go reads it.
func (c *Context) mapIndex(t *ir.Type, m, k string) string {
	find := c.mapFind(t)
	at := c.mapOp(t, "at", "V NAME(M m, K k)", "\tint64_t i = "+find+"(m, k);\n\tif (i < 0) {\n\t\tV zero;\n\t\tmemset(&zero, 0, sizeof zero);\n\t\treturn zero;\n\t}\n\treturn m.vals[i];\n")
	return at + "(" + m + ", " + k + ")"
}

// mapEntry is where a field of m[k] is read, for operand, m[k] as mapIndex
// renders it: a pointer to the value, or to V's zero where m holds no k,
// rather than a copy of the whole value to read one field of.
func (c *Context) mapEntry(e ir.Expr, operand string) (string, bool) {
	ix, ok := e.(*ir.Index)
	if !ok {
		return "", false
	}
	t := ix.Operand.ExprType()
	if t == nil || t.Kind != ir.TypeMap {
		return "", false
	}
	args, ok := strings.CutPrefix(operand, c.mapType(t)+"_at(")
	if !ok {
		return "", false
	}
	find := c.mapFind(t)
	entry := c.mapOp(t, "entry", "const V *NAME(M m, K k)", "\tstatic const V zero;\n\tint64_t i = "+find+"(m, k);\n\treturn i < 0 ? &zero : &m.vals[i];\n")
	return entry + "(" + args, true
}

// mapIntrinsic renders a call of one of sngl:builtin's map methods.
func (c *Context) mapIntrinsic(n *ir.Call, t *ir.Type, m string) string {
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	switch n.Func.Intrinsic {
	case "map.length":
		return "(" + m + ").len"
	case "map.contains":
		return "(" + c.mapFind(t) + "(" + m + ", " + arg(1) + ") >= 0)"
	case "map.get":
		find := c.mapFind(t)
		get := c.mapOp(t, "get", "V NAME(M m, K k, V otherwise)", "\tint64_t i = "+find+"(m, k);\n\treturn i < 0 ? otherwise : m.vals[i];\n")
		if len(n.Args) < 3 {
			return c.mapIndex(t, m, arg(1))
		}
		return get + "(" + m + ", " + arg(1) + ", " + arg(2) + ")"
	case "map.keys", "map.values":
		part, elem := "keys", t.Elems[0]
		if n.Func.Intrinsic == "map.values" {
			part, elem = "vals", t.Elems[1]
		}
		l := c.listType(ir.ListOf(elem))
		et := c.typeName(elem)
		name := c.mapType(t) + "_" + part
		c.file.helper(name, fmt.Sprintf("static %s %s(%s m) {\n\t%s l = {sngl_alloc(sizeof(%s) * (size_t)(m.len + 1)), m.len, m.len};\n\tif (m.len > 0) {\n\t\tmemcpy(l.data, m.%s, sizeof(%s) * (size_t)m.len);\n\t}\n\treturn l;\n}\n", l, name, c.mapType(t), l, et, part, et))
		return name + "(" + m + ")"
	}
	return c.fail("the intrinsic %s", n.Func.Intrinsic)
}

// mapRemove renders a map's `remove`: the entries but the key's, in their
// order, into arrays of their own. Not moved along in place: a loop over the
// map walks a copy of its struct, the same arrays, and would skip the entry
// after the one removed and read the last twice; it walks the arrays it began
// with instead. A holder's map is its own first (unshare), gives back the key
// and the value it took out, and the arrays it outgrew, as the turn ends.
func (c *Context) mapRemove(n *ir.Call) string {
	recv := n.Args[0].Value
	if u, ok := recv.(*ir.Unary); ok && u.Op == ast.UnaryAddr {
		recv = u.Operand
	}
	t := recv.ExprType()
	if t != nil && t.Kind == ir.TypeRef {
		t = t.Elems[0]
	}
	if t == nil || t.Kind != ir.TypeMap {
		return c.fail("map.remove of a %s", t)
	}
	m := c.EvalExpr(recv)
	suffix, held := c.kept(recv)
	alloc, before, drop, after := "sngl_alloc", "", "", ""
	if held {
		m = irwalk.EvalMutTarget(c, recv)
		alloc = "sngl_keep_alloc"
		if u := c.unshare(t); u != "" {
			before = "\t" + u + "(m);\n"
		}
		for _, d := range []string{c.drop(t.Elems[0], "m->keys[i]"), c.drop(t.Elems[1], "m->vals[i]")} {
			if d != "" {
				drop += "\t" + d + "\n"
			}
		}
		drop += "\tsngl_drop(m->keys);\n\tsngl_drop(m->vals);\n"
		after = "\tsngl_kept_len(m->keys, m->len);\n\tsngl_kept_len(m->vals, m->len);\n"
	}
	find, size, reindex := c.mapFind(t), c.mapKeysSize(t), c.mapReindex(t)
	rm := c.mapOp(t, "remove"+suffix, "void NAME(M *m, K k)", `	int64_t i = `+find+`(*m, k);
	if (i < 0) {
		return;
	}
`+before+`	K *keys = `+alloc+`(`+size+`(m->len));
	V *vals = `+alloc+`(sizeof(V) * (size_t)m->len);
	memcpy(keys, m->keys, sizeof(K) * (size_t)i);
	memcpy(vals, m->vals, sizeof(V) * (size_t)i);
	memcpy(keys + i, m->keys + i + 1, sizeof(K) * (size_t)(m->len - i - 1));
	memcpy(vals + i, m->vals + i + 1, sizeof(V) * (size_t)(m->len - i - 1));
`+drop+`	m->cap = m->len;
	m->len--;
	m->keys = keys;
	m->vals = vals;
	`+reindex+`(m);
`+after)
	return rm + "(&(" + m + "), " + c.EvalExpr(n.Args[1].Value) + ")"
}

// mapLoop is the head of a loop over a map, its key and value bound in the
// order they were first set.
func (c *Context) mapLoop(n *ir.For) string {
	t := n.Iter.ExprType()
	m := c.mapType(t)
	v := fmt.Sprintf("__loop%d", c.file.next())
	head := "for (struct { " + m + " m; int64_t i; } " + v + " = {" + c.EvalExpr(n.Iter) + ", 0}; " + v + ".i < " + v + ".m.len; " + v + ".i++) {"
	if n.Key != "" {
		head += " " + c.typeName(t.Elems[0]) + " " + Local(n.Key) + " = " + v + ".m.keys[" + v + ".i];"
	}
	if n.Value != "" {
		head += " " + c.typeName(t.Elems[1]) + " " + Local(n.Value) + " = " + v + ".m.vals[" + v + ".i];"
	}
	return head
}

// stringIntrinsic renders a call of one of sngl:builtin's string methods,
// most of them the runtime's.
func (c *Context) stringIntrinsic(n *ir.Call) (string, bool) {
	runtime := map[string]string{
		"string.length":    "sngl_string_length",
		"string.indexOf":   "sngl_string_index_of",
		"string.substring": "sngl_string_substring",
		"string.upper":     "sngl_string_upper",
		"string.lower":     "sngl_string_lower",
		"string.trim":      "sngl_string_trim",
		"string.replace":   "sngl_string_replace",
	}
	if n.Func.Intrinsic == "color.hex" && len(n.Args) == 1 {
		// The color is the program's struct, which the runtime does not
		// know: a function of the file hands it its channels.
		t := c.typeName(n.Args[0].Value.ExprType())
		name := t + "_hex"
		c.file.helper(name, "static sngl_string "+name+"("+t+" c) {\n\treturn sngl_color_hex(c.r, c.g, c.b, c.a);\n}\n")
		return name + "(" + c.EvalExpr(n.Args[0].Value) + ")", true
	}
	if n.Func.Intrinsic == "string.fromCodePoint" && len(n.Args) == 1 {
		return c.fromCodePoint() + "(" + c.EvalExpr(n.Args[0].Value) + ")", true
	}
	fn, ok := runtime[n.Func.Intrinsic]
	if !ok && n.Func.Intrinsic != "string.split" {
		return "", false
	}
	// Rendered once it is known to be one: rendering a lambda lifts it.
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = c.EvalExpr(a.Value)
	}
	if ok {
		return fn + "(" + strings.Join(args, ", ") + ")", true
	}
	t := ir.ListOf(ir.TypString)
	grow := c.listGrow(t, false)
	// An empty separator splits s into its characters, each of the bytes of
	// its UTF-8 sequence, as Go's strings.Split does; a byte no sequence
	// starts is one of its own. Each piece is copied, with the NUL a string
	// ends in.
	split := c.listOp(t, "split", "L NAME(sngl_string s, sngl_string sep)", `	L out = {0};
	if (sep.len == 0) {
		for (size_t i = 0; i < s.len;) {
			unsigned char b = (unsigned char)s.ptr[i];
			size_t n = b < 0x80 ? 1 : (b >> 5) == 6 ? 2 : (b >> 4) == 14 ? 3 : (b >> 3) == 30 ? 4 : 1;
			if (n > s.len - i) {
				n = 1;
			}
			`+grow+`(&out, 1);
			out.data[out.len++] = sngl_string_bytes(s.ptr + i, n);
			i += n;
		}
		return out;
	}
	int64_t from = 0;
	for (;;) {
		int64_t at = sep.len == 0 ? -1 : sngl_string_find(s, sep, from);
		`+grow+`(&out, 1);
		if (at < 0) {
			out.data[out.len++] = sngl_string_bytes(s.ptr + from, s.len - (size_t)from);
			return out;
		}
		out.data[out.len++] = sngl_string_bytes(s.ptr + from, (size_t)(at - from));
		from = at + (int64_t)sep.len;
	}
`)
	return split + "(" + strings.Join(args, ", ") + ")", true
}

// numberIntrinsic is n, a call of a numeric builtin sngl:builtin or
// sngl:math answers in SNGL and every language answers natively, as C
// answers it: math.h's, and the least or greatest of a value and the rest of
// the list it is handed, through a function of the list's.
func (c *Context) numberIntrinsic(n *ir.Call) (string, bool) {
	id := n.Func.Intrinsic
	if !strings.HasPrefix(id, "int.") && !strings.HasPrefix(id, "float.") && !strings.HasPrefix(id, "math.") {
		return "", false
	}
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	math := func(fn string) string {
		c.file.include("math.h", true)
		args := make([]string, len(n.Args))
		for i := range n.Args {
			args[i] = arg(i)
		}
		return fn + "(" + strings.Join(args, ", ") + ")"
	}
	switch id {
	case "math.pow", "math.sqrt", "math.sin", "math.cos", "math.tan", "math.asin", "math.acos", "math.atan", "math.atan2":
		return math(strings.TrimPrefix(id, "math.")), true
	case "float.floor":
		return math("floor"), true
	case "float.ceil":
		return math("ceil"), true
	case "float.round":
		// C's round, as Go's math.Round, rounds half away from zero.
		return math("round"), true
	case "float.abs":
		return math("fabs"), true
	case "int.abs":
		c.file.helper("sngl_int_abs", "static int64_t sngl_int_abs(int64_t x) {\n\treturn x < 0 ? -x : x;\n}\n")
		return "sngl_int_abs(" + arg(0) + ")", true
	case "int.min", "float.min", "int.max", "float.max":
		if len(n.Args) != 2 {
			return "", false
		}
		cmp := "<"
		if strings.HasSuffix(id, "max") {
			cmp = ">"
		}
		op := strings.TrimPrefix(strings.TrimPrefix(id, "int."), "float.")
		fn := c.listOp(n.Args[1].Value.ExprType(), op, "T NAME(T m, L l)", `	for (int64_t i = 0; i < l.len; i++) {
		if (l.data[i] `+cmp+` m) {
			m = l.data[i];
		}
	}
	return m;
`)
		return fn + "(" + arg(0) + ", " + arg(1) + ")", true
	case "int.parse", "float.parse":
		opt := c.typeName(n.Type)
		name := opt + "_" + strings.ReplaceAll(id, ".", "_")
		params, args := "sngl_string s", "s, &o.value"
		call := name + "(" + arg(0)
		if id == "int.parse" {
			params, args = params+", int64_t base", "s, base, &o.value"
			base := "10"
			if len(n.Args) > 1 {
				base = arg(1)
			}
			call += ", " + base
		}
		c.file.helper(name, "static "+opt+" "+name+"("+params+") {\n\t"+opt+" o = {0};\n\to.ok = sngl_"+strings.ReplaceAll(id, ".", "_")+"("+args+");\n\treturn o;\n}\n")
		return call + ")", true
	case "int.clamp", "float.clamp":
		t := c.typeName(n.Type)
		name := "sngl_" + strings.ReplaceAll(id, ".", "_")
		c.file.helper(name, "static "+t+" "+name+"("+t+" x, "+t+" lo, "+t+" hi) {\n\tif (x > hi) {\n\t\tx = hi;\n\t}\n\treturn x < lo ? lo : x;\n}\n")
		return name + "(" + arg(0) + ", " + arg(1) + ", " + arg(2) + ")", true
	}
	return "", false
}

// optionIntrinsic is n, a call of option[T].or, as C answers it: through a
// function of the option's type.
func (c *Context) optionIntrinsic(n *ir.Call) (string, bool) {
	if n.Func.Intrinsic != "option.or" || len(n.Args) != 2 {
		return "", false
	}
	ot := n.Args[0].Value.ExprType()
	opt, t := c.typeName(ot), c.typeName(n.Type)
	name := "sngl_opt_or_" + typeToken(n.Type)
	if c.ptrOption(ot) {
		c.file.helper(name, "static "+t+" "+name+"("+opt+" o, "+t+" otherwise) {\n\treturn o != NULL ? o : otherwise;\n}\n")
	} else {
		c.file.helper(name, "static "+t+" "+name+"("+opt+" o, "+t+" otherwise) {\n\treturn o.ok ? o.value : otherwise;\n}\n")
	}
	return name + "(" + c.EvalExpr(n.Args[0].Value) + ", " + c.EvalExpr(n.Args[1].Value) + ")", true
}

// ptrOption reports whether t is an option C holds as the pointer it is
// (optType).
func (c *Context) ptrOption(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeOption && len(t.Elems) == 1 && (c.isPointer(t.Elems[0]) || cHandle(t.Elems[0]))
}

// keptArg is v, an element pushed into recv, a list of type t, as recv keeps
// it where recv is a holder's.
func (c *Context) keptArg(t *ir.Type, recv ir.Expr, v string) string {
	if c.holder(recv) {
		return c.keep(t.Elems[0], v)
	}
	return v
}

// fromCodePoint is the function of the file answering string.fromCodePoint:
// the UTF-8 of one code point, and U+FFFD for a number no character is --
// negative, past U+10FFFF, or a surrogate half.
func (c *Context) fromCodePoint() string {
	name := "sngl_from_code_point"
	c.file.helper(name, `static sngl_string sngl_from_code_point(int64_t c) {
	if (c < 0 || c > 0x10FFFF || (c >= 0xD800 && c <= 0xDFFF)) {
		c = 0xFFFD;
	}
	char b[4];
	size_t n;
	if (c < 0x80) {
		b[0] = (char)c;
		n = 1;
	} else if (c < 0x800) {
		b[0] = (char)(0xC0 | (c >> 6));
		b[1] = (char)(0x80 | (c & 0x3F));
		n = 2;
	} else if (c < 0x10000) {
		b[0] = (char)(0xE0 | (c >> 12));
		b[1] = (char)(0x80 | ((c >> 6) & 0x3F));
		b[2] = (char)(0x80 | (c & 0x3F));
		n = 3;
	} else {
		b[0] = (char)(0xF0 | (c >> 18));
		b[1] = (char)(0x80 | ((c >> 12) & 0x3F));
		b[2] = (char)(0x80 | ((c >> 6) & 0x3F));
		b[3] = (char)(0x80 | (c & 0x3F));
		n = 4;
	}
	return sngl_string_bytes(b, n);
}
`)
	return name
}
