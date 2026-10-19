package checker

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/imports"
	"duckfam.us/sngl/ir"
)

// resolveType converts an AST TypeExpr to an IR *Type. A nil TypeExpr yields
// TypInvalid; callers that must not tolerate a missing annotation should use
// resolveTypeRequired instead.
func (c *checker) resolveType(te ast.TypeExpr) *ir.Type {
	if te == nil {
		return TypInvalid
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
	case *ast.ComponentType:
		// The grammar admits a component type wherever a type is written; what
		// it *means* is a slot, and only a component's parameter list has one.
		// registerComponentDecl reads a slot's type itself and never gets here.
		c.error(t.Pos, "a component type declares a slot, so only a parameter of a component may have one")
		return TypInvalid
	case *ast.VariadicType:
		c.error(t.Pos, "`...` bounds how many nodes a slot takes, so only a slot's type may carry one")
		return TypInvalid
	default:
		c.error(ast.Pos{}, "internal: unknown type expression %T", te)
		return TypInvalid
	}
}

// resolveTypeRequired resolves a TypeExpr and errors at pos if it is nil.
// Use this at sites where an explicit type annotation is mandatory.
func (c *checker) resolveTypeRequired(te ast.TypeExpr, pos ast.Pos, what string) *ir.Type {
	if te == nil {
		c.error(pos, "type annotation required for %s", what)
		return TypInvalid
	}
	return c.resolveType(te)
}

// namesType reports whether t is the type called name, ignoring any type
// arguments: the receiver of a `list[T]` method is a `list[T]`, and of a
// `Box[T]` method a `Box[T]`, whichever element types they carry.
func namesType(t *ir.Type, name string) bool {
	if t == nil || name == "" {
		return false
	}
	s := t.String()
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	return s == name
}

// constructBuiltinGeneric applies a generic built-in constructor (identified by
// its #[builtin] kind) to the type arguments of t. The construction logic stays
// in the compiler; only the name→kind binding lives in scope.
func (c *checker) constructBuiltinGeneric(id ir.BuiltinKind, sd *ir.StructDef, t *ast.NamedType) *ir.Type {
	switch id {
	case ir.BuiltinList:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "list requires a type argument, e.g. list[int]")
			return c.listOf(TypInvalid)
		}
		return c.listOf(c.resolveType(t.TypeArgs[0]))
	case ir.BuiltinOption:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "option requires a type argument, e.g. option[int]")
			return OptionOf(TypInvalid)
		}
		return OptionOf(c.resolveType(t.TypeArgs[0]))
	case ir.BuiltinMap:
		if len(t.TypeArgs) != 2 {
			c.error(t.Pos, "map requires exactly 2 type arguments (key, value), got %d", len(t.TypeArgs))
			return TypInvalid
		}
		k := c.resolveType(t.TypeArgs[0])
		v := c.resolveType(t.TypeArgs[1])
		if !isComparable(k) {
			c.error(t.Pos, "map key type %s is not comparable", k)
			return TypInvalid
		}
		return c.mapOf(k, v)
	case ir.BuiltinIter:
		if len(t.TypeArgs) != 1 {
			c.error(t.Pos, "iter requires exactly 1 type argument, got %d", len(t.TypeArgs))
			return TypInvalid
		}
		return IterOf(c.resolveType(t.TypeArgs[0]))
	case ir.BuiltinRef:
		if len(t.TypeArgs) == 0 {
			c.error(t.Pos, "ref requires a type argument, e.g. ref[int]")
			return ir.RefOf(TypInvalid)
		}
		return ir.RefOf(c.resolveType(t.TypeArgs[0]))
	case ir.BuiltinReadonly:
		// The checker's own: what it gives a loop variable bound by copy. The
		// argument is what the program meant, so it stands in after the error.
		c.error(t.Pos, "readonly[T] is the type of a loop variable bound by copy, and a program may not write it")
		if len(t.TypeArgs) == 1 {
			return c.resolveType(t.TypeArgs[0])
		}
		return TypInvalid
	}
	// The macro validates the id set, so an unhandled kind is a compiler bug.
	panic(fmt.Sprintf("sngl: builtin generic kind %v has no constructor", id))
}

