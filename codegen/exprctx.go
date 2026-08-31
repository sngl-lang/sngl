package codegen

import (
	"maps"
	"strings"

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
// It wraps the checker's Package and is the sole translation context.
type ExprCtx struct {
	Pkg       *ir.Package
	Component *ir.Component // current component (nil for top-level)
	// Window is the window whose body is being translated, if any. A window
	// declares state the way a component does, and it nests: a `window`
	// written inside a component sees that component's declarations too, so
	// this is an inner scope beside Component rather than a replacement.
	Window   *ir.Window
	Locals   map[string]bool   // for-loop vars, lambda params
	Renames  map[string]string // original → unique name (component inlining)
	EventVar string            // what the handler's event parameter maps to (e.g., "e.target")
	// EventParam is the handler parameter EventVar stands for. `event` was an
	// ambient name once and is a declared parameter now, so the substitution
	// is by declaration: a handler may call its parameter whatever it likes,
	// and a local called "event" that is not one must not be swapped.
	EventParam ir.Symbol
	Helpers    map[string]bool // helper functions needed (populated during codegen)
	// NativeImports collects module → set of imported names; populated as
	// native calls are emitted.
	NativeImports map[string]map[string]bool
	// Maps mirrors Request.Maps: when true, translators emit source-map
	// hooks (e.g. Go `//line file:lineno` directives before statements).
	Maps bool
	// OutDir mirrors Request.OutDir, for translators emitting source maps.
	OutDir string
	// ContextVar is the expression to supply for native context args
	// (e.g., "r.Context()").
	ContextVar string
	// RawFieldAccess names identifiers whose Select-field accesses bypass
	// method/getter lowering — used by test runners that emit `_test.go`
	// into the same Go package as the generated Model so they can read and
	// write unexported fields directly.
	RawFieldAccess map[string]bool
	// MethodFields names identifiers whose bare `recv.<field>` Select access
	// should lower to a zero-arg method call `recv.<field>()` instead of a
	// raw field read.
	MethodFields map[string]bool
	// IdentRewrites remaps bare identifiers regardless of scope, applied
	// first in identifier translation.
	IdentRewrites map[string]string
	// FreeFuncs names the user functions a host emits as free package-level
	// functions rather than as methods on its receiver, so a call to one
	// renders under its exported name from any scope.
	//
	// A top-level function has no component in scope and so can read no
	// component state; there is nothing for a receiver to carry. Emitting one
	// as a method anyway is what left a type method — which is free, having no
	// receiver to be a method on — calling `m.format(…)` with no `m` in sight.
	FreeFuncs map[string]bool
	// StateReceiver, when non-empty, names a struct receiver onto which
	// component/package state vars are projected as EXPORTED fields. Set by
	// the html backend (route mode) so a state read `count` renders
	// `<recv>.Count` and an assignment `count = …` renders `<recv>.Count = …`,
	// instead of the default Model-receiver `m.count`. Used by renderRoute /
	// POST-action emission where the per-session State struct is the receiver.
	StateReceiver string
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
	c.Window = nil
	return c
}

// ForWindow returns a new ExprCtx with win as the innermost scope. Any
// component scope is kept: a `window` written inside a component reads that
// component's vars, and dropping them made every one of those reads render as
// a bare identifier.
func (ctx *ExprCtx) ForWindow(win *ir.Window) *ExprCtx {
	c := ctx.Clone()
	c.Window = win
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

	// Window-scoped declarations. A window's state is its own, the way a
	// component's is: passHoistState puts it in Window.Vars so that a read of
	// it resolves here rather than falling through to package scope and out
	// the bottom as an unknown name -- which is what made it render as a bare
	// identifier no target had declared.
	if ctx.Window != nil {
		for _, v := range ctx.Window.Vars {
			if v.Name == name {
				if v.IsConst {
					return v, NameConst
				}
				return v, NameStateVar
			}
		}
		for _, f := range ctx.Window.Funcs {
			if f.Name == name {
				if IsComputed(f) {
					return f, NameComputed
				}
				return f, NameFunc
			}
		}
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
		Pkg:           ctx.Pkg,
		Component:     ctx.Component,
		Window:        ctx.Window,
		Locals:        maps.Clone(ctx.Locals),
		Renames:       maps.Clone(ctx.Renames),
		EventVar:      ctx.EventVar,
		EventParam:    ctx.EventParam,
		Helpers:       ctx.Helpers,       // shared — helpers accumulate globally
		NativeImports: ctx.NativeImports, // shared — accumulates across clones
		Maps:          ctx.Maps,
		OutDir:        ctx.OutDir,
		ContextVar:    ctx.ContextVar,
		// Deep-copy the test/http-only maps (nil-safe via maps.Clone).
		RawFieldAccess: maps.Clone(ctx.RawFieldAccess),
		MethodFields:   maps.Clone(ctx.MethodFields),
		IdentRewrites:  maps.Clone(ctx.IdentRewrites),
		FreeFuncs:      ctx.FreeFuncs, // shared — one decision for the whole build
		StateReceiver:  ctx.StateReceiver,
	}
}

// WithLocal returns a clone with an additional local variable.
func (ctx *ExprCtx) WithLocal(name string) *ExprCtx {
	c := ctx.Clone()
	c.Locals[name] = true
	return c
}

// WithEvent returns a clone bound to a handler's event parameter.
func (ctx *ExprCtx) WithEvent(eventVar string, param ir.Symbol) *ExprCtx {
	c := ctx.Clone()
	c.EventVar = eventVar
	c.EventParam = param
	return c
}

// IsComputed reports whether a function is a computed field
// (zero-param, expression body, non-test). A leading synthetic `this`
// parameter (from a desugared nested method on a component/struct/enum)
// does not count toward the param count.
func IsComputed(f *ir.Func) bool {
	if f.AST == nil || f.IsTest {
		return false
	}
	// Expression-bodied zero-arg funcs are computeds. Block-bodied zero-arg
	// funcs are computeds too when they return a value (e.g.
	// `func total() int { ... }`); a void block func (e.g. `increment()`)
	// is an action handler, not a reactive property.
	if f.AST.Body == nil && f.Return == nil {
		return false
	}
	n := len(f.Params)
	if f.Receiver != "" {
		// A receiver-bearing func is a component computed only when it
		// carries the synthetic receiver param (added by registerNestedMethods
		// for bare `func name()` decls inside a component) typed as a
		// component. Explicit type methods like `func widget.select() bool`
		// have a receiver but no such param — they are NOT computeds.
		if n == 0 || !f.Params[0].Receiver {
			return false
		}
		if t := f.Params[0].Type; t != nil && t.Kind != ir.TypeComponent {
			return false
		}
		n--
	}
	return n == 0
}

// CollectTestFuncs walks pkg.Funcs / pkg.Components for test funcs
// (IsTest) and returns parallel slices of funcs and short suffixes
// (with the "test" prefix stripped) plus the methodFields set built
// from every component's funcs and computed package funcs. Shared
// across platforms whose codegen emits an agent-mode test file.
// TestComponentParam is the name a test binds its component instance to, or
// "" when the test declared none: `func testPress(t Test, c main)` says `c`,
// and `func testAddition(t Test)` says nothing at all.
//
// The name is the declaration's, not the emitter's. Binding a hardcoded `c`
// into every test collided with any local of that name -- silently, because
// the page it produced no longer parsed and all the runner could report was
// that the agent never connected.
func TestComponentParam(fn *ir.Func) string {
	if fn == nil {
		return ""
	}
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			return p.Name
		}
	}
	return ""
}

func CollectTestFuncs(pkg *ir.Package) (fns []*ir.Func, suffixes []string, methodFields map[string]bool) {
	methodFields = map[string]bool{}
	if pkg == nil {
		return
	}
	for _, comp := range pkg.Components {
		for _, f := range comp.Funcs {
			methodFields[f.Name] = true
		}
	}
	for _, f := range pkg.Funcs {
		if IsComputed(f) {
			methodFields[f.Name] = true
		}
		if f.IsTest {
			fns = append(fns, f)
			suffixes = append(suffixes, strings.TrimPrefix(f.Name, "test"))
		}
	}
	return
}
