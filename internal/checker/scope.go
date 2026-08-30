package checker

import (
	"errors"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:fix inline
type Symbol = ir.Symbol

//go:fix inline
type LoopVar = ir.LoopVar

//go:fix inline
type Namespace = ir.Namespace

//go:fix inline
type Scope = ir.Scope

//go:fix inline
type TypeSym = ir.TypeSym

//go:fix inline
type SymbolTable = ir.SymbolTable

//go:fix inline
var NewScope = ir.NewScope

//go:fix inline
var NewSymbolTable = ir.NewSymbolTable

func structDecl(st *ir.SymbolTable, name string) *ir.StructDef {
	if sym, ok := st.LookupType(name); ok {
		if sd, isStruct := sym.(*ir.StructDef); isStruct {
			return sd
		}
	}
	return nil
}

// lookupMethod finds a method on the declaration recv names, resolved through
// the scope the checker is currently in. That is the scope AttachMethod wrote
// through, so the two always agree on which declaration a receiver name means.
func (c *checker) lookupMethod(recv, method string) (*ir.Func, bool) {
	return ir.LookupMethodIn(c.scope, recv, method)
}

// declareMethod makes fn a member of the declaration its receiver names,
// reporting an unknown receiver at pos. Returns the member it collides with
// when the caller must report a duplicate, and nil when the attach stands:
// because it succeeded, because it deliberately shadows a standard-library
// member (which user code may do), or because the receiver carries no member
// list — a namespace, whose members are the declarations of its package.
func (c *checker) declareMethod(pos ast.Pos, fn *ir.Func) *ir.Func {
	err := ir.AttachMethod(c.scope, fn.Receiver, fn)
	switch {
	case err == nil, errors.Is(err, ir.ErrNoMethodHost):
		return nil
	case errors.Is(err, ir.ErrUnknownReceiver):
		// Without a receiver to attach to there is no member list to hold the
		// method, so it could never be found again.
		c.error(pos, "undefined: %s (no type to declare method %q on)", fn.Receiver, fn.Name)
		return nil
	}
	var red *ir.RedeclaredError
	if !errors.As(err, &red) {
		return nil
	}
	prev, isFunc := red.Prev.(*ir.Func)
	if isFunc && prev.Stdlib && !fn.Stdlib {
		if err := ir.ReplaceMethod(c.scope, fn.Receiver, fn); err != nil {
			return nil
		}
		return nil
	}
	return prev
}

// bindLib binds a declaration the standard library makes. A collision here is
// a duplicate inside lib/, which is a compiler bug rather than anything a
// program did, so it is reported against the library's own position.
func (c *checker) bindLib(pos ast.Pos, scope *ir.Scope, sym ir.Symbol) {
	if err := scope.Declare(sym); err != nil {
		c.error(pos, "stdlib declares %s twice", sym.SymName())
	}
}

// bindLifted binds a name a dot import lifted. Unlike bindLib it does not lift
// the symbol's wildcard pattern with it (ir.Scope.DeclareName): the import
// names the declaration, not the open set of names that declaration also
// answers to.
func (c *checker) bindLifted(pos ast.Pos, scope *ir.Scope, sym ir.Symbol) {
	if err := scope.DeclareName(sym); err != nil {
		c.error(pos, "stdlib declares %s twice", sym.SymName())
	}
}

// bindDeclared binds a top-level declaration once claimTopLevel has ruled on
// the name. That rule permits exactly one rebinding — a declaration shadowing
// a name a dot import lifted into this same scope — so the write is a
// deliberate replace, and a rejected claim binds nothing rather than
// clobbering the name it just reported a conflict on.
func (c *checker) bindDeclared(claimed bool, sym ir.Symbol) {
	if !claimed {
		return
	}
	c.scope.Replace(sym)
}

// importScope is where an import binds the name it introduces: the file's own
// scope, or — while a library package loads — the scope above that package's
// root, so a dot import of the package does not lift its imports on.
func (c *checker) importScope() *ir.Scope {
	if c.libImportScope != nil {
		return c.libImportScope
	}
	return c.scope
}

// bindImport binds the namespace an import declares, once claimTopLevel has
// ruled on the alias.
func (c *checker) bindImport(claimed bool, sym ir.Symbol) {
	if !claimed {
		return
	}
	c.importScope().Replace(sym)
}

// mergeInto binds a declaration into a package's surface as the package's
// files are merged. Two files of one package declaring the same name is a
// duplicate: files are read in no guaranteed order, so tolerating one would
// leave the exposed declaration up to that order.
func (c *checker) mergeInto(pos ast.Pos, dst *ir.Scope, sym ir.Symbol) {
	if err := dst.Declare(sym); err != nil {
		c.error(pos, "%s is declared in more than one file of this package", sym.SymName())
	}
}

func declPos(sym ir.Symbol) ast.Pos {
	switch d := sym.(type) {
	case *ir.Var:
		return varPos(d)
	case *ir.Func:
		return funcDeclPos(d)
	case *ir.Component:
		return compDeclPos(d)
	case *ir.StructDef:
		if d.AST != nil {
			return d.AST.Pos
		}
	case *ir.EnumDef:
		if d.AST != nil {
			return d.AST.Pos
		}
	case *ir.UnitDef:
		if d.AST != nil {
			return d.AST.Pos
		}
	}
	return ast.Pos{}
}

// typeParamLike is a type parameter of either the ast or the ir shape. The
// two carry the same two facts and no package needs to name the other's.
type typeParamLike interface {
	ParamName() string
	ParamPos() ast.Pos
}

// pushTypeParams opens a scope holding the type parameters and returns the pop.
// A free function rather than a method because Go allows no type parameters on
// one, and the two packages' parameters differ only in the name they answer to.
func pushTypeParams[T typeParamLike](c *checker, groups ...[]T) func() {
	c.pushScope()
	for _, g := range groups {
		for _, p := range g {
			c.declare(p.ParamPos(), &ir.TypeParamSym{Name: p.ParamName()})
		}
	}
	return c.popScope
}
