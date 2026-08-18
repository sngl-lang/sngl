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

// Windows returns a WindowCtx for each window in the package.
// Collects from pkg.Windows (root-level) and from Window statements
// in the main component body (including those expanded from for-loops).
// If no windows are found, synthesizes one from the main component.
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

	// Window statements in main component body.
	main := ctx.MainComponent()
	if main != nil {
		for _, w := range collectWindows(main.Body) {
			out = append(out, &WindowCtx{
				Window: w,
				Vars:   w.Vars,
				Funcs:  w.Funcs,
				Body:   w.Body,
				Name:   w.Name,
			})
		}
	}

	if len(out) > 0 {
		return out
	}

	// No explicit windows — synthesize from main component.
	if main == nil {
		return nil
	}
	vars := append(append([]*ir.Var{}, ctx.Pkg.Vars...), main.Vars...)
	funcs := append(append([]*ir.Func{}, ctx.Pkg.Funcs...), main.Funcs...)
	return []*WindowCtx{{
		Body:  main.Body,
		Vars:  vars,
		Funcs: funcs,
		Name:  "main",
	}}
}

// collectWindows walks a statement tree and returns all Window nodes found
// at the top level or inside expanded for-loops, if-blocks, and platform filters.
func collectWindows(stmts []ir.Stmt) []*ir.Window {
	var windows []*ir.Window
	ir.InspectStmts(stmts, ir.Inspector{Stmt: func(s ir.Stmt) error {
		if w, ok := s.(*ir.Window); ok {
			windows = append(windows, w)
			// A window is a leaf here: its own body is a separate root, and
			// windows do not nest inside one another.
			return ir.SkipDir
		}
		return nil
	}})
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

// MainComponent returns the component that codegen should treat as the
// app's root. By default that's the component literally named "main";
// when RootComponent is set (e.g. by the test launcher to isolate a
// component-under-test), it's the component with that name. If the
// override target is missing, falls back to "main".
func (ctx *CodegenCtx) MainComponent() *ir.Component {
	if ctx.RootComponent != "" {
		for _, c := range ctx.Pkg.Components {
			if c.Name == ctx.RootComponent {
				return c
			}
		}
	}
	for _, c := range ctx.Pkg.Components {
		if c.Name == "main" {
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
			seen[f] = true
			out = append(out, f)
		}
	}
	add(pkgFuncs)
	if main := ctx.MainComponent(); main != nil {
		add(main.Funcs)
	}
	return out
}

// NonMainComponents returns all components except the one MainComponent
// designates as the app root. Under test/agent mode RootComponent overrides
// the default "main" lookup, so this filter follows the same selection to
// avoid emitting the root component a second time as a sub-component.
func (ctx *CodegenCtx) NonMainComponents() []*ComponentCtx {
	main := ctx.MainComponent()
	var out []*ComponentCtx
	for _, cc := range ctx.Components() {
		if cc.Component != main {
			out = append(out, cc)
		}
	}
	return out
}

// --- Visual node walking ---

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
		case *ir.PlatformFilter:
			walkVisual(n.Body, fn, depth)
		case *ir.SlotInst:
			walkVisual(n.Children, fn, depth)
		case *ir.ErrorBoundary:
			walkVisual(n.Children, fn, depth)
		case *ir.ContextProvider:
			walkVisual(n.Children, fn, depth)
		case *ir.Window:
			walkVisual(n.Body, fn, depth)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
			// Not a visual statement; nothing to visit.
		default:
			panic(fmt.Sprintf("walkVisual: unhandled stmt %T", n))
		}
	}
}

// --- NodeInst accessors ---

// NodeProp returns the value of a named prop on a NodeInst, or nil.
func NodeProp(n *ir.NodeInst, name string) ir.Expr {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return nil
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
	style := NodeProp(n, "style")
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

// --- IR literal extraction ---

// IRLiteralString extracts a string value from an IR Literal expression.
// Returns the raw string content (without quotes) and true, or ("", false).
func IRLiteralString(e ir.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeString {
		return "", false
	}
	// Raw includes quotes for string literals; strip them.
	s := lit.Raw
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s, _ = strconv.Unquote(s)
	}
	return s, true
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
	return lit.Raw == "true", true
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
			return lit.Raw
		case ir.TypeBool:
			return lit.Raw == "true"
		case ir.TypeInt:
			n, _ := strconv.Atoi(lit.Raw)
			return n
		case ir.TypeFloat:
			f, _ := strconv.ParseFloat(lit.Raw, 64)
			return f
		case ir.TypeNull:
			return nil
		}
	}
	return lit.Raw
}
