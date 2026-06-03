package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passFlattenStructSpread = pass{
	name:    "NoStructSpread",
	enabled: func(c Caps) bool { return c.NoStructSpread },
	apply:   lowerFlattenStructSpread,
}

// lowerFlattenStructSpread rewrites struct literals that contain `...literal`
// spread fields into flat literals (last-write-wins). Spreads whose operand is
// not itself a struct literal (opaque runtime values) are left intact for a
// later runtime-merge pass; today's backends handle those as they do now.
func lowerFlattenStructSpread(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		expr:  flattenSpreadExpr,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return flattenSpreadStmts(stmts, flattenSpreadExpr) },
	})
	return nil
}

// flattenSpreadExpr recurses into every sub-expression, then collapses any
// spread-bearing struct literal it produced. Mirrors rewriteUnitExpr's
// traversal so every expr position is covered.
func flattenSpreadExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = flattenSpreadExpr(x.Left)
		x.Right = flattenSpreadExpr(x.Right)
	case *ir.Unary:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Ternary:
		x.Cond = flattenSpreadExpr(x.Cond)
		x.Then = flattenSpreadExpr(x.Then)
		x.Else = flattenSpreadExpr(x.Else)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = flattenSpreadExpr(x.Receiver)
		}
		for i := range x.Args {
			x.Args[i].Value = flattenSpreadExpr(x.Args[i].Value)
		}
	case *ir.Conversion:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Select:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Index:
		x.Operand = flattenSpreadExpr(x.Operand)
		x.Idx = flattenSpreadExpr(x.Idx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = flattenSpreadExpr(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = flattenSpreadExpr(x.Fields[i].Value)
			}
		}
		return flattenStructLit(x)
	case *ir.Spread:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = flattenSpreadExpr(x.Entries[i].Key)
			x.Entries[i].Value = flattenSpreadExpr(x.Entries[i].Value)
		}
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, flattenSpreadExpr)
		}
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = flattenSpreadExpr(x.State.Fields[i].Value)
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, flattenSpreadExpr)
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no struct literals beneath.
	default:
		panic(fmt.Sprintf("flattenSpreadExpr: unhandled %T", x))
	}
	return e
}

// flattenStructLit collapses a spread-bearing struct literal into a flat one
// when every spread operand is itself a struct literal. Returns sl unchanged
// when it has no spreads or any spread operand is opaque. Field values are
// assumed already flattened (flattenSpreadExpr recurses first). Last write by
// name wins; first-seen position is preserved.
func flattenStructLit(sl *ir.StructLit) *ir.StructLit {
	hasSpread, allLiteral := false, true
	for _, f := range sl.Fields {
		if f.Spread {
			hasSpread = true
			if _, ok := f.Value.(*ir.StructLit); !ok {
				allLiteral = false
			}
		}
	}
	if !hasSpread || !allLiteral {
		return sl
	}
	var order []string
	vals := map[string]ir.FieldInit{}
	set := func(fi ir.FieldInit) {
		if _, seen := vals[fi.Name]; !seen {
			order = append(order, fi.Name)
		}
		vals[fi.Name] = fi
	}
	for _, f := range sl.Fields {
		if f.Spread {
			for _, g := range f.Value.(*ir.StructLit).Fields {
				set(g) // literal spread splices every written field
			}
			continue
		}
		set(f)
	}
	flat := make([]ir.FieldInit, 0, len(order))
	for _, name := range order {
		flat = append(flat, vals[name])
	}
	out := *sl
	out.Fields = flat
	return &out
}

func flattenSpreadStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = flattenSpreadStmts(n.Body, rewrite)
			n.Else = flattenSpreadStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = flattenSpreadStmts(n.Body, rewrite)
			n.Else = flattenSpreadStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = flattenSpreadStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = flattenSpreadStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = flattenSpreadStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = flattenSpreadStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = flattenSpreadStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = flattenSpreadStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = flattenSpreadStmts(n.Body, rewrite)
		case *ir.Toggle:
			n.Target = rewrite(n.Target)
		case *ir.ContextProvider:
			n.Value = rewrite(n.Value)
			n.Children = flattenSpreadStmts(n.Children, rewrite)
		default:
			panic(fmt.Sprintf("flattenSpreadStmts: unhandled %T", n))
		}
	}
	return stmts
}
