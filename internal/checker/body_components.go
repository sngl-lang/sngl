package checker

import (
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// reportBodyComponentCollisions reports a body-local component whose emitted
// name another declaration of this package would also emit. Interim, for #198.
//
// Every platform sets InlineComponents=false, so passNoInlineComponents
// substitutes each component into its caller and renames the state it carries
// per call site (__instN). What survives that is a recursion cycle, and a
// surviving component is emitted under its declared name -- two of one name
// then emit one host declaration twice, which `sngl generate` writes and exits
// 0 on. #198 renames per body and removes this.
//
// So the question is asked of the cycles only, and after pass2: a body-local
// component that is not in one shares nothing with a namesake, which is what
// lets a body-local component shadow a top-level one.
func (c *checker) reportBodyComponentCollisions() {
	if c.pkg == nil || len(c.bodyComps) == 0 {
		return
	}
	surviving := recursiveComponents(c.pkg)
	byName := map[string][]*ir.Component{}
	for _, comp := range c.pkg.Components {
		if surviving[comp] {
			byName[comp.Name] = append(byName[comp.Name], comp)
		}
	}
	// One report per colliding name, at the body-local declaration that came
	// second: registration order, and so document order for a multi-file
	// package, which is why the message names the other position too.
	reported := map[string]bool{}
	for _, comp := range c.bodyComps {
		if !surviving[comp] || reported[comp.Name] {
			continue
		}
		for _, other := range byName[comp.Name] {
			if other == comp {
				continue
			}
			later, earlier := comp, other
			if comparePos(compDeclPos(other), compDeclPos(comp)) > 0 {
				later, earlier = other, comp
			}
			reported[comp.Name] = true
			c.error(compDeclPos(later), "%q is declared twice in this package and both declarations recurse, so neither is inlined away and the two would emit one host component; a body-local component is not yet renamed per body (see #198) (other declaration at %s)", comp.Name, compDeclPos(earlier))
			break
		}
	}
}

// comparePos orders two positions by file, then line, then column.
func comparePos(a, b ast.Pos) int {
	if a.File != b.File {
		return strings.Compare(a.File, b.File)
	}
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return a.Column - b.Column
}

// recursiveComponents is every component of pkg that reaches itself through
// the components it instantiates. Those are the ones passNoInlineComponents
// leaves standing, so they are the ones whose declared name a backend emits.
func recursiveComponents(pkg *ir.Package) map[*ir.Component]bool {
	own := make(map[*ir.Component]bool, len(pkg.Components))
	for _, comp := range pkg.Components {
		own[comp] = true
	}
	// ir.Walk does not descend a NodeInst's Component -- that is a reference
	// edge, not ownership -- so one walk per component gives the direct edges.
	edges := make(map[*ir.Component]map[*ir.Component]bool, len(pkg.Components))
	for _, comp := range pkg.Components {
		out := map[*ir.Component]bool{}
		_ = ir.Walk(comp, func(n ir.Node) error {
			if ni, ok := n.(*ir.NodeInst); ok && ni.Component != nil && own[ni.Component] {
				out[ni.Component] = true
			}
			return nil
		})
		edges[comp] = out
	}
	out := map[*ir.Component]bool{}
	for _, comp := range pkg.Components {
		seen := map[*ir.Component]bool{}
		stack := slices.Collect(maps.Keys(edges[comp]))
		for len(stack) > 0 {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if next == comp {
				out[comp] = true
				break
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			stack = append(stack, slices.Collect(maps.Keys(edges[next]))...)
		}
	}
	return out
}
