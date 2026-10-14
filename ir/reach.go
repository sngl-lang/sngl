package ir

// Reached is what a program reaches: its own declarations, and each
// declaration of a package it imports that a name in them leads to -- a call,
// a node's component, a type, a struct literal, a context read, a member
// read through a package's name -- through bodies, signatures and the
// target's override of each declaration on the way. A scope and an overlay
// name what is in scope rather than what is used, so neither is followed.
//
// A nil *Reached reaches everything, for a caller that builds no target.
type Reached struct {
	decls map[Symbol]struct{}
	queue []Symbol
	t     target
	root  *Package
}

// ReachedFrom walks pkg for the target named by platform and language.
func ReachedFrom(pkg *Package, platform, language string) *Reached {
	r := &Reached{decls: map[Symbol]struct{}{}, t: target{platform, language}, root: pkg}
	for _, v := range pkg.Consts {
		r.add(v)
	}
	for _, v := range pkg.BuildConsts {
		r.add(v)
	}
	for _, v := range pkg.Vars {
		r.add(v)
	}
	for _, f := range pkg.Funcs {
		r.add(f)
	}
	for _, s := range pkg.Structs {
		r.add(s)
	}
	for _, e := range pkg.Enums {
		r.add(e)
	}
	for _, u := range pkg.Units {
		r.add(u)
	}
	for _, c := range pkg.Contexts {
		r.add(c)
	}
	for _, c := range pkg.Components {
		r.add(c)
	}
	for _, o := range pkg.Outputs {
		if o.Options != nil {
			r.expr(o.Options)
		}
	}
	r.stmts(pkg.Body)
	for len(r.queue) > 0 {
		sym := r.queue[len(r.queue)-1]
		r.queue = r.queue[:len(r.queue)-1]
		r.decl(sym)
	}
	r.queue = nil
	return r
}

// Has reports whether decl is reached.
func (r *Reached) Has(decl Symbol) bool {
	if r == nil {
		return true
	}
	_, ok := r.decls[decl]
	return ok
}

func (r *Reached) add(sym Symbol) {
	switch sym.(type) {
	case *Component, *Func, *Var, *StructDef, *EnumDef, *UnitDef, *Context:
	default:
		return
	}
	if _, ok := r.decls[sym]; ok {
		return
	}
	r.decls[sym] = struct{}{}
	r.queue = append(r.queue, sym)
}

func (r *Reached) decl(sym Symbol) {
	switch d := sym.(type) {
	case *Component:
		if d.Family != nil {
			r.add(d.Family)
		}
		for _, t := range d.FamilyTypeArgs {
			r.typ(t)
		}
		if d.Record != nil {
			r.add(d.Record)
		}
		r.typ(d.ChildrenType)
		for _, p := range d.Props {
			r.typ(p.Type)
			r.expr(p.Default)
		}
		for _, e := range d.Events {
			r.params(e.Params)
		}
		for _, s := range d.Slots {
			r.slot(s)
		}
		r.body(d.Body)
		r.stmts(d.DeclaredBody)
		if o, ok := r.override(d, d.PlatformOverrides, d.LanguageOverrides); ok {
			r.body(o.Body)
		}
	case *Func:
		r.fn(d)
		if o, ok := r.override(d, d.PlatformOverrides, d.LanguageOverrides); ok {
			r.body(o.Body)
			if o.RecvParam != nil {
				r.typ(o.RecvParam.Type)
			}
		}
	case *Var:
		r.typ(d.Type)
		r.expr(d.Init)
	case *StructDef:
		for _, f := range d.Fields {
			r.typ(f.Type)
			r.expr(f.Default)
		}
		if d.RecordOf != nil {
			r.add(d.RecordOf)
		}
	case *EnumDef:
		for _, m := range d.Members {
			r.expr(m.Value)
		}
	case *Context:
		r.typ(d.Typ)
		r.expr(d.Default)
	}
}

