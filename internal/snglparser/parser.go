package snglparser

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
	errs        []error
	noStructLit bool // when true, IDENT { is not parsed as struct literal (if/for contexts)
}

func (p *parser) pos() ast.Pos {
	return ast.Pos{Line: p.cur.Line, Column: p.cur.Column}
}

func (p *parser) advance() Token {
	prev := p.cur
	p.cur = p.lex.NextToken()
	return prev
}

func (p *parser) at(t TokenType) bool {
	return p.cur.Type == t
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
		switch p.cur.Type {
		case KW_IMPORT:
			doc.Imports = append(doc.Imports, p.parseImport())
		case KW_OUTPUT:
			doc.Outputs = append(doc.Outputs, p.parseOutput()...)
		case KW_STRUCT:
			doc.Structs = append(doc.Structs, p.parseStruct())
		case KW_ENUM:
			doc.Enums = append(doc.Enums, p.parseEnum())
		case KW_UNIT:
			doc.Units = append(doc.Units, p.parseUnitDecl())
		case KW_STYLE:
			doc.Styles = append(doc.Styles, p.parseStyleDecl())
		case KW_STYLES:
			doc.StyleDefs = append(doc.StyleDefs, p.parseStyles()...)
		case KW_TEST:
			doc.Tests = append(doc.Tests, p.parseTestDef(true))
		case KW_FUNC:
			doc.Functions = append(doc.Functions, p.parseFuncDef())
		case KW_COMPONENT:
			comp := p.parseComponent()
			if comp.Name == "main" {
				// The main component becomes the app root.
				// Hoist data/computeds/consts/functions to document level.
				doc.App = &ast.App{Pos: comp.Pos, Children: comp.Body}
				doc.Data = append(doc.Data, comp.Data...)
				doc.Computeds = append(doc.Computeds, comp.Computeds...)
				doc.Consts = append(doc.Consts, comp.Consts...)
				doc.Functions = append(doc.Functions, comp.Functions...)
			} else {
				doc.Components = append(doc.Components, comp)
			}
		default:
			p.errorf("unexpected token %v at top level", p.cur.Literal)
			p.advance()
		}
	}
	return doc
}

func (p *parser) parseImport() *ast.Import {
	pos := p.pos()
	p.expect(KW_IMPORT)
	path := p.expect(STRING)
	return &ast.Import{Pos: pos, Path: path.Literal}
}

func (p *parser) parseOutput() []*ast.Output {
	p.expect(KW_OUTPUT)
	if p.at(LBRACE) {
		return p.parseOutputGroup()
	}
	return []*ast.Output{p.parseSingleOutput()}
}

func (p *parser) parseSingleOutput() *ast.Output {
	pos := p.pos()
	lang := p.expect(IDENT).Literal
	platform := p.expect(IDENT).Literal
	opts := map[string]string{}
	if p.at(LPAREN) {
		opts = p.parseKVList()
	}
	return &ast.Output{Pos: pos, Lang: lang, Platform: platform, Options: opts}
}

