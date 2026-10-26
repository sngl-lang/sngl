package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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
// renders, which the view counts into posVar and lenFunc counts for Tab.
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
		if comp.RuntimeInstance {
			if err := refuseFocusInRecursion(comp); err != nil {
				return err
			}
		}
		lowerFocusInOwner(comp.Stmts, &comp.Vars, &comp.Funcs)
	}
	lowerFocusInOwner(pkg.Body, &pkg.Vars, &pkg.Funcs)
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

	shown := focusShownExprs(stmts, slots)
	if !slices.ContainsFunc(shown, func(e ir.Expr) bool { return e != nil }) {
		*funcs = append(*funcs,
			buildFocusNav("__focusNext", slots, focusIDIdent, true),
			buildFocusNav("__focusPrev", slots, focusIDIdent, false),
		)
		return
	}
	// Some stop may draw nothing, and Tab passes over one that does: a step
	// moves to the adjacent stop, and the step is taken again while the stop
	// reached is not shown -- once per other stop at most, so a screen showing
	// none of them comes back to where it was. Settle moves focus off a stop
	// that stopped being shown, and says whether it did; the target calls it
	// once the program has changed what it shows.
	shownFn := buildFocusShown(slots, shown, focusIDIdent)
	stepNext := buildFocusNav("__focusStepNext", slots, focusIDIdent, true)
	stepPrev := buildFocusNav("__focusStepPrev", slots, focusIDIdent, false)
	next := buildFocusSkip("__focusNext", stepNext, shownFn, len(slots))
	*funcs = append(*funcs, shownFn, stepNext, stepPrev, next,
		buildFocusSkip("__focusPrev", stepPrev, shownFn, len(slots)),
		&ir.Func{
			Name:        "__focusSettle",
			Return:      ir.TypBool,
			Synthesized: true,
			Block: []ir.Stmt{
				&ir.If{
					Cond: notExpr(callFocusFunc(shownFn, ir.TypBool)),
					Body: []ir.Stmt{
						&ir.CallStmt{Call: callFocusFunc(next, ir.TypVoid)},
						&ir.Return{Value: &ir.Literal{Type: ir.TypBool, Value: "true"}},
					},
				},
				&ir.Return{Value: &ir.Literal{Type: ir.TypBool, Value: "false"}},
			},
		},
	)
}

// ---- stops that draw nothing ----

// focusShownExprs is, per slot, what says the slot's stop is drawn, or nil
// where it always is. A static stop is drawn where the branches around it are
// taken and its `focus` is enabled -- on bubbletea, the target that orders
// focus, where its Style does not display it none -- and a loop slot where the
// loop renders a stop. A condition the stop's owner cannot read outside its
// view -- one naming a loop's variable, a parameter, or a local the view
// declares -- is left out, so that stop is taken to be drawn.
func focusShownExprs(stmts []ir.Stmt, slots []focusSlot) []ir.Expr {
	locals := map[*ir.Var]bool{}
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil {
			locals[lv.Sym] = true
		}
		return nil
	})
	readable := func(e ir.Expr) bool {
		ok := true
		_ = ir.WalkExprs(e, func(x ir.Expr) error {
			id, isID := x.(*ir.Ident)
			if !isID {
				return nil
			}
			switch sym := id.Sym.(type) {
			case *ir.LoopVar, *ir.Param:
				ok = false
			case *ir.Var:
				ok = !locals[sym]
			}
			if !ok {
				return ir.SkipAll
			}
			return nil
		})
		return ok
	}
	static := map[*ir.NodeInst]int{}
	shown := make([]ir.Expr, len(slots))
	for i, s := range slots {
		if s.isLoop {
			shown[i] = &ir.Binary{Type: ir.TypBool, Op: ast.BinGt, Left: callLoopLen(s.loop), Right: intLiteralLit(0)}
		} else {
			static[s.node] = i
		}
	}
	var walk func(stmts []ir.Stmt, conds []ir.Expr)
	walk = func(stmts []ir.Stmt, conds []ir.Expr) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if i, ok := static[n]; ok {
					own := conds
					if e := focusEnabledField(n); e != nil {
						if _, known := foldedBool(e.Value); !known && readable(e.Value) {
							own = append(slices.Clip(conds), e.Value)
						}
					}
					if len(own) > 0 {
						shown[i] = andAll(own)
					}
				}
				for _, b := range suppliedBodies(n) {
					walk(b, conds)
				}
			case *ir.If:
				if n.FromTernary || !readable(n.Cond) {
					walk(n.Body, conds)
					walk(n.Else, conds)
					continue
				}
				walk(n.Body, append(slices.Clip(conds), deepCloneExpr(n.Cond)))
				walk(n.Else, append(slices.Clip(conds), notExpr(deepCloneExpr(n.Cond))))
			case *ir.Match:
				for _, b := range ir.TransparentBlocks(n) {
					walk(*b, conds)
				}
			case *ir.SlotInst:
				walk(n.Children, conds)
			case *ir.ErrorBoundary:
				walk(n.Children, conds)
			}
		}
	}
	walk(stmts, nil)
	// The stop is the node's whatever its record says, which the view has no
	// use for once read: a stop in a loop is counted while it is displayed
	// none, which is the loop's to answer.
	_ = ir.Walk(stmts, func(node ir.Node) error {
		if n, ok := node.(*ir.NodeInst); ok {
			if e := focusEnabledField(n); e != nil {
				if _, known := foldedBool(e.Value); !known {
					e.Value = &ir.Literal{Type: ir.TypBool, Value: "true"}
				}
			}
		}
		return nil
	})
	return shown
}

