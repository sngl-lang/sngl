package checker

import (
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resolveType converts an AST TypeExpr to an IR *Type. A nil TypeExpr yields
// TypDyn; callers that must not tolerate a missing annotation should use
// resolveTypeRequired instead.
func (c *checker) resolveType(te ast.TypeExpr) *ir.Type {
	if te == nil {
		return TypDyn
	}
	switch t := te.(type) {
	case *ast.NamedType:
		return c.resolveNamedType(t)
	case *ast.FuncType:
		return c.resolveFuncType(t)
	case *ast.StructDef:
		return c.resolveAnonStruct(t)
	case *ast.EnumDef:
		return c.resolveAnonEnum(t)
	case *ast.UnitDef:
		return c.resolveAnonUnit(t)
	default:
		c.error(ast.Pos{}, "internal: unknown type expression %T", te)
		return TypDyn
	}
}

// resolveTypeRequired resolves a TypeExpr and errors at pos if it is nil.
// Use this at sites where an explicit type annotation is mandatory.
func (c *checker) resolveTypeRequired(te ast.TypeExpr, pos ast.Pos, what string) *ir.Type {
	if te == nil {
		c.error(pos, "type annotation required for %s", what)
		return TypDyn
	}
	return c.resolveType(te)
}

// constructBuiltinGeneric applies a generic built-in constructor (identified by
// its #[builtin.generic] kind) to the type arguments of t. The construction
// logic stays in the compiler; only the name→kind binding lives in scope.
func (c *checker) constructBuiltinGeneric(id ast.BuiltinKind, t *ast.NamedType) *ir.Type {
	switch id {
	case ast.BuiltinList:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "list requires a type argument, e.g. list<int>")
			return ListOf(TypDyn)
		}
		// list<shape> is the only valid use of the shape type.
		if named, ok := t.TypeArgs[0].(*ast.NamedType); ok && named.Name == "shape" {
			return ListOf(ir.TypShape)
		}
		return ListOf(c.resolveType(t.TypeArgs[0]))
	case ast.BuiltinOption:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "option requires a type argument, e.g. option<int>")
			return OptionOf(TypDyn)
		}
		return OptionOf(c.resolveType(t.TypeArgs[0]))
	case ast.BuiltinMap:
		if len(t.TypeArgs) != 2 {
			c.error(t.Pos, "map requires exactly 2 type arguments (key, value), got %d", len(t.TypeArgs))
			return TypDyn
		}
		k := c.resolveType(t.TypeArgs[0])
		v := c.resolveType(t.TypeArgs[1])
		if !isComparable(k) {
			c.error(t.Pos, "map key type %s is not comparable", k)
			return TypDyn
		}
		return ir.MapOf(k, v)
	case ast.BuiltinIter:
		if len(t.TypeArgs) != 1 {
			c.error(t.Pos, "iter requires exactly 1 type argument, got %d", len(t.TypeArgs))
			return TypDyn
		}
		return IterOf(c.resolveType(t.TypeArgs[0]))
	case ast.BuiltinRef:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "ref requires a type argument, e.g. ref<int>")
			return ir.RefOf(TypDyn)
		}
		return ir.RefOf(c.resolveType(t.TypeArgs[0]))
	}
	// Unknown kind would be a compiler bug (macro validates the id set).
	return TypDyn
}

