package testutil

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/ir"
)

// Roots is what a platform harness builds a checked fixture from: the
// program, where the package body renders something, and otherwise each
// component the fixture declares, built as a test build roots it -- with no
// call site, each prop holding its default or its type's zero.
//
// A package with no body has no root, and the generators then emit every
// component as an entry nothing renders, which no build does: a component
// binding a two-way prop of a generic one reached Go with its T unbound. A
// component another one renders is not a root either: it is built where that
// one renders it, as a program builds it -- `framed`, a wrapper rendering a
// page, means nothing outside the stack it is written in. Nor is a generic
// one, since nothing would bind its type parameters.
func Roots(pkg *ir.Package) []string {
	if len(pkg.Body) > 0 {
		return []string{""}
	}
	rendered := map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		_ = ir.Walk(c, func(n ir.Node) error {
			if x, ok := n.(*ir.NodeInst); ok && x.Component != c {
				rendered[x.Component] = true
			}
			return nil
		})
	}
	var roots []string
	for _, c := range pkg.Components {
		if rendered[c] || c.PkgPath() != "" || len(c.TypeParams) > 0 || c.Primitive() || c.Builtin != ir.BuiltinNone {
			continue
		}
		roots = append(roots, c.Name)
	}
	if len(roots) == 0 {
		return []string{""}
	}
	return roots
}

// RootAt is a copy of pkg built from root, one of Roots, and the options
// telling the generator so; pkg itself and nil options for the program.
func RootAt(pkg *ir.Package, root string) (*ir.Package, *ir.StructLit) {
	if root == "" {
		return pkg, nil
	}
	rooted := ir.ClonePackage(pkg)
	build.IsolateRootComponent(rooted, root)
	opts := &ir.StructLit{}
	codegen.SetOptionField(opts, "rootComponent", root)
	return rooted, opts
}
