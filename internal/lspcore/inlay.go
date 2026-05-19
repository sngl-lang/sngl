package lspcore

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// InlayHintResult is a single inlay-hint placement: position to render at,
// label string, and right-padding hint for the editor.
type InlayHintResult struct {
	Position    Position
	Label       string
	PaddingLeft bool
}

// Viewport assumptions for resolving relative units. Documented in
// docs/superpowers/specs/2026-05-18-lsp-previews-design.md §F3.
const (
	rootFontPx     = 16.0
	viewportWidth  = 1280.0
	viewportHeight = 800.0
)

// ComputeInlayHints returns inlay hints for measurement literals inside the
// given range. Hints are emitted only for units whose resolved pixel value
// is meaningful without parent context: em, rem, vw, vh. px and pct are
// skipped.
func ComputeInlayHints(content string, doc *ast.Document, rng Range) []InlayHintResult {
	var out []InlayHintResult
	if doc == nil {
		return out
	}
	WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if lit.Kind != ast.LiteralUnit {
			return
		}
		n, suffix, ok := parseUnitLiteral(lit.Raw)
		if !ok {
			return
		}
		px, ok := resolvePx(n, suffix)
		if !ok {
			return
		}
		startLine := lit.Pos.Line - 1
		startCol := lit.Pos.Column - 1
		endCol := startCol + len(lit.Raw)
		anchor := Position{Line: startLine, Character: endCol}
		if !rangeContains(rng, anchor) {
			return
		}
		out = append(out, InlayHintResult{
			Position:    anchor,
			Label:       fmt.Sprintf("(%dpx)", px),
			PaddingLeft: true,
		})
	})
	return out
}

// parseUnitLiteral splits "12em" → (12, "em"), "1.5rem" → (1.5, "rem").
// Returns ok=false if Raw doesn't fit the <number><suffix> shape.
func parseUnitLiteral(raw string) (n float64, suffix string, ok bool) {
	i := 0
	for i < len(raw) {
		c := raw[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' {
			i++
			continue
		}
		break
	}
	if i == 0 || i == len(raw) {
		return 0, "", false
	}
	num, err := strconv.ParseFloat(raw[:i], 64)
	if err != nil {
		return 0, "", false
	}
	return num, strings.ToLower(raw[i:]), true
}

// resolvePx returns the resolved pixel value for the given (n, unit) under
// the documented viewport assumptions. ok=false for units that don't get a
// hint (px, pct, anything non-measurement).
func resolvePx(n float64, suffix string) (int, bool) {
	var px float64
	switch suffix {
	case "em", "rem":
		px = n * rootFontPx
	case "vw":
		px = n / 100.0 * viewportWidth
	case "vh":
		px = n / 100.0 * viewportHeight
	default:
		return 0, false
	}
	return int(px + 0.5), true
}

// rangeContains reports whether p falls within [rng.Start, rng.End).
func rangeContains(rng Range, p Position) bool {
	if p.Line < rng.Start.Line || p.Line > rng.End.Line {
		return false
	}
	if p.Line == rng.Start.Line && p.Character < rng.Start.Character {
		return false
	}
	if p.Line == rng.End.Line && p.Character >= rng.End.Character {
		return false
	}
	return true
}
