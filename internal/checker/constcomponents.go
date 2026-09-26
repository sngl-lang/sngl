package checker

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A `const component` says its render depends only on its props, and a
// `const` slot says the same of every population written for it. The render
// is every expression the view body evaluates -- a node's props and bindings,
// an `if` or `for` head, a slot insertion's arguments, a context override's
// value -- and not a handler's or an effect's body, which run later and are
// the place state is for. So a var only handlers touch is allowed, and html's
// `timer` override keeps its setInterval handle in one.
//
// What the render may read is what a call site can hold constant: a prop, a
// const, a const func's result, a loop variable over those, a slot
// invocation's arguments, and a context, which is a prop an ancestor supplies
// rather than the caller. A var read there, or a call to a function that is
// not const, is a positioned error.

// renderCheck walks one render and reports its first offence.
type renderCheck struct {
	c *checker
	// what names the render in the diagnostic: "const component badge".
	what string
	// skipConstProps leaves an argument bound to a const prop to the const
	// argument check, which already reports it, positioned at the same place.
	skipConstProps bool
	reported       bool
}

func (r *renderCheck) report(pos ast.Pos, fallback ast.Pos, format string, args ...any) {
	if r.reported {
		return
	}
	r.reported = true
	if !pos.IsSet() {
		pos = fallback
	}
	r.c.error(pos, "%s "+format, append([]any{r.what}, args...)...)
}

func (r *renderCheck) stmts(ss []ir.Stmt, at ast.Pos) {
	for _, s := range ss {
		if r.reported {
			return
		}
		r.stmt(s, at)
	}
}

func (r *renderCheck) stmt(s ir.Stmt, at ast.Pos) {
	switch n := s.(type) {
	case *ir.NodeInst:
		if sp := stmtPos(n.AST); sp != nil {
			at = *sp
		}
		for _, a := range n.Props {
			if r.skipConstProps && propDeclaredConst(n.Component, a.Name) {
				continue
			}
			r.expr(a.Value, at)
		}
		for _, b := range n.Bindings {
			r.expr(b.Target, at)
		}
		r.expr(n.Key, at)
		r.stmts(n.Children, at)
		for _, name := range ir.SlotNames(n.Slots) {
			if sc := n.Slots[name]; sc != nil {
				r.stmts(sc.Body, at)
			}
		}
	case *ir.If:
		r.expr(n.Cond, at)
		r.stmts(n.Body, at)
		r.stmts(n.Else, at)
	case *ir.For:
		r.expr(n.Iter, at)
		r.stmts(n.Body, at)
		r.stmts(n.Else, at)
	case *ir.SlotInst:
		for _, a := range n.Args {
			r.expr(a, at)
		}
		r.stmts(n.Children, at)
		for _, name := range ir.SlotNames(n.Slots) {
			if sc := n.Slots[name]; sc != nil {
				r.stmts(sc.Body, at)
			}
		}
	case *ir.ErrorBoundary:
		r.stmts(n.Children, at)
		r.stmts(n.Failed, at)
	case *ir.ContextProvider:
		r.expr(n.Value, at)
		r.stmts(n.Children, at)
	default:
		// Nothing else belongs in a view body; what does is read whole.
		_ = ir.WalkExprs(s, func(e ir.Expr) error { return r.visit(e, at) })
	}
}

func (r *renderCheck) expr(e ir.Expr, at ast.Pos) {
	if e == nil || r.reported {
		return
	}
	_ = ir.WalkExprs(e, func(x ir.Expr) error { return r.visit(x, at) })
}

func (r *renderCheck) visit(e ir.Expr, at ast.Pos) error {
	if r.reported {
		return ir.SkipAll
	}
	switch x := e.(type) {
	case *ir.Lambda, *ir.Closure:
		// A function value is not evaluated by the render; what it reads, it
		// reads when it runs.
		return ir.SkipDir
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && !v.IsConst {
			r.report(identPos(x), at, "renders var %q: a const render reads only props, consts and const funcs", v.Name)
		}
	case *ir.Call:
		if x.Func == nil || x.Func.Const || x.Event != "" {
			return nil
		}
		name := funcDeclName(x.Func)
		r.report(callExprPos(x), at, "calls %s in its render, which is not const (declare it const func %s)", name, name)
	}
	return nil
}