// resolveNamedType resolves a named type reference to an IR *Type.
func (c *checker) resolveNamedType(t *ast.NamedType) *ir.Type {
	// Qualified type: pkg.Type
	if t.Package != "" {
		return c.resolveQualifiedType(t.Package, t.Name, t.TypeArgs)
	}

	// Builtin scalar primitives — resolved from the shared registry
	// (ir/builtins.go) so this site can't drift from the base scope /
	// conversion switches.
	if b, ok := ir.LookupBuiltinScalar(t.Name); ok {
		return b.Type
	}
	switch t.Name {
	case "color", "date", "time", "dateTime":
		// These are uniformly carried as TypeStructs backed by their stdlib
		// StructDefs (lib/types.sngl). Look up via the scope chain.
		if sym, ok := c.scope.Lookup(t.Name); ok {
			if typ := sym.SymType(); typ != nil {
				return typ
			}
		}
		// Stdlib not yet registered (early bootstrap) — fall back to dyn.
		return TypDyn
	}

	// Generic built-in constructors resolve through scope: a #[builtin.generic]
	// StructDef (lib/types.sngl) carries the constructor id, and the compiler
	// applies the type arguments. Because this goes through the scope chain, a
	// user declaration of the same name shadows the built-in like any other.
	if sym, ok := c.scope.Lookup(t.Name); ok {
		if sd, ok := sym.(*ir.StructDef); ok && sd.Builtin.IsGeneric() {
			return c.constructBuiltinGeneric(sd.Builtin, t)
		}
	}
	// component/shape have no `<T>` decl to carry a marker: `component` is a
	// bare kind and `shape` is only valid inside `list<shape>`.
	switch t.Name {
	case "component":
		return &ir.Type{Kind: ir.TypeComponent}
	case "shape":
		c.error(t.Pos, "shape is only valid as a children type (list<shape>)")
		return TypDyn
	}

	// Type parameter (checked before scope so generic params shadow types like T).
	if slices.Contains(c.typeParams, t.Name) {
		return &ir.Type{Kind: ir.TypeTypeParam, ParamName: t.Name}
	}

	// User-defined type from scope chain.
	if sym, ok := c.scope.Lookup(t.Name); ok {
		if c.rejectUnexported(t.Pos, sym) {
			return TypDyn
		}
		if typ := sym.SymType(); typ != nil {
			// If this is a generic struct and type args are provided, substitute.
			if typ.Kind == ir.TypeStruct {
				if sd, ok := typ.Decl.(*ir.StructDef); ok && len(sd.TypeParams) > 0 {
					return c.applyStructTypeArgs(t.Pos, typ, sd, t.TypeArgs)
				}
			}
			return typ
		}
	}

	c.error(t.Pos, "unknown type %q", t.Name)
	return TypDyn
}

// applyStructTypeArgs produces a concrete *ir.Type for a parameterized struct
// by substituting the supplied type arguments for the struct's type parameters.
// The result's Kind is TypeStruct; Elems holds the bound type arguments in
// declaration order (matching TypeParams on the StructDef).
func (c *checker) applyStructTypeArgs(pos ast.Pos, base *ir.Type, sd *ir.StructDef, args []ast.TypeExpr) *ir.Type {
	if len(args) != len(sd.TypeParams) {
		c.error(pos, "type %s requires %d type argument(s), got %d",
			sd.Name, len(sd.TypeParams), len(args))
		// Return the unparameterized base type rather than dyn so downstream
		// code can still see the struct shape.
		return base
	}
	elems := make([]*ir.Type, len(args))
	for i, a := range args {
		elems[i] = c.resolveType(a)
	}
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd, Elems: elems}
}

// resolveQualifiedType resolves a pkg.Type reference.
func (c *checker) resolveQualifiedType(pkg, name string, _ []ast.TypeExpr) *ir.Type {
	sym, ok := c.scope.Lookup(pkg)
	if !ok {
		c.error(ast.Pos{}, "unknown namespace %q", pkg)
		return TypDyn
	}
	ns, ok := sym.(*ir.Namespace)
	if !ok {
		c.error(ast.Pos{}, "%q is not a namespace", pkg)
		return TypDyn
	}
	if ns.Pkg == nil {
		// Native import — accept as dyn.
		return TypDyn
	}
	if tsym, ok := ns.Pkg.Symbols.LookupType(name); ok {
		if c.rejectUnexported(ast.Pos{}, tsym) {
			return TypDyn
		}
		return tsym.SymType()
	}
	c.error(ast.Pos{}, "unknown type %q in namespace %q", name, pkg)
	return TypDyn
}

// resolveFuncType resolves an AST function type to an IR *Type.
func (c *checker) resolveFuncType(t *ast.FuncType) *ir.Type {
	params := make([]*ir.Param, len(t.Params))
	for i, p := range t.Params {
		params[i] = &ir.Param{Name: p.Name, Type: c.resolveTypeRequired(p.Type, ast.Pos{}, "function-type parameter")}
	}
	var ret *ir.Type
	if t.Return != nil {
		ret = c.resolveType(t.Return)
	}
	return &ir.Type{
		Kind: ir.TypeFunc,
		Sig: &ir.FuncSig{
			Params: params,
			Return: ret,
		},
	}
}

