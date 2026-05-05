package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passEnum = pass{
	name:    "NoEnum",
	enabled: func(c Caps) bool { return c.NoEnum },
	apply:   lowerEnum,
}

// lowerEnum rewrites enum-member references to int literals (ordinal in
// declaration order). Does not delete enum declarations from pkg.Enums —
// that is DCE's job.
func lowerEnum(pkg *ir.Package, _ Caps) error {
	if pkg == nil {
		return nil
	}

	ordinals := make(map[*ir.EnumDef]map[string]int)
	for _, e := range pkg.Enums {
		m := make(map[string]int, len(e.Members))
		for i, mem := range e.Members {
			m[mem.Name] = i
		}
		ordinals[e] = m
	}

	memberOrdinal := func(decl *ir.EnumDef, name string) (int, bool) {
		if m, ok := ordinals[decl]; ok {
			if ord, ok := m[name]; ok {
				return ord, true
			}
		}
		m := make(map[string]int, len(decl.Members))
		for i, mem := range decl.Members {
			m[mem.Name] = i
		}
		ordinals[decl] = m
		ord, ok := m[name]
		return ord, ok
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return rewriteEnumExpr(e, memberOrdinal)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteEnumStmts(stmts, rewrite) },
	})
	return nil
}

func rewriteEnumExpr(e ir.Expr, memberOrdinal func(*ir.EnumDef, string) (int, bool)) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Ident:
		// Bare-member shorthand resolved against context enum.
		if x.Member != "" && x.Type != nil && x.Type.Kind == ir.TypeEnum && x.Type.Decl != nil {
			if decl, ok := x.Type.Decl.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Member); ok {
					return intLiteral(ord)
				}
			}
		}
	case *ir.Select:
		// Qualified access: Status.active. Operand is the enum decl Ident.
		if id, ok := x.Operand.(*ir.Ident); ok {
			if decl, ok := id.Sym.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Field); ok {
					return intLiteral(ord)
				}
			}
		}
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Binary:
		x.Left = rewriteEnumExpr(x.Left, memberOrdinal)
		x.Right = rewriteEnumExpr(x.Right, memberOrdinal)
	case *ir.Unary:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Ternary:
		x.Cond = rewriteEnumExpr(x.Cond, memberOrdinal)
		x.Then = rewriteEnumExpr(x.Then, memberOrdinal)
		x.Else = rewriteEnumExpr(x.Else, memberOrdinal)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteEnumExpr(x.Receiver, memberOrdinal)
		}
		for i := range x.Args {
			x.Args[i].Value = rewriteEnumExpr(x.Args[i].Value, memberOrdinal)
		}
	case *ir.Conversion:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Index:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
		x.Idx = rewriteEnumExpr(x.Idx, memberOrdinal)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteEnumExpr(x.Elems[i], memberOrdinal)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewriteEnumExpr(x.Fields[i].Value, memberOrdinal)
			}
		}
	case *ir.Spread:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	}
	return e
}

func rewriteEnumStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
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
			n.Body = rewriteEnumStmts(n.Body, rewrite)
			n.Else = rewriteEnumStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = rewriteEnumStmts(n.Body, rewrite)
			n.Else = rewriteEnumStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = rewriteEnumStmts(n.Body, rewrite)
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
			n.Children = rewriteEnumStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteEnumStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteEnumStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteEnumStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteEnumStmts(n.Handler.Func.Block, rewrite)
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
			n.Body = rewriteEnumStmts(n.Body, rewrite)
		}
	}
	return stmts
}

func intLiteral(n int) *ir.Literal {
	return &ir.Literal{
		Type: ir.TypInt,
		Raw:  strconv.Itoa(n),
	}
}
