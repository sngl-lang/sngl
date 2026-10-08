package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/names"
	"duckfam.us/sngl/ir"
)

// nestedFunc is one `func` written inside a function body, and the function
// that wrote it. Recorded here rather than read back later, because by the time
// renameNestedFuncs runs the scope it was written in is gone.
type nestedFunc struct {
	fn *ir.Func
	in *ir.Func
}

// checkNestedFunc registers and checks a `func` written as a statement: in a
// function body, or in an if/for block, a handler or a provider's children.
//
// Either way the declaration is hoisted -- it joins the enclosing component,
// window or package, which is what a backend emits from. Inside a *function*
// body that leaves two things to reconcile, and ir.Func.Nested is which
// declarations they apply to:
//
//   - the name is the enclosing body's, the rule a body-local struct or
//     component already follows (registerBodyDecl), while the namespace a
//     backend emits into is flat. renameNestedFuncs is that half.
//   - the hoist is not a closure, so the enclosing params and locals must not
//     resolve from inside one. The scope swap below is that half.
//
// A func in a node's block -- a window's body, say -- reaches here too and is
// neither: it belongs to the node's container, the way a component-body func
// belongs to the component, and it keeps the name it was written under.
func (c *checker) checkNestedFunc(x *ast.FuncDef) {
	fn, built := c.nestedFuncs[x]
	if !built {
		fn = c.buildFunc(x)
		fn.Nested = c.currentFunc != nil
		if c.nestedFuncs == nil {
			c.nestedFuncs = map[*ast.FuncDef]*ir.Func{}
		}
		c.nestedFuncs[x] = fn
		if fn.Nested {
			c.nestedOrder = append(c.nestedOrder, nestedFunc{fn: fn, in: c.currentFunc})
		}

		switch {
		case c.currentComponent != nil:
			c.currentComponent.Funcs = append(c.currentComponent.Funcs, fn)
		case c.pkg != nil:
			c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
		}
	}

	// Bound in the body that wrote it, so a sibling body does not see it and
	// two in one body are that scope's duplicate.
	c.declare(x.Pos, fn)

	// The scope the enclosing function was *entered* from, plus the funcs
	// hoisted out of it, this one included: they share the flat namespace a
	// backend emits into, so a sibling call and recursion resolve while the
	// enclosing params and locals stay out of reach.
	if outer := c.funcOuterScope; fn.Nested && outer != nil {
		if c.nestedScope == nil {
			c.nestedScope = ir.NewScope(outer)
		}
		// The duplicate was already reported against the body's own scope by
		// the declare above; here it is the same symbol arriving twice.
		_ = c.nestedScope.Declare(fn)
		hidden, scope := c.nestedHidden, c.scope
		c.nestedHidden, c.scope = scope, c.nestedScope
		defer func() { c.nestedHidden, c.scope = hidden, scope }()
	}
	c.checkFuncBody(fn)
}

// captureHint explains an unresolved name that the enclosing function body
// would have resolved. Without it the diagnostic is a bare "undefined", which
// says nothing about why a name plainly written two lines up is not there.
func (c *checker) captureHint(name string) string {
	if c.nestedHidden == nil {
		return ""
	}
	if _, ok := c.nestedHidden.Lookup(name); !ok {
		return ""
	}
	return " — a nested func is hoisted rather than closed over, so it cannot read the enclosing function's params or locals; pass it as a parameter"
}

// renameNestedFuncs gives every func hoisted out of a function body an emitted
// name of its own.
//
// The name one is *written* under is scoped to a single body, so two bodies may
// each declare `helper` and mean two functions; the namespace every backend
// emits into is flat, and takes the second as a redeclaration of the first.
// Renaming is available here because a call site holds the declaration
// (ir.Call.Func) rather than the name.
//
// Runs at the end of the check, once every body has resolved: the scopes hold
// the written name, so renaming earlier would leave a body unable to find what
// it calls.
func (c *checker) renameNestedFuncs(pkg *ir.Package) {
	if len(c.nestedOrder) == 0 {
		return
	}
	renaming := make(map[*ir.Func]bool, len(c.nestedOrder))
	for _, n := range c.nestedOrder {
		renaming[n.fn] = true
	}
	// The names to avoid are the ones nothing here is about to change: a
	// declaration counted against itself would take a suffix for colliding
	// with nobody.
	//
	// One registry for every kind, because the namespace being avoided is the
	// one every backend has to satisfy at once -- Go's package scope holds a
	// type and a func together. Reserved from slices and never from a map, so
	// two runs allocate the same names.
	reg := &names.Registry{}
	if pkg != nil {
		for _, s := range pkg.Structs {
			reg.Reserve(s.Name)
		}
		for _, e := range pkg.Enums {
			reg.Reserve(e.Name)
		}
		for _, u := range pkg.Units {
			reg.Reserve(u.Name)
		}
		for _, comp := range pkg.Components {
			reg.Reserve(comp.Name)
		}
	}
	for _, o := range ir.Owners(pkg) {
		for _, f := range o.Funcs {
			if !renaming[f] {
				reg.Reserve(f.Name)
			}
		}
	}
	for _, n := range c.nestedOrder {
		// The enclosing name is already renamed where that body was itself
		// nested, because nestedOrder is in declaration order: three levels
		// compose rather than collide.
		n.fn.Name = reg.Unique(n.in.Name + "__" + n.fn.Name)
	}
	c.orderNestedFuncs(pkg)
}

// orderNestedFuncs moves each hoisted func ahead of the body that declared it.
//
// The hoist appends, so a nested func always landed *after* its only caller.
// android emits an owner's funcs as local `fun`s inside one composable, where a
// local function may not be referenced above its declaration -- so the append
// order is the one order that does not compile.
func (c *checker) orderNestedFuncs(pkg *ir.Package) {
	declaredIn := map[*ir.Func]*ir.Func{}
	children := map[*ir.Func][]*ir.Func{}
	for _, n := range c.nestedOrder {
		declaredIn[n.fn] = n.in
		// Built by walking nestedOrder rather than by ranging declaredIn: a
		// map hands them back in no order, and two nested in one body would
		// swap places between runs.
		children[n.in] = append(children[n.in], n.fn)
	}
	reorder := func(fns []*ir.Func) {
		// A collection holds some of the chain and not the rest -- a component
		// method is in pkg.Funcs while the funcs it wrote are only in
		// comp.Funcs -- so membership is what bounds the walk.
		in := make(map[*ir.Func]bool, len(fns))
		for _, fn := range fns {
			in[fn] = true
		}
		out := make([]*ir.Func, 0, len(fns))
		emitted := make(map[*ir.Func]bool, len(fns))
		var emit func(fn *ir.Func)
		emit = func(fn *ir.Func) {
			if emitted[fn] || !in[fn] {
				return
			}
			emitted[fn] = true
			for _, ch := range children[fn] {
				emit(ch)
			}
			out = append(out, fn)
		}
		for _, fn := range fns {
			// A child is emitted by its parent, and reached here only when the
			// parent is in some other collection.
			if owner, nested := declaredIn[fn]; nested && in[owner] {
				continue
			}
			emit(fn)
		}
		copy(fns, out)
	}
	if pkg == nil {
		return
	}
	reorder(pkg.Funcs)
	for _, comp := range pkg.Components {
		reorder(comp.Funcs)
	}
}
