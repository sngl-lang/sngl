package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// inferI18nInterp checks a translatable string. F1 scope: emit the
// no-static-text warning. Subsequent tasks (F2, G1, G2) add ICU placeholder
// validation and lower to ir.Call(i18n.tr, ...).
func (c *checker) inferI18nInterp(x *ast.I18nInterpExpr) ir.Expr {
	if !hasStaticText(x.Parts) {
		c.warn(x.Pos, "translatable string contains no static text; translators will have nothing to translate")
	}
	// F1 placeholder return: a string-typed empty literal. F2/G1/G2 will
	// replace this with proper validation + lowering.
	return &ir.Literal{Type: TypString, Raw: `""`}
}

// hasStaticText reports whether any part contains literal non-whitespace text,
// recursing through case bodies.
func hasStaticText(parts []ast.Expr) bool {
	for _, p := range parts {
		switch v := p.(type) {
		case *ast.LiteralExpr:
			if hasNonWhitespace(v.Raw) {
				return true
			}
		case *ast.I18nPlaceholderExpr:
			for _, ca := range v.Cases {
				if hasStaticText(ca.Body) {
					return true
				}
			}
		}
	}
	return false
}

func hasNonWhitespace(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}
