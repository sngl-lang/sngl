package checker

import "git.duckfam.us/jonathan/sngl/ir"

import "maps"

import "git.duckfam.us/jonathan/sngl/ast"

// checkExpr infers the type of an expression, stores it in TypeMap, and returns it.
func (c *checker) checkExpr(e ast.Expr) *ir.Type {
	if e == nil {
		return TypDyn
	}
	t := c.inferExpr(e)
	c.pkg.TypeMap[e] = t
	return t
}

// checkExprExpecting checks an expression with an expected type hint.
// When the expected type is an enum, bare member names resolve automatically.
func (c *checker) checkExprExpecting(e ast.Expr, expected *ir.Type) *ir.Type {
	saved := c.expected
	c.expected = expected
	t := c.checkExpr(e)
	c.expected = saved
	return t
}

// inferExpr dispatches on expression type to infer its type.
func (c *checker) inferExpr(e ast.Expr) *ir.Type {
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
		// Check sub-expressions for side effects and type errors.
		for _, part := range x.Parts {
			c.checkExpr(part)
		}
		return TypString
	case *ast.LambdaExpr:
		return c.inferLambda(x)
	case *ast.SpreadExpr:
		return c.checkExpr(x.Operand)
	case *ast.ParenExpr:
		return c.checkExpr(x.Inner)
	case *ast.ElementRefExpr:
		return TypDyn
	case *ast.EventRefExpr:
		return c.inferEventRef(x)
	default:
		return TypDyn
	}
}

func (c *checker) inferLiteral(x *ast.LiteralExpr) *ir.Type {
	switch x.Kind {
	case ast.LiteralInt:
		return TypInt
	case ast.LiteralFloat:
		return TypFloat
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		return TypString
	case ast.LiteralBool:
		return TypBool
	case ast.LiteralNull:
		return TypNull
	case ast.LiteralColor:
		return TypColor
	}
	return TypDyn
}

func (c *checker) inferUnitLiteral(x *ast.UnitLiteral) *ir.Type {
	ud, ok := c.unitBySuffix[x.Suffix]
	if !ok {
		c.error(x.Pos, "unknown unit suffix %q", x.Suffix)
		return TypDyn
	}
	return ud.SymType()
}

func (c *checker) inferIdent(x *ast.IdentExpr) *ir.Type {
	// Builtin constants.
	switch x.Name {
	case "PLATFORM", "LANGUAGE":
		return TypString
	case "true", "false":
		return TypBool
	case "null":
		return TypNull
	}

	sym, ok := c.scope.Lookup(x.Name)
	if !ok {
		// When expected type is an enum, resolve bare member names.
		if c.expected != nil && c.expected.Kind == ir.TypeEnum {
			if ed, ok := c.expected.Decl.(*ir.EnumDef); ok {
				for _, m := range ed.Members {
					if m.Name == x.Name {
						return c.expected
					}
				}
			}
		}
		c.error(x.Pos, "undefined: %s", x.Name)
		return TypDyn
	}
	if t := sym.SymType(); t != nil {
		return t
	}
	return TypDyn
}