func (c *checker) resolveNamedType(t *ast.NamedType) *ir.Type {
	// Qualified type: pkg.Type
	if t.Package != "" {
		return c.resolveQualifiedType(t)
	}

	// A scalar is its declaration in sngl:builtin, or its name in the
	// universe before that package is in scope; either stands for the type
	// its kind does. A program's own declaration of the name is found first
	// and shadows it, as any declaration does.
	if sym, ok := c.scope.Lookup(t.Name); ok {
		if typ, _, ok := scalarOf(sym); ok {
			if typ == ir.TypAny {
				if c.anyWritten != nil {
					*c.anyWritten = append(*c.anyWritten, t.Pos)
					return TypInvalid
				}
				c.error(t.Pos, anyNotAType)
				return TypInvalid
			}
			return typ
		}
	}

	// Generic built-in constructors resolve through scope: a #[builtin]-marked
	// StructDef (lib/types.sngl) carries the constructor id, and the compiler
	// applies the type arguments. Because this goes through the scope chain, a
	// user declaration of the same name shadows the built-in like any other.
	if sym, ok := c.scope.Lookup(t.Name); ok {
		if sd, ok := sym.(*ir.StructDef); ok && sd.Builtin.IsGeneric() {
			return c.constructBuiltinGeneric(sd.Builtin, sd, t)
		}
	}
	// `component` has no `<T>` decl to carry a marker; it is a bare kind.
	if t.Name == "component" {
		return &ir.Type{Kind: ir.TypeComponent}
	}

	if sym, ok := c.scope.Lookup(t.Name); ok {
		// The export rule is the one between packages: a bare name declared
		// in this one -- a library's own `_page` -- is its own to name.
		if !c.inLibSource() && !c.resolvedInPackage(t.Name) && c.rejectUnexported(t.Pos, sym) {
			return TypInvalid
		}
		if typ := sym.SymType(); typ != nil {
			if typ.Kind == ir.TypeStruct {
				if sd, ok := typ.Decl.(*ir.StructDef); ok && len(sd.TypeParams) > 0 {
					return c.applyStructTypeArgs(t.Pos, typ, sd, t.TypeArgs)
				}
			}
			if typ.Kind == ir.TypeComponent {
				if comp, ok := typ.Decl.(*ir.Component); ok && len(comp.TypeParams) > 0 {
					return c.applyComponentTypeArgs(t.Pos, typ, comp, t.TypeArgs)
				}
			}
			if typ.Kind == ir.TypeEnum {
				if ed, ok := typ.Decl.(*ir.EnumDef); ok && len(ed.TypeParams) > 0 {
					return c.applyEnumTypeArgs(t.Pos, typ, ed, t.TypeArgs)
				}
			}
			if len(t.TypeArgs) > 0 && c.refuseTypeArgs(t, sym) {
				return TypInvalid
			}
			return c.dataRecord(typ)
		}
	}

	c.error(t.Pos, "unknown type %q%s", t.Name, c.stdlibHintFor(t.Name, hintType))
	return TypInvalid
}

// applyComponentTypeArgs is a component type carrying the type arguments it
// was written with, `page[P]`: the instances whose call sites bound the
// declaration's parameters that way. An argument list stopping short fills
// from each parameter's default, as a struct type's does, so a bare `page` is
// `page[struct {}]` -- and one with no default has to be written.
func (c *checker) applyComponentTypeArgs(pos ast.Pos, base *ir.Type, comp *ir.Component, args []ast.TypeExpr) *ir.Type {
	if len(args) > len(comp.TypeParams) {
		c.error(pos, "%s takes %d type argument(s), got %d", comp.Name, len(comp.TypeParams), len(args))
		return base
	}
	elems := make([]*ir.Type, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		switch {
		case i < len(args):
			elems[i] = c.resolveType(args[i])
		case tp.Default != nil:
			elems[i] = tp.Default
		default:
			c.error(pos, "component type %s requires a type argument for %s, which has no default", comp.Name, tp.Name)
			return base
		}
	}
	c.checkWrittenArgs(comp.TypeParams, elems, args)
	cp := *base
	cp.Elems = elems
	return &cp
}

// applyStructTypeArgs produces a concrete *ir.Type for a parameterized struct
// by substituting the supplied type arguments for the struct's type parameters.
// The result's Kind is TypeStruct; Elems holds the bound type arguments in
// declaration order (matching TypeParams on the StructDef).
func (c *checker) applyStructTypeArgs(pos ast.Pos, base *ir.Type, sd *ir.StructDef, args []ast.TypeExpr) *ir.Type {
	elems := make([]*ir.Type, 0, len(sd.TypeParams))
	for i := range sd.TypeParams {
		switch {
		case i < len(args):
			elems = append(elems, c.resolveType(args[i]))
		default:
			def := c.typeParamDefault(sd, i)
			if def == nil {
				c.error(pos, "type %s requires a type argument for %s, which has no default",
					sd.Name, sd.TypeParams[i].Name)
				// The unparameterized base rather than invalid, so downstream code
				// can still see the struct shape.
				return base
			}
			elems = append(elems, def)
		}
	}
	if len(args) > len(sd.TypeParams) {
		c.error(pos, "type %s takes %d type argument(s), got %d",
			sd.Name, len(sd.TypeParams), len(args))
		return base
	}
	params := make([]ir.TypeParam, len(sd.TypeParams))
	for i, tp := range sd.TypeParams {
		params[i] = tp
		params[i].Constraint = c.typeParamConstraint(sd, i)
	}
	c.checkWrittenArgs(params, elems, args)
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd, Elems: elems}
}

