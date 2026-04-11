package parser

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Parse reads a .sngl file and produces a typed SNGL AST.
func Parse(filename string, r io.Reader) (doc *ast.Document, err error) {
	data, readErr := io.ReadAll(r)
	if readErr != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, readErr)
	}
	p := &parser{
		filename: filename,
		lex:      newLexer(string(data)),
	}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(parserBailout); !ok {
				panic(r) // re-panic on unexpected panics
			}
			// Bailout hit — likely an infinite error loop in the parser.
			p.errs = append(p.errs, fmt.Errorf("parser bailout: too many errors (possible infinite loop)"))
		}
		if len(p.errs) > 0 {
			err = p.joinErrors()
		}
	}()
	p.advance() // prime the first token
	doc = p.parseDocument()
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
		p.cur = p.nextToken()

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

// nextToken returns the next token from the lexer, draining any non-fatal
// lexer errors into the parser's error list.
func (p *parser) nextToken() Token {
	tok := p.lex.NextToken()
	for _, e := range p.lex.errors {
		p.errs = append(p.errs, fmt.Errorf("%s:%s", p.filename, e))
	}
	p.lex.errors = p.lex.errors[:0]
	return tok
}

// peekToken returns the next token without consuming it.
func (p *parser) peekToken() Token {
	if p.peeked != nil {
		return *p.peeked
	}
	var tok Token
	for {
		tok = p.nextToken()

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
		tok = p.nextToken()
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
			tok2 = p.nextToken()
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
		tok := p.nextToken()
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
		tok = p.nextToken()
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
		tok = p.nextToken()
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
			tok2 = p.nextToken()
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

const maxErrors = 100

func (p *parser) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.errs = append(p.errs, fmt.Errorf("%s:%d:%d: %s", p.filename, p.cur.Line, p.cur.Column, msg))
	if len(p.errs) >= maxErrors {
		panic(parserBailout{})
	}
}

// parserBailout is used to abort parsing after too many errors.
type parserBailout struct{}

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
			doc.Decls = append(doc.Decls, imp)
		case KW_OUTPUT:
			doc.OutputLine = p.cur.Line
			defaults, outputs := p.parseOutput()
			if defaults != nil {
				doc.OutputDefaults = defaults
			}
			doc.Outputs = append(doc.Outputs, outputs...)
			for _, o := range outputs {
				doc.Decls = append(doc.Decls, o)
			}
		case KW_STRUCT:
			sd := p.parseStruct()
			sd.Disabled = disabled
			doc.Structs = append(doc.Structs, sd)
			doc.Decls = append(doc.Decls, sd)
		case KW_ENUM:
			ed := p.parseEnum()
			ed.Disabled = disabled
			doc.Enums = append(doc.Enums, ed)
			doc.Decls = append(doc.Decls, ed)
		case KW_UNIT:
			u := p.parseUnitDecl()
			doc.Units = append(doc.Units, u)
			doc.Decls = append(doc.Decls, u)
		case KW_STYLE:
			s := p.parseStyleDecl()
			doc.Styles = append(doc.Styles, s)
			doc.Decls = append(doc.Decls, s)
		case KW_TIMER:
			t := p.parseTimer()
			t.Disabled = disabled
			doc.Timers = append(doc.Timers, t)
			doc.Decls = append(doc.Decls, t)
		case KW_TEST:
			p.errorf("test keyword is no longer supported; use func testName(t T, ...) { ... } instead")
			p.advance()
		case KW_CONST:
			consts := p.parseConstDecl()
			for _, c := range consts {
				c.Disabled = disabled
				doc.Decls = append(doc.Decls, c)
			}
			doc.Consts = append(doc.Consts, consts...)
		case KW_VAR:
			vars := p.parseVarDecl()
			for _, d := range vars {
				d.Disabled = disabled
				doc.Decls = append(doc.Decls, d)
			}
			doc.Data = append(doc.Data, vars...)
		case KW_FUNC:
			fn := p.parseFuncDef()
			fn.Disabled = disabled
			doc.Functions = append(doc.Functions, fn)
			doc.Decls = append(doc.Decls, fn)
		case KW_WINDOW:
			win := p.parseWindow()
			win.Disabled = disabled
			doc.Windows = append(doc.Windows, win)
			doc.Decls = append(doc.Decls, win)
		case KW_COMPONENT:
			comp := p.parseComponent()
			comp.Disabled = disabled
			if comp.Name == "main" {
				app := &ast.App{Pos: comp.Pos}
				if len(comp.Windows) > 0 {
					app.Windows = comp.Windows
				} else {
					app.Children = comp.Body
				}
				doc.App = app
				doc.Data = append(doc.Data, comp.Data...)
				doc.Consts = append(doc.Consts, comp.Consts...)
				doc.Functions = append(doc.Functions, comp.Functions...)
				doc.Timers = append(doc.Timers, comp.Timers...)
			} else {
				doc.Components = append(doc.Components, comp)
			}
			doc.Decls = append(doc.Decls, comp)
		default:
			p.errorf("unexpected token %v at top level", p.cur.Literal)
			p.advance()
		}
	}
	// Promote top-level windows into App.
	if len(doc.Windows) > 0 {
		if doc.App == nil {
			doc.App = &ast.App{}
		}
		doc.App.Windows = append(doc.App.Windows, doc.Windows...)
	}

	doc.Comments = p.comments
	// Only merge comments that aren't inside nested blocks (components, tests).
	// Build a set of line ranges covered by nested blocks.
	type lineRange struct{ start, end, braceCol, braceLine int }
	var nested []lineRange
	for _, d := range doc.Decls {
		switch v := d.(type) {
		case *ast.Component:
			nested = append(nested, lineRange{v.Pos.Line, v.EndLine, v.BraceCol, v.BraceLine})
		case *ast.Window:
			nested = append(nested, lineRange{v.Pos.Line, v.EndLine, v.BraceCol, v.BraceLine})
		}
	}
	var topComments []ast.Comment
	for _, c := range p.comments {
		inside := false
		for _, r := range nested {
			if c.Pos.Line > r.start && c.Pos.Line < r.end {
				// Between start and end lines (exclusive) — but only if on or after the brace line
				if c.Pos.Line >= r.braceLine {
					inside = true
					break
				}
			}
			if c.Pos.Line == r.braceLine && r.braceLine != r.end && r.braceCol > 0 && c.Pos.Column > r.braceCol {
				inside = true
				break
			}
		}
		if !inside {
			topComments = append(topComments, c)
		}
	}
	// Clear Inline on comments that are on a block's start line but before
	// the opening brace — they can't remain inline after formatting spreads
	// the declaration across multiple lines.
	for i := range topComments {
		if !topComments[i].Inline {
			continue
		}
		for _, r := range nested {
			if topComments[i].Pos.Line >= r.start && topComments[i].Pos.Line < r.braceLine {
				topComments[i].Inline = false
				// Also update doc.Comments
				for j := range doc.Comments {
					if doc.Comments[j].Pos == topComments[i].Pos {
						doc.Comments[j].Inline = false
					}
				}
				break
			}
		}
	}
	doc.Decls = mergeCommentsIntoDecls(doc.Decls, topComments)
	// Sync Inline flags back to doc.Comments so the formatter's emitInlineComment works
	inlineLines := map[int]bool{}
	for _, d := range doc.Decls {
		if c, ok := d.(*ast.Comment); ok && c.Inline {
			inlineLines[c.Pos.Line] = true
		}
	}
	for i := range doc.Comments {
		if inlineLines[doc.Comments[i].Pos.Line] {
			doc.Comments[i].Inline = true
		}
	}
	return doc
}

