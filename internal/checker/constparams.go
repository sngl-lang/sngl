package checker

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A `const` parameter or prop says every call site passes a compile-time
// value, and the test of one is ir.IsConst, the test `const(...)` applies.
//
// The check waits, as a `const(...)` assertion does: whether a call is a
// constant depends on the callee being declared const, and a callee may be
// registered after the call that reaches it. So each argument bound to a const
// parameter is recorded where it is bound, positioned at the argument, and
// judged by runConstArgChecks at the end of the check.

// constArg is one argument bound to a const parameter or prop: where it was
// written, what it checked to, and the parameter it was bound to, spelled for
// the diagnostic.
type constArg struct {
	pos   ast.Pos
	value ir.Expr
	what  string
}

// deferConstArg records an argument bound to a const parameter.
func (c *checker) deferConstArg(pos ast.Pos, value ir.Expr, what string) {
	if value == nil {
		return
	}
	c.constArgs = append(c.constArgs, constArg{pos: pos, value: value, what: what})
}

// deferConstProp records an argument bound to comp's prop name when that prop
// is const. A name no prop declares, or a wildcard's, is left to the checks
// that report it.
func (c *checker) deferConstProp(pos ast.Pos, value ir.Expr, comp *ir.Component, name string) {
	if comp == nil {
		return
	}
	for _, p := range comp.Props {
		if p.Name == name && p.Const {
			c.deferConstArg(pos, value, constPropLabel(name, comp))
			return
		}
	}
}

// runConstArgChecks judges every argument deferConstArg recorded. A component
// body is read more than once, so one argument may have been recorded twice;
// the diagnostic is reported once.
func (c *checker) runConstArgChecks() { c.runConstArgChecksFrom(0) }

// runConstArgChecksFrom judges the arguments recorded since mark and drops
// them, leaving earlier ones for whoever recorded them. A library package loads
// in the middle of a program's check, on the same checker; its own arguments
// are judged when it finishes loading, and the program's are still pending.
func (c *checker) runConstArgChecksFrom(mark int) {
	if mark > len(c.constArgs) {
		return
	}
	seen := map[string]bool{}
	for _, a := range c.constArgs[mark:] {
		if ir.IsConst(a.value) {
			continue
		}
		msg := fmt.Sprintf("%s is const: %s", a.what, nonConstReason(a.value))
		key := a.pos.String() + msg
		if seen[key] {
			continue
		}
		seen[key] = true
		c.error(a.pos, "%s", msg)
	}
	c.constArgs = c.constArgs[:mark]
}

// checkConstProp holds a `const` prop to the two forms it cannot share a
// declaration with. A binding writes back to the caller's value, which a
// compile-time value has none of; and #[construct] is what const already says
// -- the instance reads the value once -- so writing both says it twice.
func (c *checker) checkConstProp(pd ast.Param, prop *ir.Prop) {
	if !prop.Const {
		return
	}
	if prop.Bidirectional {
		c.error(pd.Pos, "prop %q cannot be const and a binding: a binding writes back to the caller's value, and a compile-time value has none", pd.Name)
	}
	if prop.Construct {
		c.error(pd.Pos, "prop %q is const, which already says it is read once while the instance is built; drop #[construct]", pd.Name)
		prop.Construct = false
	}
}

// constParamLabel spells a const parameter for a diagnostic.
func constParamLabel(name string) string {
	return "param " + strconv.Quote(name)
}

// constPropLabel spells a const prop for a diagnostic.
func constPropLabel(name string, comp *ir.Component) string {
	if comp == nil || comp.Name == "" {
		return "prop " + strconv.Quote(name)
	}
	return "prop " + strconv.Quote(name) + " of " + comp.DisplayName()
}

// nonConstReason names the first thing in e that keeps it from being a
// compile-time value, for the diagnostic: the var it reads, the parameter that
// is not const, the call to a function that is not. It walks the same shape
// ir.IsConst does and stops where IsConst would say no.
func nonConstReason(e ir.Expr) string {
	if r := findNonConst(e); r != "" {
		return r
	}
	return "the value is not a constant expression"
}

func findNonConst(e ir.Expr) string {
	if e == nil || ir.IsConst(e) {
		return ""
	}
	switch x := e.(type) {
	case *ir.Ident:
		switch s := x.Sym.(type) {
		case *ir.Var:
			return strconv.Quote(s.Name) + " is a var"
		case *ir.Param:
			return strconv.Quote(s.Name) + " is a parameter that is not const"
		case *ir.LoopVar:
			return strconv.Quote(s.Name) + " is a loop variable"
		}
		return strconv.Quote(x.Name) + " is not a constant"
	case *ir.Binary:
		return firstNonConst(x.Left, x.Right)
	case *ir.Unary:
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return "a reference is never a constant"
		}
		return findNonConst(x.Operand)
	case *ir.Ternary:
		return firstNonConst(x.Cond, x.Then, x.Else)
	case *ir.Call:
		for _, a := range x.Args {
			if r := findNonConst(a.Value); r != "" {
				return r
			}
		}
		if r := findNonConst(x.Receiver); r != "" {
			return r
		}
		if x.Func != nil {
			return x.Func.Name + " is not const"
		}
		return "the call is not to a const func"
	case *ir.Conversion:
		return findNonConst(x.Operand)
	case *ir.Select:
		return findNonConst(x.Operand)
	case *ir.Index:
		return firstNonConst(x.Operand, x.Idx)
	case *ir.ListLit:
		return firstNonConst(x.Elems...)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if r := findNonConst(f.Value); r != "" {
				return r
			}
		}
	case *ir.Spread:
		return findNonConst(x.Operand)
	case *ir.Closure:
		return "a closure is never a constant"
	}
	return ""
}

func firstNonConst(es ...ir.Expr) string {
	for _, e := range es {
		if r := findNonConst(e); r != "" {
			return r
		}
	}
	return ""
}
