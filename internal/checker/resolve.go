package checker

import (
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resolveType converts an AST TypeExpr to an IR *Type.
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
		return TypDyn
	}
}

// resolveNamedType resolves a named type reference to an IR *Type.
func (c *checker) resolveNamedType(t *ast.NamedType) *ir.Type {
	// Qualified type: pkg.Type
	if t.Package != "" {
		return c.resolveQualifiedType(t.Package, t.Name, t.TypeArg)
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
		elem := c.resolveType(t.TypeArg)
		return ListOf(elem)
	case "option":
		inner := c.resolveType(t.TypeArg)
		return OptionOf(inner)
	case "component":
		return &ir.Type{Kind: ir.TypeComponent}
	}

	// User-defined type from symbol table.
	if sym, ok := c.symtab.LookupType(t.Name); ok {
		return sym.SymType()
	}

	// Type parameter.
	if slices.Contains(c.typeParams, t.Name) {
		return &ir.Type{Kind: ir.TypeTypeParam, ParamName: t.Name}
	}

	c.error(t.Pos, "unknown type %q", t.Name)
	return TypDyn
}

// resolveQualifiedType resolves a pkg.Type reference.
func (c *checker) resolveQualifiedType(pkg, name string, _ ast.TypeExpr) *ir.Type {
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
		return tsym.SymType()
	}
	c.error(ast.Pos{}, "unknown type %q in namespace %q", name, pkg)
	return TypDyn
}

// resolveFuncType resolves an AST function type to an IR *Type.
func (c *checker) resolveFuncType(t *ast.FuncType) *ir.Type {
	params := make([]*ir.Param, len(t.Params))
	for i, p := range t.Params {
		params[i] = &ir.Param{Type: c.resolveType(p)}
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
	fields := make([]*ir.StructField, len(s.Fields))
	for i, f := range s.Fields {
		fields[i] = &ir.StructField{
			Name:    f.Name,
			Type:    c.resolveType(f.Type),
			Default: f.Default,
		}
	}
	return &ir.StructDef{
		AST:    s,
		Name:   s.Name,
		Fields: fields,
		Pos:    s.Pos,
	}
}

// buildEnumDef builds an IR EnumDef from an AST EnumDef.
func (c *checker) buildEnumDef(e *ast.EnumDef) *ir.EnumDef {
	members := make([]*ir.EnumMember, len(e.Members))
	for i, m := range e.Members {
		members[i] = &ir.EnumMember{
			Name:  m.Name,
			Value: m.Value,
		}
	}
	return &ir.EnumDef{
		AST:     e,
		Name:    e.Name,
		Members: members,
		Pos:     e.Pos,
	}
}

// buildUnitDef builds an IR UnitDef from an AST UnitDef,
// resolving suffix conversion factors.
func (c *checker) buildUnitDef(u *ast.UnitDef) *ir.UnitDef {
	suffixes := make([]*ir.UnitSuffix, len(u.Suffixes))
	for i, s := range u.Suffixes {
		us := &ir.UnitSuffix{
			Name:   s.Name,
			Factor: 1.0,
			IsBase: s.Factor == nil,
		}
		if s.Factor != nil {
			us.Factor = c.evalUnitFactor(s.Factor)
		}
		suffixes[i] = us
	}
	return &ir.UnitDef{
		AST:      u,
		Name:     u.Name,
		Suffixes: suffixes,
		Pos:      u.Pos,
	}
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
		num, _ := strconv.ParseFloat(x.Raw, 64)
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

// parseScheme extracts the scheme and URI from an import path.
// Returns ("go", "pkg/path") for "go://pkg/path", or ("", path) for directory imports.
func parseScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}

// namespaceFromPath derives a namespace alias from an import path.
// Uses the last path segment: "widgets/counter" → "counter".
func namespaceFromPath(path string) string {
	_, uri := parseScheme(path)
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// buildParams converts AST Params to IR Params.
func (c *checker) buildParams(pl ast.ParamList) []*ir.Param {
	params := make([]*ir.Param, len(pl.Params))
	for i, p := range pl.Params {
		params[i] = &ir.Param{
			Name:       p.Name,
			Type:       c.resolveType(p.Type),
			HasDefault: p.Default != nil,
			Pos:        p.Pos,
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
	fn := &ir.Func{
		AST:        f,
		Name:       f.Name,
		TypeParams: f.TypeParams,
		Params:     c.buildParams(f.Params),
		Return:     c.resolveType(f.ReturnType),
		Body:       f.Body,
		IsTest:     f.IsTest(),
		Pos:        f.Pos,
	}
	c.typeParams = prevTypeParams
	if f.Block.IsDefined() {
		fn.ASTBlock = &f.Block
	}
	if isMethod {
		fn.Receiver = typeName
		fn.Name = methodName
	}
	return fn
}
