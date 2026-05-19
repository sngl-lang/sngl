package lsp

import (
	"encoding/json"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
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
// ColorInformation entry for every `#hex` color literal.
func computeDocumentColors(content string, doc *ast.Document) []ColorInformation {
	_ = content
	out := []ColorInformation{}
	if doc == nil {
		return out
	}
	walkLiterals(doc, func(lit *ast.LiteralExpr) {
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
	return out
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

// walkLiterals invokes fn for every LiteralExpr in the document.
// Minimal walker scoped to what documentColor needs.
func walkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	var walkBlock func(b ast.StmtBlock)
	var walkArgs func(args ast.ArgList)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.LiteralExpr:
			fn(x)
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.CallExpr:
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
