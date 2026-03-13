package checker

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
)

// BuildCelEnv creates a CEL environment from scope variables and SNGL built-in functions.
func BuildCelEnv(scope *Scope, structs ...*ast.StructDef) (*cel.Env, error) {
	opts := scope.EnvOpts()

	// Struct constructor functions
	for _, sd := range structs {
		var paramTypes []*cel.Type
		for _, f := range sd.Fields {
			paramTypes = append(paramTypes, TypeHintToCelType(f.Type))
		}
		overloadID := sd.Name
		for _, f := range sd.Fields {
			overloadID += "_" + f.Type
		}
		opts = append(opts, cel.Function(sd.Name,
			cel.Overload(overloadID, paramTypes, cel.DynType),
		))
	}

	// Built-in mutation functions
	opts = append(opts,
		cel.Function("set",
			cel.Overload("set_dyn_dyn", []*cel.Type{cel.DynType, cel.DynType}, MutationType),
		),
		cel.Function("toggle",
			cel.Overload("toggle_dyn", []*cel.Type{cel.DynType}, MutationType),
		),
		cel.Function("push",
			cel.Overload("push_list_dyn", []*cel.Type{cel.ListType(cel.DynType), cel.DynType}, MutationType),
		),
		cel.Function("remove",
			cel.Overload("remove_list_int", []*cel.Type{cel.ListType(cel.DynType), cel.IntType}, MutationType),
		),
		cel.Function("emit",
			cel.Overload("emit_string_dyn", []*cel.Type{cel.StringType, cel.DynType}, MutationType),
		),
	)

	return cel.NewEnv(opts...)
}

// checkExpr re-parses and type-checks a CEL expression within the given scope.
// On success it replaces expr.AST with the checked AST and returns the output type.
func checkExpr(scope *Scope, expr *ast.Expr, structs ...*ast.StructDef) (*cel.Type, error) {
	if expr.CEL == "" {
		return nil, fmt.Errorf("not a CEL expression")
	}
	env, err := BuildCelEnv(scope, structs...)
	if err != nil {
		return nil, fmt.Errorf("building CEL env: %w", err)
	}
	celAst, iss := env.Parse(expr.CEL)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("CEL parse %q: %w", expr.CEL, iss.Err())
	}
	celAst, iss = env.Check(celAst)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("CEL check %q: %w", expr.CEL, iss.Err())
	}
	expr.AST = celAst
	return celAst.OutputType(), nil
}