// checkWrittenArgs holds each type argument a type was written with to its
// parameter's constraint, measured with every parameter bound -- the rest by
// their defaults -- and reports it where it is written. A default is checked
// where it is declared.
func (c *checker) checkWrittenArgs(params []ir.TypeParam, elems []*ir.Type, args []ast.TypeExpr) {
	bindings := make(map[string]*ir.Type, len(params))
	for i, tp := range params {
		bindings[tp.Name] = elems[i]
	}
	for i, a := range args {
		if i < len(params) {
			c.checkConstraint(*a.ExprPos(), params[i], elems[i], bindings)
		}
	}
}

func (c *checker) resolveQualifiedType(t *ast.NamedType) *ir.Type {
	pkg, name, args := t.Package, t.Name, t.TypeArgs
	sym, ok := c.scope.Lookup(pkg)
	if !ok {
		c.error(ast.Pos{}, "unknown namespace %q", pkg)
		return TypInvalid
	}
	ns, ok := sym.(*ir.Namespace)
	if !ok {
		c.error(ast.Pos{}, "%q is not a namespace", pkg)
		return TypInvalid
	}
	if ns.Pkg == nil {
		// An import that resolved to no package -- a check with no resolver
		// -- has no members, and the import is where that is the problem.
		return TypInvalid
	}
	if tsym, ok := ns.Pkg.Symbols.LookupMemberType(name); ok {
		if c.rejectUnexported(t.Pos, tsym) {
			return TypInvalid
		}
		typ := tsym.SymType()
		if typ != nil && typ.Kind == ir.TypeStruct {
			if sd, ok := typ.Decl.(*ir.StructDef); ok {
				// A #[builtin] generic is the built-in however it was reached.
				// Unqualified resolution dispatches on the kind a few lines up;
				// without the same dispatch here, `go.chan[T]` would come back an
				// ordinary generic struct that shared a declaration with the
				// built-in but not a type, and fail to unify with itself. list,
				// map and option are ambient, and importing sngl:builtin
				// explicitly is an error, so a generic built-in reached
				// qualified is one a target package declares.
				if sd.Builtin.IsGeneric() {
					return c.constructBuiltinGeneric(sd.Builtin, sd, t)
				}
				if len(sd.TypeParams) > 0 {
					return c.applyStructTypeArgs(t.Pos, typ, sd, args)
				}
			}
		}
		if typ != nil && typ.Kind == ir.TypeEnum {
			if ed, ok := typ.Decl.(*ir.EnumDef); ok && len(ed.TypeParams) > 0 {
				return c.applyEnumTypeArgs(t.Pos, typ, ed, args)
			}
		}
		if len(args) > 0 {
			c.error(ast.Pos{}, "type %q in namespace %q takes no type arguments", name, pkg)
		}
		return typ
	}
	// A component names a type two ways: a family, in a return position or a
	// slot saying which family, and any other component as the type of a
	// handle to one of its instances -- `nav.page` for a page's value, as the
	// bare `page` names it inside its own package.
	if sym, ok := ns.Pkg.Symbols.LookupMember(name); ok {
		if f, ok := sym.(*ir.Component); ok {
			if c.rejectUnexported(t.Pos, sym) {
				return TypInvalid
			}
			if len(f.TypeParams) > 0 {
				return c.applyComponentTypeArgs(t.Pos, f.SymType(), f, args)
			}
			return c.dataRecord(f.SymType())
		}
	}
	c.error(ast.Pos{}, "unknown type %q in namespace %q", name, pkg)
	return TypInvalid
}

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

// resolveAnonStruct types a written `struct { … }` through the same interning
// a literal uses, so the two positions name one declaration. A generic one is
// left alone: a type parameter has no canonical spelling until it is bound.
func (c *checker) resolveAnonStruct(s *ast.StructDef) *ir.Type {
	sd := c.buildStructDef(s)
	if len(sd.TypeParams) > 0 {
		return sd.SymType()
	}
	return c.anonStructType(sd.Fields)
}

