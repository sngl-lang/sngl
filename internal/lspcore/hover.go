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
	return hoverInfoWithOptions(doc, word, HoverOptions{})
}

func hoverInfoWithOptions(doc *ast.Document, word string, opts HoverOptions) string {
	if info := hoverInStmtsWithOptions(doc.Stmts, doc, word, opts); info != "" {
		return info
	}

	if opts.StdlibSymbol != nil {
		if md, ok := opts.StdlibSymbol(word); ok {
			return md
		}
	}

	if desc, ok := keywordDocs[word]; ok {
		return fmt.Sprintf("```sngl\n%s\n```\n\n%s\n", word, desc)
	}

	return ""
}

// hoverInStmtsWithOptions searches a slice of statements for hover info,
// recursing into component bodies.
func hoverInStmtsWithOptions(stmts []ast.Stmt, doc *ast.Document, word string, opts HoverOptions) string {
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
				return formatComponentHoverWithDoc(s, doc, opts)
			}
			// Also search inside the component body for nested declarations.
			if info := hoverInStmtsWithOptions(s.Body.Stmts, doc, word, opts); info != "" {
				return info
			}
		case *ast.StructDef:
			if s.Name == word {
				return formatStructHover(s, doc)
			}
		case *ast.EnumDef:
			if s.Name == word {
				return formatEnumHover(s, doc)
			}
		case *ast.UnitDef:
			if s.Name == word {
				return formatUnitHover(s, doc)
			}
		}
	}
	return ""
}

