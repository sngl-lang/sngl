package lspcore

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// LSP SymbolKind values.
const (
	symClass      = 5
	symMethod     = 6
	symProperty   = 7
	symField      = 8
	symEnum       = 10
	symFunction   = 12
	symVariable   = 13
	symConstant   = 14
	symObject     = 19
	symEnumMember = 22
	symStruct     = 23
	symEvent      = 24
)

// DocSymbol is one entry of a file's outline: a declaration, the whole of it
// in Range and its name in Selection, in the UTF-16 columns LSP counts.
type DocSymbol struct {
	Name      string
	Detail    string
	Kind      int
	Range     Range
	Selection Range
	Children  []DocSymbol
}

// DocumentSymbols outlines the declarations pkg, the checked package, has in
// file, and the window and other nodes written at its top level. ix supplies
// where each name is written. A symbol is nested in the one whose source holds
// it, so a method is under its type where it is written there and at the top
// level where it is written `func Point.len`, which the IR does not tell apart.
func DocumentSymbols(pkg *ir.Package, ix *Index, file, content string) []DocSymbol {
	if pkg == nil || ix == nil {
		return nil
	}
	toks, _ := parser.Tokenize(content)
	o := &outliner{ix: ix, file: file, lines: strings.Split(content, "\n"), toks: toks, seen: map[any]bool{}}

	for _, v := range slices.Concat(pkg.Consts, pkg.Vars) {
		o.variable(v)
	}
	for _, f := range pkg.Funcs {
		o.fn(f)
	}
	for _, c := range pkg.Components {
		o.component(c)
	}
	for _, sd := range pkg.Structs {
		if sd.AST != nil && !sd.Anon {
			o.decl(sd, sd.AST.Pos, symStruct, "")
			for _, f := range sd.Fields {
				o.member(f, symField, typeDetail(f.Type))
			}
			for _, m := range sd.Methods {
				o.fn(m)
			}
		}
	}
	for _, ed := range pkg.Enums {
		if ed.AST != nil {
			o.decl(ed, ed.AST.Pos, symEnum, "")
			for _, m := range ed.Members {
				o.member(m, symEnumMember, typeDetail(m.Payload))
			}
			for _, m := range ed.Methods {
				o.fn(m)
			}
		}
	}
	for _, ud := range pkg.Units {
		if ud.AST != nil {
			o.decl(ud, ud.AST.Pos, symStruct, "")
		}
	}
	for _, st := range pkg.Body {
		o.node(st)
	}
	return nest(o.flat)
}

// nest puts each symbol in the innermost one before it whose range holds it.
func nest(flat []DocSymbol) []DocSymbol {
	slices.SortStableFunc(flat, func(a, b DocSymbol) int {
		if c := comparePos(a.Range.Start, b.Range.Start); c != 0 {
			return c
		}
		return comparePos(b.Range.End, a.Range.End)
	})
	var root []DocSymbol
	var place func(list *[]DocSymbol, s DocSymbol)
	place = func(list *[]DocSymbol, s DocSymbol) {
		if n := len(*list); n > 0 {
			last := &(*list)[n-1]
			if comparePos(last.Range.Start, s.Range.Start) <= 0 && comparePos(s.Range.End, last.Range.End) <= 0 {
				place(&last.Children, s)
				return
			}
		}
		*list = append(*list, s)
	}
	for _, s := range flat {
		place(&root, s)
	}
	return root
}

func comparePos(a, b Position) int {
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return a.Character - b.Character
}

type outliner struct {
	ix    *Index
	file  string
	lines []string
	toks  []parser.Token
	seen  map[any]bool
	flat  []DocSymbol
}

func (o *outliner) position(p ast.Pos) Position {
	line := ""
	if p.Line-1 < len(o.lines) && p.Line > 0 {
		line = o.lines[p.Line-1]
	}
	return Position{Line: p.Line - 1, Character: UTF16Column(line, p.Column-1)}
}

