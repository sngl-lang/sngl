package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passFocusOrder = pass{
	name:    "FocusOrder",
	enabled: func(c Caps) bool { return c.FocusOrder },
	apply:   lowerFocusOrder,
}

// lowerFocusOrder walks each component's and window's visual body, finds
// nodes with focusable=true (via component prop default or explicit override),
// assigns them sequential integer IDs, and injects:
//
//   - var __focusID int — currently focused slot (0-based)
//   - func __focusNext() — advances focus modulo total count
//   - func __focusPrev() — retreats focus modulo total count
//   - __focusID prop on each focusable NodeInst — value is __focusID==<id>
//
// For-loop bodies are walked so loop-contained focusables are collected in
// document order; their IDs are static (the same slot is focused regardless
// of which loop iteration rendered it). Dynamic focus tracking for loop items
// is not yet implemented.
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

// lowerFocusInOwner scans stmts for focusable nodes and injects focus state
// into the owning vars/funcs slices if any focusable nodes are found.
func lowerFocusInOwner(stmts []ir.Stmt, vars *[]*ir.Var, funcs *[]*ir.Func) {
	nodes := collectFocusableNodes(stmts)
	if len(nodes) == 0 {
		return
	}
	n := len(nodes)

	focusVar := &ir.Var{
		Name:        "__focusID",
		Type:        ir.TypInt,
		Init:        intLiteralLit(0),
		Synthesized: true,
	}
	*vars = append(*vars, focusVar)

	focusIdent := func() *ir.Ident {
		return &ir.Ident{Name: "__focusID", Type: ir.TypInt, Sym: focusVar, Synthesized: true}
	}

	// Inject __focusID == id as a prop on each focusable node.
	for id, node := range nodes {
		node.Props = append(node.Props, ir.Arg{
			Name: "__focusID",
			Value: &ir.Binary{
				Type:  ir.TypBool,
				Op:    ast.BinEq,
				Left:  focusIdent(),
				Right: intLiteralLit(id),
			},
		})
	}

	// __focusNext: __focusID = (__focusID + 1) % n
	nextFunc := &ir.Func{
		Name:        "__focusNext",
		Return:      ir.TypVoid,
		Synthesized: true,
		Block: []ir.Stmt{
			&ir.Assign{
				Target: focusIdent(),
				Op:     ast.AssignSet,
				Value: &ir.Binary{
					Type: ir.TypInt,
					Op:   ast.BinMod,
					Left: &ir.Binary{
						Type:  ir.TypInt,
						Op:    ast.BinAdd,
						Left:  focusIdent(),
						Right: intLiteralLit(1),
					},
					Right: intLiteralLit(n),
				},
			},
		},
	}

	// __focusPrev: __focusID = (__focusID + n - 1) % n
	prevFunc := &ir.Func{
		Name:        "__focusPrev",
		Return:      ir.TypVoid,
		Synthesized: true,
		Block: []ir.Stmt{
			&ir.Assign{
				Target: focusIdent(),
				Op:     ast.AssignSet,
				Value: &ir.Binary{
					Type: ir.TypInt,
					Op:   ast.BinMod,
					Left: &ir.Binary{
						Type:  ir.TypInt,
						Op:    ast.BinAdd,
						Left:  focusIdent(),
						Right: intLiteralLit(n - 1),
					},
					Right: intLiteralLit(n),
				},
			},
		},
	}

	*funcs = append(*funcs, nextFunc, prevFunc)
}

// collectFocusableNodes returns NodeInsts with focusable=true in document order.
// The default value of the focusable prop on the component declaration is used
// when the caller does not pass focusable explicitly.
func collectFocusableNodes(stmts []ir.Stmt) []*ir.NodeInst {
	var out []*ir.NodeInst
	scanFocusables(stmts, &out)
	return out
}

func scanFocusables(stmts []ir.Stmt, out *[]*ir.NodeInst) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if nodeEffectiveFocusable(n) {
				*out = append(*out, n)
			}
			scanFocusables(n.Children, out)
		case *ir.For:
			scanFocusables(n.Body, out)
		case *ir.If:
			scanFocusables(n.Body, out)
			scanFocusables(n.Else, out)
		case *ir.Window:
			scanFocusables(n.Body, out)
		case *ir.PlatformFilter:
			scanFocusables(n.Body, out)
		case *ir.SlotInst:
			scanFocusables(n.Children, out)
		case *ir.ErrorBoundary:
			scanFocusables(n.Children, out)
		case *ir.ContextProvider:
			scanFocusables(n.Children, out)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
			// leaf statements, no children
		}
	}
}

// nodeEffectiveFocusable returns true when a NodeInst's effective focusable
// value is true. Checks the explicit prop first; falls back to the component's
// default for that prop.
func nodeEffectiveFocusable(n *ir.NodeInst) bool {
	for _, p := range n.Props {
		if p.Name == "focusable" {
			if lit, ok := p.Value.(*ir.Literal); ok {
				return lit.Raw == "true"
			}
			return true // non-literal truthy value: treat as focusable
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
