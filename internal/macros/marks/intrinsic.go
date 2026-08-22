package marks

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

// The flags #[intrinsic] accepts after the id. They are bare idents rather
// than booleans so the mark reads as a list of claims about the declaration:
// `#[intrinsic("ListPush", usable, mutates, mutatesReceiver)]`.
const (
	// flagUsable says the SNGL body computes the same result the native
	// implementation would, so a backend that does not implement the id may
	// emit the body instead. Without it the declaration is a signature only.
	flagUsable = "usable"
	// flagMutates says a call has an effect beyond its result, so it survives
	// dead-code elimination and is never folded at compile time.
	flagMutates = "mutates"
	// flagReadonly says a call's result depends on host state — the clock, the
	// environment, the filesystem — so it must not be folded either, though it
	// has no effect of its own.
	flagReadonly = "readonly"
	// flagMutatesReceiver says the first argument is modified in place. It is
	// separate from mutates: an effect on the world and an effect on the
	// receiver are different facts, and reactivity needs the second one to
	// treat a statement-level call as a write to the receiver's variable.
	flagMutatesReceiver = "mutatesReceiver"
)

var intrinsicFlags = []string{flagUsable, flagMutates, flagReadonly, flagMutatesReceiver}

func init() {
	// #[intrinsic("...")] lives in the same macro package as #[builtin], so a
	// library file that marks compiler magic needs one import for all of it.
	expand.RegisterPre("internal/marks", "intrinsic", []expand.Param{
		{Name: "id", Kind: expand.ArgString},
		{Name: "flags", Kind: expand.ArgIdent, Variadic: true, Enum: intrinsicFlags},
	}, intrinsicHandler)
}

// intrinsicHandler implements #[intrinsic("Id", flags...)], stamping the id and
// what the compiler needs to know about the call onto the function.
//
// The id is what a backend matches to emit a native implementation. The flags
// carry the facts that have no other surface in SNGL: whether the body is a
// usable definition, whether a call has effects, and whether it writes through
// its receiver.
func intrinsicHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	id := args.String("id")
	if id == "" {
		return decl, fmt.Errorf("#[intrinsic] requires a non-empty id")
	}
	flags := args.Idents("flags")
	for i, f := range flags {
		if slices.Contains(flags[:i], f) {
			return decl, fmt.Errorf("#[intrinsic(%q)] repeats flag %s", id, f)
		}
	}
	if slices.Contains(flags, flagMutates) && slices.Contains(flags, flagReadonly) {
		return decl, fmt.Errorf("#[intrinsic(%q)] is both %s and %s; a call either has an effect or only reads host state",
			id, flagMutates, flagReadonly)
	}
	taggable, ok := decl.(ast.IntrinsicTaggable)
	if !ok {
		return decl, fmt.Errorf("#[intrinsic(%q)] cannot mark %T", id, decl)
	}
	taggable.SetIntrinsic(ast.IntrinsicMark{
		ID:              id,
		BodyUsable:      slices.Contains(flags, flagUsable),
		Mutates:         slices.Contains(flags, flagMutates),
		Readonly:        slices.Contains(flags, flagReadonly),
		MutatesReceiver: slices.Contains(flags, flagMutatesReceiver),
	})
	return decl, nil
}
