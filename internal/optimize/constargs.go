package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A const parameter or prop is folded by contract. The checker has held every
// argument bound to one to ir.IsConst; this is where that becomes a literal --
// an ir.Literal, or a list, struct or map literal whose leaves are literals --
// which is the one form a backend reading a descriptor has to handle. An
// argument that does not fold (a `js:` const call with no evaluator, a const
// recursion that does not settle) is a build error at the argument, rather
// than a primitive handed an expression its codegen has no way to read.
//
// An argument still naming a const parameter is left alone: it sits in a body
// that has not been substituted yet, and the call site that substitutes it is
// where the value arrives.

// foldConstArg folds an argument bound to a const parameter all the way to a
// literal and reports one that does not get there.
func foldConstArg(value ir.Expr, pos ast.Pos, what string, ctx *evalCtx) ir.Expr {
	folded := foldOwned(value, ctx)
	if IsLiteralTree(folded) || namesConstParam(folded) {
		return folded
	}
	if ctx != nil && ctx.err == nil {
		at := ""
		if pos.IsSet() {
			at = pos.String() + ": "
		}
		ctx.err = fmt.Errorf("%s%s is const, and its argument does not fold to a literal at build time", at, what)
	}
	return folded
}

// IsLiteralTree reports whether e is a literal all the way down: what a const
// argument is after folding. A conversion of one counts -- the checker wraps an
// argument in the coercion its parameter asks for, and a coerced literal is
// still one value the build knows.
func IsLiteralTree(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		// A bare enum member is its declaration's constant.
		return x.Member != ""
	case *ir.Select:
		// `Color.red`: a member of an enum reached through its type.
		if id, ok := x.Operand.(*ir.Ident); ok {
			_, isEnum := id.Sym.(*ir.EnumDef)
			return isEnum
		}
		return false
	case *ir.Conversion:
		return IsLiteralTree(x.Operand)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if !IsLiteralTree(el) {
				return false
			}
		}
		return true
	case *ir.StructLit:
		for _, f := range x.Fields {
			if !IsLiteralTree(f.Value) {
				return false
			}
		}
		return true
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			if !IsLiteralTree(kv.Key) || !IsLiteralTree(kv.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// namesConstParam reports whether e reads a const parameter: a value that is a
// constant at every call site, and not yet the one at this site.
func namesConstParam(e ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if id, ok := x.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && p.Const {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}

// constPropOf is comp's prop name when it is declared const, or nil.
func constPropOf(comp *ir.Component, name string) *ir.Prop {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p.Name == name && p.Const {
			return p
		}
	}
	return nil
}

// constParamOf is the const parameter a call argument is bound to, or nil.
func constParamOf(call *ir.Call, i int) *ir.Param {
	if call == nil || call.Func == nil || i >= len(call.Args) {
		return nil
	}
	name := call.Args[i].Name
	if name == "" {
		return nil
	}
	for _, p := range call.Func.Params {
		if p.Name == name && p.Const {
			return p
		}
	}
	return nil
}

func nodePos(n *ir.NodeInst) ast.Pos {
	if n == nil || n.AST == nil {
		return ast.Pos{}
	}
	switch s := n.AST.(type) {
	case *ast.VisualNode:
		return s.Pos
	case *ast.CallStmt:
		return s.Pos
	}
	return ast.Pos{}
}

func callPos(c *ir.Call) ast.Pos {
	if c == nil || c.AST == nil {
		return ast.Pos{}
	}
	return c.AST.Pos
}
