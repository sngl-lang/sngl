package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passFocusOrder = pass{
	name:    "FocusOrder",
	enabled: func(c Caps) bool { return c.FocusOrder },
	apply:   lowerFocusOrder,
}

// focusSlot is one position in the tab-order sequence.
// It is either a single static focusable node or a for-loop
// whose body contains focusable nodes.
type focusSlot struct {
	isLoop  bool
	slotIdx int
	node    *ir.NodeInst // non-nil for static slot
	loop    *loopSlotInfo
}

type loopSlotInfo struct {
	forStmt   *ir.For
	cursorVar *ir.Var    // __focusLoopN_cursor
	keyVar    *ir.LoopVar // synthetic loop key (integer index)
}

func lowerFocusOrder(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	for _, comp := range pkg.Components {
		lowerFocusInOwner(comp.Body, &comp.Vars, &comp.Funcs)
	}
	for _, w := range pkg.Windows {
		lowerFocusInOwner(w.Body, &w.Vars, &w.Funcs)
	}
	return nil
}

func lowerFocusInOwner(stmts []ir.Stmt, vars *[]*ir.Var, funcs *[]*ir.Func) {
	slots := gatherFocusSlots(stmts)
	if len(slots) == 0 {
		return
	}

	// Ensure every loop slot has an integer key variable.
	for i := range slots {
		if slots[i].isLoop {
			ensureLoopKey(slots[i].loop, i)
		}
	}

	// __focusID tracks which slot is active.
	focusIDVar := &ir.Var{
		Name:        "__focusID",
		Type:        ir.TypInt,
		Init:        intLiteralLit(0),
		Synthesized: true,
	}
	*vars = append(*vars, focusIDVar)
	focusIDIdent := func() *ir.Ident {
		return &ir.Ident{Name: "__focusID", Type: ir.TypInt, Sym: focusIDVar, Synthesized: true}
	}

	// Per-loop cursor vars (__focusLoopN_cursor).
	for i := range slots {
		s := &slots[i]
		if !s.isLoop {
			continue
		}
		name := fmt.Sprintf("__focusLoop%d_cursor", s.slotIdx)
		cv := &ir.Var{Name: name, Type: ir.TypInt, Init: intLiteralLit(0), Synthesized: true}
		s.loop.cursorVar = cv
		*vars = append(*vars, cv)
	}

	// Inject __focused bool prop on every focusable node.
	injectFocusedProps(stmts, slots, focusIDIdent)

	// Synthesize __focusNext / __focusPrev.
	*funcs = append(*funcs,
		buildFocusNav("__focusNext", slots, focusIDIdent, true),
		buildFocusNav("__focusPrev", slots, focusIDIdent, false),
	)
}

// ensureLoopKey makes sure the for-loop has an integer key (index) variable
// that can be compared against the cursor. If Key is empty we inject a
// synthetic one so both the for-header and the __focused expression share it.
func ensureLoopKey(ls *loopSlotInfo, slotIdx int) {
	if ls.forStmt.Key == "" {
		synthetic := fmt.Sprintf("__focusIdx%d", slotIdx)
		ls.forStmt.Key = synthetic
		ls.keyVar = &ir.LoopVar{Name: synthetic, Type: ir.TypInt}
	} else {
		ls.keyVar = &ir.LoopVar{Name: ls.forStmt.Key, Type: ir.TypInt}
	}
}

// ---- slot gathering ----

func gatherFocusSlots(stmts []ir.Stmt) []focusSlot {
	var slots []focusSlot
	walkForSlots(stmts, &slots)
	return slots
}

