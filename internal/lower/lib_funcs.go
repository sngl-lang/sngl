package lower

import (
	"maps"
	"slices"

	"duckfam.us/sngl/ir"
)

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
var passLibFuncs = pass{
	name:    "LibFuncs",
	enabled: func(c Features) bool { return true },
	apply:   lowerLibFuncs,
}

func lowerLibFuncs(pkg *ir.Package, feats Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	have := map[*ir.Func]bool{}
	for _, fn := range pkg.Funcs {
		have[fn] = true
	}
	// A library function promoted already, by an earlier run, stands for
	// every declaration of its name: the frozen original a body spliced or
	// released since still calls, and a test build's copy of its package.
	promoted := promotedFuncs(pkg)
	repointPromoted(pkg, promoted)
	// A func with no body is a native or an intrinsic and is emitted as the
	// call itself; one with a receiver travels with whatever declares it. What
	// is left is a plain helper another package declared, which a program's own
	// func list would never mention -- a program's declarations carry an empty
	// Pkg, so a non-empty one is exactly the library case.
	//
	// A method of a library struct travels with nothing: the struct is
	// promoted by the shake as a declaration of the program's, and its
	// methods are declared beside the program's own -- `http.Response`'s
	// `checked`. Nor does a method of a node the target makes itself, a GTK
	// widget's action (`bar.pulse()`), since no component declaration is
	// written for one.
	wanted := func(fn *ir.Func) bool {
		return fn != nil && !have[fn] && len(fn.Block) > 0 &&
			(!fn.IsMethod() || libStructMethod(fn) || intrinsicMethod(fn)) && fn.Intrinsic == "" && fn.Foreign.Name == "" &&
			fn.PkgPath() != ""
	}
	// The package, rather than a list of roots assembled here: that list named
	// component and window bodies and their funcs, so a helper called only
	// from a timer handler, a var initializer or a prop default was never
	// promoted and reached the backend undeclared.
	var roots []any
	push := func(v any) { roots = append(roots, v) }
	// A placed node's protocol functions are spliced by passPlacement, after
	// this pass, and a helper one calls is promoted here; one no splice
	// calls is shaken once the build has lowered. A copy is walked, since
	// the walk repoints what it promotes and the library's own is frozen.
	// Only a placed build splices them: cgogen's wrappers lower a platform's
	// package with no features, and write no SNGL helper.
	if feats.Placement {
		for _, fn := range placedProtocols(pkg) {
			push(ir.CloneExprSharingDecls(&ir.Lambda{Func: fn}).(*ir.Lambda).Func.Block)
		}
	}
	var added []*ir.Func
	// What the target's generated entry calls (sngl:x/gen's #[entry]) no
	// SNGL code names: each is promoted as a root of its own, where the
	// program starts as the target's own entry does -- not under a test
	// agent's, nor an `@run` handler's.
	if pkg.Run == nil && !opts.Tests && opts.Platform != "" {
		for _, tp := range ir.TargetPackages(pkg) {
			if tp.Path != "platform/"+opts.Platform && tp.Path != "sngl:platform/"+opts.Platform {
				continue
			}
			for _, fn := range tp.Funcs {
				if !fn.GenEntry || len(fn.Block) == 0 {
					continue
				}
				key := libFuncKey(fn)
				c := promoted[key]
				if c == nil {
					c = fn
					if fn.Frozen {
						c = thaw(fn)
					}
					have[fn], have[c] = true, true
					promoted[key] = c
					added = append(added, c)
					push(c.Block)
				}
				if !slices.Contains(pkg.Entries, c) {
					pkg.Entries = append(pkg.Entries, c)
				}
			}
		}
	}
	foreign := importedVars(pkg)
	for {
		push(pkg)
		promoteFuncs(&roots, push, wanted, have, promoted, &added)
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

// placedProtocols is each protocol function of an #[intrinsic] component pkg
// names, in a stable order, through the bodies of the library components it
// names: the platform's override of `ui.tabs` is a library body that names
// the Tabs node, which a later pass splices into the program, after this
// pass and CMarshal have run.
func placedProtocols(pkg *ir.Package) []*ir.Func {
	var comps []*ir.Component
	walked := map[*ir.Component]bool{}
	var bodies []*ir.Component
	note := func(c *ir.Component) {
		if c == nil {
			return
		}
		if c.Protocol != nil && !slices.Contains(comps, c) {
			comps = append(comps, c)
		}
		if c.PkgPath() != "" && !walked[c] {
			walked[c] = true
			bodies = append(bodies, c)
		}
	}
	visit := func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			note(x.Component)
		case ir.Expr:
			if t := x.ExprType(); t != nil {
				c, _ := t.Decl.(*ir.Component)
				note(c)
			}
		}
		return nil
	}
	_ = ir.Walk(pkg, visit)
	for len(bodies) > 0 {
		c := bodies[0]
		bodies = bodies[1:]
		_ = ir.Walk(c, visit)
	}
	var fns []*ir.Func
	add := func(fn *ir.Func) {
		if fn != nil && len(fn.Block) > 0 && !slices.Contains(fns, fn) {
			fns = append(fns, fn)
		}
	}
	for _, c := range comps {
		p := c.Protocol
		for _, fn := range []*ir.Func{p.Create, p.Append, p.Remove, p.InsertBefore, p.Contents, p.Anchor, p.Redraw} {
			add(fn)
		}
		for _, m := range []map[string]*ir.Func{p.Set, p.On, p.Fire} {
			for _, k := range slices.Sorted(maps.Keys(m)) {
				add(m[k])
			}
		}
	}
	return fns
}

