package gtk4

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Rich text on GTK, which is the one target where the *host* resolves the
// cascade. Pango markup nests -- `<b><i>x</i></b>` is what `bold { italic {
// … } }` means -- so nothing is flattened here and the tree an author wrote is
// the tree GTK is handed. What this file builds is the string.
//
// A flow is one GtkLabel and a span is no widget at all, so the span nodes
// reach the walk and emit nothing of their own. The markup is assembled as a
// Go *expression*: a run whose words are a literal is escaped at build time
// and one whose words come from state is `gtk4rt.Escape(expr)`, concatenated
// with the rest. That is what makes reactivity cost nothing -- the updater
// that moved the state assigns the span's `text` prop, and this sets the whole
// label again from the same expression.
//
// The tree is collected ahead of the walk rather than accumulated during it,
// for the reason fyne's collectNodes gives: a reactive splice sets a prop on a
// span created in a sibling Func, and the scope emitting that splice has never
// seen the flow it belongs to.

const (
	flowTag = "Flow"
	spanTag = "Span"
)

// markupTrees is this scope's view of the package's flows and spans. Empty
// rather than nil for a translator built without a shared struct, which is
// every unit test that emits one widget.
func (t *gtk4Translator) markupTrees() *markupTrees {
	if t.shared == nil || t.shared.markup == nil {
		return newMarkupTrees()
	}
	return t.shared.markup
}

// markupSpan is one span the pre-pass found: what it was instantiated with,
// and the spans written inside it, in order.
type markupSpan struct {
	props map[string]ir.Expr
	kids  []string
}

// markupTrees is every flow in the package and the spans under it.
type markupTrees struct {
	spans map[string]*markupSpan
	// flows maps a flow's node id to its own style, and roots to the spans
	// written directly in it.
	flows map[string]map[string]ir.Expr
	roots map[string][]string
	// ownerOf is the flow a span belongs to, however deep. It is what lets a
	// prop assignment in a handler reach the label it has to rewrite.
	ownerOf map[string]string
}

func newMarkupTrees() *markupTrees {
	return &markupTrees{
		spans:   map[string]*markupSpan{},
		flows:   map[string]map[string]ir.Expr{},
		roots:   map[string][]string{},
		ownerOf: map[string]string{},
	}
}

// collectMarkup walks everything in the package that may hold a view body and
// records the flows and spans in it.
//
// Every owner is walked rather than only the Model's funcs, because a span may
// be created in a slot renderer hanging off a component and assigned in a
// handler promoted out of another one.
func collectMarkup(pkg *ir.Package) *markupTrees {
	mt := newMarkupTrees()
	if pkg == nil {
		return mt
	}
	var edges [][2]string
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for i, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if tag, ok := createNodeTag(n); ok && (tag == flowTag || tag == spanTag) {
					props := harvestNodeProps(n.Name, stmts[i+1:])
					if tag == flowTag {
						mt.flows[n.Name] = props
					} else {
						mt.spans[n.Name] = &markupSpan{props: props}
					}
				}
			case *ir.CallStmt:
				if p, c, ok := appendEdge(n); ok {
					edges = append(edges, [2]string{p, c})
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.NodeInst:
				walk(n.Children)
			case *ir.Window:
				walk(n.Body)
			}
		}
	}
	for _, o := range ir.Owners(pkg) {
		if o.Body != nil {
			walk(*o.Body)
		}
		for _, fn := range o.Funcs {
			if fn != nil {
				walk(fn.Block)
			}
		}
	}
	for _, e := range edges {
		parent, child := e[0], e[1]
		if _, ok := mt.spans[child]; !ok {
			continue
		}
		if _, ok := mt.flows[parent]; ok {
			mt.roots[parent] = append(mt.roots[parent], child)
			continue
		}
		if sp, ok := mt.spans[parent]; ok {
			sp.kids = append(sp.kids, child)
		}
	}
	for flow, roots := range mt.roots {
		for _, r := range roots {
			mt.claim(flow, r)
		}
	}
	return mt
}

// claim records which flow a span's words end up in, down the whole subtree.
func (mt *markupTrees) claim(flow, span string) {
	if _, seen := mt.ownerOf[span]; seen {
		return
	}
	mt.ownerOf[span] = flow
	if sp := mt.spans[span]; sp != nil {
		for _, k := range sp.kids {
			mt.claim(flow, k)
		}
	}
}

