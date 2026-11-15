package c

import (
	crt "duckfam.us/sngl/pkg/c"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// A codec call -- sngl:json's decode and encode, sngl:http's send and get --
// is a function of the file, written once per type it reads or writes: C
// has no reflection to hand a descriptor to, so the plan (ir.PlanCodec) is
// rendered as the functions that walk it. Each type's decoder and encoder is
// written once by its type token and declared ahead, so a type holding
// itself calls its own. What they read and write goes through
// sngl_codec.c, which words a failure as every target does, and what they
// make is the turn's, as any value a statement makes is.

func init() {
	codegen.DeclareLangImplements(langC, ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic)
}

// codecIntrinsic renders a codec call, http.pathEscape or one of
// sngl:settings' store intrinsics, and reports whether n was one.
func (c *Context) codecIntrinsic(n *ir.Call) (string, bool) {
	arg := func(i int) string { return c.EvalExpr(n.Args[i].Value) }
	switch n.Func.Intrinsic {
	case "http.pathEscape":
		return "sngl_path_escape(" + arg(0) + ")", true
	case "settings._persistent":
		return "sngl_settings_persistent()", true
	case "settings._read":
		return "sngl_settings_read(" + arg(0) + ", " + arg(1) + ")", true
	case "settings._write":
		return "sngl_settings_write(" + arg(0) + ", " + arg(1) + ", " + arg(2) + ")", true
	}
	if !codegen.IsCodecCall(n) {
		return "", false
	}
	t, format, _ := ir.CodecCall(n)
	switch n.Func.Intrinsic {
	case ir.JSONDecodeIntrinsic:
		return c.decodeCall(n, t) + "(" + arg(0) + ")", true
	case ir.JSONEncodeIntrinsic:
		root := ir.PlanCodec(t, format).Root
		name := "sngl_jenc_" + typeToken(root.Type)
		enc := c.encoder(root)
		c.file.helper(name, fmt.Sprintf("static sngl_string %s(%s v) {\n\tsngl_buf b = {0};\n\t%s(&b, v);\n\treturn sngl_buf_string(&b);\n}\n", name, c.typeName(root.Type), enc))
		return name + "(" + arg(0) + ")", true
	}
	return c.requestCall(n, t, format), true
}

// decodeCall is the function reading text as the Result n answers.
func (c *Context) decodeCall(n *ir.Call, t *ir.Type) string {
	root := ir.PlanCodec(t, ir.CodecJSON).Root
	rt := c.typeName(n.Type)
	name := "sngl_jdecode_" + typeToken(n.Type)
	dec := c.decoder(root)
	str := ir.TypString
	notJSON := c.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__snglNotJSON", str)))
	failed := c.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("sngl_json_failure", str)))
	ok := c.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__snglV", t)))
	c.file.helper(name, fmt.Sprintf(`static %s %s(sngl_string text) {
	const sngl_json *v = sngl_json_parse(text);
	if (v == NULL) {
		sngl_string __snglNotJSON = SNGL_STR("not JSON");
		return %s;
	}
	%s __snglV;
	if (!%s(v, NULL, &__snglV)) {
		return %s;
	}
	return %s;
}
`, rt, name, notJSON, c.typeName(root.Type), dec, failed, ok))
	return name
}

// codecFunc declares the function name with sig ahead and reports whether
// it is still to be written: once per name, and before its body asks for
// the functions it calls, which may be itself.
func (c *Context) codecFunc(name, sig string) bool {
	if c.file.helperNames[name] {
		return false
	}
	c.file.helper(name, "")
	c.file.protos = append(c.file.protos, sig+";")
	return true
}

