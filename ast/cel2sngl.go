package ast

import (
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

// CELToSNGL translates a CEL expression AST into a native SNGL expression Node.
func CELToSNGL(e celast.Expr) Node {
	if e == nil {
		return nil
	}
	switch e.Kind() {
	case celast.LiteralKind:
		return celLiteral(e)
	case celast.IdentKind:
		return &IdentExpr{Name: e.AsIdent()}
	case celast.SelectKind:
		sel := e.AsSelect()
		return &SelectExpr{
			Operand: CELToSNGL(sel.Operand()),
			Field:   sel.FieldName(),
		}
	case celast.CallKind:
		return celCall(e)
	case celast.ListKind:
		return celList(e)
	case celast.StructKind:
		return celStruct(e)
	default:
		return &LiteralExpr{Kind: LiteralNull}
	}
}

func celLiteral(e celast.Expr) Node {
	v := e.AsLiteral()
	switch v.Type() {
	case types.StringType:
		return &LiteralExpr{Value: v.Value(), Kind: LiteralString}
	case types.IntType:
		return &LiteralExpr{Value: v.Value(), Kind: LiteralInt}
	case types.DoubleType:
		return &LiteralExpr{Value: v.Value(), Kind: LiteralFloat}
	case types.BoolType:
		return &LiteralExpr{Value: v.Value(), Kind: LiteralBool}
	case types.NullType:
		return &LiteralExpr{Kind: LiteralNull}
	default:
		return &LiteralExpr{Value: v.Value(), Kind: LiteralString}
	}
}

var celBinaryOps = map[string]BinaryOp{
	operators.Add:           BinAdd,
	operators.Subtract:      BinSub,
	operators.Multiply:      BinMul,
	operators.Divide:        BinDiv,
	operators.Modulo:        BinMod,
	operators.Equals:        BinEq,
	operators.NotEquals:     BinNeq,
	operators.Less:          BinLt,
	operators.LessEquals:    BinLte,
	operators.Greater:       BinGt,
	operators.GreaterEquals: BinGte,
	operators.LogicalAnd:    BinAnd,
	operators.LogicalOr:     BinOr,
}

func celCall(e celast.Expr) Node {
	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	// Binary operators
	if op, ok := celBinaryOps[fn]; ok && len(args) == 2 {
		return &BinaryExpr{
			Op:    op,
			Left:  CELToSNGL(args[0]),
			Right: CELToSNGL(args[1]),
		}
	}

	// Ternary
	if fn == operators.Conditional && len(args) == 3 {
		return &TernaryExpr{
			Cond: CELToSNGL(args[0]),
			Then: CELToSNGL(args[1]),
			Else: CELToSNGL(args[2]),
		}
	}

	// Unary not
	if fn == operators.LogicalNot && len(args) == 1 {
		return &UnaryExpr{Op: UnaryNot, Operand: CELToSNGL(args[0])}
	}

	// Unary negate
	if fn == operators.Negate && len(args) == 1 {
		return &UnaryExpr{Op: UnaryNeg, Operand: CELToSNGL(args[0])}
	}

	// Index operator
	if fn == operators.Index && len(args) == 2 {
		return &IndexExpr{
			Operand: CELToSNGL(args[0]),
			Index:   CELToSNGL(args[1]),
		}
	}

	// Mutation functions → statement nodes
	switch fn {
	case "set":
		if len(args) == 2 {
			return &AssignStmt{
				Target: CELToSNGL(args[0]),
				Op:     AssignSet,
				Value:  CELToSNGL(args[1]),
			}
		}
	case "toggle":
		if len(args) == 1 {
			return &ToggleStmt{Target: CELToSNGL(args[0])}
		}
	case "push":
		if len(args) == 2 {
			return &MethodExpr{
				Receiver: CELToSNGL(args[0]),
				Method:   "push",
				Args:     []Node{CELToSNGL(args[1])},
			}
		}
	case "remove":
		if len(args) == 2 {
			return &MethodExpr{
				Receiver: CELToSNGL(args[0]),
				Method:   "remove",
				Args:     []Node{CELToSNGL(args[1])},
			}
		}
	case "emit":
		if len(args) >= 1 {
			// First arg is the event name (string literal)
			name := ""
			if args[0].Kind() == celast.LiteralKind {
				if s, ok := args[0].AsLiteral().Value().(string); ok {
					name = s
				}
			}
			var emitArgs []Node
			for _, a := range args[1:] {
				emitArgs = append(emitArgs, CELToSNGL(a))
			}
			return &EmitStmt{Name: name, Args: emitArgs}
		}
	}

	// Member function calls
	if call.IsMemberFunction() {
		target := CELToSNGL(call.Target())
		snglArgs := make([]Node, len(args))
		for i, a := range args {
			snglArgs[i] = CELToSNGL(a)
		}
		return &MethodExpr{
			Receiver: target,
			Method:   fn,
			Args:     snglArgs,
		}
	}

	// Free function calls (string, int, float, size, etc.)
	snglArgs := make([]Node, len(args))
	for i, a := range args {
		snglArgs[i] = CELToSNGL(a)
	}
	return &CallExpr{
		Func: fn,
		Args: snglArgs,
	}
}

func celList(e celast.Expr) Node {
	list := e.AsList()
	elems := list.Elements()

	// A CEL list of mutations → StmtBlock
	if len(elems) > 0 && isMutationExpr(elems[0]) {
		stmts := make([]Node, len(elems))
		for i, el := range elems {
			stmts[i] = CELToSNGL(el)
		}
		return &StmtBlock{Stmts: stmts}
	}

	nodes := make([]Node, len(elems))
	for i, el := range elems {
		nodes[i] = CELToSNGL(el)
	}
	return &ListExpr{Elements: nodes}
}

func celStruct(e celast.Expr) Node {
	s := e.AsStruct()
	fields := make([]StructFieldLit, 0, len(s.Fields()))
	for _, f := range s.Fields() {
		sf := f.AsStructField()
		fields = append(fields, StructFieldLit{
			Name:  sf.Name(),
			Value: CELToSNGL(sf.Value()),
		})
	}
	return &StructExpr{
		Name:   s.TypeName(),
		Fields: fields,
	}
}

// isMutationExpr checks if a CEL expression is a mutation call (set, toggle, push, remove).
func isMutationExpr(e celast.Expr) bool {
	if e.Kind() != celast.CallKind {
		return false
	}
	switch e.AsCall().FunctionName() {
	case "set", "toggle", "push", "remove", "emit":
		return true
	}
	return false
}