// mergeCommentsIntoDecls merges comments into a declaration slice, preserving
// source order. Inline comments (same line as a declaration) are placed
// immediately after the declaration; non-inline comments are placed before
// the next declaration.
func mergeCommentsIntoDecls(decls []ast.Decl, comments []ast.Comment) []ast.Decl {
	if len(comments) == 0 {
		return decls
	}

	// Build a set of declaration line numbers so we can mark inline comments.
	declLines := map[int]bool{}
	for _, d := range decls {
		declLines[d.DeclPos().Line] = true
	}

	// Mark inline comments.
	for i := range comments {
		if declLines[comments[i].Pos.Line] {
			comments[i].Inline = true
		}
	}

	// Merge: non-inline comments go before the next decl; inline comments go after
	// the decl they share a line with.
	result := make([]ast.Decl, 0, len(decls)+len(comments))
	ci := 0
	for _, d := range decls {
		dLine := d.DeclPos().Line
		// Emit non-inline comments that come before this decl
		for ci < len(comments) && comments[ci].Pos.Line < dLine {
			if !comments[ci].Inline {
				c := comments[ci]
				result = append(result, &c)
			}
			ci++
		}
		result = append(result, d)
		// Emit inline comments on the same line as this decl
		for ci < len(comments) && comments[ci].Pos.Line == dLine {
			if comments[ci].Inline {
				c := comments[ci]
				result = append(result, &c)
			}
			ci++
		}
	}
	// Emit remaining comments
	for ci < len(comments) {
		c := comments[ci]
		result = append(result, &c)
		ci++
	}
	return result
}