// decoder is the function reading a JSON value into a value of node's type:
// `bool f(const sngl_json *v, const sngl_json_path *p, T *out)`, false with
// sngl_json_failure set where the value does not match.
func (c *Context) decoder(node *ir.Codec) string {
	t := node.Type
	ct := c.typeName(t)
	name := "sngl_jd_" + typeToken(t)
	sig := fmt.Sprintf("static bool %s(const sngl_json *v, const sngl_json_path *p, %s *out)", name, ct)
	if !c.codecFunc(name, sig) {
		return name
	}
	c.file.include("string.h", true)
	var b strings.Builder
	switch node.Kind {
	case ir.CodecBool:
		b.WriteString("\treturn sngl_json_bool(v, p, out);\n")
	case ir.CodecInt:
		lo, hi := intRange(t)
		fmt.Fprintf(&b, "\tint64_t i;\n\tif (!sngl_json_int(v, p, %s, %s, &i)) {\n\t\treturn false;\n\t}\n\t*out = (%s)i;\n\treturn true;\n", lo, hi, ct)
	case ir.CodecFloat:
		fmt.Fprintf(&b, "\tdouble f;\n\tif (!sngl_json_float(v, p, &f)) {\n\t\treturn false;\n\t}\n\t*out = (%s)f;\n\treturn true;\n", ct)
	case ir.CodecString:
		b.WriteString("\treturn sngl_json_string(v, p, out);\n")
	case ir.CodecDate:
		b.WriteString("\treturn sngl_json_date(v, p, out);\n")
	case ir.CodecDatetime:
		b.WriteString("\treturn sngl_json_datetime(v, p, out);\n")
	case ir.CodecOption:
		elem := c.decoder(node.Elem)
		fmt.Fprintf(&b, "\tmemset(out, 0, sizeof *out);\n\tif (v->kind == SNGL_JSON_NULL) {\n\t\treturn true;\n\t}\n\tout->ok = true;\n\treturn %s(v, p, &out->value);\n", elem)
	case ir.CodecList:
		elem := c.decoder(node.Elem)
		et := c.typeName(node.Elem.Type)
		fmt.Fprintf(&b, `	if (v->kind != SNGL_JSON_ARRAY) {
		return sngl_json_want(p, "an array");
	}
	%s l = {sngl_alloc(sizeof(%s) * (size_t)(v->len + 1)), v->len, v->len};
	for (int64_t i = 0; i < v->len; i++) {
		sngl_json_path q = {p, {"", 0}, i};
		if (!%s(&v->items[i], &q, &l.data[i])) {
			return false;
		}
	}
	*out = l;
	return true;
`, ct, et, elem)
	case ir.CodecMap:
		elem := c.decoder(node.Elem)
		slot := c.mapSlot(t)
		fmt.Fprintf(&b, `	if (v->kind != SNGL_JSON_OBJECT) {
		return sngl_json_want(p, "an object");
	}
	%s m = {0};
	int64_t *order;
	int64_t n = sngl_json_sorted(v, &order);
	for (int64_t i = 0; i < n; i++) {
		sngl_json_path q = {p, v->keys[order[i]], -1};
		if (!%s(&v->items[order[i]], &q, %s(&m, v->keys[order[i]]))) {
			return false;
		}
	}
	*out = m;
	return true;
`, ct, elem, slot)
	case ir.CodecEnum:
		wires := make([]string, len(node.Members))
		b.WriteString("\tif (v->kind == SNGL_JSON_STRING) {\n")
		for i, m := range node.Members {
			wires[i] = m.WireName()
			fmt.Fprintf(&b, "\t\tif (sngl_string_eq(v->text, %s)) {\n\t\t\t*out = %d;\n\t\t\treturn true;\n\t\t}\n", StringLiteral(wires[i]), i)
		}
		fmt.Fprintf(&b, "\t}\n\treturn sngl_json_want(p, %s);\n", strconv.Quote("one of "+strings.Join(wires, ", ")))
	case ir.CodecStruct:
		b.WriteString("\tif (v->kind != SNGL_JSON_OBJECT) {\n\t\treturn sngl_json_want(p, \"an object\");\n\t}\n\tmemset(out, 0, sizeof *out);\n")
		for i, f := range node.Fields {
			dec := c.decoder(f.Codec)
			wire := StringLiteral(f.Wire)
			fmt.Fprintf(&b, "\tconst sngl_json *f%d = sngl_json_field(v, %s);\n", i, wire)
			present := ""
			if f.Codec.Kind == ir.CodecOption {
				present = fmt.Sprintf("f%d != NULL && ", i)
			} else {
				fmt.Fprintf(&b, "\tif (f%d == NULL) {\n\t\treturn sngl_json_missing(p, %s);\n\t}\n", i, wire)
			}
			fmt.Fprintf(&b, "\tsngl_json_path q%d = {p, %s, -1};\n\tif (%s!%s(f%d, &q%d, &out->%s)) {\n\t\treturn false;\n\t}\n", i, wire, present, dec, i, i, Local(f.Name))
		}
		b.WriteString("\treturn true;\n")
	default:
		return c.fail("decoding a %s", t)
	}
	c.file.setHelper(name, sig+" {\n"+b.String()+"}\n")
	return name
}

