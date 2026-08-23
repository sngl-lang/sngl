package consteval

import (
	"strconv"
	"time"
)

// SNGL models a duration as a unit value whose base is milliseconds
// (`unit duration { ms, s = 1000ms, ... }` in lib/std/units.sngl) and a
// datetime as a string-domain type, so both encode as literals rather than as
// the struct reflection would otherwise walk.
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
