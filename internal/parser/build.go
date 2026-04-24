package parser

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// builder converts an egg parse tree ([]int32) into AST nodes.
type builder struct {
	file     string
	filtered []Token
	comments []Token
}

func newBuilder(file string, filtered, comments []Token) *builder {
	return &builder{file: file, filtered: filtered, comments: comments}
}

// body returns the children portion of a non-terminal parse tree node.
// Given tree = [-sym, count, ...children], returns tree[2:2+count].
func body(tree []int32) []int32 {
	if len(tree) < 2 {
		return nil
	}
	count := int(tree[1])
	return tree[2 : 2+count]
}

// --- nodeIter: cursor over sibling elements in a flat parse tree ---

type nodeIter struct {
	tree     []int32
	pos      int
	filtered []Token
}

func (b *builder) iter(children []int32) nodeIter {
	return nodeIter{tree: children, filtered: b.filtered}
}

func (it *nodeIter) done() bool { return it.pos >= len(it.tree) }

func (it *nodeIter) peek() int32 { return it.tree[it.pos] }

func (it *nodeIter) isNonTerminal() bool { return it.tree[it.pos] < 0 }

func (it *nodeIter) symbol() Symbol { return Symbol(-it.tree[it.pos]) }

func (it *nodeIter) token() Token { return tokenAt(it.filtered, it.tree[it.pos]) }

func (it *nodeIter) tokenType() TokenType { return it.token().Type }

// shift consumes a terminal and returns its Token.
func (it *nodeIter) shift() Token {
	tok := tokenAt(it.filtered, it.tree[it.pos])
	it.pos++
	return tok
}

// shiftIdx consumes a terminal and returns its index.
func (it *nodeIter) shiftIdx() int32 {
	idx := it.tree[it.pos]
	it.pos++
	return idx
}

// skip advances past the current element (terminal or non-terminal).
func (it *nodeIter) skip() {
	if it.tree[it.pos] < 0 {
		count := int(it.tree[it.pos+1])
		it.pos += 2 + count
	} else {
		it.pos++
	}
}

// enter consumes a non-terminal and returns an iterator over its children.
func (it *nodeIter) enter() nodeIter {
	count := int(it.tree[it.pos+1])
	children := it.tree[it.pos+2 : it.pos+2+count]
	it.pos += 2 + count
	return nodeIter{tree: children, filtered: it.filtered}
}

// childSlice returns the raw children slice of the current non-terminal without consuming.
func (it *nodeIter) childSlice() []int32 {
	count := int(it.tree[it.pos+1])
	return it.tree[it.pos+2 : it.pos+2+count]
}

func (b *builder) pos(idx int32) ast.Pos {
	tok := tokenAt(b.filtered, idx)
	return ast.Pos{File: b.file, Line: tok.Line, Column: tok.Column}
}

func (b *builder) posFromToken(tok Token) ast.Pos {
	return ast.Pos{File: b.file, Line: tok.Line, Column: tok.Column}
}

// --- Document ---

func (b *builder) buildDocument(children []int32) *ast.Document {
	doc := &ast.Document{}
	it := b.iter(children)
	for !it.done() {
		// Document = { [ slashdash ] Stmt semi } .
		if !it.isNonTerminal() {
			tok := it.token()
			if tok.Type == SLASHDASH {
				sdPos := it.shift()
				if !it.done() && it.isNonTerminal() && it.symbol() == Stmt {
					inner := b.buildStmt(it.enter())
					doc.Stmts = append(doc.Stmts, &ast.DisabledDecl{
						Pos:   b.posFromToken(sdPos),
						Inner: inner,
					})
				}
				continue
			}
			it.skip() // semi
			continue
		}
		if it.symbol() == Stmt {
			s := b.buildStmt(it.enter())
			if s != nil {
				doc.Stmts = append(doc.Stmts, s)
			}
		} else {
			it.skip()
		}
	}
	b.injectComments(doc)
	return doc
}

// injectComments inserts comment tokens into doc.Stmts at positions
// determined by their source line numbers.
func (b *builder) injectComments(doc *ast.Document) {
	if len(b.comments) == 0 {
		return
	}
	var merged []ast.Stmt
	ci := 0
	for _, s := range doc.Stmts {
		pos := s.StmtPos()
		line := 0
		if pos != nil {
			line = pos.Line
		}
		// Insert all comments before this statement.
		for ci < len(b.comments) && (line == 0 || b.comments[ci].Line < line) {
			merged = append(merged, b.commentToStmt(b.comments[ci]))
			ci++
		}
		merged = append(merged, s)
	}
	// Trailing comments after all statements.
	for ci < len(b.comments) {
		merged = append(merged, b.commentToStmt(b.comments[ci]))
		ci++
	}
	doc.Stmts = merged
}

func (b *builder) commentToStmt(tok Token) *ast.Comment {
	return &ast.Comment{
		Pos:    ast.Pos(b.posFromToken(tok)),
		Text:   tok.Literal,
		Block:  tok.Type == BLOCK_COMMENT,
		Inline: false, // TODO: detect inline comments
	}
}

// --- Stmt dispatch ---

func (b *builder) buildStmt(it nodeIter) ast.Stmt {
	if it.done() {
		return nil
	}
	if it.isNonTerminal() {
		switch it.symbol() {
		case ImportDecl:
			return b.buildImportDecl(it.enter())
		case StructDecl:
			return b.buildStructDecl(it.enter())
		case EnumDecl:
			return b.buildEnumDecl(it.enter())
		case UnitDecl:
			return b.buildUnitDecl(it.enter())
		case ConstDecl:
			return b.buildConstDecl(it.enter())
		case VarDecl:
			return b.buildVarDecl(it.enter())
		case FuncDecl:
			return b.buildFuncDecl(it.enter())
		case ComponentDecl:
			return b.buildComponentDecl(it.enter())
		case IfNode:
			return b.buildIfNode(it.enter())
		case ForNode:
			return b.buildForNode(it.enter())
		case PlatformNode:
			return b.buildPlatformNode(it.enter())
		case VisualOrStmt:
			return b.buildVisualOrStmt(it.enter())
		}
		it.skip()
		return nil
	}
	// kw_return [ Expr ]
	tok := it.token()
	if tok.Type == KW_RETURN {
		pos := b.posFromToken(it.shift())
		var val ast.Expr
		if !it.done() && it.isNonTerminal() {
			val = b.buildExpr(it.enter())
		}
		return &ast.ReturnStmt{Pos: ast.Pos(pos), Value: val}
	}
	it.skip()
	return nil
}

// exprStmt wraps an Expr as a Stmt for use in StmtBlock.Stmts.
type exprStmt struct {
	ast.Expr
}

func (e exprStmt) StmtPos() *ast.Pos { return e.Expr.ExprPos() }

// --- Imports ---

func (b *builder) buildImportDecl(it nodeIter) *ast.Import {
	// ImportDecl = kw_import [ ident fat_arrow ] str_full .
	pos := b.posFromToken(it.shift()) // kw_import
	imp := &ast.Import{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		imp.Alias = it.shift().Literal
		it.skip() // fat_arrow
	}
	if !it.done() && !it.isNonTerminal() {
		imp.Path = stripQuotes(it.shift().Literal)
	}
	return imp
}

// --- Struct declaration ---

func (b *builder) buildStructDecl(it nodeIter) *ast.StructDef {
	// StructDecl = kw_struct [ ident ] lbrace { StructField } rbrace .
	pos := b.posFromToken(it.shift()) // kw_struct
	s := &ast.StructDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		s.Name = it.shift().Literal
	}
	lbraceLine := 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		lbraceLine = it.token().Line
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == StructField {
			s.Fields = append(s.Fields, b.buildStructField(it.enter()))
		} else {
			if !it.isNonTerminal() && it.tokenType() == RBRACE {
				if it.token().Line > lbraceLine {
					s.IsMultiline = true
				}
			}
			it.skip() // rbrace
		}
	}
	return s
}

