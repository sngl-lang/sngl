package ir

import (
	"slices"

	"duckfam.us/sngl/ast"
)

// MatchArms is what a match statement and a match expression share: the
// subject, nil for a headless match, and the arms in the order they are tried.
//
// An arm is tried by comparing the subject with each of its patterns with
// `==`, or, headless, by testing each pattern as a condition; the first arm
// one of whose patterns holds is taken. An else arm is taken unasked, and so
// is the last arm when Exhaustive says the arms above it and it cover every
// value the subject can hold.
//
// The subject is cheap to read again: the checker binds one that calls
// anything to a local ahead of the statement wherever a statement can hold
// one, and refuses one that writes state anywhere else. So a lowering may
// compare a fresh copy of it per pattern (Chain, TernaryChain) and a target
// that switches on it evaluates it once either way.
type MatchArms struct {
	Subject    Expr
	Arms       []*MatchArm
	Exhaustive bool
}

// MatchArm is one arm of a match: its patterns, or Else, and what it does --
// Body in a statement, Value in an expression.
type MatchArm struct {
	Pos ast.Pos
	// Patterns are compared with the subject, or are the conditions of a
	// headless match. Empty for an else arm, and for an arm every pattern of
	// which failed to check, which then is never taken.
	Patterns []Expr
	Else     bool
	Body     []Stmt
	Value    Expr
}

// Match is a match statement.
type Match struct {
	AST *ast.MatchStmt
	MatchArms
	// Instances is NodeInst.Instances, for a match at the root of a spliced
	// body, as If.Instances is.
	Instances []string `json:",omitempty"`
	// FromValue marks the match passTernary writes for a match expression,
	// as If.FromTernary marks the if it writes for a ternary: its arms assign
	// a sibling temp and render nothing.
	FromValue bool `json:"-"`
}

// ComputesValue reports whether s is a statement passTernary wrote to compute
// an expression's value into a temp -- an if for a ternary, a match for a
// match expression -- which a view emits as imperative code rather than as a
// conditional over what it renders.
func ComputesValue(s Stmt) bool {
	switch n := s.(type) {
	case *If:
		return n.FromTernary
	case *Match:
		return n.FromValue
	}
	return false
}

// MatchExpr is a match expression, whose value is the taken arm's Value.
type MatchExpr struct {
	AST  *ast.MatchExpr
	Type *Type
	MatchArms
}

func (*Match) stmtNode()     {}
func (*MatchExpr) exprNode() {}

func (x *MatchExpr) ExprType() *Type { return x.Type }

// Unasked reports whether arm i is taken without testing it: an else, or the
// last arm of an exhaustive match.
func (m *MatchArms) Unasked(i int) bool {
	return m.Arms[i].Else || (m.Exhaustive && i == len(m.Arms)-1)
}

// Heads are what a match evaluates to choose an arm: the subject, where it
// has one, and every arm's patterns -- the conditions of a headless match.
func (m *MatchArms) Heads() []Expr {
	var out []Expr
	if m.Subject != nil {
		out = append(out, m.Subject)
	}
	for _, arm := range m.Arms {
		out = append(out, arm.Patterns...)
	}
	return out
}

// Headless reports whether the arms are conditions rather than values the
// subject is compared with.
func (m *MatchArms) Headless() bool { return m.Subject == nil }

// Test is the condition that selects arm i: the subject compared with each
// pattern, joined by `||`, or the patterns themselves in a headless match. An
// arm with no pattern that is not taken unasked is never taken, and tests the
// literal false.
func (m *MatchArms) Test(i int) Expr {
	var out Expr
	for _, p := range m.Arms[i].Patterns {
		var t Expr = p
		if m.Subject != nil {
			t = &Binary{Type: TypBool, Op: ast.BinEq, Left: CloneExprSharingDecls(m.Subject), Right: p}
		}
		if out == nil {
			out = t
		} else {
			out = &Binary{Type: TypBool, Op: ast.BinOr, Left: out, Right: t}
		}
	}
	if out == nil {
		return &Literal{Type: TypBool, Value: "false"}
	}
	return out
}

// ArmBlocks are the arms' bodies, in order: the blocks a match statement
// carries a question into, as an if's body and else are.
func (m *Match) ArmBlocks() []*[]Stmt {
	out := make([]*[]Stmt, len(m.Arms))
	for i, arm := range m.Arms {
		out[i] = &arm.Body
	}
	return out
}