func notExpr(e ir.Expr) ir.Expr {
	return &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: e}
}

// andAll is a copy of each of conds, joined by `&&`: a stop's conditions are
// shared with the stops beside it, and a later pass rewrites one in place.
func andAll(conds []ir.Expr) ir.Expr {
	e := deepCloneExpr(conds[0])
	for _, c := range conds[1:] {
		e = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: e, Right: deepCloneExpr(c)}
	}
	return e
}

func callFocusFunc(f *ir.Func, t *ir.Type) *ir.Call {
	return &ir.Call{Type: t, Func: f}
}

// buildFocusShown builds __focusShown, whether the stop focus is on is drawn.
func buildFocusShown(slots []focusSlot, shown []ir.Expr, focusIDIdent func() *ir.Ident) *ir.Func {
	var block []ir.Stmt
	for i := range slots {
		if shown[i] == nil {
			continue
		}
		block = append(block, &ir.If{
			Cond: focusEqExpr(focusIDIdent(), i),
			Body: []ir.Stmt{&ir.Return{Value: shown[i]}},
		})
	}
	block = append(block, &ir.Return{Value: &ir.Literal{Type: ir.TypBool, Value: "true"}})
	return &ir.Func{Name: "__focusShown", Return: ir.TypBool, Synthesized: true, Block: block}
}

// buildFocusSkip builds a move that takes step, then takes it again while the
// stop reached is not shown, at most once per other stop.
func buildFocusSkip(name string, step, shown *ir.Func, stops int) *ir.Func {
	block := []ir.Stmt{&ir.CallStmt{Call: callFocusFunc(step, ir.TypVoid)}}
	for range stops - 1 {
		block = append(block, &ir.If{
			Cond: notExpr(callFocusFunc(shown, ir.TypBool)),
			Body: []ir.Stmt{&ir.CallStmt{Call: callFocusFunc(step, ir.TypVoid)}},
		})
	}
	return &ir.Func{Name: name, Return: ir.TypVoid, Synthesized: true, Block: block}
}

// refuseFocusInRecursion: Tab and Update reach only the stops the window
// renders, and a recursion renders each level from a function of its own.
func refuseFocusInRecursion(comp *ir.Component) error {
	slots := gatherFocusSlots(comp.Stmts)
	if len(slots) == 0 {
		return nil
	}
	var at *ir.NodeInst
	if slots[0].isLoop {
		_ = ir.Walk(slots[0].loop.forStmt.Body, func(node ir.Node) error {
			if n, ok := node.(*ir.NodeInst); ok && nodeEffectiveFocusable(n) {
				at = n
				return ir.SkipAll
			}
			return nil
		})
	} else {
		at = slots[0].node
	}
	// An inlined primitive is positioned in its platform package.
	pos := ir.StmtPos(at)
	if comp.AST != nil && pos.File != comp.AST.Pos.File {
		pos = comp.AST.Pos
	}
	return fmt.Errorf("%s: the recursive component %q renders a focusable node in its own body; this target's focus order reaches only what the window renders, and a recursion renders each level from a function of its own -- render the focusable nodes outside %q", pos, comp.Name, comp.Name)
}