func (p *parser) parseImport() *ast.Import {
	pos := p.pos()
	p.expect(KW_IMPORT)

	imp := &ast.Import{Pos: pos}

	if p.at(IDENT) {
		// import alias => "path"
		imp.Alias = p.advance().Literal
		p.expect(FAT_ARROW)
		imp.Path = p.expect(STRING).Literal
		imp.Namespace = imp.Alias
	} else {
		// import "path"
		imp.Path = p.expect(STRING).Literal
	}

	// Extract scheme from path
	rest := imp.Path
	if scheme, after, ok := strings.Cut(rest, "://"); ok {
		imp.Scheme = scheme
		rest = after
	}

	// Derive namespace from path if no alias
	if imp.Alias == "" {
		ns := rest
		if i := strings.LastIndex(ns, "/"); i >= 0 {
			ns = ns[i+1:]
		}
		// Strip file extensions (e.g. "types.d.ts" → "types")
		if i := strings.Index(ns, "."); i >= 0 {
			ns = ns[:i]
		}
		// Strip @version and #hash for remote schemes
		if i := strings.Index(ns, "@"); i >= 0 {
			ns = ns[:i]
		}
		imp.Namespace = ns
	}

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
		langTok := p.expect(IDENT)
		lang := langTok.Literal
		langLine := langTok.Line
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
			outputs = append(outputs, &ast.Output{Pos: pos, Lang: lang, Platform: platform, Options: opts, LangLine: langLine})
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
	s := &ast.StyleDecl{Pos: pos, Name: name}
	p.parseStyleProps(s)
	p.expect(RBRACE)
	return s
}

func (p *parser) parseStyleProps(s *ast.StyleDecl) {
	s.Props = map[string]ast.Expr{}
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		name := p.expect(IDENT).Literal
		p.expect(ASSIGN)
		s.Props[name] = p.parseExprAsExpr()
		s.PropOrder = append(s.PropOrder, name)
		p.skipSemicolons()
	}
}

// --- Component ---

type componentState struct {
	Data    []*ast.Data
	Consts  []*ast.Const
	Windows []*ast.Window
}

