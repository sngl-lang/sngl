package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
	"maps"
)

// exprType extracts the resolved type from an ir.Expr, returning TypDyn for nil.
func exprType(e ir.Expr) *ir.Type {
	if e == nil {
		return TypDyn
	}
	if t := e.ExprType(); t != nil {
		return t
	}
	return TypDyn
}

// checkExpr infers the type of an expression and returns its IR form.
func (c *checker) checkExpr(e ast.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	return c.inferExpr(e)
}

// checkExprExpecting checks an expression with an expected type hint.
// When the expected type is an enum, bare member names resolve automatically.
func (c *checker) checkExprExpecting(e ast.Expr, expected *ir.Type) ir.Expr {
	saved := c.expected
	c.expected = expected
	t := c.checkExpr(e)
	c.expected = saved
	return t
}

// inferExpr dispatches on expression type to infer its type.
func (c *checker) inferExpr(e ast.Expr) ir.Expr {
	switch x := e.(type) {
	case *ast.LiteralExpr:
		return c.inferLiteral(x)
	case *ast.UnitLiteral:
		return c.inferUnitLiteral(x)
	case *ast.IdentExpr:
		return c.inferIdent(x)
	case *ast.BinaryExpr:
		return c.inferBinary(x)
	case *ast.UnaryExpr:
		return c.inferUnary(x)
	case *ast.TernaryExpr:
		return c.inferTernary(x)
	case *ast.CallExpr:
		return c.inferCall(x)
	case *ast.SelectExpr:
		return c.inferSelect(x)
	case *ast.IndexExpr:
		return c.inferIndex(x)
	case *ast.StructExpr:
		return c.inferStructLit(x)
	case *ast.ListExpr:
		return c.inferListLit(x)
	case *ast.InterpolationExpr:
		return c.inferInterpolation(x)
	case *ast.LambdaExpr:
		return c.inferLambda(x)
	case *ast.SpreadExpr:
		operand := c.checkExpr(x.Operand)
		return &ir.Spread{AST: x, Type: exprType(operand), Operand: operand}
	case *ast.ParenExpr:
		return c.checkExpr(x.Inner)
	case *ast.ElementRefExpr:
		return &ir.Ident{Type: TypDyn, Name: x.Name, IsElementRef: true}
	case *ast.EventRefExpr:
		return c.inferEventRef(x)
	default:
		return &ir.Ident{Type: TypDyn}
	}
}

func (c *checker) inferLiteral(x *ast.LiteralExpr) ir.Expr {
	var typ *ir.Type
	switch x.Kind {
	case ast.LiteralInt:
		typ = TypInt
	case ast.LiteralFloat:
		typ = TypFloat
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		typ = TypString
	case ast.LiteralBool:
		typ = TypBool
	case ast.LiteralNull:
		typ = TypNull
	case ast.LiteralColor:
		typ = TypColor
	default:
		typ = TypDyn
	}
	return &ir.Literal{AST: x, Type: typ, Raw: x.Raw}
}

func (c *checker) inferUnitLiteral(x *ast.UnitLiteral) ir.Expr {
	ud, ok := c.unitBySuffix[x.Suffix]
	if !ok {
		c.error(x.Pos, "unknown unit suffix %q", x.Suffix)
		return &ir.Literal{AST: &x.LiteralExpr, Type: TypDyn, Raw: x.Raw, Suffix: x.Suffix}
	}
	return &ir.Literal{AST: &x.LiteralExpr, Type: ud.SymType(), Raw: x.Raw, Suffix: x.Suffix}
}

func (c *checker) inferIdent(x *ast.IdentExpr) ir.Expr {
	// Builtin constants.
	switch x.Name {
	case "PLATFORM", "LANGUAGE":
		return &ir.Ident{AST: x, Type: TypString, Name: x.Name}
	case "true", "false":
		return &ir.Ident{AST: x, Type: TypBool, Name: x.Name}
	case "null":
		return &ir.Ident{AST: x, Type: TypNull, Name: x.Name}
	}

	sym, ok := c.scope.Lookup(x.Name)
	if !ok {
		// When expected type is an enum, resolve bare member names.
		if c.expected != nil && c.expected.Kind == ir.TypeEnum {
			if ed, ok := c.expected.Decl.(*ir.EnumDef); ok {
				for _, m := range ed.Members {
					if m.Name == x.Name {
						return &ir.Ident{AST: x, Type: c.expected, Name: x.Name, Member: x.Name}
					}
				}
			}
		}
		// Try platform Resolve() inside platform blocks.
		if resolved := c.resolvePlatformIdent(x.Name); resolved != nil {
			sym = resolved
			ok = true
		}
	}
	if !ok {
		c.error(x.Pos, "undefined: %s", x.Name)
		return &ir.Ident{AST: x, Type: TypDyn, Name: x.Name}
	}
	t := sym.SymType()
	if t == nil {
		t = TypDyn
	}
	return &ir.Ident{AST: x, Type: t, Name: x.Name, Sym: sym}
}

// comparableEq reports whether two operand types can be compared with == / !=.
// Strict "like types" rule: same kind, numeric-to-numeric, or one side is null
// (option/nullable). Cross-kind comparisons like string == color, which used to
// silently return false at runtime, become check-time errors.
func comparableEq(left, right *ir.Type) bool {
	if left == nil || right == nil {
		return true
	}
	if left.Kind == ir.TypeNull || right.Kind == ir.TypeNull {
		return true
	}
	if left.IsNumeric() && right.IsNumeric() {
		return true
	}
	if left.Kind == right.Kind {
		return true
	}
	return false
}

