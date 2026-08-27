package html

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
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
// walk. Phase 3 only builds the model; the golang consumer still renders from
// the legacy baked RenderHTML string, so the two paths coexist until Phase 4.

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
	// rawElem is the `element` declaration every HTML tag resolves to. The
	// server render reads the same declaration the client render does, so the
	// two agree on the tag, on which props are boolean, and on which prop
	// holds the tag rather than describing the element.
	rawElem *ir.Component
}

func (rb *renderBuilder) writeRaw(s string) { rb.cur.WriteString(s) }

func (rb *renderBuilder) pushHole(h codegen.RouteHole) {
	rb.chunks = append(rb.chunks, rb.cur.String())
	rb.cur.Reset()
	rb.holes = append(rb.holes, h)
}

func (rb *renderBuilder) finish() *codegen.RouteRender {
	rb.chunks = append(rb.chunks, rb.cur.String())
	rb.cur.Reset()
	return &codegen.RouteRender{Chunks: rb.chunks, Holes: rb.holes}
}

// buildRenderModel walks a route window's visual tree into a RouteRender:
// static HTML in Chunks, reactive bindings as Holes. Backend handlers (per
// handlerPlacement) wrap their triggering element in a server-action <form>.
// path is the route URL the form posts to.
func buildRenderModel(pkg *ir.Package, win *codegen.WindowCtx, path string, actionIdx map[*ir.EventHandler]int) *codegen.RouteRender {
	rb := &renderBuilder{
		pkg:       pkg,
		state:     stateVarNames(pkg),
		actionIdx: actionIdx,
		rawElem:   rawElementDecl(pkg),
	}
	for _, s := range win.Body {
		rb.walkStmt(s, path)
	}
	return rb.finish()
}

func (rb *renderBuilder) walkStmt(s ir.Stmt, path string) {
	switch n := s.(type) {
	case *ir.NodeInst:
		rb.walkNode(n, path)
	case *ir.CallStmt:
		if syn := nodeFromIRCallStmt(n); syn != nil {
			rb.walkNode(syn, path)
		}
	case *ir.PlatformFilter:
		if n.Platform == "html" {
			for _, c := range n.Body {
				rb.walkStmt(c, path)
			}
		}
	case *ir.SlotInst:
		for _, c := range n.Children {
			rb.walkStmt(c, path)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			rb.walkStmt(c, path)
		}
	case *ir.If:
		for _, c := range n.Body {
			rb.walkStmt(c, path)
		}
		for _, c := range n.Else {
			rb.walkStmt(c, path)
		}
	case *ir.For:
		for _, c := range n.Body {
			rb.walkStmt(c, path)
		}
	case *ir.ContextProvider:
		for _, c := range n.Children {
			rb.walkStmt(c, path)
		}
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
		// No visual output.
	}
}

// walkNode emits one element. Element names are written verbatim as tags;
// props that reference state become holes (text-content props → HoleText,
// otherwise HoleAttr). A node carrying a backend event handler is wrapped in a
// server-action <form>.
func (rb *renderBuilder) walkNode(n *ir.NodeInst, path string) {
	// Windows and zero-visual nodes are skipped (handled at route level).
	switch n.Name {
	case "window", "timer", "slot":
		return
	}

	actionIdx, backendForm := rb.nodeActionIndex(n)
	if backendForm {
		rb.writeRaw(fmt.Sprintf(
			`<form method="post" action="%s"><input type="hidden" name="_action" value="%d">`,
			path, actionIdx))
	}

	decl := n.Component
	if decl == nil || decl.Wildcard == "" {
		decl = rb.rawElem
	}
	// A node the declaration cannot name a tag for is rendered as a container
	// rather than as a bogus <name> literal, so a user component keeps
	// rendering instead of emitting invalid markup.
	tag := "div"
	if t, ok := rawElementTag(decl, n); ok {
		tag = t
	}
	rb.writeRaw("<" + tag)

	// Attribute-style props (non-text-content) referencing state → HoleAttr.
	var textBinding *ir.Arg
	for _, p := range rb.elementAttrs(decl, n) {
		if isTextContentProp(p.Name) {
			textBinding = p
			continue
		}
		if p.Name == "style" {
			// A style struct is a set of CSS declarations; written through as
			// a value it is not a string at all.
			if css := htmlutil.BuildCSSStyleIR([]ir.Arg{*p}); css != "" {
				rb.writeRaw(` style="` + css + `"`)
			}
			continue
		}
		if rb.exprIsReactive(p.Value) {
			rb.writeRaw(" " + p.Name + `="`)
			rb.pushHole(codegen.RouteHole{Kind: codegen.HoleAttr, Expr: p.Value, Attr: p.Name})
			rb.writeRaw(`"`)
			continue
		}
		// A boolean attribute is present or absent; `open="false"` leaves a
		// <details> open, so a false one is written as nothing.
		if bv, ok := codegen.IRLiteralBool(p.Value); ok {
			if bv {
				rb.writeRaw(" " + p.Name)
			}
			continue
		}
		if s, ok := codegen.IRLiteralString(p.Value); ok {
			rb.writeRaw(" " + p.Name + `="` + s + `"`)
		}
	}
	rb.writeRaw(">")

	// Text-content binding (e.g. text(value=...)) → HoleText or literal.
	if textBinding != nil {
		if rb.exprIsReactive(textBinding.Value) {
			rb.pushHole(codegen.RouteHole{Kind: codegen.HoleText, Expr: textBinding.Value})
		} else if s, ok := codegen.IRLiteralString(textBinding.Value); ok {
			rb.writeRaw(s)
		}
	}

	for _, c := range n.Children {
		rb.walkStmt(c, path)
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
func (rb *renderBuilder) elementAttrs(decl *ir.Component, n *ir.NodeInst) []*ir.Arg {
	into := tagPropName(decl)
	wildcard := map[string]bool{}
	if decl != nil {
		for _, dp := range decl.Props {
			if dp != nil && dp.Wildcard != "" {
				wildcard[dp.Name] = true
			}
		}
	}
	var out []*ir.Arg
	for i := range n.Props {
		p := &n.Props[i]
		if p.Name == "" || p.Name == into || wildcard[p.Name] {
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

// isTextContentProp reports whether prop is rendered as an element's text
// content rather than as an attribute. The DOM-side content props are here
// too: no attribute spells them, so writing one as an attribute both invents
// markup and loses the text.
func isTextContentProp(prop string) bool {
	switch prop {
	case "value", "text", "label", "content", "textContent", "innerText":
		return true
	}
	return false
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
func stateVarNames(pkg *ir.Package) map[string]bool {
	out := map[string]bool{}
	for _, v := range routeStateVars(pkg) {
		out[v.Name] = true
	}
	return out
}

// routeStateVars returns the component state fields surfaced to the server
// State struct: the package-level and main-component non-synthesized,
// non-const vars. Mirrors htmlGen.stateVars but yields the language-agnostic
// codegen.StateVar (name + IR type).
func routeStateVars(pkg *ir.Package) []codegen.StateVar {
	var out []codegen.StateVar
	seen := map[string]bool{}
	add := func(vars []*ir.Var) {
		for _, v := range vars {
			if v == nil || v.Synthesized || v.IsConst || seen[v.Name] {
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
	return out
}
