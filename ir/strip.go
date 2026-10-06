package ir

import (
	"reflect"

	"git.duckfam.us/jonathan/sngl/ast"
)

// StripForCompare removes AST references, cross-reference pointers, and
// symbol tables from a Package so that two independently-checked packages
// can be compared with reflect.DeepEqual. The package is modified in place.
func StripForCompare(pkg *Package) {
	s := &stripper{}
	s.stripPackage(pkg)
	clearReachableTreeKinds(pkg)
}

// clearReachableTreeKinds nils TreeKinds on every package the graph reaches,
// not only the one being stripped.
//
// It is a derived index keyed by declaration, and DeepEqual compares map keys
// by pointer, so a clone's own declarations can never match the original's.
// Imports are cut above, but a *library* package is still reachable through
// the scope chain a symbol carries -- and every program reaches sngl:ui that
// way now that its window names a tree.
func clearReachableTreeKinds(pkg *Package) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value, depth int)
	walk = func(v reflect.Value, depth int) {
		if depth > 64 || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				if p, ok := v.Interface().(*Package); ok {
					p.TreeKinds = nil
				}
			}
			walk(v.Elem(), depth+1)
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).PkgPath == "" {
					walk(v.Field(i), depth+1)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i), depth+1)
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k), depth+1)
			}
		}
	}
	walk(reflect.ValueOf(pkg), 0)
}

type stripper struct{}

func (s *stripper) stripPackage(pkg *Package) {
	pkg.Symbols = nil
	// A derived index keyed by declaration. DeepEqual compares map keys by
	// pointer, so a clone's own declarations could never match the original's.
	pkg.TreeKinds = nil

	// Normalize nil slices to empty for DeepEqual.
	if pkg.Imports == nil {
		pkg.Imports = []*Import{}
	}
	if pkg.Structs == nil {
		pkg.Structs = []*StructDef{}
	}
	if pkg.Enums == nil {
		pkg.Enums = []*EnumDef{}
	}
	if pkg.Units == nil {
		pkg.Units = []*UnitDef{}
	}
	if pkg.Consts == nil {
		pkg.Consts = []*Var{}
	}
	if pkg.Vars == nil {
		pkg.Vars = []*Var{}
	}
	if pkg.Funcs == nil {
		pkg.Funcs = []*Func{}
	}
	if pkg.Components == nil {
		pkg.Components = []*Component{}
	}
	if pkg.Outputs == nil {
		pkg.Outputs = []*Output{}
	}

	for _, imp := range pkg.Imports {
		imp.AST = nil
		imp.Pkg = nil
		imp.Native = nil
	}
	for _, sd := range pkg.Structs {
		s.stripStructDef(sd)
	}
	for _, ed := range pkg.Enums {
		s.stripEnumDef(ed)
	}
	for _, ud := range pkg.Units {
		ud.AST = nil
	}
	for _, v := range pkg.Consts {
		s.stripVar(v)
	}
	for _, v := range pkg.Vars {
		s.stripVar(v)
	}
	for _, f := range pkg.Funcs {
		s.stripFunc(f)
	}
	for _, c := range pkg.Components {
		s.stripComponent(c)
	}
	s.stripStmts(pkg.Body)
	for _, o := range pkg.Outputs {
		o.AST = nil
		o.LangComp = nil
		o.PlatComp = nil
		if o.Options != nil {
			s.stripExpr(o.Options)
		}
	}
}

func (s *stripper) stripStructDef(sd *StructDef) {
	sd.AST = nil
	stripTypeParams(sd.TypeParams)
	for _, f := range sd.Fields {
		f.Default = nil
	}
}

// stripTypeParams drops what a parameter records about its source. A
// round-trip that reprints and reparses derives the position afresh, so
// comparing it would compare the two spellings rather than the two packages.
func stripTypeParams(ps []TypeParam) {
	for i := range ps {
		ps[i].Pos = ast.Pos{}
	}
}

func (s *stripper) stripEnumDef(ed *EnumDef) {
	ed.AST = nil
	for _, m := range ed.Members {
		m.Value = nil
	}
}

func (s *stripper) stripVar(v *Var) {
	v.AST = nil
	s.stripExpr(v.Init)
	v.Type = nil
	for _, h := range v.Handlers {
		h.AST = nil
		s.stripFunc(h.Func)
	}
}

func (s *stripper) stripFunc(f *Func) {
	if f == nil {
		return
	}
	f.AST = nil
	stripTypeParams(f.TypeParams)
	stripTypeParams(f.RecvTypeParams)
	f.Reads = nil
	f.Writes = nil
	f.Purity = 0
	if f.Params == nil {
		f.Params = []*Param{}
	}
	for _, p := range f.Params {
		p.Type = nil
		s.stripExpr(p.Default)
	}
	f.Return = nil
	s.stripStmts(f.Block)
}

