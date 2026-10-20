package codegen

import (
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
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
	// Platform is the build's target platform, which intrinsic dispatch needs
	// beside the language: a platform may implement an id its language cannot,
	// and the 2D primitives are exactly that.
	Platform  string
	Pkg       *ir.Package
	Component *ir.Component // current component (nil for top-level)
	// Surface is the node whose document is being translated, if any: a
	// route's params cell is the one name it binds, which package scope has
	// nothing to find for. An inner scope beside Component rather than a
	// replacement.
	Surface *ir.NodeInst
	// App says the scope is the package body, whose parent is the
	// application: a target that holds it in one Model reads the package's
	// state and consts through it, as it did through the one window that used
	// to be the scope.
	App      bool
	locals   *localScope
	EventVar string // what the handler's event parameter maps to (e.g., "e.target")
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
	// ModelParamFuncs names the methods on a user type that are lifted to a
	// free function and touch package state: those take the Model as a
	// trailing parameter. golang.ModelParamFuncs builds it and says why.
	ModelParamFuncs map[*ir.Func]bool
	// StateReceiver, when non-empty, names a struct receiver onto which
	// component/package state vars are projected as EXPORTED fields. Set by
	// the html backend (route mode) so a state read `count` renders
	// `<recv>.Count` and an assignment `count = …` renders `<recv>.Count = …`,
	// instead of the default Model-receiver `m.count`. Used by renderRoute /
	// POST-action emission where the per-session State struct is the receiver.
	StateReceiver string
	// StateFieldsExported says that projection uses Go-exported names --
	// `<recv>.Count` for `count`, `<recv>.Inc()` for `inc`. html's route mode
	// declares its per-session State that way.
	//
	// A component instance record does not. Half the names on one were
	// synthesized by a lowering pass and begin with `__`, which ExportName
	// leaves alone, so exporting would spell some fields one way and the rest
	// another -- and the two the lowering dispatches to by name (Root,
	// Destroy) are neither. Verbatim is one spelling for the whole struct.
	StateFieldsExported bool
	// OuterReceiver is what a component instance record reaches the Model
	// through, set inside the record's ctor and methods. A name the component
	// does not declare -- package state, and the page's widgets and funcs -- is
	// the Model's, and spelling it through StateReceiver named a field no
	// record has. OuterNodes are the names of the page's widgets and synthesized
	// cells; any other a node reference names is the record's own, a field
	// derived from one of its nodes included.
	OuterReceiver string
	OuterNodes    map[string]bool
	// ClosureMethods says Component's own methods are closures of the
	// function translating it -- an html instance factory -- so a call to one
	// names it bare and hands it no receiver.
	ClosureMethods bool
	// memo is shared by every clone (exprMemo).
	memo *exprMemo
}

// NewExprCtx creates an ExprCtx for a package.
func NewExprCtx(pkg *ir.Package) *ExprCtx {
	return &ExprCtx{
		Pkg:     pkg,
		Helpers: make(map[string]bool),
		memo:    &exprMemo{},
	}
}

// exprMemo is what an ExprCtx and every clone of it compute once per build.
type exprMemo struct {
	instanceRoots map[string]bool
}

// InstanceRootIDs is InstanceRootIDs of the package, walked once per build.
// The map is shared, so a caller does not write to it.
func (ctx *ExprCtx) InstanceRootIDs() map[string]bool {
	if ctx.memo == nil {
		return InstanceRootIDs(ctx.Pkg)
	}
	if ctx.memo.instanceRoots == nil {
		ctx.memo.instanceRoots = InstanceRootIDs(ctx.Pkg)
	}
	return ctx.memo.instanceRoots
}

// ForComponent returns a new ExprCtx scoped to a component.
func (ctx *ExprCtx) ForComponent(comp *ir.Component) *ExprCtx {
	c := ctx.Clone()
	c.Component = comp
	c.Surface = nil
	c.App = false
	return c
}

// ForSurface returns a new ExprCtx with the document written from n as the
// innermost scope. Any component scope is kept, since a component's vars are
// still read by bare name inside it.
func (ctx *ExprCtx) ForSurface(n *ir.NodeInst) *ExprCtx {
	c := ctx.Clone()
	c.Surface = n
	return c
}

