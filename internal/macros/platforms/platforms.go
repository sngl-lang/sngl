// Package platforms registers the marks a platform or language package uses to
// say what its declarations mean to the compiler. It is macro-only: there is no
// lib/platforms directory, only the per-target packages under it, so
// sngl://platforms contributes vocabulary rather than declarations.
//
// The vocabulary is public. A platform plugin living outside this repository
// writes the same marks its in-tree siblings do, so the package is imported
// like any other — not gated behind the internal/ tier.
//
// Today it holds #[options]. The marks that describe a native primitive
// (#[wildcard], #[primitive]) belong here too and register alongside it.
package platforms

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	// Written bare, so it resolves only in a file that dot-imports this
	// package.
	expand.RegisterPre("platforms", "options", nil, optionsHandler)
}

// optionsHandler implements #[options], which says the struct it annotates is
// a target's build-option schema — the fields an `output(...)` block may name.
//
// The mark, not the declaration's name, is what the compiler keys on: every
// site that looks up an options schema finds the marked struct, so a target may
// call the struct whatever it likes and a struct incidentally named Options is
// not one.
func optionsHandler(_ expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	taggable, ok := decl.(ast.OptionsTaggable)
	if !ok {
		return decl, fmt.Errorf("#[options] cannot mark %T; only a struct declares an options schema", decl)
	}
	if err := taggable.SetOptions(); err != nil {
		return decl, fmt.Errorf("#[options]: %w", err)
	}
	return decl, nil
}
