package compiler

import (
	celast "github.com/google/cel-go/common/ast"
)

// translateMutation converts a CEL mutation call into Go assignment statements.
// For list expressions [set(a,1), toggle(b)], each element is translated separately.
func (ec *exprContext) translateMutation(e celast.Expr) []string {
	if e.Kind() == celast.ListKind {
		list := e.AsList()
		var stmts []string
		for _, el := range list.Elements() {
			stmts = append(stmts, ec.translateMutation(el)...)
		}
		return stmts
	}

	if e.Kind() != celast.CallKind {
		return []string{"// unsupported mutation expression"}
	}

	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	switch fn {
	case "set":
		if len(args) == 2 {
			target := ec.translateMutationTarget(args[0])
			value := ec.translateExpr(args[1])
			return []string{target + " = " + value}
		}
	case "toggle":
		if len(args) == 1 {
			target := ec.translateMutationTarget(args[0])
			return []string{target + " = !" + target}
		}
	case "push":
		if len(args) == 2 {
			target := ec.translateMutationTarget(args[0])
			value := ec.translateExpr(args[1])
			return []string{target + " = append(" + target + ", " + value + ")"}
		}
	case "remove":
		if len(args) == 2 {
			target := ec.translateMutationTarget(args[0])
			idx := ec.translateExpr(args[1])
			return []string{target + " = append(" + target + "[:" + idx + "], " + target + "[" + idx + "+1:]...)"}
		}
	}

	return []string{"// unsupported mutation: " + fn}
}

// translateMutationTarget translates a CEL expression used as the first argument
// to a mutation function (the "target" being assigned to). Identifiers that are
// model fields get prefixed with "m." and exported.
func (ec *exprContext) translateMutationTarget(e celast.Expr) string {
	if e.Kind() == celast.IdentKind {
		name := e.AsIdent()
		if ec.modelFields[name] {
			return "m." + exportName(name)
		}
		return name
	}
	if e.Kind() == celast.SelectKind {
		sel := e.AsSelect()
		operand := ec.translateMutationTarget(sel.Operand())
		return operand + "." + exportName(sel.FieldName())
	}
	return ec.translateExpr(e)
}
