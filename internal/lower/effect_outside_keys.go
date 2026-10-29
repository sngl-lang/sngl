package lower

import (
	"slices"
	"strconv"

	"duckfam.us/sngl/ir"
)

// passEffectOutsideKeys hands a component built at run time each package var
// its effects are keyed on as a prop, bound at every site to the var.
//
// The effect lowering marks an `@update` owed, and settles its scope, after
// each write to what its key reads. Written outside the component -- by the
// page, or a handler of another -- that write names the instance's flag and
// settle bare, which the writer does not have and which stand for however
// many instances are live: fyne, gtk4 and android did not build, and html
// built and never ran the `@update`
// (testdata/effect_keyed_on_package_var_runtime_instance.txtar). A prop is
// what reaches every live instance already: the site hands each the new value
// through its setter, and the setter marks and settles what is keyed on it
// (settleOnSet, android's emitPropReports).
//
// A key reading the var through a call, `on=current()`, calls a copy of each
// func on the way that takes the var as a parameter, and hands it the prop
// (outsideCalls.copyOf).
var passEffectOutsideKeys = pass{
	name:    "EffectOutsideKeys",
	enabled: func(Features) bool { return true },
	apply:   lowerEffectOutsideKeys,
}

func lowerEffectOutsideKeys(pkg *ir.Package, _ Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	reactive := collectReactiveVars(pkg)
	global := map[*ir.Var]bool{}
	for _, v := range pkg.Vars {
		if reactive[v] {
			global[v] = true
		}
	}
	calls := &outsideCalls{pkg: pkg, global: global, copies: map[*ir.Func]*ir.Func{}}
	bound := map[*ir.Component][]outsideKey{}
	for _, c := range pkg.Components {
		// A test's root is built by the harness, with no site to bind it.
		if !c.RuntimeInstance || c.Name == opts.RootComponent {
			continue
		}
		var keys []outsideKey
		_ = ir.Walk(c.Body.Stmts, func(n ir.Node) error {
			fx, ok := n.(*ir.NodeInst)
			if !ok || !isEffectNode(fx) {
				return nil
			}
			for i, arg := range fx.Props {
				if arg.Name != "on" {
					continue
				}
				propOf := func(v *ir.Var) *ir.Ident {
					var p *ir.Param
					for _, k := range keys {
						if k.v == v {
							p = k.p
						}
					}
					if p == nil {
						p = &ir.Param{Name: outsideKeyName(c, v), Type: v.Type}
						c.Props = append(c.Props, &ir.Prop{Name: p.Name, Type: p.Type, Sym: p})
						keys = append(keys, outsideKey{v: v, p: p})
					}
					return &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}
				}
				holder := &ir.Return{Value: arg.Value}
				_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
					switch x := x.(type) {
					case *ir.Ident:
						if v, ok := x.Sym.(*ir.Var); ok && global[v] {
							return propOf(v), nil
						}
					case *ir.Call:
						calls.redirect(x, propOf)
					}
					return x, nil
				})
				fx.Props[i].Value = holder.Value
			}
			return nil
		})
		if len(keys) > 0 {
			bound[c] = keys
		}
	}
	if len(bound) == 0 {
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		_ = ir.Walk(o.Stmts(), func(n ir.Node) error {
			site, ok := n.(*ir.NodeInst)
			if !ok {
				return nil
			}
			for _, k := range bound[site.Component] {
				site.Props = append(site.Props, ir.Arg{Name: k.p.Name, Value: &ir.Ident{Name: k.v.Name, Type: k.v.Type, Sym: k.v}})
			}
			return nil
		})
	}
	return nil
}

// outsideKey is a package var an effect key read and the prop now standing
// for it.
type outsideKey struct {
	v *ir.Var
	p *ir.Param
}

// outsideKeyName is a name for v's prop that c declares nothing else under.
func outsideKeyName(c *ir.Component, v *ir.Var) string {
	base := "__on_" + v.Name
	name := base
	for i := 1; slices.ContainsFunc(c.Props, func(p *ir.Prop) bool { return p.Name == name }); i++ {
		name = base + strconv.Itoa(i)
	}
	return name
}

