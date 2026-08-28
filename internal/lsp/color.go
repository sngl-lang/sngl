package lsp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// parseHexColor converts "#rgb", "#rrggbb", or "#rrggbbaa" to a Color.
// Alpha defaults to 1.0 when not specified.
func parseHexColor(s string) (Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return Color{}, false
	}
	hex := s[1:]
	switch len(hex) {
	case 3:
		r, ok1 := hexNibble(hex[0])
		g, ok2 := hexNibble(hex[1])
		b, ok3 := hexNibble(hex[2])
		if !(ok1 && ok2 && ok3) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r*16+r) / 255.0,
			Green: float64(g*16+g) / 255.0,
			Blue:  float64(b*16+b) / 255.0,
			Alpha: 1.0,
		}, true
	case 4:
		r, ok1 := hexNibble(hex[0])
		g, ok2 := hexNibble(hex[1])
		b, ok3 := hexNibble(hex[2])
		a, ok4 := hexNibble(hex[3])
		if !(ok1 && ok2 && ok3 && ok4) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r*16+r) / 255.0,
			Green: float64(g*16+g) / 255.0,
			Blue:  float64(b*16+b) / 255.0,
			Alpha: float64(a*16+a) / 255.0,
		}, true
	case 6:
		r, ok1 := hexByte(hex[0], hex[1])
		g, ok2 := hexByte(hex[2], hex[3])
		b, ok3 := hexByte(hex[4], hex[5])
		if !(ok1 && ok2 && ok3) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r) / 255.0,
			Green: float64(g) / 255.0,
			Blue:  float64(b) / 255.0,
			Alpha: 1.0,
		}, true
	case 8:
		r, ok1 := hexByte(hex[0], hex[1])
		g, ok2 := hexByte(hex[2], hex[3])
		b, ok3 := hexByte(hex[4], hex[5])
		a, ok4 := hexByte(hex[6], hex[7])
		if !(ok1 && ok2 && ok3 && ok4) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r) / 255.0,
			Green: float64(g) / 255.0,
			Blue:  float64(b) / 255.0,
			Alpha: float64(a) / 255.0,
		}, true
	default:
		return Color{}, false
	}
}

// formatHexColor renders a Color as #rrggbb (alpha=1) or #rrggbbaa.
func formatHexColor(c Color) string {
	r := clamp8(c.Red)
	g := clamp8(c.Green)
	b := clamp8(c.Blue)
	if c.Alpha >= 1.0 {
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	a := clamp8(c.Alpha)
	return fmt.Sprintf("#%02x%02x%02x%02x", r, g, b, a)
}

func clamp8(v float64) int {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return int(v*255.0 + 0.5)
}

func hexNibble(c byte) (int, bool) {
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

func hexByte(hi, lo byte) (int, bool) {
	h, ok1 := hexNibble(hi)
	l, ok2 := hexNibble(lo)
	if !(ok1 && ok2) {
		return 0, false
	}
	return h*16 + l, true
}

// computeDocumentColors walks the parsed document and returns a
// ColorInformation entry for every `#hex` color literal and every
// color.rgb(...) / color.rgba(...) call with all-int-literal args.
func computeDocumentColors(content string, doc *ast.Document) []ColorInformation {
	out := []ColorInformation{}
	if doc == nil {
		return out
	}
	lspcore.WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if lit.Kind != ast.LiteralColor {
			return
		}
		c, ok := parseHexColor(lit.Raw)
		if !ok {
			return
		}
		startLine := lit.Pos.Line - 1
		startCol := lit.Pos.Column - 1
		out = append(out, ColorInformation{
			Range: Range{
				Start: Position{Line: startLine, Character: startCol},
				End:   Position{Line: startLine, Character: startCol + len(lit.Raw)},
			},
			Color: c,
		})
	})

	walkColorCalls(doc, content, func(call *ast.CallExpr, c Color, r Range) {
		out = append(out, ColorInformation{Range: r, Color: c})
	})

	// Layer 2: type-aware. Dormant until issue #76 (consteval support for
	// color/unit/enum) lands, after which color.lighten, user-defined pure
	// helpers, etc. fold to *ir.Literal and surface here. Today this path
	// only rediscovers source-form hex literals, which dedupe against
	// Layer 1 above by source range.
	layer2 := computeColorsFromIR(doc, content)
	out = mergeColorInfoDedupe(out, layer2)

	sortColorInformation(out)
	return out
}

