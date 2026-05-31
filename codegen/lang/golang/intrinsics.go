package golang

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// langGo is this backend's registry key (matches LanguageIdentifier).
const langGo = "go"

// init registers the Go emitter for every intrinsic this backend implements by
// ID (see codegen.EmitIntrinsicCall). Only import-free intrinsics are
// registered here: the in-place list mutations lower to the `append` builtin.
// Intrinsics whose native form needs an import (strings.*, math.*) still flow
// through goBuiltinMethodFromArgs until the emitter interface can declare
// required imports.
func init() {
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterIntrinsic(langGo, id, func(args []ir.Expr, tr func(ir.Expr) string) string {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a)
		})
	}

	// --- list (in-place mutations) ---
	// ListPush/ListRemove mutate the receiver slice in place and return it;
	// `append` reassigns the receiver, matching the receiver-write semantics
	// (MutatesReceiver) the checker and reactivity rely on.
	reg("ListPush", func(a []string) string {
		return a[0] + " = append(" + a[0] + ", " + a[1] + ")"
	})
	reg("ListRemove", func(a []string) string {
		return a[0] + " = append(" + a[0] + "[:" + a[1] + "], " + a[0] + "[" + a[1] + "+1:]...)"
	})
}
