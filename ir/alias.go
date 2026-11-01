package ir

import "duckfam.us/sngl/ast"

// Alias is `struct Name[…] = Type`: Type under another name, the same type
// wherever it is written rather than a new one. For now an alias names a
// constraint only, so it is met where a type parameter's constraint is
// resolved and nowhere else.
type Alias struct {
	Name string
	Pos  ast.Pos
	Pkg  *Package       `json:"-"`
	AST  *ast.StructDef `json:"-"`
	// TypeParams are the alias's own, which Constraint names and a use
	// binds: `struct Keyed[K any] = fieldsOf[struct { key K }]`.
	TypeParams []TypeParam
	// Constraint is what the alias names, resolved on Self: the type
	// parameter a function `methodsOf` names is called on, which each use
	// binds to the parameter it constrains (ExpandAlias). Nil until the
	// checker resolves it.
	Constraint *Type         `json:"-"`
	Self       *TypeParamSym `json:"-"`
}

func (a *Alias) SymName() string  { return a.Name }
func (a *Alias) SymType() *Type   { return nil }
func (a *Alias) IsExported() bool { return isExportedName(a.Name) }

// ExpandAlias is what a's constraint is where it is written with args for its
// own parameters, constraining self.
func ExpandAlias(a *Alias, args []*Type, self *Type) *Type {
	bindings := make(map[string]*Type, len(args)+1)
	for i, tp := range a.TypeParams {
		if i < len(args) {
			bindings[tp.Name] = args[i]
		}
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
