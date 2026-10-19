package openapi

import (
	"strconv"
	"strings"
	"unicode"

	"duckfam.us/sngl/internal/parser"
)

// names hands out SNGL names in one namespace, each once. A name SNGL's
// grammar refuses, or one already taken, is changed, and the change is said.
// Two names differing only in their first letter's case are one name: Go
// exports both, `petList` and `PetList`, as the same identifier.
type names struct {
	taken map[string]bool
	// fields are each struct's own namespace.
	fields map[*Schema]*names
}

func newNames(reserved []string) *names {
	n := &names{taken: map[string]bool{}, fields: map[*Schema]*names{}}
	for _, r := range reserved {
		n.taken[capital(r)] = true
	}
	return n
}

// claim takes want, or the first free name made from it, telling renamed
// when the name it answers is not the one asked for.
func (n *names) claim(want string, renamed func(from, to string)) string {
	got := want
	if parser.LookupIdent(got) != parser.IDENT {
		got += "_"
	}
	if n.taken[capital(got)] {
		base := got
		for i := 2; n.taken[capital(got)]; i++ {
			got = base + strconv.Itoa(i)
		}
	}
	n.taken[capital(got)] = true
	if got != want && renamed != nil {
		renamed(want, got)
	}
	return got
}

// claimType names a declaration the package writes: a schema, the struct a
// query is, or one an inline schema became.
func (n *names) claimType(key string, r *reader) string {
	want := capital(camel(key))
	return n.claim(want, func(from, to string) {
		r.note("%s is declared as %s: %s is taken or reserved", key, to, from)
	})
}

// claimFunc names a function or a component.
func (n *names) claimFunc(want string, r *reader) string {
	return n.claim(want, func(from, to string) {
		r.note("%s is declared as %s: the name is taken or reserved", from, to)
	})
}

// field names a property of decl.
func (n *names) field(decl *Schema, wire, owner string, r *reader) string {
	f := n.fields[decl]
	if f == nil {
		f = newNames(nil)
		n.fields[decl] = f
	}
	return f.claim(camel(wire), func(from, to string) {
		r.note("field %s of %s is %s", wire, owner, to)
	})
}

// camel is a wire name as a SNGL identifier: each run of letters and digits
// after the first starts upper case, `front_default` → `frontDefault`, and
// the first keeps its case. A name starting with a digit is given a letter
// to start with, since `_` would make it unexported.
func camel(s string) string {
	var b strings.Builder
	upper := false
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) || r > unicode.MaxASCII {
			upper = b.Len() > 0
			continue
		}
		if b.Len() == 0 && unicode.IsDigit(r) {
			b.WriteByte('n')
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// capital is s with its first letter upper case: a type's name.
func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
