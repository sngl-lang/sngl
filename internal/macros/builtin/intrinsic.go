package builtin

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

// bodyUsableFlag is the one accepted second argument to #[intrinsic]. It is a
// flag rather than a boolean literal so the mark reads as a claim about the
// declaration: `#[intrinsic("IntMin", usable)]`.
const bodyUsableFlag = "usable"

func init() {
	// #[intrinsic("...")] lives in the same macro package as #[builtin], so a
	// library file that marks compiler magic needs one import for all of it.
	expand.RegisterPre("internal/builtin", "intrinsic", []expand.Param{
		{Name: "id", Kind: expand.ArgString},
		{Name: "body", Kind: expand.ArgIdent, Optional: true},
	}, intrinsicHandler)
}

// intrinsicHandler implements #[intrinsic("Id")] and #[intrinsic("Id", usable)],
// stamping the id onto the function it annotates.
//
// The id is what a backend matches to emit a native implementation. The
// `usable` flag says the SNGL body computes the same result, so a backend that
// does not implement the id may emit the body instead; without it the
// declaration is a signature only, and a backend that cannot emit the id has
// nothing to fall back to and must say so.
func intrinsicHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	id := args.String("id")
	if id == "" {
		return decl, fmt.Errorf("#[intrinsic] requires a non-empty id")
	}
	bodyUsable := false
	if args.Has("body") {
		flag := args.Ident("body")
		if flag != bodyUsableFlag {
			return decl, fmt.Errorf("unknown #[intrinsic] flag %q (the only flag is %s)", flag, bodyUsableFlag)
		}
		bodyUsable = true
	}
	taggable, ok := decl.(ast.IntrinsicTaggable)
	if !ok {
		return decl, fmt.Errorf("#[intrinsic(%q)] cannot mark %T", id, decl)
	}
	taggable.SetIntrinsic(id, bodyUsable)
	return decl, nil
}
