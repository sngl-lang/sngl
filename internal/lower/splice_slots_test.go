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