// resolveAnonStruct resolves an anonymous struct type.
func (c *checker) resolveAnonStruct(s *ast.StructDef) *ir.Type {
	sd := c.buildStructDef(s)
	return sd.SymType()
}

// resolveAnonEnum resolves an anonymous enum type.
func (c *checker) resolveAnonEnum(e *ast.EnumDef) *ir.Type {
	ed := c.buildEnumDef(e)
	return ed.SymType()
}

// resolveAnonUnit resolves an anonymous unit type.
func (c *checker) resolveAnonUnit(u *ast.UnitDef) *ir.Type {
	ud := c.buildUnitDef(u)
	return ud.SymType()
}

// buildStructDef builds an IR StructDef from an AST StructDef.
func (c *checker) buildStructDef(s *ast.StructDef) *ir.StructDef {
	return &ir.StructDef{
		AST:        s,
		Name:       s.Name,
		TypeParams: s.TypeParams,
		Fields:     c.resolveStructFields(s),
	}
}

// resolveStructFields resolves a struct's field types. Split out from
// buildStructDef so the top-level pass1 path can register a field-less shell
// first (making the name visible for forward/mutually-recursive references)
// and resolve fields in a second sub-pass once every type shell exists.
func (c *checker) resolveStructFields(s *ast.StructDef) []*ir.StructField {
	// Push struct-level type params into scope so field types like T resolve.
	prevTypeParams := c.typeParams
	if len(s.TypeParams) > 0 {
		c.typeParams = append(append([]string(nil), c.typeParams...), s.TypeParams...)
	}
	defer func() { c.typeParams = prevTypeParams }()

	var fields []*ir.StructField
	seen := make(map[string]struct{})
	for _, f := range s.Fields() {
		fieldLabel := "struct field"
		if len(f.Names) > 0 {
			fieldLabel = "struct field " + strconv.Quote(f.Names[0])
		}
		typ := c.resolveTypeRequired(f.Type, f.Pos, fieldLabel)
		for _, name := range f.Names {
			if _, dup := seen[name]; dup {
				c.error(f.Pos, "duplicate struct field %q", name)
			}
			seen[name] = struct{}{}
			var def ir.Expr
			if f.Default != nil {
				// Placeholder; actual default checked later when scope is ready.
				def = &ir.Literal{Type: typ}
			}
			fields = append(fields, &ir.StructField{
				Name:    name,
				Type:    typ,
				Default: def,
			})
		}
	}
	return fields
}

// buildEnumDef builds an IR EnumDef from an AST EnumDef.
func (c *checker) buildEnumDef(e *ast.EnumDef) *ir.EnumDef {
	astMembers := e.Members()
	members := make([]*ir.EnumMember, len(astMembers))
	for i, m := range astMembers {
		var val ir.Expr
		if m.Value != nil {
			// Placeholder; actual value checked later when scope is ready.
			val = &ir.Literal{Type: TypDyn}
		}
		members[i] = &ir.EnumMember{
			Name:  m.Name,
			Value: val,
		}
	}
	return &ir.EnumDef{
		AST:     e,
		Name:    e.Name,
		Members: members,
	}
}

// buildUnitDef builds an IR UnitDef from an AST UnitDef,
// resolving suffix conversion factors.
func (c *checker) buildUnitDef(u *ast.UnitDef) *ir.UnitDef {
	suffixes := make([]*ir.UnitSuffix, len(u.Suffixes))
	// Stash factor lookups for suffixes defined earlier in this same unit so
	// expressions like `s = 1000ms` resolve against `ms` before registration.
	localFactors := map[string]float64{}
	localBaseNames := map[string]string{}
	for i, s := range u.Suffixes {
		us := &ir.UnitSuffix{
			Name:     s.Name,
			Factor:   1.0,
			BaseName: s.Name,
		}
		if s.Factor != nil {
			us.Factor = c.evalUnitFactorWithLocals(s.Factor, localFactors)
			if base, ok := unitFactorBaseName(s.Factor, localBaseNames); ok {
				us.BaseName = base
			}
		}
		localFactors[s.Name] = us.Factor
		localBaseNames[s.Name] = us.BaseName
		suffixes[i] = us
	}
	return &ir.UnitDef{
		AST:      u,
		Name:     u.Name,
		Suffixes: suffixes,
	}
}

