package ast

import "fmt"

// OptionsTaggable is a declaration form that can carry #[options], the mark
// that says a struct declares a target's build-option schema. As with
// ForeignTaggable, the macro asserts this interface rather than switching on
// the declaration kind, so which forms may carry the mark is a property of the
// AST.
//
// A struct is the only form with a design: an option is a named, typed field
// with a default, and `output(...)` arguments are checked against those fields.
// No other declaration form has fields to check against, so they refuse the
// mark rather than carry it nowhere.
type OptionsTaggable interface {
	SetOptions() error
}

// SetOptions marks the struct as an options schema. A second mark is an error
// rather than a no-op, for the same reason a second #[foreign] is: the mark
// says what the declaration is, and a declaration is one thing.
func (s *StructDef) SetOptions() error {
	if s.Options {
		return fmt.Errorf("already marked as an options schema")
	}
	s.Options = true
	return nil
}
