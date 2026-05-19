package lsp

import (
	"math"
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		in           string
		wantR, wantG, wantB, wantA float64
		wantOK       bool
	}{
		{"#000", 0, 0, 0, 1, true},
		{"#fff", 1, 1, 1, 1, true},
		{"#f00", 1, 0, 0, 1, true},
		{"#000000", 0, 0, 0, 1, true},
		{"#ffffff", 1, 1, 1, 1, true},
		{"#ff8040", 1, 128.0 / 255.0, 64.0 / 255.0, 1, true},
		{"#00000000", 0, 0, 0, 0, true},
		{"#ff000080", 1, 0, 0, 128.0 / 255.0, true},
		{"#abc", 0xaa / 255.0, 0xbb / 255.0, 0xcc / 255.0, 1, true},
		{"", 0, 0, 0, 0, false},
		{"#", 0, 0, 0, 0, false},
		{"#xyz", 0, 0, 0, 0, false},
		{"#fffff", 0, 0, 0, 0, false}, // 5 digits not valid
		{"#ffffffffff", 0, 0, 0, 0, false},
		{"abc", 0, 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c, ok := parseHexColor(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if !floatEq(c.Red, tt.wantR) || !floatEq(c.Green, tt.wantG) || !floatEq(c.Blue, tt.wantB) || !floatEq(c.Alpha, tt.wantA) {
				t.Fatalf("got {%v %v %v %v}, want {%v %v %v %v}", c.Red, c.Green, c.Blue, c.Alpha, tt.wantR, tt.wantG, tt.wantB, tt.wantA)
			}
		})
	}
}

func TestFormatHexColor(t *testing.T) {
	tests := []struct {
		c    Color
		want string
	}{
		{Color{1, 0, 0, 1}, "#ff0000"},
		{Color{0, 0, 0, 1}, "#000000"},
		{Color{1, 1, 1, 1}, "#ffffff"},
		{Color{1, 0, 0, 0.5}, "#ff000080"},
		{Color{1, 0, 0, 0}, "#ff000000"},
		{Color{128.0 / 255.0, 64.0 / 255.0, 32.0 / 255.0, 1}, "#804020"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatHexColor(tt.c)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestDocumentColor(t *testing.T) {
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parser.Parse("colors.sngl", src)
	got := computeDocumentColors(string(src), doc)

	want := []ColorInformation{
		{Range: Range{Start: Position{Line: 1, Character: 23}, End: Position{Line: 1, Character: 30}}, Color: Color{Red: 1, Green: 0, Blue: 0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 2, Character: 23}, End: Position{Line: 2, Character: 30}}, Color: Color{Red: 0, Green: 1, Blue: 0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 3, Character: 13}, End: Position{Line: 3, Character: 20}}, Color: Color{Red: 0xaa / 255.0, Green: 0xbb / 255.0, Blue: 0xcc / 255.0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 4, Character: 13}, End: Position{Line: 4, Character: 22}}, Color: Color{Red: 0x11 / 255.0, Green: 0x22 / 255.0, Blue: 0x33 / 255.0, Alpha: 0x44 / 255.0}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d colors, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Range != w.Range {
			t.Errorf("color %d range: got %+v, want %+v", i, g.Range, w.Range)
		}
		if !floatEq(g.Color.Red, w.Color.Red) || !floatEq(g.Color.Green, w.Color.Green) || !floatEq(g.Color.Blue, w.Color.Blue) || !floatEq(g.Color.Alpha, w.Color.Alpha) {
			t.Errorf("color %d value: got %+v, want %+v", i, g.Color, w.Color)
		}
	}
}