func (c *checker) resolveAnonEnum(e *ast.EnumDef) *ir.Type {
	ed := c.buildEnumDef(e)
	if len(e.TypeParams) > 0 {
		c.error(e.Pos, "an enum written in place takes no type parameters: declare it with a name")
		ed.TypeParams = nil
	}
	c.resolveEnumBody(ed)
	return ed.SymType()
}

func (c *checker) resolveAnonUnit(u *ast.UnitDef) *ir.Type {
	ud := c.buildUnitDef(u)
	return ud.SymType()
}

func (c *checker) buildStructDef(s *ast.StructDef) *ir.StructDef {
	defer pushTypeParams(c, s.TypeParams)()
	return &ir.StructDef{
		AST:        s,
		Name:       s.Name,
		Pkg:        c.declPkg(),
		TypeParams: c.resolveTypeParams(s.TypeParams),
		Fields:     c.resolveStructFields(s),
	}
}

// typeParamDefault is the default for a struct's i-th type parameter, or nil.
// A struct shell exists before its defaults are resolved, and a use site can
// come first, so the written form is resolved on demand when it does.
func (c *checker) typeParamDefault(sd *ir.StructDef, i int) *ir.Type {
	if i < len(sd.TypeParams) && sd.TypeParams[i].Default != nil {
		return sd.TypeParams[i].Default
	}
	if sd.AST == nil || i >= len(sd.AST.TypeParams) || sd.AST.TypeParams[i].Default == nil {
		return nil
	}
	return c.resolveType(sd.AST.TypeParams[i].Default)
}

// typeParamConstraint is the constraint on a struct's i-th type parameter.
// A struct shell exists before its constraints are resolved, as before its
// defaults, so a use site that comes first resolves the written one quietly:
// the declaration reports it.
func (c *checker) typeParamConstraint(sd *ir.StructDef, i int) *ir.Type {
	if i < len(sd.TypeParams) && sd.TypeParams[i].Constraint != nil {
		return sd.TypeParams[i].Constraint
	}
	if sd.AST == nil || i >= len(sd.AST.TypeParams) || sd.AST.TypeParams[i].Constraint == nil {
		return nil
	}
	var t *ir.Type
	c.quietly(func() {
		defer pushTypeParams(c, sd.AST.TypeParams)()
		t = c.resolveConstraint(sd.AST.TypeParams[i].Constraint)
	})
	return t
}

// inheritReceiverConstraints gives a method's receiver parameters the
// constraints of the declaration they restate, `func Holder[T].label()` or
// `func stack[M].current()`: a receiver's list writes names only, and in the
// body a T is what the declaration says it is.
// It runs before the signature is resolved, which may hand a T on to the
// declaration's own type, `_page[M]`.
func (c *checker) inheritReceiverConstraints(recv *ir.Type, written []ast.TypeParam) {
	if recv == nil || len(written) == 0 {
		return
	}
	var constraint func(i int) *ir.Type
	var declared []ir.TypeParam
	switch d := recv.Decl.(type) {
	case *ir.StructDef:
		declared = d.TypeParams
		constraint = func(i int) *ir.Type { return c.typeParamConstraint(d, i) }
	case *ir.Component:
		declared = d.TypeParams
		constraint = func(i int) *ir.Type { return d.TypeParams[i].Constraint }
	default:
		return
	}
	if len(declared) != len(written) {
		return
	}
	// A constraint naming the declaration's parameters names the receiver's
	// here, which may be spelled differently: `func Pair[X, Y].left()`.
	renamed := make(map[string]*ir.Type, len(written))
	for i, p := range written {
		renamed[declared[i].Name] = c.typeParamSym(p.Name, p.Pos).SymType()
	}
	for i, p := range written {
		if con := constraint(i).Substitute(renamed); con != nil {
			if sym := c.typeParamSym(p.Name, p.Pos); sym.Constraint == nil {
				sym.Constraint = con
			}
		}
	}
}

// typeParamShells records the names before any other declaration exists. A
// default may name a type declared further down the file, so it is left to
// resolveStructBody the way a field is.
func typeParamShells(ps []ast.TypeParam) []ir.TypeParam {
	if len(ps) == 0 {
		return nil
	}
	out := make([]ir.TypeParam, len(ps))
	for i, p := range ps {
		out[i] = ir.TypeParam{Pos: p.Pos, Name: p.Name}
	}
	return out
}