func (b *builder) buildStructField(it nodeIter) *ast.StructField {
	// StructField = IdentList Type [ assign Expr ] semi .
	f := &ast.StructField{}
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		sub := it.enter()
		if !sub.done() {
			f.Pos = b.posFromToken(sub.token())
		}
		f.Names = b.buildIdentList(sub)
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		f.Type = b.buildType(it.enter())
	}
	// [ assign Expr ]
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == ASSIGN {
			it.skip() // assign
			if !it.done() && it.isNonTerminal() {
				f.Default = b.buildExpr(it.enter())
			}
		} else {
			it.skip() // semi
		}
	}
	return f
}

// --- Enum declaration ---

func (b *builder) buildEnumDecl(it nodeIter) *ast.EnumDef {
	// EnumDecl = kw_enum [ ident ] lbrace [ ArgList ] rbrace .
	pos := b.posFromToken(it.shift()) // kw_enum
	e := &ast.EnumDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		e.Name = it.shift().Literal
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == ArgList {
			e.Members = b.buildEnumMembers(it.enter(), &e.IsMultiline)
		} else {
			it.skip() // rbrace
		}
	}
	return e
}

func (b *builder) buildEnumMembers(it nodeIter, multiline *bool) []ast.EnumMember {
	// ArgList = Arg { comma Arg } — each Arg is either ident or ident=Expr
	var members []ast.EnumMember
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Arg {
			sub := it.enter()
			m := b.buildEnumMember(sub)
			members = append(members, m)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				*multiline = true
			}
			it.skip() // comma or semi
		}
	}
	return members
}

func (b *builder) buildEnumMember(it nodeIter) ast.EnumMember {
	var m ast.EnumMember
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		nameTok := it.shift()
		m.Pos = b.posFromToken(nameTok)
		m.Name = nameTok.Literal
	}
	// Check for IdentArgCont with assign
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentArgCont {
		sub := it.enter()
		if !sub.done() && !sub.isNonTerminal() && sub.tokenType() == ASSIGN {
			sub.skip() // assign
			if !sub.done() && sub.isNonTerminal() {
				m.Value = b.buildExpr(sub.enter())
			}
		}
	}
	return m
}

// --- Unit declaration ---

func (b *builder) buildUnitDecl(it nodeIter) *ast.UnitDef {
	// UnitDecl = kw_unit [ ident ] lbrace [ ArgList ] rbrace .
	pos := b.posFromToken(it.shift()) // kw_unit
	u := &ast.UnitDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		u.Name = it.shift().Literal
	}
	if it.done() {
		return u
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == ArgList {
			u.Suffixes = b.buildUnitSuffixes(it.enter(), &u.IsMultiline)
		} else {
			it.skip() // rbrace
		}
	}
	return u
}

func (b *builder) buildUnitSuffixes(it nodeIter, multiline *bool) []*ast.UnitSuffix {
	var suffixes []*ast.UnitSuffix
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Arg {
			sub := it.enter()
			s := b.buildUnitSuffix(sub)
			suffixes = append(suffixes, s)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				*multiline = true
			}
			it.skip() // comma or semi
		}
	}
	return suffixes
}

func (b *builder) buildUnitSuffix(it nodeIter) *ast.UnitSuffix {
	s := &ast.UnitSuffix{}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		nameTok := it.shift()
		s.Pos = b.posFromToken(nameTok)
		s.Name = nameTok.Literal
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentArgCont {
		sub := it.enter()
		if !sub.done() && !sub.isNonTerminal() && sub.tokenType() == ASSIGN {
			sub.skip() // assign
			if !sub.done() && sub.isNonTerminal() {
				s.Factor = b.buildExpr(sub.enter())
			}
		}
	}
	return s
}

// --- Style declaration ---

// --- Const/Var declarations ---

func (b *builder) buildConstDecl(it nodeIter) *ast.ConstDecl {
	// ConstDecl = kw_const ConstSpec | kw_const lparen ConstSpec { comma ConstSpec } rparen .
	pos := b.posFromToken(it.shift()) // kw_const
	c := &ast.ConstDecl{Pos: ast.Pos(pos)}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		c.IsGrouped = true
		it.skip() // lparen
	}
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == ConstSpec {
			c.Specs = append(c.Specs, b.buildConstSpec(it.enter()))
		} else {
			it.skip() // comma, rparen
		}
	}
	return c
}

func (b *builder) buildConstSpec(it nodeIter) ast.VarSpec {
	// ConstSpec = IdentList [ Type ] assign Expr .
	var spec ast.VarSpec
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		spec.Names = b.buildIdentList(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		spec.Type = b.buildType(it.enter())
	}
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == ASSIGN {
			it.skip() // assign
		} else if it.isNonTerminal() {
			spec.Default = b.buildExpr(it.enter())
		} else {
			it.skip()
		}
	}
	return spec
}

func (b *builder) buildVarDecl(it nodeIter) *ast.VarDecl {
	// VarDecl = kw_var VarSpec | kw_var lparen VarSpec { comma VarSpec } rparen .
	pos := b.posFromToken(it.shift()) // kw_var
	v := &ast.VarDecl{Pos: ast.Pos(pos)}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		v.IsGrouped = true
		it.skip() // lparen
	}
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == VarSpec {
			v.Specs = append(v.Specs, b.buildVarSpec(it.enter()))
		} else {
			it.skip() // comma, rparen
		}
	}
	return v
}

func (b *builder) buildVarSpec(it nodeIter) ast.VarSpec {
	// VarSpec = IdentList [ Type ] assign Expr { VarHandler } .
	var spec ast.VarSpec
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		spec.Names = b.buildIdentList(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		spec.Type = b.buildType(it.enter())
	}
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == ASSIGN {
			it.skip()
		} else if it.isNonTerminal() && it.symbol() == VarHandler {
			spec.Handlers = append(spec.Handlers, b.buildVarHandler(it.enter()))
		} else if it.isNonTerminal() {
			spec.Default = b.buildExprNonTerminal(&it)
		} else {
			it.skip()
		}
	}
	return spec
}

func (b *builder) buildVarHandler(it nodeIter) ast.EventHandler {
	// VarHandler = at ident [ lparen [ IdentList ] rparen ] StmtBlock .
	it.skip() // at
	nameTok := it.shift()
	h := ast.EventHandler{
		Pos:  ast.Pos(b.posFromToken(nameTok)),
		Name: nameTok.Literal,
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
			names := b.buildIdentList(it.enter())
			for _, name := range names {
				h.Params.Params = append(h.Params.Params, ast.Param{Name: name})
			}
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		h.Body = b.buildStmtBlock(it.enter())
	}
	return h
}

func (b *builder) buildIdentList(it nodeIter) []string {
	// IdentList = ident { comma ident } .
	var names []string
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == IDENT {
			names = append(names, it.shift().Literal)
		} else {
			it.skip() // comma
		}
	}
	return names
}

// --- Functions ---

