package html

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A ui.Style reaches this platform as the declarations html.sngl's `css`
// answers for it, which every element's style is: a list of
// `Declaration{property, value}`, an empty value writing nothing. The fold
// has reduced each one whose field the build can read to text, which is
// written in the markup; one reading what only the page knows is written
// where the page runs, with setProperty, which removes the property for an
// empty value. A list the fold could not spell out -- a Style held in state
// and handed through whole -- is written as a whole, through cssText.
//
// Two declarations can write the same longhand -- `border-width` and an empty
// `border-bottom` after it, `display` twice -- and written one setProperty
// apiece the later one's removal took what the earlier one wrote. So the
// declarations are grouped by what they can share (styleGroup), and a group
// of several the page writes is written as one: every property removed, then
// the ones with a value set in order.

// declaration is one CSS declaration a style writes. text is its value when
// the build knows it.
type declaration struct {
	lit      *ir.StructLit
	property string
	value    ir.Expr
	text     string
	static   bool
}

// declarationsOf reads the declarations of a style that write something --
// every one but those the build knows are empty -- and false for a value that
// is no list literal of them.
func declarationsOf(style ir.Expr) ([]declaration, bool) {
	list, ok := style.(*ir.ListLit)
	if !ok {
		return nil, false
	}
	var out []declaration
	for _, el := range list.Elems {
		sl, ok := el.(*ir.StructLit)
		if !ok {
			return nil, false
		}
		d := declaration{lit: sl}
		p, ok := codegen.IRLiteralString(sl.Field("property"))
		if !ok {
			return nil, false
		}
		d.property = p
		d.value = sl.Field("value")
		d.text, d.static = codegen.IRLiteralString(d.value)
		if d.static && d.text == "" {
			continue
		}
		out = append(out, d)
	}
	return out, true
}

// styleGroup is what a property can share a longhand with: a CSS shorthand
// and its longhands share their first word (`border`, `border-width`,
// `border-bottom`), and a group wider than what it shares costs only a
// rewrite.
func styleGroup(property string) string {
	group, _, _ := strings.Cut(strings.TrimPrefix(property, "-"), "-")
	return group
}

// styleWrite is what the page writes for a style: one declaration, or a
// group of several written together.
type styleWrite struct {
	decls []declaration
}

// splitStyle parts a style's declarations into the text the build writes
// into the markup and what the page writes: every declaration of a group one
// of whose values only the page knows.
func splitStyle(style ir.Expr) (static []declaration, writes []styleWrite) {
	decls, _ := declarationsOf(style)
	dynamic := map[string]bool{}
	for _, d := range decls {
		if !d.static && d.value != nil {
			dynamic[styleGroup(d.property)] = true
		}
	}
	at := map[string]int{}
	for _, d := range decls {
		g := styleGroup(d.property)
		if !dynamic[g] {
			static = append(static, d)
			continue
		}
		if d.value == nil {
			continue
		}
		i, ok := at[g]
		if !ok {
			i = len(writes)
			at[g] = i
			writes = append(writes, styleWrite{})
		}
		writes[i].decls = append(writes[i].decls, d)
	}
	return static, writes
}

// staticCSS is the text of every declaration of style the build writes,
// into the element's style attribute.
func staticCSS(style ir.Expr) string {
	static, _ := splitStyle(style)
	var parts []string
	for _, d := range static {
		parts = append(parts, d.property+":"+d.text)
	}
	return strings.Join(parts, ";")
}

// cssTextHelper is the page's function from a style's declarations, held
// whole, to the text of the ones that write something.
const cssTextHelper = "_snglCSSText"

const cssTextHelperJS = `function _snglCSSText(ds) {
  return ds.filter(d => d.value !== "").map(d => d.property + ":" + d.value).join(";");
}
`

// setStylesHelper is the page's function writing a group of declarations
// as one.
const setStylesHelper = "_snglSetStyles"

const setStylesHelperJS = `function _snglSetStyles(st, ds) {
  for (const d of ds) st.removeProperty(d.property);
  for (const d of ds) if (d.value !== "") st.setProperty(d.property, d.value);
}
`

// group is the declarations of w as the list the page's helper takes.
func (w styleWrite) group(style ir.Expr) *ir.ListLit {
	list := &ir.ListLit{Type: style.(*ir.ListLit).Type}
	for _, d := range w.decls {
		list.Elems = append(list.Elems, d.lit)
	}
	return list
}

// styleWriteStmts is a style written to a node the translator reaches
// through its handle -- a handler's assignment or a factory's creation: the
// attribute for what the build knows, and a setProperty per declaration only
// the page does. A list held whole is written through cssText.
func (t *htmlTranslator) styleWriteStmts(node, style ir.Expr) []ir.Stmt {
	decl := &ir.Select{Operand: node, Field: "style", Type: ir.NativeNamed("CSSStyleDeclaration")}
	if _, ok := declarationsOf(style); !ok {
		t.jc.Ctx.Helpers[cssTextHelper] = true
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: decl, Field: "cssText", Type: ir.TypString},
			Op:     ast.AssignSet,
			Value:  &ir.Call{Type: ir.TypString, Func: &ir.Func{Name: cssTextHelper}, Args: []ir.CallArg{{Value: style}}},
		}}
	}
	var out []ir.Stmt
	if css := staticCSS(style); css != "" {
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: node,
			Func:     &ir.Func{Name: "setAttribute"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Value: "style"}},
				{Value: &ir.Literal{Type: ir.TypString, Value: css}},
			},
		}})
	}
	_, writes := splitStyle(style)
	for _, w := range writes {
		if len(w.decls) > 1 {
			t.jc.Ctx.Helpers[setStylesHelper] = true
			out = append(out, &ir.CallStmt{Call: &ir.Call{
				Type: ir.TypVoid,
				Func: &ir.Func{Name: setStylesHelper},
				Args: []ir.CallArg{{Value: decl}, {Value: w.group(style)}},
			}})
			continue
		}
		d := w.decls[0]
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: decl,
			Func:     &ir.Func{Name: "setProperty"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Value: d.property}},
				{Value: d.value},
			},
		}})
	}
	return out
}

// styleWriteUpdaters writes a page node's run-time declarations on first
// render and, unless lowering already put the writes in the handlers, again
// whenever what each reads changes: one updater per declaration, so a change
// rewrites only the declarations that read it.
func (g *htmlGen) styleWriteUpdaters(id string, style ir.Expr, lowered bool) {
	if _, ok := declarationsOf(style); !ok {
		g.ctx.Helpers[cssTextHelper] = true
		js, requires := g.exprToJSReactiveCollect(style)
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: fmt.Sprintf("$u_%s_style", id[1:]),
			body:     fmt.Sprintf(`%s.style.cssText = %s(%s);`, id, cssTextHelper, js),
			deps:     g.exprDeps(style),
			initOnly: lowered,
			requires: requires,
		})
		return
	}
	_, writes := splitStyle(style)
	for i, w := range writes {
		var body string
		value := w.decls[0].value
		if len(w.decls) > 1 {
			g.ctx.Helpers[setStylesHelper] = true
			value = w.group(style)
		}
		js, requires := g.exprToJSReactiveCollect(value)
		if len(w.decls) > 1 {
			body = fmt.Sprintf(`%s(%s.style, %s);`, setStylesHelper, id, js)
		} else {
			body = fmt.Sprintf(`%s.style.setProperty(%q, %s);`, id, w.decls[0].property, js)
		}
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: fmt.Sprintf("$u_%s_style_%d", id[1:], i),
			body:     body,
			deps:     g.exprDeps(value),
			initOnly: lowered,
			requires: requires,
		})
	}
}
