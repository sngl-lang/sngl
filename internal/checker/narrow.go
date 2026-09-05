package checker

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Flow narrowing for option<T>. A null test says, in the branch where it
// cannot be null, that the value is a T -- which is the only way to consume an
// option<T> as a T, since option declares no methods and every reader in the
// library is a total function taking a default.
//
// The judgement is not purely type-level: option<T> is a nullable
// representation on Kotlin (T?) and JavaScript (a value or null) but a pointer
// on Go (*T), so a narrowed read carries an ir.Conversion from option<T> to T
// and each language emits its own unwrap. Conversion rather than
// ir.Unary{UnaryDeref}: a deref means ref<T> to every phase that already
// reads one, and both passNoRef and the constant folder rewrite it on that
// assumption.

// narrowKey identifies what a null test narrows: a root symbol and the field
// path read off it. The root is the *symbol*, not its name, so a shadowed
// declaration is a different thing to narrow.
type narrowKey struct {
	root ir.Symbol
	path string // "" for the root itself, "next.value" for a field path
}

// narrowFact is what the test established about that path.
type narrowFact struct {
	typ  *ir.Type // the unwrapped type
	pos  ast.Pos  // where the test was written
	name string   // source spelling, for diagnostics
}

// narrowCheck defers the soundness question about one *used* narrowing to
// after the purity fixpoint, which is the first moment a call's transitive
// Writes set is complete. Recorded only when the narrowing was actually read
// through, so `if x != null { x = null }` -- which narrows nothing anyone
// used -- stays the legal program it is today.
type narrowCheck struct {
	key    narrowKey
	fact   narrowFact
	region []ir.Stmt
	expr   ir.Expr // an && right operand, which is a region of one expression
}

func (c *checker) narrowingActive() bool { return len(c.narrowed) > 0 && c.noNarrow == 0 }

// applyNarrowing rewrites a read of a narrowed path into the unwrap the
// backends emit. Called once, at the expression dispatch, so every reader of a
// narrowed value goes through it.
func (c *checker) applyNarrowing(e ir.Expr) ir.Expr {
	if e == nil || !c.narrowingActive() {
		return e
	}
	t := exprType(e)
	if t == nil || t.Kind != ir.TypeOption {
		return e
	}
	key, ok := narrowKeyOf(e)
	if !ok {
		return e
	}
	fact, ok := c.narrowed[key]
	if !ok || fact.typ == nil {
		return e
	}
	c.narrowUsed[key] = true
	return &ir.Conversion{Type: fact.typ, Operand: e}
}

// narrowKeyOf peels a field path down to the symbol it is rooted at. Only a
// variable, parameter or loop variable roots one, and only struct field reads
// extend it: an index cannot, because two spellings may name one element and a
// write through either would have to invalidate both.
func narrowKeyOf(e ir.Expr) (narrowKey, bool) {
	var segs []string
	for {
		switch n := e.(type) {
		case *ir.Select:
			segs = append(segs, n.Field)
			e = n.Operand
		case *ir.Ident:
			switch n.Sym.(type) {
			case *ir.Var, *ir.Param, *ir.LoopVar:
				slices.Reverse(segs)
				return narrowKey{root: n.Sym, path: strings.Join(segs, ".")}, true
			}
			return narrowKey{}, false
		default:
			return narrowKey{}, false
		}
	}
}

// pathPrefixOf reports whether a is a is a prefix of b in path segments.
// A write to `head` invalidates a narrowing of `head.next`; a write to
// `head.next.value` does not, since it cannot make `head.next` null.
func pathPrefixOf(a, b string) bool {
	if a == "" {
		return true
	}
	return a == b || strings.HasPrefix(b, a+".")
}

// nullOperand reports whether an operand is the null placeholder.
func nullOperand(e ir.Expr) bool {
	for {
		conv, ok := e.(*ir.Conversion)
		if !ok {
			break
		}
		e = conv.Operand
	}
	t := exprType(e)
	return t != nil && t.Kind == ir.TypeNull
}

