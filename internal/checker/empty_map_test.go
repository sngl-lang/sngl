package checker

import (
	"testing"

	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// #4 — an empty `{}` initializer against a non-string-keyed map type is a
// valid empty map, not a shape mismatch.
func TestEmptyMapLiteralNonStringKey(t *testing.T) {
	for _, mt := range []string{"map<float, int>", "map<int, string>", "map<string, int>"} {
		src := "component main node {\n    var m " + mt + " = {}\n    text(value=\"{m.length()}\")\n}\n"
		doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
		if err != nil {
			t.Fatalf("%s: parse: %v", mt, err)
		}
		_, diags := Check(doc, &Config{IsMain: true})
		for _, d := range diags {
			if d.Severity == ir.Error {
				t.Errorf("%s: unexpected error: %s", mt, d.Msg)
			}
		}
	}
}