// createNodeTag reads the tag off a `__nN = lower.CreateNode("tag")` binding.
func createNodeTag(lv *ir.LocalVar) (string, bool) {
	call, ok := lv.Init.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Intrinsic != "CreateNode" || len(call.Args) < 1 {
		return "", false
	}
	lit, ok := call.Args[0].Value.(*ir.Literal)
	if !ok || lit.Type != ir.TypString {
		return "", false
	}
	return lit.Value, true
}

// harvestNodeProps reads the prop assignments lowering emits immediately after
// a CreateNode, which is the only place they are known to be in scope.
// Mirrors fyne's harvestSpec.
func harvestNodeProps(id string, rest []ir.Stmt) map[string]ir.Expr {
	props := map[string]ir.Expr{}
	for _, s := range rest {
		asn, ok := s.(*ir.Assign)
		if !ok {
			break
		}
		sel, ok := asn.Target.(*ir.Select)
		if !ok {
			break
		}
		ident, ok := sel.Operand.(*ir.Ident)
		if !ok || !ident.IsElementRef || ident.Name != id {
			break
		}
		props[sel.Field] = asn.Value
	}
	return props
}

// appendEdge reads the parent and child of a `lower.AppendChild(p, c)`.
func appendEdge(cs *ir.CallStmt) (parent, child string, ok bool) {
	call := cs.Call
	if call == nil || call.Func == nil || call.Func.Intrinsic != ir.NodeOpAppendChild || len(call.Args) < 2 {
		return "", "", false
	}
	parent = codegen.IdentBareName(call.Args[0].Value)
	child = codegen.IdentBareName(call.Args[1].Value)
	return parent, child, parent != "" && child != ""
}

// --- Emission ---

// emitFlowCreate builds the GtkLabel one flow is, and sets it up as a
// paragraph: wrapped, and starting at the left rather than centred, which is
// what every other target does with a run of prose.
func (t *gtk4Translator) emitFlowCreate(id string) []ir.Stmt {
	var ctor ir.Expr = nativeCall("gtk_label_new", &ir.Literal{Type: ir.TypNull})
	if t.wrapped {
		if rt, ok := rtCtorForCType("GtkLabel"); ok {
			ctor = rt
		}
	}
	out := t.emitConstructorAssign(id, "GtkLabel", ctor)
	ref := t.qualifyNodeExpr(&ir.Ident{Name: id, IsElementRef: true})
	if t.wrapped {
		out = append(out,
			&ir.CallStmt{Call: rtCall("LabelSetWrap", ref, &ir.Literal{Type: ir.TypBool, Value: "true"})},
			&ir.CallStmt{Call: rtCall("LabelSetXAlign", ref, &ir.Literal{Type: ir.TypFloat, Value: "0"})},
		)
	} else {
		label := cgoCast("GtkLabel", ref)
		out = append(out,
			&ir.CallStmt{Call: nativeCall("gtk_label_set_wrap", label, nativeCall("gboolean", intLit("1")))},
			&ir.CallStmt{Call: nativeCall("gtk_label_set_xalign", cgoCast("GtkLabel", ref), nativeCall("float", &ir.Literal{Type: ir.TypFloat, Value: "0"}))},
		)
	}
	return append(out, t.emitFlowMarkup(id)...)
}

// emitFlowMarkup sets a flow's label from the whole of its span tree.
//
// Emitted where the label is built and again wherever a span's prop is
// assigned, which is the whole of this target's reactivity: the expression is
// the same one either way, so re-evaluating it is re-reading whatever state
// the runs were written against.
func (t *gtk4Translator) emitFlowMarkup(flow string) []ir.Stmt {
	expr := t.markupExpr(flow)
	ref := t.qualifyNodeExpr(&ir.Ident{Name: flow, IsElementRef: true})
	if t.wrapped {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("LabelSetMarkup", ref, expr)}}
	}
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall("gtk_label_set_markup",
		cgoCast("GtkLabel", ref), nativeCall("CString", expr))}}
}

// markupExpr is the Pango markup one flow renders, as a Go string expression.
func (t *gtk4Translator) markupExpr(flow string) ir.Expr {
	open, close := flowTags(t.markupTrees().flows[flow])
	parts := []ir.Expr{strLit(open)}
	for _, root := range t.markupTrees().roots[flow] {
		parts = append(parts, t.spanParts(root)...)
	}
	parts = append(parts, strLit(close))
	return concat(parts)
}

// spanParts is one span's markup: its own tags around its words and the spans
// written inside it.
func (t *gtk4Translator) spanParts(id string) []ir.Expr {
	sp := t.markupTrees().spans[id]
	if sp == nil {
		return nil
	}
	open, close := spanTags(sp.props)
	parts := []ir.Expr{strLit(open)}
	if text, ok := sp.props["text"]; ok && text != nil {
		parts = append(parts, escaped(text))
	}
	for _, k := range sp.kids {
		parts = append(parts, t.spanParts(k)...)
	}
	return append(parts, strLit(close))
}

