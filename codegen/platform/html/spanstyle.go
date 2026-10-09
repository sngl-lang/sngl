package html

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/htmlutil"
	"duckfam.us/sngl/ir"
)

// spanStyleProp is the prop `inline` carries a run's own style in. Read here
// and never emitted as an attribute.
const spanStyleProp = "spanStyle"

// nodeInlineCSS is everything an element's style attribute holds: the box
// style any node may carry, and the run style a span carries.
//
// One function because it is one attribute. Written as two, an element holding
// both -- a `richText` is not one today, but nothing stops one -- emits `style`
// twice and the browser keeps the last, which is the box style silently
// winning over the words. It is also what keeps the client render and the
// server render from drifting: both ask this.
func nodeInlineCSS(n *ir.NodeInst) string {
	css := htmlutil.BuildCSSStyleIR(n.Props)
	if span := spanStyleCSS(n); span != "" {
		if css != "" {
			css += ";"
		}
		css += span
	}
	return css
}

// spanStyleCSS is the CSS a run's `SpanStyle` stands for.
//
// It is read here rather than mapped in `html.sngl` because the question is
// about the *literal*: a `SpanStyle` field left alone must emit nothing, and
// which fields those are cannot be asked of a prop -- a component that tested
// `style.fontWeight == Weight.bold` tested an opaque value, so every branch
// survived and the words came out once per branch.
//
// `fontFamily` needs no test, an empty one being already skipped. The weight
// and slant are skipped at `inherit`: it is real CSS meaning what the unset
// state means, and written it put two declarations on every token of a code
// sample. The other three are the ones whose zero would be a choice: a size of
// nought, a color nobody can see, and a decoration that is off.
func spanStyleCSS(n *ir.NodeInst) string {
	sl, ok := codegen.NodeProp(n, spanStyleProp).(*ir.StructLit)
	if !ok {
		return ""
	}
	return spanStyleLitCSS(sl)
}

func spanStyleLitCSS(sl *ir.StructLit) string {
	var parts []string
	var decor []string
	for _, f := range sl.Fields {
		switch f.Name {
		case "underline":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				decor = append(decor, "underline")
			}
		case "strike":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				decor = append(decor, "line-through")
			}
		case "fontSize":
			if lit, ok := f.Value.(*ir.Literal); ok {
				if mag, _, ok := ir.UnitMagnitude(lit); ok && mag == 0 {
					continue
				}
			}
			parts = append(parts, htmlutil.StylePropToCSSIR(f.Name, f.Value))
		case "color":
			if codegen.SpanStyleUnsetColor(f.Value) {
				continue
			}
			parts = append(parts, htmlutil.StylePropToCSSIR(f.Name, f.Value))
		case "fontWeight", "fontStyle":
			if codegen.SpanStyleInherit(f.Value) {
				continue
			}
			parts = append(parts, htmlutil.StylePropToCSSIR(f.Name, f.Value))
		default:
			parts = append(parts, htmlutil.StylePropToCSSIR(f.Name, f.Value))
		}
	}
	if len(decor) > 0 {
		// One property for both, which is also why a span inside one that
		// turned a decoration on cannot turn it off.
		parts = append(parts, "text-decoration:"+strings.Join(decor, " "))
	}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ";")
}

// The rule a literal class stands for, in the light scheme and the dark one.
// A `SpanStyle` on a span and a `ui.Style` on a flow, both read by
// `spanStyleLitCSS`, which is exact for the fields a flow's rule writes but
// drops a zero color or font size. Written once per page to the stylesheet
// rather than onto the element, which is what lets the dark half answer to
// `prefers-color-scheme`; under `:where`, so a page's own CSS wins over both.
const (
	classStyleProp     = "classStyle"
	classStyleDarkProp = "classStyleDark"
)

