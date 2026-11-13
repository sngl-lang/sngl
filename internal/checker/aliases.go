package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// An alias, `struct Name = Type`, is Type under another name: the same type
// wherever it is written, not a new one. It is written with the keyword of
// what it names -- `enum Name = Type` names an enum, `unit Name = Type` a
// unit, and `struct` anything else, a constraint included, since the builtin
// types and constraints are declared as structs. Where a type is written it
// is the type it names (resolveNamedType), where a constraint is the
// constraint (namedConstraint), and where its name stands for a declaration
// -- a literal, a member, a method's receiver -- that declaration
// (aliasedDecl).

// registerAlias declares an alias, written by decl with keyword, under its
// name. What it names waits until every declaration of the package exists
// (resolveAlias).
func (c *checker) registerAlias(decl ast.Stmt, keyword, name string, pos ast.Pos, params []ast.TypeParam, written ast.TypeExpr, attrs []ast.MacroAttr) *ir.Alias {
	a := &ir.Alias{Name: name, Pos: pos, Pkg: c.declPkg(), Keyword: keyword, Decl: decl, Written: written, WrittenParams: params}
	if name == "" {
		c.error(pos, "an alias names what it stands for: `%s Name = Type`", keyword)
		return a
	}
	claimed := c.claimTopLevel(name, pos, bindDecl, "")
	c.declPkg().Aliases = append(c.declPkg().Aliases, a)
	c.bindDeclared(claimed, a)
	if len(attrs) > 0 {
		marked := decl.(markTarget)
		c.afterMarks(func() {
			for _, attr := range attrs {
				c.applyMark(attr, marked, a, aliasMarks, "an alias: it is what it names, and only #[c.native] says more, the C typedef it spells")
			}
			if a.C != nil {
				c.resolveAlias(a)
				c.checkCAlias(a, attrs[0].Pos)
			}
		})
	}
	return a
}

// checkCAlias holds a `#[c.native]` alias to what a C typedef SNGL can spell
// names: a number or a bool, which crosses cast to it, or a string, which
// crosses as UTF-16 through a pointer to 16-bit units. Anything else C
// declares is a `#[c.native]` struct.
func (c *checker) checkCAlias(a *ir.Alias, pos ast.Pos) {
	switch t := a.Type; {
	case t == nil:
	case t.Kind == ir.TypeString:
		if !a.C.IsUTF16Pointer() {
			c.error(pos, "#[c.native(%q)] marks an alias of string, which crosses as UTF-16 through a pointer to 16-bit units (`const unsigned short *`); a `char *` is what a string crosses as already", a.C)
		}
	case t.Kind == ir.TypeInt || t.Kind == ir.TypeFloat || t.Kind == ir.TypeBool:
		if a.C.Ptr > 0 {
			c.error(pos, "#[c.native(%q)] marks an alias of %s, so it names a C scalar and no pointer", a.C, t)
		}
	default:
		c.error(pos, "#[c.native(%q)] marks an alias of %s; an alias spells a C typedef of int, float, bool or string, and any other C type is a #[c.native] struct", a.C, t)
	}
}

// aliasMarks are the marks an alias takes. It is what it names, so a mark
// saying something of its own is refused, but a C function's parameter
// written with one crosses as the C typedef `#[c.native]` names (Param.CAlias).
var aliasMarks = map[markKey]bool{
	{"x/c", "native"}: true,
}

// registerAliasDecl registers stmt where it is an alias, reporting whether it
// was one.
func (c *checker) registerAliasDecl(stmt ast.Stmt) (*ir.Alias, bool) {
	switch s := stmt.(type) {
	case *ast.StructDef:
		if s.Alias != nil {
			return c.registerAlias(s, "struct", s.Name, s.Pos, s.TypeParams, s.Alias, s.Attrs), true
		}
	case *ast.EnumDef:
		if s.Alias != nil {
			return c.registerAlias(s, "enum", s.Name, s.Pos, s.TypeParams, s.Alias, s.Attrs), true
		}
	case *ast.UnitDef:
		if s.Alias != nil {
			return c.registerAlias(s, "unit", s.Name, s.Pos, nil, s.Alias, s.Attrs), true
		}
	}
	return nil, false
}