// computeColorsFromIR runs the type checker and optimizer over doc and
// walks the folded IR for color literals. Returns nothing if the document
// fails to check — Layer 1 still works in that case.
func computeColorsFromIR(doc *ast.Document, content string) []ColorInformation {
	if doc == nil {
		return nil
	}
	pkg, diags := sngl.Check(doc, ".")
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil
		}
	}
	if err := optimize.Optimize(pkg, &optimize.Config{}); err != nil {
		return nil
	}
	// Determine the source file we're providing colors for so we can
	// filter out StructLits whose AST position points at stdlib (the
	// optimizer can inline stdlib helpers like color.rgb, surfacing
	// their `color{...}` literal in the user's expression tree).
	docFile := ""
	for _, s := range doc.Stmts {
		if p := s.StmtPos(); p != nil && p.File != "" {
			docFile = p.File
			break
		}
	}
	var out []ColorInformation
	walkIRColorLiterals(pkg, func(sl *ir.StructLit) {
		if docFile != "" && sl.AST != nil && sl.AST.Pos.File != "" && sl.AST.Pos.File != docFile {
			return
		}
		c, ok := colorFromIRStructLit(sl)
		if !ok {
			return
		}
		r, ok := rangeForIRStructLit(sl, content)
		if !ok {
			return
		}
		out = append(out, ColorInformation{Range: r, Color: c})
	})
	return out
}

// colorFromIRStructLit reads r/g/b/a int field literals from a color
// StructLit and returns an LSP Color (channels normalized to 0..1).
func colorFromIRStructLit(sl *ir.StructLit) (Color, bool) {
	if sl.Def == nil || sl.Def.Name != "color" {
		return Color{}, false
	}
	var r, g, b, a int
	a = 255 // default alpha when not set
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeInt {
			return Color{}, false
		}
		n, err := strconv.Atoi(lit.Raw)
		if err != nil {
			return Color{}, false
		}
		switch f.Name {
		case "r":
			r = n
		case "g":
			g = n
		case "b":
			b = n
		case "a":
			a = n
		}
	}
	return Color{
		Red:   float64(r) / 255.0,
		Green: float64(g) / 255.0,
		Blue:  float64(b) / 255.0,
		Alpha: float64(a) / 255.0,
	}, true
}

// rangeForIRStructLit computes the LSP Range for a color StructLit using
// its source-side AST pointer. For hex-derived StructLits the AST is a
// synthesized *ast.StructExpr carrying only the original hex literal's Pos
// (no source-text length). Scan `content` forward from that position to
// find the end of the hex token so the range matches Layer 1 (#rrggbb=7,
// #rrggbbaa=9, #rgb=4). Falls back to 7 chars if the source doesn't begin
// with '#' (e.g. a real color{...} struct literal — those don't surface
// here today, but the fallback keeps us conservative).
func rangeForIRStructLit(sl *ir.StructLit, content string) (Range, bool) {
	if sl.AST == nil || !sl.AST.Pos.IsSet() {
		return Range{}, false
	}
	startLine := sl.AST.Pos.Line - 1
	startCol := sl.AST.Pos.Column - 1
	length := hexTokenLen(content, startLine, startCol)
	if length == 0 {
		length = 7
	}
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   Position{Line: startLine, Character: startCol + length},
	}, true
}

// hexTokenLen returns the length of a `#xxxx...` hex token that begins at
// (line, col) in `content` (both 0-based). Returns 0 if the position
// doesn't point at a '#'.
func hexTokenLen(content string, line, col int) int {
	lines := strings.Split(content, "\n")
	if line < 0 || line >= len(lines) {
		return 0
	}
	l := lines[line]
	if col < 0 || col >= len(l) || l[col] != '#' {
		return 0
	}
	n := 1
	for col+n < len(l) {
		ch := l[col+n]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			n++
			continue
		}
		break
	}
	return n
}

// mergeColorInfoDedupe appends entries from b to a, skipping any whose
// Range already exists in a. O(n*m) — fine for the small counts here.
func mergeColorInfoDedupe(a, b []ColorInformation) []ColorInformation {
	have := make(map[Range]bool, len(a))
	for _, c := range a {
		have[c.Range] = true
	}
	for _, c := range b {
		if have[c.Range] {
			continue
		}
		have[c.Range] = true
		a = append(a, c)
	}
	return a
}

// sortColorInformation orders entries by (line, column) so test expectations
// and editor displays are stable regardless of walk order.
func sortColorInformation(out []ColorInformation) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Range.Start.Line != out[j].Range.Start.Line {
			return out[i].Range.Start.Line < out[j].Range.Start.Line
		}
		return out[i].Range.Start.Character < out[j].Range.Start.Character
	})
}

