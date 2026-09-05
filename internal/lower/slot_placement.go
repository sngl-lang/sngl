package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A slot places its children instead of rebuilding them, where the platform's
// container can put a child at a position.
//
// Without that, a re-render removes every child and appends them all back. It
// is correct and it is what every target still does by default, but it detaches
// and reattaches nodes that did not move -- so focus, selection and scroll go
// with them, and appending one row to a list of a hundred touches a hundred and
// one nodes.
//
// With it the render walks the desired order against the order already there,
// keeping a cursor into the old list:
//
//	if <prev>[<i>] is this node   -- it is already in place, advance the cursor
//	otherwise                     -- put it before <prev>[<i>], and do not advance
//
// The cursor stays put on a mismatch because the old node there has not been
// matched yet; the next desired node may well be it. Appending is then one
// operation -- every existing row matches, the cursor reaches the end, and the
// new row is placed before nothing, which the protocol defines as the end.
// Inserting at the front is also one, because the first placement is before the
// old head and every row after it matches.
//
// What is left in the old list and not in the new one is removed at the end.
type slotPlacement struct {
	// prev is the order the parent held when the render began, and slot is the
	// order it is being given. Both are node handles.
	prev *ir.Var
	slot *ir.Var
	// idx is the cursor into prev.
	idx *ir.Var
	// refs counts the placements emitted, so the local each one binds its
	// reference node to is its own. Two children of one reactive `if` are
	// placed in the same scope, and one name for both is a redeclaration.
	refs int
	// parent is the container being placed into.
	parent ir.Expr
}

// newSlotPlacement is the placement state for one slot render, or nil when the
// target cannot place a child and the caller should keep rebuilding.
func (st *reactivityState) newSlotPlacement(slotID string, slotVar *ir.Var, parent ir.Expr) *slotPlacement {
	if !st.caps.InsertBefore {
		return nil
	}
	n := strconv.Itoa(st.instCounter)
	st.instCounter++
	return &slotPlacement{
		prev:   &ir.Var{Name: "__place" + n + "_prev", Type: ir.ListOf(ir.TypDyn), Synthesized: true},
		slot:   slotVar,
		idx:    &ir.Var{Name: "__place" + n + "_i", Type: ir.TypInt, Synthesized: true},
		parent: parent,
	}
}

// open records the order the parent already holds and starts the cursor.
func (sp *slotPlacement) open() []ir.Stmt {
	return []ir.Stmt{
		&ir.LocalVar{Name: sp.prev.Name, Type: sp.prev.Type, Sym: sp.prev, Init: varRef(sp.slot)},
		&ir.LocalVar{Name: sp.idx.Name, Type: ir.TypInt, Sym: sp.idx, Init: intLiteralLit(0)},
		&ir.Assign{Target: varRef(sp.slot), Op: ast.AssignSet, Value: &ir.ListLit{Type: ir.ListOf(ir.TypDyn)}},
	}
}

// place puts one node where the desired order says it goes, or leaves it alone
// when it is already there.
func (st *reactivityState) place(sp *slotPlacement, name string) []ir.Stmt {
	node := func() ir.Expr {
		return &ir.Ident{Name: name, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
	}
	inRange := func() ir.Expr {
		return &ir.Binary{
			Type: ir.TypBool, Op: ast.BinLt,
			Left:  varRef(sp.idx),
			Right: callListLength(varRef(sp.prev)),
		}
	}
	atCursor := func() ir.Expr {
		return &ir.Index{Type: ir.TypDyn, Operand: varRef(sp.prev), Idx: varRef(sp.idx)}
	}

	// The ref answers both questions at once. In range it is the old node at
	// the cursor, so matching it means the node is already there; out of range
	// it is null, which the protocol defines as the end -- and a node is never
	// null, so the same comparison falls through to the insert.
	ref := &ir.Var{Name: sp.idx.Name + "_ref" + strconv.Itoa(sp.refs), Type: ir.TypDyn, Synthesized: true}
	sp.refs++
	return []ir.Stmt{
		&ir.LocalVar{Name: ref.Name, Type: ir.TypDyn, Sym: ref, Init: &ir.Literal{Type: ir.TypNull}},
		&ir.If{
			Cond: inRange(),
			Body: []ir.Stmt{&ir.Assign{Target: varRef(ref), Op: ast.AssignSet, Value: atCursor()}},
		},
		&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: varRef(ref), Right: node()},
			Body: []ir.Stmt{&ir.Assign{
				Target: varRef(sp.idx),
				Op:     ast.AssignSet,
				Value:  &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: varRef(sp.idx), Right: intLiteralLit(1)},
			}},
			Else: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics[ir.NodeOpInsertBefore],
				Args: []ir.CallArg{
					{Value: sp.parent},
					{Value: node()},
					{Value: varRef(ref)},
				},
			}}},
		},
	}
}

// close removes what the parent held and the render did not ask for.
func (st *reactivityState) closeSlotPlacement(sp *slotPlacement) []ir.Stmt {
	e := &ir.LoopVar{Name: sp.prev.Name + "_e", Type: ir.TypDyn}
	eRef := func() ir.Expr {
		return &ir.Ident{Name: e.Name, Type: ir.TypDyn, IsElementRef: true, Sym: e, Synthesized: true}
	}
	return []ir.Stmt{&ir.For{
		Key:      e.Name,
		KeySym:   e,
		Iter:     varRef(sp.prev),
		ElemType: ir.TypDyn,
		Body: []ir.Stmt{&ir.If{
			Cond: &ir.Binary{
				Type: ir.TypBool, Op: ast.BinLt,
				Left:  callListIndexOf(varRef(sp.slot), eRef()),
				Right: intLiteralLit(0),
			},
			Body: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics[ir.NodeOpRemoveChild],
				Args:     []ir.CallArg{{Value: sp.parent}, {Value: eRef()}},
			}}},
		}},
	}}
}

// callListIndexOf builds `xs.indexOf(item)`. The derived `contains` is SNGL
// source rather than an intrinsic, and by this pass there is nothing left to
// inline it from.
func callListIndexOf(xs, item ir.Expr) *ir.Call {
	var params []*ir.Param
	if d := ir.LookupIntrinsic("list.indexOf"); d != nil {
		params, _ = d.Instantiate(ir.TypDyn)
	}
	return &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "indexOf", Receiver: "list", Intrinsic: "list.indexOf", Params: params, Return: ir.TypInt},
		Args: []ir.CallArg{{Value: xs}, {Value: item}},
	}
}
