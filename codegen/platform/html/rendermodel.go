package html

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// rendermodel.go builds the language-agnostic per-route render model
// (codegen.RouteRender) that the HTTPCompiler seam carries to the target
// language. The language fills the model's holes by translating their IR
// expressions against its server State via its own *IRContext.
//
// This walk is intentionally SEPARATE from htmlGen.renderIRNode (the static /
// client render path): that walk is tightly coupled to JS updater registration
// and bakes initial values into the emitted string, which is the opposite of
// what we need here (we must keep the binding IR intact and split the skeleton
// at each binding point). Rather than retrofit hole-splitting into that
// JS-entangled path, the route render model is produced by this focused IR
// walk. The two render paths coexist by route kind: a route with server
// actions renders from this model, one without from the baked RenderHTML
// string.

// renderBuilder accumulates a RouteRender: a list of static HTML chunks
// interleaved with holes. cur holds the chunk under construction; pushHole
// flushes cur as a finished chunk and appends a hole.
type renderBuilder struct {
	pkg       *ir.Package
	chunks    []string
	holes     []codegen.RouteHole
	cur       strings.Builder
	state     map[string]bool          // names of state vars (reactive bindings read these)
	actionIdx map[*ir.EventHandler]int // backend handler → action index (shared source of truth with collectActions)

	// err is the first thing the server render could not express. It has no
	// error return -- it builds a skeleton -- so the failure is carried out and
	// reported by the caller rather than written into the page.
	err error
}

func (rb *renderBuilder) fail(format string, args ...any) {
	if rb.err == nil {
		rb.err = fmt.Errorf(format, args...)
	}
}

func (rb *renderBuilder) writeRaw(s string) { rb.cur.WriteString(s) }

func (rb *renderBuilder) pushHole(h codegen.RouteHole) {
	rb.chunks = append(rb.chunks, rb.cur.String())
	rb.cur.Reset()
	rb.holes = append(rb.holes, h)
}

// sub renders a nested skeleton -- the body of a request-dependent if or for.
// names are identifiers the enclosing hole binds (a loop variable), which the
// server has a value for inside the hole and the nested render may therefore
// use.
func (rb *renderBuilder) sub(stmts []ir.Stmt, names ...string) *codegen.RouteRender {
	child := &renderBuilder{pkg: rb.pkg, state: rb.state, actionIdx: rb.actionIdx}
	if len(names) > 0 {
		child.state = maps.Clone(rb.state)
		for _, n := range names {
			if n != "" {
				child.state[n] = true
			}
		}
	}
	for _, s := range stmts {
		child.walkStmt(s)
	}
	if child.err != nil {
		rb.fail("%w", child.err)
	}
	return child.finish()
}

func (rb *renderBuilder) finish() *codegen.RouteRender {
	rb.chunks = append(rb.chunks, rb.cur.String())
	rb.cur.Reset()
	return &codegen.RouteRender{Chunks: rb.chunks, Holes: rb.holes}
}

// buildRenderModel walks a route window's visual tree into a RouteRender:
// static HTML in Chunks, reactive bindings as Holes. Backend handlers (per
// handlerPlacement) wrap their triggering element in a server-action <form>.
func buildRenderModel(pkg *ir.Package, win *codegen.WindowCtx, actionIdx map[*ir.EventHandler]int) (*codegen.RouteRender, error) {
	rb := &renderBuilder{
		pkg:       pkg,
		state:     stateVarNames(pkg, win),
		actionIdx: actionIdx,
	}
	for _, s := range win.Body {
		rb.walkStmt(s)
	}
	if rb.err != nil {
		return nil, rb.err
	}
	return rb.finish(), nil
}

func (rb *renderBuilder) walkStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		rb.walkNode(n)
	case *ir.CallStmt:
		if syn := nodeFromIRCallStmt(n); syn != nil {
			rb.walkNode(syn)
		}
	case *ir.SlotInst:
		for _, c := range n.Children {
			rb.walkStmt(c)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			rb.walkStmt(c)
		}
	case *ir.If:
		// A condition the optimizer could settle is already gone. One that is
		// left depends on the request, so both arms are skeletons the server
		// chooses between -- rendering them one after the other emitted both.
		if rb.exprIsReactive(n.Cond) {
			h := codegen.RouteHole{Kind: codegen.HoleIf, Expr: n.Cond, Then: rb.sub(n.Body)}
			if len(n.Else) > 0 {
				h.Else = rb.sub(n.Else)
			}
			rb.pushHole(h)
			return
		}
		for _, c := range n.Body {
			rb.walkStmt(c)
		}
		for _, c := range n.Else {
			rb.walkStmt(c)
		}
	case *ir.For:
		// Same: a loop still here did not unroll, so its length is the
		// request's. Walking the body once left the loop variable bound to
		// nothing, and every expression over it read as unrenderable -- which
		// is how a whole route was refused for markup the server can write.
		//
		// The two-variable form has no HoleFor spelling (the emitter ranges a
		// single name), so it keeps the old walk.
		if n.Value == "" && n.Key != "" {
			rb.pushHole(codegen.RouteHole{
				Kind: codegen.HoleFor,
				Expr: n.Iter,
				Key:  n.Key,
				Then: rb.sub(n.Body, n.Key),
			})
			return
		}
		for _, c := range n.Body {
			rb.walkStmt(c)
		}
	case *ir.ContextProvider:
		for _, c := range n.Children {
			rb.walkStmt(c)
		}
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle,
		*ir.Break, *ir.Continue:
		// No visual output.
	}
}

