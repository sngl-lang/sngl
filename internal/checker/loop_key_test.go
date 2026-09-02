package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A node in a loop carries its list-diffing key however it is written: as a
// visual node with a body, as a childless call, or as an element ref with an
// #id. The last was the form that dropped it.
func TestKeyOnEveryNodeForm(t *testing.T) {
	src := `import . "sngl:ui"

component main {
    var items list<int> = [1, 2]
    vbox() {
        for var item, i = items {
            button(text="b", key=string(i), @click { })
            text #row(value="r", key=string(i))
            vbox(key=string(i)) {
                text(value="t")
            }
        }
    }
}
`
	doc, err := parser.Parse("k.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	got := map[string]bool{}
	var walk func([]ir.Stmt)
	walk = func(ss []ir.Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ir.NodeInst:
				if n.Key != nil {
					got[n.Name] = true
				}
				walk(n.Children)
			case *ir.For:
				walk(n.Body)
			}
		}
	}
	for _, c := range pkg.Components {
		walk(c.Body)
	}
	for _, name := range []string{"button", "text", "vbox"} {
		if !got[name] {
			t.Errorf("%s: key= dropped", name)
		}
	}
}
