package c

import (
	"fmt"
	"strconv"
	"strings"

	snglI18n "duckfam.us/sngl/codegen/i18n"
	crt "duckfam.us/sngl/pkg/c"

	"duckfam.us/sngl/ir"
)

// sngl:i18n is answered by id, as Go answers it: an entry point a program
// calls (`i18n.trInline`) is the runtime's call with the locale its hidden
// parameter was handed, and a primitive one of them calls (`_translate`)
// the same call with the locale it was handed first. A template's struct of
// arguments becomes the runtime's list of them, by a function of the file
// per struct type (sngl_i18n.c).

// i18nIntrinsic renders a call of sngl:i18n, and reports whether n was one.
func (c *Context) i18nIntrinsic(n *ir.Call) (string, bool) {
	id, ok := strings.CutPrefix(n.Func.Intrinsic, "i18n.")
	if !ok {
		return "", false
	}
	primitive := strings.HasPrefix(id, "_")
	locale := func() string {
		name := "__ctx_locale"
		if primitive {
			name = "locale"
		}
		if a := n.ArgFor(name); a != nil {
			return c.EvalExpr(a)
		}
		return "sngl_i18n_default_locale()"
	}
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return c.EvalExpr(a)
		}
		return c.fail("i18n.%s without its %s", id, name)
	}
	style := func(name, otherwise string) string { return c.i18nStyle(n, name, otherwise) }
	out := ""
	switch strings.TrimPrefix(id, "_") {
	case "defaultLocale":
		out = "sngl_i18n_default_locale()"
	case "tr":
		out = c.i18nTemplate(n, "translate", locale(), arg("key"), `SNGL_STR("")`)
	case "trInline", "translate":
		out = c.i18nTemplate(n, "translate", locale(), arg("key"), arg("inlinedTemplate"))
	case "format":
		out = c.i18nTemplate(n, "format", locale(), arg("template"), "")
	case "numberInt":
		out = fmt.Sprintf("sngl_i18n_number_int(%s, (int64_t)(%s), %s)", locale(), arg("n"), style("style", "decimal"))
	case "numberFloat":
		out = fmt.Sprintf("sngl_i18n_number_float(%s, (double)(%s), %s)", locale(), arg("n"), style("style", "decimal"))
	case "date":
		out = fmt.Sprintf("sngl_i18n_date(%s, %s, %s)", locale(), arg("d"), style("style", "medium"))
	case "time":
		out = fmt.Sprintf("sngl_i18n_time(%s, %s, %s)", locale(), arg("t"), style("style", "medium"))
	case "datetime", "dateTime":
		out = fmt.Sprintf("sngl_i18n_datetime(%s, %s, %s, %s)", locale(), arg("dt"), style("dateStyle", "medium"), style("timeStyle", "medium"))
	case "select":
		cases := n.ArgFor("cases")
		if cases == nil {
			return c.fail("i18n.%s without its cases", id), true
		}
		m := c.EvalExpr(cases)
		out = fmt.Sprintf("sngl_i18n_select(%s, (%s).keys, (%s).vals, (%s).len)", arg("value"), m, m, m)
	case "plural", "selectOrdinal", "selectordinal":
		out = c.i18nPlural(n, locale(), arg("count"), id != "plural" && id != "_plural")
	case "exactly":
		out = fmt.Sprintf("((%s){.%s = %s, .%s = true})", c.typeName(n.Type), Local("n"), arg("n"), Local("exact"))
	default:
		return c.fail("the intrinsic i18n.%s", id), true
	}
	c.translates()
	return out, true
}

func (c *Context) translates() {
	c.file.translates = true
	c.file.include(crt.I18nHeaderFile, false)
}

// i18nStyle is the style argument name of n as the C string the runtime
// reads: an entry point's is a member of its enum, which NoEnum made its
// ordinal, and a primitive's the member's name already.
func (c *Context) i18nStyle(n *ir.Call, name, otherwise string) string {
	a := n.ArgFor(name)
	if a == nil {
		return strconv.Quote(otherwise)
	}
	if t := a.ExprType(); t != nil && t.Kind == ir.TypeString {
		return "(" + c.EvalExpr(a) + ").ptr"
	}
	for _, p := range n.Func.Params {
		if p.Name != name || p.Type == nil {
			continue
		}
		if ed, ok := p.Type.Decl.(*ir.EnumDef); ok {
			return c.memberNames(ed) + "[" + c.EvalExpr(a) + "]"
		}
	}
	return c.fail("i18n's %s of %s", name, a.ExprType())
}

