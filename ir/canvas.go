package ir

// A drawing is a tree, and what makes a canvas a canvas is the family its rest
// slot accepts. These are the facts both the lowering and codegen ask about
// one, which is why they are here and not in either.
//
// There used to be a lowering pass that answered them privately, lifted a
// canvas's shapes out of the tree into a synthesized function and hung it off
// the node. The shapes stay where they were written now; what reads them is
// codegen, the way it reads any other family it renders.
//
// A fourth used to live here: CrossesTreeFamily, which reported a component
// hosting a family other than its own -- a window over widgets, a canvas over
// shapes -- and was where a node id stopped carrying. Both halves of that have
// been answered since. A window *counts*, conferring `option<T>` on a handle
// read from outside it, because the surface may not be open. And a canvas no
// longer stops the hoist at all: the id is ordinary, and it is the *read* that
// passNodePropReads answers with the expression the prop was given, because a
// shape is spliced into the calls that paint it and there is nothing left to
// ask. Refusing the id said the same thing by saying nothing.

// TreeHosted is the segmented tree a component's rest slot accepts, or nil.
//
// A canvas hosts shapes; a rect, being one, hosts its own. The distinction is
// the slot, not the return position, which is what IsShapeContainer puts
// together.
func TreeHosted(comp *Component) *StructDef {
	if sd := RestSlotTree(comp); sd != nil && IsSegmentedTree(sd) {
		return sd
	}
	return nil
}

// RestSlotTree is the family a component's rest slot accepts, whichever family
// that is, and nil for a component that declares no rest slot or whose slot
// names no tree.
func RestSlotTree(comp *Component) *StructDef {
	if comp == nil {
		return nil
	}
	for _, s := range comp.Slots {
		if !s.Rest || s.Content == nil || s.Content.Kind != TypeStruct {
			continue
		}
		if sd, ok := s.Content.Decl.(*StructDef); ok && sd.IsTree {
			return sd
		}
	}
	return nil
}

// IsShapeContainer reports whether a NodeInst hosts shapes without being one:
// a canvas, not a rect. A shape's own children are drawn by the emitter that
// draws it, so only the outermost host is a drawing.
func IsShapeContainer(ni *NodeInst) bool {
	return ni != nil && ni.Component != nil &&
		!IsDrawShapeTree(ni.Component.Tree) &&
		IsDrawShapeTree(TreeHosted(ni.Component))
}

// IsTreelessNode reports whether a node's declaration belongs to no family,
// which is what `#[tree.none]` says and what `effect` and `timer` carry.
//
// Such a node may be placed in any tree, so a drawing holds them beside its
// shapes: an `effect` written in a canvas is the bracket that animates it. It
// paints nothing, so the draw walk keeps it as a child rather than painting
// it, and it is not a shape, so the widget walks skip it too.
func IsTreelessNode(n *NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Tree == nil
}

// WidgetChildren is what a walk over the *widget* tree sees beneath a node.
//
// A canvas's children are shapes, and a shape is not a widget: it has no id to
// address, no prop to patch and nothing to create. The passes that flatten the
// tree into createNode calls, allocate `__nN` refs for it, and turn a branch in
// it into a render slot all stop here.
//
// They never had to before because a lowering pass lifted a canvas's shapes out
// of the tree before any of them ran. Nothing lifts them now, and with the
// shapes left standing html turned the `if` around them into a reactive slot
// and gave two of them widget refs -- which is the whole of what those three
// passes had to be told.
//
// One function rather than the predicate written at each site: this repository
// records three separate cases of a walk being copied per caller and each copy
// forgetting a different arm.
func WidgetChildren(n *NodeInst) []Stmt {
	if IsShapeContainer(n) {
		return nil
	}
	return n.Children
}