func (s *stripper) stripComponent(c *Component) {
	c.AST = nil
	if c.Props == nil {
		c.Props = []*Prop{}
	}
	if c.Events == nil {
		c.Events = []*EventDecl{}
	}
	if c.Vars == nil {
		c.Vars = []*Var{}
	}
	if c.Funcs == nil {
		c.Funcs = []*Func{}
	}
	if c.Body == nil {
		c.Body = []Stmt{}
	}
	for _, p := range c.Props {
		p.Type = nil
		p.Sym = nil // cross-reference
		s.stripExpr(p.Default)
	}
	for _, e := range c.Events {
		for _, p := range e.Params {
			p.Type = nil
		}
	}
	c.ChildrenType = nil
	for _, v := range c.Vars {
		s.stripVar(v)
	}
	for _, f := range c.Funcs {
		s.stripFunc(f)
	}
	s.stripStmts(c.Body)
}

// --- Statements ---

func (s *stripper) stripStmts(stmts []Stmt) {
	for _, st := range stmts {
		s.stripStmt(st)
	}
}

func (s *stripper) stripStmt(st Stmt) {
	if st == nil {
		return
	}
	switch st := st.(type) {
	case *NodeInst:
		st.AST = nil
		st.Component = nil // cross-reference
		if st.Props == nil {
			st.Props = []Arg{}
		}
		if st.Handlers == nil {
			st.Handlers = []EventHandler{}
		}
		if st.Children == nil {
			st.Children = []Stmt{}
		}
		for i := range st.Props {
			s.stripExpr(st.Props[i].Value)
		}
		for i := range st.Handlers {
			st.Handlers[i].AST = nil
			s.stripFunc(st.Handlers[i].Func)
		}
		s.stripStmts(st.Children)
		s.stripExpr(st.Ref)
	case *CallStmt:
		st.AST = nil
		s.stripExpr(st.Call)
	case *SlotInst:
		st.AST = nil
		s.stripStmts(st.Children)
	case *Assign:
		st.AST = nil
		s.stripExpr(st.Target)
		s.stripExpr(st.Value)
	case *Toggle:
		st.AST = nil
		s.stripExpr(st.Target)
	case *Emit:
		st.AST = nil
		for i := range st.Args {
			s.stripExpr(st.Args[i].Value)
		}
	case *LocalVar:
		st.AST = nil
		st.Type = nil
		st.Sym = nil // cross-reference
		s.stripExpr(st.Init)
	case *Return:
		st.AST = nil
		s.stripExpr(st.Value)
	case *If:
		st.AST = nil
		s.stripExpr(st.Cond)
		s.stripStmts(st.Body)
		s.stripStmts(st.Else)
	case *For:
		st.AST = nil
		s.stripExpr(st.Iter)
		st.ElemType = nil
		st.KeySym, st.ValueSym = nil, nil // cross-references
		s.stripStmts(st.Body)
		s.stripStmts(st.Else)
	}
}

// --- Expressions ---

func (s *stripper) stripExpr(e Expr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Literal:
		e.AST = nil
		e.Type = nil
	case *Ident:
		e.AST = nil
		e.Sym = nil // cross-reference
		e.Type = nil
	case *Binary:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Left)
		s.stripExpr(e.Right)
	case *Unary:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Operand)
	case *Ternary:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Cond)
		s.stripExpr(e.Then)
		s.stripExpr(e.Else)
	case *Call:
		e.AST = nil
		e.Func = nil     // cross-reference
		e.Receiver = nil // resolution artifact
		e.Type = nil
		for i := range e.Args {
			s.stripExpr(e.Args[i].Value)
		}
	case *Conversion:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Operand)
	case *Select:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Operand)
	case *Index:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Operand)
		s.stripExpr(e.Idx)
	case *StructLit:
		e.AST = nil
		e.Def = nil // cross-reference
		e.Type = nil
		for i := range e.Fields {
			e.Fields[i].NamePos = ast.Pos{}
			s.stripExpr(e.Fields[i].Value)
		}
	case *ListLit:
		e.AST = nil
		e.Type = nil
		for _, el := range e.Elems {
			s.stripExpr(el)
		}
	case *Spread:
		e.AST = nil
		e.Type = nil
		s.stripExpr(e.Operand)
	case *Lambda:
		e.AST = nil
		e.Type = nil
		s.stripFunc(e.Func)
	}
}
