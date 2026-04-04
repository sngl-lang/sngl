package parser

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Parse reads a .sngl file and produces a typed SNGL AST.
func Parse(filename string, r io.Reader) (*ast.Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, err)
	}
	p := &parser{
		filename: filename,
		lex:      newLexer(string(data)),
	}
	p.advance() // prime the first token
	doc := p.parseDocument()
	if len(p.errs) > 0 {
		return doc, p.joinErrors()
	}
	return doc, nil
}

type parser struct {
	filename    string
	lex         *lexer
	cur         Token
	peeked      *Token // one-token lookahead buffer (nil = empty)
	errs        []error
	noStructLit bool // when true, IDENT { is not parsed as struct literal (if/for contexts)
	comments    []ast.Comment
}

func (p *parser) pos() ast.Pos {
	return ast.Pos{Line: p.cur.Line, Column: p.cur.Column}
}

func (p *parser) advance() Token {
	prev := p.cur
	if p.peeked != nil {
		p.cur = *p.peeked
		p.peeked = nil
		return prev
	}
	for {
		p.cur = p.lex.NextToken()
		if p.cur.Type == LINE_COMMENT || p.cur.Type == BLOCK_COMMENT {
			p.comments = append(p.comments, ast.Comment{
				Pos:   ast.Pos{Line: p.cur.Line, Column: p.cur.Column},
				Text:  p.cur.Literal,
				Block: p.cur.Type == BLOCK_COMMENT,
			})
			continue
		}
		break
	}
	return prev
}

// peekToken returns the next token without consuming it.
func (p *parser) peekToken() Token {
	if p.peeked != nil {
		return *p.peeked
	}
	var tok Token
	for {
		tok = p.lex.NextToken()
		if tok.Type == LINE_COMMENT || tok.Type == BLOCK_COMMENT {
			p.comments = append(p.comments, ast.Comment{
				Pos:   ast.Pos{Line: tok.Line, Column: tok.Column},
				Text:  tok.Literal,
				Block: tok.Type == BLOCK_COMMENT,
			})
			continue
		}
		break
	}
	p.peeked = &tok
	return tok
}

func (p *parser) at(t TokenType) bool {
	return p.cur.Type == t
}

// isTypedBlockFunc distinguishes "type {block}" from "Type{struct literal}" after func params.
// Called when cur is IDENT and peek is LBRACE. Returns true if this is a typed block function
// (return type followed by block body), false if IDENT{ starts a struct literal expression.
// Uses a snapshot of the lexer to scan past the { and check whether the content looks like
// a struct literal (IDENT COLON) or RBRACE (empty struct) vs block statements.
func (p *parser) isTypedBlockFunc() bool {
	// Snapshot lexer state
	savedPos := p.lex.pos
	savedLine := p.lex.line
	savedCol := p.lex.col
	savedPrevTok := p.lex.prevTok
	defer func() {
		p.lex.pos = savedPos
		p.lex.line = savedLine
		p.lex.col = savedCol
		p.lex.prevTok = savedPrevTok
	}()

	// We know peeked is LBRACE. Scan past it by reading tokens from the lexer directly.
	// The peeked token consumed the LBRACE, so the lexer is positioned after {.
	// Read the first meaningful token after {.
	var tok Token
	for {
		tok = p.lex.NextToken()
		if tok.Type == LINE_COMMENT || tok.Type == BLOCK_COMMENT || tok.Type == SEMICOLON {
			continue
		}
		break
	}
	if tok.Type == RBRACE {
		// Empty braces: "Type {}" — treat as typed block with empty body
		return true
	}
	if tok.Type == IDENT {
		// Peek at the next token after the IDENT
		var tok2 Token
		for {
			tok2 = p.lex.NextToken()
			if tok2.Type == LINE_COMMENT || tok2.Type == BLOCK_COMMENT || tok2.Type == SEMICOLON {
				continue
			}
			break
		}
		if tok2.Type == COLON {
			// IDENT COLON → struct literal field, not a block body
			return false
		}
	}
	// Everything else (var, return, assignment, etc.) → block body
	return true
}

// isGenericReturnTypeBlock checks whether the current IDENT followed by LT (peek)
// is a generic return type followed by a block body (e.g., list<T> { return ... }).
// Called when cur is IDENT and peek is LT. Uses a lexer snapshot.
func (p *parser) isGenericReturnTypeBlock() bool {
	savedPos := p.lex.pos
	savedLine := p.lex.line
	savedCol := p.lex.col
	savedPrevTok := p.lex.prevTok
	defer func() {
		p.lex.pos = savedPos
		p.lex.line = savedLine
		p.lex.col = savedCol
		p.lex.prevTok = savedPrevTok
	}()

	// Lexer is positioned after the peeked LT token.
	// Skip past <...> by counting angle bracket depth.
	depth := 1
	for depth > 0 {
		tok := p.lex.NextToken()
		switch tok.Type {
		case LT:
			depth++
		case GT:
			depth--
		case EOF, SEMICOLON:
			return false
		}
	}

	// After the closing >, next meaningful token should be LBRACE.
	var tok Token
	for {
		tok = p.lex.NextToken()
		if tok.Type == LINE_COMMENT || tok.Type == BLOCK_COMMENT {
			continue
		}
		break
	}
	if tok.Type != LBRACE {
		return false
	}

	// Check the first token inside { to distinguish block vs struct literal.
	for {
		tok = p.lex.NextToken()
		if tok.Type == LINE_COMMENT || tok.Type == BLOCK_COMMENT || tok.Type == SEMICOLON {
			continue
		}
		break
	}
	if tok.Type == RBRACE {
		return true // empty block
	}
	if tok.Type == IDENT {
		var tok2 Token
		for {
			tok2 = p.lex.NextToken()
			if tok2.Type == LINE_COMMENT || tok2.Type == BLOCK_COMMENT || tok2.Type == SEMICOLON {
				continue
			}
			break
		}
		if tok2.Type == COLON {
			return false // struct literal
		}
	}
	return true
}

func (p *parser) expect(t TokenType) Token {
	if p.cur.Type != t {
		p.errorf("expected %v, got %v (%q)", tokenNames[t], tokenNames[p.cur.Type], p.cur.Literal)
		tok := p.cur
		p.advance() // skip past bad token so the parser doesn't spin
		return tok
	}
	return p.advance()
}