// Resolve determines what a name refers to in the current scope.
func (ctx *ExprCtx) Resolve(name string) (ir.Symbol, NameKind) {
	if ctx.IsLocal(name) {
		return nil, NameLocal
	}

	// A document's route parameters are not a declaration the body made:
	// they are the binding the page's population hands what it renders, one
	// cell per document rather than one per program, so package scope has
	// nothing to find.
	if ctx.Surface != nil && ctx.Surface.Params != nil && ctx.Surface.Params.Name == name {
		return ctx.Surface.Params, NameStateVar
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

	// Package-level funcs. A method of a struct or an enum is reached through
	// its receiver and never by bare name, so a local named like one -- a
	// match binding called `checked` beside http.Response's method -- stays
	// the local.
	for _, f := range ctx.Pkg.Funcs {
		if f.Name == name && !typeMethod(f) {
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

// typeMethod reports whether f is a method declared on a struct or an enum.
func typeMethod(f *ir.Func) bool {
	switch f.RecvDecl().(type) {
	case *ir.StructDef, *ir.EnumDef:
		return true
	}
	return false
}

// RenamedName returns the renamed name for a local var, or the original if no rename.
func (ctx *ExprCtx) RenamedName(name string) string {
	if r, ok := ctx.Renamed(name); ok {
		return r
	}
	return name
}

// IsLocal reports whether name is a local of the scope: a loop variable, a
// lambda's parameter, or a name a translation declared bare.
func (ctx *ExprCtx) IsLocal(name string) bool {
	for s := ctx.locals; s != nil; s = s.parent {
		if s.batch != nil {
			if s.batch[name] {
				return true
			}
		} else if s.name == name {
			return true
		}
	}
	return false
}

// ShadowedStateVar reports a read of a state var -- the component's or the
// package's -- whose name a local of the emitted scope also binds: a render
// slot's `parent` parameter beside a program's `var parent`, or a function's
// local named like a test build's root prop. Resolve, asking by name, answers
// such a read as the local; the symbol says it is the state.
func (ctx *ExprCtx) ShadowedStateVar(sym ir.Symbol) (*ir.Var, bool) {
	v, ok := sym.(*ir.Var)
	if !ok || v.IsConst || v.NodeHandle || !ctx.IsLocal(v.Name) {
		return nil, false
	}
	if ctx.Component != nil && slices.Contains(ctx.Component.Vars, v) {
		return v, true
	}
	return v, ctx.Pkg != nil && slices.Contains(ctx.Pkg.Vars, v)
}

// BodyLocals is the name of every local a function body declares, at any
// depth, for its emitter to bind before it emits the body: one left unbound
// read as the state var of its name where there is one -- `state.label` and
// `m.label` in a free function of a test build rooted at a component with a
// `label` prop (cmd/sngl/testdata/func_local_named_like_state_runs.txt). A
// read of the state itself is told apart by its symbol (ShadowedStateVar).
func BodyLocals(block []ir.Stmt) []string {
	var out []string
	_ = ir.WalkStmts(block, func(s ir.Stmt) error {
		if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil {
			out = append(out, lv.Name)
		}
		return nil
	})
	return out
}

// Renamed is what a local renders as where a scope renamed it. A local
// declared again without a rename inside that scope keeps the rename.
func (ctx *ExprCtx) Renamed(name string) (string, bool) {
	for s := ctx.locals; s != nil; s = s.parent {
		if s.renamed && s.name == name {
			return s.as, true
		}
	}
	return "", false
}

// localScope is one frame of an ExprCtx's locals. Every clone of a scope
// shares its frames, so none is written once made.
type localScope struct {
	parent  *localScope
	batch   map[string]bool
	name    string
	as      string
	renamed bool
}

// Clone creates a copy sharing the locals, which a copy only adds frames to.
func (ctx *ExprCtx) Clone() *ExprCtx {
	return &ExprCtx{
		Platform:      ctx.Platform,
		Pkg:           ctx.Pkg,
		Component:     ctx.Component,
		Surface:       ctx.Surface,
		App:           ctx.App,
		locals:        ctx.locals,
		EventVar:      ctx.EventVar,
		EventParam:    ctx.EventParam,
		Helpers:       ctx.Helpers,       // shared — helpers accumulate globally
		NativeImports: ctx.NativeImports, // shared — accumulates across clones
		Maps:          ctx.Maps,
		OutDir:        ctx.OutDir,
		ContextVar:    ctx.ContextVar,
		// Deep-copy the test/http-only maps (nil-safe via maps.Clone).
		RawFieldAccess:  maps.Clone(ctx.RawFieldAccess),
		MethodFields:    maps.Clone(ctx.MethodFields),
		IdentRewrites:   maps.Clone(ctx.IdentRewrites),
		FreeFuncs:       ctx.FreeFuncs,       // shared — one decision for the whole build
		ModelParamFuncs: ctx.ModelParamFuncs, // shared, for the same reason
		StateReceiver:   ctx.StateReceiver,
		// The naming policy travels with the receiver it applies to.
		StateFieldsExported: ctx.StateFieldsExported,
		OuterReceiver:       ctx.OuterReceiver,
		OuterNodes:          ctx.OuterNodes,
		ClosureMethods:      ctx.ClosureMethods,
		memo:                ctx.memo,
	}
}

// WithLocal returns a clone with an additional local variable.
func (ctx *ExprCtx) WithLocal(name string) *ExprCtx {
	c := ctx.Clone()
	c.locals = &localScope{parent: ctx.locals, name: name}
	return c
}

// WithLocals returns a clone with every one of names a local.
func (ctx *ExprCtx) WithLocals(names ...string) *ExprCtx {
	c := ctx.Clone()
	if len(names) == 0 {
		return c
	}
	batch := make(map[string]bool, len(names))
	for _, n := range names {
		batch[n] = true
	}
	c.locals = &localScope{parent: ctx.locals, batch: batch}
	return c
}

// WithRenamedLocal returns a clone where name is a local that renders as `as`.
func (ctx *ExprCtx) WithRenamedLocal(name, as string) *ExprCtx {
	c := ctx.Clone()
	c.locals = &localScope{parent: ctx.locals, name: name, as: as, renamed: true}
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
	// A func that writes state is an action that happens to return a value.
	// Emitted as a computed, fyne built its body with the getter emitter,
	// which does not translate the updater the write needs: `__n0.Text = …`,
	// a field no Go type has.
	if f.Purity == ir.PurityMutates {
		return false
	}
	n := len(f.Params)
	if f.IsMethod() {
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

// TestSetContext reads `t.setContext(ctx, value)`: the context's name and the
// value. passContext leaves the context argument as its name, since that
// argument names a context rather than reading one; unlowered it is still the
// read.
func TestSetContext(c *ir.Call) (name string, value ir.Expr, ok bool) {
	if !ir.IsTestCall(c, ir.TestSetContextID) || len(c.Args) < 2 {
		return "", nil, false
	}
	switch x := c.Args[len(c.Args)-2].Value.(type) {
	case *ir.ContextRead:
		name = x.Ref.Name
	case *ir.Literal:
		name = x.Value
	default:
		return "", nil, false
	}
	return name, c.Args[len(c.Args)-1].Value, true
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

// TriggerArgs is the types of the arguments each test trigger hands an event,
// keyed "<id>.<event>" as the test wrote it -- `c.box.change(true)` is
// "box.change". It is for a platform whose invoker reads no argument of its
// own and so has only the tests to say what its signature takes.
func TriggerArgs(pkg *ir.Package) map[string][]*ir.Type {
	out := map[string][]*ir.Type{}
	fns, _, _ := CollectTestFuncs(pkg)
	for _, fn := range fns {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			c, ok := n.(*ir.Call)
			if !ok || len(c.Args) == 0 {
				return nil
			}
			tt, ok := EventTriggerTarget(c)
			if !ok {
				return nil
			}
			types := make([]*ir.Type, 0, len(c.Args))
			for _, a := range c.Args {
				t := a.Value.ExprType()
				if t == nil {
					return nil
				}
				types = append(types, t)
			}
			out[tt.ID+"."+c.Event] = types
			return nil
		})
	}
	return out
}

// TriggeredEvents is every event a test in pkg fires, keyed "<id>.<event>"
// as TriggerArgs is.
func TriggeredEvents(pkg *ir.Package) map[string]bool {
	out := map[string]bool{}
	fns, _, _ := CollectTestFuncs(pkg)
	for _, fn := range fns {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if c, ok := n.(*ir.Call); ok {
				if tt, ok := EventTriggerTarget(c); ok {
					out[tt.ID+"."+c.Event] = true
				}
			}
			return nil
		})
	}
	return out
}

// StateFieldNames is every name the emitted state object declares as a cell:
// the vars a window, the package and each component own. A test reads one as
// an ordinary field -- `c.state.entry` -- so it is what tells that shape from
// `c.<id>.<prop>`, where the middle segment is a node's `#id` and the read has
// to go through the host's view tree instead.
//
// A positive set rather than the node ids, because the state object is built
// from these same vars: what it declares and what a field read may name are
// then one list rather than two that have to agree.
func StateFieldNames(pkg *ir.Package) map[string]bool {
	names := map[string]bool{}
	if pkg == nil {
		return names
	}
	add := func(vars []*ir.Var) {
		for _, v := range vars {
			if v != nil && !v.NodeHandle {
				names[v.Name] = true
			}
		}
	}
	add(pkg.Vars)
	for _, c := range pkg.Components {
		add(c.Vars)
	}
	return names
}