func (c *checker) inferBinary(x *ast.BinaryExpr) *ir.Type {
	left := c.checkExpr(x.Left)
	right := c.checkExpr(x.Right)

	skip := left.Kind == ir.TypeDyn || right.Kind == ir.TypeDyn

	switch x.Op {
	case ast.BinAnd, ast.BinOr:
		if !skip && (left.Kind != ir.TypeBool || right.Kind != ir.TypeBool) {
			c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
		}
		return TypBool
	case ast.BinEq, ast.BinNeq:
		return TypBool
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		if !skip && (!left.IsNumeric() || !right.IsNumeric()) {
			c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
		}
		return TypBool
	case ast.BinAdd:
		if left.Kind == ir.TypeString || right.Kind == ir.TypeString {
			if !skip && (left.Kind != ir.TypeString || right.Kind != ir.TypeString) {
				c.error(x.Pos, "operator + not defined for %s and %s", left, right)
			}
			return TypString
		}
		if !skip && (!left.IsNumeric() || !right.IsNumeric()) {
			c.error(x.Pos, "operator + not defined for %s and %s", left, right)
		}
		return c.narrowNumeric(left, right)
	case ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
		if !skip && (!left.IsNumeric() || !right.IsNumeric()) {
			c.error(x.Pos, "operator %s not defined for %s and %s", binOpStr(x.Op), left, right)
		}
		return c.narrowNumeric(left, right)
	}
	return TypDyn
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

func (c *checker) inferUnary(x *ast.UnaryExpr) *ir.Type {
	operand := c.checkExpr(x.Operand)
	skip := operand.Kind == ir.TypeDyn
	switch x.Op {
	case ast.UnaryNot:
		if !skip && operand.Kind != ir.TypeBool {
			c.error(x.Pos, "operator ! not defined for %s", operand)
		}
		return TypBool
	case ast.UnaryNeg:
		if !skip && !operand.IsNumeric() {
			c.error(x.Pos, "operator - not defined for %s", operand)
		}
		return operand
	}
	return TypDyn
}

func (c *checker) inferTernary(x *ast.TernaryExpr) *ir.Type {
	c.checkExpr(x.Cond)
	then := c.checkExprExpecting(x.Then, c.expected)
	c.checkExprExpecting(x.Else, c.expected)
	return then
}

func (c *checker) inferCall(x *ast.CallExpr) *ir.Type {
	// Check if it's a method call (callee is SelectExpr).
	if sel, ok := x.Func.(*ast.SelectExpr); ok {
		return c.inferMethodCall(sel, x)
	}

	// Check if it's a builtin conversion.
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		switch ident.Name {
		case "int":
			c.checkArgs(x.Args, nil)
			return TypInt
		case "float":
			c.checkArgs(x.Args, nil)
			return TypFloat
		case "string":
			c.checkArgs(x.Args, nil)
			return TypString
		case "bool":
			c.checkArgs(x.Args, nil)
			return TypBool
		}
	}

	// Regular function call.
	calleeType := c.checkExpr(x.Func)

	// Component instantiation: text(value="hi")
	if calleeType.Kind == ir.TypeComponent {
		if comp, ok := calleeType.Decl.(*ir.Component); ok {
			c.validateComponentCallArgs(x, comp)
		}
		return calleeType
	}

	var sig *ir.FuncSig
	if calleeType.Kind == ir.TypeFunc && calleeType.Sig != nil {
		sig = calleeType.Sig
	}

	// Infer generic type params from arguments.
	if sig != nil && len(sig.TypeParams) > 0 {
		sig = c.inferTypeParams(sig, x.Args)
	}

	c.checkArgs(x.Args, sig)

	if sig != nil && sig.Return != nil {
		return sig.Return
	}
	return TypDyn
}

func (c *checker) inferMethodCall(sel *ast.SelectExpr, call *ast.CallExpr) *ir.Type {
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

	receiver := c.checkExpr(sel.Operand)

	// Namespace function call: ns.func().
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			if ns, ok := sym.(*ir.Namespace); ok && ns.Pkg != nil {
				if fsym, ok := ns.Pkg.Symbols.Root.Lookup(sel.Field); ok {
					t := fsym.SymType()
					var sig *ir.FuncSig
					if t != nil && t.Kind == ir.TypeFunc && t.Sig != nil {
						sig = t.Sig
					}
					c.checkArgs(call.Args, sig)
					if sig != nil && sig.Return != nil {
						return sig.Return
					}
					return t
				}
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
		if isStatic {
			// Static call: Type.method(args...) — all args explicit.
			c.checkArgs(call.Args, sig)
		} else if len(sig.Params) > 0 && receiver.IsAssignableTo(sig.Params[0].Type) {
			// Instance call: expr.method(args...) — receiver is implicit first arg.
			shifted := &ir.FuncSig{
				Params:     sig.Params[1:],
				Return:     sig.Return,
				TypeParams: sig.TypeParams,
			}
			c.checkArgs(call.Args, shifted)
		} else {
			c.checkArgs(call.Args, sig)
		}
		if sig.Return != nil {
			return sig.Return
		}
		return TypDyn
	}

	c.checkArgs(call.Args, nil)

	return TypDyn
}

