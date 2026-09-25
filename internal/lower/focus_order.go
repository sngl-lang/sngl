package lower

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passFocusOrder = pass{
	name:    "FocusOrder",
	enabled: func(c Features) bool { return c.FocusOrder },
	apply:   lowerFocusOrder,
}

// focusSlot is one position in the tab-order sequence: a single static
// focusable node, or the outermost for-loop whose subtree holds any.
type focusSlot struct {
	isLoop  bool
	slotIdx int
	node    *ir.NodeInst // non-nil for static slot
	loop    *loopSlotInfo
}

// A loop slot's cursor is an ordinal over the focusable nodes the loop
// renders, in render order, whatever loops and branches they sit under inside
// it. The view counts them into posVar as it renders; lenFunc counts them the
// same way for navigation.
type loopSlotInfo struct {
	forStmt   *ir.For
	cursorVar *ir.Var // __focusLoopN_cursor
	posVar    *ir.LoopVar
	lenFunc   *ir.Func
}

func lowerFocusOrder(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		if err := refuseFocusReadingSlotArgs(o.Stmts()); err != nil {
			return err
		}
	}
	for _, comp := range pkg.Components {
		lowerFocusInOwner(comp.Body, &comp.Vars, &comp.Funcs)
	}
	lowerFocusInOwner(pkg.Body, &pkg.Vars, &pkg.Funcs)
	for _, w := range pkg.Windows {
		lowerFocusInOwner(w.Children, &pkg.Vars, &pkg.Funcs)
	}
	return nil
}

func lowerFocusInOwner(stmts []ir.Stmt, vars *[]*ir.Var, funcs *[]*ir.Func) {
	slots := gatherFocusSlots(stmts)
	if len(slots) == 0 {
		return
	}

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

	for i := range slots {
		s := &slots[i]
		if !s.isLoop {
			continue
		}
		name := fmt.Sprintf("__focusLoop%d_cursor", s.slotIdx)
		cv := &ir.Var{Name: name, Type: ir.TypInt, Init: intLiteralLit(0), Synthesized: true}
		s.loop.cursorVar = cv
		s.loop.posVar = &ir.LoopVar{Name: fmt.Sprintf("__focusPos%d", s.slotIdx), Type: ir.TypInt}
		*vars = append(*vars, cv)
	}

	injectFocusedProps(stmts, slots, focusIDIdent)

	for i := range slots {
		if slots[i].isLoop {
			fn := buildLoopLenFunc(slots[i])
			slots[i].loop.lenFunc = fn
			*funcs = append(*funcs, fn)
		}
	}

	*funcs = append(*funcs,
		buildFocusNav("__focusNext", slots, focusIDIdent, true),
		buildFocusNav("__focusPrev", slots, focusIDIdent, false),
	)
}

func suppliedBodies(n *ir.NodeInst) [][]ir.Stmt {
	var out [][]ir.Stmt
	for _, s := range ir.SuppliedContent(n) {
		out = append(out, s.Body)
	}
	return out
}

// refuseFocusReadingSlotArgs reports a focusable node in a population handed
// to a component built at run time whose handler, or a branch or loop around
// it, reads the population's own parameters. Such a component is a recursion
// here, and the content has one focus stop wherever it is rendered, so Update
// runs its handler at the site that wrote it, where no argument is bound.
func refuseFocusReadingSlotArgs(stmts []ir.Stmt) error {
	var err error
	_ = ir.Walk(stmts, func(node ir.Node) error {
		n, ok := node.(*ir.NodeInst)
		if !ok || err != nil || n.Component == nil || !n.Component.RuntimeInstance {
			return nil
		}
		for _, s := range ir.SuppliedContent(n) {
			if s.Content == nil || len(s.Content.Params) == 0 {
				continue
			}
			params := map[ir.Symbol]bool{}
			for _, p := range s.Content.Params {
				params[p] = true
			}
			if at, p := focusReadsParam(s.Body, params); at != nil {
				pos := nodePos(at)
				if p.AST != nil && p.AST.Pos.IsValid() {
					pos = p.AST.Pos.String()
				}
				err = fmt.Errorf("%s: a focusable node in the %q content handed to %q reads the slot argument %q -- %q is a recursion, rendered once per level, and bubbletea gives that content one focus stop and runs its handler where the content was written, where no argument is bound; read the caller's state instead", pos, s.Decl.Name, n.Component.Name, p.Name, n.Component.Name)
				return ir.SkipAll
			}
		}
		return nil
	})
	return err
}

