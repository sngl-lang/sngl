package checker

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
	"maps"
	"slices"
)

// exprType extracts the resolved type from an ir.Expr, returning TypDyn for nil.
//
// Neither arm may become a panic. lspcore.Analyze type-checks a document that
// failed to parse, and a partial parse really does hand the checker an
// expression with a nil operand -- `[...]`, `[...()]`, `true ? : 2` and
// `const(())` all reach the nil arm today.
func exprType(e ir.Expr) *ir.Type {
	if e == nil {
		return dynFallback("no expression to take the type of")
	}
	if t := e.ExprType(); t != nil {
		return t
	}
	return dynFallback("%T carries no type", e)
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
	if sd := treeStruct(t); sd != nil {
		c.error(pos, "%s names a tree, which has no values", sd.Name)
		return true
	}
	return false
}

// treeStruct is the tree declaration t names, or nil. A tree struct holds
// nothing and no value of it exists: naming one says which family a component
// or a slot belongs to.
func treeStruct(t *ir.Type) *ir.StructDef {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	sd, ok := t.Decl.(*ir.StructDef)
	if !ok || !sd.IsTree {
		return nil
	}
	return sd
}

func (c *checker) checkExpr(e ast.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	if c.nativeValues {
		// An encoded enum member arrives as the literal it erased to, so it
		// has to be claimed before inference reads it as a plain float or
		// string. Nested values reach here too, which is why this sits at the
		// dispatch rather than in CheckNativeValue.
		if ed := expectedEnumDef(c.expected); ed != nil {
			if x, ok := c.nativeEnumMember(e, ed); ok {
				return x
			}
		}
	}
	// A null test narrows what it tested for the branch where it holds, and
	// this is the one place every read of a value passes through.
	return c.applyNarrowing(c.inferExpr(e))
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
	default:
		// Every ast.Expr the parser builds in a value position has a rule
		// above; a new node type without one is a compiler bug.
		panic(fmt.Sprintf("sngl: no type rule for expression %T", x))
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
		// A leading '-' is a separate UnaryExpr, so Raw is the unsigned
		// magnitude here. The expected type steers a bare literal toward a
		// sized width (range-checked); otherwise it stays plain int.
		typ = c.typeIntLiteral(x.Pos, x.Raw, false, c.expected)
	case ast.LiteralFloat:
		typ = c.typeFloatLiteral(c.expected)
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		// The AST carries the source spelling; the IR carries what it stands for.
		v, _ := x.StringValue()
		return &ir.Literal{AST: x, Type: TypString, Value: v}
	case ast.LiteralBool:
		typ = TypBool
	case ast.LiteralNull:
		typ = TypNull
	case ast.LiteralColor:
		return c.lowerHexLiteral(x)
	default:
		// LiteralUnit is the ninth kind and never reaches here: the parser
		// wraps it in an *ast.UnitLiteral, which inferExpr dispatches away.
		panic(fmt.Sprintf("sngl: no type for literal kind %v", x.Kind))
	}
	return &ir.Literal{AST: x, Type: typ, Value: x.Raw}
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
		return &ir.Literal{AST: x, Type: TypString, Value: x.Raw}
	}
	sym, ok := c.scope.Lookup("color")
	if !ok {
		// Stdlib not yet registered. Carry the raw hex as a string literal;
		// callers in this state are bootstrap paths that don't propagate.
		return &ir.Literal{AST: x, Type: TypString, Value: x.Raw}
	}
	sd, ok := sym.(*ir.StructDef)
	if !ok {
		return &ir.Literal{AST: x, Type: TypString, Value: x.Raw}
	}
	intLit := func(n int) *ir.Literal {
		return &ir.Literal{Type: TypInt, Value: strconv.Itoa(n)}
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
	// Value is the number and Suffix is the unit, the same split ir.Literal
	// makes for every other kind: Raw is the spelling, Value is what it stands
	// for. Carrying the spelling here instead left the suffix in both halves,
	// so every consumer that wanted the magnitude trimmed it back off and
	// every one that wanted the spelling risked writing it twice.
	num := strings.TrimSuffix(x.Raw, x.Suffix)
	ud, ok := c.unitBySuffix[x.Suffix]
	if !ok {
		c.error(x.Pos, "unknown unit suffix %q", x.Suffix)
		return &ir.Literal{AST: &x.LiteralExpr, Type: TypDyn, Value: num, Suffix: x.Suffix}
	}
	return &ir.Literal{AST: &x.LiteralExpr, Type: ud.SymType(), Value: num, Suffix: x.Suffix}
}

func (c *checker) inferIdent(x *ast.IdentExpr) ir.Expr {
	sym, ok := c.scope.Lookup(x.Name)
	if !ok {
		// When expected type is an enum, resolve bare member names.
		if c.expected != nil && c.expected.Kind == ir.TypeEnum {
			if ed, ok := c.expected.Decl.(*ir.EnumDef); ok {
				if c.enumMember(x.Pos, ed, x.Name) {
					return &ir.Ident{AST: x, Type: c.expected, Name: x.Name, Member: x.Name}
				}
			}
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
			if fn, ok := c.lookupMethod(c.currentComponent.Name, x.Name); ok {
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
		c.error(x.Pos, "undefined: %s%s", x.Name, c.stdlibHint(x.Name))
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
			// registerRootContextDecl leaves Typ nil only on its three
			// bad-default paths, each of which reported first.
			typ = ir.TypDyn
		}
		return &ir.ContextRead{AST: x, Ref: ctx, Typ: typ}
	}
	t := c.symType(sym)
	if t == nil {
		t = dynNoSymType(sym, "symbol %q (%T) has no type", x.Name, sym)
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
		reason = s.Foreign.Unusable
	case *ir.Var:
		reason = s.Foreign.Unusable
	}
	if reason != "" {
		c.error(pos, "%s cannot be used: %s", name, reason)
	}
}

// optionElem unwraps a well-formed option<T> to T; other types (including a
// malformed option with no element) pass through unchanged, so callers can
// treat `T` and `option<T>` uniformly.
func optionElem(t *ir.Type) *ir.Type {
	if t != nil && t.Kind == ir.TypeOption && len(t.Elems) == 1 {
		return t.Elems[0]
	}
	return t
}

// comparableEq reports whether two operand types can be compared with == / !=.
// Strict "like types" rule: same kind, numeric-to-numeric, one side is null, or
// an option<T> against T / option<T>.
func comparableEq(left, right *ir.Type) bool {
	if left == nil || right == nil {
		return true
	}
	if left.Kind == ir.TypeDyn || right.Kind == ir.TypeDyn {
		return true
	}
	if left.Kind == ir.TypeNull || right.Kind == ir.TypeNull {
		return true
	}
	// An option<T> compares against another option<T>, a bare T, or null
	// (handled above). Unwrap the option(s) and compare the element types, so
	// `opt == value` means "present and equal" and `option<int> ==
	// option<string>` is still rejected.
	if left.Kind == ir.TypeOption || right.Kind == ir.TypeOption {
		return comparableEq(optionElem(left), optionElem(right))
	}
	if left.IsNumeric() && right.IsNumeric() {
		// Numerics compare only at the same width and signedness; a mixed
		// pair needs an explicit conversion (untyped literals are already
		// adapted to the other operand's type before this check).
		return left.Equal(right)
	}
	if left.Kind == right.Kind {
		return true
	}
	return false
}

func (c *checker) inferBinary(x *ast.BinaryExpr) ir.Expr {
	leftExpr := c.checkExpr(x.Left)
	// `a && b` evaluates b only where a held, so a null test in a narrows what
	// follows it; `a || b` evaluates b only where a failed, which is the same
	// fact negated.
	restoreNarrow := func() {}
	if x.Op == ast.BinAnd || x.Op == ast.BinOr {
		facts := map[narrowKey]narrowFact{}
		narrowingsFrom(leftExpr, x.Op == ast.BinAnd, facts)
		var right ir.Expr
		restoreNarrow = c.pushNarrowings(facts, nil, &right)
		defer func() { restoreNarrow() }()
		rightExpr := c.checkExpr(x.Right)
		right = rightExpr
		return c.finishBinary(x, leftExpr, rightExpr)
	}
	rightExpr := c.checkExpr(x.Right)
	return c.finishBinary(x, leftExpr, rightExpr)
}

func (c *checker) finishBinary(x *ast.BinaryExpr, leftExpr, rightExpr ir.Expr) ir.Expr {
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

	// Unify numeric operands: an untyped numeric literal adapts to the other
	// operand's concrete type so arithmetic and comparison run at one width.
	// Two concrete-but-different numeric types are left as-is and rejected by
	// the per-operator checks below (the user must add an explicit cast).
	if left.IsNumeric() && right.IsNumeric() && !left.Equal(right) {
		leftExpr, rightExpr = c.unifyNumericOperands(leftExpr, rightExpr, x.Pos)
		left = exprType(leftExpr)
		right = exprType(rightExpr)
	}

	skip := left.Kind == ir.TypeDyn || right.Kind == ir.TypeDyn

	var typ *ir.Type
	switch x.Op {
	case ast.BinAnd, ast.BinOr:
		if !skip && (left.Kind != ir.TypeBool || right.Kind != ir.TypeBool) {
			c.error(x.Pos, "operator %s not defined for %s and %s", x.Op, left, right)
		}
		typ = TypBool
	case ast.BinEq, ast.BinNeq:
		if !skip && !comparableEq(left, right) {
			c.error(x.Pos, "operator %s not defined for %s and %s", x.Op, left, right)
		}
		typ = TypBool
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				// Numeric comparisons require the same width and signedness.
				if !left.Equal(right) {
					c.error(x.Pos, "operator %s not defined for %s and %s (add an explicit conversion)", x.Op, left, right)
				}
			case left.Kind == ir.TypeString && right.Kind == ir.TypeString:
				// string comparisons OK
			case left.SameUnitType(right) && left.IsSingleBaseUnit():
				// single-base unit comparisons OK (e.g. duration)
			default:
				c.error(x.Pos, "operator %s not defined for %s and %s", x.Op, left, right)
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
				c.error(x.Pos, "operator %s not defined for %s and %s", x.Op, left, right)
			}
			typ = left
			if typ.Kind != ir.TypeUnit {
				typ = right
			}
		} else {
			if !skip && (!left.IsNumeric() || !right.IsNumeric()) {
				c.error(x.Pos, "operator %s not defined for %s and %s", x.Op, left, right)
			}
			typ = c.unifyNumeric(left, right, x.Pos, x.Op)
		}
	case ast.BinMul:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.unifyNumeric(left, right, x.Pos, x.Op)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit * scalar
			case left.IsNumeric() && right.Kind == ir.TypeUnit:
				typ = right // scalar * unit
			default:
				c.error(x.Pos, "operator * not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.unifyNumeric(left, right, x.Pos, x.Op)
		}
	case ast.BinDiv:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.unifyNumeric(left, right, x.Pos, x.Op)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit / scalar
			case left.SameUnitType(right):
				typ = TypFloat // unit / unit → dimensionless ratio
			default:
				c.error(x.Pos, "operator / not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.unifyNumeric(left, right, x.Pos, x.Op)
		}
	case ast.BinMod:
		if !skip {
			switch {
			case left.IsNumeric() && right.IsNumeric():
				typ = c.unifyNumeric(left, right, x.Pos, x.Op)
			case left.Kind == ir.TypeUnit && right.IsNumeric():
				typ = left // unit % scalar
			default:
				c.error(x.Pos, "operator %% not defined for %s and %s", left, right)
				typ = TypDyn
			}
		} else {
			typ = c.unifyNumeric(left, right, x.Pos, x.Op)
		}
	default:
		panic(fmt.Sprintf("sngl: no type rule for binary operator %v", x.Op))
	}
	return &ir.Binary{AST: x, Type: typ, Op: x.Op, Left: leftExpr, Right: rightExpr}
}

// unifyNumeric returns the common type of two numeric binary operands. Operands
// must already share a width and signedness (untyped literals are adapted to
// the other operand up front); a mismatch is an error requiring an explicit
// conversion. Dyn on either side flows through without an error.
func (c *checker) unifyNumeric(left, right *ir.Type, pos ast.Pos, op ast.BinaryOp) *ir.Type {
	if left.Kind == ir.TypeDyn {
		return right
	}
	if right.Kind == ir.TypeDyn {
		return left
	}
	if left.Equal(right) {
		return left
	}
	// Unit operands keep their prior lenient behavior: a unit combined with a
	// bare scalar yields the unit type (unit * / % scalar is handled by the
	// caller's dedicated branches; this path only sees the fallthrough).
	if left.Kind == ir.TypeUnit {
		return left
	}
	if right.Kind == ir.TypeUnit {
		return right
	}
	c.error(pos, "operator %s not defined for %s and %s (add an explicit conversion)", op, left, right)
	return left
}

