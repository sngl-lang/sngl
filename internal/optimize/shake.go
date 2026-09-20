package optimize

import (
	"fmt"
	"log/slog"

	"git.duckfam.us/jonathan/sngl/ir"
)

// shakeUnused removes consts, vars, functions, structs, and enums that are
// not reachable from roots (components, windows, timers, outputs, tests).
func shakeUnused(pkg *ir.Package) error {
	used := collectUsedSymbols(pkg)

	pkg.Consts = filterVars(pkg.Consts, used)
	pkg.Vars = filterVars(pkg.Vars, used)
	pkg.Funcs = filterFuncs(pkg.Funcs, used)
	pkg.Structs = filterStructs(pkg.Structs, used)
	pkg.Enums = filterEnums(pkg.Enums, used)
	return promoteForeignStructs(pkg)
}

// promoteForeignStructs adds a library package's struct to this package's list
// when a surviving function's signature names it.
//
// Every backend emits its type declarations from pkg.Structs, which holds the
// program's own -- so a struct another package declares has no declaration in
// the output, and `shapes.Point` in a promoted helper's signature arrived in
// the generated Go as the undefined type `Point`.
//
// Here rather than in the lowering that promotes the functions, because only
// after the filters above is it known which of them survive: a helper that was
// inlined into its caller and then shaken leaves a signature nobody writes,
// and promoting its types added a declaration nothing referenced.
//
// Signatures only. A struct a body merely *builds* needs no declaration in a
// language whose literals carry none, and reaching wider pulled in `ui.Style`
// -- named by every program -- which html then emitted a constructor for.
//
// A foreign struct is skipped: it *is* a host type, and a declaration for it
// would shadow the thing it names.
func promoteForeignStructs(pkg *ir.Package) error {
	have := map[*ir.StructDef]bool{}
	byName := map[string]*ir.StructDef{}
	for _, sd := range pkg.Structs {
		have[sd] = true
		byName[sd.Name] = sd
	}
	var added []*ir.StructDef
	want := func(t *ir.Type) {
		if t == nil || t.Kind != ir.TypeStruct {
			return
		}
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok || sd == nil || have[sd] || sd.Pkg == "" || sd.Foreign.Name != "" {
			return
		}
		have[sd] = true
		added = append(added, sd)
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || fn.Pkg == "" {
			continue
		}
		for _, p := range fn.Params {
			want(p.Type)
		}
		want(fn.Return)
	}
	// Promotion is keyed by declaration and every backend emits by name, so a
	// promoted library struct whose name the program also declares would be a
	// second `type Point struct` in one file -- Go refuses it, and JS takes
	// whichever came last. Renaming is the fix this wants, and it is not a
	// rename this pass can make: the declaration is shared with the memoized
	// library package, so writing to it would reach every other build in the
	// process, and the types that point at it are shared too.
	//
	// So it is reported. A build that stops naming both declarations is worth
	// more than emitted code that does not compile, or silently uses the wrong
	// type.
	for _, sd := range added {
		if other, clash := byName[sd.Name]; clash {
			return fmt.Errorf("%s declares struct %q and so does %s, which a function this build emits names in its signature; "+
				"every target emits a struct by its name, so the two cannot both be declared -- rename the local one",
				pkgLabel(other.Pkg), sd.Name, pkgLabel(sd.Pkg))
		}
		byName[sd.Name] = sd
	}
	pkg.Structs = append(pkg.Structs, added...)
	return nil
}

// pkgLabel names a package in a diagnostic. `StructDef.Pkg` already carries
// the `sngl:` prefix for a library declaration and is empty for the program's
// own.
func pkgLabel(pkg string) string {
	if pkg == "" {
		return "this program"
	}
	return pkg
}

func filterVars(vars []*ir.Var, used map[ir.Symbol]bool) []*ir.Var {
	var out []*ir.Var
	for _, v := range vars {
		if used[v] {
			out = append(out, v)
		} else {
			slog.Debug("shaken: var", "name", v.Name, "const", v.IsConst)
		}
	}
	return out
}

