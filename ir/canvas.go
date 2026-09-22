package ir

// A drawing is a tree, and what makes a canvas a canvas is the family its rest
// slot accepts. These are the three facts both the lowering and codegen ask
// about one, which is why they are here and not in either.
//
// There used to be a lowering pass that answered them privately, lifted a
// canvas's shapes out of the tree into a synthesized function and hung it off
// the node. The shapes stay where they were written now; what reads them is
// codegen, the way it reads any other family it renders.

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

// CrossesTreeFamily reports whether a component hosts a family other than the
// one it belongs to: a `window`, which is a `root` whose children are `node`s,
// and a `canvas`, which is a `node` whose children are `shape`s. A `vbox`
// hosts its own family and does not.
//
// **A family change is where a node id stops carrying.** What a handle names
// is a thing on one rendering surface, and a family change is what a second
// surface looks like from here: two windows are two pages, and a canvas is a
// drawing rather than more widgets.
//
// The two are no longer answered the same way, and this is the half that stops
// outright. A *window* counts instead: a read from another window is
// `option<T>`, because the surface may not be open, which says the same thing
// with a type. A *canvas* has nothing to be optional about -- passShapeDraw
// splices a shape into draw calls before any backend sees the node, so there
// is no handle either way -- and a shape's props do resolve, so a typed
// `dot.r` would check clean and render nothing, which is exactly the silence
// this ended: `ui.text(value="{dot.r}")` beside a canvas holding `circle #dot`
// passed and rendered an empty span, on every target.
//
// So declareNodeIDsStmt asks after a window first and reaches this for every
// other crossing.
//
// Asked of the tree rather than of `#[builtin("window")]`, because it is the
// tree's answer -- the mark says which IR construct a declaration dispatches
// to, not what its children can see.
func CrossesTreeFamily(comp *Component) bool {
	hosted := RestSlotTree(comp)
	return hosted != nil && comp != nil && comp.Tree != nil && hosted != comp.Tree
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
// paints nothing, so the draw walk skips it, and it is not a shape, so the
// widget walks skip it too.
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
