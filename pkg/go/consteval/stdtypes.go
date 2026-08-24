package consteval

import (
	"strconv"
	"time"
)

// SNGL models a duration as a unit value whose base is milliseconds
// (`unit duration { ms, s = 1000ms, ... }` in lib/std/units.sngl) and a
// datetime as a string-domain type, so both encode as literals rather than as
// the struct reflection would otherwise walk.
//
// This is the encode-side, do-not-own-the-type cell of a matrix with two axes:
// which direction a value crosses, and whether whoever maps it owns the Go
// type.
//
//	                 own the type   do not own it
//	encode (Go→SNGL) MarshalSNGL    consteval.Register[T]  (here)
//	import (type)    —              golang.RegisterType
//
// The import row lives in codegen/scheme/golang/stdtypes.go, which registers
// the same two types in the same order. A type needs both halves: this one
// decides how the child program writes the value, that one decides what type
// the compiler checks it against.
func init() {
	Register(func(d time.Duration) ([]byte, error) {
		ms := float64(d) / float64(time.Millisecond)
		if ms == float64(int64(ms)) {
			return append(strconv.AppendInt(nil, int64(ms), 10), "ms"...), nil
		}
		return append(strconv.AppendFloat(nil, ms, 'g', -1, 64), "ms"...), nil
	})
	Register(func(t time.Time) ([]byte, error) {
		return AppendQuote(nil, t.Format(time.RFC3339Nano)), nil
	})
}
