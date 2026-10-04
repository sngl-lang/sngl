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
	// TargetName is `#[gen.name]`: the string a build-target node is known by
	// outside SNGL -- the `--platform html` a command line passes, the name a
	// plugin registers its generator under. The node itself is named
	// `platform` or `language` by convention, so it is this, not the
	// component's name, that a registry and the CLI match on.
	TargetName string `json:",omitempty"`
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

// RenderedSurface is a primitive's claim that each of its nodes is a surface
// of its own -- a toplevel the target draws a render tree in. html's Window
// is one: html's documentsOf writes a document from the first that no `if`
// over state and no `for` can take away, and html shows every other as a
// `<dialog>` in it. A target that answers its toplevels in its own codegen,
// as gtk4 does, need not say so.
const RenderedSurface = "surface"

// IsSurface reports whether comp is a primitive whose nodes are surfaces
// (#[gen.renders(surface)]).
func IsSurface(comp *Component) bool {
	g := GenCapsOf(comp)
	return g != nil && slices.Contains(g.Renders, RenderedSurface)
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

// TargetNode reports the registered name of the build-target node sym is, and
// whether it is one: a component in `build.language` or `build.platform`
// carrying `#[gen.name]`. A reference to one is how a program names a target
// -- `[platform]` on an override, `PLATFORM == html.platform` in a gate -- and
// its value is that name.
func TargetNode(sym Symbol) (*Component, string, bool) {
	c, ok := sym.(*Component)
	if !ok || c.Gen == nil || c.Gen.TargetName == "" || !IsBuildTargetTree(c.Tree) {
		return nil, "", false
	}
	return c, c.Gen.TargetName, true
}

// DisplayName is the name a diagnostic gives a component: a build-target
// node's #[gen.name], since every platform's node is called `platform` and
// "unknown prop on component platform" says nothing about which one, and the
// component's own name otherwise.
func (c *Component) DisplayName() string {
	if _, name, ok := TargetNode(c); ok {
		return name
	}
	return c.Name
}

// EmittedFamily is the family a bodyless host hosts, when that family is
// generated by an override of its own rather than rendered member by member:
// the family is what answers the host. Nil for a component with a body, or
// one whose rest slot takes no family that anything overrides.
func EmittedFamily(host *Component) *Component {
	if host == nil || !host.Bodyless {
		return nil
	}
	f := RestSlotTree(host)
	if f == nil || f == host || (len(f.PlatformOverrides) == 0 && len(f.LanguageOverrides) == 0) {
		return nil
	}
	return f
}

// IsBuildTargetTree reports whether f is `build.language` or `build.platform`
// -- the families a target package's own node is a member of.
func IsBuildTargetTree(f *Component) bool {
	return TargetTier(f) != ""
}

// TargetTier is the tier a build-target family holds -- BuiltinLanguage for
// `build.language`, BuiltinPlatform for `build.platform` -- and "" for any
// other family. Read off the family's mark, so a package declaring a
// `language` family of its own declares a different one.
func TargetTier(f *Component) BuiltinKind {
	switch {
	case isTreeRole(f, BuiltinTreeLanguage):
		return BuiltinLanguage
	case isTreeRole(f, BuiltinTreePlatform):
		return BuiltinPlatform
	}
	return ""
}

// TargetNodeOf is the build-target node of the given tier pkg declares: the
// one component whose family is that tier and that carries #[gen.name]. Found
// by what it is rather than by what it is called, which is a convention.
func TargetNodeOf(pkg *Package, tier BuiltinKind) *Component {
	if pkg == nil {
		return nil
	}
	for _, c := range pkg.Components {
		if node, _, ok := TargetNode(c); ok && TargetTier(node.Tree) == tier {
			return node
		}
	}
	return nil
}