func filterFuncs(funcs []*ir.Func, used map[ir.Symbol]bool) []*ir.Func {
	var out []*ir.Func
	for _, f := range funcs {
		if used[f] || f.IsTest {
			out = append(out, f)
		} else {
			slog.Debug("shaken: func", "name", f.Name)
		}
	}
	return out
}

func filterStructs(structs []*ir.StructDef, used map[ir.Symbol]bool) []*ir.StructDef {
	var out []*ir.StructDef
	for _, s := range structs {
		if used[s] {
			out = append(out, s)
		} else {
			slog.Debug("shaken: struct", "name", s.Name)
		}
	}
	return out
}

func filterEnums(enums []*ir.EnumDef, used map[ir.Symbol]bool) []*ir.EnumDef {
	var out []*ir.EnumDef
	for _, e := range enums {
		if used[e] {
			out = append(out, e)
		} else {
			slog.Debug("shaken: enum", "name", e.Name)
		}
	}
	return out
}

// collectUsedSymbols finds all symbols transitively reachable from roots.
func collectUsedSymbols(pkg *ir.Package) map[ir.Symbol]bool {
	used := make(map[ir.Symbol]bool)
	var walk func(ir.Symbol)

	walk = func(sym ir.Symbol) {
		if sym == nil || used[sym] {
			return
		}
		used[sym] = true

		switch s := sym.(type) {
		case *ir.Var:
			// Walk the declared type: a struct/enum referenced only as a
			// type (e.g. `var x option<User> = null`) must stay reachable,
			// or the shaker prunes its definition while codegen still emits
			// the type reference → "undefined: User".
			walkType(s.Type, used, walk)
			walkExpr(s.Init, used, walk)
			for _, h := range s.Handlers {
				walkFunc(h.Func, used, walk)
			}
		case *ir.Func:
			walkStmts(s.Block, used, walk)
			for _, p := range s.Params {
				walkType(p.Type, used, walk)
				if p.Default != nil {
					walkExpr(p.Default, used, walk)
				}
			}
			walkType(s.Return, used, walk)
		case *ir.StructDef:
			for _, f := range s.Fields {
				walkType(f.Type, used, walk)
				if f.Default != nil {
					walkExpr(f.Default, used, walk)
				}
			}
		case *ir.EnumDef:
			for _, m := range s.Members {
				if m.Value != nil {
					walkExpr(m.Value, used, walk)
				}
			}
		case *ir.Component:
			for _, p := range s.Props {
				walkType(p.Type, used, walk)
				if p.Default != nil {
					walkExpr(p.Default, used, walk)
				}
			}
			for _, v := range s.Vars {
				walk(v)
			}
			for _, f := range s.Funcs {
				walk(f)
			}
			walkStmts(s.Body, used, walk)
		}
	}

	// Roots: the package body, components, windows, timers, outputs, test
	// functions. The package body is a root like a window's: it is rendered,
	// so what it reads is used. Left out, a top-level `var` looked unread and
	// was shaken away while the body kept referring to it.
	walkStmts(pkg.Body, used, walk)
	for _, comp := range pkg.Components {
		walk(comp)
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			walk(v)
		}
		for _, f := range w.Funcs {
			walk(f)
		}
		walkStmts(w.Body, used, walk)
	}
	// Test functions are roots.
	for _, f := range pkg.Funcs {
		if f.IsTest {
			walk(f)
		}
	}
	// Vars with handlers are roots (reactive state).
	for _, v := range pkg.Vars {
		if len(v.Handlers) > 0 {
			walk(v)
		}
	}

	return used
}

func walkFunc(f *ir.Func, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if f == nil {
		return
	}
	walkStmts(f.Block, used, walk)
}

func walkStmts(stmts []ir.Stmt, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	for _, s := range stmts {
		walkStmt(s, used, walk)
	}
}

