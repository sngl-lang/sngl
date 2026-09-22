package ir

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

// IsBuildTargetTree reports whether sd is `build.language` or `build.platform`
// -- the families a target package's own node is a member of.
//
// By package and name rather than by a mark, because neither carries one: the
// three trees the compiler marks are the ones it has to name for itself, and
// this one it reaches through the declaration it is asking about.
func IsBuildTargetTree(sd *StructDef) bool {
	return sd != nil && sd.Pkg == BuildTreePkg && (sd.Name == "language" || sd.Name == "platform")
}