func (c *checker) inferBinary(x *ast.BinaryExpr) ir.Expr {
	leftExpr := c.checkExpr(x.Left)
	rightExpr := c.checkExpr(x.Right)
	left := exprType(leftExpr)
	right := exprType(rightExpr)

	skip := left.Kind == ir.TypeDyn || right.Kind == ir.TypeDyn

	var typ *ir.Type
	switch x.Op {
	case ast.BinAnd, ast.BinOr:
		if !skip && (left.Kind != ir.TypeBool || right.Kind != ir.TypeBool) {
			c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
		}
		typ = TypBool
	case ast.BinEq, ast.BinNeq:
		if !skip && !comparableEq(left, right) {
			c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
		}
		typ = TypBool
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				// int/float comparisons always OK
			case left.Kind == ir.TypeString && right.Kind == ir.TypeString:
				// string comparisons OK
			case left.SameUnitType(right) && left.IsSingleBaseUnit():
				// single-base unit comparisons OK (e.g. duration)
			default:
				c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
			}
		}
		typ = TypBool
	case ast.BinAdd, ast.BinSub:
		if left.Kind == ir.TypeString || right.Kind == ir.TypeString {
			if x.Op == ast.BinAdd {
				if !skip && (left.Kind != ir.TypeString || right.Kind != ir.TypeString) {
					c.error(x.Pos, "operator + not defined for %s and %s", left, right)
				}
				typ = TypString
			} else {
				if !skip {
					c.error(x.Pos, "operator - not defined for %s and %s", left, right)
				}
				typ = TypDyn
			}
		} else if left.Kind == ir.TypeUnit || right.Kind == ir.TypeUnit {
			// unit +/- unit: both must be same unit type
			if !skip && !left.SameUnitType(right) {
				c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
			}
			typ = left
			if typ.Kind != ir.TypeUnit {
				typ = right
			}
		} else {
			if !skip && (!left.IsNumeric() || !right.IsNumeric()) {
				c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
			}
			typ = c.narrowNumeric(left, right)
		}
	case ast.BinMul:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.narrowNumeric(left, right)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit * scalar
			case left.IsNumeric() && right.Kind == ir.TypeUnit:
				typ = right // scalar * unit
			default:
				c.error(x.Pos, "operator * not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.narrowNumeric(left, right)
		}
	case ast.BinDiv:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.narrowNumeric(left, right)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit / scalar
			case left.SameUnitType(right):
				typ = TypFloat // unit / unit → dimensionless ratio
			default:
				c.error(x.Pos, "operator / not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.narrowNumeric(left, right)
		}
	case ast.BinMod:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.narrowNumeric(left, right)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit % scalar
			default:
				c.error(x.Pos, "operator %% not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.narrowNumeric(left, right)
		}
	default:
		typ = TypDyn
	}
	return &ir.Binary{AST: x, Type: typ, Op: x.Op, Left: leftExpr, Right: rightExpr}
}

// binOpStr returns the source representation of a binary operator.
func binOpStr(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
	case ast.BinEq:
		return "=="
	case ast.BinNeq:
		return "!="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	}
	return "?"
}

// narrowNumeric returns the wider of two numeric types.
func (c *checker) narrowNumeric(left, right *ir.Type) *ir.Type {
	if left.Kind == ir.TypeFloat || right.Kind == ir.TypeFloat {
		return TypFloat
	}
	if left.Kind == ir.TypeInt || right.Kind == ir.TypeInt {
		return TypInt
	}
	if left.Kind == ir.TypeUnit {
		return left
	}
	if right.Kind == ir.TypeUnit {
		return right
	}
	return TypDyn
}

func (c *checker) inferUnary(x *ast.UnaryExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	operand := exprType(operandExpr)
	skip := operand.Kind == ir.TypeDyn
	var typ *ir.Type
	switch x.Op {
	case ast.UnaryNot:
		// Implicit call: zero-arg func returning bool.
		if wrapped, ret := c.implicitCall(x.Operand, operand, TypBool); wrapped != nil {
			x.Operand = wrapped
			operandExpr = c.checkExpr(x.Operand)
			operand = ret
			skip = false
		}
		if !skip && operand.Kind != ir.TypeBool {
			c.error(x.Pos, "operator ! not defined for %s", operand)
		}
		typ = TypBool
	case ast.UnaryNeg:
		if !skip && !operand.IsNumeric() {
			c.error(x.Pos, "operator - not defined for %s", operand)
		}
		typ = operand
	default:
		typ = TypDyn
	}
	return &ir.Unary{AST: x, Type: typ, Op: x.Op, Operand: operandExpr}
}

func (c *checker) inferTernary(x *ast.TernaryExpr) ir.Expr {
	condExpr := c.checkExpr(x.Cond)
	thenExpr := c.checkExprExpecting(x.Then, c.expected)
	elseExpr := c.checkExprExpecting(x.Else, c.expected)
	return &ir.Ternary{AST: x, Type: exprType(thenExpr), Cond: condExpr, Then: thenExpr, Else: elseExpr}
}

func (c *checker) inferCall(x *ast.CallExpr) ir.Expr {
	// Check if it's a method call (callee is SelectExpr).
	if sel, ok := x.Func.(*ast.SelectExpr); ok {
		return c.inferMethodCall(sel, x)
	}

	// Check if it's a builtin conversion.
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		switch ident.Name {
		case "int":
			args := c.checkCallArgs(x.Args, nil)
			var operand ir.Expr
			if len(args) > 0 {
				operand = args[0].Value
			}
			return &ir.Conversion{AST: x, Type: TypInt, Operand: operand}
		case "float":
			args := c.checkCallArgs(x.Args, nil)
			var operand ir.Expr
			if len(args) > 0 {
				operand = args[0].Value
			}
			return &ir.Conversion{AST: x, Type: TypFloat, Operand: operand}
		case "string":
			args := c.checkCallArgs(x.Args, nil)
			var operand ir.Expr
			if len(args) > 0 {
				operand = args[0].Value
			}
			return &ir.Conversion{AST: x, Type: TypString, Operand: operand}
		case "bool":
			args := c.checkCallArgs(x.Args, nil)
			var operand ir.Expr
			if len(args) > 0 {
				operand = args[0].Value
			}
			return &ir.Conversion{AST: x, Type: TypBool, Operand: operand}
		}
	}

	// Regular function call.
	calleeExpr := c.checkExpr(x.Func)
	calleeType := exprType(calleeExpr)

	// Component instantiation in expression position: text(value="hi")
	if calleeType.Kind == ir.TypeComponent {
		if comp, ok := calleeType.Decl.(*ir.Component); ok {
			args := c.checkComponentCallArgs(x, comp)
			return &ir.Call{AST: x, Type: calleeType, Args: args}
		}
		return &ir.Call{AST: x, Type: calleeType, Args: c.checkCallArgs(x.Args, nil)}
	}

	var sig *ir.FuncSig
	if calleeType.Kind == ir.TypeFunc && calleeType.Sig != nil {
		sig = calleeType.Sig
	}

	// Infer generic type params from arguments.
	if sig != nil && len(sig.TypeParams) > 0 {
		sig = c.inferTypeParams(sig, x.Args)
	}

	args := c.checkCallArgs(x.Args, sig)

	var resolvedFunc *ir.Func
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			if f, ok := sym.(*ir.Func); ok {
				resolvedFunc = f
			}
		}
	}

	retType := TypDyn
	if sig != nil && sig.Return != nil {
		retType = sig.Return
	}
	call := &ir.Call{AST: x, Type: retType, Func: resolvedFunc, Args: args}
	if resolvedFunc == nil {
		call.Callee = calleeExpr
	}
	return call
}