// escaped is one run's words, escaped: at build time when they are a literal,
// and through the runtime otherwise. The literal case is most of a document
// and is why there is a build-time half at all -- a page of prose would
// otherwise be a page of calls.
func escaped(e ir.Expr) ir.Expr {
	if s, ok := codegen.IRLiteralString(e); ok {
		return strLit(escapePango(s))
	}
	return rtStrCall("Escape", e)
}

// escapePango is pkg/go/gtk4rt's Escape at build time. The two have to agree,
// which is why the characters are listed once in each and nowhere else.
func escapePango(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// flowTags is the span element a flow's own typography becomes.
//
// A flow's `style` is a `ui.Style` and this platform answers none of it
// otherwise -- gtk4 has no per-widget styling and `OnPropAssign` drops the
// prop outright -- so the five typography fields are answered here, where
// Pango has them, and the rest is still dropped. That is also what seeds the
// cascade: everything inside inherits from this element, which is the host
// doing the work every other target had to do itself.
func flowTags(style map[string]ir.Expr) (open, close string) {
	sl, ok := style[stylePropName].(*ir.StructLit)
	if !ok {
		return "", ""
	}
	var attrs []string
	for _, f := range sl.Fields {
		switch f.Name {
		case "color":
			if c := colorFromExpr(f.Value); c != nil && c.A > 0 {
				attrs = append(attrs, fmt.Sprintf(`foreground="%s"`, hexColor(c)))
			}
		case "fontSize":
			if px, ok := literalNumberOf(f.Value); ok && px > 0 {
				attrs = append(attrs, pangoSize(px))
			}
		case "fontFamily":
			if s, ok := codegen.IRLiteralString(f.Value); ok && s != "" {
				attrs = append(attrs, fmt.Sprintf(`font_family="%s"`, escapePango(s)))
			}
		case "fontWeight":
			if enumOrStringOf(f.Value) == "bold" {
				attrs = append(attrs, `weight="bold"`)
			}
		case "fontStyle":
			if enumOrStringOf(f.Value) == "italic" {
				attrs = append(attrs, `style="italic"`)
			}
		}
	}
	if len(attrs) == 0 {
		return "", ""
	}
	return "<span " + strings.Join(attrs, " ") + ">", "</span>"
}

// spanTags is what one run says about its own words, as Pango elements.
//
// The named elements come first and the attribute span last, so the nesting a
// reader sees matches the order the fields are written in. A field the run
// left alone contributes nothing, which is the cascade's one requirement on
// this side and is why Pango can be left to do the rest.
func spanTags(props map[string]ir.Expr) (open, close string) {
	var opens, closes []string
	add := func(tag string) {
		opens = append(opens, "<"+tag+">")
		closes = append([]string{"</" + tag + ">"}, closes...)
	}
	if href, ok := codegen.IRLiteralString(props["href"]); ok && href != "" {
		opens = append(opens, fmt.Sprintf(`<a href="%s">`, escapePango(href)))
		closes = append([]string{"</a>"}, closes...)
	}
	var attrs []string
	if kind := enumOrStringOf(props["kind"]); kind != "" {
		if c, ok := tokenColors[kind]; ok {
			attrs = append(attrs, fmt.Sprintf(`foreground="%s"`, c))
		}
	}
	if sl, ok := props["spanStyle"].(*ir.StructLit); ok {
		for _, f := range sl.Fields {
			switch f.Name {
			case "fontWeight":
				switch enumOrStringOf(f.Value) {
				case "bold", "bolder":
					add("b")
				case "normal", "lighter":
					attrs = append(attrs, `weight="normal"`)
				}
			case "fontStyle":
				switch enumOrStringOf(f.Value) {
				case "italic", "oblique":
					add("i")
				case "normal":
					attrs = append(attrs, `style="normal"`)
				}
			case "underline":
				if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
					add("u")
				}
			case "strike":
				if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
					add("s")
				}
			case "fontFamily":
				if s, ok := codegen.IRLiteralString(f.Value); ok && s == "monospace" {
					add("tt")
				} else if ok && s != "" {
					attrs = append(attrs, fmt.Sprintf(`font_family="%s"`, escapePango(s)))
				}
			case "color":
				if codegen.SpanStyleUnsetColor(f.Value) {
					continue
				}
				if c := colorFromExpr(f.Value); c != nil {
					attrs = append(attrs, fmt.Sprintf(`foreground="%s"`, hexColor(c)))
				}
			case "fontSize":
				if px, ok := literalNumberOf(f.Value); ok && px > 0 {
					attrs = append(attrs, pangoSize(px))
				}
			}
		}
	}
	if len(attrs) > 0 {
		opens = append(opens, "<span "+strings.Join(attrs, " ")+">")
		closes = append([]string{"</span>"}, closes...)
	}
	return strings.Join(opens, ""), strings.Join(closes, "")
}