// resolveTypeParams resolves each parameter's constraint and its written
// default, if it has one. A receiver's list restates names and carries no
// constraint; every other list carries one per parameter, which the grammar
// requires.
func (c *checker) resolveTypeParams(ps []ast.TypeParam) []ir.TypeParam {
	if len(ps) == 0 {
		return nil
	}
	out := make([]ir.TypeParam, len(ps))
	for i, p := range ps {
		out[i] = ir.TypeParam{Pos: p.Pos, Name: p.Name}
		if p.Constraint != nil {
			out[i].Constraint = c.resolveConstraint(p.Constraint)
			if sym := c.typeParamSym(p.Name, p.Pos); sym.Constraint == nil {
				sym.Constraint = out[i].Constraint
			}
		}
		if p.Default != nil {
			out[i].Default = c.resolveType(p.Default)
		}
	}
	// A default is a binding like any other, and is held to the constraint
	// written beside it. What that constraint names of the list is bound from
	// the default's own fields, since nothing else is bound where a
	// declaration is checked.
	for i, p := range ps {
		if p.Default != nil {
			bindings := map[string]*ir.Type{p.Name: out[i].Default}
			inferFromConstraints(out, bindings)
			c.checkConstraint(*p.Default.ExprPos(), out[i], out[i].Default, bindings)
		}
	}
	return out
}

// resolveStructFields resolves a struct's field types. Split out from
// buildStructDef so the top-level pass1 path can register a field-less shell
// first (making the name visible for forward/mutually-recursive references)
// and resolve fields in a second sub-pass once every type shell exists.
func (c *checker) resolveStructFields(s *ast.StructDef) []*ir.StructField {
	var fields []*ir.StructField
	seen := make(map[string]struct{})
	for _, f := range s.Fields() {
		// A field's marks apply to the declaration, which may bind several
		// names; the mark refuses that itself, so applying to the first is
		// enough to reach the one it allows.
		var first *ir.StructField
		fieldLabel := "struct field"
		if len(f.Names) > 0 {
			fieldLabel = "struct field " + strconv.Quote(f.Names[0])
		}
		var typ *ir.Type
		anyAt := c.holdAny(f, func() { typ = c.resolveTypeRequired(f.Type, f.Pos, fieldLabel) })
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
			fld := &ir.StructField{Name: name, Type: typ, Default: def, DefaultWritten: f.Default != nil}
			if def != nil {
				if c.defaultPlaceholders == nil {
					c.defaultPlaceholders = map[ir.Expr]*ir.StructField{}
				}
				c.defaultPlaceholders[def] = fld
			}
			if first == nil {
				first = fld
			}
			fields = append(fields, fld)
		}
		c.applyMarks(f, first)
		if first != nil {
			c.refuseAnyUnlessUnusable(anyAt, &first.Foreign)
		}
	}
	return fields
}

func (c *checker) buildEnumDef(e *ast.EnumDef) *ir.EnumDef {
	astMembers := e.Members()
	members := make([]*ir.EnumMember, len(astMembers))
	for i, m := range astMembers {
		var val ir.Expr
		if m.Value != nil {
			// Placeholder; actual value checked later when scope is ready.
			val = &ir.Literal{Type: TypInvalid}
		}
		members[i] = &ir.EnumMember{
			Name:  m.Name,
			Value: val,
		}
		c.applyMarks(m, members[i])
	}
	return &ir.EnumDef{
		AST:        e,
		Name:       e.Name,
		Pkg:        c.declPkg(),
		TypeParams: typeParamShells(e.TypeParams),
		Members:    members,
	}
}

// resolveEnumBody resolves what each member carries, which may name any
// type the package declares -- so a top-level enum's body waits for every
// type's shell, as a struct's fields do -- and the enum's own type
// parameters.
func (c *checker) resolveEnumBody(ed *ir.EnumDef) {
	defer pushTypeParams(c, ed.AST.TypeParams)()
	ed.TypeParams = c.resolveTypeParams(ed.AST.TypeParams)
	for i, m := range ed.AST.Members() {
		if m.Payload == nil {
			continue
		}
		ed.Members[i].Payload = c.resolveType(m.Payload)
		if m.Value != nil {
			c.error(m.Pos, "member %s carries a payload, so it takes no value", m.Name)
			ed.Members[i].Value = nil
		}
	}
}

