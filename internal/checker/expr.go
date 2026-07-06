package checker

import (
	"strconv"
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

// requireValueType errors if t is TypeVoid, covering the case where a call to
// a void function is used where a value is expected. Returns true when an
// error was emitted so callers can skip further checks against a meaningless
// type.
func (c *checker) requireValueType(t *ir.Type, pos ast.Pos) bool {
	if t != nil && t.Kind == ir.TypeVoid {
		c.error(pos, "expression yields no value")
		return true
	}
	return false
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

// isAddressableListExpr reports whether e denotes a mutable, addressable list:
// a (non-const) var, a component field, or a field/element path rooted at one.
// Computed lists (filter/map/method results) and const lists are not
// addressable, so an &-bound loop variable can't write through them.
func isAddressableListExpr(e ir.Expr) bool {
	switch n := e.(type) {
	case *ir.Ident:
		switch sym := n.Sym.(type) {
		case *ir.Var:
			return !sym.IsConst
		case *ir.Component:
			return true // component self; fields on it are mutable state
		case *ir.Param:
			return sym.Name == ir.ReceiverParam // implicit component receiver
		case *ir.LoopVar:
			return true // element of an outer &-bound loop (nested addressable)
		}
		return false
	case *ir.Select:
		return isAddressableListExpr(n.Operand)
	case *ir.Index:
		return isAddressableListExpr(n.Operand)
	case *ir.Conversion:
		return isAddressableListExpr(n.Operand)
	case *ir.Unary:
		// Deref of a ref (e.g. an outer &t) — follow through.
		return isAddressableListExpr(n.Operand)
	}
	return false
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
	case *ast.MapLit:
		return c.inferMapLit(x)
	case *ast.InterpolationExpr:
		return c.inferInterpolation(x)
	case *ast.I18nInterpExpr:
		return c.inferI18nInterp(x)
	case *ast.LambdaExpr:
		return c.inferLambda(x)
	case *ast.SpreadExpr:
		operand := c.checkExpr(x.Operand)
		return &ir.Spread{AST: x, Type: exprType(operand), Operand: operand}
	case *ast.ParenExpr:
		return c.checkExpr(x.Inner)
	case *ast.ConstExpr:
		return c.inferConstExpr(x)
	case *ast.EventRefExpr:
		return c.inferEventRef(x)
	default:
		return &ir.Ident{Type: TypDyn}
	}
}

// inferConstExpr checks the operand of a const(expr) assertion. The operand
// must be a constant-foldable expression; the wrapper is erased after the
// assertion so downstream phases see the operand directly. The const-ness
// check is deferred to after purity analysis, since whether a call is
// foldable depends on the callee's purity.
func (c *checker) inferConstExpr(x *ast.ConstExpr) ir.Expr {
	operand := c.checkExpr(x.Operand)
	if operand == nil {
		return nil
	}
	c.constAsserts = append(c.constAsserts, constAssertion{pos: x.Pos, operand: operand})
	return operand
}

func (c *checker) inferLiteral(x *ast.LiteralExpr) ir.Expr {
	var typ *ir.Type
	switch x.Kind {
	case ast.LiteralInt:
		typ = TypInt
		// Detect literals outside int64 range. A leading '-' is a separate
		// UnaryExpr, so Raw is always the unsigned magnitude here.
		if _, err := strconv.ParseInt(x.Raw, 0, 64); err != nil {
			if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
				c.error(x.Pos, "integer literal %s overflows int (max %d)", x.Raw, int64(^uint64(0)>>1))
			}
		}
	case ast.LiteralFloat:
		typ = TypFloat
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		typ = TypString
	case ast.LiteralBool:
		typ = TypBool
	case ast.LiteralNull:
		typ = TypNull
	case ast.LiteralColor:
		return c.lowerHexLiteral(x)
	default:
		typ = TypDyn
	}
	return &ir.Literal{AST: x, Type: typ, Raw: x.Raw}
}

// lowerHexLiteral converts a hex color literal like #ff8040 to a
// *ir.StructLit{Def:colorStructDef, Fields:[r,g,b,a]} so downstream
// code sees the same shape as color{r=,g=,b=,a=} struct literals.
// Falls back to a Literal form if the color struct isn't yet in scope
// (early checker phases, e.g. when checking the stdlib itself).
func (c *checker) lowerHexLiteral(x *ast.LiteralExpr) ir.Expr {
	r, g, b, a, ok := parseHexChannels(x.Raw)
	if !ok {
		c.error(x.Pos, "invalid color literal %q", x.Raw)
		return &ir.Literal{AST: x, Type: TypString, Raw: x.Raw}
	}
	sym, ok := c.scope.Lookup("color")
	if !ok {
		// Stdlib not yet registered. Carry the raw hex as a string literal;
		// callers in this state are bootstrap paths that don't propagate.
		return &ir.Literal{AST: x, Type: TypString, Raw: x.Raw}
	}
	sd, ok := sym.(*ir.StructDef)
	if !ok {
		return &ir.Literal{AST: x, Type: TypString, Raw: x.Raw}
	}
	intLit := func(n int) *ir.Literal {
		return &ir.Literal{Type: TypInt, Raw: strconv.Itoa(n)}
	}
	return &ir.StructLit{
		AST:  &ast.StructExpr{Pos: x.Pos, Name: "color"},
		Type: sd.SymType(),
		Def:  sd,
		Fields: []ir.FieldInit{
			{Name: "r", Value: intLit(r)},
			{Name: "g", Value: intLit(g)},
			{Name: "b", Value: intLit(b)},
			{Name: "a", Value: intLit(a)},
		},
	}
}

// parseHexChannels parses #rgb, #rgba, #rrggbb, or #rrggbbaa to 0..255
// channels. The 3- and 4-digit short forms double each nibble (CSS-style, so
// #fff is #ffffff and #f00a is #ff0000aa). Alpha defaults to 255 for the
// 3- and 6-digit forms.
func parseHexChannels(raw string) (r, g, b, a int, ok bool) {
	if len(raw) == 0 || raw[0] != '#' {
		return 0, 0, 0, 0, false
	}
	hex := raw[1:]
	nibble := func(c byte) (int, bool) {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0'), true
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10, true
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10, true
		}
		return 0, false
	}
	byteOf := func(hi, lo byte) (int, bool) {
		h, ok1 := nibble(hi)
		l, ok2 := nibble(lo)
		if !(ok1 && ok2) {
			return 0, false
		}
		return h*16 + l, true
	}
	switch len(hex) {
	case 3:
		rv, ok1 := nibble(hex[0])
		gv, ok2 := nibble(hex[1])
		bv, ok3 := nibble(hex[2])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv*16 + rv, gv*16 + gv, bv*16 + bv, 255, true
	case 4:
		rv, ok1 := nibble(hex[0])
		gv, ok2 := nibble(hex[1])
		bv, ok3 := nibble(hex[2])
		av, ok4 := nibble(hex[3])
		if !(ok1 && ok2 && ok3 && ok4) {
			return 0, 0, 0, 0, false
		}
		return rv*16 + rv, gv*16 + gv, bv*16 + bv, av*16 + av, true
	case 6:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, 255, true
	case 8:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		av, ok4 := byteOf(hex[6], hex[7])
		if !(ok1 && ok2 && ok3 && ok4) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, av, true
	}
	return 0, 0, 0, 0, false
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
		// `this`-elision: bare name resolves to `this.<name>` inside a method
		// body when the receiver's type has a field/member/var by that name.
		// Method names (sibling calls) handled in inferCall.
		if sel := c.elideThis(x); sel != nil {
			return c.inferSelect(sel)
		}
		// Component-body bare reference: inside a component body (no `this`),
		// a bare name like `remaining` may refer to a nested method on the
		// current component. Resolve it as if it were a getter — return the
		// method's return type (the surrounding interpolation / assignment
		// will treat it as a value the same way it did pre-T7, when these
		// were closures registered on scope).
		if c.currentComponent != nil {
			if fn, ok := c.symtab.LookupMethod(c.currentComponent.Name, x.Name); ok {
				// Bare reference to a sibling component-method. Expose a func
				// type with the synthetic `this` stripped, so implicit-call
				// paths (interpolation, prop binding) and explicit `name()`
				// calls both see a callable shape consistent with the source
				// declaration.
				sig := fn.FuncSig()
				if len(sig.Params) > 0 && sig.Params[0].Receiver {
					sig = &ir.FuncSig{
						Params:     sig.Params[1:],
						Return:     sig.Return,
						TypeParams: sig.TypeParams,
						Purity:     sig.Purity,
					}
				}
				funcType := &ir.Type{Kind: ir.TypeFunc, Sig: sig}
				return &ir.Ident{AST: x, Type: funcType, Name: x.Name, Sym: fn}
			}
		}
		c.error(x.Pos, "undefined: %s", x.Name)
		return &ir.Ident{AST: x, Type: TypDyn, Name: x.Name}
	}
	// The export rule only governs cross-package access: an unexported
	// (`_`-prefixed) name is freely referenceable within the package that
	// declares it. A bare identifier that resolves inside the current package
	// (rather than the stdlib/prelude scopes above it) is therefore never an
	// export violation — this is also what lets `#id`-tagged node handles,
	// including synthesized `__nN` ids, resolve by bare name.
	if !c.resolvedInPackage(x.Name) {
		c.rejectUnexported(x.Pos, sym)
	}
	c.reportUnusable(x.Pos, x.Name, sym)
	if ctx, ok := sym.(*ir.Context); ok {
		typ := ctx.Typ
		if typ == nil {
			typ = TypDyn
		}
		return &ir.ContextRead{AST: x, Ref: ctx, Typ: typ}
	}
	t := sym.SymType()
	if t == nil {
		t = TypDyn
	}
	ident := &ir.Ident{AST: x, Type: t, Name: x.Name, Sym: sym}
	// An &-bound loop variable has type ref<T>. Auto-deref it to T (an explicit
	// Unary{Deref}, mirroring Select-operand deref) so reads type-check as the
	// element type and assignment targets resolve to the dereferenced element.
	// Lowering rewrites the ref to indexed list access. Only loop vars are
	// ref-typed at check time, so this never affects other identifiers.
	if _, isLoop := sym.(*ir.LoopVar); isLoop && t.Kind == ir.TypeRef && len(t.Elems) > 0 {
		return &ir.Unary{Type: t.Elems[0], Op: ast.UnaryDeref, Operand: ident}
	}
	return ident
}

// exported reports whether a looked-up symbol is exported. Symbols that
// don't implement the Exported interface (namespaces, type parameters,
// etc.) are treated as exported.
func exported(sym ir.Symbol) bool {
	type exportable interface{ IsExported() bool }
	if e, ok := sym.(exportable); ok {
		return e.IsExported()
	}
	return true
}

// rejectUnexported emits an error if sym is an unexported declaration.
// Returns true when it emitted an error, so callers can fall through to
// a diagnostic-friendly default.
func (c *checker) rejectUnexported(pos ast.Pos, sym ir.Symbol) bool {
	if exported(sym) {
		return false
	}
	c.error(pos, "cannot refer to unexported identifier %q", sym.SymName())
	return true
}