func (p *parser) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.errs = append(p.errs, fmt.Errorf("%s:%d:%d: %s", p.filename, p.cur.Line, p.cur.Column, msg))
}

func (p *parser) joinErrors() error {
	msgs := make([]string, len(p.errs))
	for i, e := range p.errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

func (p *parser) skipSemicolons() {
	for p.at(SEMICOLON) {
		p.advance()
	}
}

// --- Document ---

func (p *parser) parseDocument() *ast.Document {
	doc := &ast.Document{}
	for !p.at(EOF) {
		p.skipSemicolons()
		if p.at(EOF) {
			break
		}

		disabled := false
		if p.at(SLASHDASH) {
			p.advance()
			disabled = true
		}

		switch p.cur.Type {
		case KW_IMPORT:
			imp := p.parseImport()
			imp.Disabled = disabled
			doc.Imports = append(doc.Imports, imp)
		case KW_OUTPUT:
			defaults, outputs := p.parseOutput()
			if defaults != nil {
				doc.OutputDefaults = defaults
			}
			doc.Outputs = append(doc.Outputs, outputs...)
		case KW_STRUCT:
			sd := p.parseStruct()
			sd.Disabled = disabled
			doc.Structs = append(doc.Structs, sd)
		case KW_ENUM:
			ed := p.parseEnum()
			ed.Disabled = disabled
			doc.Enums = append(doc.Enums, ed)
		case KW_UNIT:
			doc.Units = append(doc.Units, p.parseUnitDecl())
		case KW_STYLE:
			doc.Styles = append(doc.Styles, p.parseStyleDecl())
		case KW_TIMER:
			t := p.parseTimer()
			t.Disabled = disabled
			doc.Timers = append(doc.Timers, t)
		case KW_TEST:
			td := p.parseTestDef(true)
			td.Disabled = disabled
			doc.Tests = append(doc.Tests, td)
		case KW_CONST:
			consts := p.parseConstDecl()
			for _, c := range consts {
				c.Disabled = disabled
			}
			doc.Consts = append(doc.Consts, consts...)
		case KW_VAR:
			vars := p.parseVarDecl()
			for _, d := range vars {
				d.Disabled = disabled
			}
			doc.Data = append(doc.Data, vars...)
		case KW_FUNC:
			fn := p.parseFuncDef()
			fn.Disabled = disabled
			doc.Functions = append(doc.Functions, fn)
		case KW_COMPONENT:
			comp := p.parseComponent()
			comp.Disabled = disabled
			if comp.Name == "main" {
				doc.App = &ast.App{Pos: comp.Pos, Children: comp.Body}
				doc.Data = append(doc.Data, comp.Data...)
				doc.Consts = append(doc.Consts, comp.Consts...)
				doc.Functions = append(doc.Functions, comp.Functions...)
				doc.Timers = append(doc.Timers, comp.Timers...)
			} else {
				doc.Components = append(doc.Components, comp)
			}
		default:
			p.errorf("unexpected token %v at top level", p.cur.Literal)
			p.advance()
		}
	}
	doc.Comments = p.comments
	return doc
}

func (p *parser) parseImport() *ast.Import {
	pos := p.pos()
	p.expect(KW_IMPORT)
	path := p.expect(STRING)
	imp := &ast.Import{Pos: pos, Path: path.Literal}
	rest := path.Literal
	if scheme, after, ok := strings.Cut(rest, "://"); ok {
		imp.Scheme = scheme
		rest = after
	}
	ns := rest
	if i := strings.LastIndex(ns, "/"); i >= 0 {
		ns = ns[i+1:]
	}
	// Strip file extensions for namespace derivation (e.g. "types.d.ts" → "types")
	if i := strings.Index(ns, "."); i >= 0 {
		ns = ns[:i]
	}
	imp.Namespace = ns
	return imp
}

func (p *parser) parseOutput() (defaults map[string]string, outputs []*ast.Output) {
	p.expect(KW_OUTPUT)
	if p.at(LPAREN) {
		defaults = p.parseKVList()
	}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		lang := p.expect(IDENT).Literal
		p.expect(LBRACE)
		for !p.at(RBRACE) && !p.at(EOF) {
			p.skipSemicolons()
			if p.at(RBRACE) {
				break
			}
			pos := p.pos()
			platform := p.expect(IDENT).Literal
			opts := map[string]string{}
			if p.at(LPAREN) {
				opts = p.parseKVList()
			}
			outputs = append(outputs, &ast.Output{Pos: pos, Lang: lang, Platform: platform, Options: opts})
			if p.at(SEMICOLON) {
				p.advance()
			}
		}
		p.expect(RBRACE)
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return defaults, outputs
}

func (p *parser) parseKVList() map[string]string {
	p.expect(LPAREN)
	opts := map[string]string{}
	for !p.at(RPAREN) && !p.at(EOF) {
		key := p.expect(IDENT).Literal
		p.expect(ASSIGN)
		val := p.expect(STRING).Literal
		opts[key] = val
		if !p.at(RPAREN) {
			p.expect(COMMA)
		}
	}
	p.expect(RPAREN)
	return opts
}

func (p *parser) parseStruct() *ast.StructDef {
	pos := p.pos()
	p.expect(KW_STRUCT)
	name := p.expect(IDENT).Literal
	p.expect(LBRACE)
	var fields []*ast.StructField
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		fpos := p.pos()
		fname := p.expect(IDENT).Literal
		ftype := p.parseTypeString()
		var fdefault ast.Expr
		if p.at(ASSIGN) {
			p.advance()
			fdefault = p.parseExprAsExpr()
		}
		fields = append(fields, &ast.StructField{Pos: fpos, Name: fname, Type: ftype, Default: fdefault})
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return &ast.StructDef{Pos: pos, Name: name, Fields: fields}
}

func (p *parser) parseEnum() *ast.EnumDef {
	pos := p.pos()
	p.expect(KW_ENUM)
	name := p.expect(IDENT).Literal
	p.expect(LBRACE)
	var values []string
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		values = append(values, p.expect(IDENT).Literal)
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RBRACE)
	return &ast.EnumDef{Pos: pos, Name: name, Values: values}
}

func (p *parser) parseStyleDecl() *ast.StyleDecl {
	pos := p.pos()
	p.expect(KW_STYLE)
	name := p.expect(IDENT).Literal
	p.expect(LBRACE)
	props := p.parseStyleProps()
	p.expect(RBRACE)
	return &ast.StyleDecl{Pos: pos, Name: name, Props: props}
}