// applyEnumTypeArgs is a generic enum's type at the arguments it was written
// with, `Result[int]`: Elems holds them in declaration order, as a struct
// type's do.
func (c *checker) applyEnumTypeArgs(pos ast.Pos, base *ir.Type, ed *ir.EnumDef, args []ast.TypeExpr) *ir.Type {
	if len(args) != len(ed.TypeParams) {
		c.error(pos, "type %s takes %d type argument(s), got %d", ed.Name, len(ed.TypeParams), len(args))
		return base
	}
	elems := make([]*ir.Type, len(args))
	for i, a := range args {
		elems[i] = c.resolveType(a)
	}
	c.checkWrittenArgs(ed.TypeParams, elems, args)
	return &ir.Type{Kind: ir.TypeEnum, Decl: ed, Elems: elems}
}

// buildUnitDef builds an IR UnitDef from an AST UnitDef,
// resolving suffix conversion factors.
func (c *checker) buildUnitDef(u *ast.UnitDef) *ir.UnitDef {
	astSuffixes := u.Suffixes()
	suffixes := make([]*ir.UnitSuffix, len(astSuffixes))
	// Stash factor lookups for suffixes defined earlier in this same unit so
	// expressions like `s = 1000ms` resolve against `ms` before registration.
	localFactors := map[string]float64{}
	localBaseNames := map[string]string{}
	for i, s := range astSuffixes {
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
	ud := &ir.UnitDef{
		AST:      u,
		Name:     u.Name,
		Pkg:      c.declPkg(),
		Suffixes: suffixes,
	}
	// The member table, built here so it exists wherever a UnitDef does --
	// registration is the only place that knows the suffixes have been
	// resolved, and a lookup that had to derive it would be a second answer.
	ud.Fields = ir.UnitFields(ud)
	return ud
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
		// Only this declaration's own suffixes, which the caller supplies as
		// `local`: a factor written in one unit cannot be denominated in
		// another's.
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
// Returns ("go", "pkg/path") for "go:pkg/path", or ("", path) for directory imports.
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
// context available, an unannotated lambda param keeps TypInvalid — lambdas are
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
			c.error(p.Pos, "lambda parameter %q needs a type: nothing it is handed to says what it takes", p.Name)
			typ = TypInvalid
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

func (c *checker) buildParams(pl ast.ParamList) []*ir.Param {
	params := make([]*ir.Param, len(pl.Params))
	seen := make(map[string]struct{}, len(pl.Params))
	for i, p := range pl.Params {
		if _, dup := seen[p.Name]; dup {
			c.error(p.Pos, "duplicate parameter %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		// `...T` on a function's parameter collects the arguments past the
		// others. A slot is the other thing `...` bounds, and a component's
		// parameters never reach here.
		variadic := false
		var typ *ir.Type
		if vt, ok := p.Type.(*ast.VariadicType); ok {
			if i != len(pl.Params)-1 {
				c.error(vt.Pos, "`...` collects the arguments past the others, so only the last parameter may carry one")
			}
			if p.Default != nil {
				c.error(vt.Pos, "parameter %q collects the arguments past the others, so it has no default: given none it is the empty list", p.Name)
			}
			variadic = true
			typ = c.listOf(c.resolveTypeRequired(vt.Elem, p.Pos, "parameter "+strconv.Quote(p.Name)))
		} else {
			typ = c.resolveTypeRequired(p.Type, p.Pos, "parameter "+strconv.Quote(p.Name))
		}
		var def ir.Expr
		if p.Default != nil {
			// Use a placeholder to signal "has default" for arity checks.
			// The actual default is checked later when the scope is ready.
			def = &ir.Literal{Type: typ}
		}
		params[i] = &ir.Param{
			Name:     p.Name,
			Type:     typ,
			Default:  def,
			Const:    p.Const,
			Variadic: variadic,
			Doc:      p.Doc(),
		}
	}
	return params
}

// funcHasBody reports whether a declaration was written with a body, either
// form.
func funcHasBody(f *ast.FuncDef) bool {
	return f.Body != nil || f.Block.IsDefined()
}

func (c *checker) buildFunc(f *ast.FuncDef) *ir.Func {
	typeName, methodName, isMethod := ast.SplitMethodName(f.Name)

	// Set type params so T resolves during param/return type resolution.
	// Include both method-level TypeParams and receiver-level RecvTypeParams so
	// that e.g. `func list[T].filter(f func(T) bool) list[T]` resolves T correctly.
	// Receiver parameters come first so the receiver type `list[T]` resolves.
	if isMethod {
		c.inheritReceiverConstraints(c.receiverType(typeName), f.RecvTypeParams)
	}
	popTypeParams := pushTypeParams(c, f.RecvTypeParams, f.TypeParams)
	// nil ReturnType means void (block body) or pending-inference (expression body);
	// leave Return nil here so checkFuncBody can infer from a `=>` body.
	var ret *ir.Type
	if f.ReturnType != nil {
		ret = c.resolveType(f.ReturnType)
	}
	fn := &ir.Func{
		AST:            f,
		Name:           f.Name,
		Pkg:            c.declPkg(),
		TypeParams:     c.resolveTypeParams(f.TypeParams),
		RecvTypeParams: c.resolveTypeParams(f.RecvTypeParams),
		Params:         c.buildParams(f.Params),
		Return:         ret,
		IsTest:         f.IsTest(),
		Const:          f.Const,
	}
	for i, p := range f.RecvTypeParams {
		fn.RecvTypeParams[i].Constraint = c.typeParamSym(p.Name, p.Pos).Constraint
	}
	// A bodyless `const func` is trusted: it is a host's identifier, an
	// intrinsic or an import, and nothing in the program says what the host
	// does, so the declaration is the whole of what is known. A bodied one is
	// held to its body by checkConstFuncs.
	if f.Const && !funcHasBody(f) {
		fn.Purity = ir.PurityPure
	}
	c.refuseParamMarks(f.Params.Params)
	popTypeParams()
	if isMethod {
		fn.Receiver = c.receiverType(typeName)
		fn.Name = methodName
		// The receiver convention: a method's first param is the receiver when
		// it is named `this` — prepended synthetically for bare component
		// funcs, or written out as `func T.m(this T)` — or when its type is
		// the type the method is attached to, whatever it is called. The
		// second form is what lets `func int.ident(n int)` be reached as
		// `1.ident()` and as `int.ident(1)`. Mark it structurally here, the
		// one place that owns the convention, so codegen/interp/lowering find
		// the receiver by Param.Receiver rather than re-matching a name.
		if len(fn.Params) > 0 && (fn.Params[0].Name == ir.ReceiverParam || namesType(fn.Params[0].Type, typeName)) {
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
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString:
		return true
	case ir.TypeStruct:
		// Conservative: accept any named struct as comparable. Refine
		// in a follow-up if we want to actually check field types. This
		// also covers the string-representable structs (color/date/time/
		// datetime).
		return t.Decl != nil
	case ir.TypeEnum, ir.TypeUnit:
		return true
	case ir.TypeTypeParam:
		// An unbound type parameter is not judgeable here; whether the key is
		// comparable is decided where the parameter is bound to a concrete
		// type. Rejecting it would make `map[K, V]` undeclarable as a
		// parameter, which the map intrinsics need.
		return true
	}
	return false
}

// resolveSlotContent reads a slot's declared type as what each supplied node
// must be, plus how many are accepted. Bare `T` takes any number; the two
// wrappers narrow it, and neither is a type the slot content ever has — they
// say a count, so they are read here rather than constructed as types.
func (c *checker) resolveSlotContent(t ast.TypeExpr) (*ir.Type, ir.SlotCard) {
	c.familyPosition++
	defer func() { c.familyPosition-- }()
	nt, ok := t.(*ast.NamedType)
	if !ok {
		return c.resolveType(t), ir.SlotAny
	}
	if card, elem := c.slotWrapper(nt); card != ir.SlotAny {
		return c.requireFamily(nt.Pos, elem), card
	}
	return c.requireFamily(nt.Pos, c.resolveType(t)), ir.SlotAny
}

// requireFamily holds a slot's content to naming a tree. What lands in a slot is
// components, so a value type there says nothing a caller could satisfy.
func (c *checker) requireFamily(pos ast.Pos, t *ir.Type) *ir.Type {
	if t == nil || t.Kind == ir.TypeInvalid {
		return t
	}
	if ir.TypeFamily(t) != nil {
		return t
	}
	// A type parameter is a tree the call site names: the wrapper that returns
	// one accepts whatever family it was handed. Which parameter, and that the
	// component declares it, is finishTreeMarks' question.
	if t.Kind == ir.TypeTypeParam {
		return t
	}
	c.error(pos, "a slot accepts a family, and %s is not one", t)
	return nil
}

// slotWrapper reports the count a wrapper names, and the type it wraps. The
// wrappers are recognised by their #[builtin] kind so a user declaration of
// either name shadows them like any other built-in.
func (c *checker) slotWrapper(nt *ast.NamedType) (ir.SlotCard, *ir.Type) {
	name := nt.Name
	var sym ir.Symbol
	if nt.Package != "" {
		name = nt.Package + "." + nt.Name
		nsSym, ok := c.scope.Lookup(nt.Package)
		if !ok {
			return ir.SlotAny, nil
		}
		ns, ok := nsSym.(*ir.Namespace)
		if !ok {
			return ir.SlotAny, nil
		}
		if ns.Pkg == nil {
			return ir.SlotAny, nil
		}
		var found bool
		if sym, found = ns.Pkg.Symbols.LookupMemberType(nt.Name); !found {
			return ir.SlotAny, nil
		}
	} else {
		var ok bool
		if sym, ok = c.scope.Lookup(nt.Name); !ok {
			return ir.SlotAny, nil
		}
	}
	sd, ok := sym.(*ir.StructDef)
	if !ok {
		return ir.SlotAny, nil
	}
	var card ir.SlotCard
	switch sd.Builtin {
	case ir.BuiltinFamilyOne:
		card = ir.SlotOne
	case ir.BuiltinOption:
		card = ir.SlotOptional
	default:
		return ir.SlotAny, nil
	}
	switch {
	case len(nt.TypeArgs) == 1:
		return card, c.resolveType(nt.TypeArgs[0])
	case len(nt.TypeArgs) == 0 && card == ir.SlotOne:
		// Bare `family.one` is a count and no family: one of whatever the slot
		// already accepts, which is the tree its owner belongs to. `option`
		// has a type to name and no such reading.
		return card, nil
	case len(nt.TypeArgs) == 0 && c.typeParamDefault(sd, 0) != nil:
		return card, c.typeParamDefault(sd, 0)
	}
	c.error(nt.Pos, "%s takes exactly one type argument", name)
	return card, &ir.Type{Kind: ir.TypeComponent}
}

// slotInserts reads the `option[…]` or `list[…]` written around a named slot's
// component type. Like the node-count wrappers it is recognised by #[builtin]
// kind, so a program's own `option` is no count and the slot is refused.
func (c *checker) slotInserts(w *ast.NamedType) ir.SlotInserts {
	if sym, ok := c.scope.Lookup(w.Name); ok {
		if sd, ok := sym.(*ir.StructDef); ok {
			switch sd.Builtin {
			case ir.BuiltinOption:
				return ir.InsertOptional
			case ir.BuiltinList:
				return ir.InsertMany
			}
		}
	}
	c.error(w.Pos, "%s[…] around a slot is not the builtin %s, so it counts nothing; a slot's count is option[…] or list[…]", w.Name, w.Name)
	return ir.InsertOne
}

// dataRecord is the type a data component's name stands for in a value
// position: its record (Component.Data). In a family position -- a return
// position, a slot's content -- the name is the family, and typ is returned as
// it is.
func (c *checker) dataRecord(typ *ir.Type) *ir.Type {
	if c.familyPosition > 0 || typ == nil || typ.Kind != ir.TypeComponent {
		return typ
	}
	if f, ok := typ.Decl.(*ir.Component); ok && f.Data && f.Record != nil {
		return &ir.Type{Kind: ir.TypeStruct, Decl: f.Record}
	}
	return typ
}

// refuseTypeArgs reports type arguments written on a declaration that takes
// none, naming it and the builtin it shadows, if any, and reports whether it
// did. Dropped, they left `list[string]` under a `component list` the
// component, and the error a "cannot infer element type of empty list
// literal" at an unrelated `[]`.
func (c *checker) refuseTypeArgs(t *ast.NamedType, sym ir.Symbol) bool {
	what := sym.SymName()
	switch sym.(type) {
	case *ir.Component:
		what = "component " + what
	case *ir.StructDef:
		what = "struct " + what
	case *ir.EnumDef:
		what = "enum " + what
	case *ir.UnitDef:
		what = "unit " + what
	default:
		return false
	}
	if pos := declPos(sym); pos.Line > 0 {
		what += fmt.Sprintf(" (at %s)", pos)
	}
	if b := c.shadowedBuiltin(t.Name, sym); b != nil {
		what += fmt.Sprintf(", which shadows the builtin %s and", b.Name)
	} else {
		what += ", which"
	}
	c.error(t.Pos, "%s here is %s takes no type arguments", t.Name, what)
	return true
}

// shadowedBuiltin is the generic builtin type called name that sym, found
// first, hides from an enclosing scope, or nil.
func (c *checker) shadowedBuiltin(name string, sym ir.Symbol) *ir.StructDef {
	found := false
	for s := c.scope; s != nil; s = s.Parent {
		other, ok := s.Symbols[name]
		if !ok {
			continue
		}
		if !found {
			found = other == sym
			continue
		}
		if sd, ok := other.(*ir.StructDef); ok && sd.Builtin.IsGeneric() {
			return sd
		}
	}
	return nil
}
