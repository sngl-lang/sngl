package optimize

import "git.duckfam.us/jonathan/sngl/ast"

// shakeUnused removes consts, data, and functions that are not referenced
// in the remaining AST after compile-time expansion and folding.
// Only runs when there's an App (visual tree) to check references against.
func shakeUnused(doc *ast.Document) {
	if doc.App == nil {
		return // no visual tree — nothing to shake against
	}
	refs := collectRefs(doc)

	// Filter consts — keep only referenced ones
	consts := doc.Consts[:0]
	for _, c := range doc.Consts {
		if refs[c.Name] {
			consts = append(consts, c)
		}
	}
	doc.Consts = consts

	// Filter data — keep referenced, extern, or has events (side effects)
	data := doc.Data[:0]
	for _, d := range doc.Data {
		if refs[d.Name] || d.Extern || len(d.Events) > 0 {
			data = append(data, d)
		}
	}
	doc.Data = data

	// Filter structs — keep only referenced ones
	structs := doc.Structs[:0]
	for _, s := range doc.Structs {
		if refs[s.Name] {
			structs = append(structs, s)
		}
	}
	doc.Structs = structs

	// Filter functions — keep referenced, stdlib, or test
	funcs := doc.Functions[:0]
	for _, fn := range doc.Functions {
		if refs[fn.Name] || fn.IsStdlib || fn.IsTest() {
			funcs = append(funcs, fn)
		}
	}
	doc.Functions = funcs
}

// collectRefs walks the document AST and returns all identifier names
// that are still referenced in expressions.
func collectRefs(doc *ast.Document) map[string]bool {
	refs := map[string]bool{}

	// Walk visual trees
	if doc.App != nil {
		for _, vn := range doc.App.Children {
			walkVNRefs(vn, refs)
		}
		for _, win := range doc.App.Windows {
			for _, vn := range win.Children {
				walkVNRefs(vn, refs)
			}
			walkExprMapRefs(win.Props, refs)
		}
	}

	// Walk data initializers and event bodies
	for _, d := range doc.Data {
		walkExprRefs(d.Init, refs)
		for _, ev := range d.Events {
			walkNodeRefs(ev.Body, refs)
		}
	}

	// Walk function bodies
	for _, fn := range doc.Functions {
		if fn.IsStdlib {
			continue
		}
		walkExprRefs(fn.Body, refs)
		if fn.Block != nil {
			for _, s := range fn.Block.Stmts {
				walkNodeRefs(s, refs)
			}
			if fn.Block.Return != nil {
				walkNodeRefs(fn.Block.Return, refs)
			}
		}
	}

	// Walk component bodies and params
	for _, comp := range doc.Components {
		for _, p := range comp.Params {
			walkExprRefs(p.Default, refs)
		}
		for _, d := range comp.Data {
			walkExprRefs(d.Init, refs)
		}
		for _, fn := range comp.Functions {
			walkExprRefs(fn.Body, refs)
		}
		for _, vn := range comp.Body {
			walkVNRefs(vn, refs)
		}
	}

	// Walk timer bodies
	for _, t := range doc.Timers {
		walkNodeRefs(t.Body, refs)
		refs[t.Active] = true
	}

	return refs
}

// walkVNRefs walks a visual node tree collecting referenced identifiers.
func walkVNRefs(vn *ast.VisualNode, refs map[string]bool) {
	if vn == nil {
		return
	}
	walkExprPtrRefs(vn.If, refs)
	walkExprPtrRefs(vn.Key, refs)
	walkExprPtrRefs(vn.Class, refs)
	walkExprPtrRefs(vn.Ref, refs)
	if vn.For != nil {
		walkExprRefs(vn.For.Iterable, refs)
		for _, child := range vn.For.Else {
			walkVNRefs(child, refs)
		}
	}
	walkExprMapRefs(vn.Props, refs)
	for _, eh := range vn.Events {
		walkExprRefs(eh.Body, refs)
	}
	walkExprMapRefs(vn.Bindings, refs)
	for _, child := range vn.Children {
		walkVNRefs(child, refs)
	}
}

func walkExprRefs(expr ast.Expr, refs map[string]bool) {
	if expr.SNGL != nil {
		walkNodeRefs(expr.SNGL, refs)
	}
}

func walkExprPtrRefs(expr *ast.Expr, refs map[string]bool) {
	if expr != nil {
		walkExprRefs(*expr, refs)
	}
}

func walkExprMapRefs(m map[string]ast.Expr, refs map[string]bool) {
	for _, e := range m {
		walkExprRefs(e, refs)
	}
}

// walkNodeRefs walks an AST node collecting IdentExpr names.
func walkNodeRefs(n ast.Node, refs map[string]bool) {
	if n == nil {
		return
	}
	switch e := n.(type) {
	case *ast.IdentExpr:
		refs[e.Name] = true
	case *ast.BinaryExpr:
		walkNodeRefs(e.Left, refs)
		walkNodeRefs(e.Right, refs)
	case *ast.UnaryExpr:
		walkNodeRefs(e.Operand, refs)
	case *ast.TernaryExpr:
		walkNodeRefs(e.Cond, refs)
		walkNodeRefs(e.Then, refs)
		walkNodeRefs(e.Else, refs)
	case *ast.CallExpr:
		refs[e.Func] = true
		for _, arg := range e.Args {
			walkNodeRefs(arg, refs)
		}
	case *ast.MethodExpr:
		walkNodeRefs(e.Receiver, refs)
		for _, arg := range e.Args {
			walkNodeRefs(arg, refs)
		}
	case *ast.SelectExpr:
		walkNodeRefs(e.Operand, refs)
	case *ast.IndexExpr:
		walkNodeRefs(e.Operand, refs)
		walkNodeRefs(e.Index, refs)
	case *ast.InterpolationExpr:
		for _, p := range e.Parts {
			walkNodeRefs(p, refs)
		}
	case *ast.ListExpr:
		for _, el := range e.Elements {
			walkNodeRefs(el, refs)
		}
	case *ast.StructExpr:
		if e.Name != "" {
			refs[e.Name] = true
		}
		for _, f := range e.Fields {
			walkNodeRefs(f.Value, refs)
		}
	case *ast.LambdaExpr:
		walkNodeRefs(e.Body, refs)
	case *ast.ParenExpr:
		walkNodeRefs(e.Inner, refs)
	case *ast.AssignStmt:
		walkNodeRefs(e.Target, refs)
		walkNodeRefs(e.Value, refs)
	case *ast.ToggleStmt:
		walkNodeRefs(e.Target, refs)
	case *ast.EmitStmt:
		for _, arg := range e.Args {
			walkNodeRefs(arg, refs)
		}
	case *ast.CallStmt:
		if e.Call != nil {
			walkNodeRefs(e.Call, refs)
		}
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			walkNodeRefs(s, refs)
		}
	case *ast.ReturnStmt:
		walkNodeRefs(e.Value, refs)
	case *ast.VarStmt:
		walkNodeRefs(e.Init, refs)
	}
}