func (p *parser) parseStyleProps() map[string]ast.Expr {
	props := map[string]ast.Expr{}
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		name := p.expect(IDENT).Literal
		p.expect(ASSIGN)
		props[name] = p.parseExprAsExpr()
		p.skipSemicolons()
	}
	return props
}

// --- Component ---

type componentState struct {
	Data   []*ast.Data
	Consts []*ast.Const
}

func (p *parser) parseComponent() *ast.Component {
	pos := p.pos()
	p.expect(KW_COMPONENT)
	name := p.expect(IDENT).Literal

	comp := &ast.Component{Pos: pos, Name: name}

	// Parse optional param list: component Name(param1 = default, @save, :count int) { ... }
	if p.at(LPAREN) {
		p.advance()
		for !p.at(RPAREN) && !p.at(EOF) {
			if p.at(AT) {
				// Event declaration: @eventName
				p.advance()
				ename := p.expect(IDENT).Literal
				comp.EventDecls = append(comp.EventDecls, &ast.EventDecl{Pos: p.pos(), Name: ename})
			} else if p.at(COLON) {
				// Bidirectional param: :name type = default
				p.advance()
				param := p.parseComponentParam()
				param.Bidirectional = true
				comp.Params = append(comp.Params, param)
				comp.EventDecls = append(comp.EventDecls, &ast.EventDecl{Pos: param.Pos, Name: param.Name})
			} else {
				comp.Params = append(comp.Params, p.parseComponentParam())
			}
			if p.at(COMMA) {
				p.advance()
			}
		}
		p.expect(RPAREN)
	}

	p.expect(LBRACE)
	cs := &componentState{}

	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}

		disabled := false
		if p.at(SLASHDASH) {
			p.advance()
			disabled = true
		}

		switch p.cur.Type {
		case KW_PROP:
			comp.PropDecls = append(comp.PropDecls, p.parsePropDecl())
		case COLON:
			// Bidirectional prop: :value string — desugars to prop + event
			p.advance()
			pd := p.parsePropDeclBody()
			pd.Bidirectional = true
			comp.PropDecls = append(comp.PropDecls, pd)
			comp.EventDecls = append(comp.EventDecls, &ast.EventDecl{Pos: pd.Pos, Name: pd.Name, PayloadType: "ChangeEvent"})
		case AT:
			// Event declaration: @click ClickEvent
			comp.EventDecls = append(comp.EventDecls, p.parseEventDecl())
		case KW_CHILDREN:
			p.advance()
			comp.ChildPolicy = p.expect(IDENT).Literal
		case KW_CONST:
			consts := p.parseConstDecl()
			for _, c := range consts {
				c.Disabled = disabled
			}
			cs.Consts = append(cs.Consts, consts...)
		case KW_VAR:
			vars := p.parseVarDecl()
			for _, d := range vars {
				d.Disabled = disabled
			}
			cs.Data = append(cs.Data, vars...)
		case KW_FUNC:
			fn := p.parseFuncDef()
			fn.Disabled = disabled
			comp.Functions = append(comp.Functions, fn)
		case KW_TIMER:
			t := p.parseTimer()
			t.Disabled = disabled
			comp.Timers = append(comp.Timers, t)
		default:
			node := p.parseNodeOrControl()
			node.Disabled = disabled
			comp.Body = append(comp.Body, node)
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)

	comp.Consts = cs.Consts
	comp.Data = cs.Data
	return comp
}

func (p *parser) parseTestDef(topLevel bool) *ast.TestDef {
	pos := p.pos()
	p.expect(KW_TEST)
	td := &ast.TestDef{Pos: pos}
	if topLevel {
		td.Component = p.expect(IDENT).Literal
	}
	if p.at(STRING) {
		td.Desc = p.expect(STRING).Literal
	}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if p.at(KW_TEST) {
			td.Subtests = append(td.Subtests, p.parseTestDef(false))
		} else {
			td.Body = append(td.Body, p.parseStmt())
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return td
}

// parseComponentParam parses a single component parameter inside ().
// Syntax: name = default | name type | name type = default | name type required
func (p *parser) parseComponentParam() *ast.Param {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	param := &ast.Param{Pos: pos, Name: name}

	if p.at(ASSIGN) {
		p.advance()
		param.Default = p.parseExprAsExpr()
	} else if !p.at(COMMA) && !p.at(RPAREN) && !p.at(EOF) {
		typeHint := p.parseTypeString()
		param.Default.TypeHint = typeHint
		if p.at(ASSIGN) {
			p.advance()
			param.Default = p.parseExprAsExpr()
			param.Default.TypeHint = typeHint
		}
	}
	if p.at(IDENT) && p.cur.Literal == "required" {
		p.advance()
		param.Required = true
	}
	return param
}

func (p *parser) parsePropDecl() *ast.PropDecl {
	p.expect(KW_PROP)
	return p.parsePropDeclBody()
}

func (p *parser) parsePropDeclBody() *ast.PropDecl {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	typeHint := p.parseTypeString()
	var enumValues []string
	if p.at(KW_ENUM) {
		p.advance()
		p.expect(LPAREN)
		for !p.at(RPAREN) && !p.at(EOF) {
			enumValues = append(enumValues, p.expect(IDENT).Literal)
			if p.at(COMMA) {
				p.advance()
			}
		}
		p.expect(RPAREN)
	}
	return &ast.PropDecl{Pos: pos, Name: name, TypeHint: typeHint, Enum: enumValues}
}

func (p *parser) parseEventDecl() *ast.EventDecl {
	pos := p.pos()
	p.expect(AT)
	name := p.expect(IDENT).Literal
	payloadType := p.expect(IDENT).Literal
	return &ast.EventDecl{Pos: pos, Name: name, PayloadType: payloadType}
}

func (p *parser) parseConstDecl() []*ast.Const {
	p.expect(KW_CONST)
	if p.at(LPAREN) {
		return p.parseGroupedConsts()
	}
	return p.parseConstSpec()
}

func (p *parser) parseConstSpec() []*ast.Const {
	pos := p.pos()
	name := p.expect(IDENT).Literal

	// Check for multi-name: NAME, NAME, ... TYPE = expr
	if p.at(COMMA) && p.isMultiName() {
		names := []string{name}
		for p.at(COMMA) && p.isMultiName() {
			p.advance()
			names = append(names, p.expect(IDENT).Literal)
		}
		typeHint := ""
		if !p.at(ASSIGN) {
			typeHint = p.parseTypeString()
		}
		p.expect(ASSIGN)
		init := p.parseExprAsExpr()
		if typeHint != "" {
			init.TypeHint = typeHint
		}
		var consts []*ast.Const
		for _, n := range names {
			consts = append(consts, &ast.Const{Pos: pos, Name: n, Init: init})
		}
		return consts
	}

	// Single const
	typeHint := ""
	if !p.at(ASSIGN) {
		typeHint = p.parseTypeString()
	}
	p.expect(ASSIGN)
	init := p.parseExprAsExpr()
	if typeHint != "" {
		init.TypeHint = typeHint
	}
	return []*ast.Const{{Pos: pos, Name: name, Init: init}}
}

func (p *parser) parseGroupedConsts() []*ast.Const {
	p.expect(LPAREN)
	var consts []*ast.Const
	for !p.at(RPAREN) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RPAREN) {
			break
		}
		consts = append(consts, p.parseConstSpec()...)
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RPAREN)
	return consts
}

