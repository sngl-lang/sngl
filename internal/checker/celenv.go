package checker

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
)

// BuildCelEnv creates a CEL environment from scope variables and SNGL built-in functions.
func BuildCelEnv(scope *Scope, structs []*ast.StructDef, protoDescs []any) (*cel.Env, error) {
	opts := scope.EnvOpts()

	// Built-in constants (values injected at compile time)
	opts = append(opts,
		cel.Variable("LANGUAGE", cel.StringType),
		cel.Variable("PLATFORM", cel.StringType),
	)

	// Proto type descriptors (for struct literal syntax like User{ name: 'val' })
	if len(protoDescs) > 0 {
		opts = append(opts, cel.TypeDescs(protoDescs...))
	}

	// Struct constructor functions (for function call syntax like User("val", 25))
	for _, sd := range structs {
		var paramTypes []*cel.Type
		for _, f := range sd.Fields {
			paramTypes = append(paramTypes, TypeHintToCelType(f.Type))
		}
		var overloadID strings.Builder
		overloadID.WriteString(sd.Name)
		for _, f := range sd.Fields {
			overloadID.WriteString("_" + f.Type)
		}
		opts = append(opts, cel.Function(sd.Name,
			cel.Overload(overloadID.String(), paramTypes, cel.DynType),
		))
	}

	// Special type constructor functions
	opts = append(opts,
		cel.Function("Color",
			cel.Overload("Color_string", []*cel.Type{cel.StringType}, ColorType),
		),
		cel.Function("Date",
			cel.Overload("Date_string", []*cel.Type{cel.StringType}, DateType),
			cel.Overload("Date_int_int_int", []*cel.Type{cel.IntType, cel.IntType, cel.IntType}, DateType),
		),
		cel.Function("Time",
			cel.Overload("Time_string", []*cel.Type{cel.StringType}, TimeType),
			cel.Overload("Time_int_int_int", []*cel.Type{cel.IntType, cel.IntType, cel.IntType}, TimeType),
		),
		cel.Function("DateTime",
			cel.Overload("DateTime_string", []*cel.Type{cel.StringType}, DateTimeType),
		),
		cel.Function("Duration",
			cel.Overload("Duration_string", []*cel.Type{cel.StringType}, DurationType),
		),
	)

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
func checkExpr(scope *Scope, expr *ast.Expr, structs []*ast.StructDef, protoDescs []any) (*cel.Type, error) {
	if expr.CEL == "" {
		return nil, fmt.Errorf("not a CEL expression")
	}
	env, err := BuildCelEnv(scope, structs, protoDescs)
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