// focusReadsParam is the first focusable node under stmts whose handler, or
// an if or for around it, reads one of params, and the name it read.
func focusReadsParam(stmts []ir.Stmt, params map[ir.Symbol]bool) (*ir.NodeInst, *ir.Ident) {
	reads := func(root any) *ir.Ident {
		var found *ir.Ident
		_ = ir.Walk(root, func(node ir.Node) error {
			if id, ok := node.(*ir.Ident); ok && id.Sym != nil && params[id.Sym] {
				found = id
				return ir.SkipAll
			}
			return nil
		})
		return found
	}
	firstFocusable := func(stmts []ir.Stmt) *ir.NodeInst {
		var found *ir.NodeInst
		_ = ir.Walk(stmts, func(node ir.Node) error {
			if n, ok := node.(*ir.NodeInst); ok && nodeEffectiveFocusable(n) {
				found = n
				return ir.SkipAll
			}
			return nil
		})
		return found
	}
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				for _, h := range n.Handlers {
					if h.Func == nil {
						continue
					}
					if p := reads(h.Func.Block); p != nil {
						return n, p
					}
				}
			}
			for _, b := range suppliedBodies(n) {
				if at, p := focusReadsParam(b, params); at != nil {
					return at, p
				}
			}
		case *ir.If:
			if p := reads(n.Cond); p != nil {
				if at := firstFocusable(append(slices.Clip(n.Body), n.Else...)); at != nil {
					return at, p
				}
			}
			for _, b := range [][]ir.Stmt{n.Body, n.Else} {
				if at, p := focusReadsParam(b, params); at != nil {
					return at, p
				}
			}
		case *ir.For:
			if p := reads(n.Iter); p != nil {
				if at := firstFocusable(append(slices.Clip(n.Body), n.Else...)); at != nil {
					return at, p
				}
			}
			for _, b := range [][]ir.Stmt{n.Body, n.Else} {
				if at, p := focusReadsParam(b, params); at != nil {
					return at, p
				}
			}
		case *ir.ErrorBoundary:
			if at, p := focusReadsParam(n.Children, params); at != nil {
				return at, p
			}
		}
	}
	return nil, nil
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
			for _, b := range suppliedBodies(n) {
				walkForSlots(b, slots)
			}
		case *ir.For:
			if hasFocusableNodes(n.Body) || hasFocusableNodes(n.Else) {
				idx := len(*slots)
				*slots = append(*slots, focusSlot{
					isLoop:  true,
					slotIdx: idx,
					loop:    &loopSlotInfo{forStmt: n},
				})
			}
		case *ir.If:
			walkForSlots(n.Body, slots)
			walkForSlots(n.Else, slots)
		case *ir.SlotInst:
			walkForSlots(n.Children, slots)
		case *ir.ErrorBoundary:
			walkForSlots(n.Children, slots)
		case *ir.ContextProvider:
			walkForSlots(n.Children, slots)
		}
	}
}

// hasFocusableNodes reports whether stmts hold a focusable NodeInst anywhere
// below them, nested loops included.
func hasFocusableNodes(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				return true
			}
			if slices.ContainsFunc(suppliedBodies(n), hasFocusableNodes) {
				return true
			}
		case *ir.For:
			if hasFocusableNodes(n.Body) || hasFocusableNodes(n.Else) {
				return true
			}
		case *ir.If:
			if hasFocusableNodes(n.Body) || hasFocusableNodes(n.Else) {
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
		}
	}
	return false
}