func (b *builder) buildFuncDecl(it nodeIter) *ast.FuncDef {
	// FuncDecl = kw_func FuncName [ TypeParamList ] FuncTail .
	pos := b.posFromToken(it.shift()) // kw_func
	f := &ast.FuncDef{Pos: pos}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncName {
		f.Name = b.buildFuncName(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
		f.TypeParams = b.buildTypeParamList(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncTail {
		b.buildFuncTail(it.enter(), f)
	}
	return f
}

func (b *builder) buildFuncName(it nodeIter) string {
	// FuncName = ident [ dot ident ] .
	name := it.shift().Literal
	if !it.done() && !it.isNonTerminal() && it.tokenType() == DOT {
		it.skip() // dot
		name += "." + it.shift().Literal
	}
	return name
}

func (b *builder) buildFuncTail(it nodeIter, f *ast.FuncDef) {
	// FuncTail = lparen [ ParamList ] rparen FuncBodyTail | FuncBodyTail .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == ParamList {
			f.Params = b.buildParamList(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncBodyTail {
		b.buildFuncBodyTail(it.enter(), f)
	}
}

func (b *builder) buildFuncBodyTail(it nodeIter, f *ast.FuncDef) {
	// FuncBodyTail = fat_arrow Expr | [ Type ] StmtBlock .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == FAT_ARROW {
		it.skip() // fat_arrow
		if !it.done() && it.isNonTerminal() {
			f.Body = b.buildExpr(it.enter())
		}
		return
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		f.ReturnType = b.buildType(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		f.Block = b.buildStmtBlock(it.enter())
	}
}

func (b *builder) buildTypeParamList(it nodeIter) []string {
	// TypeParamList = lt ident { comma ident } gt .
	var params []string
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == IDENT {
			params = append(params, it.shift().Literal)
		} else {
			it.skip() // lt, gt, comma
		}
	}
	return params
}

func (b *builder) buildParamList(it nodeIter) ast.ParamList {
	// ParamList = Param { comma Param } .
	var pl ast.ParamList
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Param {
			sub := it.enter()
			p := b.buildParam(sub)
			if !pl.Pos.IsSet() {
				pl.Pos = p.Pos
			}
			pl.Params = append(pl.Params, p)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				pl.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	return pl
}

func (b *builder) buildParam(it nodeIter) ast.Param {
	// Param = ident [ Type ] [ assign Expr ] .
	nameTok := it.shift()
	p := ast.Param{
		Pos:  ast.Pos(b.posFromToken(nameTok)),
		Name: nameTok.Literal,
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		p.Type = b.buildType(it.enter())
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip() // assign
		if !it.done() && it.isNonTerminal() {
			p.Default = b.buildExpr(it.enter())
		}
	}
	return p
}

// --- Components ---

func (b *builder) buildComponentDecl(it nodeIter) *ast.ComponentDecl {
	// ComponentDecl = kw_component ident [ lparen [ CompParamList ] rparen ] [ Type ] StmtBlock .
	pos := b.posFromToken(it.shift()) // kw_component
	c := &ast.ComponentDecl{Pos: pos}
	c.Name = it.shift().Literal // ident

	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == CompParamList {
			c.Props = b.buildCompParamList(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		c.ChildrenType = b.buildType(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		c.Body = b.buildStmtBlock(it.enter())
	}
	return c
}

func (b *builder) buildCompParamList(it nodeIter) ast.PropList {
	// CompParamList = CompParam { comma CompParam } .
	var pl ast.PropList
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == CompParam {
			p := b.buildCompParam(it.enter())
			pl.Props = append(pl.Props, p)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				pl.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	return pl
}

func (b *builder) buildCompParam(it nodeIter) ast.ParamOrEventDecl {
	// CompParam = colon ident [Type] [assign Expr] | at ident [Type] | ident CompParamTail .
	if it.done() {
		return ast.Param{}
	}
	if !it.isNonTerminal() {
		switch it.tokenType() {
		case COLON:
			it.skip() // colon
			nameTok := it.shift()
			p := ast.Param{
				Pos:           ast.Pos(b.posFromToken(nameTok)),
				Name:          nameTok.Literal,
				Bidirectional: true,
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == Type {
				p.Type = b.buildType(it.enter())
			}
			if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
				it.skip()
				if !it.done() && it.isNonTerminal() {
					p.Default = b.buildExpr(it.enter())
				}
			}
			return p
		case AT:
			it.skip() // at
			nameTok := it.shift()
			e := ast.EventDecl{
				Pos:  b.posFromToken(nameTok),
				Name: nameTok.Literal,
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == Type {
				e.Type = b.buildType(it.enter())
			}
			return e
		case IDENT:
			nameTok := it.shift()
			p := ast.Param{
				Pos:  ast.Pos(b.posFromToken(nameTok)),
				Name: nameTok.Literal,
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == CompParamTail {
				b.buildCompParamTail(it.enter(), &p)
			}
			return p
		}
	}
	it.skip()
	return ast.Param{}
}

func (b *builder) buildCompParamTail(it nodeIter, p *ast.Param) {
	// CompParamTail = assign Expr | Type [ assign Expr ] .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip()
		if !it.done() && it.isNonTerminal() {
			p.Default = b.buildExpr(it.enter())
		}
		return
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		p.Type = b.buildType(it.enter())
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip()
		if !it.done() && it.isNonTerminal() {
			p.Default = b.buildExpr(it.enter())
		}
	}
}

// --- Visual nodes + expression statements ---

func (b *builder) buildVisualOrStmt(it nodeIter) ast.Stmt {
	// VisualOrStmt = StatementPrimary { StmtPostfixOp } [ AssignOp Expr | bangbang ] .

	// Build the primary expression
	var base ast.Expr
	if !it.done() && it.isNonTerminal() && it.symbol() == StatementPrimary {
		base = b.buildStatementPrimary(it.enter())
	}
	if base == nil {
		return nil
	}

	// Apply postfix ops
	var lastBlock *ast.StmtBlock
	var args *ast.ArgList
	for !it.done() && it.isNonTerminal() && it.symbol() == StmtPostfixOp {
		base, lastBlock, args = b.applyStmtPostfixOp(it.enter(), base, lastBlock, args)
	}

	// Check for trailing assign or toggle
	if !it.done() {
		if it.isNonTerminal() && it.symbol() == AssignOp {
			op := b.buildAssignOp(it.enter())
			var val ast.Expr
			if !it.done() && it.isNonTerminal() {
				val = b.buildExpr(it.enter())
			}
			if target, ok := base.(ast.TargetExpr); ok {
				return &ast.AssignStmt{
					Pos:    ast.Pos(*base.ExprPos()),
					Target: target,
					Op:     op,
					Value:  val,
				}
			}
		}
		if !it.isNonTerminal() && it.tokenType() == BANGBANG {
			it.skip()
			if target, ok := base.(ast.TargetExpr); ok {
				return &ast.ToggleStmt{
					Pos:    ast.Pos(*base.ExprPos()),
					Target: target,
				}
			}
		}
		if it.isNonTerminal() && it.symbol() == IncDecOp {
			inner := it.enter()
			isDec := false
			if !inner.done() {
				tok := inner.shift()
				if tok.Type == MINUS_MINUS {
					isDec = true
				}
			}
			if target, ok := base.(ast.TargetExpr); ok {
				return &ast.IncDecStmt{
					Pos:    ast.Pos(*base.ExprPos()),
					Target: target,
					IsDec:  isDec,
				}
			}
		}
	}

	// Emit: base is EventRefExpr
	if evRef, ok := base.(*ast.EventRefExpr); ok {
		emit := &ast.EmitStmt{
			Pos:  ast.Pos(*base.ExprPos()),
			Name: evRef.Name,
		}
		if args != nil {
			emit.Args = *args
		}
		if lastBlock != nil {
			emit.Args.Args = append(emit.Args.Args, ast.EventHandler{
				Pos:  ast.Pos(emit.Pos),
				Name: evRef.Name,
				Body: *lastBlock,
			})
		}
		return emit
	}

	// Visual node: has a block body or args
	if lastBlock != nil || args != nil {
		vn := &ast.VisualNode{
			Pos: ast.Pos(*base.ExprPos()),
		}
		// If base is a CallExpr, decompose: Target = Func, Args = call's args
		if call, ok := base.(*ast.CallExpr); ok {
			if target, ok := call.Func.(ast.TargetExpr); ok {
				vn.Target = target
			}
			vn.Args = call.Args
		} else {
			if target, ok := base.(ast.TargetExpr); ok {
				vn.Target = target
			}
			if args != nil {
				vn.Args = *args
			}
		}
		// Extract #id from target: "name #id(...)" → Target=name, ID=id.
		if sel, ok := vn.Target.(*ast.SelectExpr); ok && sel.Kind == ast.SelectElemRef {
			if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
				vn.Target = ident
				vn.ID = sel.Field
			}
		}
		if lastBlock != nil {
			vn.Block = *lastBlock
		}
		return vn
	}

	// Call statement
	if call, ok := base.(*ast.CallExpr); ok {
		return &ast.CallStmt{
			Pos:  ast.Pos(*base.ExprPos()),
			Call: call,
		}
	}

	// Bare expression as a visual node
	vn := &ast.VisualNode{
		Pos: ast.Pos(*base.ExprPos()),
	}
	if target, ok := base.(ast.TargetExpr); ok {
		vn.Target = target
	}
	// Split "target.#id" SelectExpr into Target + ID.
	if sel, ok := vn.Target.(*ast.SelectExpr); ok && sel.Kind == ast.SelectElemRef {
		if ident, ok := sel.Operand.(*ast.IdentExpr); ok {
			vn.Target = ident
			vn.ID = sel.Field
		}
	}
	return vn
}

func (b *builder) applyStmtPostfixOp(it nodeIter, base ast.Expr, lastBlock *ast.StmtBlock, lastArgs *ast.ArgList) (ast.Expr, *ast.StmtBlock, *ast.ArgList) {
	// StmtPostfixOp = dot ident | dot at ident | dot elem_ref | elem_ref
	//               | lbracket Expr rbracket | lparen [ ArgList ] rparen | StmtBlock .
	if it.done() {
		return base, lastBlock, lastArgs
	}

	if it.isNonTerminal() {
		switch it.symbol() {
		case StmtBlock:
			block := b.buildStmtBlock(it.enter())
			return base, &block, lastArgs
		case ArgList:
			args := b.buildArgList(it.enter())
			return base, lastBlock, &args
		}
		it.skip()
		return base, lastBlock, lastArgs
	}

	tok := it.token()
	switch tok.Type {
	case DOT:
		it.skip() // dot
		if !it.done() && !it.isNonTerminal() {
			next := it.token()
			switch next.Type {
			case IDENT:
				field := it.shift()
				return &ast.SelectExpr{
					Pos:     b.posFromToken(tok),
					Operand: base,
					Field:   field.Literal,
					Kind:    ast.SelectField,
				}, nil, nil
			case AT:
				it.skip() // at
				field := it.shift()
				return &ast.SelectExpr{
					Pos:     b.posFromToken(tok),
					Operand: base,
					Field:   field.Literal,
					Kind:    ast.SelectEvent,
				}, nil, nil
			}
		}
	case ELEMENT_REF:
		ref := it.shift()
		return &ast.SelectExpr{
			Pos:     b.posFromToken(ref),
			Operand: base,
			Field:   ref.Literal,
			Kind:    ast.SelectElemRef,
		}, nil, nil
	case LBRACKET:
		it.skip() // lbracket
		var idx ast.Expr
		if !it.done() && it.isNonTerminal() {
			idx = b.buildExpr(it.enter())
		}
		// rbracket
		return &ast.IndexExpr{
			Pos:     b.posFromToken(tok),
			Operand: base,
			Index:   idx,
		}, nil, nil
	case LPAREN:
		it.skip() // lparen
		call := &ast.CallExpr{
			Pos:  ast.Pos(b.posFromToken(tok)),
			Func: base,
		}
		if !it.done() && it.isNonTerminal() && it.symbol() == ArgList {
			call.Args = b.buildArgList(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
		if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
			block := b.buildStmtBlock(it.enter())
			// Preserve EventRefExpr so caller can convert to EventHandler.
			if _, ok := base.(*ast.EventRefExpr); ok {
				return base, &block, &call.Args
			}
			return call, &block, nil
		}
		return call, nil, nil
	case LBRACE:
		// StmtBlock path
		if it.isNonTerminal() && it.symbol() == StmtBlock {
			block := b.buildStmtBlock(it.enter())
			return base, &block, lastArgs
		}
	}
	if !it.done() {
		it.skip()
	}
	return base, lastBlock, lastArgs
}

func (b *builder) buildStatementPrimary(it nodeIter) ast.Expr {
	// StatementPrimary has the same alternatives as PrimaryExpr minus FuncLit/StructLitBody
	return b.buildPrimaryInner(it, false)
}

// --- StmtBlock ---

func (b *builder) buildStmtBlock(it nodeIter) ast.StmtBlock {
	// StmtBlock = lbrace { [ slashdash ] Stmt semi } rbrace .
	var block ast.StmtBlock
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		tok := it.shift()
		block.Pos = ast.Pos(b.posFromToken(tok))
	}
	for !it.done() {
		if !it.isNonTerminal() {
			tok := it.token()
			if tok.Type == RBRACE {
				if tok.Line > block.Pos.Line {
					block.IsMultiline = true
				}
				it.skip()
				continue
			}
			if tok.Type == SLASHDASH {
				it.skip() // slashdash
				if !it.done() && it.isNonTerminal() && it.symbol() == Stmt {
					inner := b.buildStmt(it.enter())
					block.Stmts = append(block.Stmts, &ast.DisabledDecl{
						Inner: inner,
					})
				}
				continue
			}
			if tok.Type == SEMICOLON {
				block.IsMultiline = true
			}
			it.skip() // semi
			continue
		}
		if it.symbol() == Stmt {
			s := b.buildStmt(it.enter())
			if s != nil {
				block.Stmts = append(block.Stmts, s)
			}
		} else {
			it.skip()
		}
	}
	return block
}

// --- Control flow ---

func (b *builder) buildIfNode(it nodeIter) *ast.IfStmt {
	// IfNode = kw_if CondExpr StmtBlock [ kw_else StmtBlock ] .
	pos := b.posFromToken(it.shift()) // kw_if
	stmt := &ast.IfStmt{Pos: pos}
	if !it.done() && it.isNonTerminal() && it.symbol() == CondExpr {
		stmt.Cond = b.buildExpr(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		stmt.Body = b.buildStmtBlock(it.enter())
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == KW_ELSE {
		it.skip() // kw_else
		if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
			stmt.Else = b.buildStmtBlock(it.enter())
		}
	}
	return stmt
}

func (b *builder) buildForNode(it nodeIter) *ast.ForStmt {
	// ForNode = kw_for ident [ comma ident ] assign CondExpr StmtBlock [ kw_else StmtBlock ] .
	pos := b.posFromToken(it.shift()) // kw_for
	stmt := &ast.ForStmt{Pos: pos}
	stmt.Key = it.shift().Literal // ident
	if !it.done() && !it.isNonTerminal() && it.tokenType() == COMMA {
		it.skip() // comma
		stmt.Value = it.shift().Literal
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip() // assign
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == CondExpr {
		stmt.Iter = b.buildExpr(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		stmt.Body = b.buildStmtBlock(it.enter())
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == KW_ELSE {
		it.skip() // kw_else
		if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
			stmt.Else = b.buildStmtBlock(it.enter())
		}
	}
	return stmt
}

func (b *builder) buildPlatformNode(it nodeIter) *ast.PlatformStmt {
	// PlatformNode = kw_platform ident StmtBlock .
	pos := b.posFromToken(it.shift()) // kw_platform
	stmt := &ast.PlatformStmt{Pos: ast.Pos(pos)}
	stmt.Platform = it.shift().Literal // ident
	if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
		stmt.Body = b.buildStmtBlock(it.enter())
	}
	return stmt
}

// --- Expressions ---

func (b *builder) buildExpr(it nodeIter) ast.Expr {
	// Expr = TernaryExpr .
	if it.done() {
		return nil
	}
	if it.isNonTerminal() && it.symbol() == TernaryExpr {
		return b.buildTernaryExpr(it.enter())
	}
	return b.buildAnyExpr(&it)
}

// buildExprNonTerminal builds an expression from the current non-terminal.
func (b *builder) buildExprNonTerminal(it *nodeIter) ast.Expr {
	if it.done() || !it.isNonTerminal() {
		return nil
	}
	sym := it.symbol()
	sub := it.enter()
	return b.buildExprBySymbol(sym, sub)
}

func (b *builder) buildExprBySymbol(sym Symbol, it nodeIter) ast.Expr {
	switch sym {
	case Expr, CondExpr:
		return b.buildExpr(it)
	case TernaryExpr:
		return b.buildTernaryExpr(it)
	case OrExpr, CondOrExpr:
		return b.buildBinaryChain(it, sym)
	case AndExpr, CondAndExpr:
		return b.buildBinaryChain(it, sym)
	case EqExpr, CondEqExpr:
		return b.buildBinaryChain(it, sym)
	case CmpExpr, CondCmpExpr:
		return b.buildBinaryChain(it, sym)
	case AddExpr, CondAddExpr:
		return b.buildBinaryChain(it, sym)
	case MulExpr, CondMulExpr:
		return b.buildBinaryChain(it, sym)
	case UnaryExpr, CondUnaryExpr:
		return b.buildUnaryExpr(it)
	case PostfixExpr, CondPostfixExpr:
		return b.buildPostfixExpr(it)
	case PrimaryExpr:
		return b.buildPrimaryExpr(it)
	case InterpStr:
		return b.buildInterpStr(it)
	case TripleInterp:
		return b.buildTripleInterp(it)
	case AnonStructLit:
		return b.buildAnonStructLit(it)
	case FuncLit:
		return b.buildFuncLit(it)
	case StructLitBody:
		// Shouldn't reach here standalone — handled in PrimaryExpr and PostfixOp
		return nil
	}
	return nil
}

// buildAnyExpr handles both terminal and non-terminal expressions at current position.
func (b *builder) buildAnyExpr(it *nodeIter) ast.Expr {
	if it.done() {
		return nil
	}
	if it.isNonTerminal() {
		return b.buildExprNonTerminal(it)
	}
	return b.buildTerminalExpr(it)
}

func (b *builder) buildTerminalExpr(it *nodeIter) ast.Expr {
	tok := it.shift()
	return b.tokenToExpr(tok)
}

func (b *builder) tokenToExpr(tok Token) ast.Expr {
	pos := b.posFromToken(tok)
	switch tok.Type {
	case INT:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralInt, Raw: tok.Literal}
	case FLOAT:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralFloat, Raw: tok.Literal}
	case STR_FULL:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringQuoted, Raw: tok.Literal}
	case TRIPLE_FULL:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringTrippleQuoted, Raw: tok.Literal}
	case RAW_STRING:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringBackticked, Raw: tok.Literal}
	case COLOR:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralColor, Raw: tok.Literal}
	case UNIT_LITERAL:
		return &ast.UnitLiteral{
			Pos:         ast.Pos(pos),
			LiteralExpr: ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralUnit, Raw: tok.Literal},
			Suffix:      extractUnitSuffix(tok.Literal),
		}
	case IDENT:
		// Check for bool/null literals
		switch tok.Literal {
		case "true", "false":
			return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralBool, Raw: tok.Literal}
		case "null":
			return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralNull, Raw: tok.Literal}
		}
		return &ast.IdentExpr{Pos: ast.Pos(pos), Name: tok.Literal}
	case ELEMENT_REF:
		return &ast.ElementRefExpr{Pos: ast.Pos(pos), Name: tok.Literal}
	case AT:
		return &ast.EventRefExpr{Pos: ast.Pos(pos)}
	}
	return &ast.IdentExpr{Pos: ast.Pos(pos), Name: tok.Literal}
}

