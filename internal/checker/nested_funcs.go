package checker

import (
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// nestedFunc is one `func` written inside another body, and the name of the
// body that wrote it. The owner is recorded here rather than read back later
// because by the time renameNestedFuncs runs the scope it was written in is
// gone.
type nestedFunc struct {
	fn    *ir.Func
	owner string
	// in is the function that declared it, nil when the body was a component,
	// window or block rather than a function.
	in *ir.Func
}

// checkNestedFunc registers and checks a `func` declared inside another body:
// a function body, an if/for block, a handler, or a provider's children.
//
// The declaration is hoisted -- it joins the enclosing component, window or
// package, which is what a backend emits from -- while the *name* is scoped to
// the body that wrote it, the rule a body-local struct or component already
// follows (registerBodyDecl). The two halves have to be reconciled at emit,
// where the namespace is flat, and renameNestedFuncs is that.
func (c *checker) checkNestedFunc(x *ast.FuncDef) {
	fn, built := c.nestedFuncs[x]
	if !built {
		fn = c.buildFunc(x)
		fn.Nested = true
		if c.nestedFuncs == nil {
			c.nestedFuncs = map[*ast.FuncDef]*ir.Func{}
		}
		c.nestedFuncs[x] = fn
		c.nestedOrder = append(c.nestedOrder, nestedFunc{fn: fn, owner: c.nestedOwner(), in: c.currentFunc})

		switch {
		case c.currentComponent != nil:
			c.currentComponent.Funcs = append(c.currentComponent.Funcs, fn)
		case c.currentWindow != nil:
			// A window owns funcs the way a component does (ir.Owners), and
			// its state is in the same model -- so a target that emits a
			// component's funcs as methods has to emit these as methods too.
			// Registered in both collections, the way a component's nested
			// methods are, because pkg.Funcs is what a call site resolves
			// through.
			c.currentWindow.Funcs = append(c.currentWindow.Funcs, fn)
			c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
		case c.pkg != nil:
			c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
		}
	}

	// Bound in the body that wrote it, so a sibling body does not see it and
	// two in one body are that scope's duplicate.
	c.declare(x.Pos, fn)

	// The hoist gives the declaration no closure, so the body is checked
	// against the scope its enclosing function was entered from rather than
	// the one in force here. Checked in force, an enclosing param resolved and
	// then reached the backend as a bare name nothing declared.
	if outer := c.funcOuterScope; outer != nil {
		hidden, scope := c.nestedHidden, c.scope
		c.nestedHidden, c.scope = scope, outer
		defer func() { c.nestedHidden, c.scope = hidden, scope }()
	}
	c.checkFuncBody(fn)
}

// nestedOwner names the body a nested func was written in, for the emitted
// name renameNestedFuncs builds out of it.
func (c *checker) nestedOwner() string {
	switch {
	case c.currentFunc != nil:
		return c.currentFunc.Name
	case c.currentComponent != nil:
		return c.currentComponent.Name
	case c.currentWindow != nil:
		return c.currentWindow.Name
	}
	return ""
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

// renameNestedFuncs gives every hoisted func an emitted name of its own.
//
// The name a nested func is *written* under is scoped to one body, so two
// bodies may each declare `helper` and mean two functions; the namespace every
// backend emits into is flat, and would take the second declaration of one
// name as a redeclaration of the first. Renaming is what a body-local type is
// still waiting on (#198) and what its `claimBodyType` reports in the
// meantime; a func can have it now because a call site holds the declaration
// (ir.Call.Func) rather than the name.
//
// Runs at the end of the check, once every body has resolved: the scopes hold
// the written name, so renaming earlier would leave the body unable to find
// what it calls.
func (c *checker) renameNestedFuncs() {
	if len(c.nestedOrder) == 0 {
		return
	}
	taken := map[string]bool{}
	for _, o := range ir.Owners(c.pkg) {
		for _, f := range o.Funcs {
			taken[f.Name] = true
		}
	}
	for _, n := range c.nestedOrder {
		base := n.fn.Name
		if n.owner != "" {
			base = n.owner + "__" + n.fn.Name
		}
		name := base
		for i := 2; taken[name]; i++ {
			name = base + strconv.Itoa(i)
		}
		taken[name] = true
		n.fn.Name = name
	}
	c.orderNestedFuncs()
}

// orderNestedFuncs moves each hoisted func ahead of the body that declared it.
//
// The hoist appends, so a nested func always lands *after* its only caller.
// Kotlin emits an owner's funcs as local `fun`s inside one composable, where a
// local function may not be referenced above its declaration -- so the
// append order is the one order that does not compile.
func (c *checker) orderNestedFuncs() {
	declaredIn := map[*ir.Func]*ir.Func{}
	for _, n := range c.nestedOrder {
		if n.in != nil {
			declaredIn[n.fn] = n.in
		}
	}
	if len(declaredIn) == 0 {
		return
	}
	// Built from the declaration order rather than by ranging declaredIn: a
	// map hands them back in no order, and two nested in one body would swap
	// places between runs.
	children := map[*ir.Func][]*ir.Func{}
	for _, n := range c.nestedOrder {
		if n.in != nil {
			children[n.in] = append(children[n.in], n.fn)
		}
	}
	reorder := func(fns []*ir.Func) {
		out := make([]*ir.Func, 0, len(fns))
		emitted := make(map[*ir.Func]bool, len(fns))
		var emit func(fn *ir.Func)
		emit = func(fn *ir.Func) {
			if emitted[fn] {
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
			// parent is in another collection.
			if owner, nested := declaredIn[fn]; nested && slices.Contains(fns, owner) {
				continue
			}
			emit(fn)
		}
		copy(fns, out)
	}
	if c.pkg != nil {
		reorder(c.pkg.Funcs)
		for _, comp := range c.pkg.Components {
			reorder(comp.Funcs)
		}
		for _, w := range c.pkg.Windows {
			reorder(w.Funcs)
		}
	}
}
