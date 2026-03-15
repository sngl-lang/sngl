package lsp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestAstPosToLSP(t *testing.T) {
	tests := []struct {
		name string
		pos  ast.Pos
		want Position
	}{
		{"1:1 to 0:0", ast.Pos{Line: 1, Column: 1}, Position{Line: 0, Character: 0}},
		{"5:10 to 4:9", ast.Pos{Line: 5, Column: 10}, Position{Line: 4, Character: 9}},
		{"zero pos", ast.Pos{}, Position{Line: 0, Character: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := astPosToLSP(tt.pos)
			if got != tt.want {
				t.Errorf("astPosToLSP(%v) = %v, want %v", tt.pos, got, tt.want)
			}
		})
	}
}

func TestAstPosToRange(t *testing.T) {
	r := astPosToRange(ast.Pos{Line: 3, Column: 7})
	if r.Start.Line != 2 || r.Start.Character != 6 {
		t.Errorf("start = %v, want {2,6}", r.Start)
	}
	if r.End.Line != 2 || r.End.Character != 7 {
		t.Errorf("end = %v, want {2,7}", r.End)
	}
}
