package lsp

import (
	"math"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		in                         string
		wantR, wantG, wantB, wantA float64
		wantOK                     bool
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

func TestColorPresentations(t *testing.T) {
	tests := []struct {
		name string
		c    Color
		want string
	}{
		{"opaque red", Color{1, 0, 0, 1}, "#ff0000"},
		{"semi red", Color{1, 0, 0, 0.5}, "#ff000080"},
		{"black", Color{0, 0, 0, 1}, "#000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeColorPresentations(tt.c)
			if len(got) != 1 {
				t.Fatalf("got %d presentations, want 1", len(got))
			}
			if got[0].Label != tt.want {
				t.Errorf("label = %q, want %q", got[0].Label, tt.want)
			}
		})
	}
}

func TestColorPresentations_HonorsOriginal(t *testing.T) {
	tests := []struct {
		name   string
		source string // source text at the range
		color  Color
		want   string
	}{
		{"hex opaque", "#ff0000", Color{1, 0, 0, 1}, "#ff0000"},
		{"hex with alpha", "#ff0000", Color{1, 0, 0, 0.5}, "#ff000080"},
		{"rgb call opaque", "color.rgb(255, 0, 0)", Color{0, 1, 0, 1}, "color.rgb(0, 255, 0)"},
		{"rgb call gains alpha", "color.rgb(255, 0, 0)", Color{1, 0, 0, 0.5}, "color.rgba(255, 0, 0, 128)"},
		{"rgba call retains form", "color.rgba(10, 20, 30, 200)", Color{1, 1, 1, 1}, "color.rgba(255, 255, 255, 255)"},
		{"rgba call alpha=1", "color.rgba(10, 20, 30, 200)", Color{0, 0, 0, 1}, "color.rgba(0, 0, 0, 255)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeColorPresentationsForSource(tt.source, tt.color)
			if len(got) != 1 {
				t.Fatalf("got %d presentations, want 1", len(got))
			}
			if got[0].Label != tt.want {
				t.Errorf("label = %q, want %q", got[0].Label, tt.want)
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
		{Range: rangeOfSubstring(t, string(src), "#ff0000"), Color: Color{Red: 1, Green: 0, Blue: 0, Alpha: 1}},
		{Range: rangeOfSubstring(t, string(src), "#00ff00"), Color: Color{Red: 0, Green: 1, Blue: 0, Alpha: 1}},
		{Range: rangeOfSubstring(t, string(src), "#aabbcc"), Color: Color{Red: 0xaa / 255.0, Green: 0xbb / 255.0, Blue: 0xcc / 255.0, Alpha: 1}},
		{Range: rangeOfSubstring(t, string(src), "#11223344"), Color: Color{Red: 0x11 / 255.0, Green: 0x22 / 255.0, Blue: 0x33 / 255.0, Alpha: 0x44 / 255.0}},
		{Range: rangeOfSubstring(t, string(src), "color.rgb(255, 128, 64)"), Color: Color{Red: 1, Green: 128.0 / 255.0, Blue: 64.0 / 255.0, Alpha: 1}},
		{Range: rangeOfSubstring(t, string(src), "color.rgba(10, 20, 30, 200)"), Color: Color{Red: 10.0 / 255.0, Green: 20.0 / 255.0, Blue: 30.0 / 255.0, Alpha: 200.0 / 255.0}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d colors, want %d:\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
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

func TestDocumentColor_Layer2ParityWithLayer1(t *testing.T) {
	// Until issue #76 (consteval color/unit/enum support) lands, Layer 2
	// can only rediscover the same source-form color literals that
	// Layer 1 already finds. The merged result must still be exactly
	// the same 6 entries — no duplicates, same order.
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parser.Parse("colors.sngl", src)
	got := computeDocumentColors(string(src), doc)

	wantRanges := []Range{
		rangeOfSubstring(t, string(src), "#ff0000"),
		rangeOfSubstring(t, string(src), "#00ff00"),
		rangeOfSubstring(t, string(src), "#aabbcc"),
		rangeOfSubstring(t, string(src), "#11223344"),
		rangeOfSubstring(t, string(src), "color.rgb(255, 128, 64)"),
		rangeOfSubstring(t, string(src), "color.rgba(10, 20, 30, 200)"),
	}
	if len(got) != len(wantRanges) {
		t.Fatalf("got %d colors, want %d", len(got), len(wantRanges))
	}
	for i, r := range wantRanges {
		if got[i].Range != r {
			t.Errorf("color %d range: got %+v, want %+v", i, got[i].Range, r)
		}
	}
}

// rangeOfSubstring returns the LSP Range for the first occurrence of needle in src.
func rangeOfSubstring(t *testing.T, src, needle string) Range {
	t.Helper()
	idx := strings.Index(src, needle)
	if idx < 0 {
		t.Fatalf("substring %q not found in source", needle)
	}
	line := 0
	col := 0
	for i := range idx {
		if src[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return Range{
		Start: Position{Line: line, Character: col},
		End:   Position{Line: line, Character: col + len(needle)},
	}
}
