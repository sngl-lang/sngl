package lspcore

import "duckfam.us/sngl/ast"

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
