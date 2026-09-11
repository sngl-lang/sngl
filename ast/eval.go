package ast

import (
	"fmt"
	"strconv"
)

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
func EvalString(e Expr) (string, error) { return evalString(e, nil, 0) }

// EvalStringWith is EvalString with a way to resolve a name, for the one caller
// that has declarations in hand before the checker does: a mark's arguments,
// where naming the import path once and concatenating onto it beats spelling
// it on every declaration. lookup returns the expression a name stands for.
//
// Separate from EvalString rather than a parameter on it, so the scope-free
// guarantee above still holds for everyone else.
func EvalStringWith(e Expr, lookup func(string) (Expr, bool)) (string, error) {
	return evalString(e, lookup, 0)
}

// maxConstDepth bounds a const that names a const. A cycle is a real thing to
// write and there is no scope here to have rejected it earlier.
const maxConstDepth = 32

func evalString(e Expr, lookup func(string) (Expr, bool), depth int) (string, error) {
	switch x := e.(type) {
	case *IdentExpr:
		if lookup == nil {
			return "", fmt.Errorf("not a constant string expression (%T)", e)
		}
		if depth >= maxConstDepth {
			return "", fmt.Errorf("constant %q refers to itself", x.Name)
		}
		v, ok := lookup(x.Name)
		if !ok {
			return "", fmt.Errorf("%q is not a constant declared in this package", x.Name)
		}
		return evalString(v, lookup, depth+1)
	}
	switch x := e.(type) {
	case *LiteralExpr:
		switch x.Kind {
		case LiteralStringQuoted, LiteralStringBackticked, LiteralStringTrippleQuoted:
			v, _ := x.StringValue()
			return v, nil
		default:
			return "", fmt.Errorf("expected a string literal, got a %s literal", x.Kind)
		}
	case *BinaryExpr:
		if x.Op != BinAdd {
			return "", fmt.Errorf("%s is not a constant string operation", x.Op)
		}
		l, err := evalString(x.Left, lookup, depth)
		if err != nil {
			return "", err
		}
		r, err := evalString(x.Right, lookup, depth)
		if err != nil {
			return "", err
		}
		return l + r, nil
	default:
		return "", fmt.Errorf("not a constant string expression (%T)", e)
	}
}

// EvalInt evaluates a compile-time-constant integer expression before type
// checking, the integer counterpart of EvalString. It reduces integer literals,
// unary negation, and +/-/* of constants. See EvalString for the rationale.
func EvalInt(e Expr) (int64, error) {
	switch x := e.(type) {
	case *LiteralExpr:
		if x.Kind != LiteralInt {
			return 0, fmt.Errorf("expected an int literal, got a %s literal", x.Kind)
		}
		n, err := strconv.ParseInt(x.Raw, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid int literal %q: %w", x.Raw, err)
		}
		return n, nil
	case *UnaryExpr:
		if x.Op != UnaryNeg {
			return 0, fmt.Errorf("%s is not a constant int operation", x.Op)
		}
		v, err := EvalInt(x.Operand)
		if err != nil {
			return 0, err
		}
		return -v, nil
	case *BinaryExpr:
		l, err := EvalInt(x.Left)
		if err != nil {
			return 0, err
		}
		r, err := EvalInt(x.Right)
		if err != nil {
			return 0, err
		}
		switch x.Op {
		case BinAdd:
			return l + r, nil
		case BinSub:
			return l - r, nil
		case BinMul:
			return l * r, nil
		default:
			return 0, fmt.Errorf("%s is not a constant int operation", x.Op)
		}
	default:
		return 0, fmt.Errorf("not a constant int expression (%T)", e)
	}
}