// resolvedInPackage reports whether name resolves to a symbol declared within
// the current package — anywhere from the active scope up to and including the
// package root, but not in the stdlib/prelude scopes above it. The package
// root's parent is the boundary: scopes at or beyond it belong to another
// package (the stdlib prelude or an import), where the export rule applies.
func (c *checker) resolvedInPackage(name string) bool {
	boundary := c.symtab.Root.Parent
	for s := c.scope; s != nil && s != boundary; s = s.Parent {
		if _, ok := s.LookupLocal(name); ok {
			return true
		}
	}
	return false
}

// reportUnusable emits a diagnostic if sym is a scheme-imported declaration
// that cannot be modelled precisely in SNGL.
func (c *checker) reportUnusable(pos ast.Pos, name string, sym ir.Symbol) {
	var reason string
	switch s := sym.(type) {
	case *ir.Func:
		reason = s.Unusable
	case *ir.Var:
		reason = s.Unusable
	}
	if reason != "" {
		c.error(pos, "%s cannot be used: %s", name, reason)
	}
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

	// Implicit call: a bare zero-arg computed used as a binary operand is
	// called, comparing/combining its return value. The opposite operand's
	// type drives the expected return (e.g. `count > 0` calls count() → int).
	if wrapped, ret := c.implicitCall(x.Left, left, right); wrapped != nil {
		x.Left = wrapped
		leftExpr = c.checkExpr(x.Left)
		left = ret
	}
	if wrapped, ret := c.implicitCall(x.Right, right, left); wrapped != nil {
		x.Right = wrapped
		rightExpr = c.checkExpr(x.Right)
		right = ret
	}

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
	// Materialize numeric promotion so both operands of ir.Binary share the
	// result type. Codegen no longer needs to know that int+float promotes to
	// float — the IR already carries the conversion.
	if typ != nil && typ.IsNumeric() {
		if lt := exprType(leftExpr); lt.IsNumeric() && lt.Kind != typ.Kind {
			leftExpr = &ir.Conversion{Type: typ, Operand: leftExpr}
		}
		if rt := exprType(rightExpr); rt.IsNumeric() && rt.Kind != typ.Kind {
			rightExpr = &ir.Conversion{Type: typ, Operand: rightExpr}
		}
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
	case ast.UnaryAddr:
		if !skip && !c.isAddressable(operandExpr) {
			c.error(x.Pos, "cannot take address of non-lvalue expression")
		}
		typ = ir.RefOf(operand)
	case ast.UnaryDeref:
		if skip {
			typ = TypDyn
		} else if operand.Kind != ir.TypeRef {
			c.error(x.Pos, "cannot dereference non-ref type %s", operand)
			typ = TypDyn
		} else {
			typ = operand.Elems[0]
		}
	default:
		typ = TypDyn
	}
	return &ir.Unary{AST: x, Type: typ, Op: x.Op, Operand: operandExpr}
}

// isAddressable reports whether e is an lvalue suitable as the operand of `&`.
// Per the NoLambda spec: Idents resolving to *Var or *Param, Selects bottoming
// out at one of those, and Index against an addressable list operand.
func (c *checker) isAddressable(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		switch x.Sym.(type) {
		case *ir.Var, *ir.Param:
			return true
		}
		return false
	case *ir.Select:
		return c.isAddressable(x.Operand)
	case *ir.Index:
		return c.isAddressable(x.Operand)
	}
	return false
}

func (c *checker) inferTernary(x *ast.TernaryExpr) ir.Expr {
	condExpr := c.checkExpr(x.Cond)
	thenExpr := c.checkExprExpecting(x.Then, c.expected)
	elseExpr := c.checkExprExpecting(x.Else, c.expected)
	typ := exprType(thenExpr)
	// Numeric promotion: a ternary mixing int and float yields float, with an
	// explicit Conversion materialized on the narrower branch — mirroring the
	// binary-arithmetic promotion in inferBinary so codegen sees both arms (and
	// the result) at a single type. Without this, `cond ? 0 : x + 0.01` typed
	// as int and the float branch failed to assign to the int-typed temp.
	tt, et := exprType(thenExpr), exprType(elseExpr)
	if tt != nil && et != nil && tt.IsNumeric() && et.IsNumeric() && tt.Kind != et.Kind {
		typ = c.narrowNumeric(tt, et)
		if tt.Kind != typ.Kind {
			thenExpr = &ir.Conversion{Type: typ, Operand: thenExpr}
		}
		if et.Kind != typ.Kind {
			elseExpr = &ir.Conversion{Type: typ, Operand: elseExpr}
		}
	}
	return &ir.Ternary{AST: x, Type: typ, Cond: condExpr, Then: thenExpr, Else: elseExpr}
}

func (c *checker) inferCall(x *ast.CallExpr) ir.Expr {
	// Check if it's a method call (callee is SelectExpr).
	if sel, ok := x.Func.(*ast.SelectExpr); ok {
		return c.inferMethodCall(sel, x)
	}

	// Check if it's a builtin conversion. Only primitive→primitive conversions
	// are permitted; struct/func/component/list/option operands must define a
	// user method and be called as `x.string()` etc. — the cast form never
	// dispatches to user methods.
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		switch ident.Name {
		case "int":
			return c.inferBuiltinConversion(x, TypInt, ident.Name)
		case "float":
			return c.inferBuiltinConversion(x, TypFloat, ident.Name)
		case "string":
			return c.inferBuiltinConversion(x, TypString, ident.Name)
		case "bool":
			return c.inferBuiltinConversion(x, TypBool, ident.Name)
		case "duration":
			return c.inferBuiltinConversion(x, TypDuration, ident.Name)
		case "color", "date", "time", "datetime", "dateTime":
			// These are stdlib StructDef-backed types, not primitives, so the
			// cast form is just convert-to-struct. Look up the StructDef type
			// from scope; if absent (pre-stdlib), fall back to dyn so the
			// diagnostic comes from the regular path. The legacy `datetime`
			// spelling resolves to the `dateTime` StructDef.
			lookup := ident.Name
			if lookup == "datetime" {
				lookup = "dateTime"
			}
			structTyp := TypDyn
			if sym, ok := c.scope.Lookup(lookup); ok {
				if t := sym.SymType(); t != nil {
					structTyp = t
				}
			}
			return c.inferBuiltinConversion(x, structTyp, ident.Name)
		}
	}

	// `this`-elision for bare sibling-method calls: inside a method body,
	// `foo()` resolves to `this.foo()` when the receiver type has a method
	// `foo` and `foo` is not otherwise in scope.
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		if _, inScope := c.scope.Lookup(ident.Name); !inScope {
			if recv := c.currentRecvType(); recv != nil {
				if name := recvTypeName(recv); name != "" {
					if _, ok := c.symtab.LookupMethod(name, ident.Name); ok {
						// Component sibling-method calls fall through to the
						// regular path below: inferIdent resolves the bare name
						// via the currentComponent path to a receiver-stripped
						// func, and the call is built WITHOUT an explicit `this`
						// arg (Pattern A) — identical to a bare `name` reference.
						// passNoImplicitRecv / codegen then supply the
						// per-instance receiver (`m`/`state`). Building
						// `this.name()` here instead leaks the receiver *param*
						// into Args[0], which codegen emits as a literal `this`
						// (undefined in Go → compile error; wrong receiver in JS
						// → runtime error). Struct/enum receivers still need the
						// explicit `this` selector — their `this` is a real
						// emitted parameter.
						if _, isComp := recv.Decl.(*ir.Component); !isComp {
							sel := &ast.SelectExpr{
								Pos:     ident.Pos,
								Operand: &ast.IdentExpr{Pos: ident.Pos, Name: ir.ReceiverParam},
								Field:   ident.Name,
							}
							return c.inferMethodCall(sel, x)
						}
					}
				}
			}
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
		// Bare component-method ref doesn't appear in regular scope (only
		// receiver-less funcs are declared). Recover the binding from the
		// already-checked callee ident's Sym so downstream passes (computed
		// inlining, codegen) see Func instead of just Callee.
		if resolvedFunc == nil {
			if id, ok := calleeExpr.(*ir.Ident); ok {
				if f, ok := id.Sym.(*ir.Func); ok {
					resolvedFunc = f
				}
			}
		}
	}

	retType := callRetType(sig)
	call := &ir.Call{AST: x, Type: retType, Func: resolvedFunc, Args: args}
	if resolvedFunc == nil {
		call.Callee = calleeExpr
	}
	return call
}