func (p *parser) parseComponent() *ast.Component {
	pos := p.pos()
	p.expect(KW_COMPONENT)
	name := p.expect(IDENT).Literal
	// Support qualified names: component sngl.button(...)
	if p.at(DOT) {
		p.advance()
		name = name + "." + p.expect(IDENT).Literal
	}

	comp := &ast.Component{Pos: pos, Name: name}

	// Parse optional param list: Name(param1 = default, @save, :count int) { ... }
	if p.at(LPAREN) {
		p.advance()
		for !p.at(RPAREN) && !p.at(EOF) {
			if p.at(AT) {
				// Event declaration: @eventName [PayloadType]
				pos := p.pos()
				p.advance()
				ename := p.expect(IDENT).Literal
				payloadType := ""
				if p.at(IDENT) {
					payloadType = p.cur.Literal
					p.advance()
				}
				comp.EventDecls = append(comp.EventDecls, &ast.EventDecl{Pos: pos, Name: ename, PayloadType: payloadType})
			} else if p.at(COLON) {
				// Bidirectional param: :name type = default
				p.advance()
				param := p.parseComponentParam()
				param.Bidirectional = true
				comp.Params = append(comp.Params, param)
				comp.EventDecls = append(comp.EventDecls, &ast.EventDecl{Pos: param.Pos, Name: param.Name, PayloadType: "ChangeEvent"})
			} else {
				comp.Params = append(comp.Params, p.parseComponentParam())
			}
			if p.at(COMMA) {
				p.advance()
			}
		}
		p.expect(RPAREN)
	}

	// Parse optional children type (return-type position): list<component>, component, option<component>
	if !p.at(LBRACE) && !p.at(EOF) {
		comp.ChildrenType = p.parseTypeString()
	}

	comp.BraceCol = p.cur.Column
	comp.BraceLine = p.cur.Line
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
		case KW_CONST:
			consts := p.parseConstDecl()
			for _, c := range consts {
				c.Disabled = disabled
				comp.Decls = append(comp.Decls, c)
			}
			cs.Consts = append(cs.Consts, consts...)
		case KW_VAR:
			vars := p.parseVarDecl()
			for _, d := range vars {
				d.Disabled = disabled
				comp.Decls = append(comp.Decls, d)
			}
			cs.Data = append(cs.Data, vars...)
		case KW_FUNC:
			fn := p.parseFuncDef()
			fn.Disabled = disabled
			comp.Functions = append(comp.Functions, fn)
			comp.Decls = append(comp.Decls, fn)
		case KW_TIMER:
			t := p.parseTimer()
			t.Disabled = disabled
			comp.Timers = append(comp.Timers, t)
			comp.Decls = append(comp.Decls, t)
		case KW_WINDOW:
			win := p.parseWindow()
			win.Disabled = disabled
			cs.Windows = append(cs.Windows, win)
			comp.Decls = append(comp.Decls, win)
		case KW_FOR:
			// Parse the for header, then check if the body is a window.
			forPos := p.pos()
			p.expect(KW_FOR)
			variable := p.expect(IDENT).Literal
			indexVar := ""
			if p.at(COMMA) {
				p.advance()
				indexVar = p.expect(IDENT).Literal
			}
			p.expect(ASSIGN)
			p.noStructLit = true
			iterable := p.parseExprAsExpr()
			p.noStructLit = false
			p.expect(LBRACE)
			p.skipSemicolons()

			if p.at(KW_WINDOW) {
				// For-window: parse window inside the for body
				win := p.parseWindow()
				p.skipSemicolons()
				p.expect(RBRACE)
				win.For = &ast.ForClause{
					Variable: variable,
					IndexVar: indexVar,
					Iterable: iterable,
				}
				win.Disabled = disabled
				cs.Windows = append(cs.Windows, win)
				comp.Decls = append(comp.Decls, win)
			} else {
				// Regular for visual node
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
				vn.Pos = forPos
				vn.Disabled = disabled
				comp.Body = append(comp.Body, vn)
				comp.Decls = append(comp.Decls, vn)
			}
		case KW_PLATFORM:
			p.advance()
			platName := p.expect(IDENT).Literal
			p.expect(LBRACE)
			var nodes []*ast.VisualNode
			for !p.at(RBRACE) && !p.at(EOF) {
				p.skipSemicolons()
				if p.at(RBRACE) {
					break
				}
				nodes = append(nodes, p.parseNodeOrControl())
				p.skipSemicolons()
			}
			p.expect(RBRACE)
			if comp.PlatformBodies == nil {
				comp.PlatformBodies = map[string][]*ast.VisualNode{}
			}
			comp.PlatformBodies[platName] = nodes
		default:
			node := p.parseNodeOrControl()
			node.Disabled = disabled
			comp.Body = append(comp.Body, node)
			comp.Decls = append(comp.Decls, node)
		}
		p.skipSemicolons()
	}
	closeLine := p.cur.Line
	comp.EndLine = closeLine
	p.expect(RBRACE)

	comp.Consts = cs.Consts
	comp.Data = cs.Data
	comp.Windows = cs.Windows

	// Merge comments that fall inside the component body.
	bodyStart := comp.Pos.Line
	bodyEnd := closeLine
	var bodyComments []ast.Comment
	for _, c := range p.comments {
		if c.Pos.Line > bodyStart && c.Pos.Line < bodyEnd && c.Pos.Line >= comp.BraceLine {
			bodyComments = append(bodyComments, c)
		} else if c.Pos.Line == comp.BraceLine && comp.BraceLine != bodyEnd && comp.BraceCol > 0 && c.Pos.Column > comp.BraceCol {
			// Comment on the same line as { but after it — inside the body
			bodyComments = append(bodyComments, c)
		}
	}
	comp.Decls = mergeCommentsIntoDecls(comp.Decls, bodyComments)
	return comp
}

// --- Window ---

