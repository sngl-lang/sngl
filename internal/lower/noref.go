package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passNoRef = pass{
	name:    "NoRef",
	enabled: func(c Caps) bool { return c.NoRef },
	apply:   lowerNoRef,
}

// lowerNoRef boxes every addressed binding into a synthesized one-field
// reference-semantic struct. After this pass no *ir.TypeRef remains.
//
// Phase C — body lands in subsequent tasks.
func lowerNoRef(pkg *ir.Package, _ Caps) error {
	return nil
}

// seedAddressedVars walks pkg recording every *ir.Var whose address is
// taken by *ir.Unary{UnaryAddr}. Idempotent — entries set by NoLambda's
// lifter persist; this pass adds any Vars addressed by hand-written code.
func seedAddressedVars(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	if pkg.AddressedVars == nil {
		pkg.AddressedVars = map[*ir.Var]bool{}
	}
	walkPackage(pkg, walkFuncs{
		expr: func(e ir.Expr) ir.Expr {
			seedAddressedVarsInExpr(e, pkg.AddressedVars)
			return e
		},
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			for _, s := range stmts {
				seedAddressedVarsInStmt(s, pkg.AddressedVars)
			}
			return stmts
		},
	})
}

func seedAddressedVarsInExpr(e ir.Expr, set map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Unary:
		if x.Op == ast.UnaryAddr {
			if leaf := addressLeafVar(x.Operand); leaf != nil {
				set[leaf] = true
			}
		}
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Binary:
		seedAddressedVarsInExpr(x.Left, set)
		seedAddressedVarsInExpr(x.Right, set)
	case *ir.Ternary:
		seedAddressedVarsInExpr(x.Cond, set)
		seedAddressedVarsInExpr(x.Then, set)
		seedAddressedVarsInExpr(x.Else, set)
	case *ir.Call:
		seedAddressedVarsInExpr(x.Receiver, set)
		for i := range x.Args {
			seedAddressedVarsInExpr(x.Args[i].Value, set)
		}
	case *ir.Conversion:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Select:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Index:
		seedAddressedVarsInExpr(x.Operand, set)
		seedAddressedVarsInExpr(x.Idx, set)
	case *ir.ListLit:
		for _, el := range x.Elems {
			seedAddressedVarsInExpr(el, set)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			seedAddressedVarsInExpr(x.Fields[i].Value, set)
		}
	case *ir.Spread:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				seedAddressedVarsInExpr(x.State.Fields[i].Value, set)
			}
		}
		if x.Func != nil {
			for _, s := range x.Func.Block {
				seedAddressedVarsInStmt(s, set)
			}
		}
	}
}

func seedAddressedVarsInStmt(s ir.Stmt, set map[*ir.Var]bool) {
	switch n := s.(type) {
	case *ir.Assign:
		seedAddressedVarsInExpr(n.Target, set)
		seedAddressedVarsInExpr(n.Value, set)
	case *ir.LocalVar:
		seedAddressedVarsInExpr(n.Init, set)
	case *ir.Return:
		seedAddressedVarsInExpr(n.Value, set)
	case *ir.If:
		seedAddressedVarsInExpr(n.Cond, set)
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
		for _, t := range n.Else {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.For:
		seedAddressedVarsInExpr(n.Iter, set)
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
		for _, t := range n.Else {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.PlatformFilter:
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.NodeInst:
		for i := range n.Props {
			seedAddressedVarsInExpr(n.Props[i].Value, set)
		}
		seedAddressedVarsInExpr(n.Key, set)
		seedAddressedVarsInExpr(n.Ref, set)
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				for _, t := range n.Handlers[i].Func.Block {
					seedAddressedVarsInStmt(t, set)
				}
			}
		}
	case *ir.SlotInst:
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.ErrorBoundary:
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
		if n.Handler != nil && n.Handler.Func != nil {
			for _, t := range n.Handler.Func.Block {
				seedAddressedVarsInStmt(t, set)
			}
		}
	case *ir.Emit:
		for i := range n.Args {
			seedAddressedVarsInExpr(n.Args[i].Value, set)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			seedAddressedVarsInExpr(n.Call, set)
		}
	case *ir.Toggle:
		seedAddressedVarsInExpr(n.Target, set)
	}
}

// addressLeafVar peels Select/Index off operand and returns the leaf *ir.Var
// (or nil if the chain doesn't terminate at one).
func addressLeafVar(e ir.Expr) *ir.Var {
	for {
		switch x := e.(type) {
		case *ir.Ident:
			if v, ok := x.Sym.(*ir.Var); ok {
				return v
			}
			return nil
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		default:
			return nil
		}
	}
}