func (c *checker) inferUnary(x *ast.UnaryExpr) ir.Expr {
	// Negation of a bare numeric literal in a sized-numeric context folds into
	// a single signed literal so the width's range check sees the true value
	// (e.g. `int8 = -128` is in range even though +128 is not) and codegen
	// emits a valid typed constant rather than -(int8(128)).
	if x.Op == ast.UnaryNeg && c.expected.IsSized() {
		if lit, ok := x.Operand.(*ast.LiteralExpr); ok {
			switch lit.Kind {
			case ast.LiteralInt:
				typ := c.typeIntLiteral(x.Pos, lit.Raw, true, c.expected)
				return &ir.Literal{AST: lit, Type: typ, Value: "-" + lit.Raw}
			case ast.LiteralFloat:
				typ := c.typeFloatLiteral(c.expected)
				return &ir.Literal{AST: lit, Type: typ, Value: "-" + lit.Raw}
			}
		}
	}
	// `&x` names storage, not a value, so the unwrap a narrowing would wrap
	// the read in has nothing to be the address of.
	if x.Op == ast.UnaryAddr {
		defer c.suspendNarrowing()()
	}
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
		panic(fmt.Sprintf("sngl: no type rule for unary operator %v", x.Op))
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
	// The arms of a ternary are the two branches of an if, so a null test in
	// the condition narrows them the same way.
	thenFacts := map[narrowKey]narrowFact{}
	narrowingsFrom(condExpr, true, thenFacts)
	var thenRegion ir.Expr
	restoreThen := c.pushNarrowings(thenFacts, nil, &thenRegion)
	thenExpr := c.checkExprExpecting(x.Then, c.expected)
	thenRegion = thenExpr
	restoreThen()

	elseFacts := map[narrowKey]narrowFact{}
	narrowingsFrom(condExpr, false, elseFacts)
	var elseRegion ir.Expr
	restoreElse := c.pushNarrowings(elseFacts, nil, &elseRegion)
	elseExpr := c.checkExprExpecting(x.Else, c.expected)
	elseRegion = elseExpr
	restoreElse()
	typ := exprType(thenExpr)
	// Both ternary arms must share a numeric type. An untyped literal arm
	// adapts to the other arm's concrete type; two concrete-but-different
	// numeric arms need an explicit conversion (no implicit promotion).
	tt, et := exprType(thenExpr), exprType(elseExpr)
	if tt != nil && et != nil && tt.IsNumeric() && et.IsNumeric() && !tt.Equal(et) {
		thenExpr, elseExpr = c.unifyNumericOperands(thenExpr, elseExpr, x.Pos)
		typ = exprType(thenExpr)
		if tt2, et2 := exprType(thenExpr), exprType(elseExpr); !tt2.Equal(et2) {
			c.error(x.Pos, "ternary branches have mismatched types %s and %s (add an explicit conversion)", tt2, et2)
		}
	}
	return &ir.Ternary{AST: x, Type: typ, Cond: condExpr, Then: thenExpr, Else: elseExpr}
}

