package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestCollectSlotForReactiveIf(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if ifNode, ok := s.(*ir.If); ok {
			if ifNode.LoweredSlotID == "" {
				t.Errorf("expected reactive If to have LoweredSlotID set")
			}
			return
		}
	}
	t.Errorf("no If found in lowered body")
}

func TestCollectSlotForReactiveFor(t *testing.T) {
	src := `
component main {
    var items list<int> = [1, 2, 3]
    for item = items {
        text(value=string(item))
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if forNode, ok := s.(*ir.For); ok {
			if forNode.LoweredSlotID == "" {
				t.Errorf("expected reactive For to have LoweredSlotID set")
			}
			return
		}
	}
	t.Errorf("no For found in lowered body")
}

func TestCollectSlotNotSetForNonReactiveIf(t *testing.T) {
	src := `
component main {
    if true {
        text(value="hi")
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if ifNode, ok := s.(*ir.If); ok {
			if ifNode.LoweredSlotID != "" {
				t.Errorf("expected non-reactive If to have empty LoweredSlotID; got %q", ifNode.LoweredSlotID)
			}
			return
		}
	}
}

func TestSynthesizeSlotVar(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Var
	for _, v := range comp.Vars {
		if v.Name == "__slot0" {
			found = v
			break
		}
	}
	if found == nil {
		t.Fatalf("expected synthesized __slot0 var on component; got vars: %v", varSliceNames(comp.Vars))
	}
	// Verify the type is list<dyn>.
	if found.Type == nil {
		t.Errorf("__slot0 has nil Type")
	}
}

func varSliceNames(vs []*ir.Var) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name
	}
	return out
}