// tokenColors is what a markup.Token looks like on this host.
//
// A Pango foreground, which is what the family's own declaration nominates for
// gtk4 -- Pango markup has no classes and no theme names, so a color is the
// only thing it can be told. The names are CSS's, which Pango parses, and are
// chosen dark enough to read on the light theme GTK ships with; the open half
// of the palette question is a host with real colors reading them from
// somewhere an application writes, and this is not that yet.
//
// Four kinds are absent and render in the label's own color, which is what
// most themes do with them.
var tokenColors = map[string]string{
	"keyword":  "purple",
	"function": "navy",
	"type":     "olive",
	"constant": "olive",
	"number":   "teal",
	"string":   "green",
	"comment":  "gray",
}

// pangoSize is a text size as Pango takes one: thousandths of a point, near
// enough, the unit being 1024ths. A SNGL measurement is in device-independent
// pixels, which this platform reads as points the way every other measurement
// here is read as one number.
func pangoSize(px float64) string {
	return fmt.Sprintf(`size="%d"`, int(px*1024))
}

// markupColor is an RGBA read off a SNGL color literal. Its own reader rather
// than the layout one's, because a colour that is not a literal is no colour
// here: the markup is assembled once with the widget tree.
type markupColor struct{ R, G, B, A uint8 }

func colorFromExpr(e ir.Expr) *markupColor {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return nil
	}
	var c markupColor
	into := map[string]*uint8{"r": &c.R, "g": &c.G, "b": &c.B, "a": &c.A}
	seen := 0
	for _, f := range sl.Fields {
		p, ok := into[f.Name]
		if !ok {
			continue
		}
		v, ok := literalNumberOf(f.Value)
		if !ok {
			return nil
		}
		*p = uint8(v)
		seen++
	}
	if seen == 0 {
		return nil
	}
	return &c
}

// literalNumberOf reads a numeric literal, with or without a unit suffix: a
// measurement is written `6` or `6px` and both mean one number here.
func literalNumberOf(e ir.Expr) (float64, bool) {
	lit, ok := e.(*ir.Literal)
	if !ok || lit == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// enumOrStringOf reads an enum member, or a plain string, as its name. Both
// shapes reach here: the member after the optimizer folded it and the string
// before, since a caller may generate from unoptimized IR.
func enumOrStringOf(e ir.Expr) string {
	switch v := e.(type) {
	case *ir.Ident:
		if v.Member != "" {
			return v.Member
		}
	case *ir.Select:
		return v.Field
	}
	s, _ := codegen.IRLiteralString(e)
	return s
}

func hexColor(c *markupColor) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// concat folds the pieces into one Go expression, dropping the empty ones and
// running adjacent literals together so the emitted source reads as the markup
// it is rather than as a chain of empty strings.
func concat(parts []ir.Expr) ir.Expr {
	var out []ir.Expr
	for _, p := range parts {
		if lit, ok := p.(*ir.Literal); ok && lit.Type == ir.TypString {
			if lit.Value == "" {
				continue
			}
			if n := len(out); n > 0 {
				if prev, ok := out[n-1].(*ir.Literal); ok && prev.Type == ir.TypString {
					out[n-1] = strLit(prev.Value + lit.Value)
					continue
				}
			}
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return strLit("")
	}
	e := out[0]
	for _, p := range out[1:] {
		e = &ir.Binary{Type: ir.TypString, Op: ast.BinAdd, Left: e, Right: p}
	}
	return e
}

func strLit(s string) *ir.Literal { return &ir.Literal{Type: ir.TypString, Value: s} }

// rtStrCall is rtCall for a gtk4rt function returning a string, so the
// concatenation around it types as one.
func rtStrCall(name string, args ...ir.Expr) *ir.Call {
	call := rtCall(name, args...)
	call.Type = ir.TypString
	return call
}

// sortedFlows is the flows a pre-pass found, in a stable order. Only the tests
// read it; the emission is driven by the walk.
func (mt *markupTrees) sortedFlows() []string {
	out := make([]string, 0, len(mt.flows))
	for id := range mt.flows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
