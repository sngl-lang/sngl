package lsp

import (
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

type rawToken struct {
	line, col, length int
	tokenType         uint32
}

// computeSemanticTokens produces delta-encoded semantic tokens for the
// document. When pkg is non-nil, identifiers are classified by their
// resolved IR symbols; otherwise the result contains only keyword
// tokens scanned from the source.
func computeSemanticTokens(content string, doc *ast.Document, pkg *ir.Package) []uint32 {
	var tokens []rawToken

	// Keywords (always available from text scan).
	tokens = append(tokens, scanKeywordTokens(content)...)

	// IR-driven identifier classification.
	if pkg != nil {
		tokens = append(tokens, irIdentifierTokens(pkg)...)
	}

	sort.SliceStable(tokens, func(i, j int) bool {
		if tokens[i].line != tokens[j].line {
			return tokens[i].line < tokens[j].line
		}
		return tokens[i].col < tokens[j].col
	})
	tokens = dedupeSameStartTokens(tokens)
	return encodeDeltaTokens(tokens)
}

// dedupeSameStartTokens drops tokens that share the same line+col as
// an earlier one. The LSP spec disallows overlapping tokens at the
// same start; the earlier (more specific) one wins.
func dedupeSameStartTokens(tokens []rawToken) []rawToken {
	out := tokens[:0]
	lastLine, lastCol := -1, -1
	for _, t := range tokens {
		if t.line == lastLine && t.col == lastCol {
			continue
		}
		out = append(out, t)
		lastLine = t.line
		lastCol = t.col
	}
	return out
}

func encodeDeltaTokens(tokens []rawToken) []uint32 {
	data := []uint32{}
	prevLine, prevCol := 0, 0
	for _, t := range tokens {
		deltaLine := t.line - prevLine
		deltaCol := t.col
		if deltaLine == 0 {
			deltaCol = t.col - prevCol
		}
		data = append(data, uint32(deltaLine), uint32(deltaCol), uint32(t.length), t.tokenType, 0)
		prevLine = t.line
		prevCol = t.col
	}
	return data
}

// scanKeywordTokens emits one token per recognized keyword occurrence,
// skipping strings and comments. Identifier classification comes from
// the IR walker.
func scanKeywordTokens(content string) []rawToken {
	keywords := map[string]bool{
		"var": true, "const": true, "func": true, "component": true,
		"struct": true, "enum": true, "unit": true, "window": true,
		"if": true, "else": true, "for": true, "return": true,
		"break": true, "continue": true,
		"import": true,
	}
	var out []rawToken
	lines := strings.Split(content, "\n")
	for lineIdx, line := range lines {
		i := 0
		inString := false
		var stringQuote byte
		for i < len(line) {
			ch := line[i]
			if inString {
				if ch == '\\' && i+1 < len(line) {
					i += 2
					continue
				}
				if ch == stringQuote {
					inString = false
				}
				i++
				continue
			}
			if ch == '"' || ch == '\'' || ch == '`' {
				inString = true
				stringQuote = ch
				i++
				continue
			}
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
				break
			}
			if isIdentStart(ch) {
				start := i
				for i < len(line) && isIdentChar(line[i]) {
					i++
				}
				word := line[start:i]
				if keywords[word] {
					out = append(out, rawToken{
						line: lineIdx, col: start, length: len(word),
						tokenType: stKeyword,
					})
				}
				continue
			}
			i++
		}
	}
	return out
}

// irIdentifierTokens walks pkg producing one rawToken per classifiable
// IR node anchored to a source position.
func irIdentifierTokens(pkg *ir.Package) []rawToken {
	var out []rawToken
	w := &irTokenWalker{out: &out}
	for _, v := range pkg.Consts {
		w.expr(v.Init)
	}
	for _, v := range pkg.Vars {
		w.expr(v.Init)
	}
	for _, f := range pkg.Funcs {
		w.fn_(f)
	}
	for _, c := range pkg.Components {
		w.component(c)
	}
	for _, win := range pkg.Windows {
		w.window(win)
	}
	return out
}

type irTokenWalker struct {
	out *[]rawToken
}

func (w *irTokenWalker) emit(pos ast.Pos, length int, kind uint32) {
	if !pos.IsValid() {
		return
	}
	*w.out = append(*w.out, rawToken{
		line:      pos.Line - 1,
		col:       pos.Column - 1,
		length:    length,
		tokenType: kind,
	})
}

func (w *irTokenWalker) component(c *ir.Component) {
	if c == nil {
		return
	}
	for _, p := range c.Props {
		w.expr(p.Default)
	}
	for _, v := range c.Vars {
		w.expr(v.Init)
	}
	for _, f := range c.Funcs {
		w.fn_(f)
	}
	w.stmts(c.Body)
}