func (c *checker) inferSelect(x *ast.SelectExpr) *ir.Type {
	operand := c.checkExpr(x.Operand)

	switch x.Kind {
	case ast.SelectEvent:
		return TypDyn
	case ast.SelectElemRef:
		return TypDyn
	case ast.SelectField:
		// Namespace member access: ns.field.
		if ident, ok := x.Operand.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if ns, ok := sym.(*ir.Namespace); ok {
					if ns.Pkg == nil {
						// Permissive namespace (platform raw elements).
						return TypDyn
					}
					if fsym, ok := ns.Pkg.Symbols.Root.Lookup(x.Field); ok {
						t := fsym.SymType()
						x.ResolvedType = t.String()
						return t
					}
				}
			}
		}

		// Enum member access: EnumType.member.
		if operand.Kind == ir.TypeEnum && operand.Decl != nil {
			if ed, ok := operand.Decl.(*ir.EnumDef); ok {
				for _, m := range ed.Members {
					if m.Name == x.Field {
						x.ResolvedType = operand.String()
						return operand
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
						x.ResolvedType = f.Type.String()
						return f.Type
					}
				}
				c.error(x.Pos, "no field %q on struct %s", x.Field, sd.Name)
			}
		}
	}

	return TypDyn
}

func (c *checker) inferIndex(x *ast.IndexExpr) *ir.Type {
	operand := c.checkExpr(x.Operand)
	c.checkExpr(x.Index)

	if operand.Kind == ir.TypeList && len(operand.Elems) > 0 {
		t := operand.Elems[0]
		x.ResolvedType = t.String()
		return t
	}
	return TypDyn
}