func walkForSlots(stmts []ir.Stmt, slots *[]focusSlot) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				idx := len(*slots)
				*slots = append(*slots, focusSlot{slotIdx: idx, node: n})
			}
			walkForSlots(n.Children, slots)
		case *ir.For:
			if hasFocusableNodes(n.Body) {
				idx := len(*slots)
				*slots = append(*slots, focusSlot{
					isLoop:  true,
					slotIdx: idx,
					loop:    &loopSlotInfo{forStmt: n},
				})
				// Do not recurse further — the loop occupies one slot.
			}
		case *ir.If:
			walkForSlots(n.Body, slots)
			walkForSlots(n.Else, slots)
		case *ir.Window:
			walkForSlots(n.Body, slots)
		case *ir.PlatformFilter:
			walkForSlots(n.Body, slots)
		case *ir.SlotInst:
			walkForSlots(n.Children, slots)
		case *ir.ErrorBoundary:
			walkForSlots(n.Children, slots)
		case *ir.ContextProvider:
			walkForSlots(n.Children, slots)
		}
	}
}

// hasFocusableNodes reports whether stmts contain at least one focusable
// NodeInst (directly, or through If/PlatformFilter/children — but not inside
// nested For loops, which are their own slots).
func hasFocusableNodes(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				return true
			}
			if hasFocusableNodes(n.Children) {
				return true
			}
		case *ir.If:
			if hasFocusableNodes(n.Body) || hasFocusableNodes(n.Else) {
				return true
			}
		case *ir.PlatformFilter:
			if hasFocusableNodes(n.Body) {
				return true
			}
		case *ir.SlotInst:
			if hasFocusableNodes(n.Children) {
				return true
			}
		case *ir.ErrorBoundary:
			if hasFocusableNodes(n.Children) {
				return true
			}
		case *ir.ContextProvider:
			if hasFocusableNodes(n.Children) {
				return true
			}
		// *ir.For: intentionally not recursed — nested loops are their own slots.
		}
	}
	return false
}

// ---- prop injection ----

func injectFocusedProps(stmts []ir.Stmt, slots []focusSlot, focusIDIdent func() *ir.Ident) {
	// Build lookup maps.
	loopSlots := map[*ir.For]*focusSlot{}
	staticSlots := map[*ir.NodeInst]*focusSlot{}
	for i := range slots {
		s := &slots[i]
		if s.isLoop {
			loopSlots[s.loop.forStmt] = s
		} else {
			staticSlots[s.node] = s
		}
	}
	walkInjectFocused(stmts, focusIDIdent, loopSlots, staticSlots, nil)
}

func walkInjectFocused(
	stmts []ir.Stmt,
	focusIDIdent func() *ir.Ident,
	loopSlots map[*ir.For]*focusSlot,
	staticSlots map[*ir.NodeInst]*focusSlot,
	inLoop *focusSlot,
) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if slot, ok := staticSlots[n]; ok {
				// Static focusable: __focused = __focusID == slotIdx
				n.Props = append(n.Props, ir.Arg{
					Name:  "__focused",
					Value: focusEqExpr(focusIDIdent(), slot.slotIdx),
				})
			} else if inLoop != nil && nodeEffectiveFocusable(n) {
				// Loop-body focusable: __focused = __focusID == S && cursor == key
				ls := inLoop.loop
				cursorIdent := &ir.Ident{
					Name:        ls.cursorVar.Name,
					Type:        ir.TypInt,
					Sym:         ls.cursorVar,
					Synthesized: true,
				}
				keyIdent := &ir.Ident{Name: ls.keyVar.Name, Type: ir.TypInt, Sym: ls.keyVar}
				n.Props = append(n.Props, ir.Arg{
					Name: "__focused",
					Value: &ir.Binary{
						Type: ir.TypBool,
						Op:   ast.BinAnd,
						Left: focusEqExpr(focusIDIdent(), inLoop.slotIdx),
						Right: &ir.Binary{
							Type:  ir.TypBool,
							Op:    ast.BinEq,
							Left:  cursorIdent,
							Right: keyIdent,
						},
					},
				})
			}
			walkInjectFocused(n.Children, focusIDIdent, loopSlots, staticSlots, inLoop)

		case *ir.For:
			if slot, ok := loopSlots[n]; ok {
				walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, slot)
			} else {
				walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, inLoop)
			}

		case *ir.If:
			walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, inLoop)
			walkInjectFocused(n.Else, focusIDIdent, loopSlots, staticSlots, inLoop)

		case *ir.Window:
			walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, inLoop)
		case *ir.PlatformFilter:
			walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, inLoop)
		case *ir.SlotInst:
			walkInjectFocused(n.Children, focusIDIdent, loopSlots, staticSlots, inLoop)
		case *ir.ErrorBoundary:
			walkInjectFocused(n.Children, focusIDIdent, loopSlots, staticSlots, inLoop)
		case *ir.ContextProvider:
			walkInjectFocused(n.Children, focusIDIdent, loopSlots, staticSlots, inLoop)
		}
	}
}

