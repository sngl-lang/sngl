package lsp

import "git.duckfam.us/jonathan/sngl/ir"

// walkIRColorLiterals invokes fn for every *ir.StructLit whose Def is the
// "color" stdlib struct, reachable from package-level consts/vars,
// components, windows, and funcs. Skips imported packages — only the root
// package's literals matter for documentColor.
func walkIRColorLiterals(pkg *ir.Package, fn func(*ir.StructLit)) {
	if pkg == nil {
		return
	}
	w := &irLitWalker{fn: fn}
	for _, v := range pkg.Consts {
		w.expr(v.Init)
	}
	for _, v := range pkg.Vars {
		w.expr(v.Init)
	}
	for _, f := range pkg.Funcs {
		w.fn_(f)
	}
	for _, c := range pkg.Components {
		w.component(c)
	}
	for _, s := range pkg.Body {
		w.stmt(s)
	}
}

type irLitWalker struct {
	fn func(*ir.StructLit)
}

func (w *irLitWalker) component(c *ir.Component) {
	if c == nil {
		return
	}
	for _, p := range c.Props {
		w.expr(p.Default)
	}
	for _, v := range c.Vars {
		w.expr(v.Init)
	}
	for _, f := range c.Funcs {
		w.fn_(f)
	}
	w.stmts(c.Body)
}

func (w *irLitWalker) fn_(f *ir.Func) {
	if f == nil {
		return
	}
	for _, p := range f.Params {
		if p != nil {
			w.expr(p.Default)
		}
	}
	w.stmts(f.Block)
}

func (w *irLitWalker) stmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.stmt(s)
	}
}

func (w *irLitWalker) stmt(s ir.Stmt) {
	switch x := s.(type) {
	case nil:
		return
	case *ir.NodeInst:
		for _, a := range x.Props {
			w.expr(a.Value)
		}
		for _, h := range x.Handlers {
			w.fn_(h.Func)
		}
		w.expr(x.Key)
		w.expr(x.Ref)
		w.stmts(x.Children)
	case *ir.CallStmt:
		if x.Call != nil {
			w.expr(x.Call)
		}
	case *ir.SlotInst:
		w.stmts(x.Children)
	case *ir.ErrorBoundary:
		if x.Handler != nil {
			w.fn_(x.Handler.Func)
		}
		w.stmts(x.Children)
	case *ir.Assign:
		w.expr(x.Target)
		w.expr(x.Value)
	case *ir.Toggle:
		w.expr(x.Target)
	case *ir.Emit:
		for _, a := range x.Args {
			w.expr(a.Value)
		}
	case *ir.LocalVar:
		w.expr(x.Init)
	case *ir.Return:
		w.expr(x.Value)
	case *ir.If:
		w.expr(x.Cond)
		w.stmts(x.Body)
		w.stmts(x.Else)
	case *ir.For:
		w.expr(x.Iter)
		w.stmts(x.Body)
		w.stmts(x.Else)
	}
}

func (w *irLitWalker) expr(e ir.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Literal:
		// leaf — no color shape lives here post-T3.
	case *ir.Ident:
		// leaf
	case *ir.Binary:
		w.expr(x.Left)
		w.expr(x.Right)
	case *ir.Unary:
		w.expr(x.Operand)
	case *ir.Ternary:
		w.expr(x.Cond)
		w.expr(x.Then)
		w.expr(x.Else)
	case *ir.Call:
		w.expr(x.Callee)
		w.expr(x.Receiver)
		for _, a := range x.Args {
			w.expr(a.Value)
		}
	case *ir.Conversion:
		w.expr(x.Operand)
	case *ir.Select:
		w.expr(x.Operand)
	case *ir.Index:
		w.expr(x.Operand)
		w.expr(x.Idx)
	case *ir.StructLit:
		if x.Def != nil && x.Def.Name == "color" && x.AST != nil {
			w.fn(x)
		}
		for _, f := range x.Fields {
			w.expr(f.Value)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			w.expr(el)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			w.expr(kv.Key)
			w.expr(kv.Value)
		}
	case *ir.Spread:
		w.expr(x.Operand)
	case *ir.Lambda:
		w.fn_(x.Func)
	}
}
