package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ir"
)

//go:generate go tool stringer -type=NameKind -trimprefix Name

// NameKind classifies what an identifier resolves to.
type NameKind int

const (
	NameUnknown    NameKind = iota
	NameLocal               // for-loop var, lambda param, renamed var
	NameStateVar            // mutable package/component variable
	NameComputed            // zero-param function treated as derived state
	NameConst               // compile-time constant
	NameFunc                // user-defined function
	NameExternFunc          // function from native import
	NameExternVar           // variable from native import
	NameComponent           // component declaration
	NameNamespace           // import namespace
)

// ExprCtx provides typed context for expression translation.
// It wraps the checker's Package and replaces ExprScope.
type ExprCtx struct {
	Pkg       *ir.Package
	Component *ir.Component     // current component (nil for top-level)
	Locals    map[string]bool   // for-loop vars, lambda params
	Renames   map[string]string // original → unique name (component inlining)
	EventVar  string            // what "event" maps to (e.g., "e.target")
	Helpers   map[string]bool   // helper functions needed (populated during codegen)
}

// NewExprCtx creates an ExprCtx for a package.
func NewExprCtx(pkg *ir.Package) *ExprCtx {
	return &ExprCtx{
		Pkg:     pkg,
		Locals:  make(map[string]bool),
		Renames: make(map[string]string),
		Helpers: make(map[string]bool),
	}
}

// ForComponent returns a new ExprCtx scoped to a component.
func (ctx *ExprCtx) ForComponent(comp *ir.Component) *ExprCtx {
	c := ctx.Clone()
	c.Component = comp
	return c
}

// Resolve determines what a name refers to in the current scope.
func (ctx *ExprCtx) Resolve(name string) (ir.Symbol, NameKind) {
	// Locals and renames first (innermost scope).
	if ctx.Locals[name] {
		return nil, NameLocal
	}
	if _, ok := ctx.Renames[name]; ok {
		return nil, NameLocal
	}

	// Component-scoped declarations.
	if ctx.Component != nil {
		for _, v := range ctx.Component.Vars {
			if v.Name == name {
				if v.IsConst {
					return v, NameConst
				}
				return v, NameStateVar
			}
		}
		for _, f := range ctx.Component.Funcs {
			if f.Name == name {
				if IsComputed(f) {
					return f, NameComputed
				}
				return f, NameFunc
			}
		}
	}

	// Package-level vars.
	for _, v := range ctx.Pkg.Vars {
		if v.Name == name {
			return v, NameStateVar
		}
	}

	// Package-level consts.
	for _, c := range ctx.Pkg.Consts {
		if c.Name == name {
			return c, NameConst
		}
	}

	// Package-level funcs.
	for _, f := range ctx.Pkg.Funcs {
		if f.Name == name {
			if IsComputed(f) {
				return f, NameComputed
			}
			return f, NameFunc
		}
	}

	// Components.
	for _, c := range ctx.Pkg.Components {
		if c.Name == name {
			return c, NameComponent
		}
	}

	// Import namespaces and native declarations.
	for _, imp := range ctx.Pkg.Imports {
		if imp.Alias == name {
			return imp, NameNamespace
		}
		if imp.Native != nil {
			for _, f := range imp.Native.Funcs {
				if f.Name == name {
					return f, NameExternFunc
				}
			}
			for _, v := range imp.Native.Vars {
				if v.Name == name {
					return v, NameExternVar
				}
			}
		}
	}

	return nil, NameUnknown
}

// RenamedName returns the renamed name for a local var, or the original if no rename.
func (ctx *ExprCtx) RenamedName(name string) string {
	if r, ok := ctx.Renames[name]; ok {
		return r
	}
	return name
}

// Clone creates a copy with independent Locals and Renames maps.
func (ctx *ExprCtx) Clone() *ExprCtx {
	return &ExprCtx{
		Pkg:       ctx.Pkg,
		Component: ctx.Component,
		Locals:    maps.Clone(ctx.Locals),
		Renames:   maps.Clone(ctx.Renames),
		EventVar:  ctx.EventVar,
		Helpers:   ctx.Helpers, // shared — helpers accumulate globally
	}
}

// WithLocal returns a clone with an additional local variable.
func (ctx *ExprCtx) WithLocal(name string) *ExprCtx {
	c := ctx.Clone()
	c.Locals[name] = true
	return c
}

// WithEvent returns a clone with EventVar set.
func (ctx *ExprCtx) WithEvent(eventVar string) *ExprCtx {
	c := ctx.Clone()
	c.EventVar = eventVar
	return c
}

// IsComputed reports whether a function is a computed field
// (zero-param, expression body, non-test).
func IsComputed(f *ir.Func) bool {
	return f.AST != nil && f.AST.Body != nil && len(f.Params) == 0 && !f.IsTest
}
