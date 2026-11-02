package ir

import "duckfam.us/sngl/ast"

// Alias is `struct Name[…] = Type`, or `enum` or `unit` in place of
// `struct`: Type under another name, the same type wherever it is written
// rather than a new one. It names a type or a constraint, and the checker
// replaces it with what it names wherever it is written, so nothing after the
// check meets one.
type Alias struct {
	Name string
	Pos  ast.Pos
	Pkg  *Package `json:"-"`
	// Keyword is the one it is declared with: "struct", "enum" or "unit",
	// which is the kind of type it names.
	Keyword string
	// Decl is the declaration that writes it, an *ast.StructDef, EnumDef or
	// UnitDef; Written is what it names and WrittenParams its parameters, as
	// written there.
	Decl          ast.Stmt        `json:"-"`
	Written       ast.TypeExpr    `json:"-"`
	WrittenParams []ast.TypeParam `json:"-"`
	// TypeParams are the alias's own, which what it names may use and a use
	// binds: `struct Keyed[K any] = fieldsOf[struct { key K }]`.
	TypeParams []TypeParam
	// Type is the type it names, and Constraint the constraint, whichever it
	// names; both nil until the checker resolves it, and both nil after for
	// one that names itself. A constraint is resolved on Self: the type
	// parameter a function `methodsOf` names is called on, which each use
	// binds to the parameter it constrains (ExpandAlias).
	Type       *Type         `json:"-"`
	Constraint *Type         `json:"-"`
	Self       *TypeParamSym `json:"-"`
}

func (a *Alias) SymName() string  { return a.Name }
func (a *Alias) SymType() *Type   { return nil }
func (a *Alias) IsExported() bool { return isExportedName(a.Name) }

// ExpandAlias is what a names where it is written with args for its own
// parameters: its type, or its constraint constraining self.
func ExpandAlias(a *Alias, args []*Type, self *Type) *Type {
	bindings := make(map[string]*Type, len(args)+1)
	for i, tp := range a.TypeParams {
		if i < len(args) {
			bindings[tp.Name] = args[i]
		}
	}
	if a.Type != nil {
		if len(bindings) == 0 {
			return a.Type
		}
		return a.Type.Substitute(bindings)
	}
	if a.Self != nil && self != nil {
		bindings[a.Self.Name] = self
	}
	return SubstituteConstraint(a.Constraint, bindings)
}

// SubstituteConstraint is con with bindings substituted throughout: into
// `fieldsOf`'s fields, as Substitute does, and into each function `methodsOf`
// names -- its parameters, its result and the receiver it is called on --
// which a struct's substitution leaves to its declaration.
func SubstituteConstraint(con *Type, bindings map[string]*Type) *Type {
	if con == nil || len(bindings) == 0 {
		return con
	}
	methods := ConstraintMethods(con)
	if len(methods) == 0 {
		return con.Substitute(bindings)
	}
	sd := ConstraintStruct(con)
	cp := &StructDef{Pkg: sd.Pkg, Anon: true, Methods: make(map[string]*Func, len(methods))}
	for name, fn := range methods {
		f := *fn
		f.Params = make([]*Param, len(fn.Params))
		for i, p := range fn.Params {
			q := *p
			q.Type = p.Type.Substitute(bindings)
			f.Params[i] = &q
		}
		f.Return = fn.Return.Substitute(bindings)
		f.Receiver = fn.Receiver.Substitute(bindings)
		if fn.RecvParam != nil {
			r := *fn.RecvParam
			r.Type = f.Receiver
			f.RecvParam = &r
		}
		cp.Methods[name] = &f
	}
	out := *con
	out.Elems = []*Type{cp.SymType()}
	return &out
}