// parseWindow parses a window declaration:
//
//	window [name][(props)] { [decls...] [visual-nodes...] }
func (p *parser) parseWindow() *ast.Window {
	pos := p.pos()
	p.expect(KW_WINDOW)

	win := &ast.Window{Pos: pos}

	// Optional name
	if p.at(IDENT) {
		win.Name = p.advance().Literal
	}

	// Optional props in ()
	if p.at(LPAREN) {
		win.HasProps = true
		p.advance()
		for !p.at(RPAREN) && !p.at(EOF) {
			key := p.expect(IDENT).Literal
			p.expect(ASSIGN)
			val := p.parseExprAsExpr()
			if win.Props == nil {
				win.Props = map[string]ast.Expr{}
			}
			win.Props[key] = val
			win.PropOrder = append(win.PropOrder, key)
			if p.at(COMMA) {
				p.advance()
			}
		}
		p.expect(RPAREN)
	}

	win.BraceCol = p.cur.Column
	win.BraceLine = p.cur.Line
	p.expect(LBRACE)

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
		case KW_CONST:
			consts := p.parseConstDecl()
			for _, c := range consts {
				c.Disabled = disabled
				win.Decls = append(win.Decls, c)
			}
			win.Consts = append(win.Consts, consts...)
		case KW_VAR:
			vars := p.parseVarDecl()
			for _, d := range vars {
				d.Disabled = disabled
				win.Decls = append(win.Decls, d)
			}
			win.Data = append(win.Data, vars...)
		case KW_FUNC:
			fn := p.parseFuncDef()
			fn.Disabled = disabled
			win.Functions = append(win.Functions, fn)
			win.Decls = append(win.Decls, fn)
		case KW_TIMER:
			t := p.parseTimer()
			t.Disabled = disabled
			win.Timers = append(win.Timers, t)
			win.Decls = append(win.Decls, t)
		default:
			node := p.parseNodeOrControl()
			node.Disabled = disabled
			win.Children = append(win.Children, node)
			win.Decls = append(win.Decls, node)
		}
		p.skipSemicolons()
	}
	win.EndLine = p.cur.Line
	p.expect(RBRACE)

	// Merge comments that fall inside the window body.
	var bodyComments []ast.Comment
	for _, c := range p.comments {
		if c.Pos.Line > pos.Line && c.Pos.Line < win.EndLine && c.Pos.Line >= win.BraceLine {
			bodyComments = append(bodyComments, c)
		} else if c.Pos.Line == win.BraceLine && win.BraceLine != win.EndLine && win.BraceCol > 0 && c.Pos.Column > win.BraceCol {
			bodyComments = append(bodyComments, c)
		}
	}
	win.Decls = mergeCommentsIntoDecls(win.Decls, bodyComments)
	return win
}

