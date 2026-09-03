package parser

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// builder converts an egg parse tree ([]int32) into AST nodes.
type builder struct {
	file     string
	filtered []Token
	comments []Token
	// firstCodeCol maps a line to the column its leftmost code token starts
	// at, which is how a trailing comment is told from one on its own line.
	firstCodeCol map[int]int
	// claimed marks comments an expression took for itself — inside an i18n
	// placeholder, where there is no statement list for them to land in — so
	// that the statement-level pass does not place them a second time.
	claimed map[int]bool
	errors  []string
	// native holds the expression of a native-value parse; nil for a document.
	native ast.Expr
	// nativeMode is set for that parse, and is the whole of what keeps an
	// import in expression position out of ordinary source.
	nativeMode bool
}

func newBuilder(file string, filtered, comments []Token) *builder {
	b := &builder{file: file, filtered: filtered, comments: comments}
	if len(comments) > 0 {
		b.firstCodeCol = make(map[int]int, len(filtered))
		for _, tok := range filtered {
			if c, ok := b.firstCodeCol[tok.Line]; !ok || tok.Column < c {
				b.firstCodeCol[tok.Line] = tok.Column
			}
		}
	}
	return b
}

func (b *builder) errorf(pos ast.Pos, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	b.errors = append(b.errors, fmt.Sprintf("%s:%d:%d: %s", pos.File, pos.Line, pos.Column, msg))
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

func (b *builder) posFromToken(tok Token) ast.Pos {
	return ast.Pos{File: b.file, Line: tok.Line, Column: tok.Column}
}

// --- MacroAttr ---

func (b *builder) buildMacroAttr(it nodeIter) ast.MacroAttr {
	// MacroAttr = attr_open ident [ dot ident ] [ lparen [ Expr { comma Expr } ] rparen ] rbracket .
	attrOpenTok := it.shift() // attr_open
	pos := b.posFromToken(attrOpenTok)

	firstIdent := it.shift() // first ident (always present)
	firstName := firstIdent.Literal

	var alias, name string
	if !it.done() && !it.isNonTerminal() && it.tokenType() == DOT {
		it.skip() // dot
		secondIdent := it.shift()
		alias = firstName
		name = secondIdent.Literal
	} else {
		// bare name without alias
		alias = ""
		name = firstName
	}

	var args []ast.Expr
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		for !it.done() {
			if it.isNonTerminal() {
				args = append(args, b.buildExpr(it.enter()))
			} else {
				tok := it.token()
				if tok.Type == RPAREN {
					it.skip()
					break
				}
				it.skip() // comma
			}
		}
	}
	// consume rbracket
	if !it.done() && !it.isNonTerminal() && it.tokenType() == RBRACKET {
		it.skip()
	}

	return ast.MacroAttr{Pos: pos, Alias: alias, Name: name, Args: args}
}

// buildParamAttrs consumes the leading { MacroAttr } of a Param or CompParam.
func (b *builder) buildParamAttrs(it *nodeIter) []ast.MacroAttr {
	var attrs []ast.MacroAttr
	for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
		attrs = append(attrs, b.buildMacroAttr(it.enter()))
	}
	return attrs
}

// --- Document ---

func (b *builder) buildDocument(children []int32) *ast.Document {
	doc := &ast.Document{}
	it := b.iter(children)
	// Document = native_value Expr [ semi ] | { … } .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == NATIVE_VALUE {
		it.skip()
		b.nativeMode = true
		if !it.done() && it.isNonTerminal() {
			b.native = b.buildExpr(it.enter())
		}
		return doc
	}
	for !it.done() {
		// Document = { [ slashdash ] { MacroAttr } Stmt semi } .
		if !it.isNonTerminal() {
			tok := it.token()
			if tok.Type == SLASHDASH {
				sdPos := it.shift()
				// consume any MacroAttr non-terminals after slashdash
				for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
					it.skip()
				}
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
		if it.symbol() == MacroAttr {
			// Collect consecutive MacroAttr non-terminals.
			var attrs []ast.MacroAttr
			for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
				attrs = append(attrs, b.buildMacroAttr(it.enter()))
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == Stmt {
				if inner := b.buildStmt(it.enter()); inner != nil {
					doc.Stmts = append(doc.Stmts, b.attach(attrs, inner))
				}
			} else {
				b.errorf(attrs[0].Pos, "macro attribute has no following declaration")
			}
		} else if it.symbol() == Stmt {
			s := b.buildStmt(it.enter())
			if s != nil {
				doc.Stmts = append(doc.Stmts, s)
			}
		} else {
			it.skip()
		}
	}
	b.injectComments(doc)
	doc.BlankLines = b.blankLines()
	return doc
}

// blankLines finds the lines the source left empty, by looking for a gap
// between one token's last line and the next token's first. A line inside a
// triple-quoted string is not a gap, which is why the scan measures the token
// it just passed rather than counting line numbers.
func (b *builder) blankLines() map[int]bool {
	toks := make([]Token, 0, len(b.filtered)+len(b.comments))
	toks = append(toks, b.filtered...)
	toks = append(toks, b.comments...)
	sort.SliceStable(toks, func(i, j int) bool {
		if toks[i].Line != toks[j].Line {
			return toks[i].Line < toks[j].Line
		}
		return toks[i].Column < toks[j].Column
	})
	blank := map[int]bool{}
	prevEnd := 0
	for _, tok := range toks {
		if prevEnd > 0 && tok.Line-prevEnd >= 2 {
			blank[tok.Line-1] = true
		}
		if end := tok.Line + strings.Count(tok.Literal, "\n"); end > prevEnd {
			prevEnd = end
		}
	}
	return blank
}

// injectComments inserts comment tokens into the statement lists they were
// written in, at positions determined by their source line numbers. Comments
// arrive in one document-order list because the grammar does not carry them;
// placing them means walking the tree in the same order and handing each block
// the comments that fall inside its braces.
func (b *builder) injectComments(doc *ast.Document) {
	if len(b.comments) == 0 {
		return
	}
	ci := 0
	doc.Stmts = b.placeComments(doc.Stmts, &ci, math.MaxInt)
}

// placeComments merges every comment up to endLine into stmts, descending into
// each statement's blocks so that a block takes the comments written between
// its braces. A comment written after a statement starts but before one of its
// blocks opens — on a mark's line, or inside a parameter list — belongs to
// neither, and follows the statement.
func (b *builder) placeComments(stmts []ast.Stmt, ci *int, endLine int) []ast.Stmt {
	merged := make([]ast.Stmt, 0, len(stmts)+1)
	take := func(before int) {
		for *ci < len(b.comments) && b.comments[*ci].Line < before {
			if !b.claimed[*ci] {
				merged = append(merged, b.commentToStmt(b.comments[*ci]))
			}
			*ci++
		}
	}
	for _, s := range stmts {
		line := 0
		if pos := s.StmtPos(); pos != nil {
			line = pos.Line
		}
		if line == 0 {
			// A statement with no position cannot order comments against
			// itself; the comments still standing go ahead of it.
			take(endLine)
		} else {
			take(line)
		}
		merged = append(merged, s)

		blocks, commit := stmtBlocks(s)
		for _, block := range blocks {
			// A block the parser synthesized has no closing brace to bound
			// it, so nothing inside it can be claimed.
			if !block.EndPos.IsSet() {
				continue
			}
			take(block.Pos.Line)
			block.Stmts = b.placeComments(block.Stmts, ci, block.EndPos.Line)
		}
		commit()
	}
	take(endLine)
	return merged
}

func (b *builder) commentToStmt(tok Token) *ast.Comment {
	return &ast.Comment{
		Pos:    ast.Pos(b.posFromToken(tok)),
		Text:   tok.Literal,
		Block:  tok.Type == BLOCK_COMMENT,
		Inline: b.isInlineComment(tok),
	}
}

