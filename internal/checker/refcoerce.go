package checker

import (
	"maps"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A plain T is accepted where a ref<T> is expected, and reaches the callee as
// the value it already was. That is what lets one declaration serve both
// callers:
//
//	func fetch(url ref<string>) remote.Value<Result>
//
//	fetch(&url)   // the handle: the callee keeps seeing what url becomes
//	fetch(url)    // the value:  a snapshot of what it is now
//
// The `&` is the difference the caller writes, and the whole point is that it
// stays visible in the IR -- a pass reading a key can tell a live dependency
// from a snapshot by whether the argument is a ref.
//
// The coercion is only sound for a parameter the callee never does anything
// with but read: a write through a ref that is really a value would land in a
// copy nobody reads, and a ref that escapes into a struct or another call would
// outlive the expression that made it. So `*p` in a read position is the only
// use permitted, checked once every body has been checked -- a call may be
// checked before the callee's body, so the call site cannot ask.
type refCoercion struct {
	pos ast.Pos
	p   *ir.Param
}

// coerceValueToRef reports whether a value of actual may stand in for a ref<T>
// parameter, recording the call site for verifyRefCoercions to hold the callee
// to.
func (c *checker) coerceValueToRef(actual *ir.Type, p *ir.Param, pos ast.Pos) bool {
	if p == nil || p.Type == nil || p.Type.Kind != ir.TypeRef || len(p.Type.Elems) != 1 {
		return false
	}
	elem := p.Type.Elems[0]
	if elem == nil || actual == nil || !actual.IsAssignableTo(elem) {
		return false
	}
	c.refCoercions = append(c.refCoercions, refCoercion{pos: pos, p: p})
	return true
}

// verifyRefCoercions holds every callee that took a coerced argument to the one
// use the coercion is sound for. Run after all bodies are checked, so the rule
// does not depend on which of a call and its callee the checker reached first.
func (c *checker) verifyRefCoercions(pkg *ir.Package) {
	if len(c.refCoercions) == 0 {
		return
	}
	owners := paramOwners(pkg)
	// A library declaration is the common case -- sngl:remote/http's `fetch` is
	// the reason the coercion exists -- and its parameters belong to a package
	// the program is not made of.
	for _, lib := range c.libs.pkgs {
		maps.Copy(owners, paramOwners(lib))
	}
	// One diagnostic per parameter: every call site handing it a value is the
	// same mistake in the declaration, reported where the declaration is.
	done := map[*ir.Param]bool{}
	for _, rc := range c.refCoercions {
		fn := owners[rc.p]
		if fn == nil || done[rc.p] {
			continue
		}
		if derefOnly(fn.Block, rc.p) {
			continue
		}
		done[rc.p] = true
		c.error(rc.pos, "cannot pass %s as %s: %s writes through %q or lets it escape, so it needs a ref (write `&x`)",
			rc.p.Type.Elems[0], rc.p.Type, fn.Name, rc.p.Name)
	}
}

// paramOwners maps each parameter to the function that declares it.
func paramOwners(pkg *ir.Package) map[*ir.Param]*ir.Func {
	owners := map[*ir.Param]*ir.Func{}
	if pkg == nil {
		return owners
	}
	add := func(fns []*ir.Func) {
		for _, fn := range fns {
			for _, p := range fn.Params {
				owners[p] = fn
			}
		}
	}
	add(pkg.Funcs)
	for _, o := range ir.Owners(pkg) {
		add(o.Funcs)
	}
	return owners
}

// derefOnly reports whether every mention of p in stmts is `*p` in a read
// position -- the one use a coerced value can answer.
//
// Counting rather than pattern-matching a shape: the walk visits a deref and
// then its operand, so a parameter every one of whose mentions is a deref is
// one where the two counts agree. Anything else -- passing it on, storing it,
// naming it bare -- shows up as an Ident with no deref above it.
func derefOnly(stmts []ir.Stmt, p *ir.Param) bool {
	var mentions, derefs, writes int
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		switch x := e.(type) {
		case *ir.Ident:
			if x.Sym == ir.Symbol(p) {
				mentions++
			}
		case *ir.Unary:
			if x.Op == ast.UnaryDeref && namesParam(x.Operand, p) {
				derefs++
			}
		}
		return nil
	})
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		asn, isAssign := s.(*ir.Assign)
		if !isAssign {
			return nil
		}
		if u, isUnary := asn.Target.(*ir.Unary); isUnary && u.Op == ast.UnaryDeref && namesParam(u.Operand, p) {
			writes++
		}
		return nil
	})
	return mentions == derefs && writes == 0
}

func namesParam(e ir.Expr, p *ir.Param) bool {
	id, isIdent := e.(*ir.Ident)
	return isIdent && id.Sym == ir.Symbol(p)
}
