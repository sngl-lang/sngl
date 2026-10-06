package lib_test

import (
	"slices"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// TestBuiltinVocabularyIsDeclared holds each #[builtin] declaration to the
// props, events and method ids compiler code reads off it by name
// (ir.BuiltinVocabulary). A rename in lib/ alone would leave every reader
// finding nothing, and falling back to a default without a word.
func TestBuiltinVocabularyIsDeclared(t *testing.T) {
	found := map[ir.BuiltinKind]bool{}
	for _, name := range lib.Packages() {
		pkg := checker.LibPackage(name)
		if pkg == nil {
			continue
		}
		for _, c := range pkg.Components {
			v, ok := ir.BuiltinVocabulary[c.Builtin]
			if !ok {
				continue
			}
			found[c.Builtin] = true
			for _, p := range v.Props {
				if !slices.ContainsFunc(c.Props, func(x *ir.Prop) bool { return x.Name == p }) {
					t.Errorf("%s (%s) declares no prop %q, which the compiler reads", c.Name, c.Builtin, p)
				}
			}
			for _, e := range v.Events {
				if !slices.ContainsFunc(c.Events, func(x *ir.EventDecl) bool { return x.Name == e }) {
					t.Errorf("%s (%s) declares no event %q, which the compiler reads", c.Name, c.Builtin, e)
				}
			}
			for _, id := range v.Methods {
				if c.MethodByIntrinsic(id) == nil {
					t.Errorf("%s (%s) has no method marked %q, which the compiler calls", c.Name, c.Builtin, id)
				}
			}
		}
	}
	for kind := range ir.BuiltinVocabulary {
		if !found[kind] {
			t.Errorf("no library declaration carries %s", kind)
		}
	}
}