// narrowingsFrom collects what a condition establishes when it holds
// (positive) or when it fails. `&&` is the conjunction of what both operands
// establish; `||` is the same fact negated, since neither held. Every other
// form establishes nothing -- a plain non-narrowing, so the existing "cannot
// pass option<T> as T" stands rather than a wrong type reaching a backend.
func narrowingsFrom(cond ir.Expr, positive bool, out map[narrowKey]narrowFact) {
	switch n := cond.(type) {
	case *ir.Conversion:
		narrowingsFrom(n.Operand, positive, out)
	case *ir.Unary:
		if n.Op == ast.UnaryNot {
			narrowingsFrom(n.Operand, !positive, out)
		}
	case *ir.Binary:
		switch n.Op {
		case ast.BinNeq:
			if positive {
				addNullTest(n, out)
			}
		case ast.BinEq:
			if !positive {
				addNullTest(n, out)
			}
		case ast.BinAnd:
			if positive {
				narrowingsFrom(n.Left, true, out)
				narrowingsFrom(n.Right, true, out)
			}
		case ast.BinOr:
			if !positive {
				narrowingsFrom(n.Left, false, out)
				narrowingsFrom(n.Right, false, out)
			}
		}
	}
}

func addNullTest(n *ir.Binary, out map[narrowKey]narrowFact) {
	var subject ir.Expr
	switch {
	case nullOperand(n.Right):
		subject = n.Left
	case nullOperand(n.Left):
		subject = n.Right
	default:
		return
	}
	t := exprType(subject)
	if t == nil || t.Kind != ir.TypeOption || len(t.Elems) != 1 || t.Elems[0] == nil {
		return
	}
	key, ok := narrowKeyOf(subject)
	if !ok {
		return
	}
	pos := ast.Pos{}
	if n.AST != nil {
		pos = n.AST.Pos
	}
	out[key] = narrowFact{typ: t.Elems[0], pos: pos, name: narrowSpelling(key)}
}

func narrowSpelling(k narrowKey) string {
	name := ""
	if k.root != nil {
		name = k.root.SymName()
	}
	if k.path == "" {
		return name
	}
	return name + "." + k.path
}

// pushNarrowings makes a set of facts current and returns the restore. The
// restore records, for every fact this frame introduced that was read through,
// a deferred soundness check over the region.
func (c *checker) pushNarrowings(facts map[narrowKey]narrowFact, region *[]ir.Stmt, expr *ir.Expr) func() {
	if len(facts) == 0 {
		return func() {}
	}
	if c.narrowed == nil {
		c.narrowed = map[narrowKey]narrowFact{}
	}
	if c.narrowUsed == nil {
		c.narrowUsed = map[narrowKey]bool{}
	}
	type saved struct {
		fact narrowFact
		had  bool
		used bool
	}
	prev := make(map[narrowKey]saved, len(facts))
	for k, f := range facts {
		old, had := c.narrowed[k]
		prev[k] = saved{fact: old, had: had, used: c.narrowUsed[k]}
		c.narrowed[k] = f
		c.narrowUsed[k] = false
	}
	return func() {
		for k, f := range facts {
			if c.narrowUsed[k] {
				chk := narrowCheck{key: k, fact: f}
				if region != nil {
					chk.region = *region
				}
				if expr != nil {
					chk.expr = *expr
				}
				c.narrowChecks = append(c.narrowChecks, chk)
			}
			p := prev[k]
			if p.had {
				c.narrowed[k] = p.fact
			} else {
				delete(c.narrowed, k)
			}
			c.narrowUsed[k] = p.used
		}
	}
}

// suspendNarrowing turns narrowing off for the extent of the returned restore.
// An assignment target and an `&` operand are lvalues: wrapping one in the
// unwrap conversion would produce something with no storage behind it.
func (c *checker) suspendNarrowing() func() {
	c.noNarrow++
	return func() { c.noNarrow-- }
}

// clearNarrowings drops every fact for the extent of the returned restore. A
// lambda body runs later, or not at all, and by then the test that narrowed a
// value may no longer hold -- so no narrowing crosses an imperative-body
// boundary. enterFuncBody is the one place that boundary is named.
func (c *checker) clearNarrowings() func() {
	savedFacts, savedUsed := c.narrowed, c.narrowUsed
	c.narrowed, c.narrowUsed = nil, nil
	return func() { c.narrowed, c.narrowUsed = savedFacts, savedUsed }
}