func (c *checker) inferMethodCall(sel *ast.SelectExpr, call *ast.CallExpr) ir.Expr {
	// Determine if operand is a type name (static call) vs a value (instance call).
	isStatic := false
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			switch sym.(type) {
			case *ir.TypeSym, *ir.StructDef, *ir.EnumDef, *ir.UnitDef:
				isStatic = true
			}
		}
	}

	receiverExpr := c.checkExpr(sel.Operand)
	receiver := exprType(receiverExpr)

	// Namespace function or component call: ns.func() or ns.Component().
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			if ns, ok := sym.(*ir.Namespace); ok && (ns.Pkg != nil || ns.Resolve != nil) {
				if ns.Pkg != nil {
					if fsym, ok := ns.Pkg.Symbols.Root.Lookup(sel.Field); ok {
						// Component in namespace — validate visibility and props.
						if comp, ok := fsym.(*ir.Component); ok {
							// Private component filter: only for imported user packages (not stdlib/sngl).
							if ns.Name != "sngl" && len(sel.Field) > 0 && sel.Field[0] >= 'a' && sel.Field[0] <= 'z' {
								c.error(sel.Pos, "unknown component %q in package %s", sel.Field, ident.Name)
								return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
							}
							args := c.checkComponentCallArgs(call, comp)
							c.validateCallStmtComponentArgs(call, comp)
							return &ir.Call{AST: call, Type: comp.SymType(), Receiver: receiverExpr, Args: args}
						}
						// Regular function in namespace.
						t := fsym.SymType()
						var sig *ir.FuncSig
						if t != nil && t.Kind == ir.TypeFunc && t.Sig != nil {
							sig = t.Sig
						}
						args := c.checkCallArgs(call.Args, sig)
						var resolvedFunc *ir.Func
						if f, ok := fsym.(*ir.Func); ok {
							resolvedFunc = f
						}
						retType := t
						if sig != nil && sig.Return != nil {
							retType = sig.Return
						}
						return &ir.Call{AST: call, Type: retType, Func: resolvedFunc, Receiver: receiverExpr, Args: args}
					}
				}
				// Try Resolve() fallback for dynamic elements (e.g., html.code).
				if ns.Resolve != nil {
					if resolved := ns.Resolve(sel.Field); resolved != nil {
						t := resolved.SymType()
						if t == nil {
							t = TypDyn
						}
						args := c.checkCallArgs(call.Args, nil)
						return &ir.Call{AST: call, Type: t, Receiver: receiverExpr, Args: args}
					}
				}
				// Nothing found in namespace.
				c.error(sel.Pos, "unknown component %q in package %s", sel.Field, ident.Name)
				return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
			}
		}
	}

	// Type-attached method call.
	typeName := receiver.String()
	fn, ok := c.symtab.LookupMethod(typeName, sel.Field)
	// Fallback for generic types: list<int> → "list", option<int> → "option".
	if !ok {
		switch receiver.Kind {
		case ir.TypeList:
			fn, ok = c.symtab.LookupMethod("list", sel.Field)
		case ir.TypeOption:
			fn, ok = c.symtab.LookupMethod("option", sel.Field)
		}
	}
	if ok {
		sig := fn.FuncSig()
		if len(sig.TypeParams) > 0 {
			// For instance calls, bind receiver to param[0] before inferring
			// from explicit args so that e.g. list<int>.length() binds T=int.
			if !isStatic && len(sig.Params) > 0 {
				bindings := make(map[string]*ir.Type)
				bindTypeParams(sig.Params[0].Type, receiver, bindings)
				if len(bindings) > 0 {
					sig = sig.Substitute(bindings)
				}
			}
			sig = c.inferTypeParams(sig, call.Args)
		}
		var args []ir.CallArg
		if isStatic {
			// Static call: Type.method(args...) — all args explicit.
			// Type ident is only a namespace marker; drop it.
			args = c.checkCallArgs(call.Args, sig)
		} else if len(sig.Params) > 0 && receiver.IsAssignableTo(sig.Params[0].Type) {
			// Instance call: expr.method(args...) — receiver is implicit first arg.
			// Validate remaining args against the shifted sig, then prepend the
			// receiver so the IR matches the static call shape.
			shifted := &ir.FuncSig{
				Params:     sig.Params[1:],
				Return:     sig.Return,
				TypeParams: sig.TypeParams,
			}
			rest := c.checkCallArgs(call.Args, shifted)
			args = append([]ir.CallArg{{Value: receiverExpr}}, rest...)
		} else {
			rest := c.checkCallArgs(call.Args, sig)
			args = append([]ir.CallArg{{Value: receiverExpr}}, rest...)
		}
		retType := TypDyn
		if sig.Return != nil {
			retType = sig.Return
		}
		return &ir.Call{AST: call, Type: retType, Func: fn, Args: args}
	}

	args := c.checkCallArgs(call.Args, nil)
	return &ir.Call{AST: call, Type: TypDyn, Receiver: receiverExpr, Args: args}
}

func (c *checker) inferSelect(x *ast.SelectExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	operand := exprType(operandExpr)

	switch x.Kind {
	case ast.SelectEvent:
		return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
	case ast.SelectElemRef:
		return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
	case ast.SelectField:
		// Namespace member access: ns.field.
		if ident, ok := x.Operand.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if ns, ok := sym.(*ir.Namespace); ok {
					if ns.Pkg != nil {
						if fsym, ok := ns.Pkg.Symbols.Root.Lookup(x.Field); ok {
							t := fsym.SymType()
							return &ir.Select{AST: x, Type: t, Operand: operandExpr, Field: x.Field}
						}
					}
					// Try Resolve() fallback for dynamic members.
					if ns.Resolve != nil {
						if resolved := ns.Resolve(x.Field); resolved != nil {
							t := resolved.SymType()
							if t == nil {
								t = TypDyn
							}
							return &ir.Select{AST: x, Type: t, Operand: operandExpr, Field: x.Field}
						}
					}
				}
			}
		}

		// Enum member access: EnumType.member. Normalize to the same IR shape
		// as a bare enum member reference so codegen never sees a dangling
		// EnumType.member select that the target language doesn't define.
		if operand.Kind == ir.TypeEnum && operand.Decl != nil {
			if ed, ok := operand.Decl.(*ir.EnumDef); ok {
				for _, m := range ed.Members {
					if m.Name == x.Field {
						return &ir.Ident{Type: operand, Name: x.Field, Member: x.Field, Sym: ed}
					}
				}
				c.error(x.Pos, "no member %q on enum %s", x.Field, ed.Name)
			}
		}

		// Struct field access.
		if operand.Kind == ir.TypeStruct && operand.Decl != nil {
			if sd, ok := operand.Decl.(*ir.StructDef); ok {
				for _, f := range sd.Fields {
					if f.Name == x.Field {
						return &ir.Select{AST: x, Type: f.Type, Operand: operandExpr, Field: x.Field}
					}
				}
				c.error(x.Pos, "no field %q on struct %s", x.Field, sd.Name)
			}
		}

		// Built-in list/string .length yields int.
		if x.Field == "length" && (operand.Kind == ir.TypeList || operand.Kind == ir.TypeString) {
			return &ir.Select{AST: x, Type: TypInt, Operand: operandExpr, Field: x.Field}
		}
	}

	return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
}

