package checker

import (
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
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

// resolveNamedType resolves a named type reference to an IR *Type.
func (c *checker) resolveNamedType(t *ast.NamedType) *ir.Type {
	// Qualified type: pkg.Type
	if t.Package != "" {
		return c.resolveQualifiedType(t.Package, t.Name, t.TypeArgs)
	}

	// Builtin primitives.
	switch t.Name {
	case "bool":
		return TypBool
	case "int":
		return TypInt
	case "float":
		return TypFloat
	case "string":
		return TypString
	case "dyn":
		return TypDyn
	case "color":
		return TypColor
	case "date":
		return TypDate
	case "time":
		return TypTime
	case "dateTime":
		return TypDateTime
	case "duration":
		return TypDuration
	case "url":
		return TypURL
	case "email":
		return TypEmail
	case "uuid":
		return TypUUID
	case "regex":
		return TypRegex
	case "base64":
		return TypBase64
	case "ipv4":
		return TypIPV4
	case "ipv6":
		return TypIPV6
	case "hostname":
		return TypHostname
	case "decimal":
		return TypDecimal
	case "null":
		return TypNull
	}

	// Generic builtins with type argument.
	switch t.Name {
	case "list":
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "list requires a type argument, e.g. list<int>")
			return ListOf(TypDyn)
		}
		return ListOf(c.resolveType(t.TypeArgs[0]))
	case "option":
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "option requires a type argument, e.g. option<int>")
			return OptionOf(TypDyn)
		}
		return OptionOf(c.resolveType(t.TypeArgs[0]))
	case "ref":
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "ref requires a type argument, e.g. ref<int>")
			return ir.RefOf(TypDyn)
		}
		return ir.RefOf(c.resolveType(t.TypeArgs[0]))
	case "component":
		return &ir.Type{Kind: ir.TypeComponent}
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
			return typ
		}
	}

	c.error(t.Pos, "unknown type %q", t.Name)
	return TypDyn
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
		params[i] = &ir.Param{Type: c.resolveTypeRequired(p, ast.Pos{}, "function-type parameter")}
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
	var fields []*ir.StructField
	for _, f := range s.Fields {
		fieldLabel := "struct field"
		if len(f.Names) > 0 {
			fieldLabel = "struct field " + strconv.Quote(f.Names[0])
		}
		typ := c.resolveTypeRequired(f.Type, f.Pos, fieldLabel)
		for _, name := range f.Names {
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
	return &ir.StructDef{
		AST:    s,
		Name:   s.Name,
		Fields: fields,
	}
}

// buildEnumDef builds an IR EnumDef from an AST EnumDef.
func (c *checker) buildEnumDef(e *ast.EnumDef) *ir.EnumDef {
	members := make([]*ir.EnumMember, len(e.Members))
	for i, m := range e.Members {
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
	for i, s := range u.Suffixes {
		us := &ir.UnitSuffix{
			Name:   s.Name,
			Factor: 1.0,
			IsBase: s.Factor == nil,
		}
		if s.Factor != nil {
			us.Factor = c.evalUnitFactorWithLocals(s.Factor, localFactors)
		}
		localFactors[s.Name] = us.Factor
		suffixes[i] = us
	}
	return &ir.UnitDef{
		AST:      u,
		Name:     u.Name,
		Suffixes: suffixes,
	}
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
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}

// NamespaceFromPath derives a namespace alias from an import path.
// Uses the last path segment: "widgets/counter" → "counter".
func NamespaceFromPath(path string) string {
	_, uri := ParseScheme(path)
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
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
	for i, p := range pl.Params {
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
	prevTypeParams := c.typeParams
	c.typeParams = f.TypeParams
	// nil ReturnType means void (block body) or pending-inference (expression body);
	// leave Return nil here so checkFuncBody can infer from a `=>` body without
	// conflating it with an explicit `dyn` return annotation.
	var ret *ir.Type
	if f.ReturnType != nil {
		ret = c.resolveType(f.ReturnType)
	}
	fn := &ir.Func{
		AST:        f,
		Name:       f.Name,
		TypeParams: f.TypeParams,
		Params:     c.buildParams(f.Params),
		Return:     ret,
		IsTest:     f.IsTest(),
	}
	c.typeParams = prevTypeParams
	if isMethod {
		fn.Receiver = typeName
		fn.Name = methodName
	}
	return fn
}
