package lspcore

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Hover returns markdown hover info for the word at the given 1-based position.
func Hover(content string, doc *ast.Document, line, col int) string {
	if doc == nil {
		return ""
	}
	word := WordAtPosition(content, line, col)
	if word == "" {
		return ""
	}
	return HoverInfo(doc, word)
}

func WordAtPosition(content string, line, col int) string {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	l := lines[line-1]
	runes := []rune(l)
	if col < 1 || col > len(runes)+1 {
		return ""
	}

	idx := col - 1
	start := idx
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}
	end := idx
	for end < len(runes) && isIdentRune(runes[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return string(runes[start:end])
}

func isIdentRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func HoverInfo(doc *ast.Document, word string) string {
	// Search top-level and component-body statements.
	if info := hoverInStmts(doc.Stmts, doc, word); info != "" {
		return info
	}

	// TODO: stdlib hover lookup (LoadStdlib removed in v2)

	return ""
}

// hoverInStmts searches a slice of statements for hover info, recursing
// into component bodies.
func hoverInStmts(stmts []ast.Stmt, doc *ast.Document, word string) string {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDecl:
			if info := formatVarLike(s.Specs, "var", word); info != "" {
				return info
			}
		case *ast.FuncDef:
			if s.Name == word {
				return formatFuncHover(s, doc)
			}
		case *ast.ConstDecl:
			if info := formatVarLike(s.Specs, "const", word); info != "" {
				return info
			}
		case *ast.ComponentDecl:
			if s.Name == word {
				return formatComponentHoverWithDoc(s, doc)
			}
			// Also search inside the component body for nested declarations.
			if info := hoverInStmts(s.Body.Stmts, doc, word); info != "" {
				return info
			}
		case *ast.StructDef:
			if s.Name == word {
				return formatStructHover(s)
			}
		case *ast.EnumDef:
			if s.Name == word {
				var names []string
				for _, m := range s.Members {
					names = append(names, m.Name)
				}
				return fmt.Sprintf("```sngl\nenum %s { %s }\n```", s.Name, strings.Join(names, ", "))
			}
		}
	}
	return ""
}

func formatComponentHoverWithDoc(c *ast.ComponentDecl, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s\n```\n", c.Name)
	if doc != nil {
		if d := docForPos(doc, c.Pos); d != "" {
			fmt.Fprintf(&sb, "\n%s\n", d)
		}
	}
	var params []ast.Param
	for _, p := range c.Props.Props {
		if param, ok := p.(ast.Param); ok {
			params = append(params, param)
		}
	}
	if len(params) > 0 {
		sb.WriteString("\n**Params:**\n")
		for _, p := range params {
			hint := typeExprString(p.Type)
			if hint == "" {
				hint = "dyn"
			}
			if p.Default == nil {
				fmt.Fprintf(&sb, "- `%s` %s (required)\n", p.Name, hint)
			} else {
				fmt.Fprintf(&sb, "- `%s` %s\n", p.Name, hint)
			}
		}
	}
	return sb.String()
}

// docForPos extracts a doc comment immediately preceding the given position.
// It looks for contiguous // comment lines ending on the line before pos.Line.
func docForPos(doc *ast.Document, pos ast.Pos) string {
	if doc == nil {
		return ""
	}
	// Collect all comments from doc.Stmts.
	var comments []*ast.Comment
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.Comment); ok {
			comments = append(comments, c)
		}
	}
	if len(comments) == 0 {
		return ""
	}
	targetLine := pos.Line
	var lines []string
	// Gather contiguous comment lines ending at targetLine-1
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Block {
			continue
		}
		if c.Pos.Line == targetLine-1-len(lines) {
			text := strings.TrimPrefix(c.Text, "//")
			text = strings.TrimPrefix(text, " ")
			lines = append(lines, text)
		} else if len(lines) > 0 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	// Reverse since we collected bottom-up
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

func formatStructHover(s *ast.StructDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nstruct %s {\n", s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&sb, "    %s %s\n", strings.Join(f.Names, ", "), typeExprString(f.Type))
	}
	sb.WriteString("}\n```")
	return sb.String()
}

// typeExprString converts a TypeExpr to a readable string.
func typeExprString(te ast.TypeExpr) string {
	if te == nil {
		return ""
	}
	switch t := te.(type) {
	case *ast.NamedType:
		s := t.Name
		if t.Package != "" {
			s = t.Package + "." + s
		}
		if len(t.TypeArgs) > 0 {
			var parts []string
			for _, arg := range t.TypeArgs {
				parts = append(parts, typeExprString(arg))
			}
			s += "<" + strings.Join(parts, ", ") + ">"
		}
		return s
	case *ast.FuncType:
		var params []string
		for _, p := range t.Params {
			params = append(params, typeExprString(p))
		}
		s := "func(" + strings.Join(params, ", ") + ")"
		if t.Return != nil {
			s += " -> " + typeExprString(t.Return)
		}
		return s
	default:
		return fmt.Sprintf("%v", te)
	}
}

func formatFuncHover(f *ast.FuncDef, doc *ast.Document) string {
	var sb strings.Builder
	sb.WriteString("```sngl\nfunc ")
	sb.WriteString(f.Name)
	sb.WriteByte('(')
	for i, p := range f.Params.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.Name)
		if t := typeExprString(p.Type); t != "" {
			sb.WriteByte(' ')
			sb.WriteString(t)
		}
	}
	sb.WriteByte(')')
	if rt := typeExprString(f.ReturnType); rt != "" {
		sb.WriteByte(' ')
		sb.WriteString(rt)
	}
	sb.WriteString("\n```\n")
	if d := docForPos(doc, f.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func formatVarLike(specs []ast.VarSpec, kw, word string) string {
	for _, spec := range specs {
		for _, name := range spec.Names {
			if name != word {
				continue
			}
			var sb strings.Builder
			sb.WriteString("```sngl\n")
			sb.WriteString(kw)
			sb.WriteByte(' ')
			sb.WriteString(name)
			if t := typeExprString(spec.Type); t != "" {
				sb.WriteByte(' ')
				sb.WriteString(t)
			}
			if v := literalValueString(spec.Default); v != "" {
				sb.WriteString(" = ")
				sb.WriteString(v)
			}
			sb.WriteString("\n```\n")
			return sb.String()
		}
	}
	return ""
}

// literalValueString returns a compact rendering for compile-time-constant
// expressions. Currently handles only LiteralExpr; arithmetic/struct values
// fall through to "" (no value shown).
func literalValueString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	if lit, ok := e.(*ast.LiteralExpr); ok {
		switch lit.Kind {
		case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
			// Re-add quotes for string literals
			return `"` + lit.Raw + `"`
		default:
			return lit.Raw
		}
	}
	return ""
}

// HoverAt returns hover markdown for the cursor position. Tries literal
// hover first (for color/measurement literals where the cursor isn't on
// an identifier word), then falls back to word-based identifier hover.
func HoverAt(content string, doc *ast.Document, line, col int) string {
	// Literal lookup is added in a later task; for now, just delegate.
	return Hover(content, doc, line, col)
}
