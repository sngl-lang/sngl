package bubbletea

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// spanCascade is what a run of words has been told about its appearance by the
// time it is rendered: the flow it sits in, and every span between.
//
// lipgloss has no inheritance -- a style renders a string and the result is a
// string -- so the nesting an author wrote is flattened here and each leaf is
// rendered exactly once with the style it ends up with. Rendering a joined flow
// a second time is not the tidier alternative it looks like: the runs inside it
// have left their reset sequences in the string, and an outer color stops at
// the first of them.
//
// A terminal has one face and one size, so `fontFamily` and `fontSize` are read
// and dropped rather than mapped. That is what `monospace` amounts to here: the
// words are already in the only face there is.
type spanCascade struct {
	// color is the argument to lipgloss.Color(...), already quoted, or "" for
	// the terminal's own foreground.
	color     string
	bold      bool
	italic    bool
	underline bool
	strike    bool
	faint     bool
}

// empty reports whether this run needs a style at all. A flow of plain words
// is the common case, and a lipgloss.NewStyle().Render around each of them
// would be an ANSI reset per leaf for no visible difference.
func (c spanCascade) empty() bool { return c == spanCascade{} }

// style renders the cascade as a lipgloss builder chain, or "" when empty.
func (c spanCascade) style() string {
	if c.empty() {
		return ""
	}
	chain := []string{"lipgloss.NewStyle()"}
	if c.color != "" {
		chain = append(chain, fmt.Sprintf("Foreground(lipgloss.Color(%s))", c.color))
	}
	if c.bold {
		chain = append(chain, "Bold(true)")
	}
	if c.italic {
		chain = append(chain, "Italic(true)")
	}
	if c.underline {
		chain = append(chain, "Underline(true)")
	}
	if c.strike {
		chain = append(chain, "Strikethrough(true)")
	}
	if c.faint {
		chain = append(chain, "Faint(true)")
	}
	return strings.Join(chain, ".")
}

// tokenColors is what a `markup.Token` looks like in a terminal.
//
// The numbers are the terminal's own sixteen colors and not values this
// compiler chose: a document that named #7DAEA3 would be unreadable against a
// theme it never heard of, and a terminal already has a palette its owner
// picked. So the mapping answers the family's "a kind is answered by whoever is
// drawing" with an index into that palette, which is the closest a target with
// no stylesheet comes to letting an application override it.
//
// Three kinds map to nothing on purpose. `variable`, `operator` and
// `punctuation` are what most themes leave in the foreground color, and
// sixteen colors is a vocabulary to spend on the kinds a reader scans for.
var tokenColors = map[string]string{
	"keyword":  "5", // magenta
	"type":     "3", // yellow
	"function": "4", // blue
	"constant": "6", // cyan
	"number":   "6",
	"string":   "2", // green
	"comment":  "8", // bright black
}

// withToken overlays what a `token` span says it is.
func (c spanCascade) withToken(kind string) spanCascade {
	if col, ok := tokenColors[kind]; ok {
		c.color = strconv.Quote(col)
	}
	if kind == "comment" {
		c.faint = true
	}
	return c
}

// withPaletteColor overlays what a palette the program set says a token is,
// over the terminal color its kind would have had. Unset, the palette says
// nothing: at build time that is no change, and at run time it is the color
// already chosen, handed to `Or` as the fallback.
func (vc *irViewContext) withPaletteColor(c spanCascade, e ir.Expr) spanCascade {
	if e == nil || codegen.SpanStyleUnsetColor(e) {
		return c
	}
	if codegen.SpanStyleKnownColor(e) {
		c.color = lipglossColor(e, vc.gc)
		return c
	}
	fallback := c.color
	if fallback == "" {
		fallback = `""`
	}
	c.color = fmt.Sprintf("%s.Or(%s)", vc.gc.EvalExpr(e), fallback)
	return c
}

// withSpanStyle overlays what one run said about its own words.
//
// A field the run left alone leaves the enclosing run's answer standing, which
// is the whole reason `markup.SpanStyle`'s two enums lead with `inherit` and
// its color says "unset" with alpha zero. The decorations are the exception the
// declaration documents: a span inside one that turned underline on cannot turn
// it off, because there is no fourth state to say so with.
func (vc *irViewContext) withSpanStyle(c spanCascade, e ir.Expr) spanCascade {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return c
	}
	for _, f := range sl.Fields {
		switch f.Name {
		case "color":
			if !codegen.SpanStyleUnsetColor(f.Value) {
				c.color = lipglossColor(f.Value, vc.gc)
			}
		case "fontWeight":
			switch enumMember(f.Value) {
			case "bold", "bolder":
				c.bold = true
			case "lighter":
				c.faint = true
			case "normal":
				c.bold, c.faint = false, false
			}
		case "fontStyle":
			switch enumMember(f.Value) {
			case "italic", "oblique":
				c.italic = true
			case "normal":
				c.italic = false
			}
		case "underline":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				c.underline = true
			}
		case "strike":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				c.strike = true
			}
		}
	}
	return c
}

// enumMember names the member an enum-valued prop holds: an *ir.Ident carrying
// it, whether the member was written bare or as `Weight.bold`. Mirrors
// extractJoinDir, which asks the same question of JoinDir.
func enumMember(e ir.Expr) string {
	if v, ok := e.(*ir.Ident); ok {
		return v.Member
	}
	s, _ := codegen.IRLiteralString(e)
	return s
}

// spanSeedFields are the fields of a flow's own `ui.Style` that seed the
// cascade rather than styling the box.
//
// They have to be taken out of the box style rather than left in it as well:
// applying Bold to the joined flow is the second render the type's comment
// rules out, and the first inner reset would end it partway along the line.
// `fontSize` and `fontFamily` are here because a terminal honors neither, so
// leaving them in the box style would be a lipgloss call that does nothing.
var spanSeedFields = map[string]bool{
	"color": true, "fontWeight": true, "fontStyle": true,
	"fontSize": true, "fontFamily": true,
}