// walkColorCalls invokes fn for every well-formed color.rgb(r,g,b) or
// color.rgba(r,g,b,a) call in the document. Args must be integer literals
// in 0..255 — anything else (variables, arithmetic, color.lighten, etc.)
// is silently skipped.
func walkColorCalls(doc *ast.Document, content string, fn func(*ast.CallExpr, Color, Range)) {
	walkCallExprs(doc, func(call *ast.CallExpr) {
		sel, ok := call.Func.(*ast.SelectExpr)
		if !ok {
			return
		}
		ident, ok := sel.Operand.(*ast.IdentExpr)
		if !ok || ident.Name != "color" {
			return
		}
		var wantArgs int
		switch sel.Field {
		case "rgb":
			wantArgs = 3
		case "rgba":
			wantArgs = 4
		default:
			return
		}
		if len(call.Args.Args) != wantArgs {
			return
		}
		ints := make([]int, 0, wantArgs)
		for _, a := range call.Args.Args {
			arg, ok := a.(ast.Arg)
			if !ok || arg.Name != "" {
				return
			}
			lit, ok := arg.Value.(*ast.LiteralExpr)
			if !ok || lit.Kind != ast.LiteralInt {
				return
			}
			n, err := strconv.Atoi(lit.Raw)
			if err != nil || n < 0 || n > 255 {
				return
			}
			ints = append(ints, n)
		}
		c := Color{
			Red:   float64(ints[0]) / 255.0,
			Green: float64(ints[1]) / 255.0,
			Blue:  float64(ints[2]) / 255.0,
			Alpha: 1.0,
		}
		if wantArgs == 4 {
			c.Alpha = float64(ints[3]) / 255.0
		}
		rng, ok := callRange(call, content)
		if !ok {
			return
		}
		fn(call, c, rng)
	})
}

// callRange computes the LSP Range covering a CallExpr from the start of its
// callee to the matching close-paren.
func callRange(call *ast.CallExpr, content string) (Range, bool) {
	startPos := calleeStart(call.Func)
	if startPos == nil {
		return Range{}, false
	}
	startLine := startPos.Line - 1
	startCol := startPos.Column - 1
	endLine, endCol, ok := scanCloseParen(content, startPos.Line, startPos.Column)
	if !ok {
		return Range{}, false
	}
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   Position{Line: endLine - 1, Character: endCol},
	}, true
}

// calleeStart returns the position of the leftmost identifier in a callee
// expression chain (e.g. for `color.rgb`, returns the position of `color`).
func calleeStart(e ast.Expr) *ast.Pos {
	switch x := e.(type) {
	case *ast.SelectExpr:
		if p := calleeStart(x.Operand); p != nil {
			return p
		}
		return &x.Pos
	case *ast.IdentExpr:
		return &x.Pos
	case nil:
		return nil
	default:
		return x.ExprPos()
	}
}

// scanCloseParen walks `content` starting at the given 1-based line/column
// (the start of a callee identifier), finds the first '(' that begins the
// arg list, then returns the 1-based line and 0-based char-after-')' of the
// matching close paren. Tracks nested parens; ignores string contents.
func scanCloseParen(content string, line, col int) (endLine, endChar int, ok bool) {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return 0, 0, false
	}
	depth := 0
	seenOpen := false
	inString := false
	var stringQuote byte
	curLine := line
	curIdx := col - 1
	for curLine <= len(lines) {
		l := lines[curLine-1]
		for curIdx < len(l) {
			ch := l[curIdx]
			if inString {
				if ch == '\\' && curIdx+1 < len(l) {
					curIdx += 2
					continue
				}
				if ch == stringQuote {
					inString = false
				}
				curIdx++
				continue
			}
			switch ch {
			case '"', '\'', '`':
				inString = true
				stringQuote = ch
			case '(':
				depth++
				seenOpen = true
			case ')':
				depth--
				if seenOpen && depth == 0 {
					return curLine, curIdx + 1, true
				}
			}
			curIdx++
		}
		curLine++
		curIdx = 0
	}
	return 0, 0, false
}