// isInlineComment reports whether code precedes tok on its line, which makes
// it a comment about that line rather than about what follows. The difference
// is load-bearing in testdata, where an `// ERROR(check)` directive names the
// line it sits on.
func (b *builder) isInlineComment(tok Token) bool {
	col, ok := b.firstCodeCol[tok.Line]
	return ok && col < tok.Column
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
		case SlotNode:
			return b.buildSlotNode(it.enter())
		case VisualOrStmt:
			return b.buildVisualOrStmt(it.enter())
		}
		it.skip()
		return nil
	}
	// kw_return [ Expr ] | kw_break | kw_continue
	tok := it.token()
	switch tok.Type {
	case KW_RETURN:
		pos := b.posFromToken(it.shift())
		var val ast.Expr
		if !it.done() && it.isNonTerminal() {
			val = b.buildExpr(it.enter())
		}
		return &ast.ReturnStmt{Pos: ast.Pos(pos), Value: val}
	case KW_BREAK:
		return &ast.BreakStmt{Pos: ast.Pos(b.posFromToken(it.shift()))}
	case KW_CONTINUE:
		return &ast.ContinueStmt{Pos: ast.Pos(b.posFromToken(it.shift()))}
	}
	it.skip()
	return nil
}

// --- Imports ---

func (b *builder) buildImportDecl(it nodeIter) *ast.Import {
	// ImportDecl = kw_import [ ident | dot ] str_full [ fat_arrow str_full ] .
	pos := b.posFromToken(it.shift()) // kw_import
	imp := &ast.Import{Pos: pos}
	switch {
	case !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT:
		imp.Alias = it.shift().Literal
	case !it.done() && !it.isNonTerminal() && it.tokenType() == DOT:
		it.skip()
		imp.Alias = "."
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == STR_FULL {
		imp.Path = stripQuotes(it.shift().Literal)
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == FAT_ARROW {
		it.skip() // fat_arrow
		if !it.done() && !it.isNonTerminal() && it.tokenType() == STR_FULL {
			imp.Replace = stripQuotes(it.shift().Literal)
		}
	}
	return imp
}

// --- Struct declaration ---

func (b *builder) buildStructDecl(it nodeIter) *ast.StructDef {
	// StructDecl = kw_struct [ ident ] [ TypeParamList ] lbrace { StructBodyItem } rbrace .
	pos := b.posFromToken(it.shift()) // kw_struct
	s := &ast.StructDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		s.Name = it.shift().Literal
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
		s.TypeParams = b.buildTypeParams(it.enter())
	}
	lbraceLine, rbraceLine := 0, 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		lbraceLine = it.token().Line
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == StructBodyItem {
			if item := b.buildStructBodyItem(it.enter()); item != nil {
				s.Body = append(s.Body, item)
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == RBRACE {
				rbraceLine = it.token().Line
				if it.token().Line > lbraceLine {
					s.IsMultiline = true
				}
			}
			it.skip() // rbrace or semi
		}
	}
	s.Body = interleaveComments(b, lbraceLine, rbraceLine, s.Body,
		func(i ast.StructBodyItem) int { return bodyItemLine(i) },
		func(c *ast.Comment) ast.StructBodyItem { return c })
	return s
}

// bodyItemLine is the source line a struct or enum body item starts on.
func bodyItemLine(item any) int {
	if s, ok := item.(ast.Stmt); ok {
		if p := s.StmtPos(); p != nil {
			return p.Line
		}
	}
	switch v := item.(type) {
	case *ast.EnumMember:
		return v.Pos.Line
	case *ast.UnitSuffix:
		return v.Pos.Line
	}
	return 0
}

// interleaveComments puts the comments written inside a braced body among its
// items. A struct or enum body is a list of declarations rather than of
// statements, so the statement-level pass cannot reach into it — left alone,
// a comment on a field ends up below the closing brace.
func interleaveComments[T any](b *builder, openLine, closeLine int, items []T, line func(T) int, wrap func(*ast.Comment) T) []T {
	if openLine == 0 || closeLine <= openLine || len(b.comments) == 0 {
		return items
	}
	out := make([]T, 0, len(items))
	prev := openLine
	for _, item := range items {
		at := line(item)
		if at == 0 {
			out = append(out, item)
			continue
		}
		for _, c := range b.claimComments(prev, at) {
			out = append(out, wrap(c))
		}
		out = append(out, item)
		if c := b.claimInlineComment(at); c != nil {
			out = append(out, wrap(c))
		}
		prev = at + 1
	}
	for _, c := range b.claimComments(prev, closeLine) {
		out = append(out, wrap(c))
	}
	return out
}

func (b *builder) buildStructBodyItem(it nodeIter) ast.StructBodyItem {
	// StructBodyItem = { MacroAttr } StructBodyDecl .
	var attrs []ast.MacroAttr
	for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
		attrs = append(attrs, b.buildMacroAttr(it.enter()))
	}
	var inner ast.StructBodyItem
	if !it.done() && it.isNonTerminal() && it.symbol() == StructBodyDecl {
		sub := it.enter()
		if !sub.done() && sub.isNonTerminal() {
			switch sub.symbol() {
			case FuncDecl:
				inner = b.buildFuncDecl(sub.enter())
			case StructField:
				inner = b.buildStructField(sub.enter())
			}
		}
	}
	if len(attrs) == 0 || inner == nil {
		return inner
	}
	item, _ := b.attach(attrs, inner.(ast.Stmt)).(ast.StructBodyItem)
	return item
}

// attach records the marks written before a declaration on the declaration
// itself. A statement form that cannot carry one is refused here, where the
// mark's position is known — a mark annotates a declaration, and nothing
// downstream would have anything to annotate.
func (b *builder) attach(attrs []ast.MacroAttr, inner ast.Stmt) ast.Stmt {
	target, ok := inner.(ast.Attributed)
	if !ok {
		b.errorf(attrs[0].Pos, "#[%s] cannot mark %s", attrs[0].MacroName(), ast.DeclFormName(inner))
		return inner
	}
	target.SetMacroAttrs(attrs[0].Pos, attrs)
	return target
}

func (b *builder) buildStructField(it nodeIter) *ast.StructField {
	// StructField = IdentList Type [ assign Expr ] semi .
	f := &ast.StructField{}
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		sub := it.enter()
		if !sub.done() {
			f.Pos = b.posFromToken(sub.token())
		}
		f.Names, f.NamePositions = b.buildIdentListWithPos(sub)
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
	// EnumDecl = kw_enum [ ident ] lbrace { EnumBodyItem (comma|semi) } rbrace .
	pos := b.posFromToken(it.shift()) // kw_enum
	e := &ast.EnumDef{Pos: pos}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		e.Name = it.shift().Literal
	}
	lbraceLine, rbraceLine := 0, 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		lbraceLine = it.token().Line
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == EnumBodyItem {
			sub := it.enter()
			if !sub.done() && sub.isNonTerminal() {
				switch sub.symbol() {
				case FuncDecl:
					e.Body = append(e.Body, b.buildFuncDecl(sub.enter()))
				case EnumMember:
					e.Body = append(e.Body, b.buildEnumMember(sub.enter()))
				}
			}
		} else {
			if !it.isNonTerminal() {
				switch tok := it.token(); tok.Type {
				case SEMICOLON:
					e.IsMultiline = true
				case RBRACE:
					rbraceLine = tok.Line
				}
			}
			it.skip() // comma or semi or rbrace
		}
	}
	e.Body = interleaveComments(b, lbraceLine, rbraceLine, e.Body,
		func(i ast.EnumBodyItem) int { return bodyItemLine(i) },
		func(c *ast.Comment) ast.EnumBodyItem { return c })
	for _, item := range e.Body {
		if _, ok := item.(*ast.Comment); ok {
			// A comment needs a line of its own, so the body needs lines.
			e.IsMultiline = true
		}
	}
	return e
}