// memberNames is an array of the names of ed's members, by position.
func (c *Context) memberNames(ed *ir.EnumDef) string {
	names := make([]string, len(ed.Members))
	for i, m := range ed.Members {
		names[i] = strconv.Quote(m.Name)
	}
	return "((const char *const[]){" + strings.Join(names, ", ") + "})"
}

// i18nTemplate is a translation (op "translate", key and template) or a
// format (op "format", the template alone) of n's args, through the
// function of the file that lists a struct of its type as the runtime's
// arguments.
func (c *Context) i18nTemplate(n *ir.Call, op, locale, key, tmpl string) string {
	a := n.ArgFor("args")
	if a == nil {
		return c.fail("i18n.%s without its args", strings.TrimPrefix(n.Func.Intrinsic, "i18n."))
	}
	t := a.ExprType()
	sd, _ := t.Decl.(*ir.StructDef)
	if sd == nil {
		return c.fail("i18n's arguments of %s", t)
	}
	ct := c.typeName(t)
	name := "sngl_i18n_" + op + "_" + typeToken(t)
	params, call := "sngl_string locale, sngl_string key, sngl_string tmpl, "+ct+" v", "locale, key, tmpl"
	if op == "format" {
		params, call = "sngl_string locale, sngl_string tmpl, "+ct+" v", "locale, tmpl"
	}
	if !c.file.helperNames[name] {
		fields, _ := ir.FieldsOf(t)
		var b strings.Builder
		fmt.Fprintf(&b, "static sngl_string %s(%s) {\n", name, params)
		if len(fields) == 0 {
			fmt.Fprintf(&b, "\t(void)v;\n\treturn sngl_i18n_%s(%s, NULL, 0);\n}\n", op, call)
		} else {
			fmt.Fprintf(&b, "\tsngl_i18n_arg args[%d];\n\tmemset(args, 0, sizeof args);\n", len(fields))
			for i, f := range fields {
				fmt.Fprintf(&b, "\targs[%d].name = %s;\n", i, StringLiteral(f.Name))
				b.WriteString(c.i18nArg(f.Type, "v."+Local(f.Name), fmt.Sprintf("args[%d]", i), "\t"))
			}
			fmt.Fprintf(&b, "\treturn sngl_i18n_%s(%s, args, %d);\n}\n", op, call, len(fields))
		}
		c.file.include("string.h", true)
		c.file.helper(name, b.String())
	}
	if op == "format" {
		return fmt.Sprintf("%s(%s, %s, %s)", name, locale, key, c.EvalExpr(a))
	}
	return fmt.Sprintf("%s(%s, %s, %s, %s)", name, locale, key, tmpl, c.EvalExpr(a))
}

