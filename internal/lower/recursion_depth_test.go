package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// boundedSites counts the instantiations of comp that carry the depth bound,
// and the ones that do not, anywhere under root.
func boundedSites(root any, comp *ir.Component) (bounded, bare int) {
	inIf := map[*ir.NodeInst]bool{}
	_ = ir.Walk(root, func(n ir.Node) error {
		x, ok := n.(*ir.If)
		if !ok {
			return nil
		}
		for _, s := range x.Else {
			if inst, ok := s.(*ir.NodeInst); ok {
				inIf[inst] = true
			}
		}
		return nil
	})
	_ = ir.Walk(root, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || inst.Component != comp {
			return nil
		}
		if inIf[inst] {
			bounded++
		} else {
			bare++
		}
		return nil
	})
	return bounded, bare
}

// TestRecursionDepthBoundsASiteInSlotContent is the second half of the named
// slot bug: with the cycle found, the guard still has to reach the site. The
// walk that wrote it stopped at NodeInst.Slots, so a component recursing
// through slot content it supplies to another was detected and then left
// unbounded -- the one shape where the bound is a program's only protection
// from the host's stack.
func TestRecursionDepthBoundsASiteInSlotContent(t *testing.T) {
	panel := &ir.Component{Name: "panel", Body: []ir.Stmt{&ir.SlotInst{Name: "header"}}}
	endless := &ir.Component{Name: "endless"}
	site := &ir.NodeInst{Name: "endless", Component: endless}
	endless.Body = []ir.Stmt{&ir.NodeInst{
		Name:      "panel",
		Component: panel,
		Slots:     map[string]*ir.SlotContent{"header": {Body: []ir.Stmt{site}}},
	}}
	pkg := &ir.Package{Components: []*ir.Component{panel, endless}}

	if err := lowerRecursionDepth(pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerRecursionDepth: %v", err)
	}
	bounded, bare := boundedSites(endless, endless)
	if bounded != 1 || bare != 0 {
		t.Errorf("recursive site in slot content: %d bounded, %d bare; want 1 and 0", bounded, bare)
	}
}

// TestRecursionDepthBoundsEachSiteOnce pins the other side of moving to the
// shared walk: the `if` the guard emits keeps the node in its else, so the
// walk meets each site a second time inside its own replacement and must not
// wrap it again.
func TestRecursionDepthBoundsEachSiteOnce(t *testing.T) {
	self := &ir.Component{Name: "self"}
	self.Body = []ir.Stmt{&ir.NodeInst{Name: "self", Component: self}}
	pkg := &ir.Package{Components: []*ir.Component{self}}

	if err := lowerRecursionDepth(pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerRecursionDepth: %v", err)
	}
	bounded, bare := boundedSites(self, self)
	if bounded != 1 || bare != 0 {
		t.Errorf("self-recursive site: %d bounded, %d bare; want 1 and 0", bounded, bare)
	}
	// One depth arg, not one per visit.
	site := self.Body[0].(*ir.If).Else[0].(*ir.NodeInst)
	depthArgs := 0
	for _, a := range site.Props {
		if a.Name == recursionDepthProp {
			depthArgs++
		}
	}
	if depthArgs != 1 {
		t.Errorf("site carries %d %s args, want 1", depthArgs, recursionDepthProp)
	}
}