// outsideCalls is the copies of the funcs a key calls that take each package
// var they read, directly or through what they call, as a parameter.
type outsideCalls struct {
	pkg    *ir.Package
	global map[*ir.Var]bool
	// copies holds nil for a func with no copy: one reading no such var, or
	// one that cannot be copied, which keeps reading the var by name.
	copies map[*ir.Func]*ir.Func
}

// reads is the package vars fn reads, through what it calls too, in the order
// the package declares them.
func (oc *outsideCalls) reads(fn *ir.Func) []*ir.Var {
	got := map[*ir.Var]bool{}
	gatherFuncReads(fn, oc.global, got, nil)
	var out []*ir.Var
	for _, v := range oc.pkg.Vars {
		if got[v] {
			out = append(out, v)
		}
	}
	return out
}

// redirect points call at fn's copy, handing it arg(v) for each var it takes.
func (oc *outsideCalls) redirect(call *ir.Call, arg func(*ir.Var) *ir.Ident) bool {
	cp := oc.copyOf(call.Func)
	if cp == nil {
		return false
	}
	for _, v := range oc.reads(call.Func) {
		call.Args = append(call.Args, ir.CallArg{Value: arg(v)})
	}
	call.Func = cp
	return true
}

// copyOf is fn with a parameter for each package var it reads, which its body
// and the copies of what it calls read instead, or nil.
func (oc *outsideCalls) copyOf(fn *ir.Func) *ir.Func {
	if fn == nil {
		return nil
	}
	if cp, done := oc.copies[fn]; done {
		return cp
	}
	oc.copies[fn] = nil
	vars := oc.reads(fn)
	switch {
	case len(vars) == 0, fn.IsMethod(), !slices.Contains(oc.pkg.Funcs, fn):
		return nil
	case slices.ContainsFunc(fn.Params, func(p *ir.Param) bool { return p.Variadic }):
		return nil
	case slices.ContainsFunc(fn.Writes, func(v *ir.Var) bool { return slices.Contains(vars, v) }):
		return nil
	}
	cp := *fn
	cp.Name = oc.copyName(fn.Name)
	cp.Params = slices.Clone(fn.Params)
	cp.Reads = slices.DeleteFunc(slices.Clone(fn.Reads), func(v *ir.Var) bool { return slices.Contains(vars, v) })
	params := map[*ir.Var]*ir.Param{}
	for _, v := range vars {
		p := &ir.Param{Name: copyParamName(&cp, v), Type: v.Type}
		cp.Params = append(cp.Params, p)
		params[v] = p
	}
	paramOf := func(v *ir.Var) *ir.Ident {
		return &ir.Ident{Name: params[v].Name, Type: v.Type, Sym: params[v]}
	}
	oc.copies[fn] = &cp
	cp.Block = ir.CloneStmtsSharingDecls(fn.Block)
	whole := true
	_ = ir.RewriteExprs(cp.Block, func(x ir.Expr) (ir.Expr, error) {
		switch x := x.(type) {
		case *ir.Ident:
			if v, ok := x.Sym.(*ir.Var); ok && params[v] != nil {
				return paramOf(v), nil
			}
		case *ir.Call:
			if len(oc.reads(x.Func)) > 0 && !oc.redirect(x, paramOf) {
				whole = false
			}
		}
		return x, nil
	})
	if !whole {
		oc.copies[fn] = nil
		return nil
	}
	oc.pkg.Funcs = append(oc.pkg.Funcs, &cp)
	return &cp
}

func (oc *outsideCalls) copyName(name string) string {
	base := name + "__outside"
	out := base
	for i := 1; slices.ContainsFunc(oc.pkg.Funcs, func(f *ir.Func) bool { return f.Name == out }); i++ {
		out = base + strconv.Itoa(i)
	}
	return out
}

func copyParamName(fn *ir.Func, v *ir.Var) string {
	base := "__on_" + v.Name
	name := base
	for i := 1; slices.ContainsFunc(fn.Params, func(p *ir.Param) bool { return p.Name == name }); i++ {
		name = base + strconv.Itoa(i)
	}
	return name
}