// encoder is the function writing a value of node's type as JSON:
// `void f(sngl_buf *b, T v)`.
func (c *Context) encoder(node *ir.Codec) string {
	t := node.Type
	ct := c.typeName(t)
	name := "sngl_je_" + typeToken(t)
	sig := fmt.Sprintf("static void %s(sngl_buf *b, %s v)", name, ct)
	if !c.codecFunc(name, sig) {
		return name
	}
	var b strings.Builder
	switch node.Kind {
	case ir.CodecBool:
		b.WriteString("\tsngl_buf_str(b, v ? SNGL_STR(\"true\") : SNGL_STR(\"false\"));\n")
	case ir.CodecInt:
		if t.Unsigned {
			b.WriteString("\tsngl_json_uint64(b, (uint64_t)v);\n")
		} else {
			b.WriteString("\tsngl_json_int64(b, (int64_t)v);\n")
		}
	case ir.CodecFloat:
		b.WriteString("\tsngl_json_number(b, (double)v);\n")
	case ir.CodecString, ir.CodecDate:
		b.WriteString("\tsngl_json_quote(b, v);\n")
	case ir.CodecDatetime:
		b.WriteString("\tsngl_buf_put(b, \"\\\"\", 1);\n\tsngl_json_datetime_put(b, v);\n\tsngl_buf_put(b, \"\\\"\", 1);\n")
	case ir.CodecOption:
		fmt.Fprintf(&b, "\tif (!v.ok) {\n\t\tsngl_buf_put(b, \"null\", 4);\n\t\treturn;\n\t}\n\t%s(b, v.value);\n", c.encoder(node.Elem))
	case ir.CodecList:
		fmt.Fprintf(&b, "\tsngl_buf_put(b, \"[\", 1);\n\tfor (int64_t i = 0; i < v.len; i++) {\n\t\tif (i > 0) {\n\t\t\tsngl_buf_put(b, \",\", 1);\n\t\t}\n\t\t%s(b, v.data[i]);\n\t}\n\tsngl_buf_put(b, \"]\", 1);\n", c.encoder(node.Elem))
	case ir.CodecMap:
		fmt.Fprintf(&b, `	int64_t *order = sngl_string_order(v.keys, v.len);
	sngl_buf_put(b, "{", 1);
	for (int64_t i = 0; i < v.len; i++) {
		if (i > 0) {
			sngl_buf_put(b, ",", 1);
		}
		sngl_json_quote(b, v.keys[order[i]]);
		sngl_buf_put(b, ":", 1);
		%s(b, v.vals[order[i]]);
	}
	sngl_buf_put(b, "}", 1);
`, c.encoder(node.Elem))
	case ir.CodecEnum:
		b.WriteString("\tswitch (v) {\n")
		for i, m := range node.Members {
			fmt.Fprintf(&b, "\tcase %d:\n\t\tsngl_json_quote(b, %s);\n\t\tbreak;\n", i, StringLiteral(m.WireName()))
		}
		b.WriteString("\t}\n")
	case ir.CodecStruct:
		b.WriteString("\tsngl_buf_put(b, \"{\", 1);\n")
		if len(node.Fields) > 0 {
			b.WriteString("\tbool first = true;\n")
		}
		for _, f := range node.Fields {
			enc := c.encoder(f.Codec)
			field := "v." + Local(f.Name)
			put := fmt.Sprintf("\tif (!first) {\n\t\tsngl_buf_put(b, \",\", 1);\n\t}\n\tfirst = false;\n\tsngl_json_quote(b, %s);\n\tsngl_buf_put(b, \":\", 1);\n\t%s(b, %s);\n", StringLiteral(f.Wire), enc, field)
			if f.Codec.Kind == ir.CodecOption {
				put = "\tif (" + field + ".ok) {\n" + indent(put) + "\t}\n"
			}
			b.WriteString(put)
		}
		if len(node.Fields) > 0 {
			b.WriteString("\t(void)first;\n")
		}
		b.WriteString("\tsngl_buf_put(b, \"}\", 1);\n")
	default:
		return c.fail("encoding a %s", t)
	}
	c.file.setHelper(name, sig+" {\n"+b.String()+"}\n")
	return name
}