// i18nArg sets the runtime's argument to of the value v of type t: its text
// as Go's %v writes it, and the integer a plural reads of it, as Go's toInt
// reads one. A null option is "", as Go's optionValues makes it.
func (c *Context) i18nArg(t *ir.Type, v, to, tab string) string {
	switch {
	case t == nil:
	case t.Kind == ir.TypeOption && len(t.Elems) == 1:
		inner := c.i18nArg(t.Elems[0], "("+v+").value", to, tab+"\t")
		return fmt.Sprintf("%sif ((%s).ok) {\n%s%s} else {\n%s\t%s.text = SNGL_STR(\"\");\n%s}\n", tab, v, inner, tab, tab, to, tab)
	case ir.IsDateStruct(t), ir.IsTimeStruct(t), ir.IsDateTimeStruct(t):
		kind := "SNGL_I18N_DATE"
		if ir.IsTimeStruct(t) {
			kind = "SNGL_I18N_TIME"
		} else if ir.IsDateTimeStruct(t) {
			kind = "SNGL_I18N_DATETIME"
		}
		return fmt.Sprintf("%s%s.text = %s;\n%s%s.kind = %s;\n", tab, to, v, tab, to, kind)
	case t.Kind == ir.TypeString:
		return fmt.Sprintf("%s%s.text = %s;\n%s%s.i = sngl_int_of(%s);\n", tab, to, v, tab, to, v)
	case t.Kind == ir.TypeInt:
		return fmt.Sprintf("%s%s.text = sngl_int_format((int64_t)(%s));\n%s%s.kind = SNGL_I18N_INT;\n%s%s.i = (int64_t)(%s);\n", tab, to, v, tab, to, tab, to, v)
	case t.Kind == ir.TypeFloat:
		return fmt.Sprintf("%s%s.text = sngl_float_format((double)(%s));\n%s%s.kind = SNGL_I18N_FLOAT;\n%s%s.f = (double)(%s);\n%s%s.i = sngl_float_int((double)(%s));\n", tab, to, v, tab, to, tab, to, v, tab, to, v)
	case t.Kind == ir.TypeBool:
		return fmt.Sprintf("%s%s.text = sngl_bool_format(%s);\n", tab, to, v)
	case t.Kind == ir.TypeEnum && ir.PayloadEnum(t) == nil:
		if ed, ok := t.Decl.(*ir.EnumDef); ok {
			return fmt.Sprintf("%s%s.text = sngl_string_copy(%s[%s]);\n", tab, to, c.memberNames(ed), v)
		}
	}
	return tab + c.fail("a translation's argument of %s", t) + ";\n"
}

// i18nPlural is a plural or a selectordinal of count over n's forms, a map
// of PluralKey, through a function of the file per map type that lists the
// keys as the runtime reads them.
func (c *Context) i18nPlural(n *ir.Call, locale, count string, ordinal bool) string {
	forms := n.ArgFor("forms")
	if forms == nil {
		return c.fail("an i18n plural without its forms")
	}
	t := forms.ExprType()
	if t == nil || t.Kind != ir.TypeMap {
		return c.fail("an i18n plural's forms of %s", t)
	}
	mt := c.typeName(t)
	name := "sngl_i18n_plural_" + typeToken(t)
	c.file.helper(name, fmt.Sprintf(`static sngl_string %s(sngl_string locale, int64_t count, %s m, bool ordinal) {
	int64_t *ns = sngl_alloc(sizeof(int64_t) * (size_t)(m.len + 1));
	bool *exact = sngl_alloc(sizeof(bool) * (size_t)(m.len + 1));
	for (int64_t i = 0; i < m.len; i++) {
		ns[i] = (int64_t)m.keys[i].%s;
		exact[i] = m.keys[i].%s;
	}
	return sngl_i18n_plural(locale, count, ns, exact, m.vals, m.len, ordinal);
}
`, name, mt, Local("n"), Local("exact")))
	return fmt.Sprintf("%s(%s, (int64_t)(%s), %s, %t)", name, locale, count, c.EvalExpr(forms), ordinal)
}

// I18nRuntime adds sngl:i18n's runtime to sources, the files a program
// ships, where the program translates: the runtime, and the manifest it
// carries as an array of the file's bytes, which no string literal holds
// whole -- C17 guarantees one of 4095 bytes. It is the pkg-config package
// ICU links by on a system that has one, empty on Windows, whose icu.dll is
// linked by name.
func I18nRuntime(c *Context, sources map[string][]byte, manifest []byte) bool {
	if !c.file.translates {
		return false
	}
	sources[crt.I18nHeaderFile] = crt.I18nHeader
	sources[crt.I18nSourceFile] = crt.I18nSource
	var b strings.Builder
	b.WriteString("#include \"" + crt.I18nHeaderFile + "\"\n\n/* i18n.manifest.json, which the program carries. */\nconst char sngl_i18n_manifest[] = {")
	for i, by := range manifest {
		if i%16 == 0 {
			b.WriteString("\n\t")
		} else {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d,", by)
	}
	b.WriteString("\n\t0,\n};\nconst size_t sngl_i18n_manifest_len = " + strconv.Itoa(len(manifest)) + ";\n")
	sources["sngl_i18n_manifest.c"] = []byte(b.String())
	return true
}

// LoadManifest is the project's i18n.manifest.json, nil for none.
var LoadManifest = snglI18n.LoadManifest