func (c *checker) inferCall(x *ast.CallExpr) ir.Expr {
	if sel, ok := x.Func.(*ast.SelectExpr); ok {
		return c.inferMethodCall(sel, x)
	}

	// Check if it's a builtin conversion. Only primitive→primitive conversions
	// are permitted; struct/func/component/list/option operands must define a
	// user method and be called as `x.string()` etc. — the cast form never
	// dispatches to user methods.
	if ident, ok := x.Func.(*ast.IdentExpr); ok {
		// Scalar-primitive casts are dispatched from the shared registry
		// (ir/builtins.go); struct-backed casts (color/date/time) follow.
		if b, ok := ir.LookupBuiltinScalar(ident.Name); ok && b.Convertible {
			return c.inferBuiltinConversion(x, b.Type, ident.Name)
		}
		switch ident.Name {
		case "color", "date", "time", "datetime":
			// These are stdlib StructDef-backed types, not primitives, so the
			// cast form is just convert-to-struct. Look up the StructDef type
			// from scope; if absent (pre-stdlib), fall back to dyn so the
			// diagnostic comes from the regular path.
			structTyp := ir.TypDyn
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if t := c.symType(sym); t != nil {
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
					if _, ok := c.lookupMethod(name, ident.Name); ok {
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

	// Anything still not func-typed here is not callable: the conversion
	// (cast) forms above have already claimed the castable types, and
	// components were handled as instantiation. Reaching this point with a
	// type name means a cast to a type that has no cast, and with a value
	// means calling a non-function.
	//
	// TypeDyn is exempt: a dyn callee is unknown by construction, so a call on
	// it stays permissive.
	if calleeType.Kind != ir.TypeFunc && calleeType.Kind != ir.TypeDyn {
		c.errorNotCallable(x, calleeExpr, calleeType)
		return &ir.Call{AST: x, Type: TypDyn, Args: c.checkCallArgs(x.Args, nil)}
	}

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
		// primitiveConvertible takes the types themselves: color, date, time
		// and datetime are all TypeStruct, so the kinds alone cannot say which
		// one, and an identity cast has to be told apart from a cast between
		// two different string-repr types.
		if from != nil && !from.Equal(target) && !primitiveConvertible(from, target) {
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

	if c.rejectVoidReceiver(receiver, sel.Operand) {
		return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
	}

	// Namespace function or component call: ns.func() or ns.Component().
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
		if sym, ok := c.scope.Lookup(ident.Name); ok {
			if ns, ok := sym.(*ir.Namespace); ok && ns.Pkg != nil {
				if ns.Pkg != nil {
					if fsym, ok := ns.Pkg.Symbols.LookupMember(sel.Field); ok {
						if c.rejectUnexported(sel.Pos, fsym) {
							return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
						}
						// Component in namespace — validate visibility and props.
						if comp, ok := fsym.(*ir.Component); ok {
							// Private component filter: only for imported user
							// packages. Identity, not name — the stdlib is
							// bound under whatever alias the file chose.
							if ns.Pkg != c.stdlibPkg && len(sel.Field) > 0 && sel.Field[0] >= 'a' && sel.Field[0] <= 'z' {
								c.error(sel.Pos, "unknown component %q in package %s", sel.Field, ident.Name)
								return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
							}
							args := c.checkComponentCallArgs(call, comp)
							c.validateCallStmtComponentArgs(call, comp)
							return &ir.Call{AST: call, Type: comp.SymType(), Receiver: receiverExpr, Args: args}
						}
						t := c.symType(fsym)
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
				// A name the package does not declare (e.g. html.code).
				if resolved := c.nsMember(sel.Pos, ns, sel.Field); resolved != nil {
					t := resolved.SymType()
					if t == nil {
						t = dynFallback("member %s.%s (%T) has no type", ident.Name, sel.Field, resolved)
					}
					args := c.checkCallArgs(call.Args, nil)
					return &ir.Call{AST: call, Type: t, Receiver: receiverExpr, Args: args}
				}
				c.error(sel.Pos, "unknown component %q in package %s", sel.Field, ident.Name)
				return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
			}
		}
	}

	receiverExpr, receiver = callComputedOperand(receiverExpr, receiver)

	typeName := receiver.String()
	if receiver.Decl != nil {
		if owner, isSym := receiver.Decl.(ir.Symbol); isSym &&
			c.rejectForeignUnexported(sel.Pos, owner, typeName, sel.Field) {
			return &ir.Call{AST: call, Type: TypDyn, Args: c.checkCallArgs(call.Args, nil)}
		}
	}
	// The receiver's declaration owns its members, so ask it directly. Only
	// when the type has no declaration to ask — a builtin generic, whose
	// members hang off the bare constructor name — does the name matter.
	fn, ok := ir.MemberOf(receiver.Decl, sel.Field)
	if !ok {
		fn, ok = c.lookupMethod(typeName, sel.Field)
	}
	// Fallback for generic types: list<int> → "list", option<int> → "option",
	// map<K,V> → "map".
	if !ok {
		switch receiver.Kind {
		case ir.TypeList:
			fn, ok = c.lookupMethod("list", sel.Field)
		case ir.TypeOption:
			fn, ok = c.lookupMethod("option", sel.Field)
		case ir.TypeMap:
			fn, ok = c.lookupMethod("map", sel.Field)
		}
	}
	// A method reached through `option<Struct>` resolves against the struct,
	// the same way a field read through one does (see inferSelect). Asked after
	// `option`'s own members, so a name the option itself declares still wins.
	if !ok && receiver.Kind == ir.TypeOption && len(receiver.Elems) == 1 &&
		receiver.Elems[0] != nil && receiver.Elems[0].Kind == ir.TypeStruct {
		if fn, ok = ir.MemberOf(receiver.Elems[0].Decl, sel.Field); ok {
			receiver = receiver.Elems[0]
			typeName = receiver.String()
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
				for i, tp := range sig.RecvTypeParams {
					bindings[tp.Name] = receiver.Elems[i]
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
		// A static call names the receiver as its first argument, so the
		// receiver's type params are inferable from the arguments like any
		// other. Handing them to the same inference is what makes
		// `Box.same(b)` mean what `b.same()` means.
		if isStatic && len(sig.RecvTypeParams) > 0 {
			merged := *sig
			merged.TypeParams = append(append([]ir.TypeParam(nil), sig.RecvTypeParams...), sig.TypeParams...)
			merged.RecvTypeParams = nil
			sig = &merged
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
			// Infer the rest against the parameters the call actually fills.
			// For an instance call in the receiver-as-param form the receiver
			// occupies param 0 and is not in call.Args, so inferring against
			// the unshifted list matches argument 0 against the receiver's
			// parameter and a method-level type param never binds.
			inferSig := sig
			if recvParamStyle && !isStatic && len(sig.Params) > 0 {
				shifted := *sig
				shifted.Params = sig.Params[1:]
				inferSig = &shifted
			}
			inferred := c.inferTypeParams(inferSig, call.Args)
			if inferSig != sig {
				merged := *sig
				merged.Params = append([]*ir.Param{sig.Params[0]}, inferred.Params...)
				merged.Return = inferred.Return
				sig = &merged
			} else {
				sig = inferred
			}
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
			if sf := c.structField(sel.Pos, sd, sel.Field); sf != nil {
				if sf.Type != nil && sf.Type.Kind == ir.TypeFunc {
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
	return &ir.Call{
		AST:      call,
		Type:     dynSpread(receiver, "no method %q on %s", sel.Field, receiver),
		Receiver: receiverExpr,
		Args:     args,
		Event:    event,
	}
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
		case *ast.SlotNode:
			// Both forms of `slot` carry a block written in *this* component's
			// body -- a population's content at a call site, an insertion
			// point's fallback at a declaration -- so an id inside either is
			// this component's to resolve. Without this case a ref inside
			// `slot name { ... }` was undefined, while the same ref among
			// ordinary children resolved, because those arrive as the parent
			// node's Block.
			if comp := c.findHostComponentAST(n.Block.Stmts, id); comp != nil {
				return comp
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
			if comp := c.findHostComponentAST(n.Else.Stmts, id); comp != nil {
				return comp
			}
		}
	}
	return nil
}

// findDescendantHost searches the components instantiated within comp's body
// (transitively, with cycle detection) for an element ref named id, returning
// its host component. A ref living inside a child component is collected across
// the whole rendered subtree, so callers type it as list<host>. Recursion is
// bounded by `visited` — a self-instantiating component (e.g. a recursive tree
// view) is searched once. Returns nil if no descendant declares the id.
func (c *checker) findDescendantHost(comp *ir.Component, id string, visited map[string]bool) *ir.Component {
	if comp == nil || comp.AST == nil || visited[comp.Name] {
		return nil
	}
	visited[comp.Name] = true
	for _, child := range c.childComponents(comp.AST.Body.Stmts) {
		if host := c.findHostComponentAST(child.AST.Body.Stmts, id); host != nil {
			return host
		}
		if host := c.findDescendantHost(child, id, visited); host != nil {
			return host
		}
	}
	return nil
}

// childComponents returns the user components instantiated directly in stmts
// (descending through if/for/platform branches and nested node blocks, but not
// into the instantiated components' own definitions — that is findDescendantHost's
// job). Both `Comp()` call-statement and `Comp { }` visual-node forms count.
func (c *checker) childComponents(stmts []ast.Stmt) []*ir.Component {
	var out []*ir.Component
	lookup := func(name string) {
		if name == "" {
			return
		}
		if sym, ok := c.scope.Lookup(name); ok {
			if comp, ok := sym.(*ir.Component); ok && comp.AST != nil {
				out = append(out, comp)
			}
		}
	}
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ast.VisualNode:
				lookup(visualNodeTarget(n))
				walk(n.Block.Stmts)
			case *ast.CallStmt:
				if name, _, isElem := elementRefCallInfo(n.Call); isElem {
					lookup(name)
				} else if id, ok := n.Call.Func.(*ast.IdentExpr); ok {
					lookup(id.Name)
				}
			case *ast.IfStmt:
				walk(n.Body.Stmts)
				walk(n.Else.Stmts)
			case *ast.ForStmt:
				walk(n.Body.Stmts)
				walk(n.Else.Stmts)
			}
		}
	}
	walk(stmts)
	return out
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
		ir.TypeList, ir.TypeOption, ir.TypeMap, ir.TypeRemote:
		return true
	}
	return false
}

// methodNamed reports whether the type declares a method of this name, which
// is what a field read of a type with no fields usually is: the call left off.
func methodNamed(t *ir.Type, name string) bool {
	if t == nil {
		return false
	}
	decl, ok := t.Decl.(*ir.StructDef)
	if !ok {
		return false
	}
	_, found := decl.Methods[name]
	return found
}

// hasNoLegitimateFields reports whether a type cannot have any selector
// fields. Used to surface "no field X on type Y" diagnostics that would
// otherwise silently degrade to TypDyn. Kept conservative: a type qualifies
// only when it has no field-style accessor at all, which rules out most kinds
// (color.r, list.length, struct fields, component vars/props, enum members)
// and everything carrying no shape information (dyn, generic params, anonymous
// structs).
//
// remote.Value is one: it declares no fields and reaches its three facts
// through methods alone, so `box.value` is `box.value()` with the call left
// off — which degraded to dyn, passed every prop it was handed, and reached a
// backend as a field read of a struct that has none.
func hasNoLegitimateFields(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool, ir.TypeRemote:
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
		return ir.TypDyn
	}
	if sig.Return == nil {
		return TypVoid
	}
	return sig.Return
}

// rejectVoidReceiver reports a field or method named on an expression that
// produces no value. Nothing can be selected through one, so leaving it
// unreported typed the whole select dyn -- which accepted any field and any
// method name at all, `nothing().nosuchmethod()` included. The position is the
// receiver's rather than the select's: what is wrong is the thing on the left.
func (c *checker) rejectVoidReceiver(t *ir.Type, operand ast.Expr) bool {
	if t == nil || t.Kind != ir.TypeVoid {
		return false
	}
	pos := ast.Pos{}
	if p := operand.ExprPos(); p != nil {
		pos = *p
	}
	c.error(pos, "expression yields no value")
	return true
}

func (c *checker) inferSelect(x *ast.SelectExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	operand := exprType(operandExpr)

	if c.rejectVoidReceiver(operand, x.Operand) {
		return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
	}

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

	// A field read through `option<Struct>` resolves against the struct, which
	// is what every backend already emits -- Go reads through the pointer an
	// option is. Only the type it was resolved against changes; no deref node
	// is synthesized, because there is nothing for one to lower to.
	//
	// Without this the whole select degraded to dyn: `contents.value().status`
	// worked, `contents.value().nope` worked exactly as well, and both reached
	// a backend as a field read of whatever was there.
	if operand != nil && operand.Kind == ir.TypeOption && len(operand.Elems) == 1 &&
		operand.Elems[0] != nil && operand.Elems[0].Kind == ir.TypeStruct {
		operand = operand.Elems[0]
	}

	// Placed beside the ref auto-deref because it answers the same question:
	// what value is the field actually being read off. A namespace, a type name
	// or an enum never has a func type, so nothing below is disturbed.
	operandExpr, operand = callComputedOperand(operandExpr, operand)

	{
		// Namespace member access: ns.field.
		if ident, ok := x.Operand.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if ns, ok := sym.(*ir.Namespace); ok {
					if ns.Pkg != nil {
						if fsym, ok := ns.Pkg.Symbols.LookupMember(x.Field); ok {
							if c.rejectUnexported(x.Pos, fsym) {
								return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
							}
							c.reportUnusable(x.Pos, ident.Name+"."+x.Field, fsym)
							// A context reached through its package is the
							// same context: `i18n.locale` reads what `locale`
							// reads where the name is in scope unqualified.
							if ctx, isCtx := fsym.(*ir.Context); isCtx {
								typ := ctx.Typ
								if typ == nil {
									typ = ir.TypDyn // already reported, as above
								}
								return &ir.ContextRead{Ref: ctx, Typ: typ}
							}
							t := c.symType(fsym)
							return &ir.Select{AST: x, Type: t, Operand: operandExpr, Field: x.Field}
						}
					}
					// A name the package does not declare. A wildcard may still
					// answer to it -- html's element set is open, so `html.div`
					// is a name nobody declared and still resolves.
					if resolved := c.nsMember(x.Pos, ns, x.Field); resolved != nil {
						t := resolved.SymType()
						if t == nil {
							t = dynFallback("member %s.%s (%T) has no type", ident.Name, x.Field, resolved)
						}
						return &ir.Select{AST: x, Type: t, Operand: operandExpr, Field: x.Field}
					}
					// Nothing declares it and no wildcard covers it. Reported
					// rather than typed dyn: a package's members are a closed
					// set unless it says otherwise, and a dyn here made every
					// misspelled member of every target package compile.
					if isExportedMemberName(x.Field) {
						c.error(x.Pos, "undefined: %s.%s", ident.Name, x.Field)
					}
					return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
				}
			}
		}

		// Enum member access: EnumType.member. Normalize to the same IR shape
		// as a bare enum member reference so codegen never sees a dangling
		// EnumType.member select that the target language doesn't define.
		if operand.Kind == ir.TypeEnum && operand.Decl != nil {
			if ed, ok := operand.Decl.(*ir.EnumDef); ok {
				if c.enumMember(x.Pos, ed, x.Field) {
					return &ir.Ident{Type: operand, Name: x.Field, Member: x.Field, Sym: ed}
				}
				if isExportedMemberName(x.Field) {
					c.error(x.Pos, "no member %q on enum %s", x.Field, ed.Name)
				}
			}
		}

		if operand.Kind == ir.TypeStruct && operand.Decl != nil {
			if sd, ok := operand.Decl.(*ir.StructDef); ok {
				// Build type-arg substitution bindings if the struct is generic and
				// the operand carries concrete type arguments (e.g. Box<int>.value).
				var typeArgBindings map[string]*ir.Type
				if len(sd.TypeParams) > 0 && len(operand.Elems) == len(sd.TypeParams) {
					typeArgBindings = make(map[string]*ir.Type, len(sd.TypeParams))
					for i, tp := range sd.TypeParams {
						typeArgBindings[tp.Name] = operand.Elems[i]
					}
				}
				if c.rejectForeignUnexported(x.Pos, sd, sd.Name, x.Field) {
					return &ir.Select{AST: x, Type: TypDyn, Operand: operandExpr, Field: x.Field}
				}
				for _, f := range sd.Fields {
					if f.Name == x.Field {
						if f.Foreign.Unusable != "" {
							c.error(x.Pos, "field %s.%s cannot be used: %s", sd.Name, f.Name, f.Foreign.Unusable)
						}
						fieldType := f.Type
						if typeArgBindings != nil {
							fieldType = fieldType.Substitute(typeArgBindings)
						}
						return &ir.Select{AST: x, Type: fieldType, Operand: operandExpr, Field: x.Field}
					}
				}
				if _, isMethod := sd.Methods[x.Field]; isMethod {
					c.error(x.Pos, "%s.%s is a method; write %s() to call it", sd.Name, x.Field, x.Field)
				} else {
					c.error(x.Pos, "no field %q on struct %s", x.Field, sd.Name)
				}
			}
		}

		// Component-instance member access: resolve the field against the
		// component's vars, props, methods, and element refs (its own body and,
		// as list<host>, its descendant components), fully typed. Applies to any
		// component-typed operand — `this.<name>` inside a method and `c.<name>`
		// on a component-typed param/var in ordinary code or a test alike — so
		// the feature is orthogonal to context. A name matching none of these is
		// a hard error, not a silent `dyn`.
		if operand.Kind == ir.TypeComponent && operand.Decl != nil {
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
				// Nested component methods live in the symtab method table keyed
				// by component name (not comp.Funcs). A zero-user-arg computed
				// (`c.total`) reads as its return value — the Select IR is what
				// interp/codegen already evaluate as a computed read, so type it
				// with the return type directly rather than routing through the
				// implicit-call machinery (whose result type would be lost when
				// the method's return is not yet inferred). A method that takes
				// arguments, referenced bare, is a method value (func type with
				// the synthetic receiver stripped); `c.foo(...)` calls resolve
				// via inferMethodCall.
				if fn, ok := c.lookupMethod(comp.Name, x.Field); ok {
					params := fn.Params
					if len(params) > 0 && params[0].Receiver {
						params = params[1:]
					}
					if len(params) == 0 {
						// A void method referenced bare types void, the same as
						// calling it -- callRetType's rule applied here too.
						// ensureReturnType runs first so an expression body whose
						// return is not inferred yet does not read as void.
						c.ensureReturnType(fn)
						return &ir.Select{AST: x, Type: callRetType(fn.FuncSig()), Operand: operandExpr, Field: x.Field}
					}
					funcType := &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{
						Params:     params,
						Return:     fn.Return,
						TypeParams: fn.TypeParams,
						Purity:     fn.Purity,
					}}
					return &ir.Select{AST: x, Type: funcType, Operand: operandExpr, Field: x.Field}
				}
				// Element-ref id declared in the component body (e.g. `c.btn`
				// for a `c.btn.click()` event trigger, or `c.m.it` chained):
				// its value type is the host element's component type. Resolve
				// against the operand's already-known component type so inline
				// chains work, not just bare-ident operands.
				if comp.AST != nil {
					// The host is named in the component's body, so it resolves
					// through the imports of the file that body was written in
					// -- not through this one's. A test naming `c.out` would
					// otherwise have to import the vocabulary the component
					// renders with before it could ask about a ref.
					restore := c.fileOf(compDeclPos(comp))
					host := c.findHostComponentAST(comp.AST.Body.Stmts, x.Field)
					// A ref declared inside a child component is collected across
					// the rendered subtree, so it reads as list<host> (e.g.
					// `c.lbl[0]`, or `c.val[i]` from a recursive view).
					var descendant *ir.Component
					if host == nil {
						descendant = c.findDescendantHost(comp, x.Field, map[string]bool{})
					}
					restore()
					if host != nil {
						return &ir.Select{AST: x, Type: host.SymType(), Operand: operandExpr, Field: x.Field}
					}
					if descendant != nil {
						return &ir.Select{AST: x, Type: ir.ListOf(descendant.SymType()), Operand: operandExpr, Field: x.Field}
					}
				}
				c.error(x.Pos, "no member %q on component %s", x.Field, comp.Name)
			}
		}

		// Built-in list/string .length yields int.
		if x.Field == "length" && (operand.Kind == ir.TypeList || operand.Kind == ir.TypeString) {
			return &ir.Select{AST: x, Type: TypInt, Operand: operandExpr, Field: x.Field}
		}
		if hasNoLegitimateFields(operand) {
			if methodNamed(operand, x.Field) {
				c.error(x.Pos, "%s.%s is a method; write %s() to call it", operand, x.Field, x.Field)
			} else {
				c.error(x.Pos, "no field %q on type %s", x.Field, operand)
			}
		}
	}

	return &ir.Select{
		AST:     x,
		Type:    dynSpread(operand, "no field %q on %s", x.Field, operand),
		Operand: operandExpr,
		Field:   x.Field,
	}
}

func (c *checker) inferIndex(x *ast.IndexExpr) ir.Expr {
	operandExpr := c.checkExpr(x.Operand)
	indexExpr := c.checkExpr(x.Index)
	operand := exprType(operandExpr)
	operandExpr, operand = callComputedOperand(operandExpr, operand)

	// An index on nothing: `xs.push(v)[0]` reaches here with a void operand,
	// and the fallback below would hand it back as dyn, which assigns to
	// anything. Reported here so the message names the line it was written on.
	if c.requireValueType(operand, x.Pos) {
		return &ir.Index{AST: x, Type: TypDyn, Operand: operandExpr, Idx: indexExpr}
	}

	if operand.Kind == ir.TypeMap {
		if len(operand.Elems) != 2 {
			// ir.MapOf is the only constructor and substitution preserves the
			// length, so a map type with any other arity is a compiler bug.
			panic(fmt.Sprintf("sngl: map type %s carries %d element types, want 2", operand, len(operand.Elems)))
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
	return &ir.Index{
		AST:     x,
		Type:    dynSpread(operand, "%s is not indexable", operand),
		Operand: operandExpr,
		Idx:     indexExpr,
	}
}

func (c *checker) inferStructLit(x *ast.StructExpr) ir.Expr {
	var sd *ir.StructDef
	if c.nativeValues {
		// The declaration is the one the importer built: named by the value
		// itself where its runtime could say which type it was, and otherwise
		// reached through the expected type.
		sd = expectedStructDef(c.expected)
		if x.Native != nil {
			var bad bool
			if bad, sd = c.nativeStructDef(x, sd); bad {
				return &ir.Literal{Type: TypDyn}
			}
		} else if sd != nil && x.Name != "" && x.Name != sd.Name {
			c.error(x.Pos, "encoded value is a %s, but a %s was expected", x.Name, sd.Name)
			return &ir.Literal{Type: TypDyn}
		}
	} else if x.Anon != nil {
		// `struct { a int }{a = 1}` spells its own type, which wins over
		// whatever was expected.
		if t := c.resolveAnonStruct(x.Anon); t.Kind == ir.TypeStruct {
			sd, _ = t.Decl.(*ir.StructDef)
		}
	} else if x.Package != "" {
		// Qualified: pkg.Struct{...}
		if sym, ok := c.scope.Lookup(x.Package); ok {
			if ns, ok := sym.(*ir.Namespace); ok && ns.Pkg != nil {
				if tsym, ok := ns.Pkg.Symbols.LookupMemberType(x.Name); ok {
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

	var fields []ir.FieldInit
	named := map[string]bool{}
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
		name := f.Name
		field := c.structField(f.NamePos, sd, f.Name)
		if field != nil {
			expected = field.Type
			// Identical to f.Name for SNGL source, where the field was found
			// by that name; under nativeValues it is the SNGL name of a field
			// written with its foreign one.
			name = field.Name
		}
		val := c.checkExprExpecting(f.Value, expected)
		if field != nil {
			// `TreeNode{left = leaf1}` against `left option<TreeNode>` reached
			// codegen as a bare TreeNode, and Go's option is *T -- the one
			// assignment position that read the declared type and then threw
			// the conversion away.
			val = wrapOptionIfNeeded(val, expected)
		}
		// Validate field exists on struct. An unexported field of another
		// package is already reported by structField.
		if sd != nil && field == nil && isExportedMemberName(f.Name) {
			c.error(x.Pos, "unknown field %q on struct %s", f.Name, sd.Name)
		}
		// A spread carries no name, so `{...base, x = 2}` is an override
		// rather than a duplicate; only two written fields are one.
		if named[name] {
			c.error(f.NamePos, "duplicate field %q in struct literal", f.Name)
		}
		named[name] = true
		fields = append(fields, ir.FieldInit{Name: name, NamePos: f.NamePos, Value: val})
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
		fields = withFieldDefaults(sd, fields)
		return &ir.StructLit{AST: x, Type: typ, Def: sd, Fields: fields}
	}
	// Nothing named a type, so the literal's own values do.
	anonFields, ok := c.anonFieldsFromInits(fields)
	if !ok {
		return &ir.StructLit{AST: x, Type: &ir.Type{Kind: ir.TypeStruct}, Fields: fields}
	}
	anon := c.internAnonStruct(anonFields)
	return &ir.StructLit{AST: x, Type: anon.SymType(), Def: anon, Fields: fields}
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
	// An empty `{}` is a valid empty map of any key type — there are no ident
	// keys to (mis)interpret as strings.
	if len(x.Fields) == 0 {
		return &ir.MapLitIR{Type: mapType, Entries: nil}
	}
	var entries []ir.MapEntry
	for _, f := range x.Fields {
		if f.Spread {
			c.error(x.Pos, "spread not supported in map literal")
			continue
		}
		var keyIR ir.Expr
		if keyT.Kind == ir.TypeString {
			keyLit := ast.NewStringLiteral(f.Name)
			keyIR = &ir.Literal{AST: keyLit, Type: TypString, Value: f.Name}
		} else {
			// A non-string key type means the name is not a name: it is an
			// expression written where a field name would go, and it has to
			// resolve to the key type. `{true = "yes"}` for a
			// map<bool, string> is the case that matters, now that true is a
			// declaration rather than something the parser recognises.
			key := c.checkExprExpecting(&ast.IdentExpr{Pos: x.Pos, Name: f.Name}, keyT)
			if kt := exprType(key); !kt.IsAssignableTo(keyT) {
				c.error(x.Pos, "map key %q is %s, not the key type %s", f.Name, kt, keyT)
				continue
			}
			keyIR = key
		}
		val := c.checkExprExpecting(f.Value, valT)
		entries = append(entries, ir.MapEntry{Key: keyIR, Value: val})
	}
	return &ir.MapLitIR{Type: mapType, Entries: entries}
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
	// A void element makes the literal a `list<void>`, which an annotated
	// target rejects but an inferred one accepts: `var m = [xs.push(v)]`
	// checked clean and reached codegen.
	for i, el := range elems {
		c.requireValueType(exprType(el), *x.Elements[i].ExprPos())
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
	// color/date/time/datetime are StructDef-backed but still stringify like
	// string-domain primitives in interpolation.
	if ir.StringReprStruct(t) {
		return true
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool,
		ir.TypeEnum, ir.TypeUnit, ir.TypeNull:
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
	if fn, ok := c.lookupMethod(typeName, "string"); ok {
		return &ir.Call{Type: TypString, Func: fn, Args: []ir.CallArg{{Value: expr}}}
	}
	// Generic-type fallbacks so list/option implementations can register under
	// their bare name and apply to any instantiation.
	switch t.Kind {
	case ir.TypeList:
		if fn, ok := c.lookupMethod("list", "string"); ok {
			return &ir.Call{Type: TypString, Func: fn, Args: []ir.CallArg{{Value: expr}}}
		}
		// No stdlib method yet — fall back to the generic stringify path so
		// `{myList}` still produces a rendered list at runtime. Platform
		// codegen for ir.Conversion over a list emits its language-native
		// formatter (fmt.Sprint / String() / toString()).
		return &ir.Conversion{Type: TypString, Operand: expr}
	case ir.TypeOption:
		if fn, ok := c.lookupMethod("option", "string"); ok {
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
			v, _ := lit.StringValue()
			partExpr = &ir.Literal{AST: lit, Type: TypString, Value: v}
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
		// Value is the value, not its source spelling — `""` here would be the
		// two-character string, which Go codegen duly renders as "\"\"".
		return &ir.Literal{Type: TypString, Value: ""}
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
	c.refuseParamMarks(x.Params.Params)
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

	c.pushScope()
	for _, p := range fn.Params {
		c.declare(x.Pos, p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	if x.Body != nil {
		// The body runs when the lambda is called, which may be after a test
		// that narrowed something it captured has stopped holding. The block
		// form gets this from enterFuncBody; an expression body never reaches
		// one, so it says the same thing here.
		restoreNarrow := c.clearNarrowings()
		bodyExpr := c.checkExprExpecting(x.Body, fn.Return)
		restoreNarrow()
		if fn.Return == nil {
			// Expression-body lambda with no annotation and no contextual
			// return type: take the body's type as the return type. A body
			// that yields nothing gives it nothing to return, and the lowered
			// `return xs.push(v)` is not something any target can spell.
			c.requireValueType(exprType(bodyExpr), *x.Body.ExprPos())
			fn.Return = exprType(bodyExpr)
			c.returnType = fn.Return
		}
		fn.Block = []ir.Stmt{&ir.Return{AST: &ast.ReturnStmt{Pos: *x.Body.ExprPos(), Value: x.Body}, Value: bodyExpr}}
	}
	if x.Block.IsDefined() {
		restore := c.enterFuncBody()
		fn.Block = c.checkBlockIR(&x.Block)
		restore()
	}
	c.returnType = prevReturn
	c.popScope()

	return &ir.Lambda{AST: x, Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Func: fn}
}

// inferTypeParams infers concrete types for generic type params by matching
// argument types against parameter types, then returns a substituted FuncSig.
func (c *checker) inferTypeParams(sig *ir.FuncSig, args ast.ArgList) *ir.FuncSig {
	bindings := make(map[string]*ir.Type)
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
	// A parameter the arguments could not pin is bound from the type the context
	// wants, which is the only way to write one that appears in the return type
	// alone: `remote.pending()` takes no arguments, and SNGL has no syntax for
	// naming a type argument at a call site, so without this such a function is
	// undeclarable rather than merely awkward.
	//
	// Arguments come first on purpose. An argument states the type directly,
	// while the expected type states what the result has to be assignable to,
	// and the two disagree wherever a conversion would have been applied.
	if c.expected != nil && c.expected.Kind != ir.TypeDyn {
		bindTypeParams(sig.Return, c.expected, bindings)
	}
	// A parameter neither pinned nor implied falls back to its default, the way
	// a struct's does when the type-argument list stops short.
	for _, tp := range sig.TypeParams {
		if tp.Default == nil {
			continue
		}
		if _, bound := bindings[tp.Name]; !bound {
			bindings[tp.Name] = tp.Default
		}
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
			seenNamed = true
			idx := -1
			for i, p := range sig.Params {
				if p.Name == arg.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
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

func (c *checker) checkArgExpr(value ast.Expr, p *ir.Param) ir.Expr {
	expr := c.checkExprExpecting(value, p.Type)
	actual := exprType(expr)
	c.requireValueType(actual, *value.ExprPos())
	if p.Type != nil && actual.Kind != ir.TypeDyn && p.Type.Kind != ir.TypeDyn && !actual.IsAssignableTo(p.Type) {
		if adapted, ok := adaptLiteralZero(expr, p.Type); ok {
			expr = adapted
		} else if callExpr, _ := c.implicitCall(value, actual, p.Type); callExpr != nil {
			expr = c.checkExpr(callExpr)
		} else if c.coerceValueToRef(actual, p, *value.ExprPos()) {
			// The value stands as it is, and is not wrapped in a conversion to
			// ref<T>: see refcoerce.go for why the argument keeps its own type.
			return expr
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

// checkEmitArgs checks and returns what an `event(...)` hands over. Nothing
// checked it before, so an override body could forward a value of any type into
// an event declared to carry another, and the mismatch surfaced as a type error
// in the generated program rather than against the declaration that was wrong.
//
// The argument is the payload, or there is none. A shorthand accepting the
// payload's single field instead would tie the rule to the payload's shape:
// adding a second field to a one-field event would stop every such call site
// checking, so a change that is backward compatible in the declaration would
// not be in the checker. Writing the payload out costs a call site nothing and
// keeps the two independent.
//
// The argument is checked against the payload rather than on its own, so a
// struct literal here is typed by the declaration -- `change({value = opt})`
// names no type and needs none.
func (c *checker) checkEmitArgs(pos ast.Pos, evt *ir.EventDecl, args ast.ArgList) []ir.CallArg {
	var vals []ast.Expr
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Value == nil {
			continue
		}
		vals = append(vals, arg.Value)
	}
	switch {
	case len(vals) == 0:
		return nil
	case len(vals) > 1:
		c.error(pos, "event %q takes at most one argument, got %d", evt.Name, len(vals))
		return nil
	case evt.Type == nil:
		c.error(pos, "event %q carries no payload, so it takes no argument", evt.Name)
		return nil
	}
	val := c.checkExprExpecting(vals[0], evt.Type)
	if got := exprType(val); got != nil && got.Kind != ir.TypeDyn &&
		evt.Type.Kind != ir.TypeDyn && !got.IsAssignableTo(evt.Type) {
		c.error(*vals[0].ExprPos(), "cannot use %s as %s for event %q", got, evt.Type, evt.Name)
		return nil
	}
	return []ir.CallArg{{Value: val}}
}

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
					c.declare(p.Pos, &ir.Param{
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
		if sd := treeStruct(typ); sd != nil {
			c.error(decl.Pos, "%s names a tree, which has no values", sd.Name)
			typ = TypDyn
		}
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
					want, got := ir.Contrast(typ, initType)
					c.error(decl.Pos, "cannot initialize %s with %s", want, got)
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
			sym := &ir.Var{
				AST:  decl,
				Name: name,
				Type: typ,
			}
			c.declare(decl.Pos, sym)
			out = append(out, &ir.LocalVar{
				Name: name,
				Type: typ,
				Init: initExpr,
				Sym:  sym,
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
		restoreLvalue := c.suspendNarrowing()
		targetExpr := c.checkExpr(x.Target)
		restoreLvalue()
		targetType := exprType(targetExpr)
		valueExpr := c.checkExprExpecting(x.Value, targetType)
		valueType := exprType(valueExpr)
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
		restoreLvalue := c.suspendNarrowing()
		targetExpr := c.checkExpr(x.Target)
		restoreLvalue()
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
		sym := &ir.Var{
			AST:  x,
			Name: x.Name,
			Type: typ,
		}
		c.declare(x.Pos, sym)
		return &ir.LocalVar{AST: x, Name: x.Name, Type: typ, Init: initExpr, Sym: sym}
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
	case *ast.BreakStmt:
		c.requireLoop(x.Pos, "break")
		return &ir.Break{AST: x}
	case *ast.ContinueStmt:
		c.requireLoop(x.Pos, "continue")
		return &ir.Continue{AST: x}
	case *ast.CallStmt:
		// context #id(...) is only valid at file top level; reject it here.
		if c.isContextDeclCallStmt(x) {
			c.error(x.Pos, "context decl only permitted at file top level")
			return nil
		}
		// Bodyless forms of root-ish visual nodes (e.g. `timer(...)` with
		// the tick handler inside the parens) parse as CallStmt but must
		// dispatch through the VisualNode special-cases so they register
		// on package/component instead of becoming a generic node instance.
		if id, ok := x.Call.Func.(*ast.IdentExpr); ok && x.Call.ID == "" {
			// `output` is matched by name: it is a build directive with its
			// own data structure, not a component, so nothing in scope
			// resolves to it (see registerRootVisualNode).
			// A named slot's insertion is written as an ordinary node, so a
			// bodyless one (`cell(r)`) parses as a call like any other and has
			// to come back through the node path to be recognised.
			isRootish := c.builtinNodeKind(id.Name) != ir.BuiltinNone || id.Name == "output" ||
				c.enclosingSlot(id.Name) != nil
			if isRootish {
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
					args := c.checkEmitArgs(x.Pos, evt, x.Call.Args)
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
						// Declared only: a wildcard answers to every name,
						// and one reached that way is named by the name it
						// matched — the branch below, which knows that.
						if ns.Pkg != nil {
							if fsym, ok := ns.Pkg.Symbols.LookupDeclaredComponent(sel.Field); ok {
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
						if comp == nil {
							if resolved := c.nsMember(sel.Pos, ns, sel.Field); resolved != nil {
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
			// A bodyless node parses as a call, so the boundary has to be read
			// on this path too -- `text(value=v)` in a function body is the
			// same dropped node `vbox { }` is.
			if c.rejectNodeInFuncBody(x.Pos, compName) {
				return nil
			}
			c.validateCallStmtComponentArgs(x.Call, comp)
			c.checkRequiredSlots(x.Pos, comp, nil)
			if slot := findSlot(comp, ir.DefaultSlot); slot != nil {
				c.checkSlotArity(x.Pos, slot, 0, "component "+comp.Name)
			}
			props, handlers, bindings := c.checkAndSplitArgs(x.Call.Args, c.bindComponentTypeParams(comp, x.Call.Args))
			return &ir.NodeInst{
				AST:       x,
				Name:      compName,
				Component: comp,
				Props:     bindWildcardName(comp, compName, props),
				Handlers:  handlers,
				Bindings:  bindings,
				Key:       c.keyArgExpr(x.Call.Args),
			}
		}
		// Children-less element references (`text #id(...)`, `button(@click)`)
		// parse as CallStmt but semantically behave like visual nodes — emit
		// NodeInst so event handlers and the #id are preserved in IR.
		if name, id, isElem := elementRefCallInfo(x.Call); isElem {
			if c.rejectNodeInFuncBody(x.Pos, name) {
				return nil
			}
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
			props, handlers, bindings := c.checkAndSplitArgs(x.Call.Args, c.bindComponentTypeParams(argsComp, x.Call.Args))

			// A stdlib element passes nil above so event args stay leniently
			// typed, and nil is also what makes checkAndSplitArgs leave a
			// positional prop unnamed -- "no component context; can't match
			// prop names". So `text #t({}, "hi")` reached the IR with no props
			// at all, and every reader dropped them: `c.t.value` was empty and
			// a snapshot printed `text()`. The names are recoverable here,
			// where the component is known, without disturbing the leniency.
			if argsComp == nil && elemComp != nil {
				props = namePositionalProps(elemComp, props)
			}
			return &ir.NodeInst{
				AST:       x,
				Name:      name,
				Component: elemComp,
				Props:     props,
				Handlers:  handlers,
				Bindings:  bindings,
				ID:        id,
				Key:       c.keyArgExpr(x.Call.Args),
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
		thenFacts := map[narrowKey]narrowFact{}
		narrowingsFrom(condExpr, true, thenFacts)
		var thenRegion []ir.Stmt
		restoreThen := c.pushNarrowings(thenFacts, &thenRegion, nil)
		body := c.checkBlockIR(&x.Body)
		thenRegion = body
		restoreThen()

		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			elseFacts := map[narrowKey]narrowFact{}
			narrowingsFrom(condExpr, false, elseFacts)
			var elseRegion []ir.Stmt
			restoreElse := c.pushNarrowings(elseFacts, &elseRegion, nil)
			elseBody = c.checkBlockIR(&x.Else)
			elseRegion = elseBody
			restoreElse()
		}
		return &ir.If{AST: x, Cond: condExpr, Body: body, Else: elseBody}
	case *ast.ForStmt:
		// No head at all is the forever loop. A head is checked before it is
		// classified, because what it *evaluates to* is what the loop does:
		// a bool is a condition tested before each iteration, anything else
		// is an iterable to walk.
		if x.Iter == nil {
			return c.checkHeadlessFor(x, nil)
		}
		iterExpr := c.checkExpr(x.Iter)
		iter := exprType(iterExpr)
		if iter.Kind == ir.TypeBool {
			return c.checkHeadlessFor(x, iterExpr)
		}
		// A zero-arg computed is called where its result is what is wanted, so
		// `for var x = items` iterates what `func items() list<T>` returns. There
		// is no single expected type to hand implicitCall here: any of the
		// three iterable kinds will do.
		if iter.Kind == ir.TypeFunc && iter.Sig != nil && len(iter.Sig.Params) == 0 && iter.Sig.Return != nil {
			switch iter.Sig.Return.Kind {
			case ir.TypeList, ir.TypeIter, ir.TypeMap:
				x.Iter = &ast.CallExpr{Pos: *x.Iter.ExprPos(), Func: x.Iter}
				iterExpr = c.checkExpr(x.Iter)
				iter = exprType(iterExpr)
			}
		}
		// Hoist window #ids declared inside the loop body as list<Window>
		// symbols in the enclosing scope, so refs outside the loop type-check
		// against the unrolled list. Done before pushScope so the symbol
		// lives in the parent scope.
		c.checkViewMapFor(x.Pos, iter)
		hoistedIDs := c.hoistForLoopWindowIDs(&x.Body)
		c.pushScope()
		// Resolve &-binding: only the ELEMENT loop var may be &-bound, only
		// over a mutable addressable list. The element is Key in single-var
		// form and Value in two-var form. `&` on the index (two-var Key) is an
		// error. An invalid `&` is reported and downgraded to a value binding.
		// TODO(lint): a plain (non-&) element loop var is a value copy, so
		// writing through its fields (`for var t = list { t.done!! }`) silently
		// updates a throwaway copy. Surface that as a lint error suggesting
		// `&t`; for now it compiles as a no-op.
		elemRef := x.KeyRef
		// A loop that declares nothing (`for seq.count(3) { }`) has no
		// variable to bind, to ref-bind, or to complain about: only the
		// iterable is checked, and each backend emits a discard for the
		// element.
		declares := x.Key != ""
		if x.Value != "" {
			if x.KeyRef {
				c.error(x.Pos, "& cannot bind the index variable; write `for var i, &%s = …`", x.Value)
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
		// Declare loop variables based on iterator type, keeping the symbols
		// so the For statement carries what its body's Idents resolve to.
		var keySym, valueSym *ir.LoopVar
		declKey := func(t *ir.Type) {
			if !declares {
				return
			}
			keySym = &ir.LoopVar{Name: x.Key, Type: t}
			c.declare(x.Pos, keySym)
		}
		declValue := func(t *ir.Type) {
			valueSym = &ir.LoopVar{Name: x.Value, Type: t}
			c.declare(x.Pos, valueSym)
		}
		elemType := TypDyn
		switch iter.Kind {
		case ir.TypeList:
			if len(iter.Elems) > 0 {
				elemType = iter.Elems[0]
			}
			if x.Value != "" {
				// for var key, value = list: key is index (int), value is element.
				declKey(TypInt)
				declValue(elemDeclType(elemType))
			} else {
				// for var item = list: item is element.
				declKey(elemDeclType(elemType))
			}
		case ir.TypeIter:
			if len(iter.Elems) > 0 {
				elemType = iter.Elems[0]
			}
			if x.Value != "" {
				// for var key, value = iter: key is index (int), value is element.
				declKey(TypInt)
				declValue(elemType)
			} else {
				// for var item = iter: item is element.
				declKey(elemType)
			}
		case ir.TypeMap:
			if x.Value == "" && declares {
				c.error(x.Pos, "iterating over map requires two variables: for var k, v = m")
			} else if len(iter.Elems) == 2 {
				// for var k, v = map: k is key type, v is value type.
				declKey(iter.Elems[0])
				declValue(iter.Elems[1])
				elemType = iter.Elems[1]
			}
		case ir.TypeDyn:
			if x.Value != "" {
				declKey(TypDyn)
				declValue(TypDyn)
			} else {
				declKey(TypDyn)
			}
		default:
			c.error(x.Pos, "for iterator must be list, iter, or map; got %s", iter)
			if x.Value != "" {
				declKey(TypDyn)
				declValue(TypDyn)
			} else {
				declKey(TypDyn)
			}
		}
		c.loopDepth++
		body := c.checkBlockIR(&x.Body)
		c.loopDepth--
		var elseBody []ir.Stmt
		if x.Else.IsDefined() {
			// The else runs when the body never did, so it is outside this
			// loop: an escape written there acts on whichever loop encloses
			// it, which is why loopEscapes reads a nested loop's else and not
			// its body.
			elseBody = c.checkBlockIR(&x.Else)
		}
		c.popScope()
		loop := &ir.For{AST: x, Key: x.Key, Value: x.Value, KeySym: keySym, ValueSym: valueSym, Iter: iterExpr, ElemType: elemType, Body: body, Else: elseBody, HoistedWindowIDs: hoistedIDs, RefElem: elemRef}
		c.checkViewForElse(x.Pos, loop, iter)
		return loop
	case *ast.SlotNode:
		return c.checkSlotNodeIR(x)
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
		c.declare(x.Pos, fn)
		c.checkFuncBody(fn)
		// When a `func` is declared inside a nested block (provider children,
		// if/for body), it would otherwise be built and scoped but never
		// appended to any IR collection — leaving call sites referencing an
		// undefined function. Lift to the enclosing component (analogous to
		// the LocalVar→Var promotion done by passNoContext for vars).
		if c.currentComponent != nil {
			c.currentComponent.Funcs = append(c.currentComponent.Funcs, fn)
		} else if c.pkg != nil {
			c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
		}
		return nil
	case *ast.Comment:
		return nil
	case *ast.DisabledDecl:
		return nil
	}
	return nil
}

// targetPkgScope builds (and caches) a scope containing the declarations of the
// target package sngl:<uri>, for either tier: a language ships a package the
// way a platform does, and source in it is written against its own
// declarations the same way.
func (c *checker) targetPkgScope(uri string) *ir.Scope {
	if c.platformScopeCache != nil {
		if s, ok := c.platformScopeCache[uri]; ok {
			// Clone so each insertion point gets its own parent chain.
			clone := NewScope(nil)
			maps.Copy(clone.Symbols, s.Symbols)
			clone.Wildcards = slices.Clone(s.Wildcards)
			return clone
		}
	}

	name, kind, ok := targetTierName(uri)
	if !ok {
		return nil
	}
	t := c.lookupTargetIn(name, kind)
	if t == nil || targetUnavailable(t) != nil {
		return nil
	}
	if !c.hasLibPkg(uri) {
		return nil
	}
	// The same instance user code imports: a `platform x { ... }` block and an
	// `import "sngl:platform/x"` must name one declaration, not two.
	pkg := c.libPkg(uri)

	scope := NewScope(nil)
	maps.Copy(scope.Symbols, pkg.Symbols.Root.Symbols)
	scope.Wildcards = slices.Clone(pkg.Symbols.Root.Wildcards)
	// Declare the platform namespace with its package so qualified access
	// (e.g., html.Options) works inside platform blocks.
	c.bindLib(ast.Pos{}, scope, &ir.Namespace{Name: name, Pkg: pkg})

	if c.platformScopeCache == nil {
		c.platformScopeCache = make(map[string]*ir.Scope)
	}
	c.platformScopeCache[uri] = scope

	// Return a clone for this usage.
	clone := NewScope(nil)
	maps.Copy(clone.Symbols, scope.Symbols)
	clone.Wildcards = slices.Clone(scope.Wildcards)
	return clone
}

// nsMember resolves a name a namespace's package does not declare: a wildcard
// component of that package, which says in source which names it stands for.
func (c *checker) nsMember(pos ast.Pos, ns *ir.Namespace, name string) ir.Symbol {
	return c.scopeWildcard(pos, nsScope(ns), name)
}

func nsScope(ns *ir.Namespace) *ir.Scope {
	if ns == nil || ns.Pkg == nil || ns.Pkg.Symbols == nil {
		return nil
	}
	return ns.Pkg.Symbols.Root
}

// scopeWildcard is the wildcard binding of scope that covers name. A declared
// name was already looked up by the caller; two wildcards covering one name is
// an ambiguity rather than whichever was written first.
func (c *checker) scopeWildcard(pos ast.Pos, scope *ir.Scope, name string) ir.Symbol {
	if scope == nil {
		return nil
	}
	matches := scope.WildcardMatches(name)
	switch len(matches) {
	case 0:
		return nil
	case 1:
		return matches[0]
	default:
		c.error(pos, "%q matches wildcards %s and %s", name, matches[0].SymName(), matches[1].SymName())
		return nil
	}
}

// resolveQualifiedIdent reports whether a qualified name "ns.Field" resolves
// to a namespace member (component or func) via an in-scope Namespace.
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
		if _, ok := nsSym.Pkg.Symbols.LookupMember(field); ok {
			return true
		}
	}
	return false
}

// namePositionalProps gives each unnamed prop the name of the declared prop it
// binds to, matching checkAndSplitArgs's own rule: a positional argument takes
// the next declared prop, and a named one consumes no position. A wildcard prop
// has no position, so it is not among them.
func namePositionalProps(comp *ir.Component, props []ir.Arg) []ir.Arg {
	var ordered []*ir.Prop
	for _, p := range comp.Props {
		if p.Wildcard == "" {
			ordered = append(ordered, p)
		}
	}
	positional := 0
	for i := range props {
		if props[i].Name != "" {
			continue
		}
		if positional >= len(ordered) {
			break
		}
		props[i].Name = ordered[positional].Name
		positional++
	}
	return props
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
		c.declare(eh.Pos, p)
	}
	restore := c.enterFuncBody()
	fn.Block = c.checkBlockIR(&eh.Body)
	restore()
	c.popScope()
	return &ir.EventHandler{AST: eh, Name: eh.Name, Func: fn}
}

func (c *checker) errorEventType() *ir.Type {
	if sd := structDecl(c.symtab, "ErrorEvent"); sd != nil {
		return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	}
	return nil
}

// checkVisualNodeIR validates a visual node and returns the appropriate IR statement.
// Disambiguates components, platform elements, slots, and function calls.
func (c *checker) checkVisualNodeIR(vn *ast.VisualNode) ir.Stmt {
	name := visualNodeTarget(vn)

	// Built-in nodes — the compiler's own constructs, dispatched on the
	// #[builtin] mark of whatever the target resolves to rather than on the
	// literal name, so a user component of the same name shadows them (D3).
	kind, builtinComp := c.builtinNode(name)
	if kind != ir.BuiltinNone && c.rejectNodeInFuncBody(vn.Pos, name) {
		return nil
	}
	switch kind {
	case ir.BuiltinWindow:
		w := c.buildWindow(vn)
		c.bindWindow(vn.Pos, w)
		c.checkWindowBody(w)
		w.Checked = true
		return w
	case ir.BuiltinTimer:
		t := c.buildTimer(vn, builtinComp)
		if c.currentComponent != nil {
			c.currentComponent.Timers = append(c.currentComponent.Timers, t)
		} else {
			c.pkg.Timers = append(c.pkg.Timers, t)
		}
		return nil
	case ir.BuiltinErrorBoundary:
		return c.buildErrorBoundary(vn, builtinComp)
	case ir.BuiltinEffect:
		// Validated here and then left to the ordinary component path: an
		// effect is resolved, checked and lowered as the declaration it is,
		// and the kind says only that this node brackets a lifetime.
		c.checkEffectHandlers(vn)
	}
	// A named slot renders as an ordinary node: the tag is the slot's name and
	// the arguments are the values passed to it. Resolved before components so
	// a slot wins inside the body that declares it, which is what makes the
	// insertion unmarked; a slot sharing a component's name shadows it there,
	// as an inner-scope binding does anywhere else.
	if slot := c.enclosingSlot(name); slot != nil {
		return c.checkSlotInsertion(vn, slot)
	}

	// `output` is not a component (see registerRootVisualNode), so it stays a
	// literal-name match rather than resolving through scope.
	if name == "output" {
		if !c.cfg.IsMain {
			c.error(vn.Pos, "output declarations only permitted in main file")
		}
		c.buildOutputs(vn)
		return nil
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
				// A name the package does not declare: a wildcard component
				// of its own, else the platform's Resolve (gtk4.GtkBox →
				// GIR-resolved widget). Lets platform-extension bodies
				// reference native tags qualified by platform name.
				if comp == nil {
					if resolved := c.nsMember(vn.Pos, nsSym, field); resolved != nil {
						if co, ok := resolved.(*ir.Component); ok {
							comp = co
							qualifiedLocal = field
						}
					}
				}
			}
		}
	} else if sym, ok := c.lookupComponentInScope(vn.Pos, name); ok {
		if c.rejectUnexported(vn.Pos, sym) {
			return nil
		}
		if co, ok := sym.(*ir.Component); ok {
			comp = co
		}
	}

	// If not a component, check if it's a function or bare expression.
	if comp == nil {
		sym, ok := c.scope.Lookup(name)
		if !ok {
			// A qualified name: the scope holds the alias, not the dotted
			// string. A declaration reached through an import is reached the
			// same way whatever it is, so `i18n.locale(...) { }` finds the
			// context that `i18n.tr(...)` finds the function in.
			sym, ok = c.lookupQualified(name)
		}
		if ok {
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
				return &ir.CallStmt{AST: vn, Call: &ir.Call{Type: callRetType(fn.FuncSig()), Func: fn, Args: args}}
			}
		}
	}

	// Unknown component check — skip if name resolves to a variable
	// (e.g. timer condition guard like `running { ... }`), or if a namespace
	// can resolve it.
	if comp == nil && name != "" {
		if _, inScope := c.scope.Lookup(name); !inScope {
			if !c.resolveQualifiedIdent(name) {
				c.error(vn.Pos, "unknown component %q%s", name, c.stdlibHintFor(name, hintComponent))
			}
		}
	}

	if comp != nil {
		// Past the function-call fallthrough above, so the name is a
		// component rather than something that parsed like one.
		if c.rejectNodeInFuncBody(vn.Pos, name) {
			return nil
		}
		c.validateVisualNodeProps(vn, comp)
	}

	// The specialization is what the whole call site is checked against, so it
	// is minted before any of it -- a slot's content is checked here, above the
	// props, and against the unspecialized declaration `slot cell(T)` handed
	// the population a parameter named T. Once, not once per user: binding
	// walks the argument expressions, and a second walk reports each of their
	// diagnostics twice.
	spec := c.bindComponentTypeParams(comp, vn.Args)

	// A named slot's content is written in the callsite's block beside the
	// ordinary children, marked with `slot` so it reads as supplied rather
	// than rendered. Peel those off before the children are checked, so the
	// arity and tree-kind rules below see only what the anonymous slot gets.
	slotContent, childBlock := c.checkSlotPopulations(vn, spec)
	children := c.checkBlockIR(&childBlock)
	if spec != nil && spec.AST != nil {
		ct := spec.ChildrenType
		n := len(children)
		switch {
		case ct == nil && n > 0:
			c.error(vn.Pos, "component %s does not accept children", spec.Name)
		case ct != nil && ct.Kind != ir.TypeList && ct.Kind != ir.TypeOption && n != 1:
			c.error(vn.Pos, "component %s requires exactly one child", spec.Name)
		case ct != nil && ct.Kind == ir.TypeOption && n > 1:
			c.error(vn.Pos, "component %s accepts at most one child", spec.Name)
		}
		c.checkTreeMembership(vn.Pos, children, slotTree(spec, findSlot(spec, ir.DefaultSlot)), "in "+spec.Name)
	}
	props, handlers, bindings := c.checkAndSplitArgs(vn.Args, spec)

	emitName := name
	if qualifiedLocal != "" {
		emitName = qualifiedLocal
	}
	props = bindWildcardName(comp, emitName, props)
	return &ir.NodeInst{
		AST:       vn,
		Name:      emitName,
		Component: comp,
		Props:     props,
		Handlers:  handlers,
		Bindings:  bindings,
		Children:  children,
		Slots:     slotContent,
		ID:        vn.ID,
		Key:       c.keyArgExpr(vn.Args),
	}
}

// callComputedOperand inserts the elided call when expr is a bare reference to a
// zero-argument function used as an operand. `total.length()`, `doubled[0]` and
// `pt.x` all mean the computed's *result*: nothing in SNGL declares a method, an
// index or a field on a function, so a function reaching one of those positions
// can only be a call whose parentheses were left off — the same elision argument
// position gets through implicitCall.
//
// It is separate from implicitCall because these positions have no expected type
// to offer it. They do not need one: in argument position a bare function may
// legitimately be the value wanted, and there the expected type is what decides;
// here it never can be.
//
// All three positions used to fall through to a dyn result with no diagnostic —
// `plain.bogusMethod()` checked clean — and, because the operand stayed an Ident
// bound to the func rather than becoming a Call, reactivity never reached the
// vars the computed reads, so a prop reading one never re-rendered.
//
// Args stays nil: this is the implicit-receiver shape passNoImplicitRecv
// normalizes, so a component's computed picks up its `this` there rather than in
// two places that could disagree.
func callComputedOperand(expr ir.Expr, t *ir.Type) (ir.Expr, *ir.Type) {
	if t == nil || t.Kind != ir.TypeFunc || t.Sig == nil ||
		len(t.Sig.Params) != 0 || t.Sig.Return == nil {
		return expr, t
	}
	id, isIdent := expr.(*ir.Ident)
	if !isIdent {
		return expr, t
	}
	fn, isFunc := id.Sym.(*ir.Func)
	if !isFunc {
		return expr, t
	}
	ret := t.Sig.Return
	return &ir.Call{Type: ret, Func: fn}, ret
}

// implicitCall checks whether actual is a zero-arg func whose return type is
// assignable to expected. If so it wraps expr in a CallExpr
// and returns (callExpr, returnType). Otherwise returns (nil, actual).
func (c *checker) implicitCall(expr ast.Expr, actual, expected *ir.Type) (ast.Expr, *ir.Type) {
	if expected == nil || actual.Kind != ir.TypeFunc || actual.Sig == nil {
		return nil, actual
	}
	// Where a function is what is wanted, the function is the answer. Calling
	// it would be the one reading of `var h func() string = api.fetchHello`
	// that throws away what was asked for.
	if expected.Kind == ir.TypeFunc {
		return nil, actual
	}
	if len(actual.Sig.Params) != 0 || actual.Sig.Return == nil {
		return nil, actual
	}
	// The call is implied when its result can reach the expected type, not
	// only when it already is that type: `text(value=doubled)` where doubled
	// returns an int calls it and converts, as `text(value=doubled())` does.
	if !actual.Sig.Return.IsAssignableTo(expected) &&
		!primitiveConvertible(actual.Sig.Return, expected) {
		return nil, actual
	}
	call := &ast.CallExpr{Pos: *expr.ExprPos(), Func: expr}
	return call, actual.Sig.Return
}

// lookupQualified resolves an `alias.member` name through the namespace the
// alias binds.
func (c *checker) lookupQualified(name string) (ir.Symbol, bool) {
	alias, member, isQualified := strings.Cut(name, ".")
	if !isQualified {
		return nil, false
	}
	sym, ok := c.scope.Lookup(alias)
	if !ok {
		return nil, false
	}
	ns, isNS := sym.(*ir.Namespace)
	if !isNS || ns.Pkg == nil || ns.Pkg.Symbols == nil {
		return nil, false
	}
	return ns.Pkg.Symbols.LookupMember(member)
}

// structDeclOf is the declaration behind a plain struct type, and false for
// anything else -- a built-in that happens to be declared as a struct
// included, since those carry no fields to default.
func structDeclOf(t *ir.Type) (*ir.StructDef, bool) {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil, false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	if !ok || sd.Builtin != ir.BuiltinNone {
		return nil, false
	}
	return sd, true
}

// withFieldDefaults appends the declared default of every field the literal
// omits, so the value is complete before anything reads it. Doing it here
// rather than in each evaluator is what makes `color{r=255}` carry its alpha
// in the interpreter, the const folder and all four backends alike — they had
// disagreed, and the two that agreed were both wrong.
func withFieldDefaults(sd *ir.StructDef, fields []ir.FieldInit) []ir.FieldInit {
	if sd == nil {
		return fields
	}
	written := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.Spread {
			// A spread supplies whatever the runtime value holds, so which
			// fields it covers is not known here.
			return fields
		}
		written[f.Name] = true
	}
	for _, f := range sd.Fields {
		if f.Default == nil || written[f.Name] {
			continue
		}
		fields = append(fields, ir.FieldInit{Name: f.Name, Value: f.Default})
	}
	return fields
}

func componentPropType(comp *ir.Component, name string) *ir.Type {
	for _, p := range comp.Props {
		if p.Name == name {
			return p.Type
		}
	}
	// A name nobody declared still has a type when a wildcard prop covers it,
	// which is what makes its value checkable. The prop is a map keyed by the
	// name, so the value's type is the map's, not the prop's. An ambiguity is
	// reported by validateVisualNodeProps, at the use site; here it simply
	// types nothing.
	if p, ok, err := wildcardProp(comp, name); ok && err == nil {
		return wildcardValueType(p)
	}
	return nil
}

// wildcardValueType is the type a wildcard prop checks one matched value
// against: its map's value type. A wildcard prop that is not a map is a
// declaration error reported where it was written, and types nothing here.
func wildcardValueType(p *ir.Prop) *ir.Type {
	if p == nil || p.Type == nil || p.Type.Kind != ir.TypeMap || len(p.Type.Elems) != 2 {
		return nil
	}
	return p.Type.Elems[1]
}

// wildcardTarget is the wildcard prop a written prop name is collected into,
// or nil when a declared prop binds the name. A declared name always beats a
// wildcard, so the declared lookup comes first.
func wildcardTarget(comp *ir.Component, name string) *ir.Prop {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p.Name == name {
			return nil
		}
	}
	p, ok, err := wildcardProp(comp, name)
	if !ok || err != nil {
		return nil
	}
	// Only a map collects: a wildcard prop declared in source must be one
	// (checkWildcardPropType).
	if wildcardValueType(p) == nil {
		return nil
	}
	return p
}

// propTypeMismatch reports whether a prop value cannot reach its prop's type.
// wrapIfNeeded would otherwise mint a Conversion for it, and the language has
// no such cast: the same expression written out is a checker error.
func propTypeMismatch(got, expected *ir.Type) bool {
	if got == nil || expected == nil || got.Kind == ir.TypeDyn || expected.Kind == ir.TypeDyn {
		return false
	}
	if got.IsAssignableTo(expected) {
		return false
	}
	// An unresolved type parameter is the tail of an earlier error.
	if got.Kind == ir.TypeTypeParam || expected.Kind == ir.TypeTypeParam {
		return false
	}
	return true
}

// onComponent names the component in a diagnostic when there is one to name.
func onComponent(comp *ir.Component) string {
	if comp == nil {
		return ""
	}
	return " on component " + comp.Name
}

func componentEventType(comp *ir.Component, name string) *ir.Type {
	for _, e := range comp.Events {
		if e.Name == name {
			return e.Type
		}
	}
	return nil
}

func (c *checker) validateVisualNodeProps(vn *ast.VisualNode, comp *ir.Component) {
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
			if _, err := componentWildcardProp(comp, name); err != nil {
				c.error(vn.Pos, "%s", err)
			} else if !componentHasProp(comp, name) && !componentDeclaresEventNamed(comp, name) {
				c.error(vn.Pos, "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			if ok, err := componentWildcardEvent(comp, arg.Name); err != nil {
				c.error(vn.Pos, "%s", err)
			} else if !ok && !componentHasEvent(comp, arg.Name) {
				c.error(vn.Pos, "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}
}

// keyArgExpr returns the checked `key=` argument, the list-diffing key for a
// node in a loop. Every ir.NodeInst constructor calls this: the key is written
// the same way whichever form the node takes, so reading it in only some of
// them drops it silently.
func (c *checker) keyArgExpr(args ast.ArgList) ir.Expr {
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == "key" && arg.Value != nil {
			return c.checkExpr(arg.Value)
		}
	}
	return nil
}

// checkAndSplitArgs checks values and splits a checked ArgList into IR property
// assignments and event handlers. Unlike splitNodeArgs, this re-checks values
// to produce ir.Expr rather than using ast.Expr.
func (c *checker) checkAndSplitArgs(args ast.ArgList, comp *ir.Component) ([]ir.Arg, []ir.EventHandler, []ir.PropBinding) {
	var props []ir.Arg
	var handlers []ir.EventHandler
	seen := make(map[string]ast.Pos)

	// Build ordered prop list for positional binding.
	// A wildcard prop has no position: it stands for names, and binding one
	// positionally would give it a name nobody wrote.
	var orderedProps []*ir.Prop
	if comp != nil {
		for _, p := range comp.Props {
			if p.Wildcard == "" {
				orderedProps = append(orderedProps, p)
			}
		}
	}
	boundProps := make(map[string]bool)
	// Entries a wildcard prop collected, keyed by the prop's own name. Built
	// alongside props and appended as one map arg once every name is in.
	var wildcardEntries map[string][]ir.MapEntry
	// The first matched name routed into each wildcard prop, and the call site
	// that bound that prop under its own name. A call may do one or the other;
	// doing both is reported once the whole arg list has been read, so the
	// diagnostic does not depend on which spelling came first.
	var wildcardFirstName map[string]string
	var wildcardDirect map[string]ast.Pos
	noteDirect := func(name string, pos ast.Pos) {
		if comp == nil {
			return
		}
		for _, p := range comp.Props {
			if p.Name != name || p.Wildcard == "" {
				continue
			}
			if wildcardDirect == nil {
				wildcardDirect = map[string]ast.Pos{}
			}
			if _, dup := wildcardDirect[name]; !dup {
				wildcardDirect[name] = pos
			}
			return
		}
	}
	collectWildcard := func(wc *ir.Prop, name string, val ir.Expr) {
		if wildcardEntries == nil {
			wildcardEntries = map[string][]ir.MapEntry{}
		}
		if wildcardFirstName == nil {
			wildcardFirstName = map[string]string{}
		}
		key := &ir.Literal{Type: TypString, Value: name}
		wildcardEntries[wc.Name] = append(wildcardEntries[wc.Name], ir.MapEntry{Key: key, Value: val})
		if _, had := wildcardFirstName[wc.Name]; !had {
			wildcardFirstName[wc.Name] = name
		}
	}

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
						// A spread field is a written prop name like any
						// other, so a name a wildcard prop covers is
						// collected into it rather than becoming an arg under
						// its own name — which named no prop, and was dropped
						// by every backend.
						if wc := wildcardTarget(comp, f.Name); wc != nil {
							collectWildcard(wc, f.Name, selExpr)
							continue
						}
						noteDirect(f.Name, spread.Pos)
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
					// Nothing adapted it, and wrapIfNeeded would mint a cast
					// to a struct — which the language does not have, so the
					// same expression written out (Style("hi")) is a checker
					// error. Left alone it reached lowering as a panic when
					// the spread pass tried to resolve the struct type.
					if got := exprType(val); propTypeMismatch(got, expected) {
						c.error(*arg.Value.ExprPos(), "cannot use %s as %s for prop %q%s",
							got, expected, strings.TrimPrefix(resolvedName, ":"), onComponent(comp))
						continue
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
				pos := args.Pos
				if arg.Value != nil {
					pos = *arg.Value.ExprPos()
				}
				if boundProps[propName] {
					c.error(pos, "prop %q already provided on component %s", propName, comp.Name)
					continue
				}
				boundProps[propName] = true
				noteDirect(propName, pos)
			}
			// A wildcard prop collects the names it matched rather than
			// being one: the value is an entry keyed by the written name,
			// and every match on this call site lands in the same map.
			if wc := wildcardTarget(comp, strings.TrimPrefix(resolvedName, ":")); wc != nil {
				collectWildcard(wc, strings.TrimPrefix(resolvedName, ":"), val)
				continue
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
				c.declare(arg.Pos, p)
			}
			restore := c.enterFuncBody()
			fn.Block = c.checkBlockIR(&arg.Body)
			restore()
			c.popScope()
			handlers = append(handlers, ir.EventHandler{
				AST:  &arg,
				Name: arg.Name,
				Func: fn,
			})
		}
	}

	// One arg per wildcard prop, under the prop's own name, so the body that
	// declared it has something to read and every backend reaches the values
	// through the declaration rather than through the raw argument list.
	if comp != nil {
		for _, p := range comp.Props {
			entries := wildcardEntries[p.Name]
			if len(entries) == 0 {
				continue
			}
			// The prop's own name and a name its wildcard covers are two ways
			// to write the same map, and the second one appended here would
			// silently replace the first. Neither is wrong on its own, so the
			// call site is told to pick one rather than one being preferred.
			if pos, direct := wildcardDirect[p.Name]; direct {
				c.error(pos, "prop %q on component %s is bound directly and as %q, which its wildcard collects into it; bind the map or the matched names, not both",
					p.Name, comp.Name, wildcardFirstName[p.Name])
				continue
			}
			props = append(props, ir.Arg{Name: p.Name, Value: &ir.MapLitIR{Type: p.Type, Entries: entries}})
			boundProps[p.Name] = true
		}
	}

	// Arity: all required props must be bound (user-defined components only;
	// stdlib component props without defaults are optional by platform convention).
	if comp != nil && !comp.Stdlib {
		for _, p := range comp.Props {
			// A wildcard prop stands for names rather than being one, so
			// there is nothing to require: its own name binds nothing.
			if p.Wildcard != "" {
				continue
			}
			if !boundProps[p.Name] && p.Default == nil {
				c.error(args.Pos, "missing required prop %q on component %s", p.Name, comp.Name)
			}
		}
		// A prop the call omits is supplied here, so every consumer reads one
		// list of arguments rather than each evaluating the declaration's
		// defaults for itself. This is what a struct literal does with its
		// field defaults.
		for _, p := range comp.Props {
			if p.Default == nil || boundProps[p.Name] {
				continue
			}
			props = append(props, ir.Arg{Name: p.Name, Value: p.Default})
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
				if !componentHasProp(comp, propName) && !componentDeclaresEventNamed(comp, propName) {
					c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
					continue
				}
				expected = componentPropType(comp, propName)
			}

			if arg.Value != nil {
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
				c.declare(p.Pos, &ir.Param{Name: p.Name, Type: typ})
			}
			c.checkBlock(&arg.Body)
			c.popScope()
			if !componentAcceptsEvent(comp, arg.Name) {
				c.error(*call.Func.ExprPos(), "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}

	// Arity: required props must be bound (user-defined components only;
	// stdlib component props without defaults are optional by platform convention).
	if !comp.Stdlib {
		for _, p := range comp.Props {
			// A wildcard prop stands for names rather than being one, so
			// there is nothing to require: its own name binds nothing.
			if p.Wildcard != "" {
				continue
			}
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
			if _, err := componentWildcardProp(comp, propName); err != nil {
				c.error(*call.Func.ExprPos(), "%s", err)
			} else if !componentHasProp(comp, propName) && !componentDeclaresEventNamed(comp, propName) {
				c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
			}
		case ast.EventHandler:
			if ok, err := componentWildcardEvent(comp, arg.Name); err != nil {
				c.error(*call.Func.ExprPos(), "%s", err)
			} else if !ok && !componentHasEvent(comp, arg.Name) {
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
	ok, _ := componentWildcardProp(comp, name)
	return ok
}

// componentWildcardProp reports whether a wildcard prop covers a name no
// declared prop binds, and the ambiguity if two of them do.
func componentWildcardProp(comp *ir.Component, name string) (bool, error) {
	_, ok, err := wildcardProp(comp, name)
	return ok, err
}

func componentHasEvent(comp *ir.Component, name string) bool {
	for _, e := range comp.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}

// componentWildcardEvent reports whether a wildcard event covers name when no
// declared event binds, and the ambiguity if two of them do.
func componentWildcardEvent(comp *ir.Component, name string) (bool, error) {
	_, ok, err := wildcardEvent(comp, name)
	return ok, err
}

// componentAcceptsEvent reports whether a handler may be attached for name. A
// declared event binds first; failing that, an event carrying #[wildcard] with
// a matching pattern does. An ambiguity is reported where the name was
// written, and accepts here rather than reporting twice.
func componentAcceptsEvent(comp *ir.Component, name string) bool {
	if componentHasEvent(comp, name) {
		return true
	}
	ok, _ := componentWildcardEvent(comp, name)
	return ok
}

// componentDeclaresEventNamed reports whether comp declares an event of this
// exact name. Deliberately not a wildcard match: this answers "is this
// prop-position name actually an event", and a wildcard event stands for the
// names of a whole open set -- every DOM event, for html's `element` -- not for
// any name a caller might write in prop position. Accepting the wildcard there
// let `html.div(innerTxt = "x")` through, because a camelCase misspelling of a
// DOM property matches "any event name" perfectly well.
func componentDeclaresEventNamed(comp *ir.Component, name string) bool {
	return componentHasEvent(comp, name)
}

// bindWildcardName binds the name a wildcard component was resolved by to the
// prop its mark named. A component reached through a wildcard was reached by a
// name it otherwise has no way to read — `html.div` resolves `element`, and
// nothing downstream could say which tag that was. A name the call site wrote
// itself wins: that is how a name no identifier can spell is given.
func bindWildcardName(comp *ir.Component, name string, props []ir.Arg) []ir.Arg {
	if comp == nil || comp.Wildcard == "" || comp.WildcardInto == "" || name == "" {
		return props
	}
	for _, p := range props {
		if p.Name == comp.WildcardInto {
			return props
		}
	}
	return append(props, ir.Arg{
		Name:  comp.WildcardInto,
		Value: &ir.Literal{Type: TypString, Value: name},
	})
}

// hoistForLoopWindowIDs scans a for-loop body for window declarations with
// non-empty #ids and declares each as a list<Window> symbol in the current
// (enclosing) scope. After optimizer expansion, the bound value is the list
// of unrolled windows; inside the loop body, the same id remains a scalar
// Window (declared per-iteration during normal body checking).
func (c *checker) hoistForLoopWindowIDs(block *ast.StmtBlock) []*ir.Var {
	if block == nil || !block.IsDefined() {
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
		if c.isWindowNode(visualNodeTarget(n)) && n.ID != "" {
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
					c.declare(n.Pos, v)
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
	}
}

// rejectNodeInFuncBody reports a visual node written in an imperative body --
// a function, an event handler, a timer, a var handler or a lambda -- and says
// whether it did.
//
// A node is a piece of a rendered tree: it is placed once, where it is
// written, and every backend builds that tree by walking a view body. An
// imperative body is a statement stream, walked by a different emitter that
// has nowhere to put a node -- so one written there was checked, lowered and
// then dropped, and `func setup() { effect(@mount { ... }) }` compiled to an
// empty function on all three targets. The boundary is funcDepth, the same one
// checkHeadlessFor and requireLoop read from the other side: a slot's content
// and a canvas's shapes are written in view bodies and so are unaffected,
// whatever they later lower to.
func (c *checker) rejectNodeInFuncBody(pos ast.Pos, name string) bool {
	if c.funcDepth == 0 {
		return false
	}
	c.error(pos, "%s cannot be written in a function body: a node is placed in a rendered tree, and a function body renders nothing", nodeDescription(name))
	return true
}

// nodeDescription names the node a diagnostic is about, falling back to the
// generic wording when the node has no name to quote (a bare `slot`).
func nodeDescription(name string) string {
	if name == "" {
		return "a visual node"
	}
	return "visual node " + strconv.Quote(name)
}

// requireLoop reports a `break` or `continue` written where no loop encloses
// it. What counts as enclosing is loopDepth, which resets at every imperative
// body: the loop has to be one this statement can still be running inside.
//
// A view body's loop does not count either, for the reason checkHeadlessFor
// gives: the body is a template stamped once per element, not a statement
// stream, so there is no iteration for an escape to cut short.
func (c *checker) requireLoop(pos ast.Pos, kw string) {
	switch {
	case c.funcDepth == 0:
		c.error(pos, "`%s` cannot be written in a view body", kw)
	case c.loopDepth == 0:
		c.error(pos, "`%s` outside a loop", kw)
	}
}

// checkHeadlessFor checks the two loops that walk nothing: `for cond { }`,
// whose head is the already-checked bool cond, and `for { }`, which has no
// head at all and is what a nil cond means.
//
// Both are imperative-only. A view body renders a tree, and a loop there is
// how many copies of its body the tree holds: a list gives that a length and
// a count gives it a number, while a condition gives it neither. Nothing a
// mutation model could diff, and nothing a static renderer could write down --
// so it is refused here, with a position, rather than emitted as a loop that
// two of the platforms would render exactly once.
func (c *checker) checkHeadlessFor(x *ast.ForStmt, cond ir.Expr) *ir.For {
	kind := "a loop over a condition"
	if cond == nil {
		kind = "a loop with no condition"
	}
	if c.funcDepth == 0 {
		c.error(x.Pos, "%s cannot be written in a view body: a view repeats its body once per element, and there is no element here", kind)
	}
	// Only the condition form can reach this: the grammar takes `var` solely
	// in the branch that goes on to require an `=` and an expression, so a
	// headless loop carrying a variable does not parse in the first place.
	if x.Key != "" {
		c.error(x.Pos, "a loop over a condition declares no variable: drop the `var`")
	}
	if cond == nil && x.Else.IsDefined() {
		// else means the body never ran, and a loop with no condition always
		// runs its body: the block is unreachable, not empty-case handling.
		c.error(x.Pos, "`for { } else { }` has no else case: the body always runs, so drop the else")
	}
	c.pushScope()
	c.loopDepth++
	body := c.checkBlockIR(&x.Body)
	c.loopDepth--
	var elseBody []ir.Stmt
	if cond != nil && x.Else.IsDefined() {
		elseBody = c.checkBlockIR(&x.Else)
	}
	c.popScope()
	return &ir.For{AST: x, Iter: cond, ElemType: TypDyn, Body: body, Else: elseBody}
}

// checkViewMapFor reports a loop over a map written in a view body. It is the
// fourth member of the family checkHeadlessFor states: a view body's loop is
// how many copies of its body the rendered tree holds, and *in what order*. A
// map answers the first question and not the second, so there is nothing for a
// mutation model to diff against and nothing for a static renderer to write
// down -- two renders of the same map may lay the body out differently.
//
// Imposing an order instead was the alternative, and it costs a per-platform
// contingency at every backend: a key type the host cannot sort, a widget
// emitter that walks the map again elsewhere, a host map that happens to be
// insertion-ordered so the bug only appears on the other three targets.
//
// A map reaching the head with its map-ness erased is not caught here and
// cannot be: `iter<T>` is not one of the erasures, since map<K, V> is not
// assignable to it, but `dyn` is, and a dyn head says nothing about what it
// will hold.
func (c *checker) checkViewMapFor(pos ast.Pos, iter *ir.Type) {
	if c.funcDepth > 0 || iter == nil || iter.Kind != ir.TypeMap {
		return
	}
	c.error(pos, "a loop over a map cannot be written in a view body: a view repeats its body once per element, and a map yields its elements in no defined order -- iterate a list, or build one in a handler")
}

// checkViewForElse reports a `for … else` in a view body whose else cannot be
// rendered. passViewForElse asks the iterable whether it is empty rather than
// storing a flag, which puts two requirements on the head that the loop itself
// does not have:
//
//   - It must be measurable. A list and a map report a length and a sngl:seq
//     range has bounds; a pull sequence answers only by consuming the element
//     that would have been the answer.
//   - It must survive a second evaluation, being written once and read twice.
func (c *checker) checkViewForElse(pos ast.Pos, loop *ir.For, iter *ir.Type) {
	if c.funcDepth > 0 || len(loop.Else) == 0 || iter == nil {
		return
	}
	if ir.EmptyTest(loop, nil) != nil {
		if !ir.Reevaluable(loop.Iter) {
			c.error(pos, "`for … else` in a view body evaluates its iterable a second time to decide whether the else renders, so the head must be free of side effects: bind it to a var and iterate that")
		}
		return
	}
	switch {
	case ir.SeqIntrinsic(loop) != "":
		c.error(pos, "`for … else` over %s needs a literal non-zero step: which comparison says \"empty\" depends on the step's sign, and the else is decided when this compiles", ir.SeqIntrinsic(loop))
	case iter.Kind == ir.TypeDyn:
		c.error(pos, "`for … else` over dyn cannot be written in a view body: the else renders when the iterable yields nothing, and dyn says nothing about how to ask -- give the iterable a list, map or sngl:seq type")
	default:
		c.error(pos, "`for … else` over %s cannot be written in a view body: the else renders when the iterable yields nothing, and a pull sequence reports that only by consuming an element -- iterate a list, a map, or a sngl:seq range", iter)
	}
}

// errorNotCallable reports a call whose callee is neither a function nor a
// castable type. The message distinguishes a cast to a non-castable type from a
// call on an ordinary value, because the fix differs: use the type's literal
// syntax versus call the right thing.
func (c *checker) errorNotCallable(x *ast.CallExpr, callee ir.Expr, t *ir.Type) {
	id, ok := callee.(*ir.Ident)
	if !ok {
		c.error(x.Pos, "%s is not a function", t)
		return
	}
	switch sym := id.Sym.(type) {
	case *ir.StructDef:
		// The built-in generics are struct-backed but have no literal form of
		// their own, so the struct-literal hint would be wrong for them.
		if sym.Builtin.IsGeneric() {
			c.error(x.Pos, "cannot cast to %s", t)
			return
		}
		c.error(x.Pos, "cannot cast to struct %s (use a struct literal: {field = value})", t)
	case *ir.EnumDef:
		c.error(x.Pos, "cannot cast to enum %s (use one of its members)", t)
	case *ir.UnitDef:
		c.error(x.Pos, "cannot cast to unit %s (use a unit literal, e.g. 5%s)", t, t)
	default:
		c.error(x.Pos, "%s is not a function", t)
	}
}

// lookupComponentInScope resolves a bare component name through the scope chain
// rather than the flat symtab.Comps map. Both stdlib and user components are
// declared into scope, so the map adds nothing except the ability to see names
// that are not lexically visible — which is exactly the bug: platform-extension
// bodies, checked against the stdlib scope, would otherwise pick up a
// same-named user component and shadow the platform's own blueprint.
func (c *checker) lookupComponentInScope(pos ast.Pos, name string) (ir.Symbol, bool) {
	sym, ok := c.scope.Lookup(name)
	if !ok {
		// A name nobody declared is still a node name when a wildcard covers
		// it — an open element set, reached unqualified inside a `platform x
		// { ... }` body, whose scope carries the target package's wildcards.
		// Asked for here rather than in Scope.Lookup because a visual node is
		// the only bare position a wildcard stands in: as an expression the
		// same name is a misspelling.
		sym = c.scopeWildcard(pos, c.scope, name)
		if sym == nil {
			return nil, false
		}
	}
	if _, isComp := sym.(*ir.Component); !isComp {
		return nil, false
	}
	return sym, true
}

// checkSlotNodeIR checks a bare `slot`, the anonymous insertion point. A named
// one is a population, peeled off by checkSlotPopulations before the block is
// checked, so reaching here means it was written outside any call to a
// component that declares it.
func (c *checker) checkSlotNodeIR(x *ast.SlotNode) ir.Stmt {
	if x.Name != "" {
		c.error(x.Pos, "slot %q: a slot is declared in the component's parameter list, and populated only inside a call to it", x.Name)
		return nil
	}
	if len(x.Args) > 0 {
		c.error(x.Pos, "the anonymous slot takes no arguments")
	}
	if c.rejectNodeInFuncBody(x.Pos, "") {
		return nil
	}
	return &ir.SlotInst{Name: ir.DefaultSlot, Children: c.checkBlockIR(&x.Block)}
}

// enclosingSlot returns the slot of the component being checked that name
// names. Only the declaring component may insert one, and only in visual
// position — hence the funcDepth guard.
func (c *checker) enclosingSlot(name string) *ir.SlotDecl {
	if c.currentComponent == nil || name == "" || c.funcDepth > 0 {
		return nil
	}
	for _, s := range c.currentComponent.Slots {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// checkSlotInsertion checks an insertion point: its arguments, positional
// against the declaration's types, and the fallback block.
func (c *checker) checkSlotInsertion(vn *ast.VisualNode, slot *ir.SlotDecl) ir.Stmt {
	inst := &ir.SlotInst{AST: vn, Name: slot.Name}
	var args []ast.Expr
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Value == nil {
			continue
		}
		if arg.Name != "" {
			c.error(vn.Pos, "slot %q takes positional arguments: it declares types, and the names belong to whoever populates it", slot.Name)
			continue
		}
		args = append(args, arg.Value)
	}
	if len(args) != len(slot.Params) {
		c.error(vn.Pos, "slot %q takes %d argument(s), got %d", slot.Name, len(slot.Params), len(args))
	}
	for i, a := range args {
		var want *ir.Type
		if i < len(slot.Params) {
			want = slot.Params[i]
		}
		e := c.checkExprExpecting(a, want)
		if want != nil {
			if got := exprType(e); got != nil && !got.IsAssignableTo(want) {
				c.error(*a.ExprPos(), "cannot use %s as %s for argument %d of slot %q", got, want, i+1, slot.Name)
			}
		}
		inst.Args = append(inst.Args, e)
	}
	inst.Children = c.checkBlockIR(&vn.Block)
	return inst
}

func findSlot(comp *ir.Component, name string) *ir.SlotDecl {
	if comp == nil {
		return nil
	}
	for _, s := range comp.Slots {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// checkSlotPopulations pulls the `slot name(bindings) { … }` entries out of a
// call site's block, returning them by name and the block with them removed --
// what is left are the ordinary children.
func (c *checker) checkSlotPopulations(vn *ast.VisualNode, comp *ir.Component) (map[string]*ir.SlotContent, ast.StmtBlock) {
	rest := vn.Block
	if comp == nil {
		// A platform element has no declarations to populate; a `slot` written
		// in its block falls through to the statement checker, which reports it.
		return nil, rest
	}
	rest.Stmts = nil
	var content map[string]*ir.SlotContent
	for _, s := range vn.Block.Stmts {
		sn, ok := s.(*ast.SlotNode)
		if !ok || sn.Name == "" {
			rest.Stmts = append(rest.Stmts, s)
			continue
		}
		if sn.Name == ir.DefaultSlot {
			c.error(sn.Pos, "the default slot is filled by ordinary children, not by name")
			continue
		}
		decl := findSlot(comp, sn.Name)
		if decl == nil {
			c.error(sn.Pos, "component %s has no slot %q", comp.Name, sn.Name)
			continue
		}
		if _, dup := content[sn.Name]; dup {
			c.error(sn.Pos, "slot %q is already populated on component %s", sn.Name, comp.Name)
			continue
		}
		if content == nil {
			content = map[string]*ir.SlotContent{}
		}
		content[sn.Name] = c.checkSlotContent(sn, decl, comp)
	}
	c.checkRequiredSlots(vn.Pos, comp, content)
	return content, rest
}

// checkRequiredSlots reports the named slots a call site left unpopulated whose
// count gives no way to omit them.
func (c *checker) checkRequiredSlots(pos ast.Pos, comp *ir.Component, content map[string]*ir.SlotContent) {
	for _, slot := range comp.Slots {
		if slot.Name == ir.DefaultSlot || content[slot.Name] != nil {
			continue
		}
		if slot.Card == ir.SlotOne {
			c.error(pos, "component %s requires slot %q to be populated", comp.Name, slot.Name)
		}
	}
}

// checkSlotContent checks one population. The bound names are the caller's own,
// matched by position against the declaration's types, and are ordinary
// block-scoped bindings.
func (c *checker) checkSlotContent(sn *ast.SlotNode, decl *ir.SlotDecl, owner *ir.Component) *ir.SlotContent {
	if len(sn.Args) != len(decl.Params) {
		c.error(sn.Pos, "slot %q binds %d parameter(s), but declares %d", sn.Name, len(sn.Args), len(decl.Params))
	}
	sc := &ir.SlotContent{}
	c.pushScope()
	for i, a := range sn.Args {
		id, ok := a.(*ast.IdentExpr)
		if !ok {
			c.error(*a.ExprPos(), "slot %q: a population binds names, not expressions", sn.Name)
			continue
		}
		var typ *ir.Type
		if i < len(decl.Params) {
			typ = decl.Params[i]
		} else {
			// checkSlotContent reported the arity mismatch before the loop.
			typ = ir.TypDyn
		}
		p := &ir.Param{Name: id.Name, Type: typ}
		c.declare(id.Pos, p)
		sc.Params = append(sc.Params, p)
	}
	sc.Body = c.checkBlockIR(&sn.Block)
	c.popScope()
	c.checkSlotArity(sn.Pos, decl, len(sc.Body), "slot \""+sn.Name+"\"")
	c.checkTreeMembership(sn.Pos, sc.Body, slotTree(owner, decl), "in slot \""+sn.Name+"\"")
	return sc
}

// childrenTypeFor is the children contract a default slot stands for, in the
// shape the existing arity checks read.
func childrenTypeFor(slot *ir.SlotDecl) *ir.Type {
	elem := slot.Content
	if elem == nil {
		elem = &ir.Type{Kind: ir.TypeComponent}
	}
	switch slot.Card {
	case ir.SlotOne:
		return elem
	case ir.SlotOptional:
		return ir.OptionOf(elem)
	}
	return ir.ListOf(elem)
}

func (c *checker) checkSlotArity(pos ast.Pos, slot *ir.SlotDecl, n int, what string) {
	switch slot.Card {
	case ir.SlotOne:
		if n != 1 {
			c.error(pos, "%s takes exactly one node, got %d", what, n)
		}
	case ir.SlotOptional:
		if n > 1 {
			c.error(pos, "%s takes at most one node, got %d", what, n)
		}
	}
}

// slotTree is the segmented tree a slot accepts, or nil for the default tree —
// the one whose members are interchangeable.
//
// owner is the component the slot is declared on: a slot naming no tree accepts
// the owner's.
func slotTree(owner *ir.Component, slot *ir.SlotDecl) *ir.StructDef {
	if slot == nil {
		return nil
	}
	if slot.Content == nil {
		if owner == nil {
			return nil
		}
		return owner.Tree
	}
	if slot.Content.Kind != ir.TypeStruct {
		return nil
	}
	sd, ok := slot.Content.Decl.(*ir.StructDef)
	if !ok || sd.Builtin == ir.BuiltinTreeDefault || !sd.IsTree {
		return nil
	}
	return sd
}

// stmtPos is a statement's position, or nil for a synthesized node that has no
// AST to take one from.
func stmtPos(s ast.Stmt) *ast.Pos {
	if s == nil {
		return nil
	}
	return s.StmtPos()
}

// checkTreeMembership holds every supplied node to the tree the position
// accepts. Compared by declaration, so two packages each declaring a tree of
// the same name are two trees.
func (c *checker) checkTreeMembership(pos ast.Pos, content []ir.Stmt, want *ir.StructDef, where string) {
	if want == nil {
		return
	}
	for _, st := range content {
		// An `if` or a `for` is not a node in the tree, it is how the nodes
		// under it got there -- so the rule applies to its body. Reading only
		// the direct children is what rejected a canvas whose shapes come
		// from a list.
		switch s := st.(type) {
		case *ir.If:
			c.checkTreeMembership(pos, s.Body, want, where)
			c.checkTreeMembership(pos, s.Else, want, where)
			continue
		case *ir.For:
			c.checkTreeMembership(pos, s.Body, want, where)
			c.checkTreeMembership(pos, s.Else, want, where)
			continue
		}
		// A slot insertion is a position rather than a node: what lands there
		// is whatever the caller supplies, so the slot's own tree is what has
		// to match, and the population is where the content is checked.
		if si, isSlot := st.(*ir.SlotInst); isSlot {
			if slotTree(c.currentComponent, c.enclosingSlot(si.Name)) == want {
				continue
			}
		}
		ni, ok := st.(*ir.NodeInst)
		if ok && ni.Component != nil && ni.Component.Tree == want {
			continue
		}
		name := "unknown"
		at := pos
		if ok {
			name = ni.Name
			if ni.Component != nil {
				name = ni.Component.Name
			}
			// The offending child is a better place to point than the position
			// that hosts it.
			if sp := stmtPos(ni.AST); sp != nil {
				at = *sp
			}
		}
		c.error(at, "expected %s component %s, got %s", want.Name, where, name)
	}
}