func formatComponentHoverWithDoc(c *ast.ComponentDecl, doc *ast.Document, opts HoverOptions) string {
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
	if opts.ComponentImageURL != nil {
		if url, ok := opts.ComponentImageURL(c.Name); ok {
			fmt.Fprintf(&sb, "\n![%s](%s)\n", c.Name, url)
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

func formatStructHover(s *ast.StructDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nstruct %s {\n", s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&sb, "    %s %s\n", strings.Join(f.Names, ", "), typeExprString(f.Type))
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, s.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func formatEnumHover(e *ast.EnumDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nenum %s {\n", e.Name)
	for _, m := range e.Members {
		fmt.Fprintf(&sb, "    %s\n", m.Name)
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, e.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
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

func formatUnitHover(u *ast.UnitDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nunit %s {\n", u.Name)
	for _, sfx := range u.Suffixes {
		sb.WriteString("    ")
		sb.WriteString(sfx.Name)
		if sfx.Factor != nil {
			if lit, ok := sfx.Factor.(*ast.LiteralExpr); ok {
				sb.WriteString(" = ")
				sb.WriteString(lit.Raw)
			}
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, u.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// HoverOptions configures optional behaviors of HoverAt. Zero value is fine —
// callers that don't need any of these features pass HoverOptions{}.
type HoverOptions struct {
	// ComponentImageURL, if non-nil, is invoked when the hover formatter
	// renders a ComponentDecl. Returning ok=true causes the formatter to
	// append a markdown image embed (`![<Name>](url)`) below the signature
	// and doc text.
	ComponentImageURL func(componentName string) (url string, ok bool)
	// StdlibSymbol, if non-nil, is consulted when hoverInStmts doesn't
	// find the word in user code. The callback returns formatted
	// markdown for stdlib funcs/components/structs/units, or ok=false.
	StdlibSymbol func(name string) (markdown string, ok bool)
	// ComponentProp, if non-nil, is invoked when the cursor sits on a
	// named-arg key inside a visual node. Returns markdown describing
	// the resolved component prop.
	ComponentProp func(componentName, propName string) (markdown string, ok bool)
	// StructFieldType, if non-nil, is invoked when the cursor sits on
	// a key in a struct literal (e.g. `r` in `color{r=255}`). Returns
	// markdown describing the struct's field type.
	StructFieldType func(structName, fieldName string) (markdown string, ok bool)
}

// HoverAt returns hover markdown for the cursor position. Tries literal
// hover first (for color/measurement literals where the cursor isn't on
// an identifier word), then falls back to word-based identifier hover.
func HoverAt(content string, doc *ast.Document, line, col int, opts HoverOptions) string {
	if doc != nil {
		if info := hoverLiteralAt(doc, line, col); info != "" {
			return info
		}
		if info := hoverPropAt(doc, line, col, opts); info != "" {
			return info
		}
		if info := hoverStructFieldAt(doc, line, col); info != "" {
			return info
		}
		if info := hoverStructLitFieldAt(doc, line, col, opts); info != "" {
			return info
		}
	}
	return hoverWord(content, doc, line, col, opts)
}

// hoverStructFieldAt returns hover markdown when the cursor is on a
// field name inside a struct declaration. Format: `Struct.field: Type`.
func hoverStructFieldAt(doc *ast.Document, line, col int) string {
	if doc == nil {
		return ""
	}
	var found string
	walkStructDefs(doc, func(sd *ast.StructDef) {
		if found != "" {
			return
		}
		for _, f := range sd.Fields {
			for i, n := range f.Names {
				var pos ast.Pos
				if i < len(f.NamePositions) {
					pos = f.NamePositions[i]
				}
				if !pos.IsSet() {
					continue
				}
				if pos.Line != line || col < pos.Column || col >= pos.Column+len(n) {
					continue
				}
				t := typeExprString(f.Type)
				if t == "" {
					t = "dyn"
				}
				owner := sd.Name
				if owner == "" {
					owner = "struct"
				}
				found = "```sngl\n" + owner + "." + n + ": " + t + "\n```\n"
				return
			}
		}
	})
	return found
}

// hoverStructLitFieldAt returns hover markdown when the cursor is on
// a key in a struct literal (e.g. r in color{r=255}). Resolves to the
// declared struct's field type via the ComponentProp/Struct lookup.
func hoverStructLitFieldAt(doc *ast.Document, line, col int, opts HoverOptions) string {
	if doc == nil || opts.StructFieldType == nil {
		return ""
	}
	var found string
	walkStructExprs(doc, func(se *ast.StructExpr) {
		if found != "" {
			return
		}
		for _, f := range se.Fields {
			if f.Spread || f.Name == "" || !f.NamePos.IsSet() {
				continue
			}
			if f.NamePos.Line != line || col < f.NamePos.Column || col >= f.NamePos.Column+len(f.Name) {
				continue
			}
			if md, ok := opts.StructFieldType(se.Name, f.Name); ok {
				found = md
				return
			}
		}
	})
	return found
}

// walkStructDefs invokes fn for every StructDef in the document.
func walkStructDefs(doc *ast.Document, fn func(*ast.StructDef)) {
	if doc == nil {
		return
	}
	var walk func(s ast.Stmt)
	walk = func(s ast.Stmt) {
		switch x := s.(type) {
		case *ast.StructDef:
			fn(x)
		case *ast.ComponentDecl:
			for _, c := range x.Body.Stmts {
				walk(c)
			}
		}
	}
	for _, s := range doc.Stmts {
		walk(s)
	}
}

// walkStructExprs invokes fn for every StructExpr in the document.
func walkStructExprs(doc *ast.Document, fn func(*ast.StructExpr)) {
	if doc == nil {
		return
	}
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.StructExpr:
			fn(x)
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.CallExpr:
			walkE(x.Func)
			for _, a := range x.Args.Args {
				if arg, ok := a.(ast.Arg); ok {
					walkE(arg.Value)
				}
			}
		case *ast.SelectExpr:
			walkE(x.Operand)
		case *ast.IndexExpr:
			walkE(x.Operand)
			walkE(x.Index)
		case *ast.TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *ast.ListExpr:
			for _, el := range x.Elements {
				walkE(el)
			}
		case *ast.LambdaExpr:
			walkE(x.Body)
			for _, st := range x.Block.Stmts {
				walkS(st)
			}
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkE(p)
			}
		}
	}
	walkS = func(s ast.Stmt) {
		switch x := s.(type) {
		case nil:
			return
		case *ast.VarDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.ConstDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.AssignStmt:
			walkE(x.Value)
		case *ast.IfStmt:
			walkE(x.Cond)
			for _, c := range x.Body.Stmts {
				walkS(c)
			}
			for _, c := range x.Else.Stmts {
				walkS(c)
			}
		case *ast.ForStmt:
			walkE(x.Iter)
			for _, c := range x.Body.Stmts {
				walkS(c)
			}
		case *ast.VisualNode:
			for _, a := range x.Args.Args {
				if arg, ok := a.(ast.Arg); ok {
					walkE(arg.Value)
				}
			}
			for _, c := range x.Block.Stmts {
				walkS(c)
			}
		case *ast.ComponentDecl:
			for _, c := range x.Body.Stmts {
				walkS(c)
			}
		case *ast.FuncDef:
			walkE(x.Body)
			for _, c := range x.Block.Stmts {
				walkS(c)
			}
		}
	}
	for _, s := range doc.Stmts {
		walkS(s)
	}
}

// hoverPropAt returns the markdown when the cursor is on a named-arg
// key inside a visual node. Returns "" if the position isn't on a prop
// or the callback declines.
func hoverPropAt(doc *ast.Document, line, col int, opts HoverOptions) string {
	if opts.ComponentProp == nil {
		return ""
	}
	var foundComp, foundProp string
	visitArgListSites(doc, func(compName string, args ast.ArgList) {
		if foundProp != "" || compName == "" {
			return
		}
		for _, ah := range args.Args {
			arg, ok := ah.(ast.Arg)
			if !ok || arg.Name == "" || !arg.NamePos.IsSet() {
				continue
			}
			if arg.NamePos.Line == line && col >= arg.NamePos.Column && col < arg.NamePos.Column+len(arg.Name) {
				foundComp = compName
				foundProp = arg.Name
				return
			}
		}
	})
	if foundProp == "" {
		return ""
	}
	if md, ok := opts.ComponentProp(foundComp, foundProp); ok {
		return md
	}
	return ""
}

// visitArgListSites invokes fn for every (component-name, ArgList) pair
// in the document. Covers both VisualNodes (vbox { ... }) and CallStmts
// (text(value="x") without a body), since both shapes carry named args.
func visitArgListSites(doc *ast.Document, fn func(compName string, args ast.ArgList)) {
	if doc == nil {
		return
	}
	var walkStmt func(s ast.Stmt)
	walkStmt = func(s ast.Stmt) {
		switch x := s.(type) {
		case *ast.VisualNode:
			fn(visualNodeName(x), x.Args)
			for _, c := range x.Block.Stmts {
				walkStmt(c)
			}
		case *ast.CallStmt:
			if x.Call != nil {
				if id, ok := x.Call.Func.(*ast.IdentExpr); ok {
					fn(id.Name, x.Call.Args)
				}
			}
		case *ast.ComponentDecl:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		case *ast.FuncDef:
			for _, c := range x.Block.Stmts {
				walkStmt(c)
			}
		case *ast.IfStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
			for _, c := range x.Else.Stmts {
				walkStmt(c)
			}
		case *ast.ForStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		case *ast.PlatformStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		}
	}
	for _, s := range doc.Stmts {
		walkStmt(s)
	}
}

func visualNodeName(vn *ast.VisualNode) string {
	if vn == nil || vn.Target == nil {
		return ""
	}
	if id, ok := vn.Target.(*ast.IdentExpr); ok {
		return id.Name
	}
	return ""
}

// walkVisualNodes invokes fn for every VisualNode in the document, recursively.
func walkVisualNodes(doc *ast.Document, fn func(*ast.VisualNode)) {
	if doc == nil {
		return
	}
	var walkStmt func(s ast.Stmt)
	walkStmt = func(s ast.Stmt) {
		switch x := s.(type) {
		case *ast.VisualNode:
			fn(x)
			for _, child := range x.Block.Stmts {
				walkStmt(child)
			}
		case *ast.ComponentDecl:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		case *ast.FuncDef:
			for _, c := range x.Block.Stmts {
				walkStmt(c)
			}
		case *ast.IfStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
			for _, c := range x.Else.Stmts {
				walkStmt(c)
			}
		case *ast.ForStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		case *ast.PlatformStmt:
			for _, c := range x.Body.Stmts {
				walkStmt(c)
			}
		}
	}
	for _, s := range doc.Stmts {
		walkStmt(s)
	}
}

// hoverWord is the position→identifier→markdown path used by HoverAt.
// Hover() (no options) remains as a back-compat shim for callers that
// don't care about per-component image embedding.
func hoverWord(content string, doc *ast.Document, line, col int, opts HoverOptions) string {
	if doc == nil {
		return ""
	}
	word := WordAtPosition(content, line, col)
	if word == "" {
		return ""
	}
	return hoverInfoWithOptions(doc, word, opts)
}

func hoverLiteralAt(doc *ast.Document, line, col int) string {
	var found string
	WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if found != "" {
			return
		}
		if lit.Pos.Line != line {
			return
		}
		startCol := lit.Pos.Column
		endCol := startCol + len(lit.Raw)
		if col < startCol || col >= endCol {
			return
		}
		switch lit.Kind {
		case ast.LiteralColor:
			found = formatColorHover(lit.Raw)
		}
	})
	return found
}

func formatColorHover(raw string) string {
	r, g, b, ok := parseHexRGB(raw)
	if !ok {
		return ""
	}
	return fmt.Sprintf("```sngl\n%s\n```\n\nrgb(%d, %d, %d)\n", raw, r, g, b)
}

// parseHexRGB extracts 0-255 RGB channels from a #rrggbb or #rrggbbaa literal.
// 3-digit forms aren't valid SNGL color tokens (lexer rejects them).
func parseHexRGB(raw string) (r, g, b int, ok bool) {
	if len(raw) < 7 || raw[0] != '#' {
		return 0, 0, 0, false
	}
	hi, ok1 := hexByteHover(raw[1], raw[2])
	mi, ok2 := hexByteHover(raw[3], raw[4])
	lo, ok3 := hexByteHover(raw[5], raw[6])
	if !(ok1 && ok2 && ok3) {
		return 0, 0, 0, false
	}
	return hi, mi, lo, true
}

func hexByteHover(hi, lo byte) (int, bool) {
	h, ok1 := hexNibbleHover(hi)
	l, ok2 := hexNibbleHover(lo)
	if !(ok1 && ok2) {
		return 0, false
	}
	return h*16 + l, true
}

func hexNibbleHover(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}