func (b *builder) buildTernaryExpr(it nodeIter) ast.Expr {
	// TernaryExpr = OrExpr [ question Expr colon Expr ] .
	if it.done() {
		return nil
	}
	var base ast.Expr
	if it.isNonTerminal() {
		base = b.buildExprNonTerminal(&it)
	} else {
		base = b.buildTerminalExpr(&it)
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == QUESTION {
		it.skip() // question
		var then, els ast.Expr
		if !it.done() && it.isNonTerminal() {
			then = b.buildExpr(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == COLON {
			it.skip() // colon
		}
		if !it.done() && it.isNonTerminal() {
			els = b.buildExpr(it.enter())
		}
		return &ast.TernaryExpr{
			Pos:  ast.Pos(*base.ExprPos()),
			Cond: base,
			Then: then,
			Else: els,
		}
	}
	return base
}

func (b *builder) buildBinaryChain(it nodeIter, level Symbol) ast.Expr {
	// Pattern: SubExpr { OpSymbol SubExpr } .
	if it.done() {
		return nil
	}
	var left ast.Expr
	if it.isNonTerminal() {
		left = b.buildExprNonTerminal(&it)
	} else {
		left = b.buildTerminalExpr(&it)
	}

	for !it.done() {
		if !it.isNonTerminal() {
			// land/lor appear as bare terminals in OrExpr/AndExpr.
			tt := it.tokenType()
			if tt == AND || tt == OR {
				it.shift()
				op := tokenToBinaryOp(tt)
				var right ast.Expr
				if !it.done() && it.isNonTerminal() {
					right = b.buildExprNonTerminal(&it)
				}
				if right != nil {
					left = &ast.BinaryExpr{
						Pos:   ast.Pos(*left.ExprPos()),
						Op:    op,
						Left:  left,
						Right: right,
					}
				}
				continue
			}
			// Skip unexpected terminals.
			it.skip()
			continue
		}
		sym := it.symbol()
		// Check if this is an operator symbol
		if isOpSymbol(sym) {
			op := b.buildBinaryOp(it.enter())
			var right ast.Expr
			if !it.done() && it.isNonTerminal() {
				right = b.buildExprNonTerminal(&it)
			}
			if right != nil {
				left = &ast.BinaryExpr{
					Pos:   ast.Pos(*left.ExprPos()),
					Op:    op,
					Left:  left,
					Right: right,
				}
			}
		} else {
			// Sub-expression from inner level
			left = b.buildExprNonTerminal(&it)
		}
	}
	return left
}

func isOpSymbol(s Symbol) bool {
	switch s {
	case EqOp, CmpOp, AddOp, MulOp:
		return true
	}
	return false
}

func (b *builder) buildBinaryOp(it nodeIter) ast.BinaryOp {
	if it.done() {
		return ast.BinAdd
	}
	tok := it.shift()
	return tokenToBinaryOp(tok.Type)
}

func tokenToBinaryOp(t TokenType) ast.BinaryOp {
	switch t {
	case PLUS:
		return ast.BinAdd
	case MINUS:
		return ast.BinSub
	case STAR:
		return ast.BinMul
	case SLASH:
		return ast.BinDiv
	case PERCENT:
		return ast.BinMod
	case EQ:
		return ast.BinEq
	case NEQ:
		return ast.BinNeq
	case LT:
		return ast.BinLt
	case LTE:
		return ast.BinLte
	case GT:
		return ast.BinGt
	case GTE:
		return ast.BinGte
	case AND:
		return ast.BinAnd
	case OR:
		return ast.BinOr
	}
	return ast.BinAdd
}

func (b *builder) buildUnaryExpr(it nodeIter) ast.Expr {
	// UnaryExpr = PostfixExpr | bang UnaryExpr | minus UnaryExpr .
	if it.done() {
		return nil
	}
	if it.isNonTerminal() {
		return b.buildExprNonTerminal(&it)
	}
	tok := it.token()
	switch tok.Type {
	case BANG:
		pos := b.posFromToken(it.shift())
		operand := b.buildAnyExpr(&it)
		return &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryNot, Operand: operand}
	case KW_CONST:
		pos := b.posFromToken(it.shift())
		operand := b.buildAnyExpr(&it)
		return &ast.ConstExpr{Pos: ast.Pos(pos), Operand: operand}
	case MINUS:
		pos := b.posFromToken(it.shift())
		operand := b.buildAnyExpr(&it)
		return &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryNeg, Operand: operand}
	}
	return b.buildTerminalExpr(&it)
}

func (b *builder) buildPostfixExpr(it nodeIter) ast.Expr {
	// PostfixExpr = PrimaryExpr { ExprPostfixOp } .
	// CondPostfixExpr = StatementPrimary { CondPostfixOp } .
	if it.done() {
		return nil
	}
	var base ast.Expr
	if it.isNonTerminal() {
		switch it.symbol() {
		case PrimaryExpr:
			base = b.buildPrimaryExpr(it.enter())
		case StatementPrimary:
			base = b.buildStatementPrimary(it.enter())
		default:
			base = b.buildAnyExpr(&it)
		}
	} else {
		base = b.buildAnyExpr(&it)
	}

	for !it.done() && it.isNonTerminal() && (it.symbol() == ExprPostfixOp || it.symbol() == CondPostfixOp) {
		base = b.buildExprPostfixOp(it.enter(), base)
	}
	return base
}

func (b *builder) buildExprPostfixOp(it nodeIter, base ast.Expr) ast.Expr {
	// ExprPostfixOp = dot ident [StructLitBody] | dot at ident | dot elem_ref
	//              | elem_ref | lbracket Expr rbracket | lparen [ArgList] rparen .
	if it.done() {
		return base
	}
	if it.isNonTerminal() {
		// Could be ArgList inside lparen..rparen
		sym := it.symbol()
		switch sym {
		case ArgList:
			call := &ast.CallExpr{Pos: ast.Pos(*base.ExprPos()), Func: base}
			call.Args = b.buildArgList(it.enter())
			return call
		case StructLitBody:
			// StructLitBody after dot ident — qualified struct lit
			// base should be a SelectExpr from the preceding dot ident
			if sel, ok := base.(*ast.SelectExpr); ok {
				s := &ast.StructExpr{
					Pos:     ast.Pos(*sel.Operand.ExprPos()),
					Package: identName(sel.Operand),
					Name:    sel.Field,
				}
				s.Fields = b.buildStructLitFields(it.enter(), &s.Multiline)
				return s
			}
			it.skip()
			return base
		}
		it.skip()
		return base
	}

	tok := it.token()
	switch tok.Type {
	case DOT:
		it.skip() // dot
		if it.done() {
			return base
		}
		if !it.isNonTerminal() {
			next := it.token()
			switch next.Type {
			case IDENT:
				field := it.shift()
				sel := &ast.SelectExpr{
					Pos:     b.posFromToken(tok),
					Operand: base,
					Field:   field.Literal,
					Kind:    ast.SelectField,
				}
				// Check for StructLitBody after dot ident
				if !it.done() && it.isNonTerminal() && it.symbol() == StructLitBody {
					s := &ast.StructExpr{
						Pos:     ast.Pos(*base.ExprPos()),
						Package: identName(base),
						Name:    field.Literal,
					}
					s.Fields = b.buildStructLitFields(it.enter(), &s.Multiline)
					return s
				}
				return sel
			case AT:
				it.skip() // at
				field := it.shift()
				return &ast.SelectExpr{
					Pos:     b.posFromToken(tok),
					Operand: base,
					Field:   field.Literal,
					Kind:    ast.SelectEvent,
				}
			}
		}
	case ELEMENT_REF:
		ref := it.shift()
		return &ast.SelectExpr{
			Pos:     b.posFromToken(ref),
			Operand: base,
			Field:   ref.Literal,
			Kind:    ast.SelectElemRef,
		}
	case LBRACKET:
		it.skip() // lbracket
		var idx ast.Expr
		if !it.done() && it.isNonTerminal() {
			idx = b.buildExpr(it.enter())
		}
		// rbracket
		return &ast.IndexExpr{
			Pos:     b.posFromToken(tok),
			Operand: base,
			Index:   idx,
		}
	case LPAREN:
		it.skip() // lparen
		call := &ast.CallExpr{Pos: ast.Pos(b.posFromToken(tok)), Func: base}
		if !it.done() && it.isNonTerminal() && it.symbol() == ArgList {
			call.Args = b.buildArgList(it.enter())
		}
		// rparen
		return call
	}
	it.skip()
	return base
}

func (b *builder) buildPrimaryExpr(it nodeIter) ast.Expr {
	return b.buildPrimaryInner(it, true)
}

// buildPrimaryInner handles both PrimaryExpr and StatementPrimary.
// exprContext=true enables FuncLit and StructLitBody on ident.
func (b *builder) buildPrimaryInner(it nodeIter, exprContext bool) ast.Expr {
	if it.done() {
		return nil
	}

	if it.isNonTerminal() {
		sym := it.symbol()
		switch sym {
		case InterpStr:
			return b.buildInterpStr(it.enter())
		case TripleInterp:
			return b.buildTripleInterp(it.enter())
		case AnonStructLit:
			return b.buildAnonStructLit(it.enter())
		case FuncLit:
			if exprContext {
				return b.buildFuncLit(it.enter())
			}
			it.skip()
			return nil
		case StructLitBody:
			// This shouldn't appear standalone in PrimaryExpr
			it.skip()
			return nil
		case ListBody:
			le := &ast.ListExpr{}
			le.Elements = b.buildListBody(it.enter(), &le.IsMultiline)
			return le
		}
		return b.buildExprBySymbol(sym, it.enter())
	}

	tok := it.token()
	switch tok.Type {
	case LPAREN:
		it.skip() // lparen
		var inner ast.Expr
		if !it.done() && it.isNonTerminal() {
			inner = b.buildExpr(it.enter())
		}
		if !it.done() {
			it.skip() // rparen
		}
		if inner == nil {
			return nil
		}
		return &ast.ParenExpr{Pos: ast.Pos(b.posFromToken(tok)), Inner: inner}
	case LBRACKET:
		pos := b.posFromToken(it.shift()) // lbracket
		le := &ast.ListExpr{Pos: ast.Pos(pos)}
		if !it.done() && it.isNonTerminal() && it.symbol() == ListBody {
			le.Elements = b.buildListBody(it.enter(), &le.IsMultiline)
		}
		if !it.done() {
			it.skip() // rbracket
		}
		return le
	case AT:
		it.skip() // at
		if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
			nameTok := it.shift()
			return &ast.EventRefExpr{Pos: b.posFromToken(tok), Name: nameTok.Literal}
		}
		return &ast.EventRefExpr{Pos: b.posFromToken(tok)}
	case IDENT:
		identTok := it.shift()
		// In expression context, ident may be followed by StructLitBody
		if exprContext && !it.done() && it.isNonTerminal() && it.symbol() == StructLitBody {
			s := &ast.StructExpr{
				Pos:  ast.Pos(b.posFromToken(identTok)),
				Name: identTok.Literal,
			}
			s.Fields = b.buildStructLitFields(it.enter(), &s.Multiline)
			return s
		}
		return b.tokenToExpr(identTok)
	default:
		return b.tokenToExpr(it.shift())
	}
}