func (c *checker) inferIndex(x *ast.IndexExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	indexExpr := c.checkExpr(x.Index)
	operand := exprType(operandExpr)

	if operand.Kind == ir.TypeList && len(operand.Elems) > 0 {
		t := operand.Elems[0]
		return &ir.Index{AST: x, Type: t, Operand: operandExpr, Idx: indexExpr}
	}
	return &ir.Index{AST: x, Type: TypDyn, Operand: operandExpr, Idx: indexExpr}
}

func (c *checker) inferStructLit(x *ast.StructExpr) ir.Expr {
	// Look up struct type.
	var sd *ir.StructDef
	if x.Package != "" {
		// Qualified: pkg.Struct{...}
		if sym, ok := c.scope.Lookup(x.Package); ok {
			if ns, ok := sym.(*ir.Namespace); ok && ns.Pkg != nil {
				if tsym, ok := ns.Pkg.Symbols.LookupType(x.Name); ok {
					if s, ok := tsym.(*ir.StructDef); ok {
						sd = s
					}
				}
			}
		}
	} else if x.Name != "" {
		if sym, ok := c.scope.Lookup(x.Name); ok {
			if s, ok := sym.(*ir.StructDef); ok {
				sd = s
			}
		}
	} else if c.expected != nil && c.expected.Kind == ir.TypeStruct && c.expected.Decl != nil {
		// Anonymous struct literal with expected struct type: infer the type.
		if s, ok := c.expected.Decl.(*ir.StructDef); ok {
			sd = s
		}
	}

	// Check field values.
	var fields []ir.FieldInit
	for _, f := range x.Fields {
		if f.Spread {
			val := c.checkExpr(f.Value)
			fields = append(fields, ir.FieldInit{Value: val, Spread: true})
			continue
		}
		var expected *ir.Type
		if sd != nil {
			expected = structFieldType(sd, f.Name)
		}
		val := c.checkExprExpecting(f.Value, expected)
		// Validate field exists on struct.
		if sd != nil && !structHasField(sd, f.Name) {
			c.error(x.Pos, "unknown field %q on struct %s", f.Name, sd.Name)
		}
		fields = append(fields, ir.FieldInit{Name: f.Name, Value: val})
	}

	if sd != nil {
		return &ir.StructLit{AST: x, Type: sd.SymType(), Def: sd, Fields: fields}
	}
	return &ir.StructLit{AST: x, Type: &ir.Type{Kind: ir.TypeStruct}, Fields: fields}
}

func structHasField(sd *ir.StructDef, name string) bool {
	for _, f := range sd.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func structFieldType(sd *ir.StructDef, name string) *ir.Type {
	for _, f := range sd.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

func (c *checker) inferListLit(x *ast.ListExpr) ir.Expr {
	if len(x.Elements) == 0 {
		if c.expected != nil && c.expected.Kind == ir.TypeList {
			return &ir.ListLit{AST: x, Type: c.expected, Elems: nil}
		}
		c.error(x.Pos, "cannot infer element type of empty list literal; add a type annotation")
		return &ir.ListLit{AST: x, Type: ListOf(TypDyn)}
	}
	var elemExpected *ir.Type
	if c.expected != nil && c.expected.Kind == ir.TypeList && len(c.expected.Elems) > 0 {
		elemExpected = c.expected.Elems[0]
	}
	elems := make([]ir.Expr, len(x.Elements))
	elems[0] = c.checkExprExpecting(x.Elements[0], elemExpected)
	for i, e := range x.Elements[1:] {
		elems[i+1] = c.checkExprExpecting(e, elemExpected)
	}
	return &ir.ListLit{AST: x, Type: ListOf(exprType(elems[0])), Elems: elems}
}

// interpPartAlreadyString reports whether an interpolation part's type can be
// concatenated into a string chain without an explicit string() conversion.
func interpPartAlreadyString(t *ir.Type) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeDyn,
		ir.TypeColor, ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64,
		ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal:
		return true
	}
	return false
}

func (c *checker) inferInterpolation(x *ast.InterpolationExpr) ir.Expr {
	// Desugar interpolation to a chain of Binary + operations.
	// Each string literal part becomes an ir.Literal; each expression part is checked
	// and coerced to string so codegen can concatenate without type errors.
	var chain ir.Expr
	for _, part := range x.Parts {
		var partExpr ir.Expr
		if lit, ok := part.(*ast.LiteralExpr); ok {
			partExpr = &ir.Literal{AST: lit, Type: TypString, Raw: lit.Raw}
		} else {
			partExpr = c.checkExpr(part)
			if t := exprType(partExpr); !interpPartAlreadyString(t) {
				partExpr = &ir.Conversion{Type: TypString, Operand: partExpr}
			}
		}
		if chain == nil {
			chain = partExpr
		} else {
			chain = &ir.Binary{Type: TypString, Op: ast.BinAdd, Left: chain, Right: partExpr}
		}
	}
	if chain == nil {
		return &ir.Literal{Type: TypString, Raw: `""`}
	}
	return chain
}