func (c *checker) inferStructLit(x *ast.StructExpr) *ir.Type {
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
		if sym, ok := c.symtab.LookupType(x.Name); ok {
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
	for _, f := range x.Fields {
		if f.Spread {
			c.checkExpr(f.Value)
			continue
		}
		var expected *ir.Type
		if sd != nil {
			expected = structFieldType(sd, f.Name)
		}
		c.checkExprExpecting(f.Value, expected)
		// Validate field exists on struct.
		if sd != nil && !structHasField(sd, f.Name) {
			c.error(x.Pos, "unknown field %q on struct %s", f.Name, sd.Name)
		}
	}

	if sd != nil {
		return sd.SymType()
	}
	return &ir.Type{Kind: ir.TypeStruct}
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

func (c *checker) inferListLit(x *ast.ListExpr) *ir.Type {
	if len(x.Elements) == 0 {
		return ListOf(TypDyn)
	}
	var elemExpected *ir.Type
	if c.expected != nil && c.expected.Kind == ir.TypeList && len(c.expected.Elems) > 0 {
		elemExpected = c.expected.Elems[0]
	}
	elem := c.checkExprExpecting(x.Elements[0], elemExpected)
	for _, e := range x.Elements[1:] {
		c.checkExprExpecting(e, elemExpected)
	}
	return ListOf(elem)
}

func (c *checker) inferLambda(x *ast.LambdaExpr) *ir.Type {
	fn := &ir.Func{
		Params: c.buildParams(x.Params),
		Return: c.resolveType(x.ReturnType),
		Body:   x.Body,
	}
	if x.Block.IsDefined() {
		fn.ASTBlock = &x.Block
	}

	// Type-check the lambda body in a child scope.
	c.pushScope()
	for _, p := range fn.Params {
		c.scope.Declare(p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	if fn.Body != nil {
		c.checkExprExpecting(fn.Body, fn.Return)
	}
	if fn.ASTBlock != nil {
		c.checkBlock(fn.ASTBlock)
	}
	c.returnType = prevReturn
	c.popScope()

	return &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}
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
		argType := c.checkExpr(arg.Value)
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

func (c *checker) inferEventRef(x *ast.EventRefExpr) *ir.Type {
	if c.currentComponent != nil {
		for _, evt := range c.currentComponent.Events {
			if evt.Name == x.Name {
				if evt.Type != nil {
					return evt.Type
				}
				return TypDyn
			}
		}
	}
	return TypDyn
}

// checkArgs type-checks all arguments in an ArgList.
// If sig is non-nil, validates positional arg types and arity against it.
func (c *checker) checkArgs(args ast.ArgList, sig *ir.FuncSig) {
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
				argType := c.checkExprExpecting(arg.Value, expected)
				// Validate positional arg type against param.
				if sig != nil && arg.Name == "" && positional < len(sig.Params) {
					paramType := sig.Params[positional].Type
					if argType.Kind != ir.TypeDyn && paramType.Kind != ir.TypeDyn && !argType.IsAssignableTo(paramType) {
						if callExpr, _ := c.implicitCall(arg.Value, argType, paramType); callExpr != nil {
							args.Args[i] = ast.Arg{Name: arg.Name, Value: callExpr}
						} else {
							c.error(*arg.Value.ExprPos(), "argument %d: cannot pass %s as %s", positional+1, argType, paramType)
						}
					}
				}
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
			if !p.HasDefault {
				required++
			}
		}
		if positional < required {
			c.error(args.Pos, "expected %d arguments, got %d", required, positional)
		} else if positional > len(sig.Params) {
			c.error(args.Pos, "expected %d arguments, got %d", len(sig.Params), positional)
		}
	}
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
		if ir := c.checkStmt(stmt); ir != nil {
			out = append(out, ir)
		}
	}
	return out
}

// checkStmt type-checks a single statement and returns its IR form.
// Returns nil for declarations (registered on scope) and skipped nodes.
func (c *checker) checkStmt(s ast.Stmt) ir.Stmt {
	switch x := s.(type) {
	case *ast.AssignStmt:
		targetType := c.checkExpr(x.Target)
		valueType := c.checkExprExpecting(x.Value, targetType)
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
		return &ir.Assign{AST: x}
	case *ast.ToggleStmt:
		c.checkExpr(x.Target)
		return &ir.Toggle{AST: x}
	case *ast.EmitStmt:
		c.checkArgs(x.Args, nil)
		return &ir.Emit{AST: x}
	case *ast.VarStmt:
		typ := c.resolveType(x.Type)
		if x.Init != nil {
			initType := c.checkExprExpecting(x.Init, typ)
			if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		c.scope.Declare(&ir.Var{
			AST:  x,
			Name: x.Name,
			Type: typ,
		})
		return &ir.LocalVar{AST: x, Type: typ}
	case *ast.ReturnStmt:
		if x.Value != nil {
			valType := c.checkExprExpecting(x.Value, c.returnType)
			if c.returnType != nil && c.returnType.Kind != ir.TypeDyn && valType.Kind != ir.TypeDyn && !valType.IsAssignableTo(c.returnType) {
				c.error(x.Pos, "cannot return %s as %s", valType, c.returnType)
			}
		}
		return &ir.Return{AST: x}
	case *ast.CallStmt:
		c.checkExpr(x.Call)
		return c.resolveCallStmt(x)
	case *ast.IfStmt:
		condType := c.checkExpr(x.Cond)
		if condType.Kind != ir.TypeDyn && condType.Kind != ir.TypeBool {
			c.error(x.Pos, "if condition must be bool, got %s", condType)
		}
		body := c.checkBlockIR(&x.Body)
		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			elseBody = c.checkBlockIR(&x.Else)
		}
		return &ir.If{AST: x, Body: body, Else: elseBody}
	case *ast.ForStmt:
		iter := c.checkExpr(x.Iter)
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
		return &ir.For{AST: x, ElemType: elemType, Body: body, Else: elseBody}
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

	docs := t.Package()
	if len(docs) == 0 {
		return nil
	}

	scope := NewScope(nil)
	for _, doc := range docs {
		// Check each doc to get IR, then populate scope from its declarations.
		pkg, _ := Check(doc, &Config{})
		if pkg == nil {
			continue
		}
		for _, sd := range pkg.Structs {
			scope.Declare(sd)
		}
		for _, ed := range pkg.Enums {
			scope.Declare(ed)
		}
		for _, ud := range pkg.Units {
			scope.Declare(ud)
		}
		for _, fn := range pkg.Funcs {
			scope.Declare(fn)
		}
		for _, comp := range pkg.Components {
			scope.Declare(comp)
		}
		for _, v := range pkg.Vars {
			scope.Declare(v)
		}
		for _, v := range pkg.Consts {
			scope.Declare(v)
		}
	}

	if c.platformScopeCache == nil {
		c.platformScopeCache = make(map[string]*ir.Scope)
	}
	c.platformScopeCache[platform] = scope

	// Return a clone for this usage.
	clone := NewScope(nil)
	maps.Copy(clone.Symbols, scope.Symbols)
	return clone
}

// resolveCallStmt determines whether an ast.CallStmt is a void function call
// or a component instantiation (when the parser produced CallStmt for Foo()
// that is actually a component). Returns the appropriate IR statement.
func (c *checker) resolveCallStmt(x *ast.CallStmt) ir.Stmt {
	// Check if the call target is a component name.
	if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
		if sym, ok := c.symtab.LookupComponent(id.Name); ok {
			if comp, ok := sym.(*ir.Component); ok {
				return &ir.NodeInst{
					AST:       x,
					Name:      id.Name,
					Component: comp,
					Args:      x.Call.Args,
				}
			}
		}
	}
	// Resolve function if possible.
	var fn *ir.Func
	if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(id.Name); ok {
			if f, ok := sym.(*ir.Func); ok {
				fn = f
			}
		}
	}
	return &ir.CallStmt{AST: x, Call: x.Call, Func: fn}
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

	body := c.checkBlockIR(&s.Body)
	return &ir.PlatformFilter{AST: s, Body: body}
}

