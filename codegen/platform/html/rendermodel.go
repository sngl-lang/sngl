package html

import (
	"fmt"
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
// walk. Phase 3 only builds the model; the golang consumer still renders from
// the legacy baked RenderHTML string, so the two paths coexist until Phase 4.

// renderBuilder accumulates a RouteRender: a list of static HTML chunks
// interleaved with holes. cur holds the chunk under construction; pushHole
// flushes cur as a finished chunk and appends a hole.
type renderBuilder struct {
	pkg     *ir.Package
	chunks  []string
	holes   []codegen.RouteHole
	cur     strings.Builder
	state   map[string]bool // names of state vars (reactive bindings read these)
	formIdx int             // next _action index for backend forms (single route v1)
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
func buildRenderModel(pkg *ir.Package, win *codegen.WindowCtx, path string) *codegen.RouteRender {
	rb := &renderBuilder{pkg: pkg, state: stateVarNames(pkg)}
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

	backendForm := rb.nodeNeedsForm(n)
	if backendForm {
		actionIdx := rb.formIdx
		rb.formIdx++
		rb.writeRaw(fmt.Sprintf(
			`<form method="post" action="%s"><input type="hidden" name="_action" value="%d">`,
			path, actionIdx))
	}

	tag := htmlTagFor(n.Name)
	rb.writeRaw("<" + tag)

	// Attribute-style props (non-text-content) referencing state → HoleAttr.
	var textBinding *ir.Arg
	for i := range n.Props {
		p := &n.Props[i]
		if isTextContentProp(n.Name, p.Name) {
			textBinding = p
			continue
		}
		if rb.exprIsReactive(p.Value) {
			rb.writeRaw(" " + p.Name + `="`)
			rb.pushHole(codegen.RouteHole{Kind: codegen.HoleAttr, Expr: p.Value, Attr: p.Name})
			rb.writeRaw(`"`)
		} else if s, ok := codegen.IRLiteralString(p.Value); ok {
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

// nodeNeedsForm reports whether any of the node's event handlers is placed
// on the backend (and thus needs a server-action <form> wrapper).
func (rb *renderBuilder) nodeNeedsForm(n *ir.NodeInst) bool {
	for i := range n.Handlers {
		if handlerPlacement(rb.pkg, n.Handlers[i].Func) == Backend {
			return true
		}
	}
	return false
}

// exprIsReactive reports whether e reads any state var (and is therefore a
// dynamic binding that must become a hole rather than baked HTML).
func (rb *renderBuilder) exprIsReactive(e ir.Expr) bool {
	reactive := false
	walkExpr(e, func(x ir.Expr) bool {
		if id, ok := x.(*ir.Ident); ok && !id.IsElementRef && rb.state[id.Name] {
			reactive = true
		}
		return false
	})
	return reactive
}

// htmlTagFor maps a SNGL element/component name to the HTML tag the route
// skeleton emits. Layout primitives become <div>; text becomes <span>; an
// already-HTML name passes through.
func htmlTagFor(name string) string {
	switch name {
	case "vbox", "hbox", "box", "stack", "grid":
		return "div"
	case "text", "label":
		return "span"
	case "button":
		return "button"
	}
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isTextContentProp reports whether prop is rendered as the element's text
// content (vs. an attribute) for the given element.
func isTextContentProp(elem, prop string) bool {
	switch prop {
	case "value", "text", "label", "content":
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