// walkCallExprs invokes fn for every CallExpr in the document.
// Mirrors lspcore.WalkLiterals's traversal but dispatches on CallExpr.
func walkCallExprs(doc *ast.Document, fn func(*ast.CallExpr)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	var walkBlock func(b ast.StmtBlock)
	var walkArgs func(args ast.ArgList)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.LiteralExpr:
			return
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.CallExpr:
			fn(x)
			walkE(x.Func)
			walkArgs(x.Args)
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
		case *ast.StructExpr:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *ast.LambdaExpr:
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkE(p)
			}
		case *ast.ParenExpr:
			walkE(x.Inner)
		case *ast.ConstExpr:
			walkE(x.Operand)
		case *ast.SpreadExpr:
			walkE(x.Operand)
		case *ast.MapLit:
			for _, en := range x.Entries {
				walkE(en.Key)
				walkE(en.Value)
			}
		}
	}
	walkArgs = func(args ast.ArgList) {
		for _, a := range args.Args {
			switch arg := a.(type) {
			case ast.Arg:
				walkE(arg.Value)
			case ast.EventHandler:
				walkBlock(arg.Body)
			}
		}
	}
	walkBlock = func(b ast.StmtBlock) {
		for _, c := range b.Stmts {
			walkS(c)
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
			walkBlock(x.Body)
			walkBlock(x.Else)
		case *ast.ForStmt:
			walkE(x.Iter)
			walkBlock(x.Body)
			walkBlock(x.Else)
		case *ast.VisualNode:
			walkArgs(x.Args)
			walkBlock(x.Block)
		case *ast.ComponentDecl:
			walkBlock(x.Body)
		case *ast.FuncDef:
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.PlatformStmt:
			walkBlock(x.Body)
		case *ast.ReturnStmt:
			walkE(x.Value)
		case *ast.CallStmt:
			if x.Call != nil {
				walkE(x.Call)
			}
		case *ast.VarStmt:
			walkE(x.Init)
		case *ast.DisabledDecl:
			walkS(x.Inner)
		}
	}
	for _, st := range doc.Stmts {
		walkS(st)
	}
}

func (s *Server) handleDocumentColor(id json.RawMessage, params json.RawMessage) {
	var p DocumentColorParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, []ColorInformation{})
		return
	}
	s.sendResult(id, computeDocumentColors(fs.Content, fs.Doc))
}

func computeColorPresentations(c Color) []ColorPresentation {
	return []ColorPresentation{{Label: formatHexColor(c)}}
}

// computeColorPresentationsForSource picks a hex-or-call rendering based on
// the source text that the editor highlighted. Falls back to hex if the
// source doesn't begin with "color.".
func computeColorPresentationsForSource(source string, c Color) []ColorPresentation {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "color.rgba(") {
		return []ColorPresentation{{Label: formatColorRgbaCall(c)}}
	}
	if strings.HasPrefix(source, "color.rgb(") {
		if c.Alpha < 1.0 {
			// Caller gained alpha — promote to rgba so we don't silently drop it.
			return []ColorPresentation{{Label: formatColorRgbaCall(c)}}
		}
		return []ColorPresentation{{Label: formatColorRgbCall(c)}}
	}
	return computeColorPresentations(c)
}

func formatColorRgbCall(c Color) string {
	return fmt.Sprintf("color.rgb(%d, %d, %d)", clamp8(c.Red), clamp8(c.Green), clamp8(c.Blue))
}

func formatColorRgbaCall(c Color) string {
	return fmt.Sprintf("color.rgba(%d, %d, %d, %d)", clamp8(c.Red), clamp8(c.Green), clamp8(c.Blue), clamp8(c.Alpha))
}

func (s *Server) handleColorPresentation(id json.RawMessage, params json.RawMessage) {
	var p ColorPresentationParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	source := ""
	if fs := s.ws.get(p.TextDocument.URI); fs != nil {
		source = sliceRange(fs.Content, p.Range)
	}
	s.sendResult(id, computeColorPresentationsForSource(source, p.Color))
}

// sliceRange extracts the substring of `content` covered by an LSP Range.
// Lines and characters are 0-based. Returns "" if the range is malformed.
func sliceRange(content string, r Range) string {
	lines := strings.Split(content, "\n")
	if r.Start.Line < 0 || r.Start.Line >= len(lines) {
		return ""
	}
	if r.End.Line < 0 || r.End.Line >= len(lines) {
		return ""
	}
	if r.Start.Line == r.End.Line {
		l := lines[r.Start.Line]
		if r.Start.Character < 0 || r.End.Character > len(l) || r.Start.Character > r.End.Character {
			return ""
		}
		return l[r.Start.Character:r.End.Character]
	}
	var sb strings.Builder
	sb.WriteString(lines[r.Start.Line][r.Start.Character:])
	for i := r.Start.Line + 1; i < r.End.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(lines[i])
	}
	sb.WriteByte('\n')
	end := lines[r.End.Line]
	if r.End.Character > len(end) {
		return ""
	}
	sb.WriteString(end[:r.End.Character])
	return sb.String()
}
