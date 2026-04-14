package checker

import "git.duckfam.us/jonathan/sngl/ast"

// checkExpr infers the type of an expression, stores it in TypeMap, and returns it.
func (c *checker) checkExpr(e ast.Expr) *Type {
	if e == nil {
		return TypDyn
	}
	t := c.inferExpr(e)
	c.pkg.TypeMap[e] = t
	return t
}

// inferExpr dispatches on expression type to infer its type.
func (c *checker) inferExpr(e ast.Expr) *Type {
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

func (c *checker) inferLiteral(x *ast.LiteralExpr) *Type {
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

func (c *checker) inferUnitLiteral(x *ast.UnitLiteral) *Type {
	ud, ok := c.unitBySuffix[x.Suffix]
	if !ok {
		c.error(x.Pos, "unknown unit suffix %q", x.Suffix)
		return TypDyn
	}
	return ud.SymType()
}

func (c *checker) inferIdent(x *ast.IdentExpr) *Type {
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
		c.error(x.Pos, "undefined: %s", x.Name)
		return TypDyn
	}
	if t := sym.SymType(); t != nil {
		return t
	}
	return TypDyn
}

func (c *checker) inferBinary(x *ast.BinaryExpr) *Type {
	left := c.checkExpr(x.Left)
	right := c.checkExpr(x.Right)

	skip := left.Kind == TypeDyn || right.Kind == TypeDyn

	switch x.Op {
	case ast.BinAnd, ast.BinOr:
		if !skip && (left.Kind != TypeBool || right.Kind != TypeBool) {
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
		if left.Kind == TypeString || right.Kind == TypeString {
			if !skip && (left.Kind != TypeString || right.Kind != TypeString) {
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
func (c *checker) narrowNumeric(left, right *Type) *Type {
	if left.Kind == TypeFloat || right.Kind == TypeFloat {
		return TypFloat
	}
	if left.Kind == TypeInt || right.Kind == TypeInt {
		return TypInt
	}
	if left.Kind == TypeUnit {
		return left
	}
	if right.Kind == TypeUnit {
		return right
	}
	return TypDyn
}

func (c *checker) inferUnary(x *ast.UnaryExpr) *Type {
	operand := c.checkExpr(x.Operand)
	skip := operand.Kind == TypeDyn
	switch x.Op {
	case ast.UnaryNot:
		if !skip && operand.Kind != TypeBool {
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

func (c *checker) inferTernary(x *ast.TernaryExpr) *Type {
	c.checkExpr(x.Cond)
	then := c.checkExpr(x.Then)
	c.checkExpr(x.Else)
	return then
}

func (c *checker) inferCall(x *ast.CallExpr) *Type {
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
	if calleeType.Kind == TypeComponent {
		if comp, ok := calleeType.Decl.(*Component); ok {
			c.validateComponentCallArgs(x, comp)
		}
		return calleeType
	}

	var sig *FuncSig
	if calleeType.Kind == TypeFunc && calleeType.Sig != nil {
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

func (c *checker) inferMethodCall(sel *ast.SelectExpr, call *ast.CallExpr) *Type {
	// Determine if operand is a type name (static call) vs a value (instance call).
	isStatic := false
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			switch sym.(type) {
			case *TypeSym, *StructDef, *EnumDef, *UnitDef:
				isStatic = true
			}
		}
	}

	receiver := c.checkExpr(sel.Operand)

	// Namespace function call: ns.func().
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			if ns, ok := sym.(*Namespace); ok && ns.Pkg != nil {
				if fsym, ok := ns.Pkg.Symbols.Root.Lookup(sel.Field); ok {
					t := fsym.SymType()
					var sig *FuncSig
					if t != nil && t.Kind == TypeFunc && t.Sig != nil {
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
		case TypeList:
			fn, ok = c.symtab.LookupMethod("list", sel.Field)
		case TypeOption:
			fn, ok = c.symtab.LookupMethod("option", sel.Field)
		}
	}
	if ok {
		sig := fn.FuncSig()
		if len(sig.TypeParams) > 0 {
			// For instance calls, bind receiver to param[0] before inferring
			// from explicit args so that e.g. list<int>.length() binds T=int.
			if !isStatic && len(sig.Params) > 0 {
				bindings := make(map[string]*Type)
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
			shifted := &FuncSig{
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

func (c *checker) inferSelect(x *ast.SelectExpr) *Type {
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
				if ns, ok := sym.(*Namespace); ok && ns.Pkg != nil {
					if fsym, ok := ns.Pkg.Symbols.Root.Lookup(x.Field); ok {
						t := fsym.SymType()
						x.ResolvedType = t.String()
						return t
					}
				}
			}
		}

		// Struct field access.
		if operand.Kind == TypeStruct && operand.Decl != nil {
			if sd, ok := operand.Decl.(*StructDef); ok {
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

func (c *checker) inferIndex(x *ast.IndexExpr) *Type {
	operand := c.checkExpr(x.Operand)
	c.checkExpr(x.Index)

	if operand.Kind == TypeList && len(operand.Elems) > 0 {
		t := operand.Elems[0]
		x.ResolvedType = t.String()
		return t
	}
	return TypDyn
}

func (c *checker) inferStructLit(x *ast.StructExpr) *Type {
	// Look up struct type.
	var sd *StructDef
	if x.Package != "" {
		// Qualified: pkg.Struct{...}
		if sym, ok := c.scope.Lookup(x.Package); ok {
			if ns, ok := sym.(*Namespace); ok && ns.Pkg != nil {
				if tsym, ok := ns.Pkg.Symbols.LookupType(x.Name); ok {
					if s, ok := tsym.(*StructDef); ok {
						sd = s
					}
				}
			}
		}
	} else if sym, ok := c.symtab.LookupType(x.Name); ok {
		if s, ok := sym.(*StructDef); ok {
			sd = s
		}
	}

	// Check field values.
	for _, f := range x.Fields {
		if f.Spread {
			c.checkExpr(f.Value)
			continue
		}
		c.checkExpr(f.Value)
		// Validate field exists on struct.
		if sd != nil && !structHasField(sd, f.Name) {
			c.error(x.Pos, "unknown field %q on struct %s", f.Name, x.Name)
		}
	}

	if sd != nil {
		return sd.SymType()
	}
	return &Type{Kind: TypeStruct}
}

func structHasField(sd *StructDef, name string) bool {
	for _, f := range sd.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func (c *checker) inferListLit(x *ast.ListExpr) *Type {
	if len(x.Elements) == 0 {
		return ListOf(TypDyn)
	}
	elem := c.checkExpr(x.Elements[0])
	for _, e := range x.Elements[1:] {
		c.checkExpr(e)
	}
	return ListOf(elem)
}

func (c *checker) inferLambda(x *ast.LambdaExpr) *Type {
	fn := &Func{
		Params: c.buildParams(x.Params),
		Return: c.resolveType(x.ReturnType),
		Body:   x.Body,
		Pos:    x.Pos,
	}
	if x.Block.IsDefined() {
		fn.Block = &x.Block
	}

	// Type-check the lambda body in a child scope.
	c.pushScope()
	for _, p := range fn.Params {
		c.scope.Declare(p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	if fn.Body != nil {
		c.checkExpr(fn.Body)
	}
	if fn.Block != nil {
		c.checkBlock(fn.Block)
	}
	c.returnType = prevReturn
	c.popScope()

	return &Type{Kind: TypeFunc, Sig: fn.FuncSig()}
}

// inferTypeParams infers concrete types for generic type params by matching
// argument types against parameter types, then returns a substituted FuncSig.
func (c *checker) inferTypeParams(sig *FuncSig, args ast.ArgList) *FuncSig {
	bindings := make(map[string]*Type)
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
		if argType.Kind != TypeDyn {
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
func bindTypeParams(param, arg *Type, bindings map[string]*Type) {
	if param == nil || arg == nil {
		return
	}
	if param.Kind == TypeTypeParam {
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

func (c *checker) inferEventRef(x *ast.EventRefExpr) *Type {
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
func (c *checker) checkArgs(args ast.ArgList, sig *FuncSig) {
	positional := 0
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				argType := c.checkExpr(arg.Value)
				// Validate positional arg type against param.
				if sig != nil && arg.Name == "" && positional < len(sig.Params) {
					paramType := sig.Params[positional].Type
					if argType.Kind != TypeDyn && paramType.Kind != TypeDyn && !argType.IsAssignableTo(paramType) {
						c.error(*arg.Value.ExprPos(), "argument %d: cannot pass %s as %s", positional+1, argType, paramType)
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
				c.scope.Declare(&Param{
					Name: p.Name,
					Type: c.resolveType(p.Type),
					Pos:  p.Pos,
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

// checkBlock type-checks all statements in a StmtBlock.
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

// checkStmt type-checks a single statement.
func (c *checker) checkStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.AssignStmt:
		targetType := c.checkExpr(x.Target)
		valueType := c.checkExpr(x.Value)
		// Const reassignment check.
		if ident, ok := x.Target.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if v, ok := sym.(*Var); ok && v.IsConst {
					c.error(x.Pos, "cannot assign to const %q", ident.Name)
				}
			}
		}
		// Type checking.
		if x.Op == ast.AssignSet {
			if targetType.Kind != TypeDyn && valueType.Kind != TypeDyn && !valueType.IsAssignableTo(targetType) {
				c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
			}
		} else {
			// Compound assignment: both sides must be numeric (or string for +=).
			if targetType.Kind != TypeDyn && valueType.Kind != TypeDyn {
				if x.Op == ast.AssignAdd && targetType.Kind == TypeString {
					// string += string is fine.
				} else if !targetType.IsNumeric() || !valueType.IsNumeric() {
					c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
				}
			}
		}
	case *ast.ToggleStmt:
		c.checkExpr(x.Target)
	case *ast.EmitStmt:
		c.checkArgs(x.Args, nil)
	case *ast.VarStmt:
		typ := c.resolveType(x.Type)
		if x.Init != nil {
			initType := c.checkExpr(x.Init)
			if typ.Kind == TypeDyn {
				typ = initType
			}
		}
		c.scope.Declare(&Var{
			AST:  x,
			Name: x.Name,
			Type: typ,
			Pos:  x.Pos,
		})
	case *ast.ReturnStmt:
		if x.Value != nil {
			valType := c.checkExpr(x.Value)
			if c.returnType != nil && c.returnType.Kind != TypeDyn && valType.Kind != TypeDyn && !valType.IsAssignableTo(c.returnType) {
				c.error(x.Pos, "cannot return %s as %s", valType, c.returnType)
			}
		}
	case *ast.CallStmt:
		c.checkExpr(x.Call)
	case *ast.IfStmt:
		condType := c.checkExpr(x.Cond)
		if condType.Kind != TypeDyn && condType.Kind != TypeBool {
			c.error(x.Pos, "if condition must be bool, got %s", condType)
		}
		c.checkBlock(&x.Body)
		if x.Else.IsDefined() {
			c.checkBlock(&x.Else)
		}
	case *ast.ForStmt:
		iter := c.checkExpr(x.Iter)
		if iter.Kind != TypeDyn && iter.Kind != TypeList {
			c.error(x.Pos, "for iterator must be list, got %s", iter)
		}
		c.pushScope()
		// Declare loop variables.
		elemType := TypDyn
		if iter.Kind == TypeList && len(iter.Elems) > 0 {
			elemType = iter.Elems[0]
		}
		if x.Value != "" {
			// for key, value = iter: key is index, value is element.
			c.scope.Declare(&LoopVar{Name: x.Key, Type: TypInt, Pos: x.Pos})
			c.scope.Declare(&LoopVar{Name: x.Value, Type: elemType, Pos: x.Pos})
		} else {
			// for item = iter: item is element.
			c.scope.Declare(&LoopVar{Name: x.Key, Type: elemType, Pos: x.Pos})
		}
		c.checkBlock(&x.Body)
		if x.Else.IsDefined() {
			c.checkBlock(&x.Else)
		}
		c.popScope()
	case *ast.PlatformStmt:
		c.checkPlatformStmt(x)
	case *ast.VisualNode:
		c.checkVisualNode(x)
	case *ast.ConstDecl:
		c.registerConsts(x)
	case *ast.VarDecl:
		c.registerVars(x)
	case *ast.FuncDef:
		fn := c.buildFunc(x)
		c.scope.Declare(fn)
		c.checkFuncBody(fn)
	case *ast.Comment:
		// Skip.
	case *ast.DisabledDecl:
		// Skip.
	}
}

// checkPlatformStmt type-checks a platform statement body, injecting the
// platform's package scope as a fallback for unresolved identifiers.
func (c *checker) checkPlatformStmt(s *ast.PlatformStmt) {
	// Skip body when target platform is known and doesn't match.
	if c.cfg.Target != nil && c.cfg.Target.Platform != "" && c.cfg.Target.Platform != s.Platform {
		return
	}

	// Inject platform package scope as fallback between current scope and its parent.
	platformScope := c.buildPlatformPkgScope(s.Platform)
	if platformScope != nil {
		savedParent := c.scope.parent
		platformScope.parent = savedParent
		c.scope.parent = platformScope
		defer func() { c.scope.parent = savedParent }()
	}

	c.checkBlock(&s.Body)
}

// buildPlatformPkgScope builds (and caches) a scope containing declarations
// from the named platform's Package() docs.
func (c *checker) buildPlatformPkgScope(platform string) *Scope {
	if c.platformScopeCache != nil {
		if s, ok := c.platformScopeCache[platform]; ok {
			// Clone so each insertion point gets its own parent chain.
			clone := NewScope(nil)
			for k, v := range s.symbols {
				clone.symbols[k] = v
			}
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
		c.platformScopeCache = make(map[string]*Scope)
	}
	c.platformScopeCache[platform] = scope

	// Return a clone for this usage.
	clone := NewScope(nil)
	for k, v := range scope.symbols {
		clone.symbols[k] = v
	}
	return clone
}

// checkVisualNode validates a visual node's props, events, and children.
func (c *checker) checkVisualNode(vn *ast.VisualNode) {
	// Resolve target component.
	name := visualNodeTarget(vn)

	// Special root-ish nodes that can appear in if/for at top level.
	switch name {
	case "window":
		w := c.buildWindow(vn)
		if c.currentComponent != nil {
			// Component-level window — collected on the component.
		} else {
			c.pkg.Windows = append(c.pkg.Windows, w)
		}
		c.checkWindowBody(w)
		return
	case "timer":
		t := c.buildTimer(vn)
		if c.currentComponent != nil {
			c.currentComponent.Timers = append(c.currentComponent.Timers, t)
		} else {
			c.pkg.Timers = append(c.pkg.Timers, t)
		}
		return
	case "output":
		if !c.cfg.IsMain {
			c.error(vn.Pos, "output declarations only permitted in main file")
			return
		}
		c.buildOutputs(vn)
		return
	}

	// Look up component.
	var comp *Component
	if sym, ok := c.symtab.LookupComponent(name); ok {
		if co, ok := sym.(*Component); ok {
			comp = co
		}
	}

	// Check args (props + events).
	c.checkArgs(vn.Args, nil)

	// Validate props against component definition.
	if comp != nil {
		c.validateVisualNodeProps(vn, comp)
	}

	// Check block body (children).
	if vn.Block.IsDefined() {
		c.pushScope()
		for _, stmt := range vn.Block.Stmts {
			c.checkStmt(stmt)
		}
		c.popScope()
	}
}

// validateVisualNodeProps validates props and events against a component definition.
func (c *checker) validateVisualNodeProps(vn *ast.VisualNode, comp *Component) {
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
func (c *checker) validateComponentCallArgs(call *ast.CallExpr, comp *Component) {
	for _, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Value != nil {
				c.checkExpr(arg.Value)
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
				c.scope.Declare(&Param{
					Name: p.Name,
					Type: c.resolveType(p.Type),
					Pos:  p.Pos,
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

func componentHasProp(comp *Component, name string) bool {
	for _, p := range comp.Props {
		if p.Name == name {
			return true
		}
	}
	return false
}

func componentHasEvent(comp *Component, name string) bool {
	for _, e := range comp.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}
