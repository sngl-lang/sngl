package lower

import (
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passUnit = pass{
	name:    "NoUnit",
	enabled: func(c Caps) bool { return c.NoUnit },
	apply:   lowerUnit,
}

// lowerUnit rewrites every unit-typed Literal to an int literal scaled by
// its suffix Factor. Identifiers and computed expressions retain their
// original Type *Type values; downstream consumers should treat the
// rewritten Literal's TypInt as authoritative.
func lowerUnit(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	rewrite := func(e ir.Expr) ir.Expr { return rewriteUnitExpr(e) }
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteUnitStmts(stmts, rewrite) },
	})
	return nil
}

func rewriteUnitExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type != nil && x.Type.Kind == ir.TypeUnit {
			if scaled, ok := scaleUnitLiteral(x); ok {
				return scaled
			}
		}
	case *ir.Binary:
		x.Left = rewriteUnitExpr(x.Left)
		x.Right = rewriteUnitExpr(x.Right)
	case *ir.Unary:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Ternary:
		x.Cond = rewriteUnitExpr(x.Cond)
		x.Then = rewriteUnitExpr(x.Then)
		x.Else = rewriteUnitExpr(x.Else)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteUnitExpr(x.Receiver)
		}
		for i := range x.Args {
			x.Args[i].Value = rewriteUnitExpr(x.Args[i].Value)
		}
	case *ir.Conversion:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Select:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Index:
		x.Operand = rewriteUnitExpr(x.Operand)
		x.Idx = rewriteUnitExpr(x.Idx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteUnitExpr(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewriteUnitExpr(x.Fields[i].Value)
			}
		}
	case *ir.Spread:
		x.Operand = rewriteUnitExpr(x.Operand)
	}
	return e
}

func rewriteUnitStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
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
			n.Body = rewriteUnitStmts(n.Body, rewrite)
			n.Else = rewriteUnitStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = rewriteUnitStmts(n.Body, rewrite)
			n.Else = rewriteUnitStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = rewriteUnitStmts(n.Body, rewrite)
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
			n.Children = rewriteUnitStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteUnitStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteUnitStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteUnitStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteUnitStmts(n.Handler.Func.Block, rewrite)
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
			n.Body = rewriteUnitStmts(n.Body, rewrite)
		}
	}
	return stmts
}

// scaleUnitLiteral multiplies lit's numeric Raw by its suffix's Factor,
// returning a new int (or float) Literal with TypInt / TypFloat. Returns
// (nil, false) when the unit decl can't be resolved or the value can't be
// parsed.
func scaleUnitLiteral(lit *ir.Literal) (*ir.Literal, bool) {
	if lit.Type == nil || lit.Type.Decl == nil {
		return nil, false
	}
	decl, ok := lit.Type.Decl.(*ir.UnitDef)
	if !ok {
		return nil, false
	}
	factor := 1.0
	if lit.Suffix != "" {
		found := false
		for _, s := range decl.Suffixes {
			if s.Name == lit.Suffix {
				factor = s.Factor
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}

	raw := strings.TrimSuffix(lit.Raw, lit.Suffix)

	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		scaled := float64(i) * factor
		if scaled == float64(int64(scaled)) {
			return &ir.Literal{
				Type: ir.TypInt,
				Raw:  strconv.FormatInt(int64(scaled), 10),
			}, true
		}
		return &ir.Literal{
			Type: ir.TypFloat,
			Raw:  strconv.FormatFloat(scaled, 'g', -1, 64),
		}, true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		scaled := f * factor
		return &ir.Literal{
			Type: ir.TypFloat,
			Raw:  strconv.FormatFloat(scaled, 'g', -1, 64),
		}, true
	}
	return nil, false
}