func (w *irTokenWalker) window(win *ir.Window) {
	if win == nil {
		return
	}
	for i := range win.Props {
		w.expr(win.Props[i].Value)
	}
	w.stmts(win.Body)
	if win.ErrorHandler != nil {
		w.fn_(win.ErrorHandler.Func)
	}
}

func (w *irTokenWalker) fn_(f *ir.Func) {
	if f == nil {
		return
	}
	for _, p := range f.Params {
		if p != nil {
			w.expr(p.Default)
		}
	}
	w.stmts(f.Block)
}

func (w *irTokenWalker) stmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.stmt(s)
	}
}

func (w *irTokenWalker) stmt(s ir.Stmt) {
	switch x := s.(type) {
	case nil:
		return
	case *ir.NodeInst:
		w.nodeInst(x)
	case *ir.CallStmt:
		if x.Call != nil {
			w.expr(x.Call)
		}
	case *ir.SlotInst:
		w.stmts(x.Children)
	case *ir.ErrorBoundary:
		if x.Handler != nil {
			w.fn_(x.Handler.Func)
		}
		w.stmts(x.Children)
	case *ir.Assign:
		w.expr(x.Target)
		w.expr(x.Value)
	case *ir.Toggle:
		w.expr(x.Target)
	case *ir.Emit:
		for _, a := range x.Args {
			w.expr(a.Value)
		}
	case *ir.LocalVar:
		w.expr(x.Init)
	case *ir.Return:
		w.expr(x.Value)
	case *ir.If:
		w.expr(x.Cond)
		w.stmts(x.Body)
		w.stmts(x.Else)
	case *ir.For:
		w.expr(x.Iter)
		w.stmts(x.Body)
		w.stmts(x.Else)
	case *ir.Window:
		w.window(x)
	}
}

func (w *irTokenWalker) nodeInst(n *ir.NodeInst) {
	// Tag position: pull from the AST node. *ast.VisualNode has Pos
	// at the tag; that's the component/element name location.
	if vn, ok := n.AST.(*ast.VisualNode); ok {
		// Tag position. The Target identifier (or its head for
		// qualified ns.Name) sits at or just after vn.Pos. We use
		// vn.Pos for the start.
		w.emit(vn.Pos, len(n.Name), stClass)
	}
	for _, h := range n.Handlers {
		w.fn_(h.Func)
	}
	for _, a := range n.Props {
		if a.Name != "" && a.NamePos.IsSet() {
			w.emit(a.NamePos, len(a.Name), stProperty)
		}
		w.expr(a.Value)
	}
	w.expr(n.Key)
	w.expr(n.Ref)
	w.stmts(n.Children)
}

func (w *irTokenWalker) expr(e ir.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Literal:
		return
	case *ir.Ident:
		w.ident(x)
	case *ir.Binary:
		w.expr(x.Left)
		w.expr(x.Right)
	case *ir.Unary:
		w.expr(x.Operand)
	case *ir.Ternary:
		w.expr(x.Cond)
		w.expr(x.Then)
		w.expr(x.Else)
	case *ir.Call:
		w.expr(x.Callee)
		w.expr(x.Receiver)
		for _, a := range x.Args {
			w.expr(a.Value)
		}
		if x.ErrorHandler != nil {
			w.fn_(x.ErrorHandler.Func)
		}
	case *ir.Conversion:
		w.expr(x.Operand)
	case *ir.Select:
		w.expr(x.Operand)
	case *ir.Index:
		w.expr(x.Operand)
		w.expr(x.Idx)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Name != "" && f.NamePos.IsSet() {
				w.emit(f.NamePos, len(f.Name), stProperty)
			}
			w.expr(f.Value)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			w.expr(el)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			w.expr(kv.Key)
			w.expr(kv.Value)
		}
	case *ir.Spread:
		w.expr(x.Operand)
	case *ir.Lambda:
		w.fn_(x.Func)
	case *ir.Closure:
		w.fn_(x.Func)
		if x.State != nil {
			w.expr(x.State)
		}
	}
}

func (w *irTokenWalker) ident(x *ir.Ident) {
	if x.AST == nil || x.Synthesized {
		return
	}
	pos := x.AST.Pos
	length := len(x.AST.Name)
	if length == 0 {
		return
	}
	tok := uint32(stVariable)
	switch sym := x.Sym.(type) {
	case *ir.StructDef, *ir.EnumDef, *ir.UnitDef:
		tok = stType
	case *ir.Component:
		tok = stClass
	case *ir.Func:
		if sym.Receiver != "" {
			tok = stMethod
		} else {
			tok = stFunction
		}
	case *ir.Param:
		tok = stParameter
	case *ir.Import:
		tok = stNamespace
	case *ir.Namespace:
		tok = stNamespace
	case *ir.Var:
		tok = stVariable
	case *ir.LoopVar:
		tok = stVariable
	default:
		_ = sym
	}
	w.emit(pos, length, tok)
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentChar(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}