// inferBuiltinConversion type-checks `T(x)` where T is a builtin primitive
// target (int/float/string/bool). The operand must be a primitive whose kind
// appears in the allow-list for the target; other types surface a diagnostic.
func (c *checker) inferBuiltinConversion(x *ast.CallExpr, target *ir.Type, name string) ir.Expr {
	if len(x.Args.Args) != 1 {
		args := c.checkCallArgs(x.Args, nil)
		var operand ir.Expr
		if len(args) > 0 {
			operand = args[0].Value
		}
		c.error(x.Pos, "%s(): expected 1 argument, got %d", name, len(x.Args.Args))
		return &ir.Conversion{AST: x, Type: target, Operand: operand}
	}
	// Implicit func() T collapse: `string(fn)` where fn: func() T becomes
	// `string(fn())` so zero-arg funcs can flow into a cast like any other
	// value.
	arg, _ := x.Args.Args[0].(ast.Arg)
	if arg.Value != nil {
		argExpr := c.checkExpr(arg.Value)
		from := exprType(argExpr)
		if from != nil && from.Kind == ir.TypeFunc && from.Sig != nil &&
			len(from.Sig.Params) == 0 && from.Sig.Return != nil {
			call := &ast.CallExpr{Pos: *arg.Value.ExprPos(), Func: arg.Value}
			x.Args.Args[0] = ast.Arg{Name: arg.Name, Value: call}
			argExpr = c.checkExpr(call)
		}
		from = exprType(argExpr)
		fromKind := ir.TypeInvalid
		if from != nil {
			fromKind = from.Kind
			// color/date/time/dateTime values are TypeStruct-backed by their
			// stdlib StructDefs. For the explicit-cast primitiveConvertible
			// check (e.g. `string(c)`, `string(d)`), substitute the string-
			// domain TypeColor kind so the string-domain cast rules apply.
			if ir.StringReprStruct(from) {
				fromKind = ir.TypeColor
			}
		}
		// Likewise, when the *target* is a string-repr struct (e.g.
		// `date(s)`), normalize it to the TypeColor string-domain kind for
		// the convertibility check; the actual Conversion still carries the
		// concrete struct target type.
		targetKind := target.Kind
		if ir.StringReprStruct(target) {
			targetKind = ir.TypeColor
		}
		if from != nil && fromKind != targetKind && !primitiveConvertible(fromKind, targetKind) {
			c.error(x.Pos, "%s(): cannot convert %s", name, from)
		}
		return &ir.Conversion{AST: x, Type: target, Operand: argExpr}
	}
	return &ir.Conversion{AST: x, Type: target}
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
						if c.rejectUnexported(sel.Pos, fsym) {
							return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
						}
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
						// Infer generic type params from arguments (e.g. the
						// html.frontend<T>/html.backend<T> placement directives),
						// mirroring the bare-call path above.
						if sig != nil && len(sig.TypeParams) > 0 {
							sig = c.inferTypeParams(sig, call.Args)
						}
						args := c.checkCallArgs(call.Args, sig)
						var resolvedFunc *ir.Func
						if f, ok := fsym.(*ir.Func); ok {
							resolvedFunc = f
						}
						var retType *ir.Type
						if sig != nil {
							retType = callRetType(sig)
						} else {
							retType = t
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
	// Fallback for generic types: list<int> → "list", option<int> → "option",
	// map<K,V> → "map".
	if !ok {
		switch receiver.Kind {
		case ir.TypeList:
			fn, ok = c.symtab.LookupMethod("list", sel.Field)
		case ir.TypeOption:
			fn, ok = c.symtab.LookupMethod("option", sel.Field)
		case ir.TypeMap:
			fn, ok = c.symtab.LookupMethod("map", sel.Field)
		}
	}
	if ok {
		sig := fn.FuncSig()
		// recvParamStyle tracks whether the receiver is passed as the first
		// explicit param (old-style "dyn receiver" convention) vs. a new-style
		// RecvTypeParams method where the receiver is implicit (not a param).
		recvParamStyle := len(sig.RecvTypeParams) == 0

		// If the method was declared with receiver-level type params
		// (e.g. func list<T>.filter(...)), bind them from the receiver's concrete
		// type arguments before any other type-param work.
		if !recvParamStyle && !isStatic {
			bindings := make(map[string]*ir.Type, len(sig.RecvTypeParams))
			if len(receiver.Elems) == len(sig.RecvTypeParams) {
				for i, name := range sig.RecvTypeParams {
					bindings[name] = receiver.Elems[i]
				}
			} else {
				// Receiver kind matches but no concrete elems (e.g. bare "list" in
				// stdlib lookup). Fall back to binding against Params[0] if present.
				if len(sig.Params) > 0 {
					bindTypeParams(sig.Params[0].Type, receiver, bindings)
				}
			}
			if len(bindings) > 0 {
				// Substitute the recv bindings and clear RecvTypeParams so the
				// shifted-sig path below doesn't re-examine them.
				substituted := sig.Substitute(bindings)
				sig = &ir.FuncSig{
					Params:     substituted.Params,
					Return:     substituted.Return,
					TypeParams: substituted.TypeParams,
					// RecvTypeParams intentionally omitted: consumed by substitution.
					Purity:    substituted.Purity,
					Color:     substituted.Color,
					PolyParam: substituted.PolyParam,
				}
			}
			// Desugared nested methods on generic types carry both
			// RecvTypeParams AND an explicit first `this` param. After the
			// RecvTypeParams are consumed by substitution, the receiver is the
			// first explicit param — switch to recvParamStyle so the call-arg
			// dispatch shifts past it.
			if len(sig.Params) > 0 && sig.Params[0].Receiver {
				recvParamStyle = true
			}
		}
		if len(sig.TypeParams) > 0 {
			// For instance calls using the old-style receiver-as-param convention,
			// bind receiver to param[0] before inferring from explicit args so
			// that e.g. list<int>.length() binds T=int.
			if recvParamStyle && !isStatic && len(sig.Params) > 0 {
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
		} else if recvParamStyle && len(sig.Params) > 0 && receiver.IsAssignableTo(sig.Params[0].Type) {
			// Old-style instance call: receiver is the implicit first arg.
			// Validate remaining args against the shifted sig, then prepend the
			// receiver so the IR matches the static call shape.
			shifted := &ir.FuncSig{
				Params:     sig.Params[1:],
				Return:     sig.Return,
				TypeParams: sig.TypeParams,
			}
			rest := c.checkCallArgs(call.Args, shifted)
			args = append([]ir.CallArg{{Value: receiverExpr}}, rest...)
		} else if !recvParamStyle {
			// New-style RecvTypeParams method: receiver is NOT a param.
			// Check all call args against the full (substituted) sig.
			// Prepend receiver so IR shape is consistent with static call convention.
			rest := c.checkCallArgs(call.Args, sig)
			args = append([]ir.CallArg{{Value: receiverExpr}}, rest...)
		} else {
			rest := c.checkCallArgs(call.Args, sig)
			args = append([]ir.CallArg{{Value: receiverExpr}}, rest...)
		}
		retType := callRetType(sig)
		return &ir.Call{AST: call, Type: retType, Func: fn, Args: args}
	}

	// Struct field funcvar call: h.run() where h is a struct and run is a
	// func-typed field. Set Callee to the *ir.Select so the funcvar call path
	// in the JS codegen (translateIRFuncvarCall) and points-to analysis can
	// identify it as a SlotField slot.
	if receiver.Kind == ir.TypeStruct {
		if sd, ok := receiver.Decl.(*ir.StructDef); ok {
			for _, sf := range sd.Fields {
				if sf.Name == sel.Field && sf.Type != nil && sf.Type.Kind == ir.TypeFunc {
					var sig *ir.FuncSig
					if sf.Type.Sig != nil {
						sig = sf.Type.Sig
					}
					args := c.checkCallArgs(call.Args, sig)
					callee := &ir.Select{
						AST:     sel,
						Type:    sf.Type,
						Operand: receiverExpr,
						Field:   sel.Field,
					}
					retType := callRetType(sig)
					return &ir.Call{AST: call, Type: retType, Callee: callee, Args: args}
				}
			}
		}
	}

	// Element-ref event invocation: `c.<id>.<event>(payload)`. When the
	// field names a declared event on the addressed component, tag the
	// call as an event trigger and type any payload arg against the
	// event's declared type (so anonymous struct literals like
	// InputEvent{value="x"} can be written {value="x"}).
	var argSig *ir.FuncSig
	event := ""
	if evt := c.elementEvent(sel.Operand, sel.Field); evt != nil {
		event = evt.Name
		if evt.Type != nil {
			// Mark the param optional via a placeholder Default so
			// `c.btn.click()` (zero args) and `c.entry.input({…})`
			// (one struct arg) both pass arity, while the single
			// positional arg still gets the payload's expected type.
			argSig = &ir.FuncSig{Params: []*ir.Param{{
				Type:    evt.Type,
				Default: &ir.Literal{Type: evt.Type},
			}}}
		}
	}
	args := c.checkCallArgs(call.Args, argSig)
	if event == "" && isPrimitiveMethodReceiver(receiver) {
		c.error(sel.Pos, "no method %q on type %s", sel.Field, receiver)
	}
	return &ir.Call{AST: call, Type: TypDyn, Receiver: receiverExpr, Args: args, Event: event}
}

// elementEvent returns the event declared on the component addressed by
// `operand` (either `c.<id>` — look up #id inside c's component — or a
// bare element ref) whose name matches `event`, or nil when `operand`
// doesn't address a component or the component has no such event. This
// is how a sigil-free `c.inc.click()` is recognised as an event trigger
// rather than an ordinary method call.
func (c *checker) elementEvent(operand ast.Expr, event string) *ir.EventDecl {
	comp := c.elementHostComponent(operand)
	if comp == nil {
		return nil
	}
	for _, e := range comp.Events {
		if e.Name == event {
			return e
		}
	}
	return nil
}

// elementHostComponent resolves `operand` to the component instance
// hosting the addressed widget. Handles `c.<id>` where c is a
// component-typed param/var and <id> matches a NodeInst inside c's
// body. Returns nil for shapes the resolver doesn't recognise.
func (c *checker) elementHostComponent(operand ast.Expr) *ir.Component {
	sel, ok := operand.(*ast.SelectExpr)
	if !ok {
		return nil
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	if !ok {
		return nil
	}
	sym, ok := c.scope.Lookup(ident.Name)
	if !ok {
		return nil
	}
	var compType *ir.Type
	switch v := sym.(type) {
	case *ir.Param:
		compType = v.Type
	case *ir.Var:
		compType = v.Type
	}
	if compType == nil || compType.Kind != ir.TypeComponent || compType.Decl == nil {
		return nil
	}
	comp, ok := compType.Decl.(*ir.Component)
	if !ok {
		return nil
	}
	// Walk the component's AST (its IR body may not be checked yet
	// at this point in pass2) looking for a visual node with id =
	// sel.Field. The target's tag identifies the host component
	// (stdlib `input` → InputEvent on @input, etc.).
	if comp.AST == nil {
		return nil
	}
	return c.findHostComponentAST(comp.AST.Body.Stmts, sel.Field)
}

func (c *checker) findHostComponentAST(stmts []ast.Stmt, id string) *ir.Component {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VisualNode:
			if n.ID == id {
				name := visualNodeTarget(n)
				if name == "" {
					return nil
				}
				if sym, ok := c.scope.Lookup(name); ok {
					if comp, ok := sym.(*ir.Component); ok {
						return comp
					}
				}
				return nil
			}
			if comp := c.findHostComponentAST(n.Block.Stmts, id); comp != nil {
				return comp
			}
		case *ast.CallStmt:
			if name, callID, isElem := elementRefCallInfo(n.Call); isElem && callID == id {
				if sym, ok := c.scope.Lookup(name); ok {
					if comp, ok := sym.(*ir.Component); ok {
						return comp
					}
				}
				return nil
			}
		case *ast.IfStmt:
			if comp := c.findHostComponentAST(n.Body.Stmts, id); comp != nil {
				return comp
			}
			if comp := c.findHostComponentAST(n.Else.Stmts, id); comp != nil {
				return comp
			}
		case *ast.ForStmt:
			if comp := c.findHostComponentAST(n.Body.Stmts, id); comp != nil {
				return comp
			}
		case *ast.PlatformStmt:
			if comp := c.findHostComponentAST(n.Body.Stmts, id); comp != nil {
				return comp
			}
		}
	}
	return nil
}

// isPrimitiveMethodReceiver reports whether a method-call receiver type's full
// method set is known statically — only stdlib + user-attached methods, no
// dynamic dispatch. When LookupMethod misses on such a receiver the
// missing method is a real bug, not a possibility we have to leave open.
func isPrimitiveMethodReceiver(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool, ir.TypeString,
		ir.TypeList, ir.TypeOption, ir.TypeMap:
		return true
	}
	return false
}

// hasNoLegitimateFields reports whether a scalar primitive type cannot have
// any selector fields. Used to surface "no field X on type Y" diagnostics that
// would otherwise silently degrade to TypDyn. Kept conservative: only the
// pure-scalar primitives (int/float/bool) qualify because every other type
// kind in the language has at least one legitimate field-style accessor
// (color.r, list.length, struct fields, component vars/props, enum
// members, etc.) or carries no shape information at all (dyn, generic
// params, anonymous structs).
func hasNoLegitimateFields(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return true
	}
	return false
}

// callRetType returns the expression type a call through sig should produce.
// A sig with a nil Return represents a void function; calling one does not
// yield a usable value, so we tag the call expression TypVoid. Consumer sites
// that treat the call as an expression surface a normal assignability error
// against TypVoid — this makes "silent dyn" leakage from missing return
// annotations impossible. A nil sig (unknown function) stays TypDyn so the
// surrounding lookup error isn't doubled up.
func callRetType(sig *ir.FuncSig) *ir.Type {
	if sig == nil {
		return TypDyn
	}
	if sig.Return == nil {
		return TypVoid
	}
	return sig.Return
}

