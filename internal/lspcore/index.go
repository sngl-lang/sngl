package lspcore

import (
	"io/fs"
	"slices"
	"strings"
	"unicode"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// SymbolKind is what a name refers to, as an editor colours it.
type SymbolKind int

const (
	KindVariable SymbolKind = iota
	KindConstant
	KindParameter
	KindFunction
	KindMethod
	KindComponent
	KindType
	KindTypeParameter
	KindField
	KindEnumMember
	KindEvent
	KindNamespace
	KindElement // a node's `#id`
)

// A Ref is a name written in one of the package's files and the declaration
// it names, which is its identity: the declaration's own pointer, as one
// check holds it. A declaration's name is a Ref to itself.
type Ref struct {
	Pos    ast.Pos
	Len    int // in runes, as ast.Pos counts columns
	Decl   any
	Kind   SymbolKind
	IsDecl bool
}

// Site is where a declaration's name is written. A library declaration's File
// is relative to lib.FS.
type Site struct {
	Pos ast.Pos
	Len int
}

// Index is every name a checked package writes in the files it was checked
// from, and where each declaration they name is written.
type Index struct {
	Refs  []Ref
	Sites map[any]Site
	// Library holds the decls whose site is in lib.FS.
	Library map[any]bool
	// Shorthands are the positions of `{a}`, which names a field and the
	// value it takes at once, and so has a Ref to each.
	Shorthands map[ast.Pos]bool

	src Sources
}

// Sources reads a file a position names: one of the package's, or a library
// file, relative to lib.FS.
type Sources func(file string) (string, bool)

// LibrarySource reads a library file from the embedded library.
func LibrarySource(file string) (string, bool) {
	b, err := fs.ReadFile(lib.FS, file)
	return string(b), err == nil
}

// At is the name written at line:col of file, both 1-based, col in runes.
func (ix *Index) At(file string, line, col int) *Ref {
	for i := range ix.Refs {
		r := &ix.Refs[i]
		if r.Pos.File == file && r.Pos.Line == line && col >= r.Pos.Column && col < r.Pos.Column+r.Len {
			return r
		}
	}
	return nil
}

// RefsTo is every name in the package that names decl, its declaration's
// own included.
func (ix *Index) RefsTo(decl any) []Ref {
	var out []Ref
	for _, r := range ix.Refs {
		if r.Decl == decl {
			out = append(out, r)
		}
	}
	return out
}

// text is the name r covers, as its file writes it.
func (ix *Index) text(r Ref) string {
	l := ix.line(r.Pos.File, r.Pos.Line)
	if r.Pos.Column-1+r.Len > len(l) {
		return ""
	}
	return string(l[r.Pos.Column-1 : r.Pos.Column-1+r.Len])
}

func (ix *Index) line(file string, n int) []rune {
	text, ok := ix.src(file)
	if !ok {
		return nil
	}
	lines := strings.Split(text, "\n")
	if n < 1 || n > len(lines) {
		return nil
	}
	return []rune(lines[n-1])
}

// InFile is the names written in file, in source order.
func (ix *Index) InFile(file string) []Ref {
	var out []Ref
	for _, r := range ix.Refs {
		if r.Pos.File == file {
			out = append(out, r)
		}
	}
	return out
}

// BuildIndex indexes pkg, the checked package -- never a lowered or cloned
// one, whose declarations are copies -- over the files in files. src reads
// those files and the library's, since most declarations record where their
// keyword is written and their name is found after it.
func BuildIndex(pkg *ir.Package, files []string, src Sources) *Index {
	x := &indexer{
		pkg:    pkg,
		files:  map[string]bool{},
		src:    src,
		lines:  map[string][]string{},
		seen:   map[ast.Pos]bool{},
		propOf: map[*ir.Param]*ir.Prop{},
		owners: map[any]bool{},
		ix:     &Index{Sites: map[any]Site{}, Library: map[any]bool{}, Shorthands: map[ast.Pos]bool{}, src: src},
	}
	for _, f := range files {
		x.files[f] = true
	}
	if pkg == nil {
		return x.ix
	}
	for _, c := range pkg.Components {
		for _, p := range c.Props {
			if p.Sym != nil {
				x.propOf[p.Sym] = p
			}
		}
	}
	x.declarations()
	_ = ir.Walk(pkg, x.visit)
	// An emit names an event of the component it is written in, which only a
	// walk of that component knows.
	for _, c := range pkg.Components {
		_ = ir.Walk(c, func(n ir.Node) error {
			if e, ok := n.(*ir.Emit); ok && e.AST != nil && e.AST.Call != nil {
				if id, ok := e.AST.Call.Func.(*ast.IdentExpr); ok {
					for _, ev := range c.Events {
						if ev.Name == e.Name {
							x.named(id.Pos, id.Name, ev, KindEvent)
						}
					}
				}
			}
			return nil
		})
	}
	slices.SortFunc(x.ix.Refs, func(a, b Ref) int {
		if c := strings.Compare(a.Pos.File, b.Pos.File); c != 0 {
			return c
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}
		return a.Pos.Column - b.Pos.Column
	})
	return x.ix
}

type indexer struct {
	pkg    *ir.Package
	files  map[string]bool
	src    Sources
	lines  map[string][]string
	seen   map[ast.Pos]bool
	propOf map[*ir.Param]*ir.Prop
	owners map[any]bool
	ix     *Index
}

func (x *indexer) line(file string, n int) []rune {
	ls, ok := x.lines[file]
	if !ok {
		if text, found := x.src(file); found {
			ls = strings.Split(text, "\n")
		}
		x.lines[file] = ls
	}
	if n < 1 || n > len(ls) {
		return nil
	}
	return []rune(ls[n-1])
}

// written reports whether name is what the source has at pos: a position the
// checker synthesized -- an elided `this`, a lowered helper -- is not.
func (x *indexer) written(pos ast.Pos, name string) bool {
	l := x.line(pos.File, pos.Line)
	start := pos.Column - 1
	n := []rune(name)
	if start < 0 || start+len(n) > len(l) || string(l[start:start+len(n)]) != name {
		return false
	}
	end := start + len(n)
	return end == len(l) || !isNameRune(l[end])
}

func isNameRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// find is the first whole-word name at or after from, on its line or the two
// after it: where a declaration whose position is its keyword writes its
// name, or a select whose position is its dot writes its member.
func (x *indexer) find(from ast.Pos, name string) (ast.Pos, bool) {
	if name == "" || !from.IsValid() {
		return ast.Pos{}, false
	}
	n := []rune(name)
	for dl := range 3 {
		l := x.line(from.File, from.Line+dl)
		start := 0
		if dl == 0 {
			start = from.Column - 1
		}
		for i := max(start, 0); i+len(n) <= len(l); i++ {
			if string(l[i:i+len(n)]) != name || (i > 0 && isNameRune(l[i-1])) || (i+len(n) < len(l) && isNameRune(l[i+len(n)])) {
				continue
			}
			return ast.Pos{File: from.File, Line: from.Line + dl, Column: i + 1}, true
		}
	}
	return ast.Pos{}, false
}

func (x *indexer) add(pos ast.Pos, name string, decl any, kind SymbolKind, isDecl bool) {
	if decl == nil || !x.files[pos.File] || x.seen[pos] || !x.written(pos, name) {
		return
	}
	x.seen[pos] = true
	x.ix.Refs = append(x.ix.Refs, Ref{Pos: pos, Len: len([]rune(name)), Decl: decl, Kind: kind, IsDecl: isDecl})
	if isDecl {
		x.site(decl, pos, name)
	}
}

func (x *indexer) site(decl any, pos ast.Pos, name string) {
	if _, ok := x.ix.Sites[decl]; ok || !pos.IsValid() || !x.written(pos, name) {
		return
	}
	x.ix.Sites[decl] = Site{Pos: pos, Len: len([]rune(name))}
	if !x.files[pos.File] {
		x.ix.Library[decl] = true
	}
}

// named records a use of decl at pos, and where decl's own name is written,
// which a library declaration has nowhere else to say.
func (x *indexer) named(pos ast.Pos, name string, decl any, kind SymbolKind) {
	if decl == nil {
		return
	}
	x.declSite(decl)
	x.add(pos, name, decl, kind, false)
}

// declSite records where decl's name is written, for the declarations that
// carry their own AST.
func (x *indexer) declSite(decl any) {
	if _, ok := x.ix.Sites[decl]; ok {
		return
	}
	switch d := decl.(type) {
	case *ir.Func:
		if d.AST != nil {
			if p, ok := x.funcName(d.AST); ok {
				x.site(d, p, d.Name)
			}
		}
	case *ir.Component:
		if d.AST != nil {
			if p, ok := x.afterKeyword(d.AST.Pos, "component", lastSegment(d.AST.Name)); ok {
				x.site(d, p, lastSegment(d.AST.Name))
			}
		}
	case *ir.StructDef:
		if d.AST != nil && !d.Anon {
			if p, ok := x.afterKeyword(d.AST.Pos, "struct", d.AST.Name); ok {
				x.site(d, p, d.AST.Name)
			}
		}
	case *ir.EnumDef:
		if d.AST != nil {
			if p, ok := x.afterKeyword(d.AST.Pos, "enum", d.AST.Name); ok {
				x.site(d, p, d.AST.Name)
			}
		}
	case *ir.UnitDef:
		if d.AST != nil {
			if p, ok := x.afterKeyword(d.AST.Pos, "unit", d.AST.Name); ok {
				x.site(d, p, d.AST.Name)
			}
		}
	case *ir.Var:
		for _, spec := range varSpecs(d.AST) {
			for i, n := range spec.Names {
				if n == d.Name && i < len(spec.NamePositions) {
					x.site(d, spec.NamePositions[i], n)
				}
			}
		}
	}
}

// afterKeyword is where a declaration whose position is its keyword -- or
// a mark written before it -- writes its name.
func (x *indexer) afterKeyword(from ast.Pos, keyword, name string) (ast.Pos, bool) {
	kw, ok := x.find(from, keyword)
	if !ok {
		return ast.Pos{}, false
	}
	return x.find(ast.Pos{File: kw.File, Line: kw.Line, Column: kw.Column + len(keyword)}, name)
}

// members records where the members of owner are written: a prop, field,
// enum member or parameter has no AST of its own, so a library one's site is
// reachable only through the declaration that holds it.
func (x *indexer) members(owner any) {
	if owner == nil || x.owners[owner] {
		return
	}
	x.owners[owner] = true
	x.declSite(owner)
	switch d := owner.(type) {
	case *ir.Component:
		if d.AST == nil {
			return
		}
		for _, item := range d.AST.Props.Props {
			switch a := item.(type) {
			case ast.Param:
				for _, p := range d.Props {
					if p.Name == a.Name {
						x.site(p, a.Pos, a.Name)
					}
				}
			case ast.EventDecl:
				for _, e := range d.Events {
					if e.Name == a.Name {
						x.site(e, a.Pos, a.Name)
					}
				}
			}
		}
		for _, f := range d.Funcs {
			x.declSite(f)
		}
		for _, v := range d.Vars {
			x.declSite(v)
		}
	case *ir.StructDef:
		if d.AST == nil {
			return
		}
		for _, f := range d.AST.Fields() {
			for i, n := range f.Names {
				if i < len(f.NamePositions) {
					if sf := fieldOf(d.Fields, n); sf != nil {
						x.site(sf, f.NamePositions[i], n)
					}
				}
			}
		}
		for _, m := range d.Methods {
			x.declSite(m)
		}
	case *ir.EnumDef:
		if d.AST == nil {
			return
		}
		for _, m := range d.AST.Members() {
			if em := memberOf(d, m.Name); em != nil {
				x.site(em, m.Pos, m.Name)
			}
		}
		for _, m := range d.Methods {
			x.declSite(m)
		}
	case *ir.Func:
		if d.AST == nil {
			return
		}
		for _, a := range d.AST.Params.Params {
			for _, p := range d.Params {
				if p.Name == a.Name && !p.Receiver {
					x.site(x.norm(p), a.Pos, a.Name)
				}
			}
		}
	}
}

// funcName is where a func's name is written: after the receiver and its
// type parameters for a method, `func list[T].map`, whose receiver may share
// the method's name. A method written inside its type's body is named
// `Point.sum` and written `func sum`.
func (x *indexer) funcName(f *ast.FuncDef) (ast.Pos, bool) {
	kw, ok := x.find(f.Pos, "func")
	if !ok {
		return ast.Pos{}, false
	}
	from := ast.Pos{File: kw.File, Line: kw.Line, Column: kw.Column + len("func")}
	recv, name, isMethod := strings.Cut(f.Name, ".")
	if !isMethod {
		return x.find(from, f.Name)
	}
	p, ok := x.find(from, recv)
	if !ok || p.Line != kw.Line {
		return x.find(from, lastSegment(name))
	}
	l := x.line(p.File, p.Line)
	i := p.Column - 1 + len([]rune(recv))
	for i < len(l) && l[i] != '.' {
		i++
	}
	return x.find(ast.Pos{File: p.File, Line: p.Line, Column: i + 1}, lastSegment(name))
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func varSpecs(st ast.Stmt) []ast.VarSpec {
	switch d := st.(type) {
	case *ast.VarDecl:
		return d.Specs
	case *ast.ConstDecl:
		return d.Specs
	}
	return nil
}

// norm is the declaration a symbol stands for: a component's prop is read in
// its body through a Param, and named at a call site as the prop.
func (x *indexer) norm(sym any) any {
	if p, ok := sym.(*ir.Param); ok {
		if prop := x.propOf[p]; prop != nil {
			return prop
		}
	}
	return sym
}

func kindOf(decl any) SymbolKind {
	switch d := decl.(type) {
	case *ir.Func:
		if d.Receiver != nil {
			return KindMethod
		}
		return KindFunction
	case *ir.Component:
		return KindComponent
	case *ir.StructDef, *ir.EnumDef, *ir.UnitDef, *ir.TypeSym:
		return KindType
	case *ir.StructField, *ir.Prop:
		return KindField
	case *ir.EnumMember:
		return KindEnumMember
	case *ir.EventDecl:
		return KindEvent
	case *ir.Param:
		return KindParameter
	case *ir.Import:
		return KindNamespace
	case *ir.Var:
		if d.NodeHandle {
			return KindElement
		}
		if d.IsConst {
			return KindConstant
		}
	}
	return KindVariable
}

// declarations indexes every name the package's own declarations write.
func (x *indexer) declarations() {
	p := x.pkg
	for _, imp := range p.Imports {
		x.importAlias(imp)
	}
	for _, f := range p.Funcs {
		x.funcDecl(f)
	}
	for _, c := range p.Components {
		x.componentDecl(c)
	}
	for _, sd := range p.Structs {
		x.structDecl(sd)
	}
	for _, ed := range p.Enums {
		x.enumDecl(ed)
	}
	for _, ud := range p.Units {
		if ud.AST != nil {
			x.declSite(ud)
			x.add(x.ix.Sites[ud].Pos, ud.AST.Name, ud, KindType, true)
		}
	}
	for _, v := range slices.Concat(p.Consts, p.Vars) {
		x.varDecl(v)
	}
}

func (x *indexer) importAlias(imp *ir.Import) {
	if imp.AST == nil || imp.AST.Alias == "" || imp.AST.Alias == "." {
		return
	}
	if p, ok := x.find(imp.AST.Pos, imp.AST.Alias); ok {
		x.add(p, imp.AST.Alias, imp, KindNamespace, true)
	}
}

// importNamed is the import file reads a namespace through.
func (x *indexer) importNamed(file, alias string) *ir.Import {
	for _, imp := range x.pkg.Imports {
		if imp.Alias == alias && imp.AST != nil && imp.AST.Pos.File == file {
			return imp
		}
	}
	return nil
}

func (x *indexer) namespace(id *ast.IdentExpr) {
	if id == nil {
		return
	}
	x.add(id.Pos, id.Name, x.importNamed(id.Pos.File, id.Name), KindNamespace, false)
}

func (x *indexer) varDecl(v *ir.Var) {
	if v == nil || v.Synthesized {
		return
	}
	for _, spec := range varSpecs(v.AST) {
		for i, n := range spec.Names {
			if n == v.Name && i < len(spec.NamePositions) {
				x.add(spec.NamePositions[i], n, v, kindOf(v), true)
			}
		}
		x.typeRefs(spec.Type)
	}
}

func (x *indexer) funcDecl(f *ir.Func) {
	if f == nil || f.AST == nil {
		return
	}
	if p, ok := x.funcName(f.AST); ok {
		x.add(p, f.Name, f, kindOf(f), true)
	}
	x.params(f.Params, f.AST.Params.Params)
	x.typeParams(f.AST.TypeParams)
	x.typeRefs(f.AST.ReturnType)
}

// params indexes a parameter list's names. A nested method's synthetic
// receiver shifts the IR's list against the AST's, so they meet by name.
func (x *indexer) params(ps []*ir.Param, written []ast.Param) {
	for _, a := range written {
		x.typeRefs(a.Type)
		for _, p := range ps {
			if p.Name == a.Name && !p.Receiver {
				x.add(a.Pos, a.Name, x.norm(p), KindParameter, true)
				break
			}
		}
	}
}

func (x *indexer) typeParams(tps []ast.TypeParam) {
	for _, tp := range tps {
		x.add(tp.Pos, tp.Name, tp.Pos, KindTypeParameter, true)
	}
}

func (x *indexer) componentDecl(c *ir.Component) {
	if c == nil || c.AST == nil {
		return
	}
	x.declSite(c)
	if s, ok := x.ix.Sites[c]; ok {
		x.add(s.Pos, lastSegment(c.AST.Name), c, KindComponent, true)
	}
	x.typeParams(c.AST.TypeParams)
	for _, item := range c.AST.Props.Props {
		switch a := item.(type) {
		case ast.Param:
			x.typeRefs(a.Type)
			for _, p := range c.Props {
				if p.Name == a.Name {
					x.add(a.Pos, a.Name, p, KindField, true)
				}
			}
		case ast.EventDecl:
			for _, e := range c.Events {
				if e.Name == a.Name {
					x.add(a.Pos, a.Name, e, KindEvent, true)
				}
			}
		}
	}
	x.typeRefs(c.AST.ChildrenType)
	for _, v := range c.Vars {
		x.varDecl(v)
	}
}

func (x *indexer) structDecl(sd *ir.StructDef) {
	if sd == nil || sd.AST == nil || sd.Anon {
		return
	}
	x.declSite(sd)
	if s, ok := x.ix.Sites[sd]; ok {
		x.add(s.Pos, sd.AST.Name, sd, KindType, true)
	}
	x.typeParams(sd.AST.TypeParams)
	for _, m := range sd.Methods {
		x.funcDecl(m)
	}
	for _, f := range sd.AST.Fields() {
		x.typeRefs(f.Type)
		for i, n := range f.Names {
			if i < len(f.NamePositions) {
				x.add(f.NamePositions[i], n, fieldOf(sd.Fields, n), KindField, true)
			}
		}
	}
}

func (x *indexer) enumDecl(ed *ir.EnumDef) {
	if ed == nil || ed.AST == nil {
		return
	}
	x.declSite(ed)
	if s, ok := x.ix.Sites[ed]; ok {
		x.add(s.Pos, ed.AST.Name, ed, KindType, true)
	}
	x.typeParams(ed.AST.TypeParams)
	for _, m := range ed.Methods {
		x.funcDecl(m)
	}
	for _, m := range ed.AST.Members() {
		x.typeRefs(m.Payload)
		x.add(m.Pos, m.Name, memberOf(ed, m.Name), KindEnumMember, true)
	}
}

func fieldOf(fields []*ir.StructField, name string) *ir.StructField {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func memberOf(ed *ir.EnumDef, name string) *ir.EnumMember {
	for _, m := range ed.Members {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// typeRefs indexes the type names a written type names. The IR keeps no
// position for a type annotation, so they are read off the AST and resolved
// the way the checker resolves them.
func (x *indexer) typeRefs(te ast.TypeExpr) {
	switch t := te.(type) {
	case *ast.NamedType:
		if t == nil {
			return
		}
		for _, a := range t.TypeArgs {
			x.typeRefs(a)
		}
		var sym ir.Symbol
		namePos := t.Pos
		if t.Package != "" {
			imp := x.importNamed(t.Pos.File, t.Package)
			x.add(t.Pos, t.Package, imp, KindNamespace, false)
			if imp == nil || imp.Pkg == nil {
				return
			}
			sym, _ = imp.Pkg.Symbols.LookupMember(t.Name)
			p, ok := x.find(ast.Pos{File: t.Pos.File, Line: t.Pos.Line, Column: t.Pos.Column + len([]rune(t.Package))}, t.Name)
			if !ok {
				return
			}
			namePos = p
		} else if x.pkg.Symbols != nil {
			sym, _ = x.pkg.Symbols.Root.Lookup(t.Name)
		}
		switch sym.(type) {
		case *ir.StructDef, *ir.EnumDef, *ir.UnitDef, *ir.Component:
			x.named(namePos, t.Name, sym, kindOf(sym))
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			x.typeRefs(p.Type)
		}
		x.typeRefs(t.Return)
	case *ast.ComponentType:
		for _, p := range t.Params {
			x.typeRefs(p.Type)
		}
		x.typeRefs(t.Tree)
	case *ast.VariadicType:
		x.typeRefs(t.Elem)
	}
}

// visit indexes the names written in expressions and statements.
func (x *indexer) visit(n ir.Node) error {
	switch v := n.(type) {
	case *ir.Ident:
		x.ident(v)
	case *ir.Call:
		x.call(v)
	case *ir.Select:
		x.selectExpr(v)
	case *ir.StructLit:
		x.structLit(v)
	case *ir.NodeInst:
		x.nodeInst(v)
	case *ir.Lambda:
		if v.AST != nil && v.Func != nil {
			x.params(v.Func.Params, v.AST.Params.Params)
		}
	case *ir.For:
		x.loopVars(v)
	case *ir.LocalVar:
		if v.Sym != nil {
			x.varDecl(v.Sym)
		}
	}
	return nil
}

func (x *indexer) ident(v *ir.Ident) {
	if v.Synthesized {
		return
	}
	if v.Qualifier != nil && v.SelAST != nil {
		if id, ok := v.SelAST.Operand.(*ast.IdentExpr); ok {
			x.namespace(id)
		}
		if p, ok := x.find(v.SelAST.Pos, v.Name); ok {
			x.named(p, v.Name, x.norm(v.Sym), kindOf(x.norm(v.Sym)))
		}
		return
	}
	if ed, ok := v.Sym.(*ir.EnumDef); ok && v.Member != "" && v.SelAST != nil {
		x.members(ed)
		if id, ok := v.SelAST.Operand.(*ast.IdentExpr); ok {
			x.named(id.Pos, id.Name, ed, KindType)
		}
		if p, ok := x.find(v.SelAST.Pos, v.Member); ok {
			x.named(p, v.Member, memberOf(ed, v.Member), KindEnumMember)
		}
		return
	}
	if v.AST == nil {
		return
	}
	if v.Sym == nil && v.Member != "" && v.Type != nil {
		if ed, ok := v.Type.Decl.(*ir.EnumDef); ok {
			x.members(ed)
			x.named(v.AST.Pos, v.AST.Name, memberOf(ed, v.Member), KindEnumMember)
		}
		return
	}
	switch v.Sym.(type) {
	case *ir.Namespace, nil:
		return
	}
	d := x.norm(v.Sym)
	x.named(v.AST.Pos, v.AST.Name, d, kindOf(d))
}

// calleeName is the name a call's callee is written as, and the namespace
// identifier it is read through, if any.
func (x *indexer) calleeName(e ast.Expr, name string) (ast.Pos, *ast.IdentExpr, bool) {
	if ix, ok := e.(*ast.IndexExpr); ok {
		e = ix.Operand
	}
	switch c := e.(type) {
	case *ast.IdentExpr:
		return c.Pos, nil, true
	case *ast.SelectExpr:
		p, ok := x.find(c.Pos, name)
		id, _ := c.Operand.(*ast.IdentExpr)
		return p, id, ok
	}
	return ast.Pos{}, nil, false
}

func (x *indexer) call(v *ir.Call) {
	if v.AST == nil || v.Func == nil {
		return
	}
	p, qual, ok := x.calleeName(v.AST.Func, v.Func.Name)
	if !ok {
		return
	}
	if v.Qualifier != nil {
		x.namespace(qual)
	}
	x.named(p, v.Func.Name, v.Func, kindOf(v.Func))
	x.members(v.Func)
	for _, a := range v.AST.Args.Args {
		arg, isArg := a.(ast.Arg)
		if !isArg || arg.Name == "" || !arg.NamePos.IsSet() {
			continue
		}
		for _, param := range v.Func.Params {
			if param.Name == arg.Name {
				x.named(arg.NamePos, arg.Name, param, KindParameter)
			}
		}
	}
}

func (x *indexer) selectExpr(v *ir.Select) {
	if v.AST == nil || v.Operand == nil {
		return
	}
	pos := v.AST.Pos
	// An elided `this` is a select the checker wrote at the member's own
	// position.
	if id, ok := v.Operand.(*ir.Ident); !ok || id.AST == nil || id.AST.Pos != pos {
		p, found := x.find(pos, v.Field)
		if !found {
			return
		}
		pos = p
	}
	owner, d := memberDecl(v.Operand.ExprType(), v.Field)
	x.members(owner)
	x.named(pos, v.Field, d, kindOf(d))
}

// memberDecl is the declaration a member read through a value of type t
// names, and the declaration that holds it.
func memberDecl(t *ir.Type, name string) (owner, decl any) {
	for t != nil && (t.Kind == ir.TypeRef || t.Kind == ir.TypeOption || t.Kind == ir.TypeReadonly) && len(t.Elems) > 0 {
		t = t.Elems[0]
	}
	if t == nil {
		return nil, nil
	}
	switch d := t.Decl.(type) {
	case *ir.StructDef:
		if f := fieldOf(d.Fields, name); f != nil {
			return d, f
		}
	case *ir.UnitDef:
		if f := fieldOf(d.Fields, name); f != nil {
			return d, f
		}
	case *ir.Component:
		for _, p := range d.Props {
			if p.Name == name {
				return d, p
			}
		}
		for _, v := range d.Vars {
			if v.Name == name {
				return d, v
			}
		}
		for _, f := range d.Funcs {
			if f.Name == name {
				return d, f
			}
		}
		for _, e := range d.Events {
			if e.Name == name {
				return d, e
			}
		}
	}
	return nil, nil
}

func (x *indexer) structLit(v *ir.StructLit) {
	if v.AST == nil {
		return
	}
	if v.Def != nil && !v.Def.Anon && v.AST.Name != "" {
		namePos := v.AST.Pos
		if v.AST.Package != "" {
			x.add(v.AST.Pos, v.AST.Package, x.importNamed(v.AST.Pos.File, v.AST.Package), KindNamespace, false)
			p, ok := x.find(ast.Pos{File: namePos.File, Line: namePos.Line, Column: namePos.Column + len([]rune(v.AST.Package))}, v.AST.Name)
			if !ok {
				return
			}
			namePos = p
		}
		x.named(namePos, v.AST.Name, v.Def, KindType)
	}
	if v.Def == nil {
		return
	}
	x.members(v.Def)
	for _, f := range v.Fields {
		if f.Name == "" || !f.NamePos.IsSet() {
			continue
		}
		field := fieldOf(v.Def.Fields, f.Name)
		id, short := f.Value.(*ir.Ident)
		if !short || id.AST == nil || id.AST.Pos != f.NamePos || field == nil || id.Sym == nil {
			x.named(f.NamePos, f.Name, field, KindField)
			continue
		}
		// The value's Ident is at the same position, which a Ref takes once.
		value := x.norm(id.Sym)
		x.declSite(field)
		x.declSite(value)
		delete(x.seen, f.NamePos)
		x.add(f.NamePos, f.Name, field, KindField, false)
		if x.seen[f.NamePos] {
			x.ix.Refs = append(x.ix.Refs, Ref{Pos: f.NamePos, Len: len([]rune(f.Name)), Decl: value, Kind: kindOf(value)})
			x.ix.Shorthands[f.NamePos] = true
		}
	}
}

func (x *indexer) nodeInst(v *ir.NodeInst) {
	vn, ok := v.AST.(*ast.VisualNode)
	if !ok || vn == nil || v.Site != nil {
		return
	}
	c := v.Component
	name := v.Name
	if c != nil {
		name = c.Name
	}
	p, qual, found := x.calleeName(vn.Target, name)
	if found && c != nil {
		x.namespace(qual)
		x.named(p, name, c, KindComponent)
	}
	if c == nil {
		return
	}
	x.members(c)
	prop := func(n string) *ir.Prop {
		for _, p := range c.Props {
			if p.Name == n {
				return p
			}
		}
		return nil
	}
	for _, a := range v.Props {
		if a.Name != "" && a.NamePos.IsSet() {
			x.named(a.NamePos, a.Name, prop(a.Name), KindField)
		}
	}
	for _, b := range v.Bindings {
		x.named(b.NamePos, b.PropName, prop(b.PropName), KindField)
	}
	for _, h := range v.Handlers {
		if h.AST == nil {
			continue
		}
		for _, e := range c.Events {
			if e.Name == h.Name {
				x.named(h.AST.Pos, h.Name, e, KindEvent)
			}
		}
	}
	if v.ID != "" && v.Handle != nil && found {
		// `#id` records no position: it is written after the node's name.
		l := x.line(p.File, p.Line)
		for i := p.Column - 1; i+1 < len(l); i++ {
			if l[i] == '#' {
				x.add(ast.Pos{File: p.File, Line: p.Line, Column: i + 2}, v.ID, v.Handle, KindElement, true)
				break
			}
		}
	}
}

// loopVars indexes the names a loop declares, which record no position:
// `for var [&]key[, [&]value] =`.
func (x *indexer) loopVars(v *ir.For) {
	if v.AST == nil {
		return
	}
	from := v.AST.Pos
	for _, lv := range []*ir.LoopVar{v.KeySym, v.ValueSym} {
		if lv == nil {
			continue
		}
		p, ok := x.find(ast.Pos{File: from.File, Line: from.Line, Column: from.Column + len("for")}, lv.Name)
		if !ok {
			return
		}
		x.add(p, lv.Name, lv, KindVariable, true)
		from = ast.Pos{File: p.File, Line: p.Line, Column: p.Column + len([]rune(lv.Name)) - len("for")}
	}
}
