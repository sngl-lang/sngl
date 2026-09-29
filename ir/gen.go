package ir

import "slices"

// GenCaps is what the `sngl:x/gen` marks said about a declaration: the
// constructs a target emits natively, the ones it refuses, and the lowering
// passes it asks for.
//
// The names are as written, already checked against the enums the marks
// declare, and nothing here knows what any of them mean. Mapping a name to the
// pass it gates is the lowering's, which is the seam that lets the vocabulary
// live in SNGL source while the passes stay Go: `ir` would otherwise hold a
// second copy of a list it has no use for.
type GenCaps struct {
	Can    []string `json:",omitempty"`
	Cannot []string `json:",omitempty"`
	Wants  []string `json:",omitempty"`
	// Renders is `#[gen.renders]`: what the nodes an `#[intrinsic]` primitive
	// draws support. The other three describe a *target* and are read off a
	// build-tree node; this describes one primitive, which is the finer grain
	// the tier exists for.
	Renders []string `json:",omitempty"`
}

// RenderedIdentity is the one thing a primitive can claim about its nodes: a
// `#id` on one names something still there while the program runs.
const RenderedIdentity = "identity"

// HoldsIdentity reports whether comp is a primitive whose rendered nodes can be
// read back at run time.
//
// False for everything that says nothing, which is the tier's polarity and the
// answer that leaves a program working: a prop read off a node with no runtime
// identity is answered at build time with the expression the prop was given,
// where asking the host would name a field nothing declares.
func HoldsIdentity(comp *Component) bool {
	g := GenCapsOf(comp)
	if g == nil {
		return false
	}
	return slices.Contains(g.Renders, RenderedIdentity)
}

// RenderedPrimitive is the `#[intrinsic]` component comp eventually renders, or
// nil when it renders none.
//
// The first one found, depth-first, **through component bodies as well as
// children**. A target's override is rarely one node deep: fyne's `ui.text`
// renders `Label`, which is an ordinary component in `fyne.sngl` carrying a
// `Spec`, and only `Label`'s own body reaches the `Widget` primitive. Reading
// one level answered nil for every widget on that platform, and nil is
// indistinguishable from "renders nothing anyone can hold".
//
// A component's body is the override the active target supplied by the time
// this can be asked, so the chain it walks is the one that build will emit.
// `seen` is for the cycle a recursive component makes: the walk stops there
// rather than at a primitive, which is the honest answer -- nothing in the
// cycle is one.
func RenderedPrimitive(comp *Component) *Component {
	return renderedPrimitive(comp, map[*Component]bool{})
}

func renderedPrimitive(comp *Component, seen map[*Component]bool) *Component {
	if comp == nil || seen[comp] {
		return nil
	}
	// A wildcard counts as a primitive alongside an intrinsic, and html is why:
	// its widgets are not a declaration each but one `element` that every tag
	// resolves to, marked `#[wildcard]` rather than `#[intrinsic]`. Reading
	// only the intrinsic left that platform with no primitive for any widget,
	// which is indistinguishable from a widget that renders nothing -- and the
	// pass that asks then answered a live read at build time.
	if comp.Intrinsic != "" || comp.Wildcard != "" {
		return comp
	}
	seen[comp] = true
	return firstPrimitive(comp.Body, seen)
}

func firstPrimitive(stmts []Stmt, seen map[*Component]bool) *Component {
	for _, s := range stmts {
		ni, ok := s.(*NodeInst)
		if !ok {
			continue
		}
		if c := renderedPrimitive(ni.Component, seen); c != nil {
			return c
		}
		if c := firstPrimitive(ni.Children, seen); c != nil {
			return c
		}
	}
	return nil
}

// GenCapsOf returns what c declared, or nil for a declaration carrying no
// mark — which is not the same as one declaring nothing, and callers that
// answer "every pass runs" for both are correct only because the two agree
// under this polarity.
func GenCapsOf(c *Component) *GenCaps {
	if c == nil {
		return nil
	}
	return c.Gen
}

// BuildTreePkg is the package declaring the two families a build-target node
// belongs to.
const BuildTreePkg = "sngl:build"

// IsBuildTargetTree reports whether f is `build.language` or `build.platform`
// -- the families a target package's own node is a member of.
//
// By package and name rather than by a mark, because neither carries one: the
// three trees the compiler marks are the ones it has to name for itself, and
// this one it reaches through the declaration it is asking about.
func IsBuildTargetTree(f *Component) bool {
	return f.IsFamily() && f.Pkg == BuildTreePkg && (f.Name == "language" || f.Name == "platform")
}
