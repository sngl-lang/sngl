// Package snglparse reads a number a string writes, as sngl:builtin's
// int.parse and float.parse answer it: the interpreter and generated Go call
// these, so the two read one grammar. Each answers nil where the string writes
// none, as a Go program holds an option.
package snglparse

import (
	"regexp"
	"strconv"
)

// Int is the int s writes in base: an optional sign and base's digits, and
// nothing else, in int's range. Nil for anything else, a base outside 2 to 36
// included.
func Int(s string, base int) *int {
	if base < 2 || base > 36 {
		return nil
	}
	// strconv.ParseInt takes no underscore in a base it is given, nor a
	// prefix.
	v, err := strconv.ParseInt(s, base, 64)
	if err != nil {
		return nil
	}
	n := int(v)
	return &n
}

// floatSyntax is what float.parse reads: an optional sign, then digits with an
// optional point and exponent, or inf, infinity or nan in any case.
var floatSyntax = regexp.MustCompile(`^[+-]?(([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?|(?i:inf|infinity|nan))$`)

// Float is the float s writes (floatSyntax). A number past float's range is
// none.
func Float(s string) *float64 {
	if !floatSyntax.MatchString(s) {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}
