package ast

import "fmt"

// ForeignMark is what #[foreign("scheme://pkg", "Name", flags...)] records:
// the language whose name this is, the package it lives in there, the name to
// emit, and the claims about a call that SNGL cannot see for itself. A mark
// that gives a bare name leaves Scheme and Path empty — a struct field has no
// package of its own, and neither does a declaration whose target language
// spells it without qualification.
//
// It is not a foreign declaration's identity: several SNGL types map to one
// target-language type, so a mark is a codegen fact and nothing more. The
// zero value means "not foreign".
type ForeignMark struct {
	Scheme, Path, Name string
	// Pure says a call has no effects and no dependence on host state, so the
	// compiler may evaluate one at build time. It is asserted, not inferred: a
	// foreign declaration's SNGL body describes the foreign function rather
	// than implementing it, so an unmarked one is left unknown.
	Pure bool
	// Async says the foreign declaration returns a promise its caller awaits,
	// the same fact the TypeScript importer reads off Promise<T>.
	Async bool
}

// ForeignTaggable is a declaration form that can carry #[foreign(...)]. As
// with BuiltinTaggable, the macro asserts this interface rather than switching
// on the declaration kind, so which forms can carry a mark is a property of
// the AST. The three forms here are the ones the feature specifies: a struct
// and its fields, whose foreign names codegen spells, and a function, whose
// flags say what a call costs. The other declaration forms have no design and
// no fixture, so they refuse the mark rather than carry it nowhere.
type ForeignTaggable interface {
	SetForeign(ForeignMark) error
}

func (s *StructDef) SetForeign(m ForeignMark) error   { return setForeign(&s.Foreign, m) }
func (f *StructField) SetForeign(m ForeignMark) error { return setForeign(&f.Foreign, m) }
func (f *FuncDef) SetForeign(m ForeignMark) error     { return setForeign(&f.Foreign, m) }

// setForeign records a mark on a declaration that has none. A second mark is
// an error rather than an overwrite: the mark says what a declaration *is*
// outside SNGL, and a declaration is one thing.
func setForeign(dst *ForeignMark, m ForeignMark) error {
	if *dst != (ForeignMark{}) {
		return fmt.Errorf("already marked as %q", dst.Name)
	}
	*dst = m
	return nil
}