// unitFactorBaseName walks a suffix's factor expression to find which base
// suffix it ultimately reduces to. For `rem = 16em` where em is a base,
// returns ("em", true). For `m = 60s` where s = 1000ms and ms is the base,
// recurses through localBaseNames["s"] = "ms" and returns ("ms", true).
// Returns ("", false) when no source unit literal is found.
func unitFactorBaseName(e ast.Expr, localBaseNames map[string]string) (string, bool) {
	switch n := e.(type) {
	case *ast.UnitLiteral:
		if base, ok := localBaseNames[n.Suffix]; ok {
			return base, true
		}
		return n.Suffix, true
	case *ast.BinaryExpr:
		if base, ok := unitFactorBaseName(n.Left, localBaseNames); ok {
			return base, true
		}
		return unitFactorBaseName(n.Right, localBaseNames)
	case *ast.UnaryExpr:
		return unitFactorBaseName(n.Operand, localBaseNames)
	case *ast.ParenExpr:
		return unitFactorBaseName(n.Inner, localBaseNames)
	}
	return "", false
}

func (c *checker) evalUnitFactorWithLocals(e ast.Expr, local map[string]float64) float64 {
	if ul, ok := e.(*ast.UnitLiteral); ok {
		raw := strings.TrimSuffix(ul.Raw, ul.Suffix)
		raw = strings.ReplaceAll(raw, "_", "")
		num, _ := strconv.ParseFloat(raw, 64)
		if f, ok := local[ul.Suffix]; ok {
			return num * f
		}
		if ref, ok := c.unitBySuffix[ul.Suffix]; ok {
			for _, s := range ref.Suffixes {
				if s.Name == ul.Suffix {
					return num * s.Factor
				}
			}
		}
		return num
	}
	return c.evalUnitFactor(e)
}

// evalUnitFactor evaluates a unit suffix factor expression to a float64.
// Handles simple numeric literals and unit literals with known suffixes.
func (c *checker) evalUnitFactor(e ast.Expr) float64 {
	switch x := e.(type) {
	case *ast.LiteralExpr:
		switch x.Kind {
		case ast.LiteralInt:
			v, _ := strconv.ParseFloat(x.Raw, 64)
			return v
		case ast.LiteralFloat:
			v, _ := strconv.ParseFloat(x.Raw, 64)
			return v
		}
	case *ast.UnitLiteral:
		raw := strings.TrimSuffix(x.Raw, x.Suffix)
		raw = strings.ReplaceAll(raw, "_", "")
		num, _ := strconv.ParseFloat(raw, 64)
		// Look up the referenced suffix to get its factor.
		if ref, ok := c.unitBySuffix[x.Suffix]; ok {
			for _, s := range ref.Suffixes {
				if s.Name == x.Suffix {
					return num * s.Factor
				}
			}
		}
		return num
	case *ast.BinaryExpr:
		left := c.evalUnitFactor(x.Left)
		right := c.evalUnitFactor(x.Right)
		switch x.Op {
		case ast.BinMul:
			return left * right
		case ast.BinDiv:
			if right != 0 {
				return left / right
			}
		case ast.BinAdd:
			return left + right
		case ast.BinSub:
			return left - right
		}
	}
	return 1.0
}

// ParseScheme extracts the scheme and URI from an import path.
// Returns ("go", "pkg/path") for "go://pkg/path", or ("", path) for directory imports.
func ParseScheme(path string) (scheme, uri string) {
	return imports.ParseScheme(path)
}

// NamespaceFromPath derives a namespace alias from an import path.
// Uses the last path segment: "widgets/counter" → "counter".
func NamespaceFromPath(path string) string {
	return imports.NamespaceFromPath(path)
}

