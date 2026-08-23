// Package marks registers the marks that say what the compiler knows about a
// declaration: #[builtin], which names the IR construct a declaration
// dispatches to, and #[intrinsic], which names the native implementation that
// may replace a function's body.
//
// Unlike user-facing macros (e.g. #[draw.shape]) these annotate declarations in
// the library source itself; the checker runs the pre-expand pass over that
// source so the marks are in place before type-checking.
//
// The path is sngl://internal/marks and not .../builtin: sngl://builtin is
// the ambient package of built-in types, a different thing one letter away at
// the import line.
//
// See docs/superpowers/specs/2026-08-18-builtins-stdlib-split-design.md.
package marks

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	// #[builtin("...")] is written bare, with no package segment, so it
	// resolves only in a file that dot-imports this package.
	expand.RegisterPre("internal/marks", "builtin",
		[]expand.Param{{Name: "kind", Kind: expand.ArgString}}, builtinHandler)
}

// builtinHandler implements #[builtin("kind")], stamping the built-in kind onto
// the declaration it annotates.
//
// It does not know which kinds go on which declaration forms: it asserts
// ast.BuiltinTaggable and lets the AST say what can carry a mark. What a given
// kind then requires — that a node kind names a component, that a const kind
// names a const — is checked where the compiler stores the reference, because
// that is where the requirement comes from.
//
// The mark, rather than the declaration's name, is what the compiler keys on:
// resolveNamedType returns the canonical singleton (or runs the generic
// construction) for the marked decl and the plain user type otherwise, so a
// user declaration shadows a built-in like any other name.
func builtinHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	raw := args.String("kind")
	kind := ast.BuiltinKind(raw)
	if !kind.Valid() {
		return decl, fmt.Errorf("unknown builtin kind %q (valid: %s)", raw, strings.Join(kindNames(), ", "))
	}
	taggable, ok := decl.(ast.BuiltinTaggable)
	if !ok {
		return decl, fmt.Errorf("#[builtin(%q)] cannot mark %T", raw, decl)
	}
	taggable.SetBuiltin(kind)
	return decl, nil
}

func kindNames() []string {
	all := ast.AllBuiltinKinds()
	names := make([]string, len(all))
	for i, k := range all {
		names[i] = string(k)
	}
	return names
}