func (c *checker) inferSelect(x *ast.SelectExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	operand := exprType(operandExpr)

	// Auto-deref through Select: if the operand is `ref<T>`, treat the field
	// access as if the operand were dereferenced first. The IR carries an
	// explicit Unary{Deref} so downstream passes never need a special case.
	if operand != nil && operand.Kind == ir.TypeRef && len(operand.Elems) > 0 {
		operandExpr = &ir.Unary{
			AST:     nil, // synthesized — no source position
			Type:    operand.Elems[0],
			Op:      ast.UnaryDeref,
			Operand: operandExpr,
		}
		operand = operand.Elems[0]
	}

	{
		// Namespace member access: ns.field.
		if ident, ok := x.Operand.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if ns, ok := sym.(*ir.Namespace); ok {
					if ns.Pkg != nil {
						if fsym, ok := ns.Pkg.Symbols.Root.Lookup(x.Field); ok {
							if c.rejectUnexported(x.Pos, fsym) {
								return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
							}
							c.reportUnusable(x.Pos, ident.Name+"."+x.Field, fsym)
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
				// Build type-arg substitution bindings if the struct is generic and
				// the operand carries concrete type arguments (e.g. Box<int>.value).
				var typeArgBindings map[string]*ir.Type
				if len(sd.TypeParams) > 0 && len(operand.Elems) == len(sd.TypeParams) {
					typeArgBindings = make(map[string]*ir.Type, len(sd.TypeParams))
					for i, name := range sd.TypeParams {
						typeArgBindings[name] = operand.Elems[i]
					}
				}
				for _, f := range sd.Fields {
					if f.Name == x.Field {
						if f.Unusable != "" {
							c.error(x.Pos, "field %s.%s cannot be used: %s", sd.Name, f.Name, f.Unusable)
						}
						fieldType := f.Type
						if typeArgBindings != nil {
							fieldType = fieldType.Substitute(typeArgBindings)
						}
						return &ir.Select{AST: x, Type: fieldType, Operand: operandExpr, Field: x.Field}
					}
				}
				c.error(x.Pos, "no field %q on struct %s", x.Field, sd.Name)
			}
		}

		// Component instance member access via `this` (inside a nested
		// component method): resolve to a component var/prop/func. Limited
		// to `this.<name>` to preserve the historical Dyn fallback for
		// out-of-component access patterns like `c.name == "x"` which
		// type-checking has long permitted via Dyn promotion.
		if operand.Kind == ir.TypeComponent && operand.Decl != nil {
			isThis := false
			if ident, ok := x.Operand.(*ast.IdentExpr); ok && ident.Name == ir.ReceiverParam {
				isThis = true
			}
			if isThis {
				if comp, ok := operand.Decl.(*ir.Component); ok {
					for _, v := range comp.Vars {
						if v.Name == x.Field {
							return &ir.Select{AST: x, Type: v.Type, Operand: operandExpr, Field: x.Field}
						}
					}
					for _, p := range comp.Props {
						if p.Name == x.Field {
							return &ir.Select{AST: x, Type: p.Type, Operand: operandExpr, Field: x.Field}
						}
					}
					for _, fn := range comp.Funcs {
						if fn.Receiver == "" && fn.Name == x.Field {
							return &ir.Select{AST: x, Type: fn.SymType(), Operand: operandExpr, Field: x.Field}
						}
					}
				}
			}
		}

		// Built-in list/string .length yields int.
		if x.Field == "length" && (operand.Kind == ir.TypeList || operand.Kind == ir.TypeString) {
			return &ir.Select{AST: x, Type: TypInt, Operand: operandExpr, Field: x.Field}
		}
		if hasNoLegitimateFields(operand) {
			c.error(x.Pos, "no field %q on type %s", x.Field, operand)
		}
	}

	return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
}

func (c *checker) inferIndex(x *ast.IndexExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	indexExpr := c.checkExpr(x.Index)
	operand := exprType(operandExpr)

	if operand.Kind == ir.TypeMap {
		if len(operand.Elems) != 2 {
			return &ir.Index{AST: x, Type: TypDyn, Operand: operandExpr, Idx: indexExpr}
		}
		keyT, valT := operand.Elems[0], operand.Elems[1]
		if !keyT.Equal(exprType(indexExpr)) {
			c.error(x.Pos, "map index type %s does not match key type %s", exprType(indexExpr), keyT)
		}
		return &ir.Index{AST: x, Type: valT, Operand: operandExpr, Idx: indexExpr}
	}
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
					if c.rejectUnexported(x.Pos, tsym) {
						return &ir.Literal{Type: TypDyn}
					}
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
	} else if c.expected != nil && c.expected.Kind == ir.TypeMap {
		// Anonymous struct literal (all-ident keys) with expected map type:
		// reinterpret as a map literal. Only string-keyed maps are supported
		// for ident keys (the ident name becomes the string key).
		return c.reinterpretStructAsMap(x, c.expected)
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
			// Spread source must be the same struct type as the literal target,
			// otherwise the field merge is meaningless and would mis-shape the
			// resulting value at codegen time.
			if sd != nil {
				srcType := exprType(val)
				if srcType != nil && srcType.Kind != ir.TypeDyn && srcType.Kind != ir.TypeInvalid {
					srcDecl, _ := srcType.Decl.(*ir.StructDef)
					if srcType.Kind != ir.TypeStruct || srcDecl != sd {
						c.error(x.Pos, "cannot spread %s into struct %s", srcType, sd.Name)
					}
				}
			}
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
		fields = append(fields, ir.FieldInit{Name: f.Name, NamePos: f.NamePos, Value: val})
	}

	if sd != nil {
		typ := sd.SymType()
		// For generic structs initialized via anonymous literal against a
		// known expected type (e.g. `var b box<int> = {v=42}`), inherit the
		// expected type's parameter bindings so downstream method calls
		// resolve T correctly.
		if c.expected != nil && c.expected.Kind == ir.TypeStruct && c.expected.Decl == sd {
			typ = c.expected
		}
		return &ir.StructLit{AST: x, Type: typ, Def: sd, Fields: fields}
	}
	return &ir.StructLit{AST: x, Type: &ir.Type{Kind: ir.TypeStruct}, Fields: fields}
}

// reinterpretStructAsMap converts an all-ident-key StructExpr into a MapLitIR
// when the expected type is map<K,V>. Only map<string,V> is supported; ident
// names become string literal keys. For any other K, a check error is emitted.
// reinterpretStructAsMap converts an all-ident-key StructExpr into a MapLitIR
// when the expected type is map<K,V>. Only map<string,V> is supported; ident
// names become string literal keys. For any other K, a check error is emitted.
func (c *checker) reinterpretStructAsMap(x *ast.StructExpr, mapType *ir.Type) ir.Expr {
	if len(mapType.Elems) != 2 {
		c.error(x.Pos, "internal: map type missing key/value elements")
		return &ir.MapLitIR{Type: mapType, Entries: nil}
	}
	keyT := mapType.Elems[0]
	valT := mapType.Elems[1]
	if keyT.Kind != ir.TypeString {
		c.error(x.Pos, "ident-keyed literal does not match map<%s, ...> with non-string key type", keyT)
		return &ir.MapLitIR{Type: mapType, Entries: nil}
	}
	var entries []ir.MapEntry
	for _, f := range x.Fields {
		if f.Spread {
			c.error(x.Pos, "spread not supported in map literal")
			continue
		}
		keyLit := &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: f.Name}
		keyIR := &ir.Literal{AST: keyLit, Type: TypString, Raw: f.Name}
		val := c.checkExprExpecting(f.Value, valT)
		entries = append(entries, ir.MapEntry{Key: keyIR, Value: val})
	}
	return &ir.MapLitIR{Type: mapType, Entries: entries}
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

func (c *checker) inferMapLit(x *ast.MapLit) ir.Expr {
	// MapLit (non-ident keys) requires either a map expected type or inference
	// from its own entries. A struct expected type is always wrong.
	if c.expected != nil && c.expected.Kind == ir.TypeStruct {
		c.error(x.Pos, "map literal cannot be used where a struct is expected")
		return &ir.MapLitIR{AST: x, Type: ir.MapOf(TypDyn, TypDyn), Entries: nil}
	}
	if c.expected == nil || c.expected.Kind != ir.TypeMap {
		c.error(x.Pos, "anonymous map literal requires an expected type from context")
		return &ir.MapLitIR{AST: x, Type: ir.MapOf(TypDyn, TypDyn), Entries: nil}
	}
	if len(x.Entries) == 0 {
		return &ir.MapLitIR{AST: x, Type: c.expected, Entries: nil}
	}
	var keyExpected, valExpected *ir.Type
	if len(c.expected.Elems) == 2 {
		keyExpected = c.expected.Elems[0]
		valExpected = c.expected.Elems[1]
	}
	var keyT, valT *ir.Type
	var entries []ir.MapEntry
	for _, e := range x.Entries {
		k := c.checkExprExpecting(e.Key, keyExpected)
		v := c.checkExprExpecting(e.Value, valExpected)
		kT := exprType(k)
		vT := exprType(v)
		if keyT == nil {
			keyT = kT
		} else if !keyT.Equal(kT) {
			c.error(e.Pos, "map key type %s does not match earlier %s", kT, keyT)
		}
		if valT == nil {
			valT = vT
		} else if !valT.Equal(vT) {
			c.error(e.Pos, "map value type %s does not match earlier %s", vT, valT)
		}
		entries = append(entries, ir.MapEntry{Key: k, Value: v})
	}
	if !isComparable(keyT) {
		c.error(x.Pos, "map key type %s is not comparable", keyT)
	}
	return &ir.MapLitIR{AST: x, Type: ir.MapOf(keyT, valT), Entries: entries}
}

// interpPartAlreadyString reports whether an interpolation part's type can be
// concatenated into a string chain without any coercion.
func interpPartAlreadyString(t *ir.Type) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeDyn:
		return true
	}
	return false
}

// interpPartPrimitive reports whether a part's type is a stringifiable
// primitive that can flow into an interpolation via an ir.Conversion — no
// user-defined `.string()` method required.
func interpPartPrimitive(t *ir.Type) bool {
	if t == nil {
		return false
	}
	// color/date/time/dateTime are StructDef-backed but still stringify like
	// string-domain primitives in interpolation.
	if ir.StringReprStruct(t) {
		return true
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool,
		ir.TypeEnum, ir.TypeUnit, ir.TypeNull,
		ir.TypeColor, ir.TypeDuration:
		return true
	}
	return false
}

