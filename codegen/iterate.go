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
	Timers    []*ir.Timer
	Body      []ir.Stmt
	Computeds []*ir.Func         // zero-param expression-body funcs
	Handlers  []*ir.EventHandler // var-level event handlers
}

// Windows returns a WindowCtx for each window in the package: those at the
// root of a file, and those a component body renders (including the ones a
// for-loop expanded).
//
// A program declares at least one, which the checker holds it to. The
// synthesis below is for the one caller that deliberately removes them: a
// test harness isolating a component, which clears the body and the windows
// so the component under test is the whole program.
func (ctx *CodegenCtx) Windows() []*WindowCtx {
	var out []*WindowCtx

	// Root-level windows (declared outside any component).
	for _, w := range ctx.Pkg.Windows {
		out = append(out, &WindowCtx{
			Window: w,
			Vars:   w.Vars,
			Funcs:  w.Funcs,
			Body:   w.Body,
			Name:   w.Name,
		})
	}

	// Window statements in a component body.
	root := ctx.RootDecl()
	for _, comp := range ctx.Pkg.Components {
		for _, w := range collectWindows(comp.Body) {
			out = append(out, &WindowCtx{
				Window: w,
				Vars:   w.Vars,
				Funcs:  w.Funcs,
				Body:   w.Body,
				Name:   w.Name,
			})
		}
	}

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

// collectWindows walks a statement tree and returns all Window nodes found
// at the top level or inside expanded for-loops, if-blocks, and platform filters.
func collectWindows(stmts []ir.Stmt) []*ir.Window {
	var windows []*ir.Window
	ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if w, ok := s.(*ir.Window); ok {
			windows = append(windows, w)
			// A window is a leaf here: its own body is a separate root, and
			// windows do not nest inside one another.
			return ir.SkipDir
		}
		return nil
	})
	return windows
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
			Timers:    c.Timers,
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
	// A window owns funcs the way a component does, and they are all
	// synthesized: passCanvas puts a canvas draw func for a canvas in a window
	// body here, and passFocusOrder the window's __focusNext/__focusPrev. A
	// caller that missed them emitted calls to methods it never declared.
	//
	// Windows() rather than Pkg.Windows: a `window` written inside a component
	// is an ir.Window statement in that component's body and never reaches
	// Pkg.Windows, so a canvas under one had its draw func emitted nowhere.
	for _, w := range ctx.Windows() {
		add(w.Funcs)
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
		case *ir.Window:
			walkVisual(n.Body, fn, depth)
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