// --- Composite literals ---

func (b *builder) buildStructLitFields(it nodeIter, multiline *bool) []ast.StructFieldLit {
	// StructLitBody = lbrace [ AnonField { (comma | semi) AnonField } ] rbrace .
	var fields []ast.StructFieldLit
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == AnonField {
			fields = append(fields, b.buildAnonField(it.enter()))
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				*multiline = true
			}
			it.skip() // lbrace, rbrace, comma, semi
		}
	}
	return fields
}

func (b *builder) buildAnonStructLit(it nodeIter) *ast.StructExpr {
	// AnonStructLit = lbrace [ AnonField { (comma | semi) AnonField } ] rbrace .
	s := &ast.StructExpr{}
	var fields []ast.StructFieldLit
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == AnonField {
			f := b.buildAnonField(it.enter())
			fields = append(fields, f)
		} else {
			tok := it.token()
			if tok.Type == LBRACE && !s.Pos.IsSet() {
				s.Pos = ast.Pos(b.posFromToken(tok))
			}
			if tok.Type == SEMICOLON {
				s.Multiline = true
			}
			it.skip() // lbrace, rbrace, comma, semi
		}
	}
	s.Fields = fields
	return s
}

func (b *builder) buildAnonField(it nodeIter) ast.StructFieldLit {
	// AnonField = ellipsis Expr | ident assign Expr .
	if it.done() {
		return ast.StructFieldLit{}
	}
	if !it.isNonTerminal() && it.tokenType() == ELLIPSIS {
		it.skip() // ellipsis
		var val ast.Expr
		if !it.done() && it.isNonTerminal() {
			val = b.buildExpr(it.enter())
		}
		return ast.StructFieldLit{Spread: true, Value: val}
	}
	// ident assign Expr
	name := ""
	if !it.isNonTerminal() && it.tokenType() == IDENT {
		name = it.shift().Literal
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip() // assign
	}
	var val ast.Expr
	if !it.done() && it.isNonTerminal() {
		val = b.buildExpr(it.enter())
	}
	return ast.StructFieldLit{Name: name, Value: val}
}

