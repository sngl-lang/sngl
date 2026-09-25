package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passLibFuncs promotes a library package's func into this package's func list
// when a body this build emits calls it.
//
// Every backend emits from `pkg.Funcs`, which holds the program's own
// declarations. A func declared in a `sngl:` package reached codegen only if
// some pass had inlined it, and passInlinePure leaves an impure one standing --
// so a platform override calling a helper from its own package emitted the call
// and nothing that declares it. No `sngl:` package had such a helper until the
// drawing overrides needed one, which is why nothing noticed.
//
// Promoting rather than teaching each platform to look: they do not agree on
// where to look, and the one funnel that claims to answer for all of them
// (codegen.AllFuncs) is bypassed by html, which builds its own list.
//
// Runs after passCanvas, because the call may be inside a draw function that
// pass synthesized from an override's handler body.
var passLibFuncs = pass{
	name:    "LibFuncs",
	enabled: func(c Features) bool { return true },
	apply:   lowerLibFuncs,
}

func lowerLibFuncs(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	have := map[*ir.Func]bool{}
	for _, fn := range pkg.Funcs {
		have[fn] = true
	}
	// A func with no body is a native or an intrinsic and is emitted as the
	// call itself; one with a receiver travels with whatever declares it. What
	// is left is a plain helper another package declared, which a program's own
	// func list would never mention -- a program's declarations carry an empty
	// Pkg, so a non-empty one is exactly the library case.
	wanted := func(fn *ir.Func) bool {
		return fn != nil && !have[fn] && len(fn.Block) > 0 &&
			fn.Receiver == "" && fn.Intrinsic == "" && fn.Foreign.Name == "" &&
			fn.Pkg != ""
	}
	// The package, rather than a list of roots assembled here: that list named
	// component and window bodies and their funcs, so a helper called only
	// from a timer handler, a var initializer or a prop default was never
	// promoted and reached the backend undeclared.
	var roots []any
	push := func(v any) { roots = append(roots, v) }
	var added []*ir.Func
	foreign := importedVars(pkg)
	for {
		push(pkg)
		promoteFuncs(&roots, push, wanted, have, &added)
		pkg.Funcs = append(pkg.Funcs, added...)
		added = nil
		if !promoteVars(pkg, foreign) {
			break
		}
	}
	inlineLibConsts(pkg)
	// The structs those helpers' signatures name are promoted by the shake
	// instead: only there is it known which of them survive, and a struct
	// belonging to one that does not is a declaration nothing writes.
	return nil
}

func promoteFuncs(roots *[]any, push func(any), wanted func(*ir.Func) bool, have map[*ir.Func]bool, added *[]*ir.Func) {
	for len(*roots) > 0 {
		root := (*roots)[0]
		*roots = (*roots)[1:]
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			call, ok := e.(*ir.Call)
			if !ok || !wanted(call.Func) {
				return nil
			}
			have[call.Func] = true
			*added = append(*added, call.Func)
			// A promoted helper may call a second one.
			push(call.Func.Block)
			return nil
		})
	}
}

// foreignConst is the value behind a reference to a const another package
// declares, or nil for anything else.
//
// Both spellings: a bare name lifted by a dot import, and `math.tau` through
// an alias -- which is an ir.Select over the namespace and was the one that
// mattered, since a target package qualifies its imports by convention.
func foreignConst(n ir.Node, own map[*ir.Var]bool) ir.Expr {
	value := func(sym ir.Symbol) ir.Expr {
		v, ok := sym.(*ir.Var)
		if !ok || !v.IsConst || own[v] || v.Init == nil {
			return nil
		}
		// A literal only. `true` is a const of `sngl:builtin` whose
		// initializer is `0 == 0`, and substituting that emitted
		// `boolToInt((0 == 0))` where the backend already writes `true`
		// perfectly well. A literal is the case this exists for -- a number
		// or a string another package declares, which nothing in the output
		// names -- and it is the only one where the substitution is plainly
		// an improvement.
		if _, isLit := v.Init.(*ir.Literal); !isLit {
			return nil
		}
		return v.Init
	}
	switch e := n.(type) {
	case *ir.Ident:
		return value(e.Sym)
	case *ir.Select:
		id, ok := e.Operand.(*ir.Ident)
		if !ok {
			return nil
		}
		ns, ok := id.Sym.(*ir.Namespace)
		if !ok || ns.Pkg == nil || ns.Pkg.Symbols == nil {
			return nil
		}
		sym, ok := ns.Pkg.Symbols.LookupMember(e.Field)
		if !ok {
			return nil
		}
		return value(sym)
	}
	return nil
}

