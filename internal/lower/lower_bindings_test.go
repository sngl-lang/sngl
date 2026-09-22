package lower_test

import (
	"strings"
	"testing"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A two-way binding injects `event.value` into whichever handler reports the
// change. `event` names that handler's parameter, and a handler written
// without one — `@input { }` — has no parameter to name, so the pass declares
// it. It used to emit a reference to nothing, which only went unnoticed
// because Validate exempted any identifier called "event".
func TestPropBindings_EventNamesTheHandlersParam(t *testing.T) {
	src := `import . "sngl:ui"

component field(:value string) node {
    input(value=value, @input { })
}

component main node {
    var name string = ""
    field(:value=name)
}
`
	doc, err := sngl.Parse("t.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := sngl.Check(doc, ".")
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if err := lower.Lower(pkg, lower.Features{}, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	for _, e := range ir.Validate(pkg) {
		t.Errorf("post-lower: %v", e)
	}
}
