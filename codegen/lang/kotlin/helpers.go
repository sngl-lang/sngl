package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// translateIRLiteral renders an ir.Literal as its Kotlin source form. Retained
// from the (deleted) legacy translate_ir.go path because Translator.
// TranslateIRLiteral delegates to it for the computed-default / unresolved-
// literal surface (mirrors golang's helpers.go translateIRLiteral). The
// KtIRContext walk uses its own evalLiteral; this standalone form additionally
// honors the Suffix carry-through that some literal callers depend on.
func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "null"
	}
	if n.Suffix != "" {
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString, ir.TypeColor, ir.TypeDuration:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeStruct:
			if ir.StringReprStruct(n.Type) {
				return fmt.Sprintf("%q", n.Raw)
			}
			return n.Raw
		case ir.TypeInt, ir.TypeBool:
			return n.Raw
		case ir.TypeFloat:
			s := n.Raw
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
		case ir.TypeNull:
			return "null"
		}
	}
	return n.Raw
}