func (c *checker) inferLambda(x *ast.LambdaExpr) ir.Expr {
	fn := &ir.Func{
		Params: c.buildParams(x.Params),
		Return: c.resolveType(x.ReturnType),
	}

	// Type-check the lambda body in a child scope.
	c.pushScope()
	for _, p := range fn.Params {
		c.scope.Declare(p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	if x.Body != nil {
		bodyExpr := c.checkExprExpecting(x.Body, fn.Return)
		fn.Block = []ir.Stmt{&ir.Return{AST: &ast.ReturnStmt{Pos: *x.Body.ExprPos(), Value: x.Body}, Value: bodyExpr}}
	}
	if x.Block.IsDefined() {
		fn.Block = c.checkBlockIR(&x.Block)
	}
	c.returnType = prevReturn
	c.popScope()

	return &ir.Lambda{AST: x, Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Func: fn}
}

// inferTypeParams infers concrete types for generic type params by matching
// argument types against parameter types, then returns a substituted FuncSig.
func (c *checker) inferTypeParams(sig *ir.FuncSig, args ast.ArgList) *ir.FuncSig {
	bindings := make(map[string]*ir.Type)
	// Match positional args to params.
	pos := 0
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name != "" {
			continue
		}
		if pos >= len(sig.Params) {
			break
		}
		argExpr := c.checkExpr(arg.Value)
		argType := exprType(argExpr)
		if argType.Kind != ir.TypeDyn {
			bindTypeParams(sig.Params[pos].Type, argType, bindings)
		}
		pos++
	}
	if len(bindings) == 0 {
		return sig
	}
	return sig.Substitute(bindings)
}

// bindTypeParams recursively matches a param type pattern against a concrete
// arg type to extract type parameter bindings. E.g. list<T> vs list<int> → T=int.
func bindTypeParams(param, arg *ir.Type, bindings map[string]*ir.Type) {
	if param == nil || arg == nil {
		return
	}
	if param.Kind == ir.TypeTypeParam {
		if _, exists := bindings[param.ParamName]; !exists {
			bindings[param.ParamName] = arg
		}
		return
	}
	// Recurse into type arguments (list<T>, option<T>, etc.).
	if param.Kind == arg.Kind && len(param.Elems) == len(arg.Elems) {
		for i := range param.Elems {
			bindTypeParams(param.Elems[i], arg.Elems[i], bindings)
		}
	}
}

func (c *checker) inferEventRef(x *ast.EventRefExpr) ir.Expr {
	if c.currentComponent != nil {
		for _, evt := range c.currentComponent.Events {
			if evt.Name == x.Name {
				if evt.Type != nil {
					return &ir.Ident{Type: evt.Type}
				}
				return &ir.Ident{Type: TypDyn}
			}
		}
	}
	return &ir.Ident{Type: TypDyn}
}

// checkCallArgs type-checks all arguments in an ArgList and returns resolved CallArgs.
// If sig is non-nil, validates positional arg types and arity against it.
func (c *checker) checkCallArgs(args ast.ArgList, sig *ir.FuncSig) []ir.CallArg {
	var result []ir.CallArg
	positional := 0
	for i, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				// Determine expected type from function signature.
				var expected *ir.Type
				if sig != nil && arg.Name == "" && positional < len(sig.Params) {
					expected = sig.Params[positional].Type
				}
				argExpr := c.checkExprExpecting(arg.Value, expected)
				argType := exprType(argExpr)
				// Validate positional arg type against param.
				if sig != nil && arg.Name == "" && positional < len(sig.Params) {
					paramType := sig.Params[positional].Type
					if argType.Kind != ir.TypeDyn && paramType.Kind != ir.TypeDyn && !argType.IsAssignableTo(paramType) {
						if callExpr, _ := c.implicitCall(arg.Value, argType, paramType); callExpr != nil {
							args.Args[i] = ast.Arg{Name: arg.Name, Value: callExpr}
							// Re-check the wrapped call to get proper ir.Expr.
							argExpr = c.checkExpr(callExpr)
						} else {
							c.error(*arg.Value.ExprPos(), "argument %d: cannot pass %s as %s", positional+1, argType, paramType)
						}
					}
				}
				result = append(result, ir.CallArg{Name: arg.Name, Value: argExpr})
			}
			if arg.Name == "" {
				positional++
			}
		case ast.EventHandler:
			// Inline event handler — check body.
			c.pushScope()
			for _, p := range arg.Params.Params {
				c.scope.Declare(&ir.Param{
					Name: p.Name,
					Type: c.resolveType(p.Type),
				})
			}
			c.checkBlock(&arg.Body)
			c.popScope()
		}
	}
	// Arity check.
	if sig != nil {
		// Count required params (no default).
		required := 0
		for _, p := range sig.Params {
			if p.Default == nil {
				required++
			}
		}
		if positional < required {
			c.error(args.Pos, "expected %d arguments, got %d", required, positional)
		} else if positional > len(sig.Params) {
			c.error(args.Pos, "expected %d arguments, got %d", len(sig.Params), positional)
		}
	}
	return result
}

// --- Statement checking ---

// checkBlock type-checks all statements in a StmtBlock (legacy, does not return IR).
func (c *checker) checkBlock(block *ast.StmtBlock) {
	if block == nil || !block.IsDefined() {
		return
	}
	c.pushScope()
	defer c.popScope()
	for _, stmt := range block.Stmts {
		c.checkStmt(stmt)
	}
}

