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
// it, and that includes what a value in that shape is *spelled* as: a program
// reading "{width}" gets the same string on every target. What belongs to a
// platform is a spelling its host demands for its own reasons -- html's CSS
// wants the base named `pct` written "%", and that rename lives in
// internal/htmlutil, off this path entirely.

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

// UnitTermSep joins the per-base terms of a displayed unit value. A value is
// a magnitude per base and a multi-base one has no single number, so what it
// displays as is the sum it would have been written as: "3px + 2em".
const UnitTermSep = " + "

// FormatUnitTerm renders one base's magnitude as it is displayed: (3, "px")
// is "3px". Every target spells a unit value by joining these, so the two
// compile-time callers here and the three runtime helpers each backend emits
// are all restating this one rule.
func FormatUnitTerm(mag float64, base string) string {
	return FormatUnitMagnitude(mag) + base
}

// FormatUnitZero is what a value carrying no magnitude at all displays as.
// The first declared base, not the base the value was written in: a record of
// zeros has no memory of its spelling, and 0rem and 0px are the same value,
// so they have to print the same.
func FormatUnitZero(u *UnitDef) string {
	bases := u.Bases()
	if len(bases) == 0 {
		return "0"
	}
	return FormatUnitTerm(0, bases[0].Name)
}
