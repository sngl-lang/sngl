package ir

// A struct, a list and a map are values: storing one in a place -- a var, a
// field, an element, a local -- stores a copy, so a later write to either
// place leaves the other as it was. Go copies a struct on assignment and
// shares a slice's backing array, JavaScript shares every object, and Kotlin
// shares a list and a data class, so no host gives the rule on its own.
// lower.passValueCopy wraps each value it has to copy in a call of
// ValueCopyIntrinsic, which each language emits from the value's type.

// ValueCopyIntrinsic is the id of the copy passValueCopy writes.
const ValueCopyIntrinsic = "value.copy"

// HoldsValue reports whether a value of t is one a store has to copy: a list,
// a map, a struct the program declares, or an option of one. A builtin struct
// (a color, a date) is never written field by field, and a host's type is
// the host's to copy.
func HoldsValue(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeList, TypeMap:
		return true
	case TypeOption, TypeReadonly:
		return len(t.Elems) > 0 && HoldsValue(t.Elems[0])
	case TypeStruct:
		return CopiedStruct(t) != nil
	}
	return false
}

// CopiedStruct is t's declaration when a copy of t copies its fields: a
// struct the program or a library declares as data, and not a builtin kind,
// an intrinsic or a host type.
func CopiedStruct(t *Type) *StructDef {
	if t == nil || t.Kind != TypeStruct {
		return nil
	}
	def, ok := t.Decl.(*StructDef)
	if !ok || def == nil || def.Builtin != "" || def.Intrinsic != "" {
		return nil
	}
	if def.Foreign.Name != "" && !def.Foreign.Marked {
		return nil
	}
	return def
}

// CopiedField is one field of a struct value and its type at the value's
// type arguments.
type CopiedField struct {
	Field *StructField
	Type  *Type
}

// CopiedFields are the fields of a value of t whose own values a copy of it
// has to copy as well; empty for a struct whose fields are all scalars.
func CopiedFields(t *Type) []CopiedField {
	def := CopiedStruct(t)
	if def == nil {
		return nil
	}
	var bind map[string]*Type
	if len(def.TypeParams) > 0 && len(t.Elems) > 0 {
		bind = map[string]*Type{}
		for i, tp := range def.TypeParams {
			if i < len(t.Elems) {
				bind[tp.Name] = t.Elems[i]
			}
		}
	}
	var out []CopiedField
	for _, f := range def.Fields {
		ft := f.Type
		if bind != nil {
			ft = ft.Substitute(bind)
		}
		if HoldsValue(ft) {
			out = append(out, CopiedField{Field: f, Type: ft})
		}
	}
	return out
}

// SharesNothing reports whether a copy of a value of t is the host's own
// assignment on a target that copies a struct by value: a struct none of
// whose fields holds a list or a map, at any depth.
func SharesNothing(t *Type) bool {
	return sharesNothing(t, map[*StructDef]bool{})
}

func sharesNothing(t *Type, seen map[*StructDef]bool) bool {
	if !HoldsValue(t) {
		return true
	}
	def := CopiedStruct(t)
	if def == nil {
		return false
	}
	if seen[def] {
		return false
	}
	seen[def] = true
	for _, f := range CopiedFields(t) {
		if !sharesNothing(f.Type, seen) {
			return false
		}
	}
	return true
}

// Recursive reports whether a value of the struct type t can hold another
// value of its own type, so an emitter writing its copy out by type has to
// name the copy rather than expand it.
func Recursive(t *Type) bool {
	def := CopiedStruct(t)
	if def == nil {
		return false
	}
	seen := map[*StructDef]bool{}
	for _, f := range CopiedFields(t) {
		if reaches(f.Type, def, seen) {
			return true
		}
	}
	return false
}

func reaches(t *Type, target *StructDef, seen map[*StructDef]bool) bool {
	switch t.Kind {
	case TypeList, TypeOption, TypeReadonly:
		return len(t.Elems) > 0 && reaches(t.Elems[0], target, seen)
	case TypeMap:
		return len(t.Elems) > 1 && reaches(t.Elems[1], target, seen)
	}
	def := CopiedStruct(t)
	if def == nil {
		return false
	}
	if def == target {
		return true
	}
	if seen[def] {
		return false
	}
	seen[def] = true
	for _, f := range CopiedFields(t) {
		if reaches(f.Type, target, seen) {
			return true
		}
	}
	return false
}

// ValueCopy is a call copying e, a value of the type e has.
func ValueCopy(e Expr) *Call {
	t := e.ExprType()
	return &Call{
		Type: t,
		Func: &Func{
			Name:      "copy",
			Intrinsic: ValueCopyIntrinsic,
			Params:    []*Param{{Name: "v", Type: t}},
			Return:    t,
			Purity:    PurityPure,
		},
		Args: []CallArg{{Value: e}},
	}
}

// IsValueCopy reports whether e is a call ValueCopy made.
func IsValueCopy(e Expr) bool {
	c, ok := e.(*Call)
	return ok && c.Func != nil && c.Func.Intrinsic == ValueCopyIntrinsic
}
