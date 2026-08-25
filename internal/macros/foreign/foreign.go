// Package foreign registers #[foreign], the mark that says a declaration
// corresponds to something outside SNGL.
//
// It lives in sngl://std rather than sngl://internal/marks because its users
// are outside the compiler: a language plugin that describes a foreign API by
// generating marked SNGL source, a platform or language package declaring the
// host types it emits against, and user code doing the same by hand.
//
// The mark informs codegen and nothing else. It resolves no path, imports no
// module, and asserts no correspondence the compiler could check — a plugin's
// output is trusted. It does not confer type identity either: several SNGL
// types can map to one target-language type (int8 and int16 are both a JS
// number), so two declarations naming the same foreign thing stay two types.
// Only a scheme importer's own Origin unifies declarations, and a mark never
// sets one.
package foreign

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	expand.RegisterPre("std", "foreign",
		[]expand.Param{{Name: "ref", Kind: expand.ArgString}}, handler)
}

// handler implements #[foreign("...")], stamping the correspondence onto the
// declaration it annotates.
//
// The argument is written the way every other foreign reference in the
// compiler is — the scheme, the package and the name a value encoder writes
// and ir.ParseNativeDeclRef reads — so a plugin emitting an import line and a
// mark for the same declaration spells the target once.
//
// A bare name with no scheme is the short form: it sets the name to emit and
// nothing else. That is all a struct field can say, since a field has no
// package of its own.
func handler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	raw := args.String("ref")
	mark, err := parse(raw)
	if err != nil {
		return decl, err
	}
	taggable, ok := decl.(ast.ForeignTaggable)
	if !ok {
		return decl, fmt.Errorf("#[foreign(%q)] cannot mark %T", raw, decl)
	}
	if n := names(decl); n > 1 {
		return decl, fmt.Errorf("#[foreign(%q)] marks %d names at once; one foreign name cannot stand for several declarations", raw, n)
	}
	taggable.SetForeign(mark)
	return decl, nil
}

func parse(raw string) (ast.ForeignMark, error) {
	if raw == "" {
		return ast.ForeignMark{}, fmt.Errorf("#[foreign] requires a non-empty name")
	}
	if !strings.Contains(raw, "://") {
		return ast.ForeignMark{Name: raw}, nil
	}
	ref, ok := ir.ParseNativeDeclRef(raw)
	if !ok {
		return ast.ForeignMark{}, fmt.Errorf("#[foreign(%q)]: expected scheme://package#Name, or a bare name", raw)
	}
	return ast.ForeignMark{Scheme: ref.Scheme, Pkg: ref.Path, Name: ref.Name}, nil
}

// names counts the names a declaration binds, for the forms that can bind
// several at once.
func names(decl ast.Stmt) int {
	switch d := decl.(type) {
	case *ast.StructField:
		return len(d.Names)
	case *ast.ConstDecl:
		return specNames(d.Specs)
	case *ast.VarDecl:
		return specNames(d.Specs)
	}
	return 1
}

func specNames(specs []ast.VarSpec) int {
	n := 0
	for _, s := range specs {
		n += len(s.Names)
	}
	return n
}