// validateNarrowings runs after the purity fixed point, which is the first
// moment a call's transitive Writes set is complete. A narrowing that a write
// could survive is reported where the write is, rather than silently letting a
// T-typed read reach a backend that would dereference a null.
func (c *checker) validateNarrowings(reactive map[*ir.Var]struct{}) {
	for _, chk := range c.narrowChecks {
		v := &narrowValidator{c: c, chk: chk, reactive: reactive}
		if chk.expr != nil {
			ir.Walk(chk.expr, v.visit)
		}
		for _, s := range chk.region {
			if v.done {
				break
			}
			ir.Walk(s, v.visit)
		}
	}
}

type narrowValidator struct {
	c        *checker
	chk      narrowCheck
	reactive map[*ir.Var]struct{}
	done     bool
}

func (v *narrowValidator) report(pos ast.Pos, what string) {
	if v.done {
		return
	}
	v.done = true
	v.c.error(pos, "%s is %s here, so the null test on it no longer holds; read it into a local first", v.chk.fact.name, what)
}

// invalidatedBy reports whether a write to target reaches the narrowed path.
// A precise field path invalidates only when it is a prefix of the narrowed
// one; any other shape rooted at the same symbol -- an index, a deref, a
// receiver -- is taken conservatively.
func (v *narrowValidator) invalidatedBy(target ir.Expr) bool {
	if key, ok := narrowKeyOf(target); ok {
		return key.root == v.chk.key.root && pathPrefixOf(key.path, v.chk.key.path)
	}
	return rootSymbolOf(target) == v.chk.key.root
}

func rootSymbolOf(e ir.Expr) ir.Symbol {
	for {
		switch n := e.(type) {
		case *ir.Select:
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		case *ir.Conversion:
			e = n.Operand
		case *ir.Unary:
			e = n.Operand
		case *ir.Ident:
			return n.Sym
		default:
			return nil
		}
	}
}

func (v *narrowValidator) visit(n ir.Node) error {
	if v.done {
		return ir.SkipDir
	}
	switch x := n.(type) {
	case *ir.Assign:
		if v.invalidatedBy(x.Target) {
			v.report(narrowStmtPos(x.AST), "assigned")
		}
	case *ir.Toggle:
		if v.invalidatedBy(x.Target) {
			v.report(narrowStmtPos(x.AST), "assigned")
		}
	case *ir.Unary:
		if x.Op == ast.UnaryAddr && v.invalidatedBy(x.Operand) {
			v.report(v.chk.fact.pos, "taken by reference")
		}
	case *ir.Call:
		v.visitCall(x)
	}
	return nil
}

func (v *narrowValidator) visitCall(x *ir.Call) {
	if x.Func == nil {
		return
	}
	// `l.push(x)` writes through its receiver, which the intrinsic names --
	// the same fact passReactivity and analyzeEffects both read.
	if x.Func.Intrinsic != "" && len(x.Args) > 0 {
		if def, ok := ir.IntrinsicByName(x.Func.Intrinsic); ok && def.MutatesReceiver {
			if v.invalidatedBy(x.Args[0].Value) {
				v.report(v.chk.fact.pos, "mutated through a method call")
				return
			}
		}
	}
	// A callee can only reach the narrowed value if it is reactive state -- a
	// package or component var. A local, parameter or loop variable is
	// unreachable from a callee unless it was passed by reference, which the
	// UnaryAddr case above already refuses.
	//
	// The question asked of the callee is its transitive Purity rather than
	// its Writes set: the fixed point in checkBodies propagates purity and not
	// Writes, so a function that writes the var only through a callee has an
	// empty Writes of its own.
	root, ok := v.chk.key.root.(*ir.Var)
	if !ok {
		return
	}
	if _, isReactive := v.reactive[root]; !isReactive {
		return
	}
	if x.Func.Purity >= ir.PurityMutates {
		v.report(v.chk.fact.pos, "reachable by a mutating call in this branch")
	}
}

func narrowStmtPos(s ast.Stmt) ast.Pos {
	if s != nil {
		if p := s.StmtPos(); p != nil {
			return *p
		}
	}
	return ast.Pos{}
}
