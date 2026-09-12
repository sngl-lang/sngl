package ir

import (
	"strconv"
	"strings"
)

// A unit value is a magnitude per base. `unit measurement { px, em, rem =
// 16em, vw, vh, pct }` declares five independent bases -- only `rem` reduces,
// to `em` -- so `1px + 2pct` has no single number and must keep both
// magnitudes. `unit duration { ms, s = 1000ms, m = 60s, h = 60m }` declares
// one, so every duration is a number of ms.
//
// The helpers here answer that for a UnitDef so no backend has to re-derive
// it. What a value in that shape is *spelled* as belongs to the platform: the
// magnitude is 7 and the base is px, and only html knows that CSS wants
// "7px" and that the base named `pct` is spelled "%".

// Bases returns u's base suffixes in declaration order -- the ones that
// reduce to themselves, and so the ones a value of u carries a magnitude for.
func (u *UnitDef) Bases() []*UnitSuffix {
	if u == nil {
		return nil
	}
	var out []*UnitSuffix
	for _, s := range u.Suffixes {
		if s.IsBase() {
			out = append(out, s)
		}
	}
	return out
}

// SuffixByName returns the named suffix of u, or nil.
func (u *UnitDef) SuffixByName(name string) *UnitSuffix {
	if u == nil {
		return nil
	}
	for _, s := range u.Suffixes {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// IsSingleBase reports whether every suffix of u reduces to one common base,
// which is what lets a value of u be a plain number rather than a record.
func (u *UnitDef) IsSingleBase() bool { return len(u.Bases()) == 1 }

// UnitDeclOf returns the UnitDef behind a unit-typed t, or nil.
func UnitDeclOf(t *Type) *UnitDef {
	if t == nil || t.Kind != TypeUnit {
		return nil
	}
	ud, _ := t.Decl.(*UnitDef)
	return ud
}

// UnitMagnitude reduces a unit literal to the magnitude and base it stands
// for: 1rem is (16, "em") because `rem = 16em`, and 1s is (1000, "ms").
// Reports false when lit is not a unit literal or carries no resolvable
// suffix, in which case the caller has nothing to reduce and should leave the
// literal alone.
func UnitMagnitude(lit *Literal) (mag float64, base string, ok bool) {
	if lit == nil || lit.Type == nil || lit.Type.Kind != TypeUnit || lit.Suffix == "" {
		return 0, "", false
	}
	ud := UnitDeclOf(lit.Type)
	suf := ud.SuffixByName(lit.Suffix)
	if suf == nil {
		return 0, "", false
	}
	// Value is the number alone -- the suffix is Suffix. A literal may carry
	// `_` digit separators, which no host language's parser accepts.
	n, err := strconv.ParseFloat(strings.ReplaceAll(lit.Value, "_", ""), 64)
	if err != nil {
		return 0, "", false
	}
	return n * suf.Factor, suf.BaseName, true
}

// FormatUnitMagnitude renders a magnitude as source text, preferring integer
// form when the value is whole so `7px` reads as 7 rather than 7.000000.
func FormatUnitMagnitude(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Fielded is a declaration whose members include named fields. Struct and unit
// are the two -- a struct's fields are written down and a unit's are its bases
// -- so a member lookup need not know which it is holding.
type Fielded interface {
	Symbol
	FieldList() []*StructField
	MethodTable() map[string]*Func
}

// MethodTable completes Fielded for a struct.
func (s *StructDef) MethodTable() map[string]*Func { return s.Methods }

// MethodTable completes Fielded for a unit.
func (u *UnitDef) MethodTable() map[string]*Func { return u.Methods }

// UnitFields is the member table a UnitDef carries: one float field per base
// suffix, in declaration order, and none at all for a single-base unit.
func UnitFields(u *UnitDef) []*StructField {
	if u == nil || u.IsSingleBase() {
		return nil
	}
	bases := u.Bases()
	out := make([]*StructField, len(bases))
	for i, b := range bases {
		out[i] = &StructField{Name: b.Name, Type: TypFloat}
	}
	return out
}
