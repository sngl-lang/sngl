package optimize

import (
	"strings"
	"testing"
)

// Two arguments that print alike are two calls: keyed on %v, a struct's string
// field was written unquoted, so `{a = "1, b = 2"}` and `{a = "1", b = "2, b = "}`
// were one key and the second call was answered with the first one's result.
func TestInterpretedCallKeyedByValueNotPrintedForm(t *testing.T) {
	src := `
struct pair {
    a string
    b string
}

const func first(p pair) string {
    var s = p.a
    if p.b == "zz" {
        s = ""
    }
    return s
}

component main node {
    text(value=first({a="1, b = 2"}))
    text(value=first({a="1", b="2, b = "}))
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	for _, want := range []string{`value="1, b = 2"`, `value="1")`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}