// ---- prop injection ----

func injectFocusedProps(stmts []ir.Stmt, slots []focusSlot, focusIDIdent func() *ir.Ident) {
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
				n.Props = append(n.Props, ir.Arg{
					Name:  "__focused",
					Value: focusEqExpr(focusIDIdent(), slot.slotIdx),
				})
			} else if inLoop != nil && nodeEffectiveFocusable(n) {
				ls := inLoop.loop
				n.Props = append(n.Props, ir.Arg{
					Name: "__focused",
					Value: &ir.Binary{
						Type: ir.TypBool,
						Op:   ast.BinAnd,
						Left: focusEqExpr(focusIDIdent(), inLoop.slotIdx),
						Right: &ir.Binary{
							Type:  ir.TypBool,
							Op:    ast.BinEq,
							Left:  &ir.Ident{Name: ls.cursorVar.Name, Type: ir.TypInt, Sym: ls.cursorVar, Synthesized: true},
							Right: &ir.Ident{Name: ls.posVar.Name, Type: ir.TypInt, Sym: ls.posVar, Synthesized: true},
						},
					},
				})
			}
			for _, b := range suppliedBodies(n) {
				walkInjectFocused(b, focusIDIdent, loopSlots, staticSlots, inLoop)
			}

		case *ir.For:
			slot := inLoop
			if s, ok := loopSlots[n]; ok {
				slot = s
			}
			walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, slot)
			walkInjectFocused(n.Else, focusIDIdent, loopSlots, staticSlots, slot)

		case *ir.If:
			walkInjectFocused(n.Body, focusIDIdent, loopSlots, staticSlots, inLoop)
			walkInjectFocused(n.Else, focusIDIdent, loopSlots, staticSlots, inLoop)

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

// buildLoopLenFunc counts what a loop slot renders focusable right now: the
// slot's loop with every branch and nested loop kept and each focusable node
// replaced by an increment. A slot's children render nothing on this
// platform's view, so their nodes are not counted.
func buildLoopLenFunc(slot focusSlot) *ir.Func {
	ls := slot.loop
	sym := &ir.Var{Name: "__focusCount", Type: ir.TypInt, Synthesized: true}
	ref := func() *ir.Ident {
		return &ir.Ident{Name: sym.Name, Type: ir.TypInt, Sym: sym, Synthesized: true}
	}
	body := []ir.Stmt{&ir.LocalVar{Name: sym.Name, Type: ir.TypInt, Init: intLiteralLit(0), Sym: sym}}
	body = append(body, focusCountStmts([]ir.Stmt{ls.forStmt}, ref)...)
	body = append(body, &ir.Return{Value: ref()})
	return &ir.Func{
		Name:        fmt.Sprintf("__focusLoop%d_len", slot.slotIdx),
		Return:      ir.TypInt,
		Synthesized: true,
		Block:       body,
	}
}

func focusCountStmts(stmts []ir.Stmt, count func() *ir.Ident) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				out = append(out, &ir.Assign{
					Target: count(),
					Op:     ast.AssignSet,
					Value:  &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: count(), Right: intLiteralLit(1)},
				})
			}
			for _, b := range suppliedBodies(n) {
				out = append(out, focusCountStmts(b, count)...)
			}
		case *ir.For:
			body, els := focusCountStmts(n.Body, count), focusCountStmts(n.Else, count)
			if len(body) == 0 && len(els) == 0 {
				continue
			}
			loop := *n
			loop.Iter = deepCloneExpr(n.Iter)
			loop.Body, loop.Else = body, els
			out = append(out, &loop)
		case *ir.If:
			if n.FromTernary {
				continue
			}
			body, els := focusCountStmts(n.Body, count), focusCountStmts(n.Else, count)
			if len(body) == 0 && len(els) == 0 {
				continue
			}
			out = append(out, &ir.If{Cond: deepCloneExpr(n.Cond), Body: body, Else: els})
		case *ir.ErrorBoundary:
			out = append(out, focusCountStmts(n.Children, count)...)
		case *ir.ContextProvider:
			out = append(out, focusCountStmts(n.Children, count)...)
		}
	}
	return out
}