func (b *builder) buildListBody(it nodeIter, multiline *bool) []ast.Expr {
	// ListBody = [ ListElem { comma ListElem } ] .
	var elems []ast.Expr
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == ListElem {
			elems = append(elems, b.buildListElem(it.enter()))
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				*multiline = true
			}
			it.skip() // comma or semi
		}
	}
	return elems
}

func (b *builder) buildListElem(it nodeIter) ast.Expr {
	// ListElem = ellipsis Expr | Expr .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ELLIPSIS {
		pos := b.posFromToken(it.shift())
		var operand ast.Expr
		if !it.done() && it.isNonTerminal() {
			operand = b.buildExpr(it.enter())
		}
		return &ast.SpreadExpr{Pos: ast.Pos(pos), Operand: operand}
	}
	if !it.done() && it.isNonTerminal() {
		return b.buildExpr(it.enter())
	}
	return nil
}

// --- String interpolation ---

func (b *builder) buildInterpStr(it nodeIter) ast.Expr {
	// InterpStr = str_start Expr { str_resume Expr } str_end .
	var parts []ast.Expr
	pos := ast.Pos{}
	for !it.done() {
		if !it.isNonTerminal() {
			tok := it.shift()
			if !pos.IsSet() {
				pos = b.posFromToken(tok)
			}
			switch tok.Type {
			case STR_START, STR_RESUME, STR_END:
				if tok.Literal != "" {
					parts = append(parts, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		} else {
			parts = append(parts, b.buildExpr(it.enter()))
		}
	}
	return &ast.InterpolationExpr{Pos: pos, Parts: parts, Style: ast.StyleDouble}
}

func (b *builder) buildTripleInterp(it nodeIter) ast.Expr {
	// TripleInterp = triple_start Expr { str_resume Expr } triple_end .
	var parts []ast.Expr
	pos := ast.Pos{}
	for !it.done() {
		if !it.isNonTerminal() {
			tok := it.shift()
			if !pos.IsSet() {
				pos = b.posFromToken(tok)
			}
			switch tok.Type {
			case TRIPLE_START, STR_RESUME, TRIPLE_END:
				if tok.Literal != "" {
					parts = append(parts, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringTrippleQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		} else {
			parts = append(parts, b.buildExpr(it.enter()))
		}
	}
	return &ast.InterpolationExpr{Pos: pos, Parts: parts, Style: ast.StyleTriple}
}

// --- Argument lists ---

func (b *builder) buildArgList(it nodeIter) ast.ArgList {
	// ArgList = Arg { (comma | semi) Arg } .
	var al ast.ArgList
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Arg {
			sub := it.enter()
			a := b.buildArg(sub)
			if a != nil {
				if !al.Pos.IsSet() {
					al.Pos = ast.Pos(argPos(a))
				}
				al.Args = append(al.Args, a)
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				al.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	return al
}

func argPos(a ast.ArgOrEventHandler) ast.Pos {
	switch v := a.(type) {
	case ast.Arg:
		if v.Value != nil {
			return ast.Pos(*v.Value.ExprPos())
		}
	case ast.EventHandler:
		return ast.Pos(v.Pos)
	}
	return ast.Pos{}
}

func (b *builder) buildArg(it nodeIter) ast.ArgOrEventHandler {
	// Arg = colon ident [Type] [assign Expr]
	//     | ellipsis Expr
	//     | ident IdentArgCont
	//     | bang UnaryExpr ArgExprCont
	//     | minus UnaryExpr ArgExprCont
	//     | NonIdentPrimary { StmtPostfixOp } ArgExprCont
	if it.done() {
		return nil
	}

	if !it.isNonTerminal() {
		tok := it.token()
		switch tok.Type {
		case COLON:
			// Binding param: colon ident [Type] [assign Expr]
			it.skip() // colon
			nameTok := it.shift()
			p := ast.Param{
				Pos:           ast.Pos(b.posFromToken(nameTok)),
				Name:          nameTok.Literal,
				Bidirectional: true,
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == Type {
				p.Type = b.buildType(it.enter())
			}
			if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
				it.skip()
				if !it.done() && it.isNonTerminal() {
					p.Default = b.buildExpr(it.enter())
				}
			}
			// Return as named arg with binding semantics.
			// Use explicit =Expr if provided, otherwise default to same-name ident.
			var value ast.Expr = &ast.IdentExpr{Pos: ast.Pos(b.posFromToken(nameTok)), Name: nameTok.Literal}
			if p.Default != nil {
				value = p.Default
			}
			return ast.Arg{Name: ":" + nameTok.Literal, Value: value}
		case ELLIPSIS:
			it.skip() // ellipsis
			var val ast.Expr
			if !it.done() && it.isNonTerminal() {
				val = b.buildExpr(it.enter())
			}
			return ast.Arg{Value: &ast.SpreadExpr{Pos: b.posFromToken(tok), Operand: val}}
		case IDENT:
			identTok := it.shift()
			if !it.done() && it.isNonTerminal() && it.symbol() == IdentArgCont {
				return b.buildIdentArgCont(it.enter(), identTok)
			}
			return ast.Arg{Value: b.tokenToExpr(identTok)}
		case BANG:
			pos := b.posFromToken(it.shift())
			var operand ast.Expr
			if !it.done() && it.isNonTerminal() {
				operand = b.buildExprNonTerminal(&it)
			}
			base := &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryNot, Operand: operand}
			expr := b.applyArgExprCont(&it, base)
			return ast.Arg{Value: expr}
		case MINUS:
			pos := b.posFromToken(it.shift())
			var operand ast.Expr
			if !it.done() && it.isNonTerminal() {
				operand = b.buildExprNonTerminal(&it)
			}
			base := &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryNeg, Operand: operand}
			expr := b.applyArgExprCont(&it, base)
			return ast.Arg{Value: expr}
		case KW_CONST:
			pos := b.posFromToken(it.shift())
			var operand ast.Expr
			if !it.done() && it.isNonTerminal() {
				operand = b.buildExprNonTerminal(&it)
			}
			base := &ast.ConstExpr{Pos: ast.Pos(pos), Operand: operand}
			expr := b.applyArgExprCont(&it, base)
			return ast.Arg{Value: expr}
		}
	}

	// FuncLit (lambda arg: `func(params) { ... }` or `func(params) => expr`).
	if it.isNonTerminal() && it.symbol() == FuncLit {
		lam := b.buildFuncLit(it.enter())
		return ast.Arg{Value: lam}
	}

	// NonIdentPrimary { StmtPostfixOp } ArgExprCont
	if it.isNonTerminal() && it.symbol() == NonIdentPrimary {
		base := b.buildNonIdentPrimary(it.enter())
		var lastBlock *ast.StmtBlock
		var lastArgs *ast.ArgList
		for !it.done() && it.isNonTerminal() && it.symbol() == StmtPostfixOp {
			base, lastBlock, lastArgs = b.applyStmtPostfixOp(it.enter(), base, lastBlock, lastArgs)
		}
		// Convert EventRefExpr with trailing block to EventHandler.
		if ref, ok := base.(*ast.EventRefExpr); ok && lastBlock != nil {
			h := ast.EventHandler{
				Pos:  ref.Pos,
				Name: ref.Name,
				Body: *lastBlock,
			}
			if lastArgs != nil {
				// @click(e) { ... } — params from the call args
				for _, a := range lastArgs.Args {
					if arg, ok := a.(ast.Arg); ok {
						if ident, ok := arg.Value.(*ast.IdentExpr); ok && arg.Name == "" {
							h.Params.Params = append(h.Params.Params, ast.Param{
								Pos:  ident.Pos,
								Name: ident.Name,
							})
						}
					}
				}
			}
			return h
		}
		expr := b.applyArgExprCont(&it, base)
		return ast.Arg{Value: expr}
	}

	it.skip()
	return nil
}

func (b *builder) buildIdentArgCont(it nodeIter, identTok Token) ast.ArgOrEventHandler {
	// IdentArgCont = assign Expr
	//             | StructLitBody { ExprPostfixOp } ArgExprCont
	//             | { ExprPostfixOp } ArgExprCont
	if it.done() {
		return ast.Arg{Value: b.tokenToExpr(identTok)}
	}

	// Check first element
	if !it.isNonTerminal() && it.tokenType() == ASSIGN {
		// Named arg: ident = Expr
		it.skip() // assign
		var val ast.Expr
		if !it.done() && it.isNonTerminal() {
			val = b.buildExpr(it.enter())
		}
		return ast.Arg{Name: identTok.Literal, Value: val}
	}

	// Build the expression starting with the ident
	base := b.tokenToExpr(identTok)

	// StructLitBody path
	if !it.done() && it.isNonTerminal() && it.symbol() == StructLitBody {
		s := &ast.StructExpr{
			Pos:  ast.Pos(b.posFromToken(identTok)),
			Name: identTok.Literal,
		}
		s.Fields = b.buildStructLitFields(it.enter(), &s.Multiline)
		base = s
	}

	// ExprPostfixOp chain
	for !it.done() && it.isNonTerminal() && it.symbol() == ExprPostfixOp {
		base = b.buildExprPostfixOp(it.enter(), base)
	}

	// ArgExprCont
	base = b.applyArgExprCont(&it, base)

	return ast.Arg{Value: base}
}

func (b *builder) applyArgExprCont(it *nodeIter, base ast.Expr) ast.Expr {
	if it.done() {
		return base
	}
	if it.isNonTerminal() && it.symbol() == ArgExprCont {
		return b.buildArgExprCont(it.enter(), base)
	}
	return base
}

func (b *builder) buildArgExprCont(it nodeIter, base ast.Expr) ast.Expr {
	// ArgExprCont = { MulOp UnaryExpr } { AddOp MulExpr } { CmpOp AddExpr }
	//              { EqOp CmpExpr } { land EqExpr } { lor AndExpr }
	//              [ question Expr colon Expr ] .
	expr := base
	for !it.done() {
		if it.isNonTerminal() {
			sym := it.symbol()
			if isOpSymbol(sym) {
				op := b.buildBinaryOp(it.enter())
				var right ast.Expr
				if !it.done() && it.isNonTerminal() {
					right = b.buildExprNonTerminal(&it)
				}
				if right != nil {
					expr = &ast.BinaryExpr{
						Pos:   ast.Pos(*expr.ExprPos()),
						Op:    op,
						Left:  expr,
						Right: right,
					}
				}
			} else {
				// Some sub-expression
				right := b.buildExprNonTerminal(&it)
				if right != nil {
					expr = right
				}
			}
		} else {
			tok := it.token()
			switch tok.Type {
			case AND:
				it.skip()
				var right ast.Expr
				if !it.done() && it.isNonTerminal() {
					right = b.buildExprNonTerminal(&it)
				}
				if right != nil {
					expr = &ast.BinaryExpr{Pos: ast.Pos(*expr.ExprPos()), Op: ast.BinAnd, Left: expr, Right: right}
				}
			case OR:
				it.skip()
				var right ast.Expr
				if !it.done() && it.isNonTerminal() {
					right = b.buildExprNonTerminal(&it)
				}
				if right != nil {
					expr = &ast.BinaryExpr{Pos: ast.Pos(*expr.ExprPos()), Op: ast.BinOr, Left: expr, Right: right}
				}
			case QUESTION:
				it.skip()
				var then, els ast.Expr
				if !it.done() && it.isNonTerminal() {
					then = b.buildExpr(it.enter())
				}
				if !it.done() && !it.isNonTerminal() && it.tokenType() == COLON {
					it.skip()
				}
				if !it.done() && it.isNonTerminal() {
					els = b.buildExpr(it.enter())
				}
				expr = &ast.TernaryExpr{Pos: ast.Pos(*expr.ExprPos()), Cond: expr, Then: then, Else: els}
			default:
				it.skip()
			}
		}
	}
	return expr
}

func (b *builder) buildNonIdentPrimary(it nodeIter) ast.Expr {
	// NonIdentPrimary = same as PrimaryExpr except no ident.
	// Historically this also excluded FuncLit, but `t.test("...", func(t, c) {...})`
	// parses the lambda arg through here, so we need FuncLit in-scope.
	return b.buildPrimaryInner(it, true)
}

// --- Types ---

func (b *builder) buildType(it nodeIter) ast.TypeExpr {
	// Type = ident [ dot ident | lt Type gt ] | kw_component | kw_func lparen [TypeList] rparen [Type] | StructDecl | EnumDecl | UnitDecl .
	if it.done() {
		return nil
	}
	if it.isNonTerminal() {
		switch it.symbol() {
		case StructDecl:
			return b.buildStructDecl(it.enter())
		case EnumDecl:
			return b.buildEnumDecl(it.enter())
		case UnitDecl:
			return b.buildUnitDecl(it.enter())
		}
		it.skip()
		return nil
	}
	tok := it.token()
	switch tok.Type {
	case KW_COMPONENT:
		pos := b.posFromToken(it.shift())
		return &ast.NamedType{Pos: pos, Name: "component"}
	case IDENT:
		nameTok := it.shift()
		nt := &ast.NamedType{Pos: b.posFromToken(nameTok), Name: nameTok.Literal}
		if !it.done() && !it.isNonTerminal() {
			next := it.tokenType()
			switch next {
			case DOT:
				it.skip() // dot
				nt.Package = nt.Name
				nt.Name = it.shift().Literal
			case LT:
				it.skip() // lt
				if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
					nt.TypeArgs = b.buildTypeList(it.enter())
				}
				if !it.done() && !it.isNonTerminal() && it.tokenType() == GT {
					it.skip() // gt
				}
			}
		}
		return nt
	case KW_FUNC:
		pos := b.posFromToken(it.shift()) // kw_func
		ft := &ast.FuncType{Pos: pos}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
			it.skip() // lparen
			if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
				ft.Params = b.buildTypeList(it.enter())
			}
			if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
				it.skip() // rparen
			}
		}
		if !it.done() && it.isNonTerminal() && it.symbol() == Type {
			ft.Return = b.buildType(it.enter())
		}
		return ft
	}
	it.skip()
	return nil
}

func (b *builder) buildTypeList(it nodeIter) []ast.TypeExpr {
	// TypeList = Type { comma Type } .
	var types []ast.TypeExpr
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Type {
			types = append(types, b.buildType(it.enter()))
		} else {
			it.skip() // comma
		}
	}
	return types
}

// --- Lambda ---

func (b *builder) buildFuncLit(it nodeIter) *ast.LambdaExpr {
	// FuncLit = kw_func [ lparen [ ParamList ] rparen ] FuncBodyTail .
	pos := b.posFromToken(it.shift()) // kw_func
	lam := &ast.LambdaExpr{Pos: ast.Pos(pos)}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == ParamList {
			lam.Params = b.buildParamList(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncBodyTail {
		sub := it.enter()
		if !sub.done() && !sub.isNonTerminal() && sub.tokenType() == FAT_ARROW {
			sub.skip() // fat_arrow
			if !sub.done() && sub.isNonTerminal() {
				lam.Body = b.buildExpr(sub.enter())
			}
		} else {
			if !sub.done() && sub.isNonTerminal() && sub.symbol() == Type {
				lam.ReturnType = b.buildType(sub.enter())
			}
			if !sub.done() && sub.isNonTerminal() && sub.symbol() == StmtBlock {
				lam.Block = b.buildStmtBlock(sub.enter())
			}
		}
	}
	return lam
}

// --- AssignOp ---

func (b *builder) buildAssignOp(it nodeIter) ast.AssignOp {
	if it.done() {
		return ast.AssignSet
	}
	tok := it.shift()
	switch tok.Type {
	case ASSIGN:
		return ast.AssignSet
	case PLUS_ASSIGN:
		return ast.AssignAdd
	case MINUS_ASSIGN:
		return ast.AssignSub
	case STAR_ASSIGN:
		return ast.AssignMul
	case SLASH_ASSIGN:
		return ast.AssignDiv
	case PERCENT_ASSIGN:
		return ast.AssignMod
	}
	return ast.AssignSet
}

// --- Helpers ---

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.IdentExpr); ok {
		return id.Name
	}
	return ""
}

func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func extractUnitSuffix(raw string) string {
	// Unit literals are like "5px", "1.5em", "100ms"
	// Extract the non-numeric suffix
	i := 0
	for i < len(raw) && (raw[i] >= '0' && raw[i] <= '9' || raw[i] == '.' || raw[i] == '-' || raw[i] == '_') {
		i++
	}
	return raw[i:]
}

var _ = strings.Contains // keep import
