package lsp

import (
	"fmt"
	"strings"
	"testing"
)

func outline(syms []DocumentSymbol, depth int, b *strings.Builder) {
	for _, s := range syms {
		r, sel := s.Range, s.SelectionRange
		fmt.Fprintf(b, "%s%s %d %d:%d-%d:%d @%d:%d", strings.Repeat("  ", depth), s.Name, s.Kind,
			r.Start.Line+1, r.Start.Character+1, r.End.Line+1, r.End.Character+1, sel.Start.Line+1, sel.Start.Character+1)
		if s.Detail != "" {
			fmt.Fprintf(b, " %s", s.Detail)
		}
		b.WriteString("\n")
		outline(s.Children, depth+1, b)
	}
}

func TestDocumentSymbols(t *testing.T) {
	src := `import ui "sngl:ui"

const (
    limit = 3
    name = "n"
)

struct Point {
    x, y int

    func sum() => x + y
}

func Point.twice() => sum() * 2

enum Shade {
    light
    dark
}

#[ui.preview]
component Counter(label string, @bumped(n int)) ui.node {
    var count = 0
    func bump() {
        count += 1
    }

    ui.button(text=label, @click { bump() })
}

ui.window #main(title="t") {
    Counter(label="a")
}
`
	s := newSiblingSession(t, map[string]string{"main.sngl": src})
	s.open("main.sngl", src)
	s.diagnostics("main.sngl")
	var syms []DocumentSymbol
	s.call("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": s.uri("main.sngl")}}, &syms)
	var b strings.Builder
	outline(syms, 0, &b)
	want := `limit 14 4:5-4:14 @4:5 int
name 14 5:5-5:15 @5:5 string
Point 23 8:1-12:2 @8:8
  x 8 9:5-9:6 @9:5 int
  y 8 9:8-9:13 @9:8 int
  sum 6 11:5-11:24 @11:10
twice 6 14:1-14:32 @14:12
Shade 10 16:1-19:2 @16:6
  light 22 17:5-17:10 @17:5
  dark 22 18:5-18:9 @18:5
Counter 5 21:1-29:2 @22:11
  label 7 22:19-22:31 @22:19 string
  bumped 24 22:34-22:47 @22:34
  count 13 23:9-23:18 @23:9 int
  bump 6 24:5-26:6 @24:10
ui.window #main 19 31:1-33:2 @31:1
`
	if b.String() != want {
		t.Errorf("outline:\n%s\nwant:\n%s", b.String(), want)
	}
}