func (p *parser) parseOutputGroup() []*ast.Output {
	p.expect(LBRACE)
	var outputs []*ast.Output
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		lang := p.expect(IDENT).Literal
		if p.at(LBRACE) {
			// lang { platform; platform }
			p.advance()
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
		} else {
			pos := p.pos()
			platform := p.expect(IDENT).Literal
			opts := map[string]string{}
			if p.at(LPAREN) {
				opts = p.parseKVList()
			}
			outputs = append(outputs, &ast.Output{Pos: pos, Lang: lang, Platform: platform, Options: opts})
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return outputs
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
		p.expect(ASSIGN)
		fdefault := p.parseExprAsExpr()
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

func (p *parser) parseStyles() []*ast.StylePropDef {
	p.expect(KW_STYLES)
	p.expect(LBRACE)
	var defs []*ast.StylePropDef
	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		pos := p.pos()
		name := p.expect(IDENT).Literal
		typeHint := p.parseTypeString()
		var enumValues []string
		if p.at(KW_ENUM) {
			p.advance()
			p.expect(LPAREN)
			for !p.at(RPAREN) && !p.at(EOF) {
				switch {
				case p.at(IDENT):
					enumValues = append(enumValues, p.advance().Literal)
				case p.at(INT):
					enumValues = append(enumValues, p.advance().Literal)
				case p.at(STRING):
					enumValues = append(enumValues, p.advance().Literal)
				default:
					p.errorf("expected enum value, got %v", tokenNames[p.cur.Type])
					p.advance()
				}
				if p.at(COMMA) {
					p.advance()
				}
			}
			p.expect(RPAREN)
		}
		defs = append(defs, &ast.StylePropDef{Pos: pos, Name: name, TypeHint: typeHint, Enum: enumValues})
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return defs
}

// --- Component ---

type componentState struct {
	Data      []*ast.Data
	Computeds []*ast.Computed
	Consts    []*ast.Const
}

func (p *parser) parseComponent() *ast.Component {
	pos := p.pos()
	p.expect(KW_COMPONENT)
	name := p.expect(IDENT).Literal
	p.expect(LBRACE)

	comp := &ast.Component{Pos: pos, Name: name}
	cs := &componentState{}

	for !p.at(RBRACE) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RBRACE) {
			break
		}
		switch p.cur.Type {
		case KW_PARAM:
			comp.Params = append(comp.Params, p.parseParam())
		case KW_PROP:
			comp.PropDecls = append(comp.PropDecls, p.parsePropDecl())
		case KW_EVENT:
			comp.EventDecls = append(comp.EventDecls, p.parseEventDecl())
		case KW_CHILDREN:
			p.advance()
			comp.ChildPolicy = p.expect(IDENT).Literal
		case KW_CONST:
			cs.Consts = append(cs.Consts, p.parseConstDecl()...)
		case KW_VAR:
			cs.Data = append(cs.Data, p.parseVarDecl()...)
		case KW_COMPUTED:
			cs.Computeds = append(cs.Computeds, p.parseComputedDecl()...)
		case KW_FUNC:
			comp.Functions = append(comp.Functions, p.parseFuncDef())
		default:
			// Visual nodes or control flow
			comp.Body = append(comp.Body, p.parseNodeOrControl())
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)

	comp.Consts = cs.Consts
	comp.Data = cs.Data
	comp.Computeds = cs.Computeds
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

func (p *parser) parseParam() *ast.Param {
	pos := p.pos()
	p.expect(KW_PARAM)
	name := p.expect(IDENT).Literal
	param := &ast.Param{Pos: pos, Name: name}

	// param name type = default | param name = default | param name type | param name type required
	if p.at(ASSIGN) {
		p.advance()
		param.Default = p.parseExprAsExpr()
	} else if !p.at(SEMICOLON) && !p.at(RBRACE) && !p.at(EOF) && !p.at(KW_PARAM) && !p.at(KW_VAR) && !p.at(KW_CONST) && !p.at(KW_COMPUTED) {
		typeHint := p.parseTypeString()
		param.Default.TypeHint = typeHint
		if p.at(ASSIGN) {
			p.advance()
			param.Default = p.parseExprAsExpr()
			param.Default.TypeHint = typeHint
		}
	}
	// "required" modifier
	if p.at(IDENT) && p.cur.Literal == "required" {
		p.advance()
		param.Required = true
	}
	return param
}

func (p *parser) parsePropDecl() *ast.PropDecl {
	pos := p.pos()
	p.expect(KW_PROP)
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
	p.expect(KW_EVENT)
	name := p.expect(IDENT).Literal
	payloadType := p.expect(IDENT).Literal
	return &ast.EventDecl{Pos: pos, Name: name, PayloadType: payloadType}
}

func (p *parser) parseConstDecl() []*ast.Const {
	p.expect(KW_CONST)
	if p.at(LPAREN) {
		return p.parseGroupedConsts()
	}
	return []*ast.Const{p.parseSingleConst()}
}

func (p *parser) parseSingleConst() *ast.Const {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	// Optional type
	typeHint := ""
	if !p.at(ASSIGN) {
		typeHint = p.parseTypeString()
	}
	p.expect(ASSIGN)
	init := p.parseExprAsExpr()
	if typeHint != "" {
		init.TypeHint = typeHint
	}
	return &ast.Const{Pos: pos, Name: name, Init: init}
}

func (p *parser) parseGroupedConsts() []*ast.Const {
	p.expect(LPAREN)
	var consts []*ast.Const
	for !p.at(RPAREN) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RPAREN) {
			break
		}
		consts = append(consts, p.parseSingleConst())
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
	return []*ast.Data{p.parseSingleVar()}
}

func (p *parser) parseSingleVar() *ast.Data {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	d := &ast.Data{Pos: pos, Name: name}

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
	return d
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
		vars = append(vars, p.parseSingleVar())
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RPAREN)
	return vars
}

