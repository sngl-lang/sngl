package ast

import "fmt"

// TreeMark is what #[tree.kind] and #[tree.children] record about a
// component: which segmented tree it belongs to, and which one its children
// must belong to. Both are opaque names — the tree mechanism knows nothing
// about shapes, inlines or menu items, only that a name matches or does not.
type TreeMark struct {
	// Kind names the tree this component is a member of. Empty for an
	// ordinary component.
	Kind string `json:",omitempty"`
	// Children names the tree this component's children must be members of.
	// Empty means unrestricted; the checker fills it in from Kind when a
	// marked component declares no children type of its own.
	Children string `json:",omitempty"`
}

// TreeTaggable is a declaration form that can carry the tree marks. As with
// OptionsTaggable, the macro asserts the interface rather than switching on
// the declaration kind, so which forms may carry a mark is a property of the
// AST.
//
// A component is the only form with a design: a tree kind classifies a node in
// a visual tree, and no other declaration appears in one.
type TreeTaggable interface {
	SetTreeKind(kind string) error
	SetTreeChildren(kind string) error
}

// SetTreeKind marks the component as a member of the named tree.
func (c *ComponentDecl) SetTreeKind(kind string) error {
	if c.Tree.Kind != "" {
		return fmt.Errorf("already a %q node", c.Tree.Kind)
	}
	c.Tree.Kind = kind
	return nil
}

// SetTreeChildren restricts the component's children to the named tree.
func (c *ComponentDecl) SetTreeChildren(kind string) error {
	if c.Tree.Children != "" {
		return fmt.Errorf("children are already restricted to %q", c.Tree.Children)
	}
	c.Tree.Children = kind
	return nil
}
