package c

import (
	"fmt"
	"regexp"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A list is a struct per element type, `sngl_list_<elem>`: its elements on
// the heap, how many there are and how many fit. It is passed by value and
// shares its elements as a Go slice does, so a copy SNGL's value semantics
// need is the one lowering writes out (ir.ValueCopyIntrinsic). Each
// operation is a function defined for the element type the first time the
// program uses it.
//
// An option is a struct per value type, `sngl_opt_<value>`: whether it holds
// one, and the value.

// listType names list[elem]'s C struct, defined in the file.
func (c *Context) listType(t *ir.Type) string {
	elem := t.Elems[0]
	et := c.typeName(elem)
	name := "sngl_list_" + typeToken(elem)
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\t%s *data;\n\tint64_t len;\n\tint64_t cap;\n} %s;\n", name, et, name), strings.TrimSuffix(et, " *"))
	return name
}

// optType names option[value]'s C struct, defined in the file.
func (c *Context) optType(t *ir.Type) string {
	elem := t.Elems[0]
	et := c.typeName(elem)
	name := "sngl_opt_" + typeToken(elem)
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\tbool ok;\n\t%s value;\n} %s;\n", name, et, name), strings.TrimSuffix(et, " *"))
	return name
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
	of := c.listOp(t, "of", "L NAME(int64_t n, const T *xs)", `	L l = {malloc(sizeof(T) * (size_t)n), n, n};
	if (l.data == NULL) {
		sngl_panic("out of memory");
	}
	memcpy(l.data, xs, sizeof(T) * (size_t)n);
	return l;
`)
	return of + "(" + fmt.Sprint(len(elems)) + ", (" + c.typeName(t.Elems[0]) + "[]){" + strings.Join(elems, ", ") + "})"
}

func (c *Context) ListLit(n *ir.ListLit, elems []string) string {
	if n.Type == nil || n.Type.Kind != ir.TypeList {
		return c.fail("a list literal of %s", n.Type)
	}
	for _, e := range n.Elems {
		if _, ok := e.(*ir.Spread); ok {
			return c.fail("a spread into a list")
		}
	}
	return c.listOf(n.Type, elems)
}

func (c *Context) listGrow(t *ir.Type) string {
	return c.listOp(t, "grow", "void NAME(L *l, int64_t n)", `	if (l->len + n <= l->cap) {
		return;
	}
	int64_t cap = l->cap * 2;
	if (cap < l->len + n) {
		cap = l->len + n;
	}
	if (cap < 4) {
		cap = 4;
	}
	T *data = malloc(sizeof(T) * (size_t)cap);
	if (data == NULL) {
		sngl_panic("out of memory");
	}
	if (l->len > 0) {
		memcpy(data, l->data, sizeof(T) * (size_t)l->len);
	}
	l->data = data;
	l->cap = cap;
`)
}