func (p *parser) parseVarDecl() []*ast.Data {
	p.expect(KW_VAR)
	if p.at(LPAREN) {
		return p.parseGroupedVars()
	}
	return p.parseVarSpec()
}

// parseVarSpec parses one or more var declarations that may share a type.
// Supports: var x int, var x, y int, var x, y int = 0
func (p *parser) parseVarSpec() []*ast.Data {
	pos := p.pos()
	name := p.expect(IDENT).Literal

	// Check for multi-name: name1, name2, ... type
	// Multi-name requires an explicit type after the last name.
	if p.at(COMMA) && p.isMultiName() {
		names := []string{name}
		for p.at(COMMA) && p.isMultiName() {
			p.advance() // consume comma
			names = append(names, p.expect(IDENT).Literal)
		}
		typeHint := p.parseTypeString()
		var init ast.Expr
		if p.at(ASSIGN) {
			p.advance()
			init = p.parseExprAsExpr()
			init.TypeHint = typeHint
		} else {
			init.TypeHint = typeHint
		}
		var vars []*ast.Data
		for _, n := range names {
			d := &ast.Data{Pos: pos, Name: n, Init: init}
			if strings.HasPrefix(typeHint, "func:") {
				d.IsFunc = true
				params, ret := splitFuncBody(typeHint[5:])
				d.ParamTypes = params
				d.ReturnType = ret
			}
			p.parseVarModifiers(d)
			vars = append(vars, d)
		}
		return vars
	}

	d := &ast.Data{Pos: pos, Name: name}
	p.finishSingleVar(d)
	return []*ast.Data{d}
}

// isMultiName peeks ahead to check if COMMA IDENT is followed by something
// other than ASSIGN (which would indicate separate declarations).
func (p *parser) isMultiName() bool {
	if !p.at(COMMA) {
		return false
	}
	peek := p.peekToken()
	if peek.Type != IDENT {
		return false
	}
	// Save state to look past the peeked IDENT
	savedPos := p.lex.pos
	savedLine := p.lex.line
	savedCol := p.lex.col
	savedPrevTok := p.lex.prevTok
	tok := p.lex.NextToken() // read token after peeked IDENT
	p.lex.pos = savedPos
	p.lex.line = savedLine
	p.lex.col = savedCol
	p.lex.prevTok = savedPrevTok
	// If token after the second IDENT is ASSIGN, it's a separate decl (y = expr)
	return tok.Type != ASSIGN
}

func (p *parser) finishSingleVar(d *ast.Data) {
	// var name = expr | var name type = expr | var name type [modifiers]
	if p.at(ASSIGN) {
		p.advance()
		d.Init = p.parseExprAsExpr()
	} else if !p.at(SEMICOLON) && !p.at(RPAREN) && !p.at(EOF) && !p.at(KW_EXTERN) && !p.at(KW_TRIGGER) {
		typeHint := p.parseTypeString()
		d.Init.TypeHint = typeHint
		// Parse func type components into Data fields
		if strings.HasPrefix(typeHint, "func:") {
			d.IsFunc = true
			params, ret := splitFuncBody(typeHint[5:])
			d.ParamTypes = params
			d.ReturnType = ret
		}
		if p.at(ASSIGN) {
			p.advance()
			d.Init = p.parseExprAsExpr()
			d.Init.TypeHint = typeHint
		}
	}

	// Parse modifiers
	p.parseVarModifiers(d)
}

func (p *parser) parseVarModifiers(d *ast.Data) {
	for p.at(KW_EXTERN) || p.at(KW_TRIGGER) {
		if p.at(KW_EXTERN) {
			p.advance()
			d.Extern = true
		}
		if p.at(KW_TRIGGER) {
			p.advance()
			// Optional explicit name
			if p.at(LPAREN) {
				p.advance()
				triggerName := p.expect(STRING).Literal
				p.expect(RPAREN)
				d.Trigger = triggerName
			} else {
				// Auto-generate trigger name
				if len(d.Name) > 0 {
					d.Trigger = "On" + strings.ToUpper(d.Name[:1]) + d.Name[1:] + "Changed"
				} else {
					d.Trigger = "OnChanged"
				}
			}
		}
	}
}

func (p *parser) parseGroupedVars() []*ast.Data {
	p.expect(LPAREN)
	var vars []*ast.Data
	for !p.at(RPAREN) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RPAREN) {
			break
		}
		vars = append(vars, p.parseVarSpec()...)
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RPAREN)
	return vars
}

// --- Function definitions ---

func (p *parser) parseTimer() *ast.Timer {
	pos := p.pos()
	p.expect(KW_TIMER)
	interval := p.parseExprAsExpr()
	active := p.expect(IDENT).Literal
	p.expect(LBRACE)
	body := p.parseStmtList()
	p.expect(RBRACE)
	return &ast.Timer{Pos: pos, Interval: interval, Active: active, Body: body}
}