// propDeclaredConst reports whether comp declares prop name const.
func propDeclaredConst(comp *ir.Component, name string) bool {
	if comp == nil {
		return false
	}
	for _, p := range comp.Props {
		if p.Name == name {
			return p.Const
		}
	}
	return false
}

// checkConstRender holds one component body to the const render rule.
func (c *checker) checkConstRender(what string, body []ir.Stmt, at ast.Pos) {
	r := &renderCheck{c: c, what: what, skipConstProps: true}
	r.stmts(body, at)
}

// constComponentLabel names a const component's render in a diagnostic.
func constComponentLabel(name string) string {
	return "const component " + name
}

// constSlotLabel names a const slot population in a diagnostic.
func constSlotLabel(slot string, comp *ir.Component) string {
	return "the population of const slot " + strconv.Quote(slot) + " of " + comp.Name
}

// deferConstSlots records n when it populates a const slot, to be held to the
// render rule once every callee's Const is settled.
func (c *checker) deferConstSlots(n *ir.NodeInst) {
	if n == nil || n.Component == nil {
		return
	}
	for _, s := range n.Component.Slots {
		if s.Const {
			c.constSlotNodes = append(c.constSlotNodes, n)
			return
		}
	}
}

// runConstSlotChecks holds every population of a const slot to the render
// rule, positioned in the population. Its invocation arguments need not be
// constant: a slot inserted once per list item stays pure over the item, which
// is a parameter the population binds.
func (c *checker) runConstSlotChecks() { c.runConstSlotChecksFrom(0) }

// runConstSlotChecksFrom is runConstArgChecksFrom's counterpart.
func (c *checker) runConstSlotChecksFrom(mark int) {
	if mark > len(c.constSlotNodes) {
		return
	}
	seen := map[*ir.NodeInst]bool{}
	for _, n := range c.constSlotNodes[mark:] {
		if seen[n] {
			continue
		}
		seen[n] = true
		at := ast.Pos{}
		if sp := stmtPos(n.AST); sp != nil {
			at = *sp
		}
		for _, s := range n.Component.Slots {
			if !s.Const {
				continue
			}
			if s.Rest {
				c.checkConstRender(constSlotLabel(s.Name, n.Component), n.Children, at)
			}
			if sc := n.Slots[s.Name]; sc != nil {
				c.checkConstRender(constSlotLabel(s.Name, n.Component), sc.Body, at)
			}
		}
	}
	c.constSlotNodes = c.constSlotNodes[:mark]
}

// checkOverrideConst holds an override to const where it has to be. `const` on
// a base declaration is part of its contract, so every override of one is held
// to the render rule, reported at the override. An override of a non-const
// base may say const itself.
//
// A target package's overrides must end up const, inherited or written: a
// target package is what a build inlines into every program that renders the
// component, so its render may depend on nothing but what the call site says.
// This is what passInlinePure's strict mode used to find out at lowering, with
// no position.
func (c *checker) checkOverrideConst(pe pendingExtension) {
	baseConst := pe.comp.AST != nil && pe.comp.AST.Const
	if !baseConst && !pe.isConst {
		if !pe.user {
			c.error(pe.pos, "override %s[%s] in a target package must be const: a target's render depends only on its props; write `const component %s[%s]`", pe.name, pe.platform, pe.name, pe.platform)
		}
		return
	}
	c.checkConstRender(constComponentLabel(pe.name+"["+pe.platform+"]"), pe.comp.Body, pe.pos)
}

// checkTargetComponentsConst holds a target package's own bodied components to
// const, for the reason checkOverrideConst gives. A primitive -- #[intrinsic],
// a wildcard, #[builtin] -- has no body and is exempt.
func (c *checker) checkTargetComponentsConst(pkg *ir.Package) {
	for _, comp := range pkg.Components {
		if comp.Bodyless || comp.Intrinsic != "" || comp.Wildcard != "" || comp.Builtin != ir.BuiltinNone {
			continue
		}
		// `{}` renders nothing, which depends on nothing: a target's build
		// node is that, and so is anything else declared only to be named.
		if len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 {
			continue
		}
		if !comp.Const {
			c.error(compDeclPos(comp), "component %s in a target package must be const: a target's render depends only on its props; write `const component %s`", comp.Name, comp.Name)
			continue
		}
		c.checkConstRender(constComponentLabel(comp.Name), comp.Body, compDeclPos(comp))
	}
}
