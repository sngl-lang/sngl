package checker

import (
	"errors"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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

// NewSymbolTable is ir.NewSymbolTable with the scalar universe in its base
// scope (scalarUniverse).
func NewSymbolTable() *ir.SymbolTable {
	st := ir.NewSymbolTable()
	base := st.Root.Parent
	for name, typ := range scalarUniverse() {
		base.Symbols[name] = &ir.TypeSym{Name: name, Type: typ}
	}
	return st
}

// lookupMethod finds a method on the declaration recv names, resolved through
// the scope the checker is currently in. That is the scope AttachMethod wrote
// through, so the two always agree on which declaration a receiver name means.
func (c *checker) lookupMethod(recv, method string) (*ir.Func, bool) {
	fn, ok := ir.LookupMethodIn(c.scope, recv, method)
	if ok {
		// The caller is about to read fn.Return. See symType: an expression
		// body has no return type until it is checked.
		c.ensureReturnType(fn)
	}
	return fn, ok
}

// lookupBodyMethod finds a component-body method by bare name: on the
// component being checked, then on each body that declared it.
func (c *checker) lookupBodyMethod(name string) (*ir.Func, bool) {
	for comp := c.currentComponent; comp != nil; comp = c.bodyOwnerOf(comp) {
		if fn, ok := c.lookupMethod(comp.Name, name); ok {
			return fn, true
		}
		// An override's helper is in its own copy of the table, which its
		// name does not reach.
		if fn, ok := comp.Methods[name]; ok && comp == c.installedOverride {
			c.ensureReturnType(fn)
			return fn, true
		}
	}
	return nil, false
}

// memberOf is the method named name on decl, a program's additions to it
// included.
func (c *checker) memberOf(decl any, name string) (*ir.Func, bool) {
	sym, ok := decl.(ir.Symbol)
	if !ok {
		return nil, false
	}
	return ir.MethodOn(c.scope, sym, name)
}

// attachMethod makes fn a member of the declaration name resolves to. A
// program's method on one another package declared is held on the program's
// root scope (ir.Scope.Added) rather than in that declaration's table, which
// every check reading the declaration's package shares -- except an override's
// helper, which belongs to that override's own copy of the table.
func (c *checker) attachMethod(name string, fn *ir.Func) error {
	sym, found := c.scope.Lookup(name)
	if ov := c.installedOverride; found && ov != nil && (sym == c.overriddenBase || sym == ov) {
		if prev, ok := ov.Methods[fn.Name]; ok {
			if prev != fn {
				return &ir.RedeclaredError{Name: fn.Name, Prev: prev}
			}
			return nil
		}
		if ov.Methods == nil {
			ov.Methods = map[string]*ir.Func{}
		}
		ov.Methods[fn.Name] = fn
		return nil
	}
	if found && !c.inLibSource() {
		if owner := ir.DeclaringPackage(sym); owner != nil && owner != c.declPkg() {
			return ir.AddMethod(c.symtab.Root, sym, fn)
		}
	}
	return ir.AttachMethod(c.scope, name, fn)
}

// reportDuplicateMethod reports fn colliding with prev on the declaration its
// receiver names, described by on, naming the package that declared prev
// when it is another's.
func (c *checker) reportDuplicateMethod(pos ast.Pos, fn, prev *ir.Func, on string) {
	if other := prev.PkgPath(); other != fn.PkgPath() && other != "" {
		c.error(pos, "duplicate declaration of %q on %s: %s declares it", fn.Name, on, other)
		return
	}
	c.error(pos, "duplicate declaration of %q on %s", fn.Name, on)
}

// declareMethod makes fn a member of the declaration its receiver names,
// reporting an unknown receiver at pos. Returns the member it collides with,
// for the caller to report as a duplicate, and nil when the attach stands:
// because it succeeded, or because the receiver carries no member list -- a
// namespace, whose members are the declarations of its package. A method
// another package declared collides too: a program may add to a library
// type but not redefine what it has, so a library declaration means one
// thing in every program that reads it.
func (c *checker) declareMethod(pos ast.Pos, fn *ir.Func) *ir.Func {
	// The member table is the declaration's, so the receiver is the one the
	// table is found on -- in this scope, which a body-local type's method
	// is attached in.
	name := fn.RecvName()
	fn.Receiver = c.receiverType(name)
	err := c.attachMethod(name, fn)
	switch {
	case err == nil, errors.Is(err, ir.ErrNoMethodHost):
		return nil
	case errors.Is(err, ir.ErrUnknownReceiver):
		// Without a receiver to attach to there is no member list to hold the
		// method, so it could never be found again.
		c.error(pos, "undefined: %s (no type to declare method %q on)", fn.RecvName(), fn.Name)
		return nil
	}
	var red *ir.RedeclaredError
	if !errors.As(err, &red) {
		return nil
	}
	prev, _ := red.Prev.(*ir.Func)
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
	// The file's own scope sits under the package scope, so an alias one file
	// binds is unreachable from its siblings while every declaration stays
	// package-wide. Only while a file is current: registration that happens
	// outside one has nowhere else to put a name.
	if c.curFileScope != nil && c.scope == c.symtab.Root {
		return c.curFileScope
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
			c.declare(p.ParamPos(), c.typeParamSym(p.ParamName(), p.ParamPos()))
		}
	}
	return c.popScope
}

// typeParamSym is the symbol of the type parameter declared at pos. A
// declaration's parameters are pushed more than once -- its signature, then
// its body -- and each push has to bind the one symbol, since a type refers
// to its parameter by that symbol. A parameter with no position is a new one.
func (c *checker) typeParamSym(name string, pos ast.Pos) *ir.TypeParamSym {
	if !pos.IsSet() {
		return &ir.TypeParamSym{Name: name}
	}
	key := typeParamKey{pos: pos, name: name}
	if sym, ok := c.decls.typeParamSyms[key]; ok {
		return sym
	}
	if sym, ok := c.libs.decls.typeParamSyms[key]; ok {
		return sym
	}
	t := c.tables()
	if t.typeParamSyms == nil {
		t.typeParamSyms = map[typeParamKey]*ir.TypeParamSym{}
	}
	sym := &ir.TypeParamSym{Name: name}
	t.typeParamSyms[key] = sym
	return sym
}

// typeParamKey names a declared type parameter: where it is written, and what.
type typeParamKey struct {
	pos  ast.Pos
	name string
}

// receiverType is the receiver a method declared on name has: the type of the
// declaration name resolves to here. A name that resolves to nothing, or to
// something that is no type, keeps a stand-in for its name, and attaching
// reports it.
func (c *checker) receiverType(name string) *ir.Type {
	sym, ok := c.scope.Lookup(name)
	if !ok {
		return ir.ReceiverNamed(name)
	}
	if t := sym.SymType(); t != nil && t.Decl == sym {
		return t
	}
	// A name that is no type -- a value, a package -- receives no method,
	// which attaching reports; the type only carries the name there.
	return &ir.Type{Kind: ir.TypeInvalid, Decl: sym}
}
