package lower

import (
	"fmt"
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
func lowerUnit(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	rewrite := func(e ir.Expr) ir.Expr { return rewriteUnitExpr(e) }
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteStmtExprs(stmts, rewrite) },
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
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = rewriteUnitExpr(x.Entries[i].Key)
			x.Entries[i].Value = rewriteUnitExpr(x.Entries[i].Value)
		}
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = rewriteStmtExprs(x.Func.Block, rewriteUnitExpr)
		}
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = rewriteUnitExpr(x.State.Fields[i].Value)
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = rewriteStmtExprs(x.Func.Block, rewriteUnitExpr)
		}
	case *ir.Ident, *ir.ContextRead:
		// Terminal — no unit-typed sub-expressions to rewrite.
	default:
		panic(fmt.Sprintf("rewriteUnitExpr: unhandled %T", x))
	}
	return e
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
