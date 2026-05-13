package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

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
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
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
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
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

func TestSynthesizeRenderSlotFunc(t *testing.T) {
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
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Func
	for _, f := range comp.Funcs {
		if f.Name == "__renderSlot0" {
			found = f
			break
		}
	}
	if found == nil {
		t.Fatalf("expected __renderSlot0 Func on component; got: %v", funcSliceNames(comp.Funcs))
	}
	if len(found.Params) != 1 || found.Params[0].Name != "parent" {
		t.Errorf("expected __renderSlot0(parent dyn); got params %v", found.Params)
	}
	// Body should contain at least teardown For + reset Assign + If with body.
	if len(found.Block) < 3 {
		t.Errorf("expected at least 3 stmts in __renderSlot0 body, got %d", len(found.Block))
	}
}

func funcSliceNames(fs []*ir.Func) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func TestSynthesizeRenderSlotFunc_ForVariant(t *testing.T) {
	src := `
component main {
    var items list<int> = [1, 2, 3]
    for item = items {
        text(value=string(item))
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Func
	for _, f := range comp.Funcs {
		if f.Name == "__renderSlot0" {
			found = f
			break
		}
	}
	if found == nil {
		t.Fatalf("expected __renderSlot0 Func for reactive For; got: %v", funcSliceNames(comp.Funcs))
	}
}

func TestSynthesizeRenderSlotFunc_ElseBranch(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="on")
    } else {
        text(value="off")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Func
	for _, f := range comp.Funcs {
		if f.Name == "__renderSlot0" {
			found = f
			break
		}
	}
	if found == nil {
		t.Fatal("expected __renderSlot0 Func with else branch")
	}
	// Body shape: teardown For + reset Assign + If with non-empty Body and Else.
	if len(found.Block) < 3 {
		t.Fatalf("expected ≥3 stmts in __renderSlot0 body, got %d", len(found.Block))
	}
	ifStmt, ok := found.Block[2].(*ir.If)
	if !ok {
		t.Fatalf("expected stmt 3 to be *ir.If, got %T", found.Block[2])
	}
	if len(ifStmt.Body) == 0 || len(ifStmt.Else) == 0 {
		t.Errorf("expected both Body and Else populated; got Body=%d Else=%d", len(ifStmt.Body), len(ifStmt.Else))
	}
}

func TestSynthesizeRenderSlotFunc_PanicsOnNested(t *testing.T) {
	src := `
component main {
    var outer bool = true
    var inner bool = true
    if outer {
        if inner {
            text(value="x")
        }
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nested reactive If; none occurred")
		}
	}()
	_ = lowerReactivity(pkg, Caps{NoReactivity: true}, Options{})
}

func TestReactiveIfReplacedByCallStmt(t *testing.T) {
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
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if _, ok := s.(*ir.If); ok {
			t.Errorf("reactive If was not rewritten out of component body")
			return
		}
	}
	var found bool
	for _, s := range comp.Body {
		if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Name == "__renderSlot0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CallStmt __renderSlot0 in component body; got %d stmts", len(comp.Body))
	}
}

