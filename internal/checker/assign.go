package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A slot is a position a value is written into: an initializer, an argument,
// a prop, a field, a return, an assignment, a default. What a value has to be
// to go there is one rule whichever slot it is, so it is one function
// (coerce), and a slot says only what is particular to it: how a mismatch is
// worded and where it is reported, and the few allowances that belong to one
// position.
//
// Each position used to restate the rule, and each restatement drifted: the
// implicit call reached arguments and props but not initializers, a colour
// literal was validated in a var and not a const, a prop default refused the
// literal 0, and a func parameter's or struct field's default was not checked
// at all.
type slot struct {
	// pos is where a mismatch is reported; unset, the expression's own.
	pos ast.Pos
	// mismatch words the error from the two types, contrasted.
	mismatch func(want, got string) string
	// later leaves a slot whose type names a type parameter to the inference
	// that binds it, and an unresolved type parameter on either side is the
	// tail of an earlier error.
	later bool
	// keepShape mints no conversion but the T -> option<T> promotion: a
	// struct literal's field is read by pattern (see wrapOptionIfNeeded).
	keepShape bool
	// accept admits a value no general rule does, and says so by returning
	// true; the value then stands as it is.
	accept func(val ir.Expr, got *ir.Type) bool
}

// checkExprAs checks e where a value of type want is written. want may be
// nil, where nothing constrains the value: e is then checked on its own, and
// is still held to yielding one.
func (c *checker) checkExprAs(e ast.Expr, want *ir.Type, s slot) ir.Expr {
	if e == nil {
		return nil
	}
	return c.coerce(e, c.checkExprExpecting(e, want), want, s)
}

// coerce holds val, checked from written, to want. written may be nil for a
// value the program did not write as an expression (a spread's field), which
// is then never called implicitly.
//
// A value reaches want when it is assignable to it, or when it is one of two
// spellings that mean a value of it: the literal 0 for any unit's zero, and a
// zero-argument function whose result reaches want, which is called. Anything
// else is the slot's mismatch, unless the slot accepts it.
func (c *checker) coerce(written ast.Expr, val ir.Expr, want *ir.Type, s slot) ir.Expr {
	if val == nil {
		return nil
	}
	pos := s.pos
	if !pos.IsSet() && written != nil {
		pos = *written.ExprPos()
	}
	got := exprType(val)
	if c.requireValueType(got, pos) || want == nil {
		return val
	}
	if got.Kind == ir.TypeDyn || want.Kind == ir.TypeDyn {
		return val
	}
	if s.later && (mentionsTypeParam(want) || got.Kind == ir.TypeTypeParam) {
		return val
	}
	if !got.IsAssignableTo(want) {
		if adapted, ok := adaptLiteralZero(val, want); ok {
			return adapted
		}
		if written != nil {
			if call, _ := c.implicitCall(written, got, want); call != nil {
				return c.shape(c.checkExpr(call), want, s)
			}
		}
		if s.accept != nil && s.accept(val, got) {
			return val
		}
		w, g := ir.Contrast(want, got)
		c.error(pos, "%s", s.mismatch(w, g))
		return val
	}
	c.validateStringDomainLiteral(pos, want, val)
	return c.shape(val, want, s)
}

// shape is the conversion a value that reached want is wrapped in, if any.
func (c *checker) shape(val ir.Expr, want *ir.Type, s slot) ir.Expr {
	if s.keepShape {
		return wrapOptionIfNeeded(val, want)
	}
	return wrapIfNeeded(val, want)
}

// The wordings of the slots more than one position shares.

func initSlot(pos ast.Pos) slot {
	return slot{pos: pos, mismatch: func(want, got string) string {
		return "cannot initialize " + want + " with " + got
	}}
}

func returnSlot(pos ast.Pos) slot {
	return slot{pos: pos, mismatch: func(want, got string) string {
		return "cannot return " + got + " as " + want
	}}
}

func passSlot() slot {
	return slot{mismatch: func(want, got string) string {
		return "cannot pass " + got + " as " + want
	}}
}

func defaultSlot(pos ast.Pos) slot {
	return slot{pos: pos, later: true, mismatch: func(want, got string) string {
		return "default value type " + got + " does not match param type " + want
	}}
}
