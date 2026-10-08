package checker

import (
	"testing"

	"duckfam.us/sngl/internal/parser"
)

// lspcore.Analyze checks a document even when the parse reported errors, so a
// partial parse's nil sub-expressions reach the checker in production. Each of
// these leaves an expression node with a nil operand, which is why exprType's
// nil arm may not become a panic.
func TestPartialParseDoesNotPanic(t *testing.T) {
	srcs := map[string]string{
		"spread_no_operand": "component main node {\n    var xs = [...]\n}\n",
		"spread_after_elem": "component main node {\n    var xs = [1, ...]\n}\n",
		"spread_empty_expr": "component main node {\n    var xs = [...()]\n}\n",
		"const_empty_expr":  "component main node {\n    var x = const(())\n}\n",
		"ternary_no_then":   "component main node {\n    var x = true ? : 2\n}\n",
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			doc, err := parser.Parse("partial.sngl", []byte(withStd(src)))
			if err == nil {
				t.Fatalf("want a parse error, got none")
			}
			if doc == nil {
				t.Skip("parser returned no document, so the checker never sees this shape")
			}
			Check(doc, &Config{IsMain: true}) // must not panic
		})
	}
}