func (p *parser) parseFuncDef() *ast.FuncDef {
	pos := p.pos()
	p.expect(KW_FUNC)
	name := p.expect(IDENT).Literal
	if p.at(DOT) {
		p.advance()
		// Allow keywords as method names (e.g., list.return)
		tok := p.cur
		if tok.Type == IDENT || (tok.Type >= KW_IMPORT && tok.Type <= KW_NULL) {
			name = name + "." + tok.Literal
			p.advance()
		} else {
			name = name + "." + p.expect(IDENT).Literal
		}
	}

	// Parse optional type parameters: func name<T, U>(...)
	var typeParams []string
	if p.at(LT) {
		p.advance()
		for {
			typeParams = append(typeParams, p.expect(IDENT).Literal)
			if !p.at(COMMA) {
				break
			}
			p.advance()
		}
		p.expect(GT)
	}

	// Parse parameter list
	p.expect(LPAREN)
	var params []*ast.FuncParam
	for !p.at(RPAREN) && !p.at(EOF) {
		ppos := p.pos()
		pname := p.expect(IDENT).Literal
		ptype := p.parseTypeString()
		params = append(params, &ast.FuncParam{Pos: ppos, Name: pname, Type: ptype})
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RPAREN)

	fd := &ast.FuncDef{Pos: pos, Name: name, TypeParams: typeParams, Params: params}

	if p.at(LBRACE) {
		// Void block form: func name(params) { ... }
		p.advance()
		fd.Block = p.parseFuncBlock()
		p.expect(RBRACE)
	} else if p.at(IDENT) && p.peekToken().Type == LBRACE && p.isTypedBlockFunc() {
		// Typed block form: func name(params) type { ... }
		fd.ReturnType = p.parseTypeString()
		p.advance() // consume LBRACE
		fd.Block = p.parseFuncBlock()
		p.expect(RBRACE)
	} else if p.at(IDENT) && p.peekToken().Type == LT && p.isGenericReturnTypeBlock() {
		// Typed block form with generic return type: func name(params) list<T> { ... }
		fd.ReturnType = p.parseTypeString()
		p.advance() // consume LBRACE
		fd.Block = p.parseFuncBlock()
		p.expect(RBRACE)
	} else {
		// Expression form: func name(params) expr
		fd.Body = p.parseExprAsExpr()
	}

	return fd
}

func (p *parser) parseFuncBlock() *ast.FuncBlock {
	fb := &ast.FuncBlock{}
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if p.at(KW_RETURN) {
			p.advance()
			if p.at(SEMICOLON) || p.at(RBRACE) {
				fb.Return = nil // bare return
			} else {
				fb.Return = p.parseExpression()
			}
			p.skipSemicolons()
			break
		}
		if p.at(KW_VAR) {
			p.advance()
			vname := p.expect(IDENT).Literal
			vtype := ""
			if !p.at(ASSIGN) {
				vtype = p.parseTypeString()
			}
			p.expect(ASSIGN)
			init := p.parseExpression()
			fb.Stmts = append(fb.Stmts, &ast.VarStmt{Name: vname, Type: vtype, Init: init})
		} else {
			fb.Stmts = append(fb.Stmts, p.parseStmt())
		}
		p.skipSemicolons()
	}
	return fb
}

// --- Type parsing ---

func (p *parser) parseTypeString() string {
	if p.at(KW_FUNC) {
		return p.parseFuncType()
	}
	if p.at(KW_ENUM) {
		return p.parseInlineEnumType()
	}
	name := p.expect(IDENT).Literal
	if p.at(DOT) {
		p.advance()
		name = name + "." + p.expect(IDENT).Literal
	}
	if p.at(LT) {
		p.advance()
		var inner strings.Builder
		inner.WriteString(p.parseTypeString())
		for p.at(COMMA) {
			p.advance()
			inner.WriteString("," + p.parseTypeString())
		}
		p.expect(GT)
		// Convert list<Todo> → list:Todo, option<T> → option:T for compatibility
		if name == "list" || name == "option" {
			return name + ":" + inner.String()
		}
		return name + "<" + inner.String() + ">"
	}
	return name
}

func (p *parser) parseFuncType() string {
	p.expect(KW_FUNC)
	p.expect(LPAREN)
	var params []string
	for !p.at(RPAREN) && !p.at(EOF) {
		params = append(params, p.parseTypeString())
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RPAREN)
	result := "func:" + strings.Join(params, ":")
	if p.at(ARROW) {
		p.advance()
		retType := p.parseTypeString()
		result += "~" + retType
	}
	return result
}

func (p *parser) parseInlineEnumType() string {
	p.expect(KW_ENUM)
	p.expect(LT)
	var values []string
	values = append(values, p.expect(IDENT).Literal)
	for p.at(PIPE) {
		p.advance()
		values = append(values, p.expect(IDENT).Literal)
	}
	p.expect(GT)
	return "enum:" + strings.Join(values, "|")
}

// --- Visual Nodes ---

func (p *parser) parseNodeOrControl() *ast.VisualNode {
	switch p.cur.Type {
	case KW_IF:
		return p.parseIfNode()
	case KW_FOR:
		return p.parseForNode()
	default:
		return p.parseVisualNode()
	}
}

func (p *parser) parseIfNode() *ast.VisualNode {
	p.expect(KW_IF)
	p.noStructLit = true
	cond := p.parseExprAsExpr()
	p.noStructLit = false
	p.expect(LBRACE)
	p.skipSemicolons()
	vn := p.parseNodeOrControl()
	p.skipSemicolons()
	p.expect(RBRACE)
	vn.If = &cond
	return vn
}

func (p *parser) parseForNode() *ast.VisualNode {
	p.expect(KW_FOR)
	variable := p.expect(IDENT).Literal
	indexVar := ""
	if p.at(COMMA) {
		p.advance()
		indexVar = p.expect(IDENT).Literal
	}
	p.expect(KW_IN)
	p.noStructLit = true
	iterable := p.parseExprAsExpr()
	p.noStructLit = false
	p.expect(LBRACE)
	p.skipSemicolons()
	vn := p.parseNodeOrControl()
	p.skipSemicolons()
	p.expect(RBRACE)
	fc := &ast.ForClause{
		Variable: variable,
		IndexVar: indexVar,
		Iterable: iterable,
	}
	if p.at(KW_ELSE) {
		p.advance()
		p.expect(LBRACE)
		for !p.at(RBRACE) && !p.at(EOF) {
			p.skipSemicolons()
			if p.at(RBRACE) {
				break
			}
			fc.Else = append(fc.Else, p.parseNodeOrControl())
			p.skipSemicolons()
		}
		p.expect(RBRACE)
	}
	vn.For = fc
	return vn
}

