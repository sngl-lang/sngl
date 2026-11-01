package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// An alias, `struct Name = Type`, is Type under another name: the same type
// wherever it is written, not a new one. It is written with the keyword of
// what it names, and for now it names a constraint only -- one sngl:builtin
// declares, or another alias -- so it is resolved where a type parameter's
// constraint is (namedConstraint) and refused anywhere a type is written.

// registerAlias declares s, an alias, under its name. What it names waits
// until every declaration of the package exists (aliasConstraint).
func (c *checker) registerAlias(s *ast.StructDef) *ir.Alias {
	a := &ir.Alias{Name: s.Name, Pos: s.Pos, Pkg: c.declPkg(), AST: s}
	if s.Name == "" {
		c.error(s.Pos, "an alias names what it stands for: `struct Name = Type`")
		return a
	}
	if len(s.Attrs) > 0 {
		c.error(s.Attrs[0].Pos, "an alias takes no marks: it is what it names")
	}
	claimed := c.claimTopLevel(s.Name, s.Pos, bindDecl, "")
	c.declPkg().Aliases = append(c.declPkg().Aliases, a)
	c.bindDeclared(claimed, a)
	return a
}

// aliasConstraint is the constraint a names, resolved the first time it is
// asked: at the package's root, through the imports of the file declaring
// it, whatever scope asks. Its parameters are its own, and a function
// `methodsOf` names is called on a.Self, which each use binds to the
// parameter it constrains (ir.ExpandAlias).
func (c *checker) aliasConstraint(a *ir.Alias) *ir.Type {
	if a.Constraint != nil {
		return a.Constraint
	}
	if a.Pkg != c.declPkg() || a.AST == nil || a.AST.Alias == nil {
		return ir.TypAny
	}
	if c.aliasing[a] {
		c.error(a.Pos, "the alias %s names itself", a.Name)
		return ir.TypAny
	}
	if c.aliasing == nil {
		c.aliasing = map[*ir.Alias]bool{}
	}
	c.aliasing[a] = true
	defer delete(c.aliasing, a)

	scope, fileScope, parent := c.scope, c.curFileScope, c.symtab.Root.Parent
	defer func() { c.scope, c.curFileScope, c.symtab.Root.Parent = scope, fileScope, parent }()
	c.scope = c.symtab.Root
	c.enterFileOf(a.Pos)

	defer pushTypeParams(c, a.AST.TypeParams)()
	a.TypeParams = c.resolveTypeParams(a.AST.TypeParams)
	a.Self = &ir.TypeParamSym{Name: a.Name}
	con := c.resolveConstraintFor(a.AST.Alias, a.Self.SymType(), true)
	if a.Constraint == nil {
		a.Constraint = con
	}
	return a.Constraint
}

// expandAlias is the constraint t, naming a, says on self: a's with t's type
// arguments for its parameters, each held to its own constraint, and self for
// the parameter it is resolved on.
func (c *checker) expandAlias(t *ast.NamedType, a *ir.Alias, self *ir.Type) *ir.Type {
	if con := c.aliasConstraint(a); a.Constraint == nil {
		return con
	}
	if len(t.TypeArgs) > len(a.TypeParams) {
		c.error(t.Pos, "%s takes %d type argument(s), got %d", a.Name, len(a.TypeParams), len(t.TypeArgs))
		return ir.TypAny
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
			return ir.TypAny
		}
	}
	c.checkWrittenArgs(a.TypeParams, args, t.TypeArgs)
	return ir.ExpandAlias(a, args, self)
}
