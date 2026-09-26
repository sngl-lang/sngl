package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Rich text on Compose, which is the second target where the *host* resolves
// the cascade and the one it fits best.
//
// A Compose span is not a composable: it is a piece of an `AnnotatedString`
// *value*, built by a `buildAnnotatedString { … }` block. So this file looks
// like gtk4's and not like fyne's or bubbletea's -- a flow is one `Text` and a
// span is no widget at all -- and what it builds is a Kotlin expression.
//
// Nothing is flattened. A Compose `SpanStyle` leaves every field unset by
// default and overlapping styles merge in the order they were pushed, with an
// unset field leaving the enclosing one standing -- which is exactly what
// `markup.SpanStyle`'s `inherit` members mean. So `withStyle` nests the way
// `bold { italic { … } }` nests and Compose does the resolving.
//
// `textDecoration` is the one field that does not work that way, and the only
// thing carried down this walk. Compose applies a decoration as a single span
// setting both of the paint's flags, so an inner run naming LineThrough turns
// the enclosing underline *off* rather than adding to it. A run therefore
// writes the whole set it has accumulated, not just its own.
//
// The tree is read straight off the `ir.NodeInst` children, because android is
// a RenderModel target and that is what its emitter walks. gtk4 needs a
// package-wide pre-pass only because it reads lowered CreateNode statements,
// where a reactive splice assigns a prop in a scope that never saw the flow.
// Reactivity costs nothing here for the same reason bubbletea's does: Compose
// re-renders from state, so the flow is rebuilt with the rest of the screen.

const (
	flowTag = "Flow"
	spanTag = "Span"
)

// decoration is the underline and strikethrough a run has been told about by
// the flow it sits in and every span between.
type decoration struct{ underline, strike bool }

// textDecoration renders the accumulated set as the Compose value, or "" for
// no decoration at all.
func (d decoration) textDecoration() string {
	switch {
	case d.underline && d.strike:
		return "TextDecoration.combine(listOf(TextDecoration.Underline, TextDecoration.LineThrough))"
	case d.underline:
		return "TextDecoration.Underline"
	case d.strike:
		return "TextDecoration.LineThrough"
	}
	return ""
}

// renderFlow emits one flow of rich text as the single `Text` it is.
//
// The `AnnotatedString` is built from the span tree, which no prop can carry,
// so this tag is answered before renderIntrinsic reads the declaration. The
// two Styles are still the declaration's and still go through the paths every
// other node's do: `modifier` is the box the flow sits in, `style` the
// typography its words start from -- which is the seed every other target has
// to apply itself and Compose inherits from the `Text`.
func (cc *irComposeContext) renderFlow(n *ir.NodeInst, comp *ir.Component) {
	cc.kc.RequireImport("androidx.compose.ui.text.buildAnnotatedString")
	body := cc.renderAnnotated(n.Children)
	args := []string{"text = buildAnnotatedString {\n" + body + strings.Repeat("    ", cc.indent) + "}"}
	args = append(args, "modifier = "+cc.intrinsicModifier(n, comp))
	if ts := cc.textStyleExpr(n, "style"); ts != "" {
		args = append(args, "style = "+ts)
	}
	cc.line("Text(%s)", strings.Join(args, ", "))
}

// renderAnnotated renders the spans of a flow into a buffer of their own, one
// level in. Mirrors renderNested, which does the same for a Compose slot.
func (cc *irComposeContext) renderAnnotated(stmts []ir.Stmt) string {
	savedBuf, savedAxis, savedRoot := cc.buf, cc.parentAxis, cc.atRoot
	cc.buf, cc.parentAxis, cc.atRoot = &strings.Builder{}, "", false
	cc.indent++
	for _, stmt := range stmts {
		cc.renderSpan(stmt, decoration{})
	}
	cc.indent--
	body := cc.buf.String()
	cc.buf, cc.parentAxis, cc.atRoot = savedBuf, savedAxis, savedRoot
	return body
}

// renderSpan emits one span into the builder block.
//
// A walk of its own rather than a case in renderStmt, for the reason
// bubbletea's has one: what it carries is the decoration set, and renderStmt
// hands each child a composable rather than a piece of a value.
func (cc *irComposeContext) renderSpan(stmt ir.Stmt, dec decoration) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		if _, tag := composeIntrinsic(s); tag != spanTag {
			// A span that is not the primitive: a recursive component in the
			// family, which nothing substitutes away. There is no composable
			// to call from inside a builder block, so what it says is lost --
			// the same limit fyne and bubbletea reach through their own
			// fallbacks, stated here by rendering nothing.
			return
		}
		cc.renderSpanPrimitive(s, dec)
	case *ir.If:
		cc.line("%s", cc.kc.IfHead(s, cc.kc.EvalExpr(s.Cond)))
		cc.indent++
		for _, child := range s.Body {
			cc.renderSpan(child, dec)
		}
		cc.indent--
		if len(s.Else) > 0 {
			cc.line("%s", cc.kc.ElseHead())
			cc.indent++
			for _, child := range s.Else {
				cc.renderSpan(child, dec)
			}
			cc.indent--
		}
		cc.line("%s", cc.kc.BlockEnd())
	case *ir.For:
		cc.renderSpanFor(s, dec)
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			cc.renderSpan(child, dec)
		}
	}
	// Anything else contributes no words: an imperative statement a lowering
	// pass hoisted into the flow has nothing to append.
}

