package lower

import (
	"slices"
	"testing"

	"duckfam.us/sngl/ir"
)

// TestBlocksReachSlotContent is the sixth walker's share of the same
// omission. A handler on a node inside slot content is an imperative block
// like any other: passForElse desugars a for-else in one and passCSE binds a
// call it makes twice, and neither could see it. allBlocks additionally owes
// the slot body itself, since passEffect puts a settle in a view body.
func TestBlocksReachSlotContent(t *testing.T) {
	handler := &ir.Func{Block: []ir.Stmt{&ir.Return{}}}
	slotBody := []ir.Stmt{&ir.NodeInst{
		Name:     "button",
		Handlers: []ir.EventHandler{{Name: "click", Func: handler}},
	}}
	sc := &ir.SlotContent{Body: slotBody}
	main := &ir.Component{Name: "main", Body: []ir.Stmt{&ir.NodeInst{
		Name:  "panel",
		Slots: map[string]*ir.SlotContent{"header": sc},
	}}}
	pkg := &ir.Package{Components: []*ir.Component{main}}

	has := func(blocks []*[]ir.Stmt, want *[]ir.Stmt) bool {
		return slices.Contains(blocks, want)
	}
	if !has(imperativeBlocks(pkg), &handler.Block) {
		t.Error("imperativeBlocks misses a handler on a node in slot content")
	}
	all := allBlocks(pkg)
	if !has(all, &handler.Block) {
		t.Error("allBlocks misses a handler on a node in slot content")
	}
	if !has(all, &sc.Body) {
		t.Error("allBlocks misses the slot content body itself")
	}
}