// splitFlowStyle divides a flow's style into the cascade its words start from
// and the fields that style the block it sits in.
func (vc *irViewContext) splitFlowStyle(fields []codegen.StyleField) (spanCascade, []codegen.StyleField) {
	var seed spanCascade
	box := make([]codegen.StyleField, 0, len(fields))
	for _, f := range fields {
		if !spanSeedFields[f.Name] {
			box = append(box, f)
			continue
		}
		switch f.Name {
		case "color":
			seed.color = lipglossColor(f.Value, vc.gc)
		case "fontWeight":
			if enumMember(f.Value) == "bold" {
				seed.bold = true
			}
		case "fontStyle":
			if enumMember(f.Value) == "italic" {
				seed.italic = true
			}
		}
	}
	return seed, box
}

// renderFlow emits one flow of rich text: the words appended in order, then the
// block style applied to the whole of it.
func (vc *irViewContext) renderFlow(n *ir.NodeInst, resultVar string) {
	seed, box := vc.splitFlowStyle(codegen.NodeStyleFields(n))
	vc.line(`%s = ""`, resultVar)
	for _, child := range n.Children {
		vc.renderSpan(child, resultVar, seed)
	}
	if style := buildIRStyleExpr(box, vc.gc, vc.scaleFactor); style != "lipgloss.NewStyle()" {
		vc.line(`%s = %s.Render(%s)`, resultVar, style, resultVar)
	}
}

// renderSpan appends one span's words to the flow's accumulator, carrying the
// cascade down to the leaves.
//
// It is a walk of its own rather than a case in renderStmt because the cascade
// is what it carries: renderStmt hands each child a var of its own and joins
// the results, which is exactly the second render a flow cannot afford.
func (vc *irViewContext) renderSpan(stmt ir.Stmt, accVar string, c spanCascade) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		if btIntrinsic(s) != "Span" {
			// A span that is not the primitive: a recursive component in the
			// family, which nothing substitutes away. It renders its words
			// through the ordinary path, which is to say without the cascade.
			vc.renderSpanFallback(s, accVar)
			return
		}
		c = vc.withSpanStyle(c, codegen.NodeProp(s, "spanStyle"))
		if k := codegen.NodeProp(s, "kind"); k != nil {
			c = c.withToken(enumMember(k))
		}
		c = vc.withPaletteColor(c, codegen.NodeProp(s, "color"))
		if text := codegen.NodeProp(s, "text"); text != nil {
			vc.requireImport("fmt")
			words := fmt.Sprintf("fmt.Sprint(%s)", vc.gc.EvalExpr(text))
			if style := c.style(); style != "" {
				words = fmt.Sprintf("%s.Render(%s)", style, words)
			}
			vc.line(`%s += %s`, accVar, words)
		}
		for _, child := range s.Children {
			vc.renderSpan(child, accVar, c)
		}
	case *ir.If:
		vc.line("%s", vc.gc.IfHead(s, vc.gc.EvalExpr(s.Cond)))
		vc.indent++
		for _, child := range s.Body {
			vc.renderSpan(child, accVar, c)
		}
		vc.indent--
		if len(s.Else) > 0 {
			vc.line("%s", vc.gc.ElseHead())
			vc.indent++
			for _, child := range s.Else {
				vc.renderSpan(child, accVar, c)
			}
			vc.indent--
		}
		vc.line("%s", vc.gc.BlockEnd())
	case *ir.For:
		vc.renderSpanFor(s, accVar, c)
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderSpan(child, accVar, c)
		}
	case *ir.SlotInst:
		fn, call := vc.slotCall(s, accVar)
		vc.line("if %s != nil {", fn)
		vc.indent++
		vc.line("%s += %s", accVar, call)
		vc.indent--
		if len(s.Children) > 0 {
			vc.line("} else {")
			vc.indent++
			for _, child := range s.Children {
				vc.renderSpan(child, accVar, c)
			}
			vc.indent--
		}
		vc.line("}")
	default:
		// Anything else in a flow renders nothing: an imperative statement a
		// lowering pass hoisted here has no words to contribute.
		vc.renderStmt(stmt, "")
	}
}

// renderSpanFor walks a loop inside a flow. The loop head is the Go driver's,
// as it is everywhere else; what differs is that each iteration appends to the
// one accumulator rather than to a slice that is joined afterwards.
func (vc *irViewContext) renderSpanFor(s *ir.For, accVar string, c spanCascade) {
	loopGC := vc.gc
	if s.Key != "" && s.Key != "_" {
		loopGC = loopGC.WithLocal(s.Key)
	}
	if s.Value != "" && s.Value != "_" {
		loopGC = loopGC.WithLocal(s.Value)
	}
	saved := vc.gc
	vc.gc = loopGC
	vc.line("%s", vc.gc.ForHead(s, vc.gc.EvalExpr(s.Iter)))
	vc.indent++
	for _, child := range s.Body {
		vc.renderSpan(child, accVar, c)
	}
	vc.indent--
	vc.line("%s", vc.gc.BlockEnd())
	vc.gc = saved
}

// renderSpanFallback renders a node in a flow that is not the Span primitive
// through the ordinary path and appends the string it produced.
func (vc *irViewContext) renderSpanFallback(n *ir.NodeInst, accVar string) {
	tmp := accVar + "Part"
	vc.line("var %s string", tmp)
	vc.renderNode(n, tmp)
	vc.line(`%s += %s`, accVar, tmp)
}