// interpolateStringify turns an interpolation part into a string-typed ir.Expr.
// Primitives and dyn flow through (with a wrapping Conversion for primitives);
// zero-arg funcs get an implicit call; other types must define a `.string()`
// method. When no path exists an error is emitted and the original expr is
// returned so chain assembly continues.
func (c *checker) interpolateStringify(partAst ast.Expr, expr ir.Expr, pos ast.Pos) ir.Expr {
	t := exprType(expr)
	// Implicit call: `{fn}` where fn returns T becomes `{fn()}`. Re-check via
	// the ast layer so Func resolution (plain / method / namespace) runs.
	if t != nil && t.Kind == ir.TypeFunc && t.Sig != nil &&
		len(t.Sig.Params) == 0 && t.Sig.Return != nil && partAst != nil {
		call := &ast.CallExpr{Pos: *partAst.ExprPos(), Func: partAst}
		expr = c.checkExpr(call)
		t = exprType(expr)
	}
	if interpPartAlreadyString(t) {
		return expr
	}
	if interpPartPrimitive(t) {
		return &ir.Conversion{Type: TypString, Operand: expr}
	}
	// User-defined or stdlib method lookup: `.string()` on the value's type.
	typeName := t.String()
	if fn, ok := c.symtab.LookupMethod(typeName, "string"); ok {
		return &ir.Call{Type: TypString, Func: fn, Args: []ir.CallArg{{Value: expr}}}
	}
	// Generic-type fallbacks so list/option implementations can register under
	// their bare name and apply to any instantiation.
	switch t.Kind {
	case ir.TypeList:
		if fn, ok := c.symtab.LookupMethod("list", "string"); ok {
			return &ir.Call{Type: TypString, Func: fn, Args: []ir.CallArg{{Value: expr}}}
		}
		// No stdlib method yet — fall back to the generic stringify path so
		// `{myList}` still produces a rendered list at runtime. Platform
		// codegen for ir.Conversion over a list emits its language-native
		// formatter (fmt.Sprint / String() / toString()).
		return &ir.Conversion{Type: TypString, Operand: expr}
	case ir.TypeOption:
		if fn, ok := c.symtab.LookupMethod("option", "string"); ok {
			return &ir.Call{Type: TypString, Func: fn, Args: []ir.CallArg{{Value: expr}}}
		}
		return &ir.Conversion{Type: TypString, Operand: expr}
	}
	c.error(pos, "cannot interpolate %s: type has no string() method", t)
	return expr
}

