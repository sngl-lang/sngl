package android

import (
	"slices"

	"duckfam.us/sngl/ir"
)

const internetPermission = "android.permission.INTERNET"

// permissions are what the app's manifest asks for: every
// #[android.permission] on a declaration the program reaches -- a function
// it calls, a component it renders -- and on this package's restatement of
// an intrinsic a call's id names, sorted.
func permissions(pkg *ir.Package) []string {
	var out []string
	add := func(ps []string) {
		for _, p := range ps {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			if x.Func != nil {
				add(x.Func.Permissions)
				if def := ir.LookupIntrinsic(x.Func.Intrinsic); def != nil {
					add(def.Permissions)
				}
			}
		case *ir.NodeInst:
			if x.Component != nil {
				add(x.Component.Permissions)
			}
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// withPermission is ps with p, once.
func withPermission(ps []string, p string) []string {
	if slices.Contains(ps, p) {
		return ps
	}
	return append(slices.Clone(ps), p)
}