// walkNode emits one element. Element names are written verbatim as tags;
// props that reference state become holes (text-content props → HoleText,
// otherwise HoleAttr). A node carrying a backend event handler is wrapped in a
// server-action <form>.
func (rb *renderBuilder) walkNode(n *ir.NodeInst) {
	// Windows and zero-visual nodes are skipped (handled at route level).
	switch n.Name {
	case "window", "timer":
		return
	}

	actionIdx, backendForm := rb.nodeActionIndex(n)
	if backendForm {
		// No action attribute: a form without one posts to the URL the page was
		// served from, which is the only spelling of a parameterised route that
		// is correct per request -- the pattern the mux registered says
		// "/p/{pkg}", and that is what a browser would have posted to.
		rb.writeRaw(fmt.Sprintf(
			`<form method="post"><input type="hidden" name="_action" value="%d">`,
			actionIdx))
	}

	// A node the declaration cannot name a tag for is rendered as a container
	// rather than as a bogus <name> literal, so a user component keeps
	// rendering instead of emitting invalid markup.
	tag := "div"
	if t, ok := rawElementTag(n); ok {
		tag = t
	}
	rb.writeRaw("<" + tag)

	// Attribute-style props (non-content) referencing state → HoleAttr.
	//
	// Which props are content comes from contentProp, the same question the
	// client render answers: `innerHTML` used to be written as an attribute of
	// its own name holding raw markup, and `value` as a text node on whatever
	// element carried it -- so an <input> got a child and never got its value.
	var textBinding *ir.Arg
	var rawBinding *ir.Arg
	wroteStyle := false
	for _, p := range rb.elementAttrs(n) {
		switch contentProp(p.Name) {
		case textContentKind:
			textBinding = p
			continue
		case rawContentKind:
			rawBinding = p
			continue
		}
		if p.Name == "style" || p.Name == spanStyleProp {
			// Both are sets of CSS declarations rather than attribute values;
			// written through as values neither is a string at all. They are
			// also one attribute, so whichever comes first in the props writes
			// the pair and the other is skipped.
			if !wroteStyle {
				wroteStyle = true
				if css := nodeInlineCSS(n); css != "" {
					rb.writeRaw(` style="` + css + `"`)
				}
			}
			continue
		}
		val := p.Value
		if rb.exprIsReactive(val) {
			rb.writeRaw(" " + p.Name + `="`)
			rb.pushHole(codegen.RouteHole{Kind: codegen.HoleAttr, Expr: val, Attr: p.Name})
			rb.writeRaw(`"`)
			continue
		}
		// A boolean attribute is present or absent; `open="false"` leaves a
		// <details> open, so a false one is written as nothing.
		if bv, ok := codegen.IRLiteralBool(val); ok {
			if bv {
				rb.writeRaw(" " + p.Name)
			}
			continue
		}
		if s, ok := codegen.IRLiteralString(val); ok {
			rb.writeRaw(" " + p.Name + `="` + s + `"`)
			continue
		}
		// Not reactive and not a literal: there is nothing to write and no hole
		// to write it into. Dropping it silently is how a bound `value` left an
		// <input> with no value at all.
		rb.fail("prop %q cannot be rendered server-side: its value is neither a literal nor a state-dependent expression", p.Name)
		return
	}
	rb.writeRaw(">")

	// A void element holds no content and takes no close tag: the parser
	// closes it, and `</input>` is invalid markup.
	if voidElements[tag] {
		if backendForm {
			rb.writeRaw("</form>")
		}
		return
	}

	// Text-content binding (e.g. text(value=...)) → HoleText or literal.
	if textBinding != nil {
		if rb.exprIsReactive(textBinding.Value) {
			rb.pushHole(codegen.RouteHole{Kind: codegen.HoleText, Expr: textBinding.Value})
		} else if s, ok := codegen.IRLiteralString(textBinding.Value); ok {
			rb.writeRaw(s)
		}
	}
	// innerHTML is markup, so a literal is written through unescaped -- which
	// is what the prop means, and why it is not an attribute. A reactive one
	// would need a hole that interpolates without escaping, and the route
	// skeleton has no such kind: escaping it would render the markup as text,
	// and not escaping an interpolated value is an injection. Say so.
	if rawBinding != nil {
		if rb.exprIsReactive(rawBinding.Value) {
			rb.fail("innerHTML cannot depend on state in a server-rendered route: the value would be interpolated into markup unescaped")
		} else if s, ok := codegen.IRLiteralString(rawBinding.Value); ok {
			rb.writeRaw(s)
		}
	}

	for _, c := range n.Children {
		rb.walkStmt(c)
	}

	rb.writeRaw("</" + tag + ">")
	if backendForm {
		rb.writeRaw("</form>")
	}
}

