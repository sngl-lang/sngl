package interp

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// AppendValueKey appends to b an encoding of v that two values share only
// when they are equal, for a cache keyed on constant values. It reports false
// for a value that has no such encoding -- a function, an owned handle, a
// builder, anything this does not know -- and is no constant to key on.
//
// A declaration a value names (a struct's, a unit's table) is written as its
// address and handed to pin, which the caller must keep alive for as long as
// it keeps the key: an address the collector reused would otherwise name
// another declaration.
func AppendValueKey(b []byte, v any, pin func(any)) ([]byte, bool) {
	switch x := v.(type) {
	case nil:
		return append(b, 'n'), true
	case bool:
		if x {
			return append(b, 't'), true
		}
		return append(b, 'f'), true
	case int:
		return strconv.AppendInt(append(b, 'i'), int64(x), 10), true
	case uint64:
		return strconv.AppendUint(append(b, 'u'), x, 10), true
	case float64:
		return strconv.AppendFloat(append(b, 'd'), x, 'g', -1, 64), true
	case string:
		return appendKeyString(append(b, 's'), x), true
	case []any:
		b = strconv.AppendInt(append(b, '['), int64(len(x)), 10)
		for _, el := range x {
			var ok bool
			if b, ok = AppendValueKey(append(b, ','), el, pin); !ok {
				return b, false
			}
		}
		return append(b, ']'), true
	case map[string]any:
		b = strconv.AppendInt(append(b, '{'), int64(len(x)), 10)
		for _, k := range slices.Sorted(maps.Keys(x)) {
			var ok bool
			if b, ok = AppendValueKey(appendKeyString(append(b, ','), k), x[k], pin); !ok {
				return b, false
			}
		}
		return append(b, '}'), true
	case *Struct:
		if x == nil {
			return append(b, 'n'), true
		}
		if x.Handle != nil {
			return b, false
		}
		pin(x.Def)
		pin(x.Type)
		b = fmt.Appendf(append(b, 'S'), "%p.%p.%d", x.Def, x.Type, len(x.Fields))
		for _, f := range x.Fields {
			var ok bool
			if b, ok = AppendValueKey(appendKeyString(append(b, ','), f.Name), f.Value, pin); !ok {
				return b, false
			}
		}
		return append(b, ')'), true
	case unitValue:
		pin(x.Table)
		b = appendKeyString(fmt.Appendf(append(b, 'U'), "%p.", x.Table), x.Suffix)
		for _, base := range slices.Sorted(maps.Keys(x.Amounts)) {
			b = strconv.AppendFloat(appendKeyString(append(b, ','), base), x.Amounts[base], 'g', -1, 64)
		}
		return append(b, ')'), true
	}
	return b, false
}

func appendKeyString(b []byte, s string) []byte {
	b = strconv.AppendInt(b, int64(len(s)), 10)
	return append(append(b, ':'), s...)
}