func focusEqExpr(left ir.Expr, slotIdx int) *ir.Binary {
	return &ir.Binary{
		Type:  ir.TypBool,
		Op:    ast.BinEq,
		Left:  left,
		Right: intLiteralLit(slotIdx),
	}
}

// ---- navigation functions ----

// buildFocusNav builds __focusNext (forward=true) or __focusPrev (forward=false).
// The generated body is an if-chain: one branch per slot that handles moving
// focus to the adjacent slot, including per-loop cursor advancement.
func buildFocusNav(name string, slots []focusSlot, focusIDIdent func() *ir.Ident, forward bool) *ir.Func {
	n := len(slots)
	var bodies [][]ir.Stmt
	for i, slot := range slots {
		var adjacent int
		if forward {
			adjacent = (i + 1) % n
		} else {
			adjacent = (i + n - 1) % n
		}

		var body []ir.Stmt
		if !slot.isLoop {
			// Static slot: just move to the adjacent slot.
			body = stmtsMoveTo(slots, adjacent, focusIDIdent, false)
		} else {
			// Loop slot: try to advance/retreat cursor within the loop first.
			ls := slot.loop
			cursorIdent := func() *ir.Ident {
				return &ir.Ident{
					Name:        ls.cursorVar.Name,
					Type:        ir.TypInt,
					Sym:         ls.cursorVar,
					Synthesized: true,
				}
			}

			// var __focusLen int = ListLength(iter)
			lenVar := &ir.LocalVar{
				Name: "__focusLen",
				Type: ir.TypInt,
				Init: callListLength(ls.forStmt.Iter),
			}
			lenIdent := func() *ir.Ident { return &ir.Ident{Name: "__focusLen", Type: ir.TypInt} }

			var innerCond ir.Expr
			var innerAdvance ir.Stmt
			var elseBody []ir.Stmt

			if forward {
				// cursor + 1 < len  →  advance cursor
				innerCond = &ir.Binary{
					Type: ir.TypBool,
					Op:   ast.BinLt,
					Left: &ir.Binary{
						Type:  ir.TypInt,
						Op:    ast.BinAdd,
						Left:  cursorIdent(),
						Right: intLiteralLit(1),
					},
					Right: lenIdent(),
				}
				innerAdvance = &ir.Assign{
					Target: cursorIdent(),
					Op:     ast.AssignSet,
					Value: &ir.Binary{
						Type:  ir.TypInt,
						Op:    ast.BinAdd,
						Left:  cursorIdent(),
						Right: intLiteralLit(1),
					},
				}
				// else: exit loop forward → go to adjacent slot (enter at start)
				elseBody = stmtsMoveTo(slots, adjacent, focusIDIdent, false)
			} else {
				// cursor > 0  →  retreat cursor
				innerCond = &ir.Binary{
					Type:  ir.TypBool,
					Op:    ast.BinGt,
					Left:  cursorIdent(),
					Right: intLiteralLit(0),
				}
				innerAdvance = &ir.Assign{
					Target: cursorIdent(),
					Op:     ast.AssignSet,
					Value: &ir.Binary{
						Type:  ir.TypInt,
						Op:    ast.BinSub,
						Left:  cursorIdent(),
						Right: intLiteralLit(1),
					},
				}
				// else: exit loop backward → go to adjacent slot (enter at end if loop)
				elseBody = stmtsMoveTo(slots, adjacent, focusIDIdent, true)
			}

			body = []ir.Stmt{
				lenVar,
				&ir.If{
					Cond: innerCond,
					Body: []ir.Stmt{innerAdvance},
					Else: elseBody,
				},
			}
		}

		bodies = append(bodies, body)
	}

	// Build a single if/else-if chain so that matching and mutating __focusID
	// in one branch does not trigger a subsequent branch in the same call.
	// Construct from the tail so each node becomes the Else of the previous.
	var chain ir.Stmt
	for i := len(slots) - 1; i >= 0; i-- {
		chain = &ir.If{
			Cond: focusEqExpr(focusIDIdent(), i),
			Body: bodies[i],
			Else: func() []ir.Stmt {
				if chain == nil {
					return nil
				}
				return []ir.Stmt{chain}
			}(),
		}
	}

	var block []ir.Stmt
	if chain != nil {
		block = []ir.Stmt{chain}
	}
	return &ir.Func{
		Name:        name,
		Return:      ir.TypVoid,
		Synthesized: true,
		Block:       block,
	}
}