// checkVisualNodeIR validates a visual node and returns the appropriate IR statement.
// Disambiguates components, platform elements, slots, and function calls.
func (c *checker) checkVisualNodeIR(vn *ast.VisualNode) ir.Stmt {
	name := visualNodeTarget(vn)

	// Special root-ish nodes — registered on package, not returned as IR stmts.
	switch name {
	case "window":
		w := c.buildWindow(vn)
		c.pkg.Windows = append(c.pkg.Windows, w)
		c.checkWindowBody(w)
		w.Checked = true
		return nil
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

	// Look up component.
	var comp *ir.Component
	if sym, ok := c.symtab.LookupComponent(name); ok {
		if co, ok := sym.(*ir.Component); ok {
			comp = co
		}
	}

	// If not a component, check if it's a function in scope.
	if comp == nil {
		if sym, ok := c.scope.Lookup(name); ok {
			if fn, ok := sym.(*ir.Func); ok {
				// This is a function call, not a visual node.
				c.checkVisualNodeArgs(vn.Args, nil)
				return &ir.CallStmt{AST: vn, Func: fn}
			}
		}
	}

	// Component or platform element.
	c.checkVisualNodeArgs(vn.Args, comp)
	if comp != nil {
		c.validateVisualNodeProps(vn, comp)
	}

	children := c.checkBlockIR(&vn.Block)
	return &ir.NodeInst{
		AST:       vn,
		Name:      name,
		Component: comp,
		Args:      vn.Args,
		Children:  children,
		ID:        vn.ID,
	}
}

// implicitCall checks whether actual is a zero-arg func whose return type is
// assignable to expected. If so it wraps expr in a CallExpr, updates TypeMap,
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
	ret := actual.Sig.Return
	c.pkg.TypeMap[call] = ret
	return call, ret
}

// checkVisualNodeArgs type-checks args, threading expected types from component props.
func (c *checker) checkVisualNodeArgs(args ast.ArgList, comp *ir.Component) {
	for i, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				var expected *ir.Type
				if comp != nil && arg.Name != "" {
					expected = componentPropType(comp, arg.Name)
				}
				actual := c.checkExprExpecting(arg.Value, expected)
				if expected != nil && actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
					if call, ret := c.implicitCall(arg.Value, actual, expected); call != nil {
						args.Args[i] = ast.Arg{Name: arg.Name, Value: call}
						actual = ret
					} else {
						c.error(*arg.Value.ExprPos(), "cannot pass %s as %s", actual, expected)
					}
				}
			}
		case ast.EventHandler:
			c.pushScope()
			for _, p := range arg.Params.Params {
				typ := c.resolveType(p.Type)
				if typ.Kind == ir.TypeDyn && comp != nil {
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
		}
	}
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
			if arg.Name == "" {
				continue // positional args
			}
			if !componentHasProp(comp, arg.Name) && !componentHasEvent(comp, arg.Name) {
				c.error(vn.Pos, "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			if !componentHasEvent(comp, arg.Name) {
				c.error(vn.Pos, "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}
}

// validateComponentCallArgs validates a component call (text(value="hi"))
// against the component's prop and event declarations.
func (c *checker) validateComponentCallArgs(call *ast.CallExpr, comp *ir.Component) {
	for i, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				var expected *ir.Type
				if arg.Name != "" {
					expected = componentPropType(comp, arg.Name)
				}
				actual := c.checkExprExpecting(arg.Value, expected)
				if expected != nil && actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
					if callExpr, ret := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
						call.Args.Args[i] = ast.Arg{Name: arg.Name, Value: callExpr}
						actual = ret
					} else {
						c.error(*arg.Value.ExprPos(), "cannot pass %s as %s", actual, expected)
					}
				}
			}
			if arg.Name == "" {
				continue // positional args
			}
			if !componentHasProp(comp, arg.Name) && !componentHasEvent(comp, arg.Name) {
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