// classRules is the stylesheet a node's class asks for: nothing unless the
// class is a literal and one of the two styles sets something.
func classRules(n *ir.NodeInst) []string {
	class, ok := codegen.IRLiteralString(codegen.NodeProp(n, "class"))
	if !ok || class == "" || strings.ContainsAny(class, " \t\n") {
		return nil
	}
	var rules []string
	if sl, ok := codegen.NodeProp(n, classStyleProp).(*ir.StructLit); ok {
		if css := spanStyleLitCSS(sl); css != "" {
			rules = append(rules, ":where(."+class+") { "+css+" }")
		}
	}
	if sl, ok := codegen.NodeProp(n, classStyleDarkProp).(*ir.StructLit); ok {
		if css := spanStyleLitCSS(sl); css != "" {
			rules = append(rules, "@media (prefers-color-scheme: dark) { :where(."+class+") { "+css+" } }")
		}
	}
	return rules
}

// spanColorHelper names the page's function from a SNGL color to a CSS one,
// written when a run's color is only known at run time.
const spanColorHelper = "_snglSpanColor"

const spanColorHelperJS = `function _snglSpanColor(c) {
  if (c.a === 0) return "";
  if (c.a === 255) return "#" + [c.r, c.g, c.b].map(v => v.toString(16).padStart(2, "0")).join("");
  return "rgba(" + c.r + "," + c.g + "," + c.b + "," + c.a / 255 + ")";
}
`

// spanStyleWrites is a run's style written to an element that exists, field by
// field so the box style on the same attribute survives. A field the build can
// read is its declaration; a color it cannot is converted where the page runs,
// the unset one clearing the property rather than painting nothing.
func (t *htmlTranslator) spanStyleWrites(node ir.Expr, sl *ir.StructLit) []ir.Stmt {
	style := &ir.Select{Operand: node, Field: "style", Type: ir.TypDyn}
	var out []ir.Stmt
	for _, f := range sl.Fields {
		if f.Name == "color" && !codegen.SpanStyleKnownColor(f.Value) {
			t.jc.Ctx.Helpers[spanColorHelper] = true
			out = append(out, &ir.Assign{
				Target: &ir.Select{Operand: style, Field: "color", Type: ir.TypDyn},
				Op:     ast.AssignSet,
				Value: &ir.Call{
					Type: ir.TypString,
					Func: &ir.Func{Name: spanColorHelper},
					Args: []ir.CallArg{{Value: f.Value}},
				},
			})
			continue
		}
		css := spanStyleLitCSS(&ir.StructLit{Type: sl.Type, Fields: []ir.FieldInit{f}})
		for decl := range strings.SplitSeq(css, ";") {
			name, val, ok := strings.Cut(decl, ":")
			if !ok {
				continue
			}
			out = append(out, &ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: style,
				Func:     &ir.Func{Name: "setProperty"},
				Args: []ir.CallArg{
					{Value: &ir.Literal{Type: ir.TypString, Value: name}},
					{Value: &ir.Literal{Type: ir.TypString, Value: val}},
				},
			}})
		}
	}
	return out
}

// spanColorWrite is the updater for a run whose color the page learns only as
// it runs. The rest of the style is a literal and already in the markup.
func (g *htmlGen) spanColorWrite(id string, style ir.Expr, lowered bool) {
	sl, ok := style.(*ir.StructLit)
	if !ok {
		return
	}
	for _, f := range sl.Fields {
		if f.Name != "color" || codegen.SpanStyleKnownColor(f.Value) {
			continue
		}
		g.ctx.Helpers[spanColorHelper] = true
		jsVal, requires := g.exprToJSReactiveCollect(f.Value)
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: fmt.Sprintf("$u_%s_%s", id[1:], spanStyleProp),
			body:     fmt.Sprintf(`%s.style.color = %s(%s);`, id, spanColorHelper, jsVal),
			deps:     g.exprDeps(f.Value),
			initOnly: lowered,
			requires: requires,
		})
	}
}
