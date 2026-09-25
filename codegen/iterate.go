package codegen

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// WindowCtx provides all the data a platform needs to generate one window.
type WindowCtx struct {
	Window *ir.Window
	Vars   []*ir.Var
	Funcs  []*ir.Func
	Body   []ir.Stmt
	Name   string
}

// ComponentCtx provides all the data a platform needs to generate one component.
type ComponentCtx struct {
	Component *ir.Component
	Props     []*ir.Prop
	Events    []*ir.EventDecl
	Vars      []*ir.Var
	Funcs     []*ir.Func
	Body      []ir.Stmt
	Computeds []*ir.Func         // zero-param expression-body funcs
	Handlers  []*ir.EventHandler // var-level event handlers
}

// Windows returns a WindowCtx for each window ir.Owners reports: those at the
// root of a file, and those a body renders (including the ones a for-loop
// expanded).
//
// ir.Owners reports a window nested inside another as an owner of its own, and
// this does not filter it out -- passWindowNesting has failed the build long
// before any generator asks, so the pair cannot reach here. That is a
// dependency on a lowering pass having run, which the deleted local walk did
// not have.
//
// A program declares at least one, which the checker holds it to. The
// synthesis below is for the one caller that deliberately removes them: a
// test harness isolating a component, which clears the body and the windows
// so the component under test is the whole program.
func (ctx *CodegenCtx) Windows() []*WindowCtx {
	var out []*WindowCtx

	// ir.Owners is what says which declarations own state, and a window is one
	// of the three -- so which bodies a window may be written in is its answer
	// rather than a second walk here.
	for _, o := range ir.Owners(ctx.Pkg) {
		if o.Win == nil {
			continue
		}
		out = append(out, &WindowCtx{
			Window: o.Win,
			Vars:   o.Vars,
			Funcs:  o.Funcs,
			Body:   o.Stmts(),
			Name:   o.Name(),
		})
	}

	root := ctx.RootDecl()
	if len(out) > 0 || root == nil {
		return out
	}

	vars := append(append([]*ir.Var{}, ctx.Pkg.Vars...), root.Vars...)
	funcs := append(append([]*ir.Func{}, ctx.Pkg.Funcs...), root.Funcs...)
	return []*WindowCtx{{
		Body:  root.Body,
		Vars:  vars,
		Funcs: funcs,
		Name:  root.Name,
	}}
}

// Components returns a ComponentCtx for each component in the package.
func (ctx *CodegenCtx) Components() []*ComponentCtx {
	return componentsFor(ctx.Pkg.Components)
}

func componentsFor(comps []*ir.Component) []*ComponentCtx {
	out := make([]*ComponentCtx, 0, len(comps))
	for _, c := range comps {
		cc := &ComponentCtx{
			Component: c,
			Props:     c.Props,
			Events:    c.Events,
			Vars:      c.Vars,
			Funcs:     c.Funcs,
			Body:      c.Body,
		}
		for _, f := range c.Funcs {
			if IsComputed(f) {
				cc.Computeds = append(cc.Computeds, f)
			}
		}
		for _, v := range c.Vars {
			cc.Handlers = append(cc.Handlers, v.Handlers...)
		}
		out = append(out, cc)
	}
	return out
}

// RootDecl returns the component a harness has made the program's root, or
// nil -- which is every ordinary build.
//
// A component named "main" used to be that root by convention, which is what
// let a fixture render without declaring a window. It only ever worked
// because every such program is in this repository; a window is what a
// program renders now, and `main` is an ordinary component.
func (ctx *CodegenCtx) RootDecl() *ir.Component {
	if ctx.RootComponent == "" {
		return nil
	}
	for _, c := range ctx.Pkg.Components {
		if c.Name == ctx.RootComponent {
			return c
		}
	}
	return nil
}