// checkBlockIR type-checks a StmtBlock and returns typed IR statements.
func (c *checker) checkBlockIR(block *ast.StmtBlock) []ir.Stmt {
	if block == nil || !block.IsDefined() {
		return nil
	}
	c.pushScope()
	defer c.popScope()
	var out []ir.Stmt
	for _, stmt := range block.Stmts {
		if vd, ok := stmt.(*ast.VarDecl); ok {
			out = append(out, c.checkLocalVarDecl(vd)...)
			continue
		}
		if s := c.checkStmt(stmt); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// checkLocalVarDecl type-checks a var declaration inside a block and returns
// its LocalVar IR stmts. Unlike top-level registerVars, this does NOT append
// to pkg.Vars — local vars live in their enclosing function scope.
func (c *checker) checkLocalVarDecl(decl *ast.VarDecl) []ir.Stmt {
	var out []ir.Stmt
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var initExpr ir.Expr
		if spec.Default != nil {
			initExpr = c.checkExprExpecting(spec.Default, typ)
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
			c.scope.Declare(&ir.Var{
				AST:  decl,
				Name: name,
				Type: typ,
			})
			out = append(out, &ir.LocalVar{
				Name: name,
				Type: typ,
				Init: initExpr,
			})
		}
	}
	return out
}

// checkStmt type-checks a single statement and returns its IR form.
// Returns nil for declarations (registered on scope) and skipped nodes.
func (c *checker) checkStmt(s ast.Stmt) ir.Stmt {
	switch x := s.(type) {
	case *ast.AssignStmt:
		targetExpr := c.checkExpr(x.Target)
		targetType := exprType(targetExpr)
		valueExpr := c.checkExprExpecting(x.Value, targetType)
		valueType := exprType(valueExpr)
		// Const reassignment check.
		if ident, ok := x.Target.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if v, ok := sym.(*ir.Var); ok && v.IsConst {
					c.error(x.Pos, "cannot assign to const %q", ident.Name)
				}
			}
		}
		// Type checking.
		if x.Op == ast.AssignSet {
			if targetType.Kind != ir.TypeDyn && valueType.Kind != ir.TypeDyn && !valueType.IsAssignableTo(targetType) {
				c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
			}
		} else {
			// Compound assignment: both sides must be numeric (or string for +=).
			if targetType.Kind != ir.TypeDyn && valueType.Kind != ir.TypeDyn {
				if x.Op == ast.AssignAdd && targetType.Kind == ir.TypeString {
					// string += string is fine.
				} else if !targetType.IsNumeric() || !valueType.IsNumeric() {
					c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
				}
			}
		}
		return &ir.Assign{AST: x, Target: targetExpr, Op: x.Op, Value: valueExpr}
	case *ast.ToggleStmt:
		targetExpr := c.checkExpr(x.Target)
		return &ir.Toggle{AST: x, Target: targetExpr}
	case *ast.IncDecStmt:
		op := ast.BinAdd
		if x.IsDec {
			op = ast.BinSub
		}
		lowered := &ast.AssignStmt{
			Pos:    x.Pos,
			Target: x.Target,
			Op:     ast.AssignSet,
			Value: &ast.BinaryExpr{
				Pos:   x.Pos,
				Op:    op,
				Left:  x.Target,
				Right: &ast.LiteralExpr{Pos: x.Pos, Kind: ast.LiteralInt, Raw: "1"},
			},
		}
		return c.checkStmt(lowered)
	case *ast.EmitStmt:
		args := c.checkCallArgs(x.Args, nil)
		return &ir.Emit{AST: x, Name: x.Name, Args: args}
	case *ast.VarStmt:
		typ := c.resolveType(x.Type)
		var initExpr ir.Expr
		if x.Init != nil {
			initExpr = c.checkExprExpecting(x.Init, typ)
			initType := exprType(initExpr)
			if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		c.scope.Declare(&ir.Var{
			AST:  x,
			Name: x.Name,
			Type: typ,
		})
		return &ir.LocalVar{AST: x, Name: x.Name, Type: typ, Init: initExpr}
	case *ast.ReturnStmt:
		var valExpr ir.Expr
		if x.Value != nil {
			valExpr = c.checkExprExpecting(x.Value, c.returnType)
			valType := exprType(valExpr)
			if c.returnType != nil && c.returnType.Kind != ir.TypeDyn && valType.Kind != ir.TypeDyn && !valType.IsAssignableTo(c.returnType) {
				c.error(x.Pos, "cannot return %s as %s", valType, c.returnType)
			}
		}
		return &ir.Return{AST: x, Value: valExpr}
	case *ast.CallStmt:
		// Bodyless forms of root-ish visual nodes (e.g. `timer(...)` with
		// the tick handler inside the parens) parse as CallStmt but must
		// dispatch through the VisualNode special-cases so they register
		// on package/component instead of becoming a generic node instance.
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
			switch id.Name {
			case "timer", "window", "output":
				vn := &ast.VisualNode{
					Pos:    x.Pos,
					Target: id,
					Args:   x.Call.Args,
				}
				return c.checkVisualNodeIR(vn)
			}
		}
		// Check if the call target is a component — handle directly to avoid
		// double-checking args through both inferCall and resolveCallStmt.
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
			if sym, ok := c.symtab.LookupComponent(id.Name); ok {
				if comp, ok := sym.(*ir.Component); ok {
					c.validateCallStmtComponentArgs(x.Call, comp)
					props, handlers := c.checkAndSplitArgs(x.Call.Args, comp)
					var keyExpr ir.Expr
					for _, a := range x.Call.Args.Args {
						if arg, ok := a.(ast.Arg); ok && arg.Name == "key" && arg.Value != nil {
							keyExpr = c.checkExpr(arg.Value)
							break
						}
					}
					return &ir.NodeInst{
						AST:       x,
						Name:      id.Name,
						Component: comp,
						Props:     props,
						Handlers:  handlers,
						Key:       keyExpr,
					}
				}
			}
		}
		callExpr := c.checkExpr(x.Call)
		return c.resolveCallStmt(x, callExpr)
	case *ast.IfStmt:
		condExpr := c.checkExpr(x.Cond)
		condType := exprType(condExpr)
		// Implicit call: zero-arg func returning bool.
		if wrapped, ret := c.implicitCall(x.Cond, condType, TypBool); wrapped != nil {
			x.Cond = wrapped
			condExpr = c.checkExpr(x.Cond)
			condType = ret
		}
		if condType.Kind != ir.TypeDyn && condType.Kind != ir.TypeBool {
			c.error(x.Pos, "if condition must be bool, got %s", condType)
		}
		body := c.checkBlockIR(&x.Body)
		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			elseBody = c.checkBlockIR(&x.Else)
		}
		return &ir.If{AST: x, Cond: condExpr, Body: body, Else: elseBody}
	case *ast.ForStmt:
		iterExpr := c.checkExpr(x.Iter)
		iter := exprType(iterExpr)
		if iter.Kind != ir.TypeDyn && iter.Kind != ir.TypeList {
			c.error(x.Pos, "for iterator must be list, got %s", iter)
		}
		c.pushScope()
		// Declare loop variables.
		elemType := TypDyn
		if iter.Kind == ir.TypeList && len(iter.Elems) > 0 {
			elemType = iter.Elems[0]
		}
		if x.Value != "" {
			// for key, value = iter: key is index, value is element.
			c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypInt})
			c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: elemType})
		} else {
			// for item = iter: item is element.
			c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: elemType})
		}
		body := c.checkBlockIR(&x.Body)
		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			elseBody = c.checkBlockIR(&x.Else)
		}
		c.popScope()
		return &ir.For{AST: x, Key: x.Key, Value: x.Value, Iter: iterExpr, ElemType: elemType, Body: body, Else: elseBody}
	case *ast.PlatformStmt:
		return c.checkPlatformStmtIR(x)
	case *ast.VisualNode:
		return c.checkVisualNodeIR(x)
	case *ast.ConstDecl:
		c.registerConsts(x)
		return nil
	case *ast.VarDecl:
		c.registerVars(x)
		return nil
	case *ast.FuncDef:
		fn := c.buildFunc(x)
		c.scope.Declare(fn)
		c.checkFuncBody(fn)
		return nil
	case *ast.Comment:
		return nil
	case *ast.DisabledDecl:
		return nil
	}
	return nil
}

// buildPlatformPkgScope builds (and caches) a scope containing declarations
// from the named platform's Package() docs.
func (c *checker) buildPlatformPkgScope(platform string) *ir.Scope {
	if c.platformScopeCache != nil {
		if s, ok := c.platformScopeCache[platform]; ok {
			// Clone so each insertion point gets its own parent chain.
			clone := NewScope(nil)
			maps.Copy(clone.Symbols, s.Symbols)
			return clone
		}
	}

	t := c.lookupTarget(platform)
	if t == nil {
		return nil
	}

	pkg := c.buildPkgFromDocs(t.Package())
	if pkg == nil {
		return nil
	}

	scope := NewScope(nil)
	maps.Copy(scope.Symbols, pkg.Symbols.Root.Symbols)
	// Declare the platform namespace with its package so qualified access
	// (e.g., html.Options) works inside platform blocks.
	scope.Declare(&ir.Namespace{Name: platform, Pkg: pkg, Resolve: t.Resolve})

	if c.platformScopeCache == nil {
		c.platformScopeCache = make(map[string]*ir.Scope)
	}
	c.platformScopeCache[platform] = scope

	// Return a clone for this usage.
	clone := NewScope(nil)
	maps.Copy(clone.Symbols, scope.Symbols)
	return clone
}