// inlineLibConsts replaces a reference to another package's const with its
// value.
//
// A const is a compile-time value, and nothing declares a `sngl:` package's
// in the output -- the same gap the func promotion above closes, except that a
// value needs no declaration at all. `math.tau` reached the generated Go as
// the identifier `math.tau`, which is undefined there; the optimizer folded it
// wherever it ran, so only a pipeline that lowers without optimizing saw it,
// and the gtk4 snapshot harness is one.
//
// A const this package declares is left alone: the backend emits it.
func inlineLibConsts(pkg *ir.Package) {
	own := map[*ir.Var]bool{}
	note := func(vars []*ir.Var) {
		for _, v := range vars {
			own[v] = true
		}
	}
	note(pkg.Vars)
	for _, c := range pkg.Components {
		note(c.Vars)
	}
	rewrite := func(root any) {
		_ = ir.Rewrite(root, func(n ir.Node) (ir.Node, error) {
			if v := foreignConst(n, own); v != nil {
				return v, ir.SkipDir
			}
			return n, nil
		})
	}
	// Same reason as above: a const named only from a timer handler or a
	// declaration default is as undefined in the output as one named from a
	// component body.
	rewrite(pkg)
}

// importedVars is every var an imported SNGL package declares, directly or
// through its own imports. A const is not one: inlineLibConsts answers those
// with their value.
func importedVars(pkg *ir.Package) map[*ir.Var]bool {
	out := map[*ir.Var]bool{}
	seen := map[*ir.Package]bool{pkg: true}
	var visit func(p *ir.Package)
	visit = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			for _, v := range imp.Pkg.Vars {
				if !v.IsConst {
					out[v] = true
				}
			}
			visit(imp.Pkg)
		}
	}
	visit(pkg)
	return out
}

// promoteVars adds to pkg.Vars each imported var this build's bodies read or
// write, and reports whether it added one.
//
// The funcs another package declares are promoted above, and a func reading
// its own package's state took that state with it nowhere: `calls += 1` in a
// promoted helper named a var no target declared -- undefined on every Go
// target, a ReferenceError on html, and the text reading it drawn empty. A
// read through the package's alias, `doc.calls`, becomes the bare name the
// declaration now has here.
func promoteVars(pkg *ir.Package, foreign map[*ir.Var]bool) bool {
	if len(foreign) == 0 {
		return false
	}
	var added []*ir.Var
	take := func(v *ir.Var) {
		if foreign[v] {
			delete(foreign, v)
			added = append(added, v)
		}
	}
	_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		switch e := n.(type) {
		case *ir.Ident:
			if v, ok := e.Sym.(*ir.Var); ok {
				take(v)
			}
		case *ir.Select:
			id, ok := e.Operand.(*ir.Ident)
			if !ok {
				return n, nil
			}
			ns, ok := id.Sym.(*ir.Namespace)
			if !ok || ns.Pkg == nil || ns.Pkg.Symbols == nil {
				return n, nil
			}
			sym, ok := ns.Pkg.Symbols.LookupMember(e.Field)
			if !ok {
				return n, nil
			}
			if v, ok := sym.(*ir.Var); ok && !v.IsConst {
				take(v)
				return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}, ir.SkipDir
			}
		}
		return n, nil
	})
	pkg.Vars = append(pkg.Vars, added...)
	return len(added) > 0
}
