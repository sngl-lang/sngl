package ir

import (
	"slices"
	"sync/atomic"

	"git.duckfam.us/jonathan/sngl/ast"
)

var spreadSeq atomic.Int64

// NewSpreadID names one spread site, for the Selects that read its fields.
func NewSpreadID() int { return int(spreadSeq.Add(1)) }

// SpreadGroup is the field reads of one spread site within a statement.
// Operand is the expression the site spreads, as the first read holds it.
type SpreadGroup struct {
	ID      int
	Operand Expr
	Selects []*Select
}

// StatementSpreads returns the spread sites s evaluates unconditionally, each
// operand's own sites ahead of it. A site under `&&`, `||`, a ternary branch
// or a lambda is left out, as is a condition loop's head: binding its operand
// once before s would evaluate it where s might not, or once where s asks
// every iteration.
func StatementSpreads(s Stmt) []SpreadGroup {
	var c spreadCollector
	for _, e := range spreadOwnExprs(s) {
		c.walk(e, false)
	}
	var out []SpreadGroup
	for _, g := range c.groups {
		if !g.conditional {
			out = append(out, g.SpreadGroup)
		}
	}
	return out
}

func spreadOwnExprs(s Stmt) []Expr {
	switch n := s.(type) {
	case *Assign:
		return []Expr{n.Value}
	case *LocalVar:
		return []Expr{n.Init}
	case *Return:
		return []Expr{n.Value}
	case *CallStmt:
		if n.Call == nil {
			return nil
		}
		return []Expr{n.Call}
	case *If:
		return []Expr{n.Cond}
	case *For:
		if DeriveIterKind(n) == IterCondition {
			return nil
		}
		return []Expr{n.Iter}
	case *Emit:
		out := make([]Expr, 0, len(n.Args))
		for _, a := range n.Args {
			out = append(out, a.Value)
		}
		return out
	}
	return nil
}

type spreadGroupAcc struct {
	SpreadGroup
	conditional bool
}

type spreadCollector struct {
	byID   map[int]int
	groups []spreadGroupAcc
}

func (c *spreadCollector) walk(e Expr, cond bool) {
	if e == nil {
		return
	}
	_ = Walk(e, func(n Node) error {
		switch x := n.(type) {
		case Stmt, *Lambda:
			return SkipDir
		case *Binary:
			if x.Op == ast.BinAnd || x.Op == ast.BinOr {
				c.walk(x.Left, cond)
				c.walk(x.Right, true)
				return SkipDir
			}
		case *Ternary:
			c.walk(x.Cond, cond)
			c.walk(x.Then, true)
			c.walk(x.Else, true)
			return SkipDir
		case *Select:
			if x.Spread != 0 {
				c.walk(x.Operand, cond)
				c.add(x, cond)
				return SkipDir
			}
		}
		return nil
	})
}

func (c *spreadCollector) add(x *Select, cond bool) {
	if c.byID == nil {
		c.byID = map[int]int{}
	}
	i, ok := c.byID[x.Spread]
	if !ok {
		i = len(c.groups)
		c.byID[x.Spread] = i
		c.groups = append(c.groups, spreadGroupAcc{SpreadGroup: SpreadGroup{ID: x.Spread, Operand: x.Operand}})
	}
	g := &c.groups[i]
	g.conditional = g.conditional || cond
	if !slices.Contains(g.Selects, x) {
		g.Selects = append(g.Selects, x)
	}
}
