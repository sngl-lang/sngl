package optimize

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
)

// constantIdents is the set of identifiers that are compile-time constants.
var constantIdents = map[string]bool{
	"PLATFORM": true,
	"LANGUAGE": true,
}

// isConstExpr reports whether the CEL expression references only compile-time
// constant identifiers (PLATFORM, LANGUAGE) and literal values. Comprehensions
// and unknown identifiers cause it to return false.
func isConstExpr(expr celast.Expr) bool {
	switch expr.Kind() {
	case celast.LiteralKind:
		return true
	case celast.IdentKind:
		return constantIdents[expr.AsIdent()]
	case celast.CallKind:
		call := expr.AsCall()
		if call.IsMemberFunction() {
			if !isConstExpr(call.Target()) {
				return false
			}
		}
		for _, arg := range call.Args() {
			if !isConstExpr(arg) {
				return false
			}
		}
		return true
	case celast.SelectKind:
		return isConstExpr(expr.AsSelect().Operand())
	case celast.ListKind:
		for _, elem := range expr.AsList().Elements() {
			if !isConstExpr(elem) {
				return false
			}
		}
		return true
	case celast.MapKind:
		for _, entry := range expr.AsMap().Entries() {
			if !isConstExpr(entry.AsMapEntry().Key()) {
				return false
			}
			if !isConstExpr(entry.AsMapEntry().Value()) {
				return false
			}
		}
		return true
	case celast.ComprehensionKind:
		return false
	default:
		return false
	}
}

// evalConst evaluates a constant CEL expression and returns the result.
// It returns (nil, false) if the expression cannot be evaluated.
func evalConst(env *cel.Env, expr *ast.Expr, vars map[string]any) (any, bool) {
	celAst := expr.AST
	if celAst == nil {
		return nil, false
	}
	prg, err := env.Program(celAst)
	if err != nil {
		return nil, false
	}
	out, _, err := prg.Eval(vars)
	if err != nil {
		return nil, false
	}
	return out.Value(), true
}