func (p *parser) parseVisualNode() *ast.VisualNode {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	if p.at(DOT) {
		p.advance()
		name = name + "." + p.expect(IDENT).Literal
	}
	vn := &ast.VisualNode{
		Pos:       pos,
		Component: name,
	}

	// Optional element ID: #id
	if p.at(ELEMENT_REF) {
		vn.ID = p.advance().Literal
	}

	// Optional props list
	if p.at(LPAREN) {
		p.parsePropList(vn)
	}

	// Optional children block
	if p.at(LBRACE) {
		p.advance()
		for !p.at(RBRACE) && !p.at(EOF) {
			p.skipSemicolons()
			if p.at(RBRACE) {
				break
			}
			if p.at(AT) {
				// Attribute node
				p.advance()
				attrName := p.expect(IDENT).Literal
				p.expect(LPAREN)
				attrProps := map[string]ast.Expr{}
				for !p.at(RPAREN) && !p.at(EOF) {
					key := p.expect(IDENT).Literal
					p.expect(ASSIGN)
					attrProps[key] = p.parseExprAsExpr()
					if p.at(COMMA) {
						p.advance()
					}
				}
				p.expect(RPAREN)
				if vn.AttrNodes == nil {
					vn.AttrNodes = map[string]*ast.AttrNode{}
				}
				vn.AttrNodes[attrName] = &ast.AttrNode{Pos: p.pos(), Name: attrName, Props: attrProps}
			} else {
				vn.Children = append(vn.Children, p.parseNodeOrControl())
			}
			p.skipSemicolons()
		}
		p.expect(RBRACE)
	}

	return vn
}

func (p *parser) parsePropList(vn *ast.VisualNode) {
	p.expect(LPAREN)
	for !p.at(RPAREN) && !p.at(EOF) {
		if p.at(COLON) {
			// Bidirectional binding: :name=expr
			p.advance()
			bname := p.expect(IDENT).Literal
			p.expect(ASSIGN)
			expr := p.parseExprAsExpr()
			if vn.Bindings == nil {
				vn.Bindings = map[string]ast.Expr{}
			}
			vn.Bindings[bname] = expr
		} else if p.at(AT) {
			// Event handler: @event={ stmts }
			p.advance()
			eventName := p.expect(IDENT).Literal
			p.expect(ASSIGN)
			p.expect(LBRACE)
			stmts := p.parseStmtList()
			p.expect(RBRACE)
			if vn.Events == nil {
				vn.Events = map[string]ast.Expr{}
			}
			vn.Events[eventName] = ast.Expr{SNGL: stmts}
		} else {
			// Regular prop: name=expr (style keyword is also valid as a prop name)
			var propName string
			if p.at(KW_STYLE) {
				propName = p.advance().Literal
			} else {
				propName = p.expect(IDENT).Literal
			}
			p.expect(ASSIGN)
			expr := p.parseExprAsExpr()
			switch propName {
			case "key":
				vn.Key = &expr
			case "class":
				vn.Class = &expr
			case "ref":
				vn.Ref = &expr
			default:
				if vn.Props == nil {
					vn.Props = map[string]ast.Expr{}
				}
				vn.Props[propName] = expr
			}
		}
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RPAREN)
}

// --- Statement parsing ---

func (p *parser) parseStmtList() ast.Node {
	var stmts []ast.Node
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		stmts = append(stmts, p.parseStmt())
		p.skipSemicolons()
	}
	if len(stmts) == 1 {
		return stmts[0]
	}
	return &ast.StmtBlock{Stmts: stmts}
}

func (p *parser) parseStmt() ast.Node {
	// Emit statement: @name(args)
	if p.at(AT) {
		p.advance()
		name := p.expect(IDENT).Literal
		p.expect(LPAREN)
		var args []ast.Node
		for !p.at(RPAREN) && !p.at(EOF) {
			args = append(args, p.parseExpression())
			if p.at(COMMA) {
				p.advance()
			}
		}
		p.expect(RPAREN)
		return &ast.EmitStmt{Name: name, Args: args}
	}

	// Parse LValue expression first
	target := p.parseExpression()

	// Check for assignment operators
	switch p.cur.Type {
	case ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignSet, Value: value}
	case PLUS_ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignAdd, Value: value}
	case MINUS_ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignSub, Value: value}
	case STAR_ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignMul, Value: value}
	case SLASH_ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignDiv, Value: value}
	case PERCENT_ASSIGN:
		p.advance()
		value := p.parseExpression()
		return &ast.AssignStmt{Target: target, Op: ast.AssignMod, Value: value}
	case BANGBANG:
		p.advance()
		return &ast.ToggleStmt{Target: target}
	}

	// Method calls and function calls are valid as statements
	if call, ok := target.(*ast.CallExpr); ok {
		return &ast.CallStmt{Call: call}
	}
	return target
}

// --- Expression parsing (precedence climbing) ---

func (p *parser) parseExpression() ast.Node {
	return p.parseTernary()
}

// parseLambdaExpr parses: func(param, param Type) expr
// Parameter types are optional. Unambiguous LL(1) — func keyword starts it.
func (p *parser) parseLambdaExpr() ast.Node {
	p.advance() // consume func
	p.expect(LPAREN)
	var params []string
	var paramTypes []string
	for !p.at(RPAREN) {
		name := p.expect(IDENT).Literal
		params = append(params, name)
		// Optional type annotation
		typeHint := ""
		if p.at(IDENT) {
			typeHint = p.advance().Literal
		}
		paramTypes = append(paramTypes, typeHint)
		if !p.at(RPAREN) {
			p.expect(COMMA)
		}
	}
	p.expect(RPAREN)
	body := p.parseExpression()
	return &ast.LambdaExpr{Params: params, ParamTypes: paramTypes, Body: body}
}

func (p *parser) parseTernary() ast.Node {
	cond := p.parseLogicalOr()
	if p.at(QUESTION) {
		p.advance()
		then := p.parseExpression()
		p.expect(COLON)
		els := p.parseExpression()
		return &ast.TernaryExpr{Cond: cond, Then: then, Else: els}
	}
	return cond
}

