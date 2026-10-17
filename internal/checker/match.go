package checker

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A match is checked into the constructs every later phase already knows: a
// statement becomes an if/else-if chain and an expression a chain of
// ternaries, each arm's patterns compared with `==` and joined by `||`. Nothing
// after the checker learns there was a match, so the family, slot and
// element-ref rules, the interpreter, the lowering and every backend read it
// as the if or the ternary it is.
//
// What a match says that the chain does not -- that its arms cover every case,
// that no pattern is written twice -- is checked here.
//
// The subject is evaluated once wherever a statement can hold it: in an
// imperative block or an expression body, a subject that calls anything is
// bound to a local ahead of the statement (preludeFor, bodyPrelude). Elsewhere -- a view body, which has nowhere to
// put the local, or a position evaluated only some of the time -- it is read
// again for each arm it is tested against, so it must be free of side effects
// (reportImpureMatchSubjects).

// matchArmIR is one arm, checked: the test that selects it (nil when it is the
// arm taken whatever the subject is) and what it does.
type matchArmIR struct {
	pos   ast.Pos
	cond  ir.Expr
	body  []ir.Stmt
	value ir.Expr
}

// checkMatchStmt checks a match written where a statement goes.
func (c *checker) checkMatchStmt(x *ast.MatchStmt) ir.Stmt {
	arms := c.checkMatch(&x.Match, false)
	if len(arms) == 0 {
		return nil
	}
	var chain []ir.Stmt
	for i, arm := range slices.Backward(arms) {
		if arm.cond == nil {
			// The last arm of an exhaustive match is taken unasked. Alone, it
			// is still a block of its own, which a literal true says without
			// a construct for it.
			if i == 0 {
				chain = []ir.Stmt{&ir.If{AST: &ast.IfStmt{Pos: arm.pos}, Cond: boolLiteral(true), Body: arm.body}}
				continue
			}
			chain = arm.body
			continue
		}
		chain = []ir.Stmt{&ir.If{AST: &ast.IfStmt{Pos: arm.pos}, Cond: arm.cond, Body: arm.body, Else: chain}}
	}
	if len(chain) == 1 {
		return chain[0]
	}
	return nil
}

// inferMatch checks a match written where a value goes.
func (c *checker) inferMatch(x *ast.MatchExpr) ir.Expr {
	expected := c.expected
	arms := c.checkMatch(&x.Match, true)
	if len(arms) == 0 {
		return invalidValue()
	}
	// What the arms join to: the type wanted, or the first arm's.
	want := expected
	if want == nil || want.Kind == ir.TypeDyn {
		want = exprType(arms[0].value)
	}
	for _, arm := range arms[1:] {
		t := exprType(arm.value)
		if t == nil || want == nil || t.Kind == ir.TypeInvalid || want.Kind == ir.TypeInvalid || t.Kind == ir.TypeDyn || want.Kind == ir.TypeDyn {
			continue
		}
		if !t.IsAssignableTo(want) {
			c.error(arm.pos, "match arms have mismatched types %s and %s", want, t)
		}
	}
	last := arms[len(arms)-1]
	out := last.value
	if last.cond != nil {
		// Only an arm that failed to check leaves the last arm a test; the
		// diagnostic is already reported, and the chain needs an end.
		out = invalidValue()
		out = &ir.Ternary{AST: &ast.TernaryExpr{Pos: last.pos}, Type: want, Cond: last.cond, Then: last.value, Else: out}
	}
	for i := len(arms) - 2; i >= 0; i-- {
		arm := arms[i]
		out = &ir.Ternary{AST: &ast.TernaryExpr{Pos: arm.pos}, Type: want, Cond: arm.cond, Then: arm.value, Else: out}
	}
	return out
}