// resolveAlias resolves what a names, the first time it is asked: at the
// package's root, through the imports of the file declaring it, whatever
// scope asks. Its parameters are its own. A constraint's function `methodsOf`
// names is called on a.Self, which each use binds to the parameter it
// constrains (ir.ExpandAlias).
func (c *checker) resolveAlias(a *ir.Alias) {
	if a.Type != nil || a.Constraint != nil || a.Pkg != c.declPkg() || a.Written == nil {
		return
	}
	switch c.aliasing[a] {
	case aliasResolving:
		c.error(a.Pos, "the alias %s names itself", a.Name)
		c.aliasing[a] = aliasCycle
		return
	case aliasCycle:
		return
	}
	if c.aliasing == nil {
		c.aliasing = map[*ir.Alias]aliasState{}
	}
	c.aliasing[a] = aliasResolving
	defer func() {
		if c.aliasing[a] == aliasResolving {
			delete(c.aliasing, a)
		}
	}()

	scope, fileScope, parent := c.scope, c.curFileScope, c.symtab.Root.Parent
	defer func() { c.scope, c.curFileScope, c.symtab.Root.Parent = scope, fileScope, parent }()
	c.scope = c.symtab.Root
	c.enterFileOf(a.Pos)

	defer pushTypeParams(c, a.WrittenParams)()
	a.TypeParams = c.resolveTypeParams(a.WrittenParams)
	if c.namesConstraint(a.Written) {
		self := &ir.TypeParamSym{Name: a.Name}
		con := c.resolveConstraintFor(a.Written, self.SymType(), true)
		if a.Keyword != "struct" {
			c.error(a.Pos, "a constraint is aliased with `struct`: `struct %s = …`", a.Name)
		}
		if a.Constraint == nil {
			a.Self, a.Constraint = self, con
		}
		return
	}
	t := c.resolveType(a.Written)
	if want := aliasKeyword(t); t.Kind != ir.TypeInvalid && want != a.Keyword {
		c.error(a.Pos, "%s is %s %s, so its alias is written `%s %s = …`", t, article(want), want, want, a.Name)
	}
	if a.Type == nil && a.Constraint == nil {
		a.Type = t
	}
}

// aliasState is where resolving an alias stands: aliasResolving while what
// it names is, which one naming itself meets again, and aliasCycle after,
// so that it is reported once.
type aliasState int

const (
	aliasDone aliasState = iota
	aliasResolving
	aliasCycle
)

// aliasKeyword is the keyword an alias of t is written with: the kind of
// declaration t is.
func aliasKeyword(t *ir.Type) string {
	switch t.Kind {
	case ir.TypeEnum:
		return "enum"
	case ir.TypeUnit:
		return "unit"
	}
	return "struct"
}

func article(word string) string {
	if word != "" && (word[0] == 'e' || word[0] == 'u') {
		return "an"
	}
	return "a"
}

// namesConstraint reports whether te names a constraint: one sngl:builtin
// declares, or an alias of one.
func (c *checker) namesConstraint(te ast.TypeExpr) bool {
	t, ok := te.(*ast.NamedType)
	if !ok {
		return false
	}
	var sym ir.Symbol
	if t.Package != "" {
		if a := c.qualifiedAlias(t); a != nil {
			sym = a
		}
	} else {
		sym, _ = c.scope.Lookup(t.Name)
	}
	switch s := sym.(type) {
	case *ir.StructDef:
		return s.Builtin.IsConstraint()
	case *ir.Alias:
		c.resolveAlias(s)
		return s.Constraint != nil
	}
	typ, _, ok := scalarOf(sym)
	return ok && typ == ir.TypAny
}

