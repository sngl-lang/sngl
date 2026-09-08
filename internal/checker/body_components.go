package checker

import (
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// reportBodyComponentCollisions reports a body-local component whose emitted
// name another declaration of this package would also emit. Asked of the
// recursion cycles only, since those are the components passNoInlineComponents
// leaves standing. Interim, removed by #198.
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
	// One report per colliding name, at whichever declaration came second.
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

// reportBodyComponentCapture reports a body-local component that captures its
// owner's state and recurses: nothing splices a cycle, so its surviving render
// would name a var only an instance of the owner has. Same shape as
// reportBodyComponentCollisions -- a codegen limitation, not a language rule.
func (c *checker) reportBodyComponentCapture() {
	if c.pkg == nil || len(c.bodyComps) == 0 {
		return
	}
	surviving := recursiveComponents(c.pkg)
	owners := ir.BodyOwners(c.pkg)
	for _, comp := range c.bodyComps {
		if !surviving[comp] || !ir.CapturesEnclosingState(comp, owners) {
			continue
		}
		c.error(compDeclPos(comp), "%q recurses and reads the state of the body it is declared in, so it is not inlined away and the capture has no instance to read from; give it a prop instead", comp.Name)
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
// the components it instantiates -- the ones a backend emits by name.
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