// AllFuncs returns every function codegen should emit: the package-level
// funcs plus the main component's funcs, deduped by pointer. Nested component
// methods are registered in BOTH pkg.Funcs and component.Funcs (the checker's
// registerNestedMethods appends the same *ir.Func to each), so a naive
// pkg.Funcs+main.Funcs concatenation double-emits them. All platforms must go
// through here rather than concatenating themselves.
func (ctx *CodegenCtx) AllFuncs() []*ir.Func {
	pkgFuncs := ctx.Pkg.Funcs
	out := make([]*ir.Func, 0, len(pkgFuncs))
	seen := make(map[*ir.Func]bool, len(pkgFuncs))
	add := func(fns []*ir.Func) {
		for _, f := range fns {
			if seen[f] {
				continue
			}
			// A host identifier is filtered here rather than at the emitter
			// because fyne and bubbletea rebuild a component func into a fresh
			// ir.Func to give it a Model receiver, and that copy carries no
			// Foreign for an emitter to test.
			if f.Foreign.Name != "" && !f.Foreign.Marked {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	add(pkgFuncs)
	if main := ctx.RootDecl(); main != nil {
		add(main.Funcs)
	}
	return out
}

// NonRootComponents returns every component except the one a harness made the
// root, which its target emits as the Model rather than a second time as a
// sub-component. With no harness there is no such component, and this is all
// of them.
func (ctx *CodegenCtx) NonRootComponents() []*ComponentCtx {
	main := ctx.RootDecl()
	var out []*ComponentCtx
	for _, cc := range ctx.Components() {
		if cc.Component != main {
			out = append(out, cc)
		}
	}
	return out
}

// WalkVisualTree walks IR statements, calling fn for each NodeInst.
// Return true from fn to skip automatic child traversal.
func WalkVisualTree(stmts []ir.Stmt, fn func(node *ir.NodeInst, depth int) bool) {
	walkVisual(stmts, fn, 0)
}

func walkVisual(stmts []ir.Stmt, fn func(*ir.NodeInst, int) bool, depth int) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			// A window is the surface its children are drawn on rather than a
			// node drawn on one, so it is walked through: the callers count
			// depth to indent markup and to decide what a node's parent is,
			// and neither wants a page in the middle of that.
			if ir.IsWindowNode(n) {
				walkVisual(n.Children, fn, depth)
				continue
			}
			if !fn(n, depth) {
				walkVisual(n.Children, fn, depth+1)
			}
		case *ir.If:
			walkVisual(n.Body, fn, depth)
			walkVisual(n.Else, fn, depth)
		case *ir.For:
			walkVisual(n.Body, fn, depth)
		case *ir.SlotInst:
			walkVisual(n.Children, fn, depth)
		case *ir.ErrorBoundary:
			walkVisual(n.Children, fn, depth)
		case *ir.ContextProvider:
			walkVisual(n.Children, fn, depth)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Not a visual statement; nothing to visit.
		default:
			panic(fmt.Sprintf("walkVisual: unhandled stmt %T", n))
		}
	}
}

// NodeProp returns the value of a named prop on a NodeInst, or nil.
func NodeProp(n *ir.NodeInst, name string) ir.Expr {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value
		}
	}
	// A name no prop declares may still have been written at the call site
	// and collected by a wildcard prop, which binds under its own name.
	return WildcardProps(n)[name]
}

// WildcardProps is the names a node's wildcard props collected, mapped to the
// values written for them. A wildcard prop binds once, under its own name, and
// holds the matched names as its keys — so this is where a backend that reads
// props by the name the author wrote finds them.
//
// Returns nil for a node with no wildcard prop, which is nearly all of them.
func WildcardProps(n *ir.NodeInst) map[string]ir.Expr {
	if n == nil || n.Component == nil {
		return nil
	}
	var out map[string]ir.Expr
	for _, dp := range n.Component.Props {
		if dp.Wildcard == "" {
			continue
		}
		for _, p := range n.Props {
			if p.Name != dp.Name {
				continue
			}
			m, ok := p.Value.(*ir.MapLitIR)
			if !ok {
				continue
			}
			for _, e := range m.Entries {
				k, ok := e.Key.(*ir.Literal)
				if !ok {
					continue
				}
				if out == nil {
					out = map[string]ir.Expr{}
				}
				out[k.Value] = e.Value
			}
		}
	}
	return out
}