// resolvePlatformIdent tries the current platform's Resolve() for an unknown
// identifier. Returns nil when not inside a platform block or when the
// platform cannot resolve the name.
func (c *checker) resolvePlatformIdent(name string) ir.Symbol {
	if c.currentPlatform == "" {
		return nil
	}
	t := c.lookupTarget(c.currentPlatform)
	if t == nil {
		return nil
	}
	return t.Resolve(name)
}

// resolveQualifiedIdent reports whether a qualified name "ns.Field" resolves
// to a namespace member (component, func, or platform-resolved element like
// html.div) via an in-scope Namespace.
func (c *checker) resolveQualifiedIdent(name string) bool {
	ns, field, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	sym, ok := c.scope.Lookup(ns)
	if !ok {
		return false
	}
	nsSym, ok := sym.(*ir.Namespace)
	if !ok {
		return false
	}
	if nsSym.Pkg != nil {
		if _, ok := nsSym.Pkg.Symbols.Root.Lookup(field); ok {
			return true
		}
	}
	if nsSym.Resolve != nil {
		if nsSym.Resolve(field) != nil {
			return true
		}
	}
	return false
}

// resolveCallStmt converts a checked call expression into a CallStmt.
// Component instantiations are handled before this is called.
func (c *checker) resolveCallStmt(x *ast.CallStmt, callExpr ir.Expr) ir.Stmt {
	var call *ir.Call
	if c, ok := callExpr.(*ir.Call); ok {
		call = c
	} else {
		// For conversions or other expression types, wrap in a Call.
		call = &ir.Call{AST: x.Call, Type: exprType(callExpr)}
	}
	return &ir.CallStmt{AST: x, Call: call}
}

// checkPlatformStmtIR type-checks a platform statement and returns IR.
func (c *checker) checkPlatformStmtIR(s *ast.PlatformStmt) ir.Stmt {
	// Skip body when target platform is known and doesn't match.
	if c.cfg.Target != nil && c.cfg.Target.Platform != "" && c.cfg.Target.Platform != s.Platform {
		return nil
	}

	// Inject platform package scope as fallback between current scope and its parent.
	platformScope := c.buildPlatformPkgScope(s.Platform)
	if platformScope != nil {
		savedParent := c.scope.Parent
		platformScope.Parent = savedParent
		c.scope.Parent = platformScope
		defer func() { c.scope.Parent = savedParent }()
	}

	// Track current platform so Resolve() fallback works on unknown identifiers.
	savedPlatform := c.currentPlatform
	c.currentPlatform = s.Platform
	defer func() { c.currentPlatform = savedPlatform }()

	body := c.checkBlockIR(&s.Body)
	return &ir.PlatformFilter{AST: s, Platform: s.Platform, Body: body}
}

// checkVisualNodeIR validates a visual node and returns the appropriate IR statement.
// Disambiguates components, platform elements, slots, and function calls.
func (c *checker) checkVisualNodeIR(vn *ast.VisualNode) ir.Stmt {
	name := visualNodeTarget(vn)

	// Special root-ish nodes — registered on package, not returned as IR stmts.
	switch name {
	case "window":
		w := c.buildWindow(vn)
		c.checkWindowBody(w)
		w.Checked = true
		return w
	case "timer":
		t := c.buildTimer(vn)
		if c.currentComponent != nil {
			c.currentComponent.Timers = append(c.currentComponent.Timers, t)
		} else {
			c.pkg.Timers = append(c.pkg.Timers, t)
		}
		return nil
	case "output":
		if !c.cfg.IsMain {
			c.error(vn.Pos, "output declarations only permitted in main file")
		}
		c.buildOutputs(vn)
		return nil
	case "slot":
		children := c.checkBlockIR(&vn.Block)
		return &ir.SlotInst{AST: vn, Children: children}
	}

	// Look up component — supports bare ("Foo") and qualified ("pkg.Foo") names.
	var comp *ir.Component
	if ns, field, ok := strings.Cut(name, "."); ok {
		if sym, sok := c.scope.Lookup(ns); sok {
			if nsSym, nok := sym.(*ir.Namespace); nok {
				if nsSym.Pkg != nil {
					if fsym, ok := nsSym.Pkg.Symbols.LookupComponent(field); ok {
						if co, ok := fsym.(*ir.Component); ok {
							comp = co
						}
					}
				}
			}
		}
	} else if sym, ok := c.symtab.LookupComponent(name); ok {
		if co, ok := sym.(*ir.Component); ok {
			comp = co
		}
	}

	// If not a component, check if it's a function or bare expression.
	if comp == nil {
		if sym, ok := c.scope.Lookup(name); ok {
			// Bare variable used as statement — flag it (no block, no args).
			if _, isFunc := sym.(*ir.Func); !isFunc && !vn.Block.IsDefined() && len(vn.Args.Args) == 0 {
				c.error(vn.Pos, "expression must be a statement")
				return nil
			}
			if fn, ok := sym.(*ir.Func); ok {
				// This is a function call, not a visual node.
				props, _ := c.checkAndSplitArgs(vn.Args, nil)
				var args []ir.CallArg
				for _, p := range props {
					args = append(args, ir.CallArg{Name: p.Name, Value: p.Value})
				}
				return &ir.CallStmt{AST: vn, Call: &ir.Call{Type: TypDyn, Func: fn, Args: args}}
			}
		}
	}

	// Unknown component check — skip if name resolves to a variable
	// (e.g. timer condition guard like `running { ... }`),
	// or if the platform/namespace can resolve it.
	if comp == nil && name != "" {
		if _, inScope := c.scope.Lookup(name); !inScope {
			if !c.resolveQualifiedIdent(name) && c.resolvePlatformIdent(name) == nil {
				c.error(vn.Pos, "unknown component %q", name)
			}
		}
	}

	// Validate props on known components.
	if comp != nil {
		c.validateVisualNodeProps(vn, comp)
	}

	children := c.checkBlockIR(&vn.Block)
	if comp != nil && len(children) > 0 && comp.ChildrenType == nil {
		c.error(vn.Pos, "component %s does not accept children", comp.Name)
	}
	props, handlers := c.checkAndSplitArgs(vn.Args, comp)

	// Extract key= arg for loop diffing.
	var keyExpr ir.Expr
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == "key" && arg.Value != nil {
			keyExpr = c.checkExpr(arg.Value)
			break
		}
	}

	return &ir.NodeInst{
		AST:       vn,
		Name:      name,
		Component: comp,
		Props:     props,
		Handlers:  handlers,
		Children:  children,
		ID:        vn.ID,
		Key:       keyExpr,
	}
}

