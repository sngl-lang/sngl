package ir

// BuilderIntrinsic is sngl:builtin's `builder`: text built a piece at a time.
// It is an intrinsic struct with no fields, so to the checker an ordinary
// value; what holds the text is each target's.
const BuilderIntrinsic = "builtin:builder"

// The builder's methods, as their intrinsic ids.
const (
	BuilderAdd    = "builder.add"
	BuilderLine   = "builder.line"
	BuilderString = "builder.string"
)

// IsBuilder reports whether t is sngl:builtin's builder.
func IsBuilder(t *Type) bool {
	if t == nil || t.Kind != TypeStruct {
		return false
	}
	def, ok := t.Decl.(*StructDef)
	return ok && def != nil && def.Intrinsic == BuilderIntrinsic
}

// HoldsBuilder reports whether a value of t holds a builder anywhere: as
// itself, an element, or a field.
func HoldsBuilder(t *Type) bool {
	return holdsBuilder(t, map[*StructDef]bool{})
}

func holdsBuilder(t *Type, seen map[*StructDef]bool) bool {
	if t == nil {
		return false
	}
	if IsBuilder(t) {
		return true
	}
	for _, e := range t.Elems {
		if holdsBuilder(e, seen) {
			return true
		}
	}
	if def := CopiedStruct(t); def != nil && !seen[def] {
		seen[def] = true
		for _, f := range CopiedFields(t) {
			if holdsBuilder(f.Type, seen) {
				return true
			}
		}
	}
	return false
}
