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
	enabled: func(c Caps) bool { return true },
	apply:   lowerLibFuncs,
}

func lowerLibFuncs(pkg *ir.Package, _ Caps, _ Options) error {
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
	var roots []any
	push := func(v any) { roots = append(roots, v) }
	for _, c := range pkg.Components {
		push(c.Body)
		for _, fn := range c.Funcs {
			push(fn.Block)
		}
	}
	for _, w := range pkg.Windows {
		push(w.Body)
		for _, fn := range w.Funcs {
			push(fn.Block)
		}
	}
	for _, fn := range pkg.Funcs {
		push(fn.Block)
	}
	var added []*ir.Func
	for len(roots) > 0 {
		root := roots[0]
		roots = roots[1:]
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			call, ok := e.(*ir.Call)
			if !ok || !wanted(call.Func) {
				return nil
			}
			have[call.Func] = true
			added = append(added, call.Func)
			// A promoted helper may call a second one.
			push(call.Func.Block)
			return nil
		})
	}
	pkg.Funcs = append(pkg.Funcs, added...)
	inlineLibConsts(pkg)
	promoteLibStructs(pkg, added)
	return nil
}

// promoteLibStructs adds a library package's struct to this package's list
// when emitted code names it as a type.
//
// The third of the same shape: every backend emits its type declarations from
// `pkg.Structs`, which holds the program's own, so a struct another package
// declares had no declaration in the output -- `shapes.Point` reached the
// generated Go as the undefined type `Point`. A platform override is the first
// thing to name one, because it is the first library body a build emits.
//
// A foreign struct is skipped: `#[cnative("C.GdkPixbuf")]` *is* a host type
// and emitting a declaration for it would shadow the thing it names.
func promoteLibStructs(pkg *ir.Package, promoted []*ir.Func) {
	have := map[*ir.StructDef]bool{}
	for _, sd := range pkg.Structs {
		have[sd] = true
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
	// Only the promoted helpers' own signatures. A struct a body merely
	// *builds* needs no declaration of its own in a language whose literals
	// carry none, and reaching wider pulled in `ui.Style` -- referenced by
	// every program -- which html then emitted a constructor for. A signature
	// is different: it writes the type's name, so the name has to exist.
	for _, fn := range promoted {
		for _, p := range fn.Params {
			want(p.Type)
		}
		want(fn.Return)
	}
	pkg.Structs = append(pkg.Structs, added...)
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
	for _, w := range pkg.Windows {
		note(w.Vars)
	}
	rewrite := func(root any) {
		_ = ir.Rewrite(root, func(n ir.Node) (ir.Node, error) {
			if v := foreignConst(n, own); v != nil {
				return v, ir.SkipDir
			}
			return n, nil
		})
	}
	for _, c := range pkg.Components {
		rewrite(c.Body)
		for _, fn := range c.Funcs {
			rewrite(fn.Block)
		}
	}
	for _, w := range pkg.Windows {
		rewrite(w.Body)
		for _, fn := range w.Funcs {
			rewrite(fn.Block)
		}
	}
	for _, fn := range pkg.Funcs {
		rewrite(fn.Block)
	}
}