func (c *checker) inferInterpolation(x *ast.InterpolationExpr) ir.Expr {
	// Desugar interpolation to a chain of Binary + operations. Each string
	// literal part becomes an ir.Literal; each expression part is checked and
	// stringified via interpolateStringify so the chain is string-typed end to
	// end.
	var chain ir.Expr
	for _, part := range x.Parts {
		var partExpr ir.Expr
		if lit, ok := part.(*ast.LiteralExpr); ok {
			partExpr = &ir.Literal{AST: lit, Type: TypString, Raw: lit.Raw}
		} else {
			partExpr = c.interpolateStringify(part, c.checkExpr(part), *part.ExprPos())
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
	// Infer unannotated lambda params from the expected function-type context
	// (e.g. callback passed to `t.test(name, func(t, c) { ... })`). Falling back
	// to `buildParams` would error on every untyped param; the contextual sig
	// supplies those types.
	var expectedSig *ir.FuncSig
	if c.expected != nil && c.expected.Kind == ir.TypeFunc {
		expectedSig = c.expected.Sig
	}
	params := c.buildLambdaParams(x.Params, expectedSig)
	var ret *ir.Type
	if x.ReturnType != nil {
		ret = c.resolveType(x.ReturnType)
	} else if expectedSig != nil {
		ret = expectedSig.Return
	}
	fn := &ir.Func{
		Params: params,
		Return: ret,
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
		if fn.Return == nil {
			// Expression-body lambda with no annotation and no contextual
			// return type: take the body's type as the return type.
			fn.Return = exprType(bodyExpr)
			c.returnType = fn.Return
		}
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
	// Recurse into func signatures: func(T) U vs func(int) string → T=int, U=string.
	if param.Kind == ir.TypeFunc && arg.Kind == ir.TypeFunc &&
		param.Sig != nil && arg.Sig != nil {
		if len(param.Sig.Params) == len(arg.Sig.Params) {
			for i := range param.Sig.Params {
				bindTypeParams(param.Sig.Params[i].Type, arg.Sig.Params[i].Type, bindings)
			}
		}
		bindTypeParams(param.Sig.Return, arg.Sig.Return, bindings)
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

// paramNameOK reports whether a param can be targeted by name at a call site.
func paramNameOK(p *ir.Param) bool {
	return p.Name != "" && !strings.HasPrefix(p.Name, "_")
}

// bindArgs resolves args against a FuncSig using Pythonic rules. Returns one
// *ir.Expr per param slot (nil = use default). Reports errors via c.error.
func (c *checker) bindArgs(callPos ast.Pos, args []ast.ArgOrEventHandler, sig *ir.FuncSig) ([]*ir.Expr, bool) {
	n := len(sig.Params)
	bound := make([]*ir.Expr, n)
	positional := 0
	seenNamed := false
	ok := true

	for _, a := range args {
		arg, isArg := a.(ast.Arg)
		if !isArg {
			continue
		}
		if arg.Value == nil {
			continue
		}

		// Struct spread: ...expr expands struct fields as named args.
		if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
			operandIR := c.checkExpr(spread.Operand)
			operandType := exprType(operandIR)
			if operandType == nil || operandType.Kind != ir.TypeStruct {
				typStr := "(nil)"
				if operandType != nil {
					typStr = operandType.String()
				}
				c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
				ok = false
				continue
			}
			sd := operandType.Decl.(*ir.StructDef)
			seenNamed = true
			for _, f := range sd.Fields {
				idx := -1
				for i, p := range sig.Params {
					if paramNameOK(p) && p.Name == f.Name {
						idx = i
						break
					}
				}
				if idx == -1 {
					continue // no matching param; ignore silently
				}
				if bound[idx] != nil {
					c.error(spread.Pos, "parameter %q already provided", f.Name)
					ok = false
					continue
				}
				var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
				p := sig.Params[idx]
				if p.Type != nil && f.Type.Kind != ir.TypeDyn && p.Type.Kind != ir.TypeDyn &&
					!f.Type.IsAssignableTo(p.Type) {
					c.error(spread.Pos, "cannot use field %q (%s) as parameter %q (%s)",
						f.Name, f.Type, p.Name, p.Type)
					ok = false
					continue
				}
				if p.Type != nil {
					selExpr = wrapIfNeeded(selExpr, p.Type)
				}
				bound[idx] = &selExpr
			}
			continue
		}

		if arg.Name == "" {
			// Positional arg.
			if seenNamed {
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				ok = false
				// Still check the expression for side-effects (e.g. async detection).
				c.checkExpr(arg.Value)
				continue
			}
			if positional >= n {
				c.error(*arg.Value.ExprPos(), "too many arguments: expected %d", n)
				ok = false
				positional++
				continue
			}
			expr := c.checkArgExpr(arg.Value, sig.Params[positional])
			bound[positional] = &expr
			positional++
		} else {
			// Named arg.
			seenNamed = true
			idx := -1
			for i, p := range sig.Params {
				if p.Name == arg.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
				// Check if any param has an empty name (anonymous func type).
				hasUnnamed := false
				for _, p := range sig.Params {
					if p.Name == "" {
						hasUnnamed = true
						break
					}
				}
				if hasUnnamed {
					c.error(arg.NamePos, "no parameter name: function type has unnamed parameters")
				} else {
					c.error(arg.NamePos, "unknown parameter %q", arg.Name)
				}
				ok = false
				continue
			}
			p := sig.Params[idx]
			if !paramNameOK(p) {
				c.error(arg.NamePos, "parameter %q must be passed positionally", p.Name)
				ok = false
				continue
			}
			if bound[idx] != nil {
				c.error(arg.NamePos, "parameter %q already provided", p.Name)
				ok = false
				continue
			}
			expr := c.checkArgExpr(arg.Value, p)
			bound[idx] = &expr
		}
	}

	// Every required param must be filled.
	for i, p := range sig.Params {
		if bound[i] == nil && p.Default == nil {
			if p.Name != "" {
				c.error(callPos, "missing required argument %q", p.Name)
			} else {
				c.error(callPos, "missing required argument at position %d", i+1)
			}
			ok = false
		}
	}

	return bound, ok
}

// checkArgExpr type-checks a single call argument against a target param.
func (c *checker) checkArgExpr(value ast.Expr, p *ir.Param) ir.Expr {
	expr := c.checkExprExpecting(value, p.Type)
	actual := exprType(expr)
	c.requireValueType(actual, *value.ExprPos())
	if p.Type != nil && actual.Kind != ir.TypeDyn && p.Type.Kind != ir.TypeDyn && !actual.IsAssignableTo(p.Type) {
		if adapted, ok := adaptLiteralZero(expr, p.Type); ok {
			expr = adapted
		} else if callExpr, _ := c.implicitCall(value, actual, p.Type); callExpr != nil {
			expr = c.checkExpr(callExpr)
		} else {
			c.error(*value.ExprPos(), "cannot pass %s as %s", actual, p.Type)
		}
	}
	if p.Type != nil {
		expr = wrapIfNeeded(expr, p.Type)
	}
	return expr
}

// checkCallArgs type-checks all arguments in an ArgList and returns resolved CallArgs.
// If sig is non-nil, validates arg types and arity against it using Pythonic named/positional rules.
func (c *checker) checkCallArgs(args ast.ArgList, sig *ir.FuncSig) []ir.CallArg {
	// No sig: check exprs, pass through names unchanged (dynamic call).
	if sig == nil {
		var result []ir.CallArg
		for _, a := range args.Args {
			switch arg := a.(type) {
			case ast.Arg:
				if arg.Value != nil {
					if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
						c.error(spread.Pos, "... struct spread cannot be used in a dynamic function call")
						continue
					}
					expr := c.checkExpr(arg.Value)
					c.requireValueType(exprType(expr), *arg.Value.ExprPos())
					result = append(result, ir.CallArg{Name: arg.Name, NamePos: arg.NamePos, Value: expr})
				}
			case ast.EventHandler:
				// Per-call @error handlers are extracted (with proper ErrorEvent
				// defaulting on the param) by resolveCallStmt. Skip here.
				if arg.Name == "error" {
					continue
				}
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
		return result
	}

	bound, _ := c.bindArgs(args.Pos, args.Args, sig)

	// Build result in param order; omit slots with nil (default param).
	var result []ir.CallArg
	for i, expr := range bound {
		if expr == nil {
			continue
		}
		result = append(result, ir.CallArg{
			Name:  sig.Params[i].Name,
			Value: *expr,
		})
	}

	// For error recovery, check and append any overflow positional args (those
	// beyond len(sig.Params)). Downstream passes (e.g. async analysis) need
	// their IR exprs to be reachable via call args even when arity is wrong.
	positional := 0
	for _, a := range args.Args {
		arg, isArg := a.(ast.Arg)
		if !isArg || arg.Value == nil || arg.Name != "" {
			continue
		}
		if positional >= len(sig.Params) {
			expr := c.checkExpr(arg.Value)
			result = append(result, ir.CallArg{Value: expr})
		}
		positional++
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
			// Capturing a context into a local var would freeze the value and
			// miss reactive updates. Reject here; read the context at each use site.
			if _, ok := initExpr.(*ir.ContextRead); ok {
				c.error(decl.Pos, "context value cannot be captured into a local var (read at use site instead)")
			}
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
					initExpr = adapted
				} else {
					c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
				}
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			if c.requireValueType(initType, decl.Pos) {
				// Prevent void propagation into an inferred var type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			} else {
				initExpr = wrapIfNeeded(initExpr, typ)
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
				// Context names are read-only; assignment would bypass reactivity.
				if _, ok := sym.(*ir.Context); ok {
					c.error(x.Pos, "cannot assign to context %q", ident.Name)
					return &ir.Assign{AST: x, Target: targetExpr, Op: x.Op, Value: valueExpr}
				}
			}
		}
		// Type checking.
		c.requireValueType(valueType, x.Pos)
		if x.Op == ast.AssignSet {
			if targetType.Kind != ir.TypeDyn && valueType.Kind != ir.TypeDyn && !valueType.IsAssignableTo(targetType) {
				if adapted, ok := adaptLiteralZero(valueExpr, targetType); ok {
					valueExpr = adapted
				} else {
					c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
				}
			}
			if targetType.Kind != ir.TypeDyn {
				valueExpr = wrapIfNeeded(valueExpr, targetType)
			}
		} else {
			// Compound assignment: both sides must be numeric (or string for +=).
			// The value type must be assignable to the target type so that any
			// implicit numeric widening is materialized as ir.Conversion.
			if targetType.Kind != ir.TypeDyn && valueType.Kind != ir.TypeDyn {
				if x.Op == ast.AssignAdd && targetType.Kind == ir.TypeString {
					// string += string is fine.
				} else if !targetType.IsNumeric() || !valueType.IsNumeric() {
					c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
				} else if !valueType.IsAssignableTo(targetType) {
					c.error(x.Pos, "cannot assign %s to %s", valueType, targetType)
				} else {
					valueExpr = wrapIfNeeded(valueExpr, targetType)
				}
			}
		}
		return &ir.Assign{AST: x, Target: targetExpr, Op: x.Op, Value: valueExpr}
	case *ast.ToggleStmt:
		targetExpr := c.checkExpr(x.Target)
		t := exprType(targetExpr)
		if t != nil && t.Kind != ir.TypeBool && t.Kind != ir.TypeDyn && t.Kind != ir.TypeInvalid {
			c.error(x.Pos, "toggle target must be bool, got %s", t)
		}
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
	case *ast.VarStmt:
		typ := c.resolveType(x.Type)
		var initExpr ir.Expr
		if x.Init != nil {
			initExpr = c.checkExprExpecting(x.Init, typ)
			// Capturing a context into a local var would freeze the value and
			// miss reactive updates. Reject here; read the context at each use site.
			if _, ok := initExpr.(*ir.ContextRead); ok {
				c.error(x.Pos, "context value cannot be captured into a local var (read at use site instead)")
			}
			initType := exprType(initExpr)
			if c.requireValueType(initType, x.Pos) {
				// Avoid propagating void into an inferred var type.
			} else if typ.Kind == ir.TypeDyn {
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
			c.requireValueType(valType, x.Pos)
			if c.returnType != nil && c.returnType.Kind != ir.TypeDyn && valType.Kind != ir.TypeDyn && !valType.IsAssignableTo(c.returnType) {
				if adapted, ok := adaptLiteralZero(valExpr, c.returnType); ok {
					valExpr = adapted
				} else {
					c.error(x.Pos, "cannot return %s as %s", valType, c.returnType)
				}
			}
			if c.returnType != nil && c.returnType.Kind != ir.TypeDyn {
				valExpr = wrapIfNeeded(valExpr, c.returnType)
			}
		}
		return &ir.Return{AST: x, Value: valExpr}
	case *ast.CallStmt:
		// context #id(...) is only valid at file top level; reject it here.
		if isContextDeclCallStmt(x) {
			c.error(x.Pos, "context decl only permitted at file top level")
			return nil
		}
		// Bodyless forms of root-ish visual nodes (e.g. `timer(...)` with
		// the tick handler inside the parens) parse as CallStmt but must
		// dispatch through the VisualNode special-cases so they register
		// on package/component instead of becoming a generic node instance.
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok && x.Call.ID == "" {
			switch id.Name {
			case "timer", "window", "output", "errorBoundary":
				vn := &ast.VisualNode{
					Pos:    x.Pos,
					Target: id,
					Args:   x.Call.Args,
				}
				return c.checkVisualNodeIR(vn)
			}
		}
		// A bare `event(args)` whose name matches an event declared on the
		// enclosing component is an emit, not a function call. Events share
		// the component's namespace but cannot collide with funcs/vars, so
		// resolving by name here is unambiguous. A `name #id(...)` element-ref
		// declaration (ID set) is never an emit.
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok && x.Call.ID == "" && c.currentComponent != nil {
			for _, evt := range c.currentComponent.Events {
				if evt.Name == id.Name {
					args := c.checkCallArgs(x.Call.Args, nil)
					return &ir.Emit{AST: x, Name: id.Name, Args: args}
				}
			}
		}
		// Check if the call target is a component — handle directly to avoid
		// double-checking args through both inferCall and resolveCallStmt.
		// Bare `Foo(...)` resolves via the symbol table; qualified
		// `pkg.Foo(...)` resolves through the namespace's package.
		var comp *ir.Component
		var compName string
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
			if sym, ok := c.symtab.LookupComponent(id.Name); ok {
				if c.rejectUnexported(x.Pos, sym) {
					return nil
				}
				if co, ok := sym.(*ir.Component); ok {
					comp = co
					compName = id.Name
				}
			}
		} else if sel, ok := x.Call.Func.(*ast.SelectExpr); ok {
			if nsIdent, ok := sel.Operand.(*ast.IdentExpr); ok {
				if sym, ok := c.scope.Lookup(nsIdent.Name); ok {
					if ns, ok := sym.(*ir.Namespace); ok {
						// A declared component in the namespace's package.
						// Visibility (unexported names) is enforced by
						// rejectUnexported, not by casing heuristics.
						if ns.Pkg != nil {
							if fsym, ok := ns.Pkg.Symbols.LookupComponent(sel.Field); ok {
								if c.rejectUnexported(x.Pos, fsym) {
									return nil
								}
								if co, ok := fsym.(*ir.Component); ok {
									comp = co
									compName = nsIdent.Name + "." + sel.Field
								}
							}
						}
						// Bodyless raw platform elements (`html.progress(...)`,
						// `html.hr`) resolve through the platform namespace's
						// Resolve(). They are visual nodes, not function calls,
						// so emit an ir.NodeInst — mirroring the with-body path
						// in checkVisualNodeIR — so the reactivity lowering pass
						// walks their reactive props and wires the mutation
						// updaters. Without this they became ir.CallStmt, which
						// lowering skips, leaving reactive attributes frozen.
						if comp == nil && ns.Resolve != nil {
							if resolved := ns.Resolve(sel.Field); resolved != nil {
								if co, ok := resolved.(*ir.Component); ok {
									comp = co
									compName = sel.Field
								}
							}
						}
					}
				}
			}
		}
		// An element-ref declaration (`Comp #id(...)`) is handled uniformly by
		// the elementRefCallInfo path below, which preserves the #id and applies
		// the same stdlib-lenient / user-component-strict arg checking.
		if comp != nil && x.Call.ID == "" {
			c.validateCallStmtComponentArgs(x.Call, comp)
			props, handlers, bindings := c.checkAndSplitArgs(x.Call.Args, comp)
			var keyExpr ir.Expr
			for _, a := range x.Call.Args.Args {
				if arg, ok := a.(ast.Arg); ok && arg.Name == "key" && arg.Value != nil {
					keyExpr = c.checkExpr(arg.Value)
					break
				}
			}
			return &ir.NodeInst{
				AST:       x,
				Name:      compName,
				Component: comp,
				Props:     props,
				Handlers:  handlers,
				Bindings:  bindings,
				Key:       keyExpr,
			}
		}
		// Children-less element references (`text #id(...)`, `button(@click)`)
		// parse as CallStmt but semantically behave like visual nodes — emit
		// NodeInst so event handlers and the #id are preserved in IR.
		if name, id, isElem := elementRefCallInfo(x.Call); isElem {
			// Resolve the addressed component (stdlib `input`, user
			// component, …) so later passes — including the test-side
			// event-arg typer — can see what payload `@<event>` takes.
			// Resolve before checkAndSplitArgs so spread props on
			// user-defined components can be matched against prop names.
			var elemComp *ir.Component
			if sym, ok := c.scope.Lookup(name); ok {
				if sd, ok := sym.(*ir.Component); ok {
					elemComp = sd
				}
			}
			// Pass the component to checkAndSplitArgs only for user-defined
			// components. Stdlib components use nil to preserve lenient
			// arg-checking behavior (events use platform-specific types).
			argsComp := elemComp
			if argsComp != nil && argsComp.Stdlib {
				argsComp = nil
			}
			props, handlers, bindings := c.checkAndSplitArgs(x.Call.Args, argsComp)
			return &ir.NodeInst{
				AST:       x,
				Name:      name,
				Component: elemComp,
				Props:     props,
				Handlers:  handlers,
				Bindings:  bindings,
				ID:        id,
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
		// Hoist window #ids declared inside the loop body as list<Window>
		// symbols in the enclosing scope, so refs outside the loop type-check
		// against the unrolled list. Done before pushScope so the symbol
		// lives in the parent scope.
		hoistedIDs := c.hoistForLoopWindowIDs(&x.Body)
		c.pushScope()
		// Resolve &-binding: only the ELEMENT loop var may be &-bound, only
		// over a mutable addressable list. The element is Key in single-var
		// form and Value in two-var form. `&` on the index (two-var Key) is an
		// error. An invalid `&` is reported and downgraded to a value binding.
		// TODO(lint): a plain (non-&) element loop var is a value copy, so
		// writing through its fields (`for t = list { t.done!! }`) silently
		// updates a throwaway copy. Surface that as a lint error suggesting
		// `&t`; for now it compiles as a no-op.
		elemRef := x.KeyRef
		if x.Value != "" {
			if x.KeyRef {
				c.error(x.Pos, "& cannot bind the index variable; write `for i, &%s = …`", x.Value)
			}
			elemRef = x.ValueRef
		}
		if elemRef {
			if iter.Kind != ir.TypeList {
				c.error(x.Pos, "&-bound loop variable requires a list; got %s", iter)
				elemRef = false
			} else if !isAddressableListExpr(iterExpr) {
				c.error(x.Pos, "cannot &-bind: the iterable is not an addressable mutable list; iterate a list variable or field, or drop the &")
				elemRef = false
			}
		}
		// elemDeclType wraps the element type in ref<T> when &-bound so reads
		// auto-deref and field/element writes type-check as lvalues; the
		// lowering rewrites uses to indexed list access.
		elemDeclType := func(t *ir.Type) *ir.Type {
			if elemRef {
				return ir.RefOf(t)
			}
			return t
		}
		// Declare loop variables based on iterator type.
		elemType := TypDyn
		switch iter.Kind {
		case ir.TypeList:
			if len(iter.Elems) > 0 {
				elemType = iter.Elems[0]
			}
			if x.Value != "" {
				// for key, value = list: key is index (int), value is element.
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypInt})
				c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: elemDeclType(elemType)})
			} else {
				// for item = list: item is element.
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: elemDeclType(elemType)})
			}
		case ir.TypeIter:
			if len(iter.Elems) > 0 {
				elemType = iter.Elems[0]
			}
			if x.Value != "" {
				// for key, value = iter: key is index (int), value is element.
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypInt})
				c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: elemType})
			} else {
				// for item = iter: item is element.
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: elemType})
			}
		case ir.TypeMap:
			if x.Value == "" {
				c.error(x.Pos, "iterating over map requires two variables: for k, v = m")
			} else if len(iter.Elems) == 2 {
				// for k, v = map: k is key type, v is value type.
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: iter.Elems[0]})
				c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: iter.Elems[1]})
				elemType = iter.Elems[1]
			}
		case ir.TypeDyn:
			if x.Value != "" {
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypDyn})
				c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: TypDyn})
			} else {
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypDyn})
			}
		default:
			c.error(x.Pos, "for iterator must be list, iter, or map; got %s", iter)
			if x.Value != "" {
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypDyn})
				c.scope.Declare(&ir.LoopVar{Name: x.Value, Type: TypDyn})
			} else {
				c.scope.Declare(&ir.LoopVar{Name: x.Key, Type: TypDyn})
			}
		}
		body := c.checkBlockIR(&x.Body)
		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			elseBody = c.checkBlockIR(&x.Else)
		}
		c.popScope()
		return &ir.For{AST: x, Key: x.Key, Value: x.Value, Iter: iterExpr, ElemType: elemType, Body: body, Else: elseBody, HoistedWindowIDs: hoistedIDs, RefElem: elemRef}
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
		// When a `func` is declared inside a nested block (provider children,
		// if/for body), it would otherwise be built and scoped but never
		// appended to any IR collection — leaving call sites referencing an
		// undefined function. Lift to the enclosing component (analogous to
		// the LocalVar→Var promotion done by passNoContext for vars).
		if c.currentComponent != nil {
			c.currentComponent.Funcs = append(c.currentComponent.Funcs, fn)
		} else if c.pkg != nil {
			c.pkg.Funcs = append(c.pkg.Funcs, fn)
		}
		return nil
	case *ast.Comment:
		return nil
	case *ast.DisabledDecl:
		return nil
	case *ast.AttrDecl:
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