func walkStmt(s ir.Stmt, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ir.NodeInst:
		if n.Component != nil {
			walk(n.Component)
		}
		for _, p := range n.Props {
			walkExpr(p.Value, used, walk)
		}
		for _, h := range n.Handlers {
			walkFunc(h.Func, used, walk)
		}
		walkStmts(n.Children, used, walk)
	case *ir.CallStmt:
		if n.Call != nil {
			walkCallExpr(n.Call, used, walk)
		}
	case *ir.SlotInst:
		walkStmts(n.Children, used, walk)
	case *ir.Assign:
		walkExpr(n.Target, used, walk)
		walkExpr(n.Value, used, walk)
	case *ir.Toggle:
		walkExpr(n.Target, used, walk)
	case *ir.Emit:
		for _, a := range n.Args {
			walkExpr(a.Value, used, walk)
		}
	case *ir.LocalVar:
		walkType(n.Type, used, walk)
		walkExpr(n.Init, used, walk)
	case *ir.Return:
		walkExpr(n.Value, used, walk)
	case *ir.If:
		walkExpr(n.Cond, used, walk)
		walkStmts(n.Body, used, walk)
		walkStmts(n.Else, used, walk)
	case *ir.For:
		walkExpr(n.Iter, used, walk)
		walkStmts(n.Body, used, walk)
		walkStmts(n.Else, used, walk)
	case *ir.Window:
		for i := range n.Props {
			walkExpr(n.Props[i].Value, used, walk)
		}
		for _, v := range n.Vars {
			walk(v)
		}
		for _, f := range n.Funcs {
			walk(f)
		}
		walkStmts(n.Body, used, walk)
	case *ir.ContextProvider:
		walkExpr(n.Value, used, walk)
		walkStmts(n.Children, used, walk)
	case *ir.ErrorBoundary:
		if n.Handler != nil && n.Handler.Func != nil {
			walkFunc(n.Handler.Func, used, walk)
		}
		walkStmts(n.Children, used, walk)
	case *ir.CanvasRedrawStmt:
		// Carries only NodeInst/Func pointers already tracked by other walk paths.
	case *ir.Break, *ir.Continue:
		// A loop escape names no declaration.
	default:
		panic(fmt.Sprintf("walkStmt: unhandled stmt %T", n))
	}
}

func walkExpr(e ir.Expr, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if x.Sym != nil {
			walk(x.Sym)
		}
	case *ir.Binary:
		walkExpr(x.Left, used, walk)
		walkExpr(x.Right, used, walk)
	case *ir.Unary:
		walkExpr(x.Operand, used, walk)
	case *ir.Ternary:
		walkExpr(x.Cond, used, walk)
		walkExpr(x.Then, used, walk)
		walkExpr(x.Else, used, walk)
	case *ir.Call:
		walkCallExpr(x, used, walk)
	case *ir.Conversion:
		walkExpr(x.Operand, used, walk)
	case *ir.Select:
		walkExpr(x.Operand, used, walk)
	case *ir.Index:
		walkExpr(x.Operand, used, walk)
		walkExpr(x.Idx, used, walk)
	case *ir.ListLit:
		for _, el := range x.Elems {
			walkExpr(el, used, walk)
		}
	case *ir.StructLit:
		if x.Def != nil {
			walk(x.Def)
		}
		for _, f := range x.Fields {
			walkExpr(f.Value, used, walk)
		}
	case *ir.Spread:
		walkExpr(x.Operand, used, walk)
	case *ir.Lambda:
		if x.Func != nil {
			walkFunc(x.Func, used, walk)
		}
	case *ir.Closure:
		if x.Func != nil {
			walkFunc(x.Func, used, walk)
		}
		if x.State != nil {
			walkExpr(x.State, used, walk)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			walkExpr(kv.Key, used, walk)
			walkExpr(kv.Value, used, walk)
		}
	case *ir.ContextRead:
		if x.Ref != nil {
			walk(x.Ref)
		}
	case *ir.Literal:
		// No subexpressions or referenced symbols.
	default:
		panic(fmt.Sprintf("walkExpr: unhandled expr %T", x))
	}
}

func walkCallExpr(call *ir.Call, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if call.Func != nil {
		walk(call.Func)
	}
	walkExpr(call.Receiver, used, walk)
	for _, a := range call.Args {
		walkExpr(a.Value, used, walk)
	}
}

func walkType(t *ir.Type, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if t == nil {
		return
	}
	if t.Decl != nil {
		walk(t.Decl)
	}
	for _, elem := range t.Elems {
		walkType(elem, used, walk)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			walkType(p.Type, used, walk)
		}
		walkType(t.Sig.Return, used, walk)
	}
}