// checkMatch checks a match's subject and arms and the rules a match states,
// and returns the arms in order. isExpr says which arm form belongs.
func (c *checker) checkMatch(m *ast.Match, isExpr bool) []matchArmIR {
	expected := c.expected
	subject, read := c.matchSubject(m)
	subjType := exprType(subject)
	known := subject == nil || (subjType != nil && subjType.Kind != ir.TypeInvalid && subjType.Kind != ir.TypeDyn)

	cov := newMatchCoverage(subjType)
	var out []matchArmIR
	var elseAt *ast.MatchArm
	for _, arm := range m.Arms {
		if elseAt != nil {
			c.error(arm.Pos, "arm is unreachable after else: else is taken whenever no arm before it is")
		}
		var conds []ir.Expr
		restore := c.noPrelude()
		for _, p := range arm.Patterns {
			if cond := c.checkMatchPattern(m, p, subject, read, cov); cond != nil {
				conds = append(conds, cond)
			}
		}
		restore()
		a := matchArmIR{pos: arm.Pos}
		if arm.Else {
			elseAt = arm
		} else {
			a.cond = orAll(conds)
		}
		if isExpr {
			if arm.IsBlock() {
				c.error(arm.Pos, "an arm of a match expression is written pattern => value: a block gives the match no value")
				a.value = invalidValue()
			} else {
				want := expected
				if want == nil && len(out) > 0 {
					want = exprType(out[0].value)
				}
				restore := c.noPrelude()
				a.value = c.checkExprExpecting(arm.Value, want)
				restore()
			}
		} else {
			if !arm.IsBlock() {
				c.error(arm.Pos, "an arm of a match statement is a block: write the pattern followed by { … }")
				restore := c.noPrelude()
				c.checkExpr(arm.Value)
				restore()
			} else {
				a.body = c.checkScopeBlockIR(&arm.Body)
			}
		}
		if a.cond == nil && !arm.Else {
			// Every pattern failed to check, and each was reported. Keep the
			// arm a test so that it does not stand in for an else.
			a.cond = boolLiteral(false)
		}
		out = append(out, a)
	}

	if elseAt == nil && known {
		if missing := cov.missing(); missing != "" {
			c.error(m.Pos, "match is not exhaustive: %s", missing)
		} else if len(out) > 0 {
			// Every case is covered, so the last arm is taken unasked.
			out[len(out)-1].cond = nil
		}
	}
	return out
}

// matchSubject checks the subject, and returns it with the function each arm
// reads it through: a read of the local it was bound to, a fresh copy of the
// expression where it is read again, or nil for a headless match.
func (c *checker) matchSubject(m *ast.Match) (ir.Expr, func() ir.Expr) {
	if m.Subject == nil {
		return nil, nil
	}
	restore := c.noPrelude()
	subject := c.checkExpr(m.Subject)
	restore()
	// A zero-argument computed is called, as an if's condition is: matching
	// a function against values says nothing.
	if t := exprType(subject); t != nil && t.Kind == ir.TypeFunc && t.Sig != nil && len(t.Sig.Params) == 0 &&
		t.Sig.Return != nil && t.Sig.Return.Kind != ir.TypeVoid {
		restore := c.noPrelude()
		subject = c.checkExpr(&ast.CallExpr{Pos: *m.Subject.ExprPos(), Func: m.Subject})
		restore()
	}
	if !callsAnything(subject) {
		return subject, func() ir.Expr { return ir.CloneExprSharingDecls(subject) }
	}
	if c.prelude != nil {
		c.matchSeq++
		name := "__match" + strconv.Itoa(c.matchSeq-1)
		typ := exprType(subject)
		sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
		*c.prelude = append(*c.prelude, &ir.LocalVar{Name: name, Type: typ, Init: subject, Sym: sym})
		return subject, func() ir.Expr { return &ir.Ident{Name: name, Sym: sym, Type: typ} }
	}
	c.matchSubjects = append(c.matchSubjects, viewSpread{pos: *m.Subject.ExprPos(), operand: subject})
	return subject, func() ir.Expr { return ir.CloneExprSharingDecls(subject) }
}