func (r *Reached) slot(s *SlotDecl) {
	r.params(s.Params)
	r.typ(s.Content)
	if s.Values != nil {
		r.typ(s.Values.Type)
		r.expr(s.Values.Default)
	}
	for _, e := range s.Slots {
		r.slot(e)
	}
}

func (r *Reached) body(b Body) {
	for _, v := range b.Vars {
		r.add(v)
	}
	for _, f := range b.Funcs {
		r.add(f)
	}
	for _, d := range b.BodyDecls {
		r.add(d)
	}
	r.stmts(b.Stmts)
}

func (r *Reached) fn(f *Func) {
	r.typ(f.Receiver)
	r.params(f.Params)
	r.typ(f.Return)
	r.stmts(f.Block)
}

func (r *Reached) params(ps []*Param) {
	for _, p := range ps {
		r.typ(p.Type)
		r.expr(p.Default)
	}
}

func (r *Reached) typ(t *Type) {
	if t == nil {
		return
	}
	if t.Decl != nil {
		r.add(t.Decl)
	}
	for _, e := range t.Elems {
		r.typ(e)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			r.typ(p.Type)
		}
		r.typ(t.Sig.Return)
	}
}

func (r *Reached) stmts(stmts []Stmt) {
	if len(stmts) > 0 {
		_ = Walk(stmts, r.visit)
	}
}

func (r *Reached) expr(e Expr) {
	if e != nil {
		_ = Walk(e, r.visit)
	}
}

func (r *Reached) visit(n Node) error {
	if e, ok := n.(Expr); ok {
		r.typ(e.ExprType())
	}
	switch x := n.(type) {
	case *Ident:
		if x.Sym != nil {
			r.add(x.Sym)
		}
	case *Select:
		if id, ok := x.Operand.(*Ident); ok {
			if ns, ok := id.Sym.(*Namespace); ok && ns.Pkg != nil && ns.Pkg.Symbols != nil {
				if sym, ok := ns.Pkg.Symbols.LookupMember(x.Field); ok {
					r.add(sym)
				}
			}
		}
	case *Call:
		if x.Func != nil {
			r.add(x.Func)
		}
	case *StructLit:
		if x.Def != nil {
			r.add(x.Def)
		}
	case *ContextRead:
		if x.Ref != nil {
			r.add(x.Ref)
		}
	case *Closure:
		if x.Func != nil {
			r.add(x.Func)
		}
	case *Lambda:
		if x.Func != nil {
			r.params(x.Func.Params)
			r.typ(x.Func.Return)
		}
	case *LocalVar:
		r.typ(x.Type)
	case *If:
		if x.Catch != nil && x.Catch.Func != nil {
			r.add(x.Catch.Func)
		}
	case *NodeInst:
		if x.Component != nil {
			r.add(x.Component)
		}
		if x.Params != nil {
			r.typ(x.Params.Type)
		}
		for _, h := range x.Handlers {
			if h.Func != nil {
				r.add(h.Func)
			}
		}
	}
	return nil
}

// override is the body the target picks for sym, as the installed maps of a
// build's copy would hold it: on a checked package the library's and the
// program's overrides are still in its overlays, the program's winning.
func (r *Reached) override(sym Symbol, platform, language map[string]Override) (Override, bool) {
	var added []*Overrides
	for _, o := range []Overlay{r.root.Overlay, r.root.Targets} {
		if ov := o.Overrides[sym]; ov != nil {
			added = append(added, ov)
		}
	}
	if k := r.t.platform; k != "" {
		for _, ov := range added {
			if b, ok := ov.Platform[k]; ok {
				return b, true
			}
		}
		if b, ok := platform[k]; ok {
			return b, true
		}
	}
	if k := r.t.language; k != "" {
		for _, ov := range added {
			if b, ok := ov.Language[k]; ok {
				return b, true
			}
		}
		if b, ok := language[k]; ok {
			return b, true
		}
	}
	return Override{}, false
}