func (p *parser) parseComputedDecl() []*ast.Computed {
	p.expect(KW_COMPUTED)
	if p.at(LPAREN) {
		return p.parseGroupedComputeds()
	}
	return []*ast.Computed{p.parseSingleComputed()}
}

func (p *parser) parseSingleComputed() *ast.Computed {
	pos := p.pos()
	name := p.expect(IDENT).Literal
	p.expect(ASSIGN)
	expr := p.parseExprAsExpr()
	return &ast.Computed{Pos: pos, Name: name, Expr: expr}
}

func (p *parser) parseGroupedComputeds() []*ast.Computed {
	p.expect(LPAREN)
	var computeds []*ast.Computed
	for !p.at(RPAREN) && !p.at(EOF) {
		p.skipSemicolons()
		if p.at(RPAREN) {
			break
		}
		computeds = append(computeds, p.parseSingleComputed())
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RPAREN)
	return computeds
}

// --- Function definitions ---

func (p *parser) parseFuncDef() *ast.FuncDef {
	pos := p.pos()
	p.expect(KW_FUNC)
	name := p.expect(IDENT).Literal

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

	// Optional return type: present if next token is not = or {
	retType := ""
	if !p.at(ASSIGN) && !p.at(LBRACE) {
		retType = p.parseTypeString()
	}

	fd := &ast.FuncDef{Pos: pos, Name: name, Params: params, ReturnType: retType}

	if p.at(ASSIGN) {
		// Single-expression form: func name(params) type = expr
		p.advance()
		fd.Body = p.parseExprAsExpr()
	} else if p.at(LBRACE) {
		// Block form: func name(params) type { stmts; return expr }
		p.advance()
		fd.Block = p.parseFuncBlock()
		p.expect(RBRACE)
	} else {
		p.errorf("expected = or { after function signature")
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
	if p.at(LT) {
		p.advance()
		var inner strings.Builder
		inner.WriteString(p.parseTypeString())
		for p.at(COMMA) {
			p.advance()
			inner.WriteString("," + p.parseTypeString())
		}
		p.expect(GT)
		// Convert list<Todo> → list:Todo for compatibility
		if name == "list" {
			return "list:" + inner.String()
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
	vn.For = &ast.ForClause{
		Variable: variable,
		IndexVar: indexVar,
		Iterable: iterable,
	}
	return vn
}

func (p *parser) parseVisualNode() *ast.VisualNode {
	pos := p.pos()
	name := p.expect(IDENT).Literal
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
		if p.at(AT) {
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
		} else if p.cur.Type == KW_STYLE && p.cur.Literal == "style" {
			// style={...}
			p.advance()
			p.expect(ASSIGN)
			p.expect(LBRACE)
			if vn.StyleAttrs == nil {
				vn.StyleAttrs = map[string]ast.Expr{}
			}
			for !p.at(RBRACE) && !p.at(EOF) {
				key := p.expect(IDENT).Literal
				p.expect(ASSIGN)
				vn.StyleAttrs[key] = p.parseExprAsExpr()
				if p.at(COMMA) {
					p.advance()
				}
			}
			p.expect(RBRACE)
		} else {
			// Regular prop: name=expr
			propName := p.expect(IDENT).Literal
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
			} else {
				field = p.expect(IDENT).Literal
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
		elems = append(elems, p.parseExpression())
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
		fname := p.expect(IDENT).Literal
		p.expect(COLON)
		fval := p.parseExpression()
		fields = append(fields, ast.StructFieldLit{Name: fname, Value: fval})
		if p.at(COMMA) {
			p.advance()
		}
		p.skipSemicolons()
	}
	p.expect(RBRACE)
	return &ast.StructExpr{Name: name, Fields: fields}
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
