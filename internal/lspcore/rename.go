package lspcore

import (
	"fmt"
	"slices"
	"unicode"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
)

// Edit replaces Len runes at Pos with Text.
type Edit struct {
	Pos  ast.Pos
	Len  int
	Text string
}

// CanRename says why the declaration r names cannot be renamed here, or nil.
func CanRename(ix *Index, r *Ref) error {
	switch {
	case ix.Library[r.Decl]:
		return fmt.Errorf("it is declared in the library")
	case r.Kind == KindTypeParameter:
		return fmt.Errorf("a type parameter's uses are not indexed")
	}
	for _, ref := range ix.RefsTo(r.Decl) {
		if ref.IsDecl {
			return nil
		}
	}
	return fmt.Errorf("it is not declared in this package")
}

// RenameEdits is the edits, by file, that rename the declaration r names, and
// every name for it, to name. A shorthand field, `{a}`, is written out so
// that it goes on naming the other of its two: `{b=a}` when the field is
// renamed, `{a=b}` when the value is.
func RenameEdits(ix *Index, r *Ref, name string) (map[string][]Edit, error) {
	if err := CanRename(ix, r); err != nil {
		return nil, err
	}
	if !isName(name) {
		return nil, fmt.Errorf("%q is not a name", name)
	}
	out := map[string][]Edit{}
	for _, ref := range ix.RefsTo(r.Decl) {
		text := name
		if ix.Shorthands[ref.Pos] {
			old := ""
			for _, other := range ix.Refs {
				if other.Pos == ref.Pos {
					old = ix.text(other)
					break
				}
			}
			if ref.Kind == KindField {
				text = name + "=" + old
			} else {
				text = old + "=" + name
			}
		}
		out[ref.Pos.File] = append(out[ref.Pos.File], Edit{Pos: ref.Pos, Len: ref.Len, Text: text})
	}
	return out, nil
}

func isName(s string) bool {
	if s == "" || slices.Contains(parser.Keywords(), s) {
		return false
	}
	for i, r := range s {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