// buildLambdaParams mirrors buildParams but accepts a contextual signature so
// unannotated lambda params can adopt the expected parameter types. With no
// context available, an unannotated lambda param keeps TypDyn — lambdas are
// often handed to dynamically-dispatched callers (e.g. the testrunner's
// `t.test("...", func(t, c) { ... })`) where the surrounding method is
// resolved at runtime rather than through a typed stdlib signature.
func (c *checker) buildLambdaParams(pl ast.ParamList, expected *ir.FuncSig) []*ir.Param {
	params := make([]*ir.Param, len(pl.Params))
	for i, p := range pl.Params {
		var typ *ir.Type
		switch {
		case p.Type != nil:
			typ = c.resolveType(p.Type)
		case expected != nil && i < len(expected.Params):
			typ = expected.Params[i].Type
		default:
			typ = TypDyn
		}
		var def ir.Expr
		if p.Default != nil {
			def = &ir.Literal{Type: typ}
		}
		params[i] = &ir.Param{
			Name:    p.Name,
			Type:    typ,
			Default: def,
		}
	}
	return params
}

// buildParams converts AST Params to IR Params.
func (c *checker) buildParams(pl ast.ParamList) []*ir.Param {
	params := make([]*ir.Param, len(pl.Params))
	seen := make(map[string]struct{}, len(pl.Params))
	for i, p := range pl.Params {
		if _, dup := seen[p.Name]; dup {
			c.error(p.Pos, "duplicate parameter %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		typ := c.resolveTypeRequired(p.Type, p.Pos, "parameter "+strconv.Quote(p.Name))
		var def ir.Expr
		if p.Default != nil {
			// Use a placeholder to signal "has default" for arity checks.
			// The actual default is checked later when the scope is ready.
			def = &ir.Literal{Type: typ}
		}
		params[i] = &ir.Param{
			Name:    p.Name,
			Type:    typ,
			Default: def,
		}
	}
	return params
}

// buildFunc builds an IR Func from an AST FuncDef.
func (c *checker) buildFunc(f *ast.FuncDef) *ir.Func {
	typeName, methodName, isMethod := ast.SplitMethodName(f.Name)

	// Set type params so T resolves during param/return type resolution.
	// Include both method-level TypeParams and receiver-level RecvTypeParams so
	// that e.g. `func list<T>.filter(f func(T) bool) list<T>` resolves T correctly.
	prevTypeParams := c.typeParams
	combined := f.TypeParams
	if len(f.RecvTypeParams) > 0 {
		combined = append(append([]string(nil), f.RecvTypeParams...), f.TypeParams...)
	}
	c.typeParams = combined
	// nil ReturnType means void (block body) or pending-inference (expression body);
	// leave Return nil here so checkFuncBody can infer from a `=>` body without
	// conflating it with an explicit `dyn` return annotation.
	var ret *ir.Type
	if f.ReturnType != nil {
		ret = c.resolveType(f.ReturnType)
	}
	fn := &ir.Func{
		AST:            f,
		Name:           f.Name,
		TypeParams:     f.TypeParams,
		RecvTypeParams: f.RecvTypeParams,
		Params:         c.buildParams(f.Params),
		Return:         ret,
		IsTest:         f.IsTest(),
	}
	c.typeParams = prevTypeParams
	if isMethod {
		fn.Receiver = typeName
		fn.Name = methodName
		// The receiver convention: a method's first param named `this` is the
		// implicit receiver — whether prepended synthetically (bare component
		// funcs) or written explicitly (`func T.m(this T)`). Mark it
		// structurally here, the one place that owns the convention, so
		// codegen/interp/lowering identify the receiver via Param.Receiver
		// rather than re-matching the name (which a non-method param could
		// coincidentally share).
		if len(fn.Params) > 0 && fn.Params[0].Name == ir.ReceiverParam {
			fn.Params[0].Receiver = true
		}
	}
	return fn
}

// isComparable reports whether values of t can be used as map keys.
// Primitives and structs of comparable fields qualify; lists, maps,
// and functions do not.
func isComparable(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString,
		ir.TypeDuration, ir.TypeColor:
		return true
	case ir.TypeStruct:
		// Conservative: accept any named struct as comparable. Refine
		// in a follow-up if we want to actually check field types. This
		// also covers the string-representable structs (color/date/time/
		// dateTime).
		return t.Decl != nil
	case ir.TypeEnum, ir.TypeUnit:
		return true
	}
	return false
}
