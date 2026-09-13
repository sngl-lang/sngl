package kotlin

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// translateIRLiteral renders an ir.Literal as its Kotlin source form, for
// Translator.TranslateIRLiteral's computed-default / unresolved-literal surface
// (mirrors golang's helpers.go translateIRLiteral).
//
// It carried its own copy of the spelling until it was found to have missed the
// `f` on a 32-bit float that ktLiteral had grown; a nil literal is the only
// thing it still answers for itself.
func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "null"
	}
	return ktLiteral(n)
}