// listAt is the element of list xs of type t at i, which raises past the
// end as every target does.
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
func (c *Context) MutTargetIndex(n *ir.Index, operand, idx string) string {
	if t := n.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap {
		return "(*" + c.mapSlot(t) + "(&(" + operand + "), " + idx + "))"
	}
	return c.Index(n, operand, idx)
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
	switch id {
	case "list.length":
		return "(" + xs + ").len", true
	case "list.push":
		grow := c.listGrow(t)
		push := c.listOp(t, "push", "void NAME(L *l, T v)", "\t"+grow+"(l, 1);\n\tl->data[l->len++] = v;\n")
		return push + "(&(" + xs + "), " + arg(1) + ")", true
	case "list.clear":
		return "((" + xs + ").len = 0)", true
	case "list.remove":
		rm := c.listOp(t, "remove", "void NAME(L *l, int64_t i)", `	if (i < 0 || i >= l->len) {
		return;
	}
	memmove(&l->data[i], &l->data[i + 1], sizeof(T) * (size_t)(l->len - i - 1));
	l->len--;
`)
		return rm + "(&(" + xs + "), " + arg(1) + ")", true
	case "list.insert":
		grow := c.listGrow(t)
		ins := c.listOp(t, "insert", "void NAME(L *l, int64_t i, T v)", `	if (i < 0 || i > l->len) {
		return;
	}
	`+grow+`(l, 1);
	memmove(&l->data[i + 1], &l->data[i], sizeof(T) * (size_t)(l->len - i));
	l->data[i] = v;
	l->len++;
`)
		return ins + "(&(" + xs + "), " + arg(1) + ", " + arg(2) + ")", true
	case "list.reverse":
		rev := c.listOp(t, "reverse", "L NAME(L l)", `	L out = {malloc(sizeof(T) * (size_t)(l.len + 1)), l.len, l.len};
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
	L out = {malloc(sizeof(T) * (size_t)(end - start + 1)), end - start, end - start};
	if (end > start) {
		memcpy(out.data, &l.data[start], sizeof(T) * (size_t)(end - start));
	}
	return out;
`)
		return sl + "(" + xs + ", " + arg(1) + ", " + arg(2) + ")", true
	case "list.indexOf":
		eq := c.equal(t.Elems[0], "l.data[i]", "v")
		idx := c.listOp(t, "index_of", "int64_t NAME(L l, T v)", "\tfor (int64_t i = 0; i < l.len; i++) {\n\t\tif ("+eq+") {\n\t\t\treturn i;\n\t\t}\n\t}\n\treturn -1;\n")
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
		grow := c.listGrow(t)
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
	%s out = {malloc(sizeof(%s) * (size_t)(l.len + 1)), l.len, l.len};
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
		sort := c.listOp(t, strings.TrimPrefix(id, "list."), sig, `	T *out = l->data;
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
	t := iter.ExprType()
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
		return "(" + c.EvalExpr(n.Operand) + ").value", true
	case ir.IsOptionWrap(n):
		return "((" + c.optType(n.Type) + "){true, " + c.EvalExpr(n.Operand) + "})", true
	}
	if lit, ok := n.Operand.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull && n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeOption, ir.TypeList, ir.TypeMap, ir.TypeFunc:
			return "((" + c.typeName(n.Type) + "){0})", true
		}
	}
	return "", false
}

// optionCompare is a comparison with null, which asks whether an option holds
// a value, or of an option with a value, and reports whether n was one.
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
	case ir.IsNullLiteral(n.Right) && lt != nil && lt.Kind == ir.TypeOption:
		return not + "(" + left + ").ok", true
	case ir.IsNullLiteral(n.Left) && rt != nil && rt.Kind == ir.TypeOption:
		return not + "(" + right + ").ok", true
	case lt != nil && lt.Kind == ir.TypeOption && rt != nil && rt.Kind != ir.TypeOption:
		eq := "((" + left + ").ok && " + c.equal(lt.Elems[0], "("+left+").value", right) + ")"
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
		return c.listOp(t, "copy", "L NAME(L l)", `	L out = {malloc(sizeof(T) * (size_t)(l.len + 1)), l.len, l.len};
	for (int64_t i = 0; i < l.len; i++) {
		out.data[i] = `+elem+`;
	}
	return out;
`) + "(" + v + ")"
	case ir.TypeMap:
		kc, vc := c.copyValue(t.Elems[0], "m.keys[i]"), c.copyValue(t.Elems[1], "m.vals[i]")
		return c.mapOp(t, "copy", "M NAME(M m)", `	M out = {malloc(sizeof(K) * (size_t)(m.len + 1)), malloc(sizeof(V) * (size_t)(m.len + 1)), m.len, m.len};
	for (int64_t i = 0; i < m.len; i++) {
		out.keys[i] = `+kc+`;
		out.vals[i] = `+vc+`;
	}
	return out;
`) + "(" + v + ")"
	case ir.TypeOption:
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
			c.file.helper(name, "")
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

// mapType names map[k, v]'s C struct, defined in the file.
func (c *Context) mapType(t *ir.Type) string {
	kt, vt := c.typeName(t.Elems[0]), c.typeName(t.Elems[1])
	name := "sngl_map_" + typeToken(t.Elems[0]) + "__" + typeToken(t.Elems[1])
	c.file.define(name, fmt.Sprintf("typedef struct %s {\n\t%s *keys;\n\t%s *vals;\n\tint64_t len;\n\tint64_t cap;\n} %s;\n", name, kt, vt, name),
		strings.TrimSuffix(kt, " *"), strings.TrimSuffix(vt, " *"))
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
// has none.
func (c *Context) mapFind(t *ir.Type) string {
	eq := c.equal(t.Elems[0], "m.keys[i]", "k")
	return c.mapOp(t, "find", "int64_t NAME(M m, K k)", "\tfor (int64_t i = 0; i < m.len; i++) {\n\t\tif ("+eq+") {\n\t\t\treturn i;\n\t\t}\n\t}\n\treturn -1;\n")
}

// mapSlot is the function answering where a key's value is in a map of t,
// a place made for it, holding V's zero, where it has none.
func (c *Context) mapSlot(t *ir.Type) string {
	find := c.mapFind(t)
	return c.mapOp(t, "slot", "V *NAME(M *m, K k)", `	int64_t i = `+find+`(*m, k);
	if (i >= 0) {
		return &m->vals[i];
	}
	if (m->len == m->cap) {
		int64_t cap = m->cap < 4 ? 4 : m->cap * 2;
		K *keys = malloc(sizeof(K) * (size_t)cap);
		V *vals = malloc(sizeof(V) * (size_t)cap);
		if (keys == NULL || vals == NULL) {
			sngl_panic("out of memory");
		}
		if (m->len > 0) {
			memcpy(keys, m->keys, sizeof(K) * (size_t)m->len);
			memcpy(vals, m->vals, sizeof(V) * (size_t)m->len);
		}
		m->keys = keys;
		m->vals = vals;
		m->cap = cap;
	}
	m->keys[m->len] = k;
	memset(&m->vals[m->len], 0, sizeof(V));
	return &m->vals[m->len++];
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
	of := c.mapOp(n.Type, "of", "M NAME(int64_t n, const K *ks, const V *vs)", "\tM m = {0};\n\tfor (int64_t i = 0; i < n; i++) {\n\t\t*"+slot+"(&m, ks[i]) = vs[i];\n\t}\n\treturn m;\n")
	return of + "(" + fmt.Sprint(len(keys)) + ", (" + kt + "[]){" + strings.Join(keys, ", ") + "}, (" + vt + "[]){" + strings.Join(vals, ", ") + "})"
}

// mapIndex is m[k] read, V's zero where m holds no k, as Go reads it.
func (c *Context) mapIndex(t *ir.Type, m, k string) string {
	find := c.mapFind(t)
	at := c.mapOp(t, "at", "V NAME(M m, K k)", "\tint64_t i = "+find+"(m, k);\n\tif (i < 0) {\n\t\tV zero;\n\t\tmemset(&zero, 0, sizeof zero);\n\t\treturn zero;\n\t}\n\treturn m.vals[i];\n")
	return at + "(" + m + ", " + k + ")"
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
		c.file.helper(name, fmt.Sprintf("static %s %s(%s m) {\n\t%s l = {malloc(sizeof(%s) * (size_t)(m.len + 1)), m.len, m.len};\n\tif (m.len > 0) {\n\t\tmemcpy(l.data, m.%s, sizeof(%s) * (size_t)m.len);\n\t}\n\treturn l;\n}\n", l, name, c.mapType(t), l, et, part, et))
		return name + "(" + m + ")"
	}
	return c.fail("the intrinsic %s", n.Func.Intrinsic)
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
		"int.parse":        "sngl_int_parse",
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
	grow := c.listGrow(t)
	split := c.listOp(t, "split", "L NAME(sngl_string s, sngl_string sep)", `	L out = {0};
	int64_t from = 0;
	for (;;) {
		int64_t at = sep.len == 0 ? -1 : sngl_string_find(s, sep, from);
		`+grow+`(&out, 1);
		if (at < 0) {
			out.data[out.len++] = sngl_string_substring((sngl_string){s.ptr + from, s.len - (size_t)from}, 0, INT64_MAX);
			return out;
		}
		out.data[out.len++] = (sngl_string){s.ptr + from, (size_t)(at - from)};
		from = at + (int64_t)sep.len;
	}
`)
	return split + "(" + strings.Join(args, ", ") + ")", true
}