// renderSpanPrimitive emits the Span primitive: its link, then its style, then
// its words and the spans written inside it.
//
// The link is outside the style because that is the order the two nest in:
// `withLink` marks the range and `withStyle` colors it, and the underline this
// platform's `link` override asks for is a style like any other.
func (cc *irComposeContext) renderSpanPrimitive(n *ir.NodeInst, dec decoration) {
	closes := 0
	if href := codegen.NodeProp(n, "href"); href != nil {
		if h := cc.kc.EvalExpr(href); h != "" && h != `""` {
			cc.kc.RequireImport("androidx.compose.ui.text.LinkAnnotation")
			cc.kc.RequireImport("androidx.compose.ui.text.withLink")
			cc.line("withLink(LinkAnnotation.Url(%s)) {", h)
			cc.indent++
			closes++
		}
	}
	style, dec := cc.spanStyleExpr(n, dec)
	if style != "" {
		cc.kc.RequireImport("androidx.compose.ui.text.SpanStyle")
		cc.kc.RequireImport("androidx.compose.ui.text.withStyle")
		cc.line("withStyle(%s) {", style)
		cc.indent++
		closes++
	}
	if text := codegen.NodeProp(n, "text"); text != nil {
		cc.line("append(%s)", cc.kc.EvalExpr(text))
	}
	for _, child := range n.Children {
		cc.renderSpan(child, dec)
	}
	for range closes {
		cc.indent--
		cc.line("}")
	}
}

// renderSpanFor walks a loop inside a flow. The head is the Kotlin driver's,
// as it is everywhere else; what differs is that the body appends to the one
// builder rather than emitting composables.
func (cc *irComposeContext) renderSpanFor(s *ir.For, dec decoration) {
	loopKC := cc.kc
	if s.Key != "" && s.Key != "_" {
		loopKC = loopKC.WithLocal(s.Key)
	}
	if s.Value != "" && s.Value != "_" {
		loopKC = loopKC.WithLocal(s.Value)
	}
	saved := cc.kc
	cc.kc = loopKC
	cc.line("%s", cc.kc.ForHead(s, cc.kc.EvalExpr(s.Iter)))
	cc.indent++
	for _, child := range s.Body {
		cc.renderSpan(child, dec)
	}
	cc.indent--
	cc.line("%s", cc.kc.BlockEnd())
	cc.kc = saved
}