func (b *builder) buildEnumMember(it nodeIter) *ast.EnumMember {
	m := &ast.EnumMember{}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
		nameTok := it.shift()
		m.Pos = b.posFromToken(nameTok)
		m.Name = nameTok.Literal
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
		it.skip() // assign
		if !it.done() && it.isNonTerminal() {
			m.Value = b.buildExpr(it.enter())
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
	lbraceLine, rbraceLine := 0, 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LBRACE {
		lbraceLine = it.token().Line
	}
	it.skip() // lbrace
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == ArgList {
			for _, s := range b.buildUnitSuffixes(it.enter(), &u.IsMultiline) {
				u.Body = append(u.Body, s)
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == RBRACE {
				rbraceLine = it.token().Line
			}
			it.skip() // rbrace
		}
	}
	u.Body = interleaveComments(b, lbraceLine, rbraceLine, u.Body,
		func(i ast.UnitBodyItem) int { return bodyItemLine(i) },
		func(c *ast.Comment) ast.UnitBodyItem { return c })
	for _, item := range u.Body {
		if _, ok := item.(*ast.Comment); ok {
			// A comment needs a line of its own, so the body needs lines.
			u.IsMultiline = true
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
	openLine := 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		c.IsGrouped = true
		openLine = b.posFromToken(it.shift()).Line // lparen
	}
	for !it.done() {
		switch {
		case it.isNonTerminal() && it.symbol() == ConstSpec:
			c.Specs = append(c.Specs, b.buildConstSpec(it.enter()))
		case !it.isNonTerminal() && it.tokenType() == RPAREN:
			c.EndPos = ast.Pos(b.posFromToken(it.shift()))
		default:
			it.skip() // comma
		}
	}
	c.Tail = b.attachSpecComments(openLine, c.EndPos.Line, c.Specs)
	return c
}

func (b *builder) buildConstSpec(it nodeIter) ast.VarSpec {
	// ConstSpec = IdentList [ Type ] assign Expr .
	var spec ast.VarSpec
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		spec.Names, spec.NamePositions = b.buildIdentListWithPos(it.enter())
		if len(spec.NamePositions) > 0 {
			spec.Pos = spec.NamePositions[0]
		}
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
	openLine := 0
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		v.IsGrouped = true
		openLine = b.posFromToken(it.shift()).Line // lparen
	}
	for !it.done() {
		switch {
		case it.isNonTerminal() && it.symbol() == VarSpec:
			v.Specs = append(v.Specs, b.buildVarSpec(it.enter()))
		case !it.isNonTerminal() && it.tokenType() == RPAREN:
			v.EndPos = ast.Pos(b.posFromToken(it.shift()))
		default:
			it.skip() // comma
		}
	}
	v.Tail = b.attachSpecComments(openLine, v.EndPos.Line, v.Specs)
	return v
}

// attachSpecComments hands each spec in a group the comments written above it
// and after it, and returns the ones left between the last spec and the closing
// paren.
//
// A group is not a StmtBlock, so the statement-level pass cannot descend into
// it: everything written inside came out below the whole declaration, in a pile
// and detached from what it documented. A one-line group shares its line with
// whatever follows, so it claims nothing.
func (b *builder) attachSpecComments(openLine, endLine int, specs []ast.VarSpec) []*ast.Comment {
	if openLine == 0 || endLine <= openLine {
		return nil
	}
	prev := openLine
	for i := range specs {
		line := specs[i].Pos.Line
		if line == 0 {
			continue
		}
		specs[i].Leading = b.claimComments(prev, line)
		specs[i].Trailing = b.claimInlineComment(line)
		prev = line
	}
	return b.claimComments(prev, endLine)
}

func (b *builder) buildVarSpec(it nodeIter) ast.VarSpec {
	// VarSpec = IdentList [ Type ] assign Expr { VarHandler } .
	var spec ast.VarSpec
	if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
		spec.Names, spec.NamePositions = b.buildIdentListWithPos(it.enter())
		if len(spec.NamePositions) > 0 {
			spec.Pos = spec.NamePositions[0]
		}
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == Type {
		spec.Type = b.buildType(it.enter())
	}
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == ASSIGN {
			it.skip()
		} else if it.isNonTerminal() && it.symbol() == VarHandler {
			spec.Handlers = append(spec.Handlers, b.buildAtHandler(it.enter()))
		} else if it.isNonTerminal() {
			spec.Default = b.buildExprNonTerminal(&it)
		} else {
			it.skip()
		}
	}
	return spec
}