// implicitCall checks whether actual is a zero-arg func whose return type is
// assignable to expected. If so it wraps expr in a CallExpr
// and returns (callExpr, returnType). Otherwise returns (nil, actual).
func (c *checker) implicitCall(expr ast.Expr, actual, expected *ir.Type) (ast.Expr, *ir.Type) {
	if expected == nil || actual.Kind != ir.TypeFunc || actual.Sig == nil {
		return nil, actual
	}
	if len(actual.Sig.Params) != 0 || actual.Sig.Return == nil {
		return nil, actual
	}
	if !actual.Sig.Return.IsAssignableTo(expected) {
		return nil, actual
	}
	call := &ast.CallExpr{Pos: *expr.ExprPos(), Func: expr}
	return call, actual.Sig.Return
}

// componentPropType returns the type of a named prop on a component, or nil.
func componentPropType(comp *ir.Component, name string) *ir.Type {
	for _, p := range comp.Props {
		if p.Name == name {
			return p.Type
		}
	}
	return nil
}

// componentEventType returns the payload type of a named event on a component, or nil.
func componentEventType(comp *ir.Component, name string) *ir.Type {
	for _, e := range comp.Events {
		if e.Name == name {
			return e.Type
		}
	}
	return nil
}

// validateVisualNodeProps validates props and events against a component definition.
func (c *checker) validateVisualNodeProps(vn *ast.VisualNode, comp *ir.Component) {
	// Validate args match props/events.
	for _, a := range vn.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "" || arg.Name == "key" {
				continue // positional args and key (handled by loop diffing)
			}
			// Strip : prefix for bidirectional binding lookup.
			name := arg.Name
			if strings.HasPrefix(name, ":") {
				name = name[1:]
			}
			if !componentHasProp(comp, name) && !componentHasEvent(comp, name) {
				c.error(vn.Pos, "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			if !componentHasEvent(comp, arg.Name) {
				c.error(vn.Pos, "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}
}

// checkAndSplitArgs checks values and splits a checked ArgList into IR property
// assignments and event handlers. Unlike splitNodeArgs, this re-checks values
// to produce ir.Expr rather than using ast.Expr.
func (c *checker) checkAndSplitArgs(args ast.ArgList, comp *ir.Component) ([]ir.Arg, []ir.EventHandler) {
	var props []ir.Arg
	var handlers []ir.EventHandler
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "key" {
				continue // handled separately by checkVisualNodeIR
			}
			var expected *ir.Type
			if comp != nil && arg.Name != "" {
				propName := arg.Name
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				expected = componentPropType(comp, propName)
			}
			var val ir.Expr
			if arg.Value != nil {
				val = c.checkExprExpecting(arg.Value, expected)
				// Implicit call: func() T used where T is expected.
				if expected != nil {
					actual := exprType(val)
					if actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
						if callExpr, _ := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
							val = c.checkExpr(callExpr)
						}
					}
				}
			}
			props = append(props, ir.Arg{Name: arg.Name, Value: val})
		case ast.EventHandler:
			params := make([]*ir.Param, len(arg.Params.Params))
			for i, p := range arg.Params.Params {
				typ := c.resolveType(p.Type)
				if typ.Kind == ir.TypeDyn && comp != nil {
					if et := componentEventType(comp, arg.Name); et != nil {
						typ = et
					}
				}
				params[i] = &ir.Param{Name: p.Name, Type: typ}
			}
			fn := &ir.Func{Params: params}
			// Check the handler body in a scoped context.
			c.pushScope()
			for _, p := range params {
				c.scope.Declare(p)
			}
			fn.Block = c.checkBlockIR(&arg.Body)
			c.popScope()
			handlers = append(handlers, ir.EventHandler{
				AST:  &arg,
				Name: arg.Name,
				Func: fn,
			})
		}
	}
	return c.desugarBindings(comp, props, handlers)
}

// checkComponentCallArgs validates and type-checks a component call (text(value="hi"))
// against the component's prop and event declarations, returning resolved args.
func (c *checker) checkComponentCallArgs(call *ast.CallExpr, comp *ir.Component) []ir.CallArg {
	var result []ir.CallArg
	for i, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				var expected *ir.Type
				if arg.Name != "" {
					expected = componentPropType(comp, arg.Name)
				}
				argExpr := c.checkExprExpecting(arg.Value, expected)
				actual := exprType(argExpr)
				if expected != nil && actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
					if callExpr, ret := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
						call.Args.Args[i] = ast.Arg{Name: arg.Name, Value: callExpr}
						argExpr = c.checkExpr(callExpr)
						_ = ret
					} else {
						c.error(*arg.Value.ExprPos(), "cannot pass %s as %s", actual, expected)
					}
				}
				result = append(result, ir.CallArg{Name: arg.Name, Value: argExpr})
			}
			if arg.Name == "" || arg.Name == "key" {
				continue // positional args and key (handled by loop diffing)
			}
			propName := arg.Name
			if strings.HasPrefix(propName, ":") {
				propName = propName[1:]
			}
			if !componentHasProp(comp, propName) && !componentHasEvent(comp, propName) {
				c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			c.pushScope()
			for _, p := range arg.Params.Params {
				typ := c.resolveType(p.Type)
				if typ.Kind == ir.TypeDyn {
					if et := componentEventType(comp, arg.Name); et != nil {
						typ = et
					}
				}
				c.scope.Declare(&ir.Param{
					Name: p.Name,
					Type: typ,
				})
			}
			c.checkBlock(&arg.Body)
			c.popScope()
			if !componentHasEvent(comp, arg.Name) {
				c.error(*call.Func.ExprPos(), "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}
	return result
}

// validateCallStmtComponentArgs validates prop/event names on a component call
// parsed as a CallStmt. Value checking is deferred to checkAndSplitArgs.
func (c *checker) validateCallStmtComponentArgs(call *ast.CallExpr, comp *ir.Component) {
	for _, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "" || arg.Name == "key" {
				continue
			}
			propName := arg.Name
			if strings.HasPrefix(propName, ":") {
				propName = propName[1:]
			}
			if !componentHasProp(comp, propName) && !componentHasEvent(comp, propName) {
				c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			if !componentHasEvent(comp, arg.Name) {
				c.error(*call.Func.ExprPos(), "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}
}

func componentHasProp(comp *ir.Component, name string) bool {
	for _, p := range comp.Props {
		if p.Name == name {
			return true
		}
	}
	return false
}

func componentHasEvent(comp *ir.Component, name string) bool {
	for _, e := range comp.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}