// spanStyleExpr is what one run says about its own words, as the Compose
// `SpanStyle` to push around them, and the decoration set its children inherit.
//
// Read off the `*ir.StructLit` rather than asked of the prop: which fields a
// `SpanStyle` literal set is a question about the literal, and a prop is
// opaque to the `if` that would ask it. A field the run left alone contributes
// nothing, which is what lets Compose merge the nesting.
func (cc *irComposeContext) spanStyleExpr(n *ir.NodeInst, dec decoration) (string, decoration) {
	var parts []string
	themed := ""
	if kind := enumMember(codegen.NodeProp(n, "kind")); kind != "" {
		if role, ok := tokenColors[kind]; ok {
			themed = "MaterialTheme.colorScheme." + role
		}
	}
	// A palette the program set wins over the scheme's role, and the unset
	// color is the palette saying nothing -- at run time too, where the role
	// is what `let` falls back to.
	switch pal := codegen.NodeProp(n, "color"); {
	case pal == nil || codegen.SpanStyleUnsetColor(pal):
		if themed != "" {
			parts = append(parts, "color = "+themed)
		}
	case codegen.SpanStyleKnownColor(pal):
		parts = append(parts, "color = "+composeColorExpr(cc.kc.EvalExpr(pal)))
	default:
		if themed == "" {
			themed = "ComposeColor.Unspecified"
		}
		parts = append(parts, fmt.Sprintf("color = (%s).let { if (it.a == 0) %s else ComposeColor(it.r, it.g, it.b, it.a) }", cc.kc.EvalExpr(pal), themed))
	}
	own := decoration{}
	sl, _ := codegen.NodeProp(n, "spanStyle").(*ir.StructLit)
	if sl != nil {
		for _, f := range sl.Fields {
			switch f.Name {
			case "color":
				if codegen.SpanStyleUnsetColor(f.Value) {
					continue
				}
				parts = append(parts, "color = "+composeColorExpr(cc.kc.EvalExpr(f.Value)))
			case "fontSize":
				if v := cc.styleValue(f.Value); v != "" && v != "0" && v != "0.0" {
					parts = append(parts, "fontSize = "+v+".sp")
				}
			case "fontFamily":
				if s, ok := codegen.IRLiteralString(f.Value); ok && s != "" {
					parts = append(parts, "fontFamily = "+fontFamilyExpr(s))
					cc.kc.RequireImport("androidx.compose.ui.text.font.FontFamily")
				}
			case "fontWeight":
				if w := composeWeight(enumMember(f.Value)); w != "" {
					parts = append(parts, "fontWeight = "+w)
				}
			case "fontStyle":
				if st := composeSlant(enumMember(f.Value)); st != "" {
					parts = append(parts, "fontStyle = "+st)
					cc.kc.RequireImport("androidx.compose.ui.text.font.FontStyle")
				}
			case "underline":
				if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
					own.underline = true
				}
			case "strike":
				if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
					own.strike = true
				}
			}
		}
	}
	// A run that names a decoration writes the whole accumulated set, since
	// the one it names would otherwise turn the enclosing one off; a run that
	// names none writes nothing and leaves the enclosing span's standing.
	if own.underline || own.strike {
		dec.underline = dec.underline || own.underline
		dec.strike = dec.strike || own.strike
		parts = append(parts, "textDecoration = "+dec.textDecoration())
		cc.kc.RequireImport("androidx.compose.ui.text.style.TextDecoration")
	}
	if len(parts) == 0 {
		return "", dec
	}
	return "SpanStyle(" + strings.Join(parts, ", ") + ")", dec
}

// fontFamilyExpr is a face name as Compose spells one. Only the generic
// families are names Compose has: anything else is a font resource an
// application registers, which SNGL has no way to name yet, so it falls back
// to the default rather than to a lookup that would not compile.
func fontFamilyExpr(name string) string {
	switch name {
	case "monospace":
		return "FontFamily.Monospace"
	case "serif":
		return "FontFamily.Serif"
	case "sans-serif":
		return "FontFamily.SansSerif"
	case "cursive":
		return "FontFamily.Cursive"
	}
	return "FontFamily.Default"
}

// composeWeight maps a markup.Weight to the Compose value, or "" for the
// `inherit` that leaves the enclosing run's alone.
func composeWeight(member string) string {
	switch member {
	case "normal":
		return "FontWeight.Normal"
	case "bold":
		return "FontWeight.Bold"
	case "lighter":
		return "FontWeight.Light"
	case "bolder":
		return "FontWeight.ExtraBold"
	}
	return ""
}

// composeSlant maps a markup.Slant. Compose has upright and italic and no
// third face, so `oblique` is italic -- which is what a font without a true
// italic is synthesized into anyway.
func composeSlant(member string) string {
	switch member {
	case "normal":
		return "FontStyle.Normal"
	case "italic", "oblique":
		return "FontStyle.Italic"
	}
	return ""
}

// tokenColors is what a `markup.Token` looks like on this host: a role in the
// Material color scheme, which is the answer fyne gives in its own vocabulary
// and the better half of the family's "a kind is answered by whoever is
// drawing". An application that themes its app themes its code samples with
// it, light and dark come out right without this compiler naming a value, and
// every role is a color Material guarantees reads against the surface it is
// drawn on.
//
// What a scheme does not have is seven hues. Three accents, an error color and
// two muted roles is the vocabulary, so the kinds are grouped by what they
// are: a keyword is the primary accent, the names of things share the
// secondary, the literals share the tertiary, and a comment is
// onSurfaceVariant -- which is the one mapping that is exactly right, a
// comment being what a reader is meant to scan past.
//
// Four kinds are absent and render in the flow's own color, as they do on
// every other target: `variable`, `operator`, `punctuation` and `plain`.
var tokenColors = map[string]string{
	"keyword":  "primary",
	"function": "secondary",
	"type":     "secondary",
	"string":   "tertiary",
	"constant": "tertiary",
	"number":   "tertiary",
	"comment":  "onSurfaceVariant",
}

// enumMember names the member an enum-valued prop holds: an *ir.Ident carrying
// it, whether the member was written bare or as `Weight.bold`.
func enumMember(e ir.Expr) string {
	if v, ok := e.(*ir.Ident); ok {
		return v.Member
	}
	s, _ := codegen.IRLiteralString(e)
	return s
}