// nodeActionIndex reports whether the node carries a backend event handler
// (and thus needs a server-action <form> wrapper) and, if so, the action index
// of its FIRST backend handler. The index is read from the shared actionIdx map
// minted by collectActions, so the form's hidden _action value is the exact
// POST switch case that runs that handler's mutations — never an independently
// counted value that could drift.
func (rb *renderBuilder) nodeActionIndex(n *ir.NodeInst) (int, bool) {
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if idx, ok := rb.actionIdx[h]; ok {
			return idx, true
		}
	}
	return 0, false
}

// exprIsReactive reports whether e reads any state var (and is therefore a
// dynamic binding that must become a hole rather than baked HTML).
func (rb *renderBuilder) exprIsReactive(e ir.Expr) bool {
	reactive := false
	ir.WalkExprs(e, func(x ir.Expr) error {
		if id, ok := x.(*ir.Ident); ok && !id.IsElementRef && rb.state[id.Name] {
			reactive = true
		}
		return nil
	})
	return reactive
}

// elementAttrs is the props to write as attributes, in a stable order: the
// ones the node was called with, minus the prop holding the tag (it names the
// element rather than describing it) and minus the wildcard container, whose
// entries are unpacked back into the attribute names they were written under
// and appended sorted.
func (rb *renderBuilder) elementAttrs(n *ir.NodeInst) []*ir.Arg {
	var out []*ir.Arg
	for i := range n.Props {
		p := &n.Props[i]
		if p.Name == "" || p.Name == tagProp || p.Name == attrsProp {
			continue
		}
		out = append(out, p)
	}
	extra := codegen.WildcardProps(n)
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		out = append(out, &ir.Arg{Name: name, Value: extra[name]})
	}
	return out
}

// logicalMutations returns the handler body with visual/DOM-patch statements
// removed, leaving only logical state mutations (and any other non-DOM
// statements). A DOM-patch is an ir.Assign whose target is a field Select on an
// element-ref ident (e.g. `__n0.value = ...`) — the lowered client-side patch
// that has no place in a server-side action body.
func logicalMutations(block []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(block))
	for _, s := range block {
		if isDOMPatchStmt(s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// isDOMPatchStmt reports whether s is a lowered DOM-patch assignment
// (assignment to a field on an element-ref ident).
func isDOMPatchStmt(s ir.Stmt) bool {
	a, ok := s.(*ir.Assign)
	if !ok {
		return false
	}
	sel, ok := a.Target.(*ir.Select)
	if !ok {
		return false
	}
	id, ok := sel.Operand.(*ir.Ident)
	return ok && id.IsElementRef
}

// stateVarNames returns the set of (non-synthesized) state var names.
// stateVarNames is the set of names a route's markup may depend on: the
// server State struct's fields, plus the window's own vars.
//
// A window's vars are what its URL template declares -- `/p/{pkg}` puts `pkg`
// in scope for the body (checker.go, buildWindow). Those are known per request
// exactly as state is, so an expression over one renders into a hole. Left
// out, `class=active ? "active" : ""` where `active` came from the path was
// neither a literal nor state-dependent, and the route was refused.
func stateVarNames(pkg *ir.Package, win *codegen.WindowCtx) map[string]bool {
	out := map[string]bool{}
	for _, v := range routeStateVars(pkg, win) {
		out[v.Name] = true
	}
	if win != nil {
		for _, v := range win.Vars {
			if v != nil && !v.IsConst {
				out[v.Name] = true
			}
		}
	}
	return out
}

// routeStateVars returns the state fields surfaced to a route's server State
// struct: the package-level vars, the main component's, and the ones the
// route's own window owns. Mirrors htmlGen.stateVars but yields the
// language-agnostic codegen.StateVar (name + IR type).
//
// The window is the usual owner rather than the unusual one: passRootWindow
// hoists a root component's declarations there (#215). stateVarNames below has
// always counted a window's vars, so leaving them out here rendered the markup
// as a hole into a State struct that had no such field.
//
// win may be nil, which is every caller that asks the package-wide question.
func routeStateVars(pkg *ir.Package, win *codegen.WindowCtx) []codegen.StateVar {
	var out []codegen.StateVar
	seen := map[string]bool{}
	add := func(vars []*ir.Var) {
		for _, v := range vars {
			// RouteParam: a window's `{x}` var is bound from the URL the
			// request arrived on, so it is a handler local rather than a field
			// of the per-session State the window's other vars become.
			if v == nil || v.Synthesized || v.IsConst || v.RouteParam || seen[v.Name] {
				continue
			}
			seen[v.Name] = true
			out = append(out, codegen.StateVar{Name: v.Name, Type: v.Type})
		}
	}
	if pkg != nil {
		add(pkg.Vars)
		if main := mainIRComponent(pkg); main != nil {
			add(main.Vars)
		}
	}
	if win != nil {
		add(win.Vars)
	}
	return out
}
