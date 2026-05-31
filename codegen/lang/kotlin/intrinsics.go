package kotlin

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// langKt is this backend's registry key (matches LanguageIdentifier).
const langKt = "kotlin"

// init registers the Kotlin emitter for the intrinsics this backend implements
// by ID (see codegen.EmitIntrinsicCall). Only import-free intrinsics are
// registered here: the in-place list mutations map to MutableList.add /
// removeAt. Intrinsics whose native form needs an import flow through
// ktBuiltinMethodFromArgs until the emitter interface can declare imports.
func init() {
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterIntrinsic(langKt, id, func(args []ir.Expr, tr func(ir.Expr) string) string {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a)
		})
	}

	// --- list (in-place mutations) ---
	reg("ListPush", func(a []string) string { return a[0] + ".add(" + a[1] + ")" })
	reg("ListRemove", func(a []string) string { return a[0] + ".removeAt(" + a[1] + ")" })
}
