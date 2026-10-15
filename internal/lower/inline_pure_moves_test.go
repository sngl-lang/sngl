package lower

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func TestInlinePureMovesContentInsertedOnce(t *testing.T) {
	rest := func() *ir.SlotInst { return &ir.SlotInst{Name: "content", Rest: true} }
	for _, tc := range []struct {
		name  string
		body  []ir.Stmt
		moved bool
	}{
		{"once", []ir.Stmt{rest()}, true},
		{"in both branches", []ir.Stmt{&ir.If{Body: []ir.Stmt{rest()}, Else: []ir.Stmt{rest()}}}, false},
		// A fallback is spliced once per use, so what it inserts may be too.
		{"and in a fallback", []ir.Stmt{rest(), &ir.SlotInst{Name: "header", Children: []ir.Stmt{rest()}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := &ir.Component{Name: "card", Stmts: []ir.Stmt{&ir.NodeInst{Name: "div", Children: tc.body}}}
			child := &ir.NodeInst{Name: "text"}
			main := &ir.Component{Name: "main", Stmts: []ir.Stmt{
				&ir.NodeInst{Name: "card", Component: card, Children: []ir.Stmt{child}},
			}}
			if err := lowerInlinePure(&ir.Package{Components: []*ir.Component{card, main}}, Features{}, Options{}); err != nil {
				t.Fatal(err)
			}
			var placed []*ir.NodeInst
			ir.WalkView(main.Stmts, func(s ir.Stmt) bool {
				if n, ok := s.(*ir.NodeInst); ok && n.Name == "text" {
					placed = append(placed, n)
				}
				return true
			})
			if len(placed) == 0 {
				t.Fatal("the content was not spliced")
			}
			for i, n := range placed {
				if moved := n == child; moved != (tc.moved && i == 0) {
					t.Errorf("insertion %d holds the call site's own node: %v", i, moved)
				}
			}
		})
	}
}

func TestInlinePureTakesTheBodyOfASoleInstance(t *testing.T) {
	text := func() *ir.NodeInst { return &ir.NodeInst{Name: "text"} }
	texts := func(stmts []ir.Stmt) (all []*ir.NodeInst, others int) {
		ir.WalkView(stmts, func(s ir.Stmt) bool {
			if n, ok := s.(*ir.NodeInst); ok {
				if n.Name == "text" {
					all = append(all, n)
				} else {
					others++
				}
			}
			return true
		})
		return all, others
	}
	t.Run("one call site", func(t *testing.T) {
		own := text()
		card := &ir.Component{Name: "card", Stmts: []ir.Stmt{own}}
		pkg := &ir.Package{Components: []*ir.Component{card}, Body: []ir.Stmt{&ir.NodeInst{Name: "card", Component: card}}}
		if err := lowerInlinePure(pkg, Features{}, Options{}); err != nil {
			t.Fatal(err)
		}
		if got, _ := texts(pkg.Body); len(got) != 1 || got[0] != own {
			t.Errorf("the call site holds %v, not the declaration's own node", got)
		}
	})
	t.Run("two call sites", func(t *testing.T) {
		own := text()
		card := &ir.Component{Name: "card", Stmts: []ir.Stmt{own}}
		call := func() ir.Stmt { return &ir.NodeInst{Name: "card", Component: card} }
		pkg := &ir.Package{Components: []*ir.Component{card}, Body: []ir.Stmt{call(), call()}}
		if err := lowerInlinePure(pkg, Features{}, Options{}); err != nil {
			t.Fatal(err)
		}
		got, _ := texts(pkg.Body)
		if len(got) != 2 || got[0] == own || got[1] == own {
			t.Errorf("two call sites hold %v; each wants a copy", got)
		}
	})
	// A wrapper copied twice before its own body is walked holds two calls of
	// a component its declaration names once.
	t.Run("in a body copied before it is walked", func(t *testing.T) {
		leaf := &ir.Component{Name: "leaf", Stmts: []ir.Stmt{text()}}
		wrap := &ir.Component{Name: "wrap", Stmts: []ir.Stmt{&ir.NodeInst{Name: "leaf", Component: leaf}}}
		callWrap := func() ir.Stmt { return &ir.NodeInst{Name: "wrap", Component: wrap} }
		page := &ir.Component{Name: "page", Stmts: []ir.Stmt{callWrap(), callWrap()}}
		pkg := &ir.Package{
			Components: []*ir.Component{page, wrap, leaf},
			Body:       []ir.Stmt{&ir.NodeInst{Name: "page", Component: page}},
		}
		if err := lowerInlinePure(pkg, Features{}, Options{}); err != nil {
			t.Fatal(err)
		}
		if got, others := texts(pkg.Body); len(got) != 2 || others != 0 {
			t.Errorf("the page renders %d texts and %d other nodes, want 2 and 0", len(got), others)
		}
	})
}
