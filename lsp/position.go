package lsp

import "git.duckfam.us/jonathan/sngl/ast"

// LSP positions are 0-based; ast.Pos is 1-based.

func astPosToLSP(p ast.Pos) Position {
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

func astPosToRange(p ast.Pos) Range {
	start := astPosToLSP(p)
	end := Position{Line: start.Line, Character: start.Character + 1}
	return Range{Start: start, End: end}
}
