package lspcore

import (
	"unicode/utf16"

	"duckfam.us/sngl/ast"
)

// AstPosToLSP converts 1-based ast.Pos to 0-based LSP Position.
func AstPosToLSP(p ast.Pos) Position {
	line := p.Line - 1
	col := p.Column - 1
	if line < 0 {
		line = 0
	}
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Character: col}
}

// AstPosToRange creates a Range covering a single character at the given position.
func AstPosToRange(p ast.Pos) Range {
	start := AstPosToLSP(p)
	end := Position{Line: start.Line, Character: start.Character + 1}
	return Range{Start: start, End: end}
}

// UTF16Column is runeCol, a 0-based rune offset into line, counted in the
// UTF-16 units an LSP position counts: the two differ past a character
// outside the Basic Multilingual Plane.
func UTF16Column(line string, runeCol int) int {
	n := 0
	for i, r := range []rune(line) {
		if i == runeCol {
			break
		}
		n += utf16.RuneLen(r)
	}
	return n
}

// RuneColumn is the inverse of UTF16Column.
func RuneColumn(line string, utf16Col int) int {
	n := 0
	for i, r := range []rune(line) {
		if n >= utf16Col {
			return i
		}
		n += utf16.RuneLen(r)
	}
	return len([]rune(line))
}