func TestReactiveForReplacedByCallStmt(t *testing.T) {
	src := `
component main {
    var items list<int> = [1, 2, 3]
    for item = items {
        text(value=string(item))
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if _, ok := s.(*ir.For); ok {
			t.Errorf("reactive For was not rewritten out of component body")
			return
		}
	}
}

func TestSlotUpdaterSplicedAfterMutation(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	// Walk the button's @click handler body and find a CallStmt __renderSlot0
	// spliced after the visible = !visible assignment.
	var found bool
	for _, s := range comp.Body {
		nodeInst, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, h := range nodeInst.Handlers {
			if h.Name != "click" || h.Func == nil {
				continue
			}
			for _, b := range h.Func.Block {
				if cs, ok := b.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Name == "__renderSlot0" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected CallStmt __renderSlot0 spliced after visible mutation")
	}
}

// A Var that affects ONLY a slot (no prop deps) still gets a slot
// updater spliced after its mutations. Regression guard: ensure the
// early-return-on-empty-reverseDeps path doesn't skip slot deps.
func TestSlotOnlyVarStillSplicesSlot(t *testing.T) {
	src := `
component main {
    var show bool = true
    button(@click { show = !show })
    if show {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found bool
	for _, s := range comp.Body {
		nodeInst, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, h := range nodeInst.Handlers {
			if h.Name != "click" || h.Func == nil {
				continue
			}
			for _, b := range h.Func.Block {
				if cs, ok := b.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Name == "__renderSlot0" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected __renderSlot0 splice for slot-only-dep var")
	}
}

func TestSlotParentRefMatchesEnclosingNode(t *testing.T) {
	src := `
component main {
    var visible bool = true
    vbox(style={gap=4}) {
        button(@click { visible = !visible })
        if visible {
            text(value="hi")
        }
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	// Find the initial slot call inside the vbox and the splice in @click.
	// Both should carry the same parent identifier.
	var initialParent, spliceParent string
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.CallStmt:
				if n.Call == nil || n.Call.Func == nil || n.Call.Func.Name != "__renderSlot0" {
					continue
				}
				if len(n.Call.Args) == 0 {
					continue
				}
				if id, ok := n.Call.Args[0].Value.(*ir.Ident); ok {
					if initialParent == "" {
						initialParent = id.Name
					} else if spliceParent == "" {
						spliceParent = id.Name
					}
				}
			case *ir.NodeInst:
				walk(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						walk(h.Func.Block)
					}
				}
			}
		}
	}
	walk(comp.Body)
	if initialParent == "" || spliceParent == "" {
		t.Fatalf("expected two __renderSlot0 call sites; found initial=%q splice=%q", initialParent, spliceParent)
	}
	if initialParent != spliceParent {
		t.Errorf("parent ref mismatch: initial=%q, splice=%q (splice should use the same parent as initial)", initialParent, spliceParent)
	}
	if initialParent == "__root" {
		t.Errorf("expected non-root parent for slot nested inside a vbox; got __root")
	}
}

func TestNonReactiveIfPreserved(t *testing.T) {
	src := `
component main {
    if true {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found bool
	for _, s := range comp.Body {
		if _, ok := s.(*ir.If); ok {
			found = true
		}
	}
	if !found {
		t.Errorf("expected non-reactive If to be preserved in component body")
	}
}

func TestSlotIdentsAreSynthesized(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(@click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var checked bool
	for _, s := range comp.Body {
		cs, ok := s.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil || cs.Call.Func.Name != "__renderSlot0" {
			continue
		}
		if len(cs.Call.Args) != 1 {
			t.Fatalf("expected 1 arg on __renderSlot0 call, got %d", len(cs.Call.Args))
		}
		id, ok := cs.Call.Args[0].Value.(*ir.Ident)
		if !ok {
			t.Fatalf("expected *ir.Ident arg, got %T", cs.Call.Args[0].Value)
		}
		if !id.Synthesized {
			t.Errorf("expected __root Ident to have Synthesized=true; got false")
		}
		checked = true
	}
	if !checked {
		t.Fatal("did not find __renderSlot0 CallStmt at top level")
	}
}

func TestRootVarSynthesizedWhenSlotExists(t *testing.T) {
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
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Var
	for _, v := range comp.Vars {
		if v.Name == "__root" {
			found = v
			break
		}
	}
	if found == nil {
		t.Fatal("__root Var not synthesized")
	}
	if !found.Synthesized {
		t.Error("__root Var not marked Synthesized")
	}
}

func TestRootVarOmittedWhenNoSlots(t *testing.T) {
	src := `
component main {
    var count int = 0
    button(text=string(count), @click { count = count + 1 })
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, v := range comp.Vars {
		if v.Name == "__root" {
			t.Error("__root unexpectedly synthesized for component with no reactive slots")
		}
	}
}