// checkMatchPattern checks one pattern and returns the test it makes: the
// subject compared with it, or the pattern itself in a headless match.
func (c *checker) checkMatchPattern(m *ast.Match, p ast.Expr, subject ir.Expr, read func() ir.Expr, cov *matchCoverage) ir.Expr {
	pos := *p.ExprPos()
	if subject == nil {
		cond := c.checkExpr(p)
		t := exprType(cond)
		if wrapped, ret := c.implicitCall(p, t, ir.TypBool); wrapped != nil {
			cond = c.checkExpr(wrapped)
			t = ret
		}
		if t != nil && t.Kind != ir.TypeBool && t.Kind != ir.TypeDyn && t.Kind != ir.TypeInvalid {
			c.error(pos, "a headless match arm is a condition: %s is not bool", t)
			return nil
		}
		return cond
	}
	subjType := exprType(subject)
	want := optionElem(subjType)
	if e := comparedEnum(subjType); e != nil {
		want = e
	}
	pat := c.checkExprExpecting(p, want)
	patType := exprType(pat)
	if patType == nil || patType.Kind == ir.TypeInvalid || subjType == nil || subjType.Kind == ir.TypeInvalid {
		return nil
	}
	if !ir.IsConst(pat) {
		c.error(pos, "a pattern must be a constant: a match compares its subject with values known when the program is built")
		return nil
	}
	if !comparableEq(subjType, patType) {
		c.error(pos, "pattern of type %s cannot match %s", patType, subjType)
		return nil
	}
	if key, label, ok := patternKey(pat); ok {
		if !cov.add(key) {
			c.error(pos, "pattern %s is already matched by an arm above", label)
			return nil
		}
	}
	synth := &ast.BinaryExpr{Pos: pos, Op: ast.BinEq, Left: m.Subject, Right: p}
	return c.finishBinary(synth, read(), pat)
}

// noPrelude marks what is checked next as evaluated only some of the time --
// a branch, a short-circuited operand, a match's own arms -- where binding a
// subject ahead of the statement would evaluate it where the statement might
// not.
func (c *checker) noPrelude() func() {
	saved := c.prelude
	c.prelude = nil
	return func() { c.prelude = saved }
}

// preludeFor makes p the statements an imperative block writes ahead of the
// statement checked next. A view body has no such statements.
func (c *checker) preludeFor(p *[]ir.Stmt) func() {
	saved := c.prelude
	if c.funcDepth > 0 {
		c.prelude = p
	} else {
		c.prelude = nil
	}
	return func() { c.prelude = saved }
}

// bodyPrelude makes p the statements written ahead of an expression body,
// which is the one statement `return body` of a function or a lambda wherever
// that is written, a view body included.
func (c *checker) bodyPrelude(p *[]ir.Stmt) func() {
	saved := c.prelude
	c.prelude = p
	return func() { c.prelude = saved }
}

// reportImpureMatchSubjects refuses a subject read once per arm that writes
// state, which would then write it once per arm. Run after the purity fixed
// point, which is what says.
func (c *checker) reportImpureMatchSubjects() {
	for _, ms := range c.matchSubjects {
		var culprit *ir.Func
		_ = ir.WalkExprs(ms.operand, func(e ir.Expr) error {
			if call, ok := e.(*ir.Call); ok && call.Func != nil && call.Func.Purity >= ir.PurityMutates && culprit == nil {
				culprit = call.Func
			}
			return nil
		})
		if culprit != nil {
			c.error(ms.pos, "a match here reads its subject once for each arm it tests, and %s writes state, so the subject must be free of side effects: bind it to a var and match that", culprit.Name)
		}
	}
}

// callsAnything reports whether evaluating e makes a call, which is what makes
// reading it again cost something.
func callsAnything(e ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(e, func(n ir.Expr) error {
		if _, ok := n.(*ir.Call); ok {
			found = true
		}
		return nil
	})
	return found
}

