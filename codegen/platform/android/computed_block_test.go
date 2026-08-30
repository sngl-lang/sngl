package android

import (
	"strings"
	"testing"
)

// TestComputedBlockBodyEmitted guards that a computed whose body is more than
// one `return expr` keeps that body. It used to collapse to
// `derivedStateOf { "" }` — the statements were dropped and the type zero
// (always the string zero, whatever the computed's type) took their place.
//
// The emitted shape is `derivedStateOf(fun(): T { ... })`: an anonymous
// function, because `return` inside the non-inline `derivedStateOf` lambda is
// a non-local return and does not compile.
func TestComputedBlockBodyEmitted(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "local var",
			body: `func c() string { var d = n * 2; return "d={d}" }`,
			want: []string{"derivedStateOf(fun(): String {", "var d = (n * 2)", `return ("d=" + d.toString())`},
		},
		{
			name: "if with early return",
			body: `func c() string { if n > 2 { return "big" } return "small" }`,
			want: []string{"derivedStateOf(fun(): String {", "if ((n > 2))", `return "big"`, `return "small"`},
		},
		{
			name: "loop over a reactive list",
			body: `func c() int { var s = 0; for x = xs { s = s + x } return s }`,
			want: []string{"derivedStateOf(fun(): Int {", "var s = 0", "for (x in xs)", "s = (s + x)", "return s"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `import . "sngl:ui"
component main {
    var n = 3
    var xs = [1, 2, 3]
    ` + tc.body + `
    text(value="{c}")
}`
			out := compileCanvasSrc(t, src)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("generated Kotlin missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, `derivedStateOf { "" }`) {
				t.Errorf("computed body was discarded for the type zero:\n%s", out)
			}
		})
	}
}
