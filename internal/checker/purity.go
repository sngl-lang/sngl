package checker

import "git.duckfam.us/jonathan/sngl/ast"

// analyzePurity determines the purity level of a function by walking its body.
func analyzePurity(f *Func, vars map[string]*Var) Purity {
	w := &purityWalker{vars: vars}
	if f.Body != nil {
		w.walkExpr(f.Body)
	}
	if f.ASTBlock != nil {
		w.walkBlock(f.ASTBlock)
	}
	if w.mutates {
		return PurityMutates
	}
	if w.readsVar {
		return PurityReadonly
	}
	return PurityPure
}

// trackAccess populates Func.Reads/Writes and Var.Refs by walking the function body.
func trackAccess(f *Func, vars map[string]*Var) {
	w := &accessWalker{vars: vars, reads: make(map[string]bool), writes: make(map[string]bool)}
	if f.Body != nil {
		w.walkExpr(f.Body)
	}
	if f.ASTBlock != nil {
		w.walkBlock(f.ASTBlock)
	}
	for name := range w.reads {
		if v, ok := vars[name]; ok {
			f.Reads = append(f.Reads, v)
		}
	}
	for name := range w.writes {
		if v, ok := vars[name]; ok {
			f.Writes = append(f.Writes, v)
		}
	}
}

// --- purity walker ---

type purityWalker struct {
	vars     map[string]*Var
	readsVar bool
	mutates  bool
}

func (w *purityWalker) walkExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.IdentExpr:
		if _, ok := w.vars[x.Name]; ok {
			w.readsVar = true
		}
	case *ast.BinaryExpr:
		w.walkExpr(x.Left)
		w.walkExpr(x.Right)
	case *ast.UnaryExpr:
		w.walkExpr(x.Operand)
	case *ast.TernaryExpr:
		w.walkExpr(x.Cond)
		w.walkExpr(x.Then)
		w.walkExpr(x.Else)
	case *ast.CallExpr:
		w.walkExpr(x.Func)
		w.walkArgList(x.Args)
	case *ast.SelectExpr:
		w.walkExpr(x.Operand)
	case *ast.IndexExpr:
		w.walkExpr(x.Operand)
		w.walkExpr(x.Index)
	case *ast.StructExpr:
		for _, f := range x.Fields {
			w.walkExpr(f.Value)
		}
	case *ast.ListExpr:
		for _, el := range x.Elements {
			w.walkExpr(el)
		}
	case *ast.InterpolationExpr:
		for _, part := range x.Parts {
			w.walkExpr(part)
		}
	case *ast.LambdaExpr:
		w.walkExpr(x.Body)
		w.walkBlock(&x.Block)
	case *ast.SpreadExpr:
		w.walkExpr(x.Operand)
	case *ast.ParenExpr:
		w.walkExpr(x.Inner)
	}
}

func (w *purityWalker) walkBlock(block *ast.StmtBlock) {
	if block == nil || !block.IsDefined() {
		return
	}
	for _, s := range block.Stmts {
		w.walkStmt(s)
	}
}

func (w *purityWalker) walkStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.AssignStmt:
		w.mutates = true
		w.walkExpr(x.Value)
	case *ast.ToggleStmt:
		w.mutates = true
	case *ast.EmitStmt:
		w.mutates = true
		w.walkArgList(x.Args)
	case *ast.VarStmt:
		if x.Init != nil {
			w.walkExpr(x.Init)
		}
	case *ast.ReturnStmt:
		if x.Value != nil {
			w.walkExpr(x.Value)
		}
	case *ast.CallStmt:
		w.walkExpr(x.Call)
	case *ast.IfStmt:
		w.walkExpr(x.Cond)
		w.walkBlock(&x.Body)
		w.walkBlock(&x.Else)
	case *ast.ForStmt:
		w.walkExpr(x.Iter)
		w.walkBlock(&x.Body)
		w.walkBlock(&x.Else)
	case *ast.PlatformStmt:
		w.walkBlock(&x.Body)
	case *ast.VisualNode:
		w.walkArgList(x.Args)
		w.walkBlock(&x.Block)
	}
}

func (w *purityWalker) walkArgList(args ast.ArgList) {
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			w.walkExpr(arg.Value)
		case ast.EventHandler:
			w.walkBlock(&arg.Body)
		}
	}
}

// --- access walker ---

type accessWalker struct {
	vars   map[string]*Var
	reads  map[string]bool
	writes map[string]bool
}

func (w *accessWalker) walkExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.IdentExpr:
		if _, ok := w.vars[x.Name]; ok {
			w.reads[x.Name] = true
		}
	case *ast.BinaryExpr:
		w.walkExpr(x.Left)
		w.walkExpr(x.Right)
	case *ast.UnaryExpr:
		w.walkExpr(x.Operand)
	case *ast.TernaryExpr:
		w.walkExpr(x.Cond)
		w.walkExpr(x.Then)
		w.walkExpr(x.Else)
	case *ast.CallExpr:
		w.walkExpr(x.Func)
		w.walkArgList(x.Args)
	case *ast.SelectExpr:
		w.walkExpr(x.Operand)
	case *ast.IndexExpr:
		w.walkExpr(x.Operand)
		w.walkExpr(x.Index)
	case *ast.StructExpr:
		for _, f := range x.Fields {
			w.walkExpr(f.Value)
		}
	case *ast.ListExpr:
		for _, el := range x.Elements {
			w.walkExpr(el)
		}
	case *ast.InterpolationExpr:
		for _, part := range x.Parts {
			w.walkExpr(part)
		}
	case *ast.LambdaExpr:
		w.walkExpr(x.Body)
		w.walkBlock(&x.Block)
	case *ast.SpreadExpr:
		w.walkExpr(x.Operand)
	case *ast.ParenExpr:
		w.walkExpr(x.Inner)
	}
}

func (w *accessWalker) walkBlock(block *ast.StmtBlock) {
	if block == nil || !block.IsDefined() {
		return
	}
	for _, s := range block.Stmts {
		w.walkStmt(s)
	}
}

func (w *accessWalker) walkStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.AssignStmt:
		// Target is a write.
		if ident, ok := x.Target.(*ast.IdentExpr); ok {
			if _, ok := w.vars[ident.Name]; ok {
				w.writes[ident.Name] = true
			}
		}
		w.walkExpr(x.Value)
	case *ast.ToggleStmt:
		if ident, ok := x.Target.(*ast.IdentExpr); ok {
			if _, ok := w.vars[ident.Name]; ok {
				w.writes[ident.Name] = true
			}
		}
	case *ast.EmitStmt:
		w.walkArgList(x.Args)
	case *ast.VarStmt:
		if x.Init != nil {
			w.walkExpr(x.Init)
		}
	case *ast.ReturnStmt:
		if x.Value != nil {
			w.walkExpr(x.Value)
		}
	case *ast.CallStmt:
		w.walkExpr(x.Call)
	case *ast.IfStmt:
		w.walkExpr(x.Cond)
		w.walkBlock(&x.Body)
		w.walkBlock(&x.Else)
	case *ast.ForStmt:
		w.walkExpr(x.Iter)
		w.walkBlock(&x.Body)
		w.walkBlock(&x.Else)
	case *ast.PlatformStmt:
		w.walkBlock(&x.Body)
	case *ast.VisualNode:
		w.walkArgList(x.Args)
		w.walkBlock(&x.Block)
	}
}

func (w *accessWalker) walkArgList(args ast.ArgList) {
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			w.walkExpr(arg.Value)
		case ast.EventHandler:
			w.walkBlock(&arg.Body)
		}
	}
}