// libFuncKey names a library function whatever copy of its package
// declares it: its package's path, its receiver's name and its own.
func libFuncKey(fn *ir.Func) string {
	return fn.PkgPath() + "\x00" + fn.RecvName() + "\x00" + fn.Name
}

// promotedFuncs is each library function pkg declares, by libFuncKey.
func promotedFuncs(pkg *ir.Package) map[string]*ir.Func {
	promoted := map[string]*ir.Func{}
	for _, fn := range pkg.Funcs {
		if fn.PkgPath() != "" && fn.Name != "" {
			promoted[libFuncKey(fn)] = fn
		}
	}
	return promoted
}

// repointPromoted makes each call in pkg of a library function that pkg has
// promoted call the promoted one, where it names another declaration of it: a
// body spliced after passLibFuncs, a placed node's protocol, names the frozen
// original.
func repointPromoted(pkg *ir.Package, promoted map[string]*ir.Func) {
	if len(promoted) == 0 {
		return
	}
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		var fn *ir.Func
		switch x := e.(type) {
		case *ir.Call:
			fn = x.Func
		case *ir.Ident:
			fn, _ = x.Sym.(*ir.Func)
		}
		if fn == nil || fn.PkgPath() == "" || fn.Name == "" {
			return nil
		}
		if c := promoted[libFuncKey(fn)]; c != nil && c != fn {
			repoint(e, c)
		}
		return nil
	})
}

// libStructMethod reports whether fn is a method a library declares on a
// struct of its own, rather than on a builtin type or a component.
func libStructMethod(fn *ir.Func) bool {
	sd, ok := fn.RecvDecl().(*ir.StructDef)
	return ok && sd.Builtin == "" && sd.Intrinsic == "" && fn.Lowered.HostReceiver == ""
}

// intrinsicMethod reports whether fn is a method a library declares on an
// #[intrinsic] component, which is a function of the node.
func intrinsicMethod(fn *ir.Func) bool {
	comp, ok := fn.RecvDecl().(*ir.Component)
	return ok && comp.Intrinsic != ""
}

func promoteFuncs(roots *[]any, push func(any), wanted func(*ir.Func) bool, have map[*ir.Func]bool, promoted map[string]*ir.Func, added *[]*ir.Func) {
	for len(*roots) > 0 {
		root := (*roots)[0]
		*roots = (*roots)[1:]
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			// Called, or taken as a value: sngl:http's `closed`, which a
			// test starts with as its answer.
			var fn *ir.Func
			switch x := e.(type) {
			case *ir.Call:
				fn = x.Func
			case *ir.Ident:
				fn, _ = x.Sym.(*ir.Func)
			}
			if !wanted(fn) {
				return nil
			}
			if c := promoted[libFuncKey(fn)]; c != nil {
				repoint(e, c)
				return nil
			}
			have[fn] = true
			// The checked library's own declaration, which a body spliced
			// after the build's copy was made still calls: the program's is a
			// copy, since the passes after this one rename and fold what the
			// program declares.
			key := libFuncKey(fn)
			if fn.Frozen {
				c := thaw(fn)
				have[c] = true
				repoint(e, c)
				fn = c
			}
			promoted[key] = fn
			*added = append(*added, fn)
			// A promoted helper may call a second one.
			push(fn.Block)
			return nil
		})
	}
}

// thaw is a copy of the frozen fn the build may write: its parameters, its
// body and the locals the body declares its own (a rename of one wrote the
// library), every other declaration it names shared.
func thaw(fn *ir.Func) *ir.Func {
	c := ir.CopyFunc(fn)
	c.Frozen = false
	return c
}

// repoint makes e, a call of a function or a reference to one, name fn.
func repoint(e ir.Expr, fn *ir.Func) {
	switch x := e.(type) {
	case *ir.Call:
		x.Func = fn
	case *ir.Ident:
		x.Sym = fn
	}
}

// foreignConst is the value behind a reference to a const another package
// declares, or nil for anything else.
//
// Both spellings are the one Ident: a bare name lifted by a dot import, and
// `math.tau` through an alias, which a target package writes by convention.
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
	if e, ok := n.(*ir.Ident); ok {
		return value(e.Sym)
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
	taken := map[*ir.Var]bool{}
	take := func(v *ir.Var) {
		if foreign[v] {
			delete(foreign, v)
			added = append(added, v)
			taken[v] = true
		}
	}
	_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		// A read through the package's alias, `doc.calls`, is of the
		// declaration this package now holds, named bare.
		if e, ok := n.(*ir.Ident); ok {
			if v, ok := e.Sym.(*ir.Var); ok {
				take(v)
				if taken[v] {
					e.Qualifier = nil
				}
			}
		}
		return n, nil
	})
	pkg.Vars = append(pkg.Vars, added...)
	return len(added) > 0
}
