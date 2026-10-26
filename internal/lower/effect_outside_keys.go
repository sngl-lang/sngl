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
// Only what the key reads by name moves; a key reading the var through a call
// still reads it there.
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
				holder := &ir.Return{Value: arg.Value}
				_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
					id, ok := x.(*ir.Ident)
					if !ok {
						return x, nil
					}
					v, ok := id.Sym.(*ir.Var)
					if !ok || !global[v] {
						return x, nil
					}
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
					return &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}, nil
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
