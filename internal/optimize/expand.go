package optimize

import (
	"maps"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// expandForWindows walks the main component body and expands for-loops
// that contain window declarations over const iterables into individual
// window statements. This enables multi-page HTML generation.
func expandForWindows(pkg *ir.Package, ctx *evalCtx) {
	var mainComp *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			mainComp = c
			break
		}
	}
	if mainComp == nil {
		return
	}

	var expanded []ir.Stmt
	for _, s := range mainComp.Body {
		if fs, ok := s.(*ir.For); ok {
			if stmts := expandForStmt(fs, ctx); stmts != nil {
				expanded = append(expanded, stmts...)
				continue
			}
		}
		expanded = append(expanded, s)
	}
	mainComp.Body = expanded
}

// expandForStmt tries to expand a for-loop over a const iterable.
// Returns nil if the iterable can't be evaluated. A successful expansion to
// zero items returns a non-nil empty slice — distinct from "couldn't
// evaluate" — so the caller can drop the for-loop instead of leaving it for
// codegen to choke on.
func expandForStmt(fs *ir.For, ctx *evalCtx) []ir.Stmt {
	val, ok := evalExpr(fs.Iter, ctx)
	if !ok {
		return nil
	}
	items, ok := val.([]any)
	if !ok {
		return nil
	}

	// Find the LoopVar symbols for the for statement's key and value.
	keyVar := findLoopVar(fs, fs.Key)
	valueVar := findLoopVar(fs, fs.Value)

	result := make([]ir.Stmt, 0, len(items))
	for i, item := range items {
		// Create a child context with loop variables bound.
		childCtx := &evalCtx{
			platform:      ctx.platform,
			language:      ctx.language,
			dir:           ctx.dir,
			noCacheBust:   ctx.noCacheBust,
			pkg:           ctx.pkg,
			nativeImports: ctx.nativeImports,
			fileAssets:    ctx.fileAssets,
			values:        make(map[ir.Symbol]any, len(ctx.values)+2),
		}
		maps.Copy(childCtx.values, ctx.values)
		if keyVar != nil {
			childCtx.values[keyVar] = item
		}
		if valueVar != nil {
			childCtx.values[valueVar] = i
		}

		// Clone and optimize the body for this iteration. Using foldStmts so
		// nested const-iterable fors and const-cond ifs unroll/inline too.
		cloned := make([]ir.Stmt, len(fs.Body))
		for i, bodyStmt := range fs.Body {
			cloned[i] = cloneStmt(bodyStmt)
		}
		result = append(result, foldStmts(cloned, childCtx)...)

		// Propagate file assets back.
		ctx.fileAssets = childCtx.fileAssets
	}
	return result
}

// findLoopVar searches the for-loop's body for a LoopVar with the given name.
// The checker creates LoopVar symbols and references them from Ident nodes
// inside the for body.
func findLoopVar(fs *ir.For, name string) *ir.LoopVar {
	if name == "" {
		return nil
	}
	var found *ir.LoopVar
	walkForBody(fs.Body, func(e ir.Expr) {
		if id, ok := e.(*ir.Ident); ok {
			if lv, ok := id.Sym.(*ir.LoopVar); ok && lv.Name == name {
				found = lv
			}
		}
	})
	return found
}

func walkForBody(stmts []ir.Stmt, visit func(ir.Expr)) {
	for _, s := range stmts {
		walkStmtExprs(s, visit)
	}
}

func walkStmtExprs(s ir.Stmt, visit func(ir.Expr)) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, p := range n.Props {
			walkAllExprs(p.Value, visit)
		}
		for _, s := range n.Children {
			walkStmtExprs(s, visit)
		}
	case *ir.If:
		walkAllExprs(n.Cond, visit)
		walkForBody(n.Body, visit)
		walkForBody(n.Else, visit)
	case *ir.For:
		walkAllExprs(n.Iter, visit)
		walkForBody(n.Body, visit)
	case *ir.Assign:
		walkAllExprs(n.Value, visit)
	case *ir.Return:
		walkAllExprs(n.Value, visit)
	case *ir.LocalVar:
		walkAllExprs(n.Init, visit)
	case *ir.CallStmt:
		if n.Call != nil {
			walkAllExprs(n.Call, visit)
		}
	case *ir.Window:
		walkAllExprs(n.Href, visit)
		walkAllExprs(n.Title, visit)
		walkAllExprs(n.Favicon, visit)
		walkForBody(n.Body, visit)
	case *ir.PlatformFilter:
		walkForBody(n.Body, visit)
	case *ir.SlotInst:
		walkForBody(n.Children, visit)
	}
}

func walkAllExprs(e ir.Expr, visit func(ir.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ir.Binary:
		walkAllExprs(x.Left, visit)
		walkAllExprs(x.Right, visit)
	case *ir.Unary:
		walkAllExprs(x.Operand, visit)
	case *ir.Ternary:
		walkAllExprs(x.Cond, visit)
		walkAllExprs(x.Then, visit)
		walkAllExprs(x.Else, visit)
	case *ir.Call:
		walkAllExprs(x.Receiver, visit)
		for _, a := range x.Args {
			walkAllExprs(a.Value, visit)
		}
	case *ir.Conversion:
		walkAllExprs(x.Operand, visit)
	case *ir.Select:
		walkAllExprs(x.Operand, visit)
	case *ir.Index:
		walkAllExprs(x.Operand, visit)
		walkAllExprs(x.Idx, visit)
	case *ir.ListLit:
		for _, el := range x.Elems {
			walkAllExprs(el, visit)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			walkAllExprs(f.Value, visit)
		}
	}
}

// substituteLoopVars replaces LoopVar references in cloned statements
// with literal values. This is used during for-loop expansion.
func substituteLoopVars(stmts []ir.Stmt, ctx *evalCtx) []ir.Stmt {
	_ = slices.Clone(stmts) // ensure we don't mutate the original
	for i, s := range stmts {
		stmts[i] = substituteLoopVarStmt(s, ctx)
	}
	return stmts
}

func substituteLoopVarStmt(s ir.Stmt, ctx *evalCtx) ir.Stmt {
	// The folding pass handles LoopVar substitution via evalIdent,
	// so we just fold the statement.
	return foldStmt(s, ctx)
}