// expandAlias is what t, naming a, stands for: a's type or constraint with
// t's type arguments for its parameters, each held to its own constraint, and
// self for the parameter a constraint is resolved on. Nil where a names
// nothing, having named itself.
func (c *checker) expandAlias(t *ast.NamedType, a *ir.Alias, self *ir.Type) *ir.Type {
	c.resolveAlias(a)
	if a.Type == nil && a.Constraint == nil {
		return nil
	}
	if len(t.TypeArgs) > len(a.TypeParams) {
		c.error(t.Pos, "%s takes %d type argument(s), got %d", a.Name, len(a.TypeParams), len(t.TypeArgs))
		return nil
	}
	args := make([]*ir.Type, len(a.TypeParams))
	for i, tp := range a.TypeParams {
		switch {
		case i < len(t.TypeArgs):
			args[i] = c.resolveType(t.TypeArgs[i])
		case tp.Default != nil:
			args[i] = tp.Default
		default:
			c.error(t.Pos, "%s requires a type argument for %s, which has no default", a.Name, tp.Name)
			return nil
		}
	}
	c.checkWrittenArgs(a.TypeParams, args, t.TypeArgs)
	return ir.ExpandAlias(a, args, self)
}

// aliasType is the type t names where it names an alias of one, ok false
// where it names no alias. A constraint's alias is refused, as a constraint
// is where a type is written.
func (c *checker) aliasType(t *ast.NamedType, sym ir.Symbol) (*ir.Type, bool) {
	a, ok := sym.(*ir.Alias)
	if !ok {
		return nil, false
	}
	c.resolveAlias(a)
	if a.Constraint != nil {
		name := t.Name
		if t.Package != "" {
			name = t.Package + "." + t.Name
		}
		c.error(t.Pos, "`%s` is an alias of a constraint, not a type: only a type parameter's constraint names it", name)
		return TypInvalid, true
	}
	if typ := c.expandAlias(t, a, nil); typ != nil {
		return typ, true
	}
	return TypInvalid, true
}

// aliasedDecl is the declaration sym names where it is an alias of a type
// that has one -- a struct, an enum, a unit, or a builtin type's -- and sym
// otherwise: `Alias{…}`, `Alias.member` and `func Alias.m()` mean what they
// would with the declaration's own name.
func (c *checker) aliasedDecl(sym ir.Symbol) ir.Symbol {
	a, ok := sym.(*ir.Alias)
	if !ok {
		return sym
	}
	c.resolveAlias(a)
	if a.Type == nil {
		return sym
	}
	if a.Type.Decl != nil {
		return a.Type.Decl
	}
	if d, ok := c.scope.Lookup(a.Type.String()); ok {
		return d
	}
	return sym
}

// literalAlias is the type a literal written with an alias's name builds,
// `Alias{…}` or `Alias[int]{…}`: the alias's type, arguments and all. Nil
// where x names no alias of a type.
func (c *checker) literalAlias(x *ast.StructExpr) *ir.Type {
	if x.Name == "" || x.Anon != nil || x.Native != nil {
		return nil
	}
	var sym ir.Symbol
	if x.Package != "" {
		nt := &ast.NamedType{Pos: x.Pos, Package: x.Package, Name: x.Name}
		if a := c.qualifiedAlias(nt); a != nil {
			sym = a
		}
	} else {
		sym, _ = c.scope.Lookup(x.Name)
	}
	if _, ok := sym.(*ir.Alias); !ok {
		return nil
	}
	nt := &ast.NamedType{Pos: x.Pos, Package: x.Package, Name: x.Name}
	for _, a := range x.TypeArgs {
		if te, ok := ast.TypeFromExpr(a); ok {
			nt.TypeArgs = append(nt.TypeArgs, te)
		}
	}
	t, _ := c.aliasType(nt, sym)
	return t
}