func (p *parser) parseLogicalOr() ast.Node {
	left := p.parseLogicalAnd()
	for p.at(OR) {
		p.advance()
		right := p.parseLogicalAnd()
		left = &ast.BinaryExpr{Op: ast.BinOr, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseLogicalAnd() ast.Node {
	left := p.parseEquality()
	for p.at(AND) {
		p.advance()
		right := p.parseEquality()
		left = &ast.BinaryExpr{Op: ast.BinAnd, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseEquality() ast.Node {
	left := p.parseComparison()
	for p.at(EQ) || p.at(NEQ) {
		op := ast.BinEq
		if p.cur.Type == NEQ {
			op = ast.BinNeq
		}
		p.advance()
		right := p.parseComparison()
		left = &ast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseComparison() ast.Node {
	left := p.parseAddition()
	for p.at(LT) || p.at(GT) || p.at(LTE) || p.at(GTE) {
		var op ast.BinaryOp
		switch p.cur.Type {
		case LT:
			op = ast.BinLt
		case GT:
			op = ast.BinGt
		case LTE:
			op = ast.BinLte
		case GTE:
			op = ast.BinGte
		}
		p.advance()
		right := p.parseAddition()
		left = &ast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseAddition() ast.Node {
	left := p.parseMultiplication()
	for p.at(PLUS) || p.at(MINUS) {
		op := ast.BinAdd
		if p.cur.Type == MINUS {
			op = ast.BinSub
		}
		p.advance()
		right := p.parseMultiplication()
		left = &ast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseMultiplication() ast.Node {
	left := p.parseUnary()
	for p.at(STAR) || p.at(SLASH) || p.at(PERCENT) {
		var op ast.BinaryOp
		switch p.cur.Type {
		case STAR:
			op = ast.BinMul
		case SLASH:
			op = ast.BinDiv
		case PERCENT:
			op = ast.BinMod
		}
		p.advance()
		right := p.parseUnary()
		left = &ast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *parser) parseUnary() ast.Node {
	if p.at(BANG) {
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Op: ast.UnaryNot, Operand: operand}
	}
	if p.at(MINUS) {
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Op: ast.UnaryNeg, Operand: operand}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Node {
	node := p.parsePrimary()
	for {
		if p.at(DOT) {
			p.advance()
			var field string
			if p.at(AT) {
				p.advance()
				field = "@" + p.expect(IDENT).Literal
			} else if p.at(IDENT) || p.cur.Type.IsKeyword() {
				field = p.cur.Literal
				p.advance()
			} else {
				field = p.expect(IDENT).Literal // will error
			}
			// Method call: .field(args)
			if p.at(LPAREN) {
				p.advance()
				var args []ast.Node
				for !p.at(RPAREN) && !p.at(EOF) {
					args = append(args, p.parseExpression())
					if p.at(COMMA) {
						p.advance()
					}
				}
				p.expect(RPAREN)
				node = &ast.MethodExpr{Receiver: node, Method: field, Args: args}
			} else if p.at(LBRACE) && !p.noStructLit {
				// Qualified struct literal: ns.Type{field: value}
				if ident, ok := node.(*ast.IdentExpr); ok {
					node = p.parseStructLiteral(ident.Name + "." + field)
				} else {
					node = &ast.SelectExpr{Operand: node, Field: field}
				}
			} else {
				node = &ast.SelectExpr{Operand: node, Field: field}
			}
		} else if p.at(LBRACKET) {
			p.advance()
			index := p.parseExpression()
			p.expect(RBRACKET)
			node = &ast.IndexExpr{Operand: node, Index: index}
		} else if p.at(LPAREN) {
			// Function call on identifier
			if ident, ok := node.(*ast.IdentExpr); ok {
				p.advance()
				var args []ast.Node
				for !p.at(RPAREN) && !p.at(EOF) {
					args = append(args, p.parseExpression())
					if p.at(COMMA) {
						p.advance()
					}
				}
				p.expect(RPAREN)
				node = &ast.CallExpr{Func: ident.Name, Args: args}
			} else {
				break
			}
		} else {
			break
		}
	}
	return node
}

func (p *parser) parsePrimary() ast.Node {
	switch p.cur.Type {
	case INT:
		tok := p.advance()
		val, err := strconv.Atoi(tok.Literal)
		if err != nil {
			p.errorf("invalid integer literal %q: %v", tok.Literal, err)
		}
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralInt}
	case FLOAT:
		tok := p.advance()
		val, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			p.errorf("invalid float literal %q: %v", tok.Literal, err)
		}
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralFloat}
	case STRING:
		tok := p.advance()
		return p.parseStringWithInterpolation(tok.Literal)
	case COLOR:
		tok := p.advance()
		return &ast.LiteralExpr{Value: tok.Literal, Kind: ast.LiteralColor}
	case UNIT_LITERAL:
		tok := p.advance()
		num, suffix := splitUnitLiteral(tok.Literal)
		return &ast.LiteralExpr{Value: ast.UnitLiteral{Number: num, Suffix: suffix}, Kind: ast.LiteralUnit}
	case KW_TRUE:
		p.advance()
		return &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}
	case KW_FALSE:
		p.advance()
		return &ast.LiteralExpr{Value: false, Kind: ast.LiteralBool}
	case KW_NULL:
		p.advance()
		return &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}
	case LPAREN:
		p.advance()
		expr := p.parseExpression()
		p.expect(RPAREN)
		return expr
	case LBRACKET:
		return p.parseListLiteral()
	case LBRACE:
		// Anonymous struct literal: {field=val, ...}
		return p.parseAnonStructLiteral()
	case KW_FUNC:
		// Lambda expression: func(params) expr
		// Distinct from func declaration (which has a name after func).
		// In expression context, func is always a lambda.
		return p.parseLambdaExpr()
	case ELEMENT_REF:
		tok := p.advance()
		return &ast.ElementRefExpr{Name: tok.Literal}
	case IDENT, KW_EVENT:
		// KW_EVENT is allowed as an identifier in expression context
		// (it refers to the event payload variable in event handlers).
		tok := p.advance()
		// Check for struct literal: Name{field: value}
		// Disabled in if/for contexts (same restriction as Go).
		if p.at(LBRACE) && !p.noStructLit {
			return p.parseStructLiteral(tok.Literal)
		}
		return &ast.IdentExpr{Name: tok.Literal}
	default:
		p.errorf("unexpected token in expression: %v (%q)", tokenNames[p.cur.Type], p.cur.Literal)
		p.advance()
		return &ast.LiteralExpr{Kind: ast.LiteralNull}
	}
}

func (p *parser) parseListLiteral() ast.Node {
	p.expect(LBRACKET)
	var elems []ast.Node
	for !p.at(RBRACKET) && !p.at(EOF) {
		if p.at(ELLIPSIS) {
			p.advance()
			elems = append(elems, &ast.SpreadExpr{Operand: p.parseExpression()})
		} else {
			elems = append(elems, p.parseExpression())
		}
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RBRACKET)
	return &ast.ListExpr{Elements: elems}
}

func (p *parser) parseStructLiteral(name string) ast.Node {
	p.expect(LBRACE)
	var fields []ast.StructFieldLit
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if p.at(ELLIPSIS) {
			p.advance()
			operand := p.parseExpression()
			fields = append(fields, ast.StructFieldLit{Value: operand, Spread: true})
		} else {
			fname := p.expect(IDENT).Literal
			p.expect(COLON)
			fval := p.parseExpression()
			fields = append(fields, ast.StructFieldLit{Name: fname, Value: fval})
		}
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return &ast.StructExpr{Name: name, Fields: fields}
}

// parseAnonStructLiteral parses an anonymous struct literal: {field=val, field2=val2}.
// Uses = as field separator (not :). Used for style props and other type-inferred contexts.
func (p *parser) parseAnonStructLiteral() ast.Node {
	p.expect(LBRACE)
	var fields []ast.StructFieldLit
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if p.at(ELLIPSIS) {
			p.advance()
			operand := p.parseExpression()
			fields = append(fields, ast.StructFieldLit{Value: operand, Spread: true})
		} else {
			fname := p.expect(IDENT).Literal
			p.expect(ASSIGN)
			fval := p.parseExpression()
			fields = append(fields, ast.StructFieldLit{Name: fname, Value: fval})
		}
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return &ast.StructExpr{Name: "", Fields: fields}
}

func (p *parser) parseStringWithInterpolation(raw string) ast.Node {
	// Check for interpolation markers {expr}
	if !strings.Contains(raw, "{") {
		return &ast.LiteralExpr{Value: raw, Kind: ast.LiteralString}
	}

	var parts []ast.Node
	i := 0
	runes := []rune(raw)
	var buf strings.Builder

	for i < len(runes) {
		if runes[i] == '{' {
			// Find matching }
			start := i
			i++
			depth := 1
			var exprBuf strings.Builder
			for i < len(runes) && depth > 0 {
				if runes[i] == '{' {
					depth++
				} else if runes[i] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				exprBuf.WriteRune(runes[i])
				i++
			}
			if depth > 0 {
				p.errorf("unterminated interpolation in string")
				buf.WriteRune('{')
				i = start + 1
				continue
			}
			if strings.TrimSpace(exprBuf.String()) == "" {
				p.errorf("empty interpolation in string")
				buf.WriteRune('{')
				i = start + 1
				continue
			}
			i++ // consume closing }
			// Parse the inner expression.
			innerParser := &parser{
				filename: p.filename,
				lex:      newLexer(exprBuf.String()),
			}
			innerParser.advance()
			expr := innerParser.parseExpression()
			// Allow trailing semicolons from automatic semicolon insertion at EOF.
			if innerParser.at(SEMICOLON) {
				innerParser.advance()
			}
			if len(innerParser.errs) > 0 || !innerParser.at(EOF) {
				p.errorf("invalid expression in interpolation: {%s}", exprBuf.String())
				buf.WriteRune('{')
				i = start + 1
				continue
			}
			// Flush text before the interpolation.
			if buf.Len() > 0 {
				parts = append(parts, &ast.LiteralExpr{Value: buf.String(), Kind: ast.LiteralString})
				buf.Reset()
			}
			parts = append(parts, expr)
		} else {
			buf.WriteRune(runes[i])
			i++
		}
	}
	if buf.Len() > 0 {
		parts = append(parts, &ast.LiteralExpr{Value: buf.String(), Kind: ast.LiteralString})
	}

	if len(parts) == 1 {
		return parts[0]
	}
	return &ast.InterpolationExpr{Parts: parts}
}

// --- Helpers ---

// parseExprAsExpr parses a SNGL expression and wraps it in an ast.Expr.
func (p *parser) parseExprAsExpr() ast.Expr {
	node := p.parseExpression()

	// For literals, also populate the Literal field for backward compat
	if lit, ok := node.(*ast.LiteralExpr); ok {
		switch lit.Kind {
		case ast.LiteralInt:
			return ast.Expr{Literal: lit.Value, SNGL: node}
		case ast.LiteralFloat:
			return ast.Expr{Literal: lit.Value, SNGL: node}
		case ast.LiteralString:
			return ast.Expr{Literal: lit.Value, SNGL: node}
		case ast.LiteralBool:
			return ast.Expr{Literal: lit.Value, SNGL: node}
		case ast.LiteralNull:
			return ast.Expr{Literal: nil, SNGL: node}
		case ast.LiteralColor:
			return ast.Expr{Literal: lit.Value, SNGL: node, TypeHint: "color"}
		case ast.LiteralUnit:
			ul := lit.Value.(ast.UnitLiteral)
			return ast.Expr{Literal: lit.Value, SNGL: node, TypeHint: "unit:" + ul.Suffix}
		}
	}
	return ast.Expr{SNGL: node}
}

// splitUnitLiteral splits "12px" into ("12", "px"), "-3.5em" into ("-3.5", "em").
func splitUnitLiteral(s string) (number, suffix string) {
	i := len(s) - 1
	for i >= 0 && isLetter(rune(s[i])) {
		i--
	}
	return s[:i+1], s[i+1:]
}

func (p *parser) parseUnitDecl() *ast.UnitDef {
	pos := p.pos()
	p.expect(KW_UNIT)
	name := p.expect(IDENT).Literal
	def := &ast.UnitDef{Pos: pos, Name: name}
	p.expect(LPAREN)
	for !p.at(RPAREN) && !p.at(EOF) {
		spos := p.pos()
		sname := p.expect(IDENT).Literal
		suffix := &ast.UnitSuffix{Pos: spos, Name: sname}
		if p.at(ASSIGN) {
			p.advance()
			suffix.Factor = p.parseExpression()
		}
		def.Suffixes = append(def.Suffixes, suffix)
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RPAREN)
	return def
}