func suppliedBodies(n *ir.NodeInst) [][]ir.Stmt {
	var out [][]ir.Stmt
	for _, s := range ir.SuppliedContent(n) {
		out = append(out, s.Body)
	}
	return out
}

// refuseFocusReadingSlotArgs: content handed to a recursion is one focus stop,
// whose handler Update runs where it was written, with no slot argument bound.
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
		case *ir.Match:
			for _, h := range n.Heads() {
				if p := reads(h); p != nil {
					if at := firstFocusable([]ir.Stmt{n}); at != nil {
						return at, p
					}
				}
			}
			for _, arm := range n.Arms {
				if at, p := focusReadsParam(arm.Body, params); at != nil {
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
		case *ir.If, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				walkForSlots(*b, slots)
			}
		case *ir.SlotInst:
			walkForSlots(n.Children, slots)
		case *ir.ErrorBoundary:
			walkForSlots(n.Children, slots)
		}
	}
}

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
		case *ir.If, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				if hasFocusableNodes(*b) {
					return true
				}
			}
		case *ir.SlotInst:
			if hasFocusableNodes(n.Children) {
				return true
			}
		case *ir.ErrorBoundary:
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

		case *ir.If, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				walkInjectFocused(*b, focusIDIdent, loopSlots, staticSlots, inLoop)
			}

		case *ir.SlotInst:
			walkInjectFocused(n.Children, focusIDIdent, loopSlots, staticSlots, inLoop)
		case *ir.ErrorBoundary:
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

// buildLoopLenFunc is the slot's loop with each focusable node replaced by an
// increment.
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
		case *ir.Match:
			if n.FromValue {
				continue
			}
			arms := make([][]ir.Stmt, len(n.Arms))
			counts := false
			for i, arm := range n.Arms {
				arms[i] = focusCountStmts(arm.Body, count)
				counts = counts || len(arms[i]) > 0
			}
			if !counts {
				continue
			}
			m := n.Over(func(i int, _ []ir.Stmt) []ir.Stmt { return arms[i] })
			m.Subject = deepCloneExpr(n.Subject)
			out = append(out, m)
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
	return &ir.Call{
		Type: ir.TypInt,
		Func: ir.IntrinsicMethod("length", "list.length", iter.ExprType(), ir.TypInt),
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
	// One that may be enabled is a stop, which focusShownExprs passes over
	// while it is not.
	if slices.ContainsFunc(n.Props, func(p ir.Arg) bool { return p.Name == "focus" }) {
		e := focusEnabledField(n)
		if e == nil {
			return false
		}
		on, known := foldedBool(e.Value)
		return on || !known
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

// focusEnabledField is the `enabled` field of n's `focus` record, nil where n
// carries none or its record is not a literal.
func focusEnabledField(n *ir.NodeInst) *ir.FieldInit {
	for _, p := range n.Props {
		if p.Name != "focus" {
			continue
		}
		if sl, ok := p.Value.(*ir.StructLit); ok {
			for i := range sl.Fields {
				if sl.Fields[i].Name == "enabled" {
					return &sl.Fields[i]
				}
			}
		}
	}
	return nil
}

// foldedBool is what e says where the build can read it here: a literal, the
// negation of one, or an equality between two members of one enum -- what an
// override's `style.display != ui.Display.none` is once a Style literal's
// field is read into it. The optimizer folds the rest, after this pass.
func foldedBool(e ir.Expr) (value, known bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type != nil && x.Type.Equal(ir.TypBool) {
			return x.Value == "true", true
		}
	case *ir.Unary:
		if x.Op == ast.UnaryNot {
			v, ok := foldedBool(x.Operand)
			return !v, ok
		}
	case *ir.Binary:
		if x.Op != ast.BinEq && x.Op != ast.BinNeq {
			return false, false
		}
		l, lok := enumMember(x.Left)
		r, rok := enumMember(x.Right)
		if !lok || !rok || !x.Left.ExprType().Equal(x.Right.ExprType()) {
			return false, false
		}
		return (l == r) == (x.Op == ast.BinEq), true
	}
	return false, false
}

// enumMember is the member e names, where e names one of a plain enum.
func enumMember(e ir.Expr) (string, bool) {
	id, ok := e.(*ir.Ident)
	if !ok || id.Member == "" || id.Type == nil || id.Type.Kind != ir.TypeEnum || ir.PayloadEnum(id.Type) != nil {
		return "", false
	}
	return id.Member, true
}
