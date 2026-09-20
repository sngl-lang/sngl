package html

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/ir"
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
// Four of the seven fields need no test. `fontFamily` is a string and an empty
// one is already skipped, and `fontWeight: inherit` and `font-style: inherit`
// are real CSS that means exactly what the unset state means. The other three
// are the ones whose zero would be a choice: a size of nought, a color nobody
// can see, and a decoration that is off.
func spanStyleCSS(n *ir.NodeInst) string {
	sl, ok := codegen.NodeProp(n, spanStyleProp).(*ir.StructLit)
	if !ok {
		return ""
	}
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
			if transparentColor(f.Value) {
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

// transparentColor reports whether a color literal is the one a `SpanStyle`
// says "no color" with: alpha zero, which is the one value that cannot also be
// a choice, since nothing painted with it would be visible.
func transparentColor(e ir.Expr) bool {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return false
	}
	for _, f := range sl.Fields {
		if f.Name != "a" {
			continue
		}
		lit, ok := f.Value.(*ir.Literal)
		return ok && lit.Value == "0"
	}
	// No alpha written at all is an opaque color: `#336699` fills it in.
	return false
}
