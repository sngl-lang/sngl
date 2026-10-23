package checker

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// ownedMark is a #[owned] whose release is judged once every body is checked:
// a `=>` release has no return type until its body is read.
type ownedMark struct {
	pos ast.Pos
	def *ir.StructDef
}

// markOwned implements #[owned(release)]: a value of the struct is the
// program's to give back, and release is how.
func markOwned(m *mark) error {
	sd, ok := m.sym.(*ir.StructDef)
	if !ok {
		return fmt.Errorf("#[owned] cannot mark %s; only a struct is a type a value of which is owned", ast.DeclFormName(m.decl))
	}
	e := m.args.Expr("release")
	sym, name, ok := m.c.lookupMarkRef(e)
	switch {
	case !ok && name == "":
		return fmt.Errorf("#[owned] names its release, a function, by name")
	case !ok:
		return fmt.Errorf("undefined: %s", name)
	}
	fn, ok := sym.(*ir.Func)
	if !ok {
		return fmt.Errorf("#[owned]: %s is not a function; the release is the function that gives a %s back", name, sd.Name)
	}
	sd.Release = fn
	m.c.ownedMarks = append(m.c.ownedMarks, ownedMark{pos: m.attr.Pos, def: sd})
	return nil
}

// lookupMarkRef resolves a declaration a mark's argument names, bare or
// through an import's alias.
func (c *checker) lookupMarkRef(e ast.Expr) (ir.Symbol, string, bool) {
	switch x := e.(type) {
	case *ast.IdentExpr:
		sym, ok := c.scope.Lookup(x.Name)
		return sym, x.Name, ok
	case *ast.SelectExpr:
		id, ok := x.Operand.(*ast.IdentExpr)
		if !ok {
			break
		}
		name := id.Name + "." + x.Field
		nsSym, ok := c.scope.Lookup(id.Name)
		ns, isNS := nsSym.(*ir.Namespace)
		if !ok || !isNS || ns.Pkg == nil {
			return nil, name, false
		}
		sym, ok := ns.Pkg.Symbols.LookupMember(x.Field)
		return sym, name, ok
	}
	return nil, "", false
}

// checkOwnedReleases refuses a release that is not a func(T) of its own type
// returning nothing.
func (c *checker) checkOwnedReleases() {
	for _, om := range c.ownedMarks {
		fn := om.def.Release
		ok := len(fn.Params) == 1 && fn.Params[0].Type != nil && fn.Params[0].Type.Decl == om.def &&
			(fn.Return == nil || fn.Return.Kind == ir.TypeVoid)
		if !ok {
			c.error(om.pos, "#[owned]: release %s must be func(%s), taking the value it gives back and answering nothing; it is %s", fn.Name, om.def.Name, fn.SymType())
		}
	}
	c.ownedMarks = nil
}

// funcParam is what a mark on a declared function's parameter is handed.
type funcParam struct {
	param  *ir.Param
	bodied bool
}

func markRetains(m *mark) error { return markTransfer(m, ir.TransferRetains, "retains") }

func markMoves(m *mark) error { return markTransfer(m, ir.TransferMoves, "moves") }

// markTransfer implements #[retains] and #[moves]: what a native does with
// an argument once the call returns. A function with a body says what it does
// with one itself, so only a bodyless declaration needs telling.
func markTransfer(m *mark, t ir.Transfer, name string) error {
	fp, ok := m.sym.(funcParam)
	if !ok || fp.bodied {
		what := "a parameter of a function with a body, which says what it does with its argument itself"
		if !ok {
			what = ast.DeclFormName(m.decl)
		}
		return fmt.Errorf("#[%s] cannot mark %s; only a parameter of a function with no body", name, what)
	}
	switch fp.param.Transfer {
	case ir.TransferBorrow:
		fp.param.Transfer = t
		return nil
	case t:
		return fmt.Errorf("parameter %q is already marked %s", fp.param.Name, name)
	}
	return fmt.Errorf("parameter %q is marked both retains and moves; a native either keeps a pointer the caller still owns or takes the value", fp.param.Name)
}

// funcParamMarks are the marks a declared function's parameter may carry.
var funcParamMarks = map[markKey]bool{
	{"macro", "retains"}: true,
	{"macro", "moves"}:   true,
}

// applyFuncParamMarks runs the marks written on a declared function's
// parameters.
func (c *checker) applyFuncParamMarks(f *ast.FuncDef, fn *ir.Func) {
	bodied := funcHasBody(f)
	// A bare component func has its receiver prepended.
	skip := len(fn.Params) - len(f.Params.Params)
	for i, p := range f.Params.Params {
		if len(p.MacroAttrs()) == 0 || skip < 0 {
			continue
		}
		marked, fp := p, funcParam{param: fn.Params[skip+i], bodied: bodied}
		c.afterMarks(func() {
			for _, attr := range marked.MacroAttrs() {
				c.applyMark(attr, marked, fp, funcParamMarks)
			}
		})
	}
}

// checkOwnedDerived refuses a component's owned derived value that reads
// something no lifetime can be keyed on. Such a value is released and
// recomputed when what it reads changes, so each read is part of its key.
func (c *checker) checkOwnedDerived() {
	for _, comp := range c.pkg.Components {
		for _, fn := range comp.Funcs {
			if !ir.Owned(fn.Return) || !isDerived(fn) {
				continue
			}
			c.reportUnkeyableReads(fn)
		}
	}
}

// isDerived reports whether fn is a derived value: a function of no
// arguments, its component's receiver aside.
func isDerived(fn *ir.Func) bool {
	for _, p := range fn.Params {
		if !p.Receiver {
			return false
		}
	}
	return true
}

func (c *checker) reportUnkeyableReads(fn *ir.Func) {
	own := map[ir.Symbol]bool{}
	for _, p := range fn.Params {
		own[p] = true
	}
	reported := map[ir.Symbol]bool{}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.LocalVar:
			if x.Sym != nil {
				own[x.Sym] = true
			}
		case *ir.Lambda:
			for _, p := range x.Func.Params {
				own[p] = true
			}
		case *ir.Ident:
			if x.AST == nil || own[x.Sym] || reported[x.Sym] || !isStateRead(x.Sym) {
				return nil
			}
			t := x.Sym.SymType()
			if ir.Keyable(t) {
				return nil
			}
			reported[x.Sym] = true
			c.error(x.AST.Pos, "derived value %s is owned (%s), so it is released and recomputed when what it reads changes; it reads %s, a %s, which cannot key that: %s does not compare alike on every target",
				fn.Name, fn.Return, x.Name, t, ir.RebuildIncomparable(t))
		}
		return nil
	})
}

// isStateRead reports whether sym is something a derived value is recomputed
// on: a variable or a parameter (a prop is one), as opposed to a constant or a
// declaration called by name.
func isStateRead(sym ir.Symbol) bool {
	switch s := sym.(type) {
	case *ir.Var:
		return !s.IsConst
	case *ir.Param:
		return true
	}
	return false
}
