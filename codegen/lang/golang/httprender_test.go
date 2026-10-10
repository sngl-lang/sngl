package golang

import (
	"bytes"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// TestRenderBodyEscapesHoles pins fix #2: server-rendered text/attr holes must
// be HTML-escaped (the client DOM path escapes via textContent/setAttribute, so
// the server path must match to avoid injecting raw markup from state). A
// string-typed hole value is emitted wrapped in html.EscapeString(...).
func TestRenderBodyEscapesHoles(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	gc := NewIRContext(ctx)

	strExpr := &ir.Ident{Name: "msg", Type: ir.TypString}
	rr := &codegen.RouteRender{
		Chunks: []string{"<span>", `" class="`, "</span>"},
		Holes: []codegen.RouteHole{
			{Kind: codegen.HoleText, Expr: strExpr},
			{Kind: codegen.HoleAttr, Expr: strExpr, Attr: "class"},
		},
	}

	var b bytes.Buffer
	writeRenderBody(&b, "\t", "__b", rr, gc)
	out := b.String()

	if !strings.Contains(out, "html.EscapeString(") {
		t.Fatalf("text/attr holes must be HTML-escaped via html.EscapeString:\n%s", out)
	}
	// Both the text and the attr hole must be escaped (two wraps).
	if n := strings.Count(out, "html.EscapeString("); n != 2 {
		t.Fatalf("expected 2 html.EscapeString wraps (text + attr), got %d:\n%s", n, out)
	}
	// The "html" import must be required so the emitted code compiles.
	imports := gc.Imports()
	found := false
	for _, imp := range imports {
		if imp == "html" {
			found = true
		}
	}
	if !found {
		t.Fatalf(`escaping must RequireImport("html"); imports=%v`, imports)
	}
}
