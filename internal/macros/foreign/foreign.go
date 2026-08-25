// Package foreign registers #[foreign], the mark that says a declaration
// corresponds to something outside SNGL.
//
// It resolves no path, imports no module, and asserts no correspondence the
// compiler could check — a plugin's output is trusted. Its `pure` flag is the
// sharp edge in that bargain: it lets the compiler evaluate a call at build
// time, so a function wrongly marked pure runs during a build.
//
// It confers no type identity: several SNGL types can map to one
// target-language type (int8 and int16 are both a JS number), so two
// declarations naming the same foreign thing stay two types. Only a scheme
// importer's own Origin unifies declarations, and a mark never sets one.
//
// A marked declaration stays the program's own — a backend emits it — so the
// mark's name is a name to spell alongside that declaration and never a
// reference redirecting to one the backend did not emit.
package foreign

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/imports"
)

// The flags #[foreign] accepts after the name. They are the claims about a
// call that SNGL cannot read off a foreign declaration and must not infer from
// the SNGL body, which describes the foreign function rather than implementing
// it.
const (
	// flagPure says a call has no effects and no dependence on host state, so
	// the compiler may evaluate one at build time. Without it a call is left
	// unknown and always survives to runtime.
	flagPure = "pure"
	// flagAsync says the declaration returns a promise its caller awaits — the
	// fact the TypeScript importer reads off Promise<T>.
	flagAsync = "async"
)

var foreignFlags = []string{flagPure, flagAsync}

func init() {
	expand.RegisterPre("std", "foreign", []expand.Param{
		{Name: "path", Kind: expand.ArgString},
		{Name: "name", Kind: expand.ArgString, Optional: true},
		{Name: "flags", Kind: expand.ArgIdent, Variadic: true, Enum: foreignFlags},
	}, handler)
}

// Two arguments are the import path the declaration comes from and its name
// there, written the way a program's own import line writes them. One argument
// is the name alone: that is all a struct field can say, since a field has no
// package of its own, and all a declaration that only renames needs. The flags
// that follow state what a call costs.
//
// Nothing here inspects the two strings beyond needing a name to set. The path
// is not resolved, the scheme is not looked up, and the name is not checked to
// exist — a plugin's output is trusted.
func handler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	path, name := args.String("path"), args.String("name")
	if !args.Has("name") {
		path, name = "", path
	}
	if name == "" {
		return decl, fmt.Errorf("#[foreign] requires a non-empty name")
	}
	flags := args.Idents("flags")
	for i, f := range flags {
		if slices.Contains(flags[:i], f) {
			return decl, fmt.Errorf("#[foreign(%q)] repeats flag %s", name, f)
		}
	}
	if _, isFunc := decl.(*ast.FuncDef); len(flags) > 0 && !isFunc {
		return decl, fmt.Errorf("#[foreign(%q)] carries %s, which describes a call; %T has none", name, flags[0], decl)
	}
	taggable, ok := decl.(ast.ForeignTaggable)
	if !ok {
		return decl, fmt.Errorf("#[foreign(%q)] cannot mark %T", name, decl)
	}
	if n := names(decl); n > 1 {
		return decl, fmt.Errorf("#[foreign(%q)] marks %d names at once; one foreign name cannot stand for several declarations", name, n)
	}
	scheme, pkgPath := imports.ParseScheme(path)
	err := taggable.SetForeign(ast.ForeignMark{
		Scheme: scheme,
		Path:   pkgPath,
		Name:   name,
		Pure:   slices.Contains(flags, flagPure),
		Async:  slices.Contains(flags, flagAsync),
	})
	if err != nil {
		return decl, fmt.Errorf("#[foreign(%q)]: %w", name, err)
	}
	return decl, nil
}

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