// queryWriter is the function writing a query struct's fields onto a
// buffer, in the order the struct declares them: an absent option left off,
// a list repeating its key.
func (c *Context) queryWriter(node *ir.Codec) string {
	t := node.Type
	name := "sngl_jq_" + typeToken(t)
	sig := fmt.Sprintf("static void %s(sngl_buf *b, %s v)", name, c.typeName(t))
	if !c.codecFunc(name, sig) {
		return name
	}
	var b strings.Builder
	for _, f := range node.Fields {
		c.queryValue(&b, f.Codec, "v."+Local(f.Name), StringLiteral(f.Wire), 1)
	}
	if len(node.Fields) == 0 {
		b.WriteString("\t(void)b;\n\t(void)v;\n")
	}
	c.file.setHelper(name, sig+" {\n"+b.String()+"}\n")
	return name
}

func (c *Context) queryValue(b *strings.Builder, node *ir.Codec, v, wire string, depth int) {
	tab := strings.Repeat("\t", depth)
	put := func(text string) {
		fmt.Fprintf(b, "%ssngl_query_put(b, %s, %s);\n", tab, wire, text)
	}
	buffered := func(write string) {
		fmt.Fprintf(b, "%s{\n%s\tsngl_buf t = {0};\n%s\t%s;\n%s\tsngl_query_put(b, %s, sngl_buf_string(&t));\n%s}\n", tab, tab, tab, write, tab, wire, tab)
	}
	switch node.Kind {
	case ir.CodecOption:
		fmt.Fprintf(b, "%sif (%s.ok) {\n", tab, v)
		c.queryValue(b, node.Elem, v+".value", wire, depth+1)
		fmt.Fprintf(b, "%s}\n", tab)
	case ir.CodecList:
		i := fmt.Sprintf("i%d", depth)
		fmt.Fprintf(b, "%sfor (int64_t %s = 0; %s < %s.len; %s++) {\n", tab, i, i, v, i)
		c.queryValue(b, node.Elem, v+".data["+i+"]", wire, depth+1)
		fmt.Fprintf(b, "%s}\n", tab)
	case ir.CodecBool:
		put("(" + v + " ? SNGL_STR(\"true\") : SNGL_STR(\"false\"))")
	case ir.CodecInt:
		if node.Type.Unsigned {
			buffered("sngl_json_uint64(&t, (uint64_t)" + v + ")")
		} else {
			buffered("sngl_json_int64(&t, (int64_t)" + v + ")")
		}
	case ir.CodecFloat:
		buffered("sngl_json_number(&t, (double)" + v + ")")
	case ir.CodecDatetime:
		buffered("sngl_json_datetime_put(&t, " + v + ")")
	case ir.CodecEnum:
		fmt.Fprintf(b, "%sswitch (%s) {\n", tab, v)
		for i, m := range node.Members {
			fmt.Fprintf(b, "%scase %d:\n", tab, i)
			fmt.Fprintf(b, "%s\tsngl_query_put(b, %s, %s);\n%s\tbreak;\n", tab, wire, StringLiteral(m.WireName()), tab)
		}
		fmt.Fprintf(b, "%s}\n", tab)
	default:
		put(v)
	}
}