// parseComponentParam parses a single component parameter inside ().
// Syntax: name = default | name type [enum(...)] [= default] [required]
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
		// Optional enum constraint: type string enum(text, password, number)
		if p.at(KW_ENUM) {
			p.advance()
			p.expect(LPAREN)
			for !p.at(RPAREN) && !p.at(EOF) {
				param.Enum = append(param.Enum, p.expect(IDENT).Literal)
				if p.at(COMMA) {
					p.advance()
				}
			}
			p.expect(RPAREN)
		}
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
			consts = append(consts, &ast.Const{Pos: pos, Name: n, Init: init, ExplicitType: typeHint != ""})
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
	return []*ast.Const{{Pos: pos, Name: name, Init: init, ExplicitType: typeHint != ""}}
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
	for _, s := range consts {
		s.Grouped = true
	}
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
		var typeHint string
		var hasExplicitType bool
		if !p.at(SEMICOLON) && !p.at(RPAREN) && !p.at(EOF) && !p.at(AT) && !p.at(ASSIGN) && !p.at(RBRACE) {
			typeHint = p.parseTypeString()
			hasExplicitType = true
		}
		var init ast.Expr
		if p.at(ASSIGN) {
			p.advance()
			init = p.parseExprAsExpr()
			init.TypeHint = typeHint
		} else {
			init.TypeHint = typeHint
		}
		var vars []*ast.Data
		for i, n := range names {
			d := &ast.Data{Pos: pos, Name: n, Init: init, ExplicitType: hasExplicitType}
			if i == 0 {
				d.MultiNames = names
			} else {
				d.IsMultiNameTail = true
			}
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
	tok := p.nextToken() // read token after peeked IDENT
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
	} else if !p.at(SEMICOLON) && !p.at(RPAREN) && !p.at(EOF) && !p.at(AT) {
		typeHint := p.parseTypeString()
		d.Init.TypeHint = typeHint
		d.ExplicitType = true
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
	for p.at(AT) {
		pos := p.pos()
		p.advance()
		kind := p.expect(IDENT).Literal // "change", "insert", "delete", "init"
		var param string
		if p.at(LPAREN) {
			p.advance()
			param = p.expect(IDENT).Literal
			p.expect(RPAREN)
		}
		p.expect(LBRACE)
		body := p.parseStmtList()
		endLine := p.pos().Line
		p.expect(RBRACE)
		d.Events = append(d.Events, ast.DataEvent{Kind: kind, Param: param, Body: body, Pos: pos, EndLine: endLine})
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
	for _, s := range vars {
		s.Grouped = true
	}
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

	// Parse parameter list (optional for expression-form with =>)
	var params []*ast.FuncParam
	hasParens := p.at(LPAREN)
	if hasParens {
		p.advance()
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
	}

	fd := &ast.FuncDef{Pos: pos, Name: name, TypeParams: typeParams, Params: params, HasParens: hasParens}

	if p.at(LBRACE) {
		// Void block form: func name(params) { ... }
		if !hasParens {
			p.errorf("block-form functions require (): func %s() { ... }", name)
		}
		fd.HasParens = true // block form always has parens
		fd.BraceCol = p.cur.Column
		p.advance()
		fd.Block = p.parseFuncBlock()
		fd.EndLine = p.pos().Line
		p.expect(RBRACE)
	} else if p.at(IDENT) && p.peekToken().Type == LBRACE && p.isTypedBlockFunc() {
		// Typed block form: func name(params) type { ... }
		if !hasParens {
			p.errorf("block-form functions require (): func %s() { ... }", name)
		}
		fd.HasParens = true
		fd.ReturnType = p.parseTypeString()
		fd.BraceCol = p.cur.Column
		p.advance() // consume LBRACE
		fd.Block = p.parseFuncBlock()
		fd.EndLine = p.pos().Line
		p.expect(RBRACE)
	} else if p.at(IDENT) && p.peekToken().Type == LT && p.isGenericReturnTypeBlock() {
		// Typed block form with generic return type: func name(params) list<T> { ... }
		if !hasParens {
			p.errorf("block-form functions require (): func %s() { ... }", name)
		}
		fd.HasParens = true
		fd.ReturnType = p.parseTypeString()
		fd.BraceCol = p.cur.Column
		p.advance() // consume LBRACE
		fd.Block = p.parseFuncBlock()
		fd.EndLine = p.pos().Line
		p.expect(RBRACE)
	} else if p.at(FAT_ARROW) {
		// Expression form: func name(params) => expr
		p.advance()
		fd.Body = p.parseExprAsExpr()
	} else {
		p.errorf("expected {, =>, or return type after func params, got %v (%q)", tokenNames[p.cur.Type], p.cur.Literal)
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
	// Accept keywords as type names (e.g., "component" in list<component>)
	var name string
	if p.at(IDENT) || p.cur.Type.IsKeyword() {
		name = p.cur.Literal
		p.advance()
	} else {
		name = p.expect(IDENT).Literal // will error
	}
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
	result := "func:" + strings.Join(params, ",")
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
	p.expect(ASSIGN)
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
		vn.HasProps = true
		p.parsePropList(vn)
	}

	// Optional children block
	if p.at(LBRACE) {
		vn.HasBody = true
		vn.BraceCol = p.cur.Column
		p.advance()
		for !p.at(RBRACE) && !p.at(EOF) {
			p.skipSemicolons()
			if p.at(RBRACE) {
				break
			}
			vn.Children = append(vn.Children, p.parseNodeOrControl())
			p.skipSemicolons()
		}
		vn.EndLine = p.pos().Line
		p.expect(RBRACE)
	}

	return vn
}

func (p *parser) parsePropList(vn *ast.VisualNode) {
	openLine := p.cur.Line
	p.expect(LPAREN)
	firstPropLine := 0
	for !p.at(RPAREN) && !p.at(EOF) {
		if firstPropLine == 0 {
			firstPropLine = p.cur.Line
		}
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
			vn.PropOrder = append(vn.PropOrder, ":"+bname)
		} else if p.at(AT) {
			// Event handler: @event(param) { stmts }
			p.advance()
			eventName := p.expect(IDENT).Literal
			var param string
			if p.at(LPAREN) {
				p.advance()
				param = p.expect(IDENT).Literal
				p.expect(RPAREN)
			}
			open := p.expect(LBRACE)
			stmts := p.parseStmtList()
			close := p.expect(RBRACE)
			multiline := close.Line > open.Line
			if vn.Events == nil {
				vn.Events = map[string]ast.EventHandler{}
			}
			vn.Events[eventName] = ast.EventHandler{Param: param, Body: ast.Expr{SNGL: stmts}, Multiline: multiline}
			vn.PropOrder = append(vn.PropOrder, "@"+eventName)
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
			vn.PropOrder = append(vn.PropOrder, propName)
		}
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons() // allow multi-line prop lists
	}
	closeTok := p.expect(RPAREN)
	// Multi-line if the first prop started on a different line than (
	if firstPropLine > 0 && firstPropLine > openLine {
		vn.MultilineProps = true
	}
	// Track end of props for EndLine (used when no body follows)
	vn.EndLine = closeTok.Line
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

// parseParenOrLambda parses either (expr) or (params) => expr.
// Parses as a parenthesized expression first. If => follows ), and the
// expression was a simple identifier or comma-separated identifiers,
// reinterprets as lambda params.
func (p *parser) parseParenOrLambda() ast.Node {
	startPos := p.pos()
	p.advance() // consume (

	// Empty parens: () => expr
	if p.at(RPAREN) {
		p.advance()
		if p.at(FAT_ARROW) {
			p.advance()
			body := p.parseExpression()
			return &ast.LambdaExpr{Body: body}
		}
		p.errorf("unexpected empty parentheses")
		return &ast.LiteralExpr{Kind: ast.LiteralNull}
	}

	// Parse as expression
	expr := p.parseExpression()

	// Check for comma — could be multi-param lambda
	if p.at(COMMA) {
		// Collect remaining comma-separated items
		// First item must be an ident (or ident type)
		first := extractLambdaParam(expr)
		if first == nil {
			// Not a valid lambda param — parse error
			p.errorf("invalid lambda parameter at %s", startPos)
			// Consume remaining until )
			for !p.at(RPAREN) && !p.at(EOF) {
				p.advance()
			}
			p.expect(RPAREN)
			return expr
		}
		params := []lambdaParam{*first}
		for p.at(COMMA) {
			p.advance()
			name := p.expect(IDENT).Literal
			typeHint := ""
			if p.at(IDENT) {
				typeHint = p.advance().Literal
			}
			params = append(params, lambdaParam{name, typeHint})
		}
		p.expect(RPAREN)
		if !p.at(FAT_ARROW) {
			p.errorf("expected => after parameter list")
			return expr
		}
		p.advance()
		var names, types []string
		for _, lp := range params {
			names = append(names, lp.name)
			types = append(types, lp.typeHint)
		}
		body := p.parseExpression()
		return &ast.LambdaExpr{Params: names, ParamTypes: types, Body: body}
	}

	p.expect(RPAREN)

	// Check for => — single-param lambda
	if p.at(FAT_ARROW) {
		lp := extractLambdaParam(expr)
		if lp != nil {
			p.advance()
			body := p.parseExpression()
			return &ast.LambdaExpr{
				Params:     []string{lp.name},
				ParamTypes: []string{lp.typeHint},
				Body:       body,
			}
		}
	}

	// Plain parenthesized expression
	return &ast.ParenExpr{Inner: expr}
}

// parseAnonFunc parses an anonymous function in expression context:
// func(params) { block } or func(params) => expr
func (p *parser) parseAnonFunc() ast.Node {
	p.advance() // consume func
	p.expect(LPAREN)

	var names, types []string
	for !p.at(RPAREN) && !p.at(EOF) {
		name := p.expect(IDENT).Literal
		typeHint := ""
		if p.at(IDENT) || p.cur.Type.IsKeyword() {
			typeHint = p.parseTypeString()
		}
		names = append(names, name)
		types = append(types, typeHint)
		if p.at(COMMA) {
			p.advance()
		}
	}
	p.expect(RPAREN)

	if p.at(LBRACE) {
		p.advance()
		block := p.parseFuncBlock()
		p.expect(RBRACE)
		return &ast.LambdaExpr{Params: names, ParamTypes: types, Block: block}
	}
	if p.at(FAT_ARROW) {
		p.advance()
		body := p.parseExpression()
		return &ast.LambdaExpr{Params: names, ParamTypes: types, Body: body}
	}
	p.errorf("expected { or => after func parameters")
	return &ast.LiteralExpr{Kind: ast.LiteralNull}
}

type lambdaParam struct {
	name     string
	typeHint string
}

// extractLambdaParam tries to interpret an expression node as a lambda parameter.
// Returns nil if the expression isn't a valid param (identifier or identifier with type).
func extractLambdaParam(expr ast.Node) *lambdaParam {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		return &lambdaParam{name: e.Name}
	default:
		return nil
	}
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
			} else if p.at(ELEMENT_REF) {
				// c.#id → select with field "#id"
				field = "#" + p.cur.Literal
				p.advance()
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
		clean := strings.ReplaceAll(tok.Literal, "_", "")
		val, err := strconv.ParseInt(clean, 0, 64)
		if err != nil {
			p.errorf("invalid integer literal %q: %v", tok.Literal, err)
		}
		return &ast.LiteralExpr{Value: int(val), Kind: ast.LiteralInt, Raw: tok.Literal}
	case FLOAT:
		tok := p.advance()
		clean := strings.ReplaceAll(tok.Literal, "_", "")
		val, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			p.errorf("invalid float literal %q: %v", tok.Literal, err)
		}
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralFloat, Raw: tok.Literal}
	case STRING:
		tok := p.advance()
		node := p.parseStringWithInterpolation(tok.Literal)
		return node
	case TRIPLE_STRING:
		tok := p.advance()
		node := p.parseStringWithInterpolation(tok.Literal)
		// Set style to triple on the result
		switch n := node.(type) {
		case *ast.LiteralExpr:
			n.Style = ast.StyleTriple
		case *ast.InterpolationExpr:
			n.Style = ast.StyleTriple
		}
		return node
	case RAW_STRING:
		tok := p.advance()
		return &ast.LiteralExpr{Value: tok.Literal, Kind: ast.LiteralString, Style: ast.StyleRaw}
	case COLOR:
		tok := p.advance()
		return &ast.LiteralExpr{Value: tok.Literal, Kind: ast.LiteralColor}
	case UNIT_LITERAL:
		tok := p.advance()
		num, suffix := splitUnitLiteral(tok.Literal)
		return &ast.LiteralExpr{Value: ast.UnitLiteral{Number: num, Suffix: suffix}, Kind: ast.LiteralUnit}
	case LPAREN:
		return p.parseParenOrLambda()
	case LBRACKET:
		return p.parseListLiteral()
	case LBRACE:
		// Anonymous struct literal: {field=val, ...}
		return p.parseAnonStructLiteral()
	case KW_FUNC:
		return p.parseAnonFunc()
	case ELEMENT_REF:
		tok := p.advance()
		return &ast.ElementRefExpr{Name: tok.Literal}
	case IDENT, KW_EVENT:
		tok := p.advance()
		// Pre-declared identifiers
		switch tok.Literal {
		case "true":
			return &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}
		case "false":
			return &ast.LiteralExpr{Value: false, Kind: ast.LiteralBool}
		case "null":
			return &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}
		}
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
	openLine := p.cur.Line
	p.expect(LBRACE)
	firstFieldLine := 0
	var fields []ast.StructFieldLit
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if firstFieldLine == 0 {
			firstFieldLine = p.cur.Line
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
	multiline := firstFieldLine > 0 && firstFieldLine > openLine
	return &ast.StructExpr{Name: name, Fields: fields, Multiline: multiline}
}

// parseAnonStructLiteral parses an anonymous struct literal: {field=val, field2=val2}.
// Uses = as field separator (not :). Used for style props and other type-inferred contexts.
func (p *parser) parseAnonStructLiteral() ast.Node {
	openLine := p.cur.Line
	p.expect(LBRACE)
	firstFieldLine := 0
	var fields []ast.StructFieldLit
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		if firstFieldLine == 0 {
			firstFieldLine = p.cur.Line
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
	multiline := firstFieldLine > 0 && firstFieldLine > openLine
	return &ast.StructExpr{Name: "", Fields: fields, Multiline: multiline}
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
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '{' {
			// Escaped brace: \{ → literal {
			buf.WriteRune('{')
			i += 2
			continue
		}
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

	// Don't unwrap single-part interpolations — "{count}" must stay as
	// InterpolationExpr, not bare IdentExpr. The optimizer can simplify
	// this when type info confirms it's safe.
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