func orAll(conds []ir.Expr) ir.Expr {
	if len(conds) == 0 {
		return nil
	}
	out := conds[0]
	for _, cond := range conds[1:] {
		out = &ir.Binary{Type: ir.TypBool, Op: ast.BinOr, Left: out, Right: cond}
	}
	return out
}

func boolLiteral(v bool) ir.Expr {
	return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(v)}
}

// patternKey is what two patterns matching the same value share, with the
// spelling a diagnostic names it by.
func patternKey(e ir.Expr) (key, label string, ok bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type == nil {
			return "", "", false
		}
		if x.Type.Kind == ir.TypeNull {
			return "null", "null", true
		}
		label = x.Value + x.Suffix
		if x.Type.Kind == ir.TypeString {
			label = strconv.Quote(x.Value)
		}
		return x.Type.Kind.String() + ":" + x.Value + x.Suffix, label, true
	case *ir.Ident:
		if x.Member != "" {
			return "member:" + x.Member, x.Member, true
		}
		if t := x.Type; t != nil && t.Kind == ir.TypeNull {
			return "null", "null", true
		}
		v, isVar := x.Sym.(*ir.Var)
		if !isVar || !v.IsConst {
			return "", "", false
		}
		if b, ok := constBool(v.Init); ok {
			return "bool:" + strconv.FormatBool(b), x.Name, true
		}
		if k, _, ok := patternKey(v.Init); ok {
			return k, x.Name, true
		}
		return fmt.Sprintf("const:%p", v), x.Name, true
	}
	return "", "", false
}

// constBool folds the comparisons of literals `true` and `false` are declared
// as.
func constBool(e ir.Expr) (bool, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type != nil && x.Type.Kind == ir.TypeBool {
			b, err := strconv.ParseBool(x.Value)
			return b, err == nil
		}
	case *ir.Binary:
		l, lok := x.Left.(*ir.Literal)
		r, rok := x.Right.(*ir.Literal)
		if !lok || !rok {
			return false, false
		}
		switch x.Op {
		case ast.BinEq:
			return l.Value == r.Value, true
		case ast.BinNeq:
			return l.Value != r.Value, true
		}
	}
	return false, false
}

// matchCoverage is what a match's patterns have covered of its subject's
// cases: an enum's members, a bool's two values, and an option's null.
type matchCoverage struct {
	subject *ir.Type
	seen    map[string]bool
}

func newMatchCoverage(subject *ir.Type) *matchCoverage {
	return &matchCoverage{subject: subject, seen: map[string]bool{}}
}

// add records a pattern's key, and reports false when an arm above matched it.
func (m *matchCoverage) add(key string) bool {
	if m.seen[key] {
		return false
	}
	m.seen[key] = true
	return true
}

// missing describes what no pattern covered, or "" when the patterns cover
// every case.
func (m *matchCoverage) missing() string {
	t := m.subject
	if t == nil {
		return "add an else arm"
	}
	var out []string
	if t.Kind == ir.TypeOption {
		if !m.seen["null"] {
			out = append(out, "null")
		}
		t = optionElem(t)
	}
	switch {
	case expectedEnumDef(t) != nil:
		for _, mem := range expectedEnumDef(t).Members {
			if !m.seen["member:"+mem.Name] {
				out = append(out, mem.Name)
			}
		}
	case t.Kind == ir.TypeBool:
		for _, v := range []string{"true", "false"} {
			if !m.seen["bool:"+v] {
				out = append(out, v)
			}
		}
	default:
		return "add an else arm"
	}
	if len(out) == 0 {
		return ""
	}
	return "missing " + strings.Join(out, ", ")
}

// invalidValue stands where a value failed to check and was reported.
func invalidValue() ir.Expr { return &ir.Literal{Type: &ir.Type{}} }