// requestCall renders http.send or http.get as a call of a function of the
// file written for it, handed the call's arguments, which answers the
// Result: a test's answer where one is set, otherwise what the platform's
// runtime got back (sngl_http_send).
func (c *Context) requestCall(n *ir.Call, t *ir.Type, format ir.CodecFormat) string {
	c.file.requests = true
	c.file.include("sngl_http.h", false)
	name := fmt.Sprintf("sngl_request_%d", c.file.next())
	// Each argument a parameter of the function, so the IR built around the
	// call reads them by name.
	at := *n
	at.Args = make([]ir.CallArg, len(n.Args))
	var params, args []string
	for i, a := range n.Args {
		at.Args[i] = a
		if ir.IsNullLiteral(a.Value) {
			continue
		}
		p := fmt.Sprintf("__snglA%d", i)
		at.Args[i].Value = codegen.HostValue(p, a.Value.ExprType())
		params = append(params, c.typeName(a.Value.ExprType())+" "+p)
		args = append(args, c.EvalExpr(a.Value))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	str := ir.TypString
	rt := c.typeName(n.Type)
	arg := func(name string) string {
		if a := at.ArgFor(name); a != nil && !ir.IsNullLiteral(a) {
			return c.EvalExpr(a)
		}
		return ""
	}
	failed := func(kind, msg string) string {
		return c.EvalExpr(codegen.ResultFailed(n.Type, kind, codegen.HostValue(msg, str)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "static %s %s(%s) {\n", rt, name, strings.Join(params, ", "))
	fmt.Fprintf(&b, "\tsngl_string __snglURL = %s;\n", arg("url"))
	if t != nil && len(ir.PlanCodec(t, format).Root.Fields) > 0 {
		q := c.queryWriter(ir.PlanCodec(t, format).Root)
		fmt.Fprintf(&b, "\tsngl_buf __snglQ = {0};\n\t%s(&__snglQ, %s);\n\t__snglURL = sngl_with_query(__snglURL, sngl_buf_string(&__snglQ));\n", q, arg("query"))
	} else if q := arg("query"); q != "" {
		fmt.Fprintf(&b, "\t(void)%s;\n", q)
	}
	life := "0"
	if id := codegen.LifetimeID(&at); id != nil {
		life = c.EvalExpr(id)
	}
	if hook, req := codegen.HTTPHook(&at, codegen.HostValue("__snglURL", str)); hook != nil {
		ht := hook.ExprType()
		answer := ht.Elems[0].Sig.Return
		got := codegen.HostValue("__snglGot", ir.MemberPayload(n.Type, "ok"))
		fmt.Fprintf(&b, "\tif (%s.ok) {\n", c.EvalExpr(hook))
		fmt.Fprintf(&b, "\t\tif (sngl_async_ended(%s)) {\n\t\t\tsngl_string __snglMsg = SNGL_STR(\"cancelled\");\n\t\t\treturn %s;\n\t\t}\n", life, failed("cancelled", "__snglMsg"))
		fmt.Fprintf(&b, "\t\t%s __snglAnswer = %s;\n", c.typeName(answer), c.callValue(c.EvalExpr(hook)+".value", []string{c.EvalExpr(req)}))
		fmt.Fprintf(&b, "\t\tif (!__snglAnswer.ok) {\n\t\t\tsngl_string __snglMsg = sngl_concat(%s, __snglURL);\n\t\t\treturn %s;\n\t\t}\n", StringLiteral(codegen.NoAnswer), failed("transport", "__snglMsg"))
		fmt.Fprintf(&b, "\t\t%s __snglGot = __snglAnswer.value;\n\t\treturn %s;\n\t}\n", c.typeName(got.Type), c.EvalExpr(codegen.ResultOK(n.Type, got)))
	} else if h := arg("hook"); h != "" {
		fmt.Fprintf(&b, "\t(void)%s;\n", h)
	}
	method := `"GET"`
	if n.Func.Intrinsic == ir.HTTPSendIntrinsic {
		method = "__snglMethod.ptr"
		m := at.ArgFor("method")
		// NoEnum made the argument its ordinal; the parameter still names
		// the enum.
		var ed *ir.EnumDef
		for _, p := range n.Func.Params {
			if p.Name == "method" && p.Type != nil {
				ed, _ = p.Type.Decl.(*ir.EnumDef)
			}
		}
		if ed == nil || m == nil {
			return c.fail("a request's method")
		}
		fmt.Fprintf(&b, "\tsngl_string __snglMethod = SNGL_STR(\"GET\");\n\tswitch (%s) {\n", c.EvalExpr(m))
		for i, mem := range ed.Members {
			fmt.Fprintf(&b, "\tcase %d:\n\t\t__snglMethod = %s;\n\t\tbreak;\n", i, StringLiteral(strings.ToUpper(mem.Name)))
		}
		b.WriteString("\t}\n")
	}
	st, ht, bt := codegen.ResponseFieldTypes(n.Type)
	c.file.helper("sngl_http_ended", "static int sngl_http_ended(void *life) {\n\treturn sngl_async_ended(*(const int64_t *)life);\n}\n")
	fmt.Fprintf(&b, "\tint64_t __snglLife = %s;\n", life)
	names, values, count := "NULL", "NULL", "0"
	if h := arg("headers"); h != "" {
		fmt.Fprintf(&b, "\tconst char **__snglNames = sngl_alloc(sizeof(char *) * (size_t)((%s).len + 1));\n\tconst char **__snglValues = sngl_alloc(sizeof(char *) * (size_t)((%s).len + 1));\n", h, h)
		fmt.Fprintf(&b, "\tfor (int64_t i = 0; i < (%s).len; i++) {\n\t\t__snglNames[i] = (%s).keys[i].ptr;\n\t\t__snglValues[i] = (%s).vals[i].ptr;\n\t}\n", h, h, h)
		names, values, count = "(const char *const *)__snglNames", "(const char *const *)__snglValues", "(size_t)("+h+").len"
	}
	body := "NULL, 0"
	if v := arg("body"); v != "" {
		body = fmt.Sprintf("(%s).ok ? (%s).value.ptr : NULL, (%s).ok ? (%s).value.len : 0", v, v, v, v)
	}
	fmt.Fprintf(&b, "\tsngl_http_answer __snglR;\n\tsngl_http_fetch(%s, __snglURL.ptr, %s, %s, %s, %s, sngl_http_ended, &__snglLife, &__snglR);\n", method, count, names, values, body)
	b.WriteString("\tsngl_string __snglMsg = sngl_string_copy(__snglR.message);\n\tint __snglFailure = __snglR.failure;\n")
	fmt.Fprintf(&b, "\t%s __snglHeaders = {0};\n\tfor (size_t i = 0; i < __snglR.nheaders; i++) {\n\t\t*%s(&__snglHeaders, sngl_string_copy(__snglR.names[i])) = sngl_string_copy(__snglR.values[i]);\n\t}\n", c.typeName(ht), c.mapSlot(ht))
	fmt.Fprintf(&b, "\t%s __snglStatus = (%s)__snglR.status;\n\tsngl_string __snglBody = sngl_string_bytes(__snglR.body, __snglR.body_len);\n\tsngl_http_answer_free(&__snglR);\n", c.typeName(st), c.typeName(st))
	b.WriteString("\tswitch (__snglFailure) {\n")
	fmt.Fprintf(&b, "\tcase SNGL_HTTP_CANCELLED:\n\t\treturn %s;\n", failed("cancelled", "__snglMsg"))
	fmt.Fprintf(&b, "\tcase SNGL_HTTP_TIMEOUT:\n\t\treturn %s;\n", failed("timeout", "__snglMsg"))
	fmt.Fprintf(&b, "\tcase SNGL_HTTP_TRANSPORT:\n\t\treturn %s;\n\t}\n", failed("transport", "__snglMsg"))
	resp := codegen.CodecResponse(n.Type, codegen.HostValue("__snglStatus", st), codegen.HostValue("__snglHeaders", ht), codegen.HostValue("__snglBody", bt))
	fmt.Fprintf(&b, "\treturn %s;\n}\n", c.EvalExpr(codegen.ResultOK(n.Type, resp)))
	c.file.protos = append(c.file.protos, fmt.Sprintf("static %s %s(%s);", rt, name, strings.Join(params, ", ")))
	c.file.helper(name, b.String())
	return name + "(" + strings.Join(args, ", ") + ")"
}

// HTTPRuntime adds to sources, the files a program ships, the stacks
// async.start runs work on (sngl_work.c) where the program starts any, and
// where it makes a request, the wait for one (sngl_fetch.c) and C's own
// HTTP client where no platform link answers one (none ships sngl_http.h):
// libcurl's, from sngl:language/c. It is the pkg-config package that client
// links, or none.
func HTTPRuntime(c *Context, sources map[string][]byte) ([]string, error) {
	if c.file.works || c.file.requests {
		sources[crt.WorkFile] = crt.WorkSource
	}
	if !c.file.requests {
		return nil, nil
	}
	names := []string{"sngl_fetch.c"}
	_, answered := sources["sngl_http.h"]
	if !answered {
		names = append(names, "sngl_http.c", "sngl_http.h")
	}
	for _, name := range names {
		data, err := fs.ReadFile(lib.FS, "language/c/rt/"+name)
		if err != nil {
			return nil, err
		}
		sources[name] = data
	}
	if answered {
		return nil, nil
	}
	return []string{"libcurl"}, nil
}

// OpenSettings is the statement at the head of a main that opens
// sngl:settings' place for the app called app, or "" where pkg keeps no
// settings: only a main opens one, which a test agent never runs.
func OpenSettings(pkg *ir.Package, app string) string {
	if pkg == nil || !codegen.UsesSettings(pkg) {
		return ""
	}
	return "\tsngl_settings_open(" + strconv.Quote(app) + ");\n"
}

// WithOpenSettings is main, a C program's entry, opening open first.
func WithOpenSettings(main, open string) string {
	at := strings.Index(main, "int main(")
	if open == "" || at < 0 {
		return main
	}
	body := at + strings.Index(main[at:], "{\n") + 2
	return main[:body] + open + main[body:]
}

// intRange is the least and greatest value of t, an int, as C spells them:
// what a JSON integer must lie between to be one. A uint64 reads no more than
// an int64 does, as on every target.
func intRange(t *ir.Type) (lo, hi string) {
	bits := t.Bits
	if bits == 0 {
		bits = 64
	}
	switch {
	case bits == 64 && t.Unsigned:
		return "0", "INT64_MAX"
	case bits == 64:
		return "INT64_MIN", "INT64_MAX"
	case t.Unsigned:
		return "0", fmt.Sprintf("UINT%d_MAX", bits)
	}
	return fmt.Sprintf("INT%d_MIN", bits), fmt.Sprintf("INT%d_MAX", bits)
}

func indent(s string) string {
	lines := strings.SplitAfter(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "\t" + l
		}
	}
	return strings.Join(lines, "")
}