// NodeHandler returns the event handler with the given name, or nil.
func NodeHandler(n *ir.NodeInst, name string) *ir.EventHandler {
	for i := range n.Handlers {
		if n.Handlers[i].Name == name {
			return &n.Handlers[i]
		}
	}
	return nil
}

// StyleField is one entry of a NodeInst's `style=` struct literal. Returned as
// an ordered slice (not a map) so platform style emitters iterate
// deterministically — a map range made android's TextStyle/Modifier emission
// non-reproducible across builds.
type StyleField struct {
	Name  string
	Value ir.Expr
}

// NodeStyleFields extracts style sub-properties from a NodeInst's "style" prop,
// preserving source order. Returns nil if there's no style prop or it isn't a
// struct literal.
func NodeStyleFields(n *ir.NodeInst) []StyleField {
	return NodeStyleFieldsOf(n, "style")
}

// NodeStyleFieldsOf is NodeStyleFields over a differently named prop, for a
// node that takes more than one Style — a declared native widget naming its
// props after the host's own arguments.
func NodeStyleFieldsOf(n *ir.NodeInst, prop string) []StyleField {
	style := NodeProp(n, prop)
	if style == nil {
		return nil
	}
	sl, ok := style.(*ir.StructLit)
	if !ok {
		return nil
	}
	out := make([]StyleField, 0, len(sl.Fields))
	for _, f := range sl.Fields {
		out = append(out, StyleField{Name: f.Name, Value: f.Value})
	}
	return out
}

// IRLiteralString extracts a string value from an IR Literal expression.
// Returns the value and true, or ("", false) for anything that is not a string
// literal.
//
// Raw is the decoded content, carrying no delimiters: the lexer builds it from
// a strings.Builder the quotes never reach, and a synthesized literal stores
// the value a backend will quote for its own target. So there is nothing here
// to unquote — a string whose content happens to begin and end with `"` is a
// string like any other.
func IRLiteralString(e ir.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeString {
		return "", false
	}
	return lit.Value, true
}

// IRLiteralBool extracts a bool value from an IR Literal expression.
func IRLiteralBool(e ir.Expr) (bool, bool) {
	if e == nil {
		return false, false
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeBool {
		return false, false
	}
	return lit.Value == "true", true
}

// IRLiteralNumber extracts the value of an int or float literal. Both kinds
// answer, because a numeric prop written `0` reaches a float field as an int
// literal unless something along the way converted it.
func IRLiteralNumber(e ir.Expr) (float64, bool) {
	if e == nil {
		return 0, false
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit.Type == nil {
		return 0, false
	}
	switch lit.Type.Kind {
	case ir.TypeInt, ir.TypeFloat:
	default:
		return 0, false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// IRIsLiteral reports whether an expression is a compile-time literal.
// List literals count when every element is itself a literal, so callers
// that expect static data (table columns, tabs items) keep working.
func IRIsLiteral(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.ListLit:
		for _, el := range x.Elems {
			if !IRIsLiteral(el) {
				return false
			}
		}
		return true
	}
	return false
}

// IRIsReactive reports whether an expression references runtime state.
func IRIsReactive(e ir.Expr) bool {
	return e != nil && !IRIsLiteral(e)
}

// IRLiteralAny extracts the literal value of an IR expression as any type.
// Returns nil for non-literals. List literals decode to []any recursively.
func IRLiteralAny(e ir.Expr) any {
	if list, ok := e.(*ir.ListLit); ok {
		out := make([]any, len(list.Elems))
		for i, el := range list.Elems {
			out[i] = IRLiteralAny(el)
		}
		return out
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit == nil {
		return nil
	}
	if lit.Type != nil {
		switch lit.Type.Kind {
		case ir.TypeString:
			if s, ok := IRLiteralString(e); ok {
				return s
			}
			return lit.Value
		case ir.TypeBool:
			return lit.Value == "true"
		case ir.TypeInt:
			n, _ := strconv.Atoi(lit.Value)
			return n
		case ir.TypeFloat:
			f, _ := strconv.ParseFloat(lit.Value, 64)
			return f
		case ir.TypeNull:
			return nil
		}
	}
	return lit.Value
}
