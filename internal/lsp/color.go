package lsp

import (
	"encoding/json"
	"fmt"

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
// ColorInformation entry for every `#hex` color literal.
func computeDocumentColors(content string, doc *ast.Document) []ColorInformation {
	_ = content
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
