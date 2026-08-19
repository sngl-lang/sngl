package ast

import "fmt"

// EvalString evaluates a compile-time-constant string expression before type
// checking has run. Some passes need a string value early — macro expansion
// (arguments are constant expressions, evaluated before the checker exists) and
// narrow static-analysis cases — and cannot rely on the checker's constant
// folding.
//
// It currently reduces string literals and `+` concatenation of them. It is a
// deliberate foothold: the constant folding the parser can do on its own lives
// here in one place and can be extended (substrings, an analogous EvalInt, …)
// as pre-check needs arise. It never consults scope, so it only ever sees
// syntactic constants.
func EvalString(e Expr) (string, error) {
	switch x := e.(type) {
	case *LiteralExpr:
		switch x.Kind {
		case LiteralStringQuoted, LiteralStringBackticked, LiteralStringTrippleQuoted:
			// Raw holds the lexer's string content (quotes already stripped).
			return x.Raw, nil
		default:
			return "", fmt.Errorf("expected a string literal, got a %s literal", x.Kind)
		}
	case *BinaryExpr:
		if x.Op != BinAdd {
			return "", fmt.Errorf("%s is not a constant string operation", x.Op)
		}
		l, err := EvalString(x.Left)
		if err != nil {
			return "", err
		}
		r, err := EvalString(x.Right)
		if err != nil {
			return "", err
		}
		return l + r, nil
	default:
		return "", fmt.Errorf("not a constant string expression (%T)", e)
	}
}