func callLoopLen(ls *loopSlotInfo) *ir.Call {
	return &ir.Call{Type: ir.TypInt, Func: ls.lenFunc}
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
			body = stmtsMoveTo(slots, adjacent, focusIDIdent, !forward)
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
			lenSym := &ir.Var{Name: "__focusLen", Type: ir.TypInt, Synthesized: true}
			lenVar := &ir.LocalVar{
				Name: "__focusLen",
				Type: ir.TypInt,
				Init: callLoopLen(ls),
				Sym:  lenSym,
			}
			lenIdent := func() *ir.Ident {
				return &ir.Ident{Name: "__focusLen", Type: ir.TypInt, Sym: lenSym, Synthesized: true}
			}

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

			stmts := []ir.Stmt{}
			if forward {
				stmts = append(stmts, lenVar)
			}
			body = append(stmts, &ir.If{
				Cond: innerCond,
				Body: []ir.Stmt{innerAdvance},
				Else: elseBody,
			})
		}

		bodies = append(bodies, body)
	}

	// Build a single if/else-if chain so that matching and mutating __focusID
	// in one branch does not trigger a subsequent branch in the same call.
	// Construct from the tail so each node becomes the Else of the previous.
	var chain ir.Stmt
	for i := range slices.Backward(slots) {
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
	lenSym := &ir.Var{Name: "__focusLen", Type: ir.TypInt, Synthesized: true}
	lenVar := &ir.LocalVar{
		Name: "__focusLen",
		Type: ir.TypInt,
		Init: callLoopLen(ls),
		Sym:  lenSym,
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
				Left:  &ir.Ident{Name: "__focusLen", Type: ir.TypInt, Sym: lenSym, Synthesized: true},
				Right: intLiteralLit(1),
			},
		},
	}
}

// callListLength builds a Call for list.length, instantiated at the element
// type of the list being measured.
func callListLength(iter ir.Expr) *ir.Call {
	var params []*ir.Param
	if def := ir.LookupIntrinsic("list.length"); def != nil {
		params, _ = def.Instantiate(listElem(iter))
	}
	fn := &ir.Func{
		Name:      "length",
		Receiver:  "list",
		Intrinsic: "list.length",
		Return:    ir.TypInt,
		Params:    params,
	}
	return &ir.Call{
		Type: ir.TypInt,
		Func: fn,
		Args: []ir.CallArg{{Value: iter}},
	}
}

// listElem is the element type of a list expression, or nil for anything else
// -- Instantiate then leaves the type variable in place.
func listElem(e ir.Expr) *ir.Type {
	if e == nil {
		return nil
	}
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeList || len(t.Elems) != 1 {
		return nil
	}
	return t.Elems[0]
}

// ---- focusable detection (unchanged) ----

// nodeEffectiveFocusable returns true when a NodeInst's effective focusable
// value is true. Checks the explicit prop first; falls back to the component's
// default for that prop.
func nodeEffectiveFocusable(n *ir.NodeInst) bool {
	// Blueprint primitives (inlined Styled/Widget) declare focusability via a
	// `focus=Focus{enabled=true}` record rather than a bare `focusable` prop.
	for _, p := range n.Props {
		if p.Name == "focus" {
			if sl, ok := p.Value.(*ir.StructLit); ok {
				for _, f := range sl.Fields {
					if f.Name == "enabled" {
						if lit, ok := f.Value.(*ir.Literal); ok {
							return lit.Value == "true"
						}
						return true
					}
				}
			}
			return false
		}
	}
	for _, p := range n.Props {
		if p.Name == "focusable" {
			if lit, ok := p.Value.(*ir.Literal); ok {
				return lit.Value == "true"
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
				return lit.Value == "true"
			}
		}
	}
	return false
}
