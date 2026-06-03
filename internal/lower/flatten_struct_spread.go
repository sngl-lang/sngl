package lower

import (
	"fmt"
	"slices"

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
	rewrite := func(e ir.Expr) ir.Expr { return flattenSpreadExprCtx(e, pkg) }
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return flattenSpreadStmts(stmts, rewrite) },
	})
	return nil
}

// flattenSpreadExprCtx recurses into every sub-expression, then collapses any
// spread-bearing struct literal it produced. Mirrors rewriteUnitExpr's
// traversal so every expr position is covered. pkg is threaded so opaque
// spreads can record the struct types needing a __merge_<Struct> helper.
func flattenSpreadExprCtx(e ir.Expr, pkg *ir.Package) ir.Expr {
	if e == nil {
		return nil
	}
	rewrite := func(x ir.Expr) ir.Expr { return flattenSpreadExprCtx(x, pkg) }
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = rewrite(x.Left)
		x.Right = rewrite(x.Right)
	case *ir.Unary:
		x.Operand = rewrite(x.Operand)
	case *ir.Ternary:
		x.Cond = rewrite(x.Cond)
		x.Then = rewrite(x.Then)
		x.Else = rewrite(x.Else)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewrite(x.Receiver)
		}
		for i := range x.Args {
			x.Args[i].Value = rewrite(x.Args[i].Value)
		}
	case *ir.Conversion:
		x.Operand = rewrite(x.Operand)
	case *ir.Select:
		x.Operand = rewrite(x.Operand)
	case *ir.Index:
		x.Operand = rewrite(x.Operand)
		x.Idx = rewrite(x.Idx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewrite(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewrite(x.Fields[i].Value)
			}
		}
		return flattenStructLitCtx(x, pkg)
	case *ir.Spread:
		x.Operand = rewrite(x.Operand)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = rewrite(x.Entries[i].Key)
			x.Entries[i].Value = rewrite(x.Entries[i].Value)
		}
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, rewrite)
		}
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = rewrite(x.State.Fields[i].Value)
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, rewrite)
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no struct literals beneath.
	default:
		panic(fmt.Sprintf("flattenSpreadExprCtx: unhandled %T", x))
	}
	return e
}

// flattenStructLitCtx collapses a spread-bearing struct literal. When every
// spread operand is a struct literal it folds to a flat literal. When any
// operand is opaque it builds a left-folded __merge_<Struct> call chain and
// records the struct on pkg.MergeStructs. Returns sl unchanged when it has no
// spreads. Field values are assumed already flattened (caller recurses first).
func flattenStructLitCtx(sl *ir.StructLit, pkg *ir.Package) ir.Expr {
	hasSpread, allLiteral := false, true
	for _, f := range sl.Fields {
		if f.Spread {
			hasSpread = true
			if _, ok := f.Value.(*ir.StructLit); !ok {
				allLiteral = false
			}
		}
	}
	if !hasSpread {
		return sl
	}
	if allLiteral {
		return flattenLiteralSpread(sl)
	}
	return buildMergeChain(sl, pkg)
}

// flattenLiteralSpread splices every written field of each literal spread onto
// the target (last write by name wins; first-seen position preserved).
func flattenLiteralSpread(sl *ir.StructLit) *ir.StructLit {
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
				set(g)
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

// buildMergeChain lowers a literal with >=1 opaque spread into nested
// __merge_<Struct> calls. Explicit fields and literal spreads accumulate into
// a struct-literal operand; each opaque spread flushes that operand and merges
// the runtime value. Left-folded → source order preserved, last wins.
func buildMergeChain(sl *ir.StructLit, pkg *ir.Package) ir.Expr {
	sd := structDefOf(sl)
	if sd == nil {
		panic("flatten_struct_spread: cannot resolve struct type for spread merge")
	}
	recordMergeStruct(pkg, sd)
	fn := &ir.Func{Name: "__merge_" + sd.Name, Synthesized: true, Return: sl.Type,
		Params: []*ir.Param{{Name: "base", Type: sl.Type}, {Name: "ov", Type: sl.Type}}}
	mkLit := func(fields []ir.FieldInit) *ir.StructLit {
		cp := append([]ir.FieldInit(nil), fields...)
		return &ir.StructLit{AST: sl.AST, Type: sl.Type, Def: sl.Def, Fields: cp}
	}
	merge := func(a, b ir.Expr) ir.Expr {
		return &ir.Call{Type: sl.Type, Func: fn, Args: []ir.CallArg{{Value: a}, {Value: b}}}
	}
	var acc ir.Expr
	var pending []ir.FieldInit
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if acc == nil {
			acc = mkLit(pending)
		} else {
			acc = merge(acc, mkLit(pending))
		}
		pending = nil
	}
	for _, f := range sl.Fields {
		if f.Spread {
			if inner, ok := f.Value.(*ir.StructLit); ok {
				pending = append(pending, inner.Fields...)
				continue
			}
			flush()
			if acc == nil {
				acc = mkLit(nil)
			}
			acc = merge(acc, f.Value)
			continue
		}
		pending = append(pending, f)
	}
	flush()
	return acc
}

// structDefOf resolves the *StructDef for a struct literal, falling back to the
// type of its first opaque spread operand (the checker guarantees same type).
func structDefOf(sl *ir.StructLit) *ir.StructDef {
	if sl.Def != nil {
		return sl.Def
	}
	if sl.Type != nil {
		if sd, ok := sl.Type.Decl.(*ir.StructDef); ok {
			return sd
		}
	}
	for _, f := range sl.Fields {
		if f.Spread && f.Value != nil {
			if t := exprStructType(f.Value); t != nil {
				if sd, ok := t.Decl.(*ir.StructDef); ok {
					return sd
				}
			}
		}
	}
	return nil
}

// exprStructType returns the static *Type of an expr when it carries one.
func exprStructType(e ir.Expr) *ir.Type {
	switch x := e.(type) {
	case *ir.Ident:
		return x.Type
	case *ir.Select:
		return x.Type
	case *ir.Call:
		return x.Type
	case *ir.Index:
		return x.Type
	}
	return nil
}

func recordMergeStruct(pkg *ir.Package, sd *ir.StructDef) {
	if !slices.Contains(pkg.MergeStructs, sd) {
		pkg.MergeStructs = append(pkg.MergeStructs, sd)
	}
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