// elementRefCallInfo recognizes CallStmts whose callee represents an element
// tag — either `text #id(...)` (a call carrying an element-ref id) or a bare
// tag ident like `button(...)` that carries event handlers. Returns the tag
// name, the #id (possibly empty), and whether this looks like an element call.
func elementRefCallInfo(call *ast.CallExpr) (string, string, bool) {
	// `text #id(...)` — a bare tag ident carrying an element-ref id.
	if call.ID != "" {
		if ident, ok := call.Func.(*ast.IdentExpr); ok {
			return ident.Name, call.ID, true
		}
	}
	switch f := call.Func.(type) {
	case *ast.IdentExpr:
		// Bare ident with an event handler — treat as element call so the
		// handler body lands on an ir.NodeInst rather than disappearing.
		// @error is the exception: it's a per-call error handler on a
		// function call, not an element event. Skip the element-ref path
		// when every handler is @error.
		hasNonError := false
		hasHandler := false
		for _, a := range call.Args.Args {
			if eh, ok := a.(ast.EventHandler); ok {
				hasHandler = true
				if eh.Name != "error" {
					hasNonError = true
				}
			}
		}
		if hasHandler && hasNonError {
			return f.Name, "", true
		}
	}
	return "", "", false
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
	if x.Call != nil {
		call.ErrorHandler = c.extractCallErrorHandler(x.Call)
	}
	return &ir.CallStmt{AST: x, Call: call}
}

// extractCallErrorHandler finds an inline @error handler in a CallExpr's args
// and returns it as a typed ir.EventHandler (param defaulted to ErrorEvent
// when no annotation was given). Returns nil if no @error is attached.
func (c *checker) extractCallErrorHandler(call *ast.CallExpr) *ir.EventHandler {
	for i := range call.Args.Args {
		eh, ok := call.Args.Args[i].(ast.EventHandler)
		if !ok || eh.Name != "error" {
			continue
		}
		return c.buildErrorHandler(&eh)
	}
	return nil
}

// buildErrorHandler type-checks the body of an @error handler with the param
// defaulted to the stdlib ErrorEvent struct when no explicit type was given.
// Reports a diagnostic if the handler has more than one param or if the
// declared type is not ErrorEvent.
func (c *checker) buildErrorHandler(eh *ast.EventHandler) *ir.EventHandler {
	errEvtType := c.errorEventType()
	params := make([]*ir.Param, len(eh.Params.Params))
	for i, p := range eh.Params.Params {
		typ := c.resolveType(p.Type)
		if typ == nil || typ.Kind == ir.TypeDyn {
			typ = errEvtType
		}
		params[i] = &ir.Param{Name: p.Name, Type: typ}
	}
	if len(params) > 1 {
		c.error(eh.Pos, "@error handler accepts at most one ErrorEvent parameter")
	}
	if len(params) == 1 && errEvtType != nil && !params[0].Type.IsAssignableTo(errEvtType) {
		c.error(eh.Pos, "@error handler parameter must be ErrorEvent, got %s", params[0].Type)
	}
	fn := &ir.Func{Params: params}
	c.pushScope()
	for _, p := range params {
		c.scope.Declare(p)
	}
	fn.Block = c.checkBlockIR(&eh.Body)
	c.popScope()
	return &ir.EventHandler{AST: eh, Name: eh.Name, Func: fn}
}

// errorEventType returns the resolved stdlib ErrorEvent type, or nil if unavailable.
func (c *checker) errorEventType() *ir.Type {
	if sd, ok := c.symtab.Types["ErrorEvent"].(*ir.StructDef); ok {
		return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	}
	return nil
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
		if w.Name != "" {
			c.scope.Declare(w)
		}
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
	case "errorBoundary":
		return c.buildErrorBoundary(vn)
	}

	// Look up component — supports bare ("Foo") and qualified ("pkg.Foo") names.
	var comp *ir.Component
	// qualifiedLocal is the local part of a qualified name, used as the IR
	// NodeInst.Name (e.g. "html.input" → "input") so downstream codegen sees
	// the native tag, not the namespace-qualified form.
	var qualifiedLocal string
	if ns, field, ok := strings.Cut(name, "."); ok {
		if sym, sok := c.scope.Lookup(ns); sok {
			if nsSym, nok := sym.(*ir.Namespace); nok {
				if nsSym.Pkg != nil {
					if fsym, ok := nsSym.Pkg.Symbols.LookupComponent(field); ok {
						if c.rejectUnexported(vn.Pos, fsym) {
							return nil
						}
						if co, ok := fsym.(*ir.Component); ok {
							comp = co
							qualifiedLocal = field
						}
					}
				}
				// Fall back to the namespace's platform Resolve (e.g.
				// html.input → raw HTML <input> component; gtk4.GtkBox →
				// GIR-resolved component). Lets platform-extension bodies
				// reference native tags qualified by platform name.
				if comp == nil && nsSym.Resolve != nil {
					if resolved := nsSym.Resolve(field); resolved != nil {
						if co, ok := resolved.(*ir.Component); ok {
							comp = co
							qualifiedLocal = field
						}
					}
				}
			}
		}
	} else if sym, ok := c.symtab.LookupComponent(name); ok {
		if c.rejectUnexported(vn.Pos, sym) {
			return nil
		}
		if co, ok := sym.(*ir.Component); ok {
			comp = co
		}
	}

	// If not a component, check if it's a function or bare expression.
	if comp == nil {
		if sym, ok := c.scope.Lookup(name); ok {
			// Context name used as visual node → ContextProvider.
			if ctx, ok := sym.(*ir.Context); ok {
				return c.buildContextProvider(vn, ctx)
			}
			// Bare variable used as statement — flag it (no block, no args).
			if _, isFunc := sym.(*ir.Func); !isFunc && !vn.Block.IsDefined() && len(vn.Args.Args) == 0 {
				c.error(vn.Pos, "expression must be a statement")
				return nil
			}
			if fn, ok := sym.(*ir.Func); ok {
				// This is a function call, not a visual node.
				props, _, _ := c.checkAndSplitArgs(vn.Args, nil)
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
	// or if the platform/namespace can resolve it. Platform-resolved
	// names (e.g. GtkButton via GIR) bind into `comp` so the NodeInst
	// carries the resolved Component (including any platform-provided
	// Native metadata) downstream.
	if comp == nil && name != "" {
		if _, inScope := c.scope.Lookup(name); !inScope {
			if c.resolveQualifiedIdent(name) {
				// namespace member — leave comp nil.
			} else if resolved := c.resolvePlatformIdent(name); resolved != nil {
				if co, ok := resolved.(*ir.Component); ok {
					comp = co
				}
			} else {
				c.error(vn.Pos, "unknown component %q", name)
			}
		}
	}

	// Validate props on known components.
	if comp != nil {
		c.validateVisualNodeProps(vn, comp)
	}

	children := c.checkBlockIR(&vn.Block)
	if comp != nil && comp.AST != nil {
		ct := comp.ChildrenType
		n := len(children)
		switch {
		case ct == nil && n > 0:
			c.error(vn.Pos, "component %s does not accept children", comp.Name)
		case ct != nil && ct.Kind != ir.TypeList && ct.Kind != ir.TypeOption && n != 1:
			c.error(vn.Pos, "component %s requires exactly one child", comp.Name)
		case ct != nil && ct.Kind == ir.TypeOption && n > 1:
			c.error(vn.Pos, "component %s accepts at most one child", comp.Name)
		}
		// Validate shape children: when ChildrenType is list<shape>, every child
		// must itself have list<shape> ChildrenType (which is how shapes are identified).
		if ct != nil && ct.Kind == ir.TypeList && len(ct.Elems) > 0 && ct.Elems[0].Kind == ir.TypeShape {
			for _, child := range children {
				ni, ok := child.(*ir.NodeInst)
				isShape := ok && ni.Component != nil &&
					ni.Component.ChildrenType != nil &&
					ni.Component.ChildrenType.Kind == ir.TypeList &&
					len(ni.Component.ChildrenType.Elems) > 0 &&
					ni.Component.ChildrenType.Elems[0].Kind == ir.TypeShape
				if !isShape {
					childName := "unknown"
					if ok && ni.Component != nil {
						childName = ni.Component.Name
					} else if ok {
						childName = ni.Name
					}
					c.error(vn.Pos, "expected shape component, got %s", childName)
				}
			}
		}
	}
	props, handlers, bindings := c.checkAndSplitArgs(vn.Args, comp)

	// Extract key= arg for loop diffing.
	var keyExpr ir.Expr
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == "key" && arg.Value != nil {
			keyExpr = c.checkExpr(arg.Value)
			break
		}
	}

	emitName := name
	if qualifiedLocal != "" {
		emitName = qualifiedLocal
	}
	return &ir.NodeInst{
		AST:       vn,
		Name:      emitName,
		Component: comp,
		Props:     props,
		Handlers:  handlers,
		Bindings:  bindings,
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
	// Skip validation for platform-synthesized components (e.g. raw HTML tags
	// or bubbletea/fyne blueprint marker tags). These are created on the fly
	// by Platform.Resolve and have no declared Props/Events — the platform
	// codegen reads their args directly.
	if comp.AST == nil && len(comp.Props) == 0 && len(comp.Events) == 0 {
		return
	}
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
func (c *checker) checkAndSplitArgs(args ast.ArgList, comp *ir.Component) ([]ir.Arg, []ir.EventHandler, []ir.PropBinding) {
	var props []ir.Arg
	var handlers []ir.EventHandler
	seen := make(map[string]ast.Pos)

	// Build ordered prop list for positional binding.
	var orderedProps []*ir.Prop
	if comp != nil {
		orderedProps = comp.Props
	}
	boundProps := make(map[string]bool)

	// Order check: positional after named is an error.
	// Spread args (...expr) expand to named props and are exempt from this check.
	seenNamed := false
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok {
			if arg.Name == "key" {
				continue
			}
			if arg.Name != "" && !strings.HasPrefix(arg.Name, ":") {
				seenNamed = true
			} else if arg.Name == "" && seenNamed && arg.Value != nil {
				if _, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
					continue // spread expands to named props; not a positional arg
				}
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				return nil, nil, nil
			}
		}
	}

	positional := 0
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "key" {
				continue // handled separately by checkVisualNodeIR
			}
			// Struct spread: ...expr expands struct fields as named props.
			if arg.Value != nil {
				if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
					operandIR := c.checkExpr(spread.Operand)
					operandType := exprType(operandIR)
					if operandType == nil || operandType.Kind != ir.TypeStruct {
						typStr := "(nil)"
						if operandType != nil {
							typStr = operandType.String()
						}
						c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
						continue
					}
					sd := operandType.Decl.(*ir.StructDef)
					seenNamed = true
					for _, f := range sd.Fields {
						if comp == nil {
							continue // no component context; can't match prop names
						}
						propType := componentPropType(comp, f.Name)
						if propType == nil {
							continue // no matching prop; ignore silently
						}
						if boundProps[f.Name] {
							c.error(spread.Pos, "prop %q already provided on component %s", f.Name, comp.Name)
							continue
						}
						var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
						if f.Type.Kind != ir.TypeDyn && propType.Kind != ir.TypeDyn {
							selExpr = wrapIfNeeded(selExpr, propType)
						}
						boundProps[f.Name] = true
						props = append(props, ir.Arg{Name: f.Name, Value: selExpr})
					}
					continue
				}
			}
			var resolvedName string
			var expected *ir.Type

			if arg.Name == "" {
				// Positional: bind to next prop slot (only when comp is known).
				if comp == nil {
					// No component context — pass through unchanged (dynamic call).
					resolvedName = ""
				} else if positional >= len(orderedProps) {
					if arg.Value != nil {
						c.error(*arg.Value.ExprPos(), "too many positional arguments")
					}
					positional++
					continue
				} else {
					p := orderedProps[positional]
					resolvedName = p.Name
					expected = p.Type
					positional++
				}
			} else {
				// Named.
				resolvedName = arg.Name
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if comp != nil {
					expected = componentPropType(comp, propName)
				}
			}

			var val ir.Expr
			if arg.Value != nil {
				val = c.checkExprExpecting(arg.Value, expected)
				c.requireValueType(exprType(val), *arg.Value.ExprPos())
				// Implicit call: func() T used where T is expected.
				if expected != nil {
					actual := exprType(val)
					if actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
						if adapted, ok := adaptLiteralZero(val, expected); ok {
							val = adapted
						} else if callExpr, _ := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
							val = c.checkExpr(callExpr)
						}
					}
					if expected.Kind != ir.TypeDyn {
						val = wrapIfNeeded(val, expected)
					}
				}
			}
			if comp != nil && resolvedName != "" {
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if boundProps[propName] {
					pos := args.Pos
					if arg.Value != nil {
						pos = *arg.Value.ExprPos()
					}
					c.error(pos, "prop %q already provided on component %s", propName, comp.Name)
					continue
				}
				boundProps[propName] = true
			}
			props = append(props, ir.Arg{Name: resolvedName, NamePos: arg.NamePos, Value: val})

		case ast.EventHandler:
			if prevPos, exists := seen[arg.Name]; exists {
				c.error(arg.Pos, "duplicate event handler %q (first at %v)", arg.Name, prevPos)
				continue
			}
			seen[arg.Name] = arg.Pos
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

	// Arity: all required props must be bound (user-defined components only;
	// stdlib component props without defaults are optional by platform convention).
	if comp != nil && !comp.Stdlib {
		for _, p := range comp.Props {
			if !boundProps[p.Name] && p.Default == nil {
				c.error(args.Pos, "missing required prop %q on component %s", p.Name, comp.Name)
			}
		}
	}

	return c.extractBindings(comp, props, handlers)
}