// buildAtHandler builds either of the two identical productions
//
//	VarHandler = at ident [ lparen [ IdentList ] rparen ] StmtBlock .
//	EventArg   = at ident [ lparen [ IdentList ] rparen ] StmtBlock .
//
// -- a handler declared on a var and one supplied as an argument are the same
// construct in two positions, so they share a builder.
func (b *builder) buildAtHandler(it nodeIter) ast.EventHandler {
	it.skip() // at
	nameTok := it.shift()
	h := ast.EventHandler{
		Pos:  ast.Pos(b.posFromToken(nameTok)),
		Name: nameTok.Literal,
	}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		it.skip() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == IdentList {
			names, positions := b.buildIdentListWithPos(it.enter())
			for i, name := range names {
				h.Params.Params = append(h.Params.Params, ast.Param{Pos: positions[i], Name: name})
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
	names, _ := b.buildIdentListWithPos(it)
	return names
}

// buildIdentListWithPos returns parallel slices of names and their
// source positions, in iteration order.
func (b *builder) buildIdentListWithPos(it nodeIter) ([]string, []ast.Pos) {
	// IdentList = ident { comma ident } .
	var names []string
	var positions []ast.Pos
	for !it.done() {
		if !it.isNonTerminal() && it.tokenType() == IDENT {
			tok := it.shift()
			names = append(names, tok.Literal)
			positions = append(positions, ast.Pos(b.posFromToken(tok)))
		} else {
			it.skip() // comma
		}
	}
	return names, positions
}

// --- Functions ---

func (b *builder) buildFuncDecl(it nodeIter) *ast.FuncDef {
	// FuncDecl = kw_func FuncName [ TargetIndex ] FuncTail .
	pos := b.posFromToken(it.shift()) // kw_func
	f := &ast.FuncDef{Pos: pos}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncName {
		b.buildFuncName(it.enter(), f)
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == TargetIndex {
		f.Target = b.buildTargetIndex(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == FuncTail {
		b.buildFuncTail(it.enter(), f)
	}
	return f
}

// buildTargetIndex builds the `[expr]` index that names the target a
// declaration implements.
func (b *builder) buildTargetIndex(it nodeIter) ast.Expr {
	// TargetIndex = lbracket Expr rbracket .
	it.skip() // lbracket
	var e ast.Expr
	if !it.done() && it.isNonTerminal() && it.symbol() == Expr {
		e = b.buildExpr(it.enter())
	}
	return e
}

func (b *builder) buildFuncName(it nodeIter, f *ast.FuncDef) {
	// FuncName = ident [ TypeParamList [ dot ident [ TypeParamList ] ] | dot ident [ TypeParamList ] ] .
	//
	// All cases after the leading ident:
	//   TypeParamList dot ident [TypeParamList] → recv<T>.method[<U>]
	//     RecvTypeParams=[T], TypeParams=[U], Name="recv.method"
	//   TypeParamList (no dot)                  → name<T>
	//     TypeParams=[T], Name="name"
	//   dot ident [TypeParamList]               → recv.method[<T>]
	//     TypeParams=[T], Name="recv.method"
	//   (nothing)                               → name
	//     Name="name"
	first := it.shift().Literal
	if it.done() {
		f.Name = first
		return
	}
	if it.isNonTerminal() && it.symbol() == TypeParamList {
		params := b.buildTypeParams(it.enter())
		if !it.done() && !it.isNonTerminal() && it.tokenType() == DOT {
			// recv<T>.method[<U>] — receiver-level type params
			it.skip() // dot
			f.RecvTypeParams = params
			f.Name = first + "." + it.shift().Literal
			// optional method-level type params after the method name
			if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
				f.TypeParams = b.buildTypeParams(it.enter())
			}
		} else {
			// name<T> — function-level type params only
			f.TypeParams = params
			f.Name = first
		}
		return
	}
	if !it.isNonTerminal() && it.tokenType() == DOT {
		it.skip() // dot
		f.Name = first + "." + it.shift().Literal
		// optional method-level type params after the method name
		if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
			f.TypeParams = b.buildTypeParams(it.enter())
		}
		return
	}
	// plain function name, no type params, no dot
	f.Name = first
}

func (b *builder) buildFuncTail(it nodeIter, f *ast.FuncDef) {
	// FuncTail = lparen [ ParamList ] rparen FuncBodyTail | FuncBodyTail .
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		open := it.shift() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == ParamList {
			f.Params = b.buildParamList(it.enter(), open.Line)
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

func (b *builder) buildTypeParams(it nodeIter) []ast.TypeParam {
	// TypeParamList = lt TypeParam { comma TypeParam } gt .
	var out []ast.TypeParam
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == TypeParam {
			out = append(out, b.buildTypeParam(it.enter()))
			continue
		}
		it.skip() // lt, gt, comma
	}
	return out
}

func (b *builder) buildTypeParam(it nodeIter) ast.TypeParam {
	// TypeParam = ident [ assign Type ] .
	var tp ast.TypeParam
	for !it.done() {
		if it.isNonTerminal() {
			if it.symbol() == Type {
				tp.Default = b.buildType(it.enter())
				continue
			}
			it.skip()
			continue
		}
		if it.tokenType() == IDENT && tp.Name == "" {
			tok := it.shift()
			tp.Pos, tp.Name = b.posFromToken(tok), tok.Literal
			continue
		}
		it.skip() // assign
	}
	return tp
}

func (b *builder) buildParamList(it nodeIter, openLine int) ast.ParamList {
	// ParamList = Param { comma Param } .
	var pl ast.ParamList
	var lines []int
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Param {
			sub := it.enter()
			p := b.buildParam(sub)
			if !pl.Pos.IsSet() {
				pl.Pos = p.Pos
			}
			pl.Params = append(pl.Params, p)
			lines = append(lines, p.Pos.Line)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				pl.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	pl.IsMultiline = pl.IsMultiline || spansLines(openLine, lines)
	b.attachParamComments(openLine, &pl)
	return pl
}

func (b *builder) buildParam(it nodeIter) ast.Param {
	// Param = { MacroAttr } ident [ Type ] [ assign Expr ] .
	attrs := b.buildParamAttrs(&it)
	nameTok := it.shift()
	p := ast.Param{
		Pos:   ast.Pos(b.posFromToken(nameTok)),
		Name:  nameTok.Literal,
		Attrs: attrs,
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
	// ComponentDecl = kw_component ident [ dot ident ] [ TypeParamList ] [ TargetIndex ] [ lparen [ CompParamList ] rparen ] [ Type ] StmtBlock .
	pos := b.posFromToken(it.shift()) // kw_component
	c := &ast.ComponentDecl{Pos: pos}
	c.Name = it.shift().Literal // ident
	// Optional .ident for qualified component decls (e.g. component sngl.vbox).
	if !it.done() && !it.isNonTerminal() && it.tokenType() == DOT {
		it.skip() // dot
		c.Name = c.Name + "." + it.shift().Literal
	}
	// After the (possibly qualified) name, `lt` can only open a type parameter
	// list -- a target index opens with `lbracket` and a prop list with
	// `lparen` -- so the three optionals stay LL(1) in this order.
	if !it.done() && it.isNonTerminal() && it.symbol() == TypeParamList {
		c.TypeParams = b.buildTypeParams(it.enter())
	}
	if !it.done() && it.isNonTerminal() && it.symbol() == TargetIndex {
		c.Target = b.buildTargetIndex(it.enter())
	}

	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		c.HasParens = true
		open := it.shift() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == CompParamList {
			c.Props = b.buildCompParamList(it.enter(), open.Line)
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

func (b *builder) buildCompParamList(it nodeIter, openLine int) ast.PropList {
	// CompParamList = CompParam { comma CompParam } .
	var pl ast.PropList
	var lines []int
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == CompParam {
			p := b.buildCompParam(it.enter())
			pl.Props = append(pl.Props, p)
			lines = append(lines, propPos(p).Line)
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				pl.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	pl.IsMultiline = pl.IsMultiline || spansLines(openLine, lines)
	b.attachPropComments(openLine, &pl)
	return pl
}

// claimInlineComment takes the comment written after code on line, if there is
// one that nothing else has claimed.
func (b *builder) claimInlineComment(line int) *ast.Comment {
	if b.claimed == nil {
		b.claimed = map[int]bool{}
	}
	for i, tok := range b.comments {
		if tok.Line > line {
			break
		}
		if tok.Line != line || b.claimed[i] || !b.isInlineComment(tok) {
			continue
		}
		b.claimed[i] = true
		return b.commentToStmt(tok)
	}
	return nil
}

// attachPropComments hands the props of a list written across lines the
// comments written among them. A list written on one line shares its line with
// whatever follows it, so a comment there is not the list's to take.
func (b *builder) attachPropComments(openLine int, pl *ast.PropList) {
	if !pl.IsMultiline || openLine == 0 {
		return
	}
	prev := openLine
	for i, p := range pl.Props {
		line := propPos(p).Line
		if line == 0 {
			continue
		}
		leading := b.claimComments(prev, line)
		trailing := b.claimInlineComment(line)
		prev = line
		if leading == nil && trailing == nil {
			continue
		}
		switch v := p.(type) {
		case ast.Param:
			v.Leading, v.Trailing = leading, trailing
			pl.Props[i] = v
		case ast.EventDecl:
			v.Leading, v.Trailing = leading, trailing
			pl.Props[i] = v
		case ast.SlotDecl:
			v.Leading, v.Trailing = leading, trailing
			pl.Props[i] = v
		}
	}
}

// attachParamComments is attachPropComments for a function's parameters.
func (b *builder) attachParamComments(openLine int, pl *ast.ParamList) {
	if !pl.IsMultiline || openLine == 0 {
		return
	}
	prev := openLine
	for i := range pl.Params {
		line := pl.Params[i].Pos.Line
		if line == 0 {
			continue
		}
		pl.Params[i].Leading = b.claimComments(prev, line)
		pl.Params[i].Trailing = b.claimInlineComment(line)
		prev = line
	}
}

// propPos is the source position of a component prop, whichever form it takes.
func propPos(p ast.ParamOrEventDecl) ast.Pos {
	switch v := p.(type) {
	case ast.Param:
		return v.Pos
	case ast.EventDecl:
		return v.Pos
	case ast.SlotDecl:
		return v.Pos
	}
	return ast.Pos{}
}

func (b *builder) buildCompParam(it nodeIter) ast.ParamOrEventDecl {
	// CompParam = { MacroAttr } CompParamBody .
	attrs := b.buildParamAttrs(&it)
	if !it.done() && it.isNonTerminal() && it.symbol() == CompParamBody {
		it = it.enter()
	}
	return b.buildCompParamBody(it, attrs)
}

func (b *builder) buildCompParamBody(it nodeIter, attrs []ast.MacroAttr) ast.ParamOrEventDecl {
	// CompParamBody = colon ident [Type] [assign Expr] | at ident [Type] | SlotParam | ident [CompParamTail] .
	if it.done() {
		return ast.Param{Attrs: attrs}
	}
	if it.isNonTerminal() && it.symbol() == SlotParam {
		return b.buildSlotParam(it.enter(), attrs)
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
				Attrs:         attrs,
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
			e.Attrs = attrs
			return e
		case IDENT:
			nameTok := it.shift()
			p := ast.Param{
				Pos:   ast.Pos(b.posFromToken(nameTok)),
				Name:  nameTok.Literal,
				Attrs: attrs,
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == CompParamTail {
				b.buildCompParamTail(it.enter(), &p)
			}
			return p
		}
	}
	it.skip()
	return ast.Param{Attrs: attrs}
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
	var elemID string
	for !it.done() && it.isNonTerminal() && it.symbol() == StmtPostfixOp {
		var id string
		base, lastBlock, args, id = b.applyStmtPostfixOp(it.enter(), base, lastBlock, args)
		if id != "" {
			elemID = id
		}
	}
	// A `name #id(...)` declaration threads its id here; land it on the
	// CallExpr so downstream (VisualNode decomposition, context decls,
	// element-ref calls) reads it from a single field.
	if elemID != "" {
		if call, ok := base.(*ast.CallExpr); ok {
			call.ID = elemID
		}
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

	// Visual node: has a block body or args
	if lastBlock != nil || args != nil {
		vn := &ast.VisualNode{
			Pos: ast.Pos(*base.ExprPos()),
		}
		// If base is a CallExpr, decompose: Target = Func, Args = call's args,
		// and carry the element-ref id (`name #id(...)`) onto the node.
		if call, ok := base.(*ast.CallExpr); ok {
			if target, ok := call.Func.(ast.TargetExpr); ok {
				vn.Target = target
			}
			vn.Args = call.Args
			vn.ID = call.ID
			vn.HasParens = true
		} else {
			if target, ok := base.(ast.TargetExpr); ok {
				vn.Target = target
			}
			if args != nil {
				vn.Args = *args
				vn.HasParens = true
			}
			vn.ID = elemID
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

	// Bare expression as a visual node (e.g. `spacer #s`).
	vn := &ast.VisualNode{
		Pos: ast.Pos(*base.ExprPos()),
		ID:  elemID,
	}
	if target, ok := base.(ast.TargetExpr); ok {
		vn.Target = target
	}
	return vn
}

func (b *builder) applyStmtPostfixOp(it nodeIter, base ast.Expr, lastBlock *ast.StmtBlock, lastArgs *ast.ArgList) (ast.Expr, *ast.StmtBlock, *ast.ArgList, string) {
	// StmtPostfixOp = dot ident | hash | lbracket Expr rbracket
	//               | lparen [ ArgList ] rparen | StmtBlock .
	// The last return value carries the element-reference id from a `hash`
	// postfix (`name #id`); the caller attaches it to the enclosing node.
	if it.done() {
		return base, lastBlock, lastArgs, ""
	}

	if it.isNonTerminal() {
		switch it.symbol() {
		case StmtBlock:
			block := b.buildStmtBlock(it.enter())
			return base, &block, lastArgs, ""
		case ArgList:
			args := b.buildArgList(it.enter(), 0)
			return base, lastBlock, &args, ""
		}
		it.skip()
		return base, lastBlock, lastArgs, ""
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
				}, nil, nil, ""
			}
		}
	case HASH:
		// `name #id` — an element-reference declaration. Leave base
		// unchanged and hand the id back to the caller, which lands it on
		// the CallExpr / VisualNode being built.
		ref := it.shift()
		return base, lastBlock, lastArgs, ref.Literal
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
		}, nil, nil, ""
	case LPAREN:
		it.skip() // lparen
		call := &ast.CallExpr{
			Pos:  ast.Pos(b.posFromToken(tok)),
			Func: base,
		}
		if !it.done() && it.isNonTerminal() && it.symbol() == ArgList {
			call.Args = b.buildArgList(it.enter(), tok.Line)
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == RPAREN {
			it.skip() // rparen
		}
		if !it.done() && it.isNonTerminal() && it.symbol() == StmtBlock {
			block := b.buildStmtBlock(it.enter())
			return call, &block, nil, ""
		}
		return call, nil, nil, ""
	case LBRACE:
		// StmtBlock path
		if it.isNonTerminal() && it.symbol() == StmtBlock {
			block := b.buildStmtBlock(it.enter())
			return base, &block, lastArgs, ""
		}
	}
	if !it.done() {
		it.skip()
	}
	return base, lastBlock, lastArgs, ""
}

// buildStatementPrimary builds StatementPrimary and CondPrimary alike: the
// latter is the former minus the brace literal, so no alternative it can hold
// is one this does not already build.
func (b *builder) buildStatementPrimary(it nodeIter) ast.Expr {
	// StatementPrimary has the same alternatives as PrimaryExpr minus FuncLit/StructLitBody
	return b.buildPrimaryInner(it, false)
}

// --- StmtBlock ---

func (b *builder) buildStmtBlock(it nodeIter) ast.StmtBlock {
	// StmtBlock = lbrace { [ slashdash ] { MacroAttr } Stmt semi } rbrace .
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
				block.EndPos = ast.Pos(b.posFromToken(tok))
				it.skip()
				continue
			}
			if tok.Type == SLASHDASH {
				it.skip() // slashdash
				// consume any MacroAttr non-terminals after slashdash
				for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
					it.skip()
				}
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
		if it.symbol() == MacroAttr {
			// Collect consecutive MacroAttr non-terminals.
			var attrs []ast.MacroAttr
			for !it.done() && it.isNonTerminal() && it.symbol() == MacroAttr {
				attrs = append(attrs, b.buildMacroAttr(it.enter()))
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == Stmt {
				if inner := b.buildStmt(it.enter()); inner != nil {
					block.Stmts = append(block.Stmts, b.attach(attrs, inner))
				}
			} else {
				b.errorf(attrs[0].Pos, "macro attribute has no following declaration")
			}
		} else if it.symbol() == Stmt {
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
	// IfNode = kw_if CondExpr StmtBlock [ kw_else ( IfNode | StmtBlock ) ] .
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
		if !it.done() && it.isNonTerminal() {
			switch it.symbol() {
			case IfNode:
				// `else if`: desugar the chained IfNode into a nested IfStmt
				// wrapped in a synthetic block, reusing IfStmt.Else StmtBlock
				// so no new AST shape is needed downstream.
				nested := b.buildIfNode(it.enter())
				stmt.Else = ast.StmtBlock{Pos: nested.Pos, Stmts: []ast.Stmt{nested}}
			case StmtBlock:
				stmt.Else = b.buildStmtBlock(it.enter())
			}
		}
	}
	return stmt
}

func (b *builder) buildForNode(it nodeIter) *ast.ForStmt {
	// ForNode = kw_for ( kw_var [ amp ] ident [ comma [ amp ] ident ] assign CondExpr | [ CondExpr ] ) StmtBlock [ kw_else StmtBlock ] .
	//
	// `var` is what distinguishes a loop that declares its element from one
	// that declares nothing (`for seq.count(3) { }`), so its absence is the
	// whole of the second form: the CondExpr, when there is one, is the head.
	// Absent, Iter stays nil and the loop is `for { }` -- what the head
	// expression *is* (iterable, condition, or nothing) is the checker's.
	pos := b.posFromToken(it.shift()) // kw_for
	stmt := &ast.ForStmt{Pos: pos}
	declares := !it.done() && !it.isNonTerminal() && it.tokenType() == KW_VAR
	if declares {
		it.skip() // kw_var
		if !it.done() && !it.isNonTerminal() && it.tokenType() == AMP {
			it.skip() // & — bind the element var as ref<T>
			stmt.KeyRef = true
		}
		stmt.Key = it.shift().Literal // ident
		if !it.done() && !it.isNonTerminal() && it.tokenType() == COMMA {
			it.skip() // comma
			if !it.done() && !it.isNonTerminal() && it.tokenType() == AMP {
				it.skip() // & on the second (element) var
				stmt.ValueRef = true
			}
			stmt.Value = it.shift().Literal
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == ASSIGN {
			it.skip() // assign
		}
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

func (b *builder) buildSlotParam(it nodeIter, attrs []ast.MacroAttr) ast.SlotDecl {
	// SlotParam = kw_slot ident [ lparen [ TypeList ] rparen ] .
	it.skip() // kw_slot
	if it.done() {
		return ast.SlotDecl{Attrs: attrs}
	}
	nameTok := it.shift()
	d := ast.SlotDecl{
		Pos:   ast.Pos(b.posFromToken(nameTok)),
		Name:  nameTok.Literal,
		Attrs: attrs,
	}
	for !it.done() {
		if it.isNonTerminal() {
			switch it.symbol() {
			case TypeList:
				d.Params = b.buildTypeList(it.enter())
			case Type:
				d.Type = b.buildType(it.enter())
			default:
				it.skip()
			}
			continue
		}
		it.skip() // lparen / rparen
	}
	return d
}

// Which site this is — anonymous insertion or population — is the checker's to
// say; both spell their arguments as expressions.
func (b *builder) buildSlotNode(it nodeIter) *ast.SlotNode {
	// SlotNode = kw_slot [ ident [ lparen [ SlotArgList ] rparen ] ] [ StmtBlock ] .
	pos := b.posFromToken(it.shift()) // kw_slot
	n := &ast.SlotNode{Pos: ast.Pos(pos)}
	for !it.done() {
		if it.isNonTerminal() {
			switch it.symbol() {
			case SlotArgList:
				n.Args = b.buildSlotArgList(it.enter())
			case StmtBlock:
				n.Block = b.buildStmtBlock(it.enter())
			default:
				it.skip()
			}
			continue
		}
		if it.tokenType() == IDENT {
			n.Name = it.shift().Literal
			continue
		}
		it.skip() // lparen / rparen
	}
	return n
}

func (b *builder) buildSlotArgList(it nodeIter) []ast.Expr {
	// SlotArgList = Expr { comma Expr } [ comma ] .
	var out []ast.Expr
	for !it.done() {
		if it.isNonTerminal() {
			if e := b.buildExpr(it.enter()); e != nil {
				out = append(out, e)
			}
			continue
		}
		it.skip() // comma
	}
	return out
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
	case I18nInterpStr:
		return b.buildI18nInterpStr(it)
	case I18nTriple:
		return b.buildI18nTriple(it)
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
	case I18N_STR_FULL:
		lit := &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringQuoted, Raw: tok.Literal}
		return &ast.I18nInterpExpr{Pos: ast.Pos(pos), Parts: []ast.Expr{lit}, Style: ast.StyleDouble}
	case I18N_TRIPLE_FULL:
		lit := &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringTrippleQuoted, Raw: tok.Literal}
		return &ast.I18nInterpExpr{Pos: ast.Pos(pos), Parts: []ast.Expr{lit}, Style: ast.StyleTriple}
	case RAW_STRING:
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringBackticked, Raw: tok.Literal}
	case HASH:
		// `#…` in value position is a color literal; the checker validates the
		// hex shape and reports `invalid color literal` otherwise. (As a
		// postfix it is instead an element reference; see applyStmtPostfixOp.)
		return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralColor, Raw: "#" + tok.Literal}
	case UNIT_LITERAL:
		return &ast.UnitLiteral{
			Pos:         ast.Pos(pos),
			LiteralExpr: ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralUnit, Raw: tok.Literal},
			Suffix:      extractUnitSuffix(tok.Literal),
		}
	case IDENT:
		// true, false and null are declarations in sngl:builtin, not names
		// the parser knows. Recognising them here made them unshadowable and
		// put three names in the grammar that the language does not reserve.
		return &ast.IdentExpr{Pos: ast.Pos(pos), Name: tok.Literal}
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
	// UnaryExpr = PostfixExpr | bang UnaryExpr | minus UnaryExpr
	//           | amp UnaryExpr | star UnaryExpr | kw_const UnaryExpr .
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
	case AMP:
		pos := b.posFromToken(it.shift())
		operand := b.buildAnyExpr(&it)
		return &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryAddr, Operand: operand}
	case STAR:
		pos := b.posFromToken(it.shift())
		operand := b.buildAnyExpr(&it)
		return &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryDeref, Operand: operand}
	}
	return b.buildTerminalExpr(&it)
}

func (b *builder) buildPostfixExpr(it nodeIter) ast.Expr {
	// PostfixExpr = PrimaryExpr { ExprPostfixOp } .
	// CondPostfixExpr = CondPrimary { CondPostfixOp } .
	if it.done() {
		return nil
	}
	var base ast.Expr
	if it.isNonTerminal() {
		switch it.symbol() {
		case PrimaryExpr:
			base = b.buildPrimaryExpr(it.enter())
		case StatementPrimary, CondPrimary:
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
			call.Args = b.buildArgList(it.enter(), 0)
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
			}
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
			call.Args = b.buildArgList(it.enter(), tok.Line)
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
		case I18nInterpStr:
			return b.buildI18nInterpStr(it.enter())
		case I18nTriple:
			return b.buildI18nTriple(it.enter())
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
		case ImportExpr:
			x := b.buildImportExpr(it.enter())
			if x == nil {
				return nil
			}
			if !it.done() && it.isNonTerminal() && it.symbol() == StructLitBody {
				x.Fields = b.buildStructLitFields(it.enter(), &x.Multiline)
			}
			return x
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
		open := it.shift() // lbracket
		le := &ast.ListExpr{Pos: ast.Pos(b.posFromToken(open))}
		if !it.done() && it.isNonTerminal() && it.symbol() == ListBody {
			le.Elements = b.buildListBody(it.enter(), &le.IsMultiline)
		}
		if !it.done() {
			// The brackets say whether the list was written across lines; a
			// trailing comma inserts no semicolon for the body to see.
			if !it.isNonTerminal() && it.token().Line > open.Line {
				le.IsMultiline = true
			}
			it.skip() // rbracket
		}
		return le
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

// buildImportExpr builds import("scheme://path").Name, the declaration an
// encoded value names. Outside a native-value parse it is an error and no
// expression at all: a hand-written program must not be able to claim a
// foreign declaration the compiler would then trust.
func (b *builder) buildImportExpr(it nodeIter) *ast.StructExpr {
	// ImportExpr = kw_import lparen str_full rparen dot ident .
	pos := ast.Pos(b.posFromToken(it.shift())) // kw_import
	if !b.nativeMode {
		b.errorf(pos, "import is not an expression")
		return nil
	}
	it.skip() // lparen
	path := stripQuotes(it.shift().Literal)
	it.skip() // rparen
	it.skip() // dot
	return &ast.StructExpr{Pos: pos, Native: &ast.NativeRef{Path: path, Name: it.shift().Literal}}
}

// --- Composite literals ---

func (b *builder) buildStructLitFields(it nodeIter, multiline *bool) []ast.StructFieldLit {
	// StructLitBody = lbrace [ AnonField { (comma | semi) AnonField } ] rbrace .
	var fields []ast.StructFieldLit
	openLine := 0
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == AnonField {
			r := b.buildAnonField(it.enter())
			fields = append(fields, r.StructField)
		} else {
			switch tok := it.token(); tok.Type {
			case SEMICOLON:
				*multiline = true
			case LBRACE:
				openLine = tok.Line
			case RBRACE:
				// The braces say whether the literal was written across
				// lines; a trailing comma inserts no semicolon to see.
				if openLine > 0 && tok.Line > openLine {
					*multiline = true
				}
			}
			it.skip() // lbrace, rbrace, comma, semi
		}
	}
	return fields
}

// anonFieldResult holds the parsed result of one AnonField node.
// When IsMap is true, the MapEntry field is valid; otherwise StructField is valid.
// IsMap is set when the key is a non-identifier expression (forces MapLit at parse time).
type anonFieldResult struct {
	IsMap       bool
	StructField ast.StructFieldLit
	MapEntry    ast.MapEntry
}

func (b *builder) buildAnonStructLit(it nodeIter) ast.Expr {
	// AnonStructLit = lbrace [ AnonField { (comma | semi) AnonField } ] rbrace .
	var pos ast.Pos
	var fields []ast.StructFieldLit
	var entries []ast.MapEntry
	anyNonIdent, multiline := false, false
	openLine := 0
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == AnonField {
			r := b.buildAnonField(it.enter())
			if r.IsMap {
				anyNonIdent = true
				entries = append(entries, r.MapEntry)
				if !pos.IsSet() {
					pos = r.MapEntry.Pos
				}
			} else {
				fields = append(fields, r.StructField)
			}
		} else {
			switch tok := it.token(); tok.Type {
			case LBRACE:
				openLine = tok.Line
				if !pos.IsSet() {
					pos = ast.Pos(b.posFromToken(tok))
				}
			case SEMICOLON:
				multiline = true
			case RBRACE:
				// The braces say whether the literal was written across
				// lines; a trailing comma inserts no semicolon to see.
				if openLine > 0 && tok.Line > openLine {
					multiline = true
				}
			}
			it.skip() // lbrace, rbrace, comma, semi
		}
	}
	// If ANY key is non-ident, produce a MapLit. Convert any already-collected
	// ident struct fields (parsed before the first non-ident was seen) into
	// MapEntry values, then append the non-ident entries.
	if anyNonIdent {
		var allEntries []ast.MapEntry
		for _, f := range fields {
			allEntries = append(allEntries, ast.MapEntry{Key: &ast.IdentExpr{Name: f.Name}, Value: f.Value})
		}
		allEntries = append(allEntries, entries...)
		return &ast.MapLit{Pos: pos, Entries: allEntries, Multiline: multiline}
	}
	s := &ast.StructExpr{Pos: pos, Fields: fields, Multiline: multiline}
	return s
}

func (b *builder) buildAnonField(it nodeIter) anonFieldResult {
	// AnonField = ellipsis Expr | Expr assign Expr .
	if it.done() {
		return anonFieldResult{}
	}
	if !it.isNonTerminal() && it.tokenType() == ELLIPSIS {
		it.skip() // ellipsis
		var val ast.Expr
		if !it.done() && it.isNonTerminal() {
			val = b.buildExpr(it.enter())
		}
		return anonFieldResult{StructField: ast.StructFieldLit{Spread: true, Value: val}}
	}
	// Expr assign Expr
	var key ast.Expr
	if !it.done() && it.isNonTerminal() {
		key = b.buildExpr(it.enter())
	}
	// Consume the assign token.
	if !it.done() && !it.isNonTerminal() {
		it.shift() // ASSIGN
	}
	var val ast.Expr
	if !it.done() && it.isNonTerminal() {
		val = b.buildExpr(it.enter())
	}
	// If key is a bare ident → struct field; otherwise → map entry (MapLit).
	if ident, ok := key.(*ast.IdentExpr); ok {
		return anonFieldResult{StructField: ast.StructFieldLit{Name: ident.Name, NamePos: ident.Pos, Value: val}}
	}
	// Non-ident key: force MapLit.
	keyPos := ast.Pos{}
	if key != nil {
		keyPos = *key.ExprPos()
	}
	return anonFieldResult{IsMap: true, MapEntry: ast.MapEntry{Pos: keyPos, Key: key, Value: val}}
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

func (b *builder) buildI18nInterpStr(it nodeIter) ast.Expr {
	// I18nInterpStr = i18n_str_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_str_end .
	var parts []ast.Expr
	pos := ast.Pos{}
	for !it.done() {
		if !it.isNonTerminal() {
			tok := it.shift()
			if !pos.IsSet() {
				pos = b.posFromToken(tok)
			}
			switch tok.Type {
			case I18N_STR_START, I18N_STR_RESUME, I18N_STR_END:
				if tok.Literal != "" {
					parts = append(parts, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		} else if it.symbol() == I18nPlaceholder {
			parts = append(parts, b.buildI18nPlaceholder(it.enter()))
		} else {
			it.skip()
		}
	}
	return &ast.I18nInterpExpr{Pos: pos, Parts: parts, Style: ast.StyleDouble}
}

func (b *builder) buildI18nTriple(it nodeIter) ast.Expr {
	// I18nTriple = i18n_triple_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_triple_end .
	var parts []ast.Expr
	pos := ast.Pos{}
	for !it.done() {
		if !it.isNonTerminal() {
			tok := it.shift()
			if !pos.IsSet() {
				pos = b.posFromToken(tok)
			}
			switch tok.Type {
			case I18N_TRIPLE_START, I18N_STR_RESUME, I18N_TRIPLE_END:
				if tok.Literal != "" {
					parts = append(parts, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringTrippleQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		} else if it.symbol() == I18nPlaceholder {
			parts = append(parts, b.buildI18nPlaceholder(it.enter()))
		} else {
			it.skip()
		}
	}
	return &ast.I18nInterpExpr{Pos: pos, Parts: parts, Style: ast.StyleTriple}
}

// claimComments takes the comments written between two lines for an expression
// that will carry them itself, and hides them from the statement-level pass.
func (b *builder) claimComments(fromLine, beforeLine int) []*ast.Comment {
	var out []*ast.Comment
	if b.claimed == nil {
		b.claimed = map[int]bool{}
	}
	for i, tok := range b.comments {
		if tok.Line >= beforeLine {
			break
		}
		if tok.Line < fromLine || b.claimed[i] {
			continue
		}
		b.claimed[i] = true
		out = append(out, b.commentToStmt(tok))
	}
	return out
}

func (b *builder) buildI18nPlaceholder(it nodeIter) ast.Expr {
	// I18nPlaceholder = Expr [ comma ident [ comma I18nThirdArg ] ] .
	ph := &ast.I18nPlaceholderExpr{}
	// First child: Expr non-terminal.
	if !it.done() && it.isNonTerminal() {
		ph.Value = b.buildExpr(it.enter())
		if ph.Value != nil {
			ph.Pos = *ph.Value.ExprPos()
		}
	}
	// Optional: comma ident [ comma I18nThirdArg ]
	for !it.done() {
		if it.isNonTerminal() {
			if it.symbol() == I18nThirdArg {
				b.buildI18nThirdArg(ph, it.enter())
			} else {
				it.skip()
			}
		} else {
			tok := it.shift()
			switch tok.Type {
			case IDENT:
				ph.Type = tok.Literal
			case COMMA:
				// separator, ignore
			}
		}
	}
	b.attachCaseComments(ph)
	return ph
}

// attachCaseComments hands each case the comments written above it and records
// whether the cases were written one per line.
func (b *builder) attachCaseComments(ph *ast.I18nPlaceholderExpr) {
	if len(ph.Cases) == 0 {
		return
	}
	lines := make([]int, len(ph.Cases))
	for i, c := range ph.Cases {
		lines[i] = c.Pos.Line
	}
	ph.Multiline = spansLines(ph.Pos.Line, lines)
	prev := ph.Pos.Line
	for i := range ph.Cases {
		ph.Cases[i].Leading = b.claimComments(prev, ph.Cases[i].Pos.Line)
		prev = ph.Cases[i].Pos.Line
	}
}

// buildI18nThirdArg populates ph.Style or ph.Cases from an I18nThirdArg node.
// I18nThirdArg = ident [ MsgBodyTail ] | assign int_lit MsgBody { MsgCase } .
// If the ident is followed by a MsgBodyTail, it is a Selector (first case).
// If the ident stands alone (no MsgBodyTail), it is a Style.
func (b *builder) buildI18nThirdArg(ph *ast.I18nPlaceholderExpr, it nodeIter) {
	// Peek at the first token to determine which alternative we're in.
	if it.done() {
		return
	}
	if it.isNonTerminal() {
		// Starts with a non-terminal — shouldn't happen per grammar, skip.
		it.skip()
		return
	}
	tok := it.shift()
	switch tok.Type {
	case IDENT:
		// Check if a MsgBodyTail follows (making this ident a Selector).
		if !it.done() && it.isNonTerminal() && it.symbol() == MsgBodyTail {
			// ident is a Selector — build as cases.
			// MsgBodyTail = MsgBody { MsgCase } .
			var c ast.I18nCase
			c.Pos = b.posFromToken(tok)
			c.Selector = tok.Literal
			tail := it.enter()
			for !tail.done() {
				if tail.isNonTerminal() {
					switch tail.symbol() {
					case MsgBody:
						b.buildMsgBodyInto(&c, tail.enter())
					case MsgCase:
						ph.Cases = append(ph.Cases, b.buildMsgCase(tail.enter()))
					default:
						tail.skip()
					}
				} else {
					tail.skip()
				}
			}
			ph.Cases = append([]ast.I18nCase{c}, ph.Cases...)
		} else {
			// Bare ident — it's a Style.
			ph.Style = tok.Literal
		}
	case ASSIGN:
		// assign int_lit MsgBody { MsgCase } — numeric selector first case.
		var c ast.I18nCase
		c.Pos = b.posFromToken(tok)
		if !it.done() && !it.isNonTerminal() {
			n := it.shift()
			c.Selector = "=" + n.Literal
		}
		for !it.done() {
			if it.isNonTerminal() {
				switch it.symbol() {
				case MsgBody:
					b.buildMsgBodyInto(&c, it.enter())
				case MsgCase:
					ph.Cases = append(ph.Cases, b.buildMsgCase(it.enter()))
				default:
					it.skip()
				}
			} else {
				it.skip()
			}
		}
		ph.Cases = append([]ast.I18nCase{c}, ph.Cases...)
	}
}

func (b *builder) buildMsgCase(it nodeIter) ast.I18nCase {
	// MsgCase = Selector MsgBody .
	// Selector = ident | eq int_lit .
	// MsgBody = i18n_case_full | i18n_case_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_case_end .
	var c ast.I18nCase
	// Parse Selector non-terminal
	if !it.done() && it.isNonTerminal() && it.symbol() == Selector {
		sel := it.enter()
		if !sel.done() && !sel.isNonTerminal() {
			tok := sel.shift()
			c.Pos = b.posFromToken(tok)
			switch tok.Type {
			case IDENT:
				c.Selector = tok.Literal
			case ASSIGN:
				if !sel.done() && !sel.isNonTerminal() {
					n := sel.shift()
					c.Selector = "=" + n.Literal
				}
			}
		}
	}
	// Parse MsgBody: either wrapped non-terminal or inline tokens
	for !it.done() {
		if it.isNonTerminal() {
			sym := it.symbol()
			switch sym {
			case MsgBody:
				sub := it.enter()
				b.buildMsgBodyInto(&c, sub)
			case I18nPlaceholder:
				c.Body = append(c.Body, b.buildI18nPlaceholder(it.enter()))
			default:
				it.skip()
			}
		} else {
			tok := it.shift()
			switch tok.Type {
			case I18N_CASE_FULL, I18N_CASE_START, I18N_STR_RESUME, I18N_CASE_END:
				if tok.Literal != "" {
					c.Body = append(c.Body, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		}
	}
	return c
}

// buildMsgBodyInto appends literal segments and nested placeholders from a MsgBody sub-iter into c.
func (b *builder) buildMsgBodyInto(c *ast.I18nCase, it nodeIter) {
	for !it.done() {
		if it.isNonTerminal() {
			if it.symbol() == I18nPlaceholder {
				c.Body = append(c.Body, b.buildI18nPlaceholder(it.enter()))
			} else {
				it.skip()
			}
		} else {
			tok := it.shift()
			switch tok.Type {
			case I18N_CASE_FULL, I18N_CASE_START, I18N_STR_RESUME, I18N_CASE_END:
				if tok.Literal != "" {
					c.Body = append(c.Body, &ast.LiteralExpr{
						Pos:  ast.Pos(b.posFromToken(tok)),
						Kind: ast.LiteralStringQuoted,
						Raw:  tok.Literal,
					})
				}
			}
		}
	}
}

// spansLines reports whether a bracketed list was written across lines: its
// first item below the opening bracket, or its items on lines of their own.
// The closing bracket is not the test — `button(text="x", @click { … })` puts
// it on a later line without breaking the argument list up.
func spansLines(openLine int, itemLines []int) bool {
	if len(itemLines) == 0 {
		return false
	}
	if openLine > 0 && itemLines[0] > openLine {
		return true
	}
	for _, line := range itemLines[1:] {
		if line != itemLines[0] {
			return true
		}
	}
	return false
}

// --- Argument lists ---

func (b *builder) buildArgList(it nodeIter, openLine int) ast.ArgList {
	// ArgList = Arg { (comma | semi) Arg } .
	var al ast.ArgList
	var lines []int
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == Arg {
			sub := it.enter()
			a := b.buildArg(sub)
			if a != nil {
				if !al.Pos.IsSet() {
					al.Pos = ast.Pos(argPos(a))
				}
				al.Args = append(al.Args, a)
				lines = append(lines, argPos(a).Line)
			}
		} else {
			if !it.isNonTerminal() && it.tokenType() == SEMICOLON {
				al.IsMultiline = true
			}
			it.skip() // comma or semi
		}
	}
	al.IsMultiline = al.IsMultiline || spansLines(openLine, lines)
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
	//     | EventArg
	//     | ident IdentArgCont
	//     | bang UnaryExpr ArgExprCont
	//     | minus UnaryExpr ArgExprCont
	//     | NonIdentPrimary { StmtPostfixOp } ArgExprCont
	if it.done() {
		return nil
	}

	if it.isNonTerminal() && it.symbol() == EventArg {
		return b.buildAtHandler(it.enter())
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
			return ast.Arg{Name: ":" + nameTok.Literal, NamePos: ast.Pos(b.posFromToken(nameTok)), Value: value}
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
		case AMP:
			pos := b.posFromToken(it.shift())
			var operand ast.Expr
			if !it.done() && it.isNonTerminal() {
				operand = b.buildExprNonTerminal(&it)
			}
			base := &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryAddr, Operand: operand}
			expr := b.applyArgExprCont(&it, base)
			return ast.Arg{Value: expr}
		case STAR:
			pos := b.posFromToken(it.shift())
			var operand ast.Expr
			if !it.done() && it.isNonTerminal() {
				operand = b.buildExprNonTerminal(&it)
			}
			base := &ast.UnaryExpr{Pos: ast.Pos(pos), Op: ast.UnaryDeref, Operand: operand}
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
			base, lastBlock, lastArgs, _ = b.applyStmtPostfixOp(it.enter(), base, lastBlock, lastArgs)
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
		return ast.Arg{Name: identTok.Literal, NamePos: ast.Pos(b.posFromToken(identTok)), Value: val}
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
		if !it.done() && !it.isNonTerminal() && it.tokenType() == DOT {
			it.skip() // dot
			nt.Package = nt.Name
			nt.Name = it.shift().Literal
		}
		// `list<int>` and `tree.one<shape>` are both spellable, so the
		// qualified branch above falls through rather than returning.
		if !it.done() && !it.isNonTerminal() && it.tokenType() == LT {
			it.skip() // lt
			if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
				nt.TypeArgs = b.buildTypeList(it.enter())
			}
			if !it.done() && !it.isNonTerminal() && it.tokenType() == GT {
				it.skip() // gt
			}
		}
		return nt
	case KW_FUNC:
		pos := b.posFromToken(it.shift()) // kw_func
		ft := &ast.FuncType{Pos: pos}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
			it.skip() // lparen
			if !it.done() && it.isNonTerminal() && it.symbol() == FuncTypeParamList {
				ft.Params = b.buildFuncTypeParamList(it.enter())
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

func (b *builder) buildFuncTypeParamList(it nodeIter) []ast.FuncTypeParam {
	// FuncTypeParamList = FuncTypeParam { comma FuncTypeParam } .
	var params []ast.FuncTypeParam
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == FuncTypeParam {
			params = append(params, b.buildFuncTypeParam(it.enter()))
		} else {
			it.skip() // comma
		}
	}
	return params
}

func (b *builder) buildFuncTypeParam(it nodeIter) ast.FuncTypeParam {
	// FuncTypeParam =
	//   ident [ dot ident [ lt TypeList gt ] | lt TypeList gt | Type ]
	// | kw_func lparen [ FuncTypeParamList ] rparen [ Type ]
	// | StructDecl | EnumDecl | UnitDecl .
	//
	// The parse tree is flat: tokens and nonterminals are direct children.
	if it.done() {
		return ast.FuncTypeParam{}
	}

	// Non-ident leading: anonymous compound type (kw_func/kw_struct/kw_enum/kw_unit).
	if it.isNonTerminal() {
		return ast.FuncTypeParam{Type: b.buildType(it)}
	}

	tok := it.tokenType()
	if tok != IDENT {
		// kw_func and friends (when not wrapped in a nonterminal)
		return ast.FuncTypeParam{Type: b.buildType(it)}
	}

	// Leading ident — shift it, then inspect what follows.
	identTok := it.shift()
	identName := identTok.Literal
	identPos := b.posFromToken(identTok)

	if it.done() {
		// Bare ident → anonymous simple type.
		return ast.FuncTypeParam{Type: &ast.NamedType{Pos: identPos, Name: identName}}
	}

	if it.isNonTerminal() {
		// Type nonterminal → leading ident is the param name.
		typ := b.buildType(it.enter())
		return ast.FuncTypeParam{Name: identName, Type: typ}
	}

	// Terminal follows: dot, lt, or something unexpected.
	switch it.tokenType() {
	case DOT:
		it.skip() // dot
		qualName := ""
		if !it.done() && !it.isNonTerminal() && it.tokenType() == IDENT {
			qualName = it.shift().Literal
		}
		nt := &ast.NamedType{Pos: identPos, Package: identName, Name: qualName}
		// Optional lt TypeList gt
		if !it.done() && !it.isNonTerminal() && it.tokenType() == LT {
			it.skip() // lt
			if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
				nt.TypeArgs = b.buildTypeList(it.enter())
			}
			if !it.done() && !it.isNonTerminal() && it.tokenType() == GT {
				it.skip() // gt
			}
		}
		return ast.FuncTypeParam{Type: nt}
	case LT:
		it.skip() // lt
		nt := &ast.NamedType{Pos: identPos, Name: identName}
		if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
			nt.TypeArgs = b.buildTypeList(it.enter())
		}
		if !it.done() && !it.isNonTerminal() && it.tokenType() == GT {
			it.skip() // gt
		}
		return ast.FuncTypeParam{Type: nt}
	default:
		// Unexpected — treat ident as anonymous type.
		return ast.FuncTypeParam{Type: &ast.NamedType{Pos: identPos, Name: identName}}
	}
}

// --- Lambda ---

func (b *builder) buildFuncLit(it nodeIter) *ast.LambdaExpr {
	// FuncLit = kw_func [ lparen [ ParamList ] rparen ] FuncBodyTail .
	pos := b.posFromToken(it.shift()) // kw_func
	lam := &ast.LambdaExpr{Pos: ast.Pos(pos)}
	if !it.done() && !it.isNonTerminal() && it.tokenType() == LPAREN {
		open := it.shift() // lparen
		if !it.done() && it.isNonTerminal() && it.symbol() == ParamList {
			lam.Params = b.buildParamList(it.enter(), open.Line)
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
