package codegen

import (
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
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Window:
			windows = append(windows, n)
		case *ir.If:
			windows = append(windows, collectWindows(n.Body)...)
			windows = append(windows, collectWindows(n.Else)...)
		case *ir.For:
			windows = append(windows, collectWindows(n.Body)...)
		case *ir.PlatformFilter:
			windows = append(windows, collectWindows(n.Body)...)
		}
	}
	return windows
}

// Components returns a ComponentCtx for each component in the package.
func (ctx *CodegenCtx) Components() []*ComponentCtx {
	out := make([]*ComponentCtx, 0, len(ctx.Pkg.Components))
	for _, c := range ctx.Pkg.Components {
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

// MainComponent returns the "main" component, or nil.
func (ctx *CodegenCtx) MainComponent() *ir.Component {
	for _, c := range ctx.Pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// NonMainComponents returns all components except "main".
func (ctx *CodegenCtx) NonMainComponents() []*ComponentCtx {
	var out []*ComponentCtx
	for _, cc := range ctx.Components() {
		if cc.Component.Name != "main" {
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
		}
	}
}

// CollectNodes returns all NodeInst nodes matching a predicate.
func CollectNodes(stmts []ir.Stmt, match func(*ir.NodeInst) bool) []*ir.NodeInst {
	var out []*ir.NodeInst
	WalkVisualTree(stmts, func(n *ir.NodeInst, _ int) bool {
		if match(n) {
			out = append(out, n)
		}
		return false
	})
	return out
}

// CollectNodesByName returns all NodeInst nodes with the given element name.
func CollectNodesByName(stmts []ir.Stmt, name string) []*ir.NodeInst {
	return CollectNodes(stmts, func(n *ir.NodeInst) bool {
		return n.Name == name
	})
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

// NodeHasHandlers reports whether the node has any event handlers.
func NodeHasHandlers(n *ir.NodeInst) bool {
	return len(n.Handlers) > 0
}

// NodeStyleFields extracts style sub-properties from a NodeInst's "style" prop.
// Returns nil if no style prop or it's not a struct literal.
func NodeStyleFields(n *ir.NodeInst) map[string]ir.Expr {
	style := NodeProp(n, "style")
	if style == nil {
		return nil
	}
	sl, ok := style.(*ir.StructLit)
	if !ok {
		return nil
	}
	fields := make(map[string]ir.Expr, len(sl.Fields))
	for _, f := range sl.Fields {
		fields[f.Name] = f.Value
	}
	return fields
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

// IRLiteralInt extracts an int value from an IR Literal expression.
func IRLiteralInt(e ir.Expr) (int, bool) {
	if e == nil {
		return 0, false
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeInt {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Raw)
	if err != nil {
		return 0, false
	}
	return n, true
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
// that expect static data (accordion items, table columns) keep working.
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