// checkComponentCallArgs validates and type-checks a component call (text(value="hi"))
// against the component's prop and event declarations, returning resolved args.
func (c *checker) checkComponentCallArgs(call *ast.CallExpr, comp *ir.Component) []ir.CallArg {
	var result []ir.CallArg

	// Order check: positional after named is an error.
	// Spread args (...expr) expand to named props and are exempt from this check.
	seenNamed := false
	for _, a := range call.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			if arg.Name != "" && arg.Name != "key" {
				seenNamed = true
			} else if arg.Name == "" && seenNamed && arg.Value != nil {
				if _, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
					continue // spread expands to named props; not a positional arg
				}
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				return nil
			}
		}
	}

	boundProps := make(map[string]bool)
	positional := 0
	for _, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "key" {
				continue // handled by loop diffing
			}
			// Struct spread: ...expr expands struct fields as named props.
			if arg.Value != nil {
				if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
					operandIR := c.checkExpr(spread.Operand)
					operandType := exprType(operandIR)
					if operandType == nil || operandType.Kind != ir.TypeStruct {
						typStr := "(nil)"
						if operandType != nil {
							typStr = operandType.String()
						}
						c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
						continue
					}
					sd := operandType.Decl.(*ir.StructDef)
					seenNamed = true
					for _, f := range sd.Fields {
						propType := componentPropType(comp, f.Name)
						if propType == nil {
							continue // no matching prop; ignore silently
						}
						if boundProps[f.Name] {
							c.error(spread.Pos, "prop %q already provided on component %s", f.Name, comp.Name)
							continue
						}
						var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
						if f.Type.Kind != ir.TypeDyn && propType.Kind != ir.TypeDyn {
							selExpr = wrapIfNeeded(selExpr, propType)
						}
						result = append(result, ir.CallArg{Name: f.Name, Value: selExpr})
						boundProps[f.Name] = true
					}
					continue
				}
			}
			var resolvedName string
			var expected *ir.Type

			if arg.Name == "" {
				if positional >= len(comp.Props) {
					if arg.Value != nil {
						c.error(*arg.Value.ExprPos(), "too many positional arguments")
					}
					positional++
					continue
				}
				p := comp.Props[positional]
				resolvedName = p.Name
				expected = p.Type
				positional++
			} else {
				resolvedName = arg.Name
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if !componentHasProp(comp, propName) && !componentHasEvent(comp, propName) {
					c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
					continue
				}
				expected = componentPropType(comp, propName)
			}

			if arg.Value != nil {
				// Duplicate prop check.
				boundKey := resolvedName
				if strings.HasPrefix(boundKey, ":") {
					boundKey = boundKey[1:]
				}
				if boundKey != "" && boundProps[boundKey] {
					c.error(arg.NamePos, "prop %q already provided on component %s", resolvedName, comp.Name)
					continue
				}
				argExpr := c.checkExprExpecting(arg.Value, expected)
				actual := exprType(argExpr)
				c.requireValueType(actual, *arg.Value.ExprPos())
				if expected != nil && actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
					if adapted, ok := adaptLiteralZero(argExpr, expected); ok {
						argExpr = adapted
					} else if callExpr, _ := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
						argExpr = c.checkExpr(callExpr)
					} else {
						c.error(*arg.Value.ExprPos(), "cannot pass %s as %s", actual, expected)
					}
				}
				if expected != nil && expected.Kind != ir.TypeDyn {
					argExpr = wrapIfNeeded(argExpr, expected)
				}
				result = append(result, ir.CallArg{Name: resolvedName, NamePos: arg.NamePos, Value: argExpr})
				if boundKey != "" {
					boundProps[boundKey] = true
				}
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
				c.scope.Declare(&ir.Param{Name: p.Name, Type: typ})
			}
			c.checkBlock(&arg.Body)
			c.popScope()
			if !componentHasEvent(comp, arg.Name) {
				c.error(*call.Func.ExprPos(), "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}

	// Arity: required props must be bound (user-defined components only;
	// stdlib component props without defaults are optional by platform convention).
	if !comp.Stdlib {
		for _, p := range comp.Props {
			if !boundProps[p.Name] && p.Default == nil {
				c.error(call.Args.Pos, "missing required prop %q on component %s", p.Name, comp.Name)
			}
		}
	}

	return result
}

// validateCallStmtComponentArgs validates prop/event names on a component call
// parsed as a CallStmt. Value checking is deferred to checkAndSplitArgs.
func (c *checker) validateCallStmtComponentArgs(call *ast.CallExpr, comp *ir.Component) {
	// Skip validation for platform-synthesized components (raw native tags
	// like html.progress) — they have no declared Props/Events; the platform
	// codegen reads their args directly. Mirrors validateVisualNodeProps.
	if comp.AST == nil && len(comp.Props) == 0 && len(comp.Events) == 0 {
		return
	}
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

// hoistForLoopWindowIDs scans a for-loop body for window declarations with
// non-empty #ids and declares each as a list<Window> symbol in the current
// (enclosing) scope. After optimizer expansion, the bound value is the list
// of unrolled windows; inside the loop body, the same id remains a scalar
// Window (declared per-iteration during normal body checking).
func (c *checker) hoistForLoopWindowIDs(block *ast.StmtBlock) []*ir.Var {
	if block == nil || !block.IsDefined() || c.windowType == nil {
		return nil
	}
	seen := map[string]bool{}
	var vars []*ir.Var
	c.collectForLoopWindowIDs(block, seen, &vars)
	return vars
}

func (c *checker) collectForLoopWindowIDs(block *ast.StmtBlock, seen map[string]bool, vars *[]*ir.Var) {
	if block == nil || !block.IsDefined() {
		return
	}
	for _, s := range block.Stmts {
		c.collectForLoopWindowIDsStmt(s, seen, vars)
	}
}

func (c *checker) collectForLoopWindowIDsStmt(s ast.Stmt, seen map[string]bool, vars *[]*ir.Var) {
	switch n := s.(type) {
	case *ast.VisualNode:
		if visualNodeTarget(n) == "window" && n.ID != "" {
			if !seen[n.ID] {
				seen[n.ID] = true
				// Skip if a symbol with this name already exists in the
				// enclosing scope (e.g., a package-level window with the
				// same id — duplicate-id checking belongs elsewhere).
				if _, ok := c.scope.Lookup(n.ID); !ok {
					v := &ir.Var{
						Name:    n.ID,
						Type:    ir.ListOf(c.windowType),
						IsConst: true,
					}
					c.scope.Declare(v)
					*vars = append(*vars, v)
				}
			}
		}
		// Don't descend into a window's body — nested windows hoist
		// against their own enclosing for, not this one.
	case *ast.IfStmt:
		c.collectForLoopWindowIDs(&n.Body, seen, vars)
		c.collectForLoopWindowIDs(&n.Else, seen, vars)
	case *ast.ForStmt:
		// Inner for-loops hoist their own ids; don't double-declare here.
	case *ast.PlatformStmt:
		c.collectForLoopWindowIDs(&n.Body, seen, vars)
	}
}
