package lsp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
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

	sortColorInformation(out)
	return out
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
		case *ast.EmitStmt:
			walkArgs(x.Args)
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

func (s *Server) handleColorPresentation(id json.RawMessage, params json.RawMessage) {
	var p ColorPresentationParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	s.sendResult(id, computeColorPresentations(p.Color))
}