// Chain is the match statement as the if/else-if chain it means: each arm an
// `if` on its test with the arms after it in its else, and an arm taken
// unasked the else itself. A sole arm taken unasked is an `if true`, which
// keeps it a block of its own. The arms' bodies and patterns are shared, not
// copied; the subject is copied per test.
func (m *Match) Chain() []Stmt {
	var chain []Stmt
	for i, arm := range slices.Backward(m.Arms) {
		if m.Unasked(i) {
			if i == 0 {
				chain = []Stmt{&If{AST: &ast.IfStmt{Pos: arm.Pos}, Cond: &Literal{Type: TypBool, Value: "true"}, Body: arm.Body}}
				continue
			}
			chain = arm.Body
			continue
		}
		chain = []Stmt{&If{AST: &ast.IfStmt{Pos: arm.Pos}, Cond: m.Test(i), Body: arm.Body, Else: chain}}
	}
	if len(chain) == 1 {
		if s, ok := chain[0].(*If); ok && len(m.Instances) > 0 {
			s.Instances = m.Instances
		}
	}
	return chain
}

// TernaryChain is the match expression as the chain of ternaries it means.
// The last arm is the value when no arm above it holds, tested or not: a
// checked match is exhaustive, so that arm is taken unasked.
func (m *MatchExpr) TernaryChain() Expr {
	if len(m.Arms) == 0 {
		return &Literal{Type: m.Type}
	}
	out := m.Arms[len(m.Arms)-1].Value
	for i := len(m.Arms) - 2; i >= 0; i-- {
		arm := m.Arms[i]
		out = &Ternary{AST: &ast.TernaryExpr{Pos: arm.Pos}, Type: m.Type, Cond: m.Test(i), Then: arm.Value, Else: out}
	}
	return out
}

// Switchable reports whether a target may compare the subject with its
// patterns by the host's own switch: a headless match, or a subject whose
// SNGL `==` is the host's equality on every language -- a bool, a number, a
// string or an enum. An option of one of those is switchable only where
// options says the language's switch compares it as `==` does: Kotlin's
// `when` takes `null ->` beside the element's values, while Go's switch
// would compare the option's pointer rather than what it points at. Anything else -- a unit, a struct -- is compared the way `==` is
// written for it, which is the if chain.
func (m *MatchArms) Switchable(options bool) bool {
	if m.Subject == nil {
		return true
	}
	t := m.Subject.ExprType()
	if t == nil {
		return false
	}
	if t.Kind == TypeOption && len(t.Elems) == 1 {
		if !options {
			return false
		}
		t = t.Elems[0]
	}
	switch {
	case t.Kind == TypeBool, t.Kind == TypeString, t.Kind == TypeEnum:
		return true
	case t.IsNumeric():
		return true
	}
	return false
}

// BreaksOut reports whether an arm holds a `break` that leaves a loop around
// the match. Go's and JavaScript's switch take a bare break as their own, so
// such a break names the loop it leaves (Break.Label).
func (m *Match) BreaksOut() bool {
	found := false
	var walk func([]Stmt)
	walk = func(stmts []Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *Break:
				found = true
			case *For:
				// A break inside is the inner loop's.
			default:
				for _, b := range TransparentBlocks(n) {
					walk(*b)
				}
			}
		}
	}
	for _, arm := range m.Arms {
		walk(arm.Body)
	}
	return found
}

// Over is a copy of m whose arms hold what each arm's body becomes under f,
// handed the arm's index, for a walk that rebuilds the control flow around
// what it keeps. The subject and patterns are shared.
func (m *Match) Over(f func(i int, body []Stmt) []Stmt) *Match {
	out := *m
	out.Arms = make([]*MatchArm, len(m.Arms))
	for i, arm := range m.Arms {
		a := *arm
		a.Body = f(i, arm.Body)
		out.Arms[i] = &a
	}
	return &out
}

// Taken is the condition under which arm i is the one taken: its own test,
// and none of the tests above it. An arm taken unasked is only the second.
func (m *MatchArms) Taken(i int) Expr {
	var out Expr
	and := func(e Expr) {
		if out == nil {
			out = e
			return
		}
		out = &Binary{Type: TypBool, Op: ast.BinAnd, Left: out, Right: e}
	}
	for j := range i {
		and(&Unary{Type: TypBool, Op: ast.UnaryNot, Operand: m.Test(j)})
	}
	if !m.Unasked(i) {
		and(m.Test(i))
	}
	if out == nil {
		return &Literal{Type: TypBool, Value: "true"}
	}
	return out
}