// stmtsMoveTo returns statements that set __focusID to slotIdx.
// When enterEnd is true and the target is a loop slot, the cursor is set
// to ListLength(iter)-1 (entering the loop from the end going backward).
// When enterEnd is false, cursor is reset to 0.
func stmtsMoveTo(slots []focusSlot, slotIdx int, focusIDIdent func() *ir.Ident, enterEnd bool) []ir.Stmt {
	setSlot := &ir.Assign{
		Target: focusIDIdent(),
		Op:     ast.AssignSet,
		Value:  intLiteralLit(slotIdx),
	}
	if !slots[slotIdx].isLoop {
		return []ir.Stmt{setSlot}
	}
	ls := slots[slotIdx].loop
	cursorIdent := &ir.Ident{
		Name:        ls.cursorVar.Name,
		Type:        ir.TypInt,
		Sym:         ls.cursorVar,
		Synthesized: true,
	}
	if !enterEnd {
		// Enter at cursor=0.
		return []ir.Stmt{
			setSlot,
			&ir.Assign{Target: cursorIdent, Op: ast.AssignSet, Value: intLiteralLit(0)},
		}
	}
	// Enter at cursor = ListLength(iter) - 1.
	lenVar := &ir.LocalVar{
		Name: "__focusLen",
		Type: ir.TypInt,
		Init: callListLength(ls.forStmt.Iter),
	}
	return []ir.Stmt{
		lenVar,
		setSlot,
		&ir.Assign{
			Target: cursorIdent,
			Op:     ast.AssignSet,
			Value: &ir.Binary{
				Type:  ir.TypInt,
				Op:    ast.BinSub,
				Left:  &ir.Ident{Name: "__focusLen", Type: ir.TypInt},
				Right: intLiteralLit(1),
			},
		},
	}
}

// callListLength builds a Call expression for the ListLength intrinsic.
func callListLength(iter ir.Expr) *ir.Call {
	var params []*ir.Param
	if def := ir.LookupIntrinsic("ListLength"); def != nil {
		params = def.Params
	}
	fn := &ir.Func{
		Name:      "ListLength",
		Intrinsic: "ListLength",
		Return:    ir.TypInt,
		Params:    params,
	}
	return &ir.Call{
		Type: ir.TypInt,
		Func: fn,
		Args: []ir.CallArg{{Value: iter}},
	}
}

// ---- focusable detection (unchanged) ----

// nodeEffectiveFocusable returns true when a NodeInst's effective focusable
// value is true. Checks the explicit prop first; falls back to the component's
// default for that prop.
func nodeEffectiveFocusable(n *ir.NodeInst) bool {
	for _, p := range n.Props {
		if p.Name == "focusable" {
			if lit, ok := p.Value.(*ir.Literal); ok {
				return lit.Raw == "true"
			}
			return true
		}
	}
	if n.Component == nil {
		return false
	}
	for _, p := range n.Component.Props {
		if p.Name == "focusable" && p.Default != nil {
			if lit, ok := p.Default.(*ir.Literal); ok {
				return lit.Raw == "true"
			}
		}
	}
	return false
}