// end is where what starts at from stops: the first `;` -- a line break is
// one -- or `,` outside the brackets it opens, or the bracket closing the list
// it is in. A token's own width is not kept, so a declaration ends where its
// terminator begins, which is just after its last token.
func (o *outliner) end(from ast.Pos) ast.Pos {
	depth := 0
	for _, t := range o.toks {
		if t.Line < from.Line || t.Line == from.Line && t.Column <= from.Column {
			continue
		}
		switch t.Type {
		case parser.LPAREN, parser.LBRACE, parser.LBRACKET:
			depth++
		case parser.RPAREN, parser.RBRACE, parser.RBRACKET:
			depth--
			if depth < 0 {
				return ast.Pos{File: from.File, Line: t.Line, Column: t.Column}
			}
		case parser.SEMICOLON, parser.COMMA, parser.EOF:
			if depth == 0 {
				return ast.Pos{File: from.File, Line: t.Line, Column: t.Column}
			}
		}
	}
	return from
}

// decl adds the symbol for decl, whose source starts at start -- its
// keyword, or a mark written before it -- and whose name the index places. A
// declaration whose name is in another file is not this file's.
func (o *outliner) decl(decl any, start ast.Pos, kind int, detail string) {
	site, ok := o.ix.Sites[decl]
	if !ok || o.seen[decl] || site.Pos.File != o.file || start.File != o.file {
		return
	}
	o.seen[decl] = true
	name := string([]rune(o.lines[site.Pos.Line-1])[site.Pos.Column-1 : site.Pos.Column-1+site.Len])
	sel := ast.Pos{File: o.file, Line: site.Pos.Line, Column: site.Pos.Column + site.Len}
	o.flat = append(o.flat, DocSymbol{
		Name:      name,
		Detail:    detail,
		Kind:      kind,
		Range:     Range{Start: o.position(start), End: o.position(o.end(site.Pos))},
		Selection: Range{Start: o.position(site.Pos), End: o.position(sel)},
	})
}

// member adds a declaration that starts at its name: a field, a prop, an
// enum member, a const or var, which a group writes several of under one
// keyword.
func (o *outliner) member(decl any, kind int, detail string) {
	if site, ok := o.ix.Sites[decl]; ok {
		o.decl(decl, site.Pos, kind, detail)
	}
}

func typeDetail(t *ir.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

func (o *outliner) variable(v *ir.Var) {
	if v == nil || v.Synthesized || v.NodeHandle {
		return
	}
	kind := symVariable
	if v.IsConst {
		kind = symConstant
	}
	o.member(v, kind, typeDetail(v.Type))
}

func (o *outliner) fn(f *ir.Func) {
	if f == nil || f.AST == nil {
		return
	}
	kind := symFunction
	if f.Receiver != nil {
		kind = symMethod
	}
	o.decl(f, f.AST.Pos, kind, "")
}

func (o *outliner) component(c *ir.Component) {
	if c == nil || c.AST == nil {
		return
	}
	o.decl(c, c.AST.Pos, symClass, "")
	for _, p := range c.Props {
		o.member(p, symProperty, typeDetail(p.Type))
	}
	for _, e := range c.Events {
		o.member(e, symEvent, "")
	}
	for _, v := range c.Vars {
		o.variable(v)
	}
	for _, f := range c.Funcs {
		o.fn(f)
	}
}

// node adds a node written at the top level, `ui.window #main`, named as it
// is written.
func (o *outliner) node(st ir.Stmt) {
	n, ok := st.(*ir.NodeInst)
	if !ok {
		return
	}
	vn, ok := n.AST.(*ast.VisualNode)
	if !ok || vn == nil || n.Site != nil {
		return
	}
	var start ast.Pos
	switch t := vn.Target.(type) {
	case *ast.IdentExpr:
		start = t.Pos
	case *ast.SelectExpr:
		id, ok := t.Operand.(*ast.IdentExpr)
		if !ok {
			return
		}
		start = id.Pos
	default:
		return
	}
	if start.File != o.file || start.Line-1 >= len(o.lines) {
		return
	}
	l := []rune(o.lines[start.Line-1])
	i := start.Column - 1
	for i < len(l) && (isNameRune(l[i]) || l[i] == '.') {
		i++
	}
	name := string(l[start.Column-1 : i])
	if vn.ID != "" {
		name += " #" + vn.ID
	}
	sel := ast.Pos{File: o.file, Line: start.Line, Column: i + 1}
	o.flat = append(o.flat, DocSymbol{
		Name:      name,
		Kind:      symObject,
		Range:     Range{Start: o.position(start), End: o.position(o.end(start))},
		Selection: Range{Start: o.position(start), End: o.position(sel)},
	})
}
