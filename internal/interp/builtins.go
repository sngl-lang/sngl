package interp

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// runIntrinsic runs this interpreter's implementation of the intrinsic named
// id. Returns (result, true, err) when it has one, (nil, false, nil) otherwise
// — the caller then falls back to the declaration's own SNGL body.
//
// The id comes from the #[intrinsic] mark on the declaration the checker
// resolved, so nothing here reconstructs a "type.method" name to dispatch on:
// a method reached through an import alias, or renamed at its declaration,
// still lands on the same implementation.
func runIntrinsic(id string, args []any) (any, bool, error) {
	fn, ok := intrinsics[id]
	if !ok {
		return nil, false, nil
	}
	result, err := fn(args)
	return result, true, err
}

type nativeFunc func(args []any) (any, error)

// intrinsics maps an intrinsic id to this interpreter's implementation of it.
// An id with no entry is one the interpreter cannot evaluate; whether that is
// an error depends on whether the declaration carries a usable body.
var intrinsics = map[string]nativeFunc{
	// --- int ---
	// --- float ---
	"IntParse": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		base := ToInt(args[1])
		v, err := strconv.ParseInt(s, base, 64)
		if err != nil {
			return nil, fmt.Errorf("int.parse(%q, %d): %w", s, base, err)
		}
		return int(v), nil
	},
	// --- float math (accurate native implementations) ---
	"MathFloor": func(args []any) (any, error) {
		return int(math.Floor(toFloat(args[0]))), nil
	},
	"MathCeil": func(args []any) (any, error) {
		return int(math.Ceil(toFloat(args[0]))), nil
	},
	"MathRound": func(args []any) (any, error) {
		return int(math.Round(toFloat(args[0]))), nil
	},
	"MathSqrt": func(args []any) (any, error) {
		return math.Sqrt(toFloat(args[0])), nil
	},
	"MathPow": func(args []any) (any, error) {
		return math.Pow(toFloat(args[0]), toFloat(args[1])), nil
	},
	"MathSin": func(args []any) (any, error) {
		return math.Sin(toFloat(args[0])), nil
	},
	"MathCos": func(args []any) (any, error) {
		return math.Cos(toFloat(args[0])), nil
	},
	"MathTan": func(args []any) (any, error) {
		return math.Tan(toFloat(args[0])), nil
	},
	"MathAsin": func(args []any) (any, error) {
		return math.Asin(toFloat(args[0])), nil
	},
	"MathAcos": func(args []any) (any, error) {
		return math.Acos(toFloat(args[0])), nil
	},
	"MathAtan": func(args []any) (any, error) {
		return math.Atan(toFloat(args[0])), nil
	},
	"MathAtan2": func(args []any) (any, error) {
		return math.Atan2(toFloat(args[0]), toFloat(args[1])), nil
	},

	// --- string ---
	"StrUpper": func(args []any) (any, error) {
		return strings.ToUpper(fmt.Sprintf("%v", args[0])), nil
	},
	"StrLower": func(args []any) (any, error) {
		return strings.ToLower(fmt.Sprintf("%v", args[0])), nil
	},
	"StrTrim": func(args []any) (any, error) {
		return strings.TrimSpace(fmt.Sprintf("%v", args[0])), nil
	},
	"StrReplace": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		old := fmt.Sprintf("%v", args[1])
		new := fmt.Sprintf("%v", args[2])
		return strings.ReplaceAll(s, old, new), nil
	},
	"StrIndexOf": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		sub := fmt.Sprintf("%v", args[1])
		return strings.Index(s, sub), nil
	},
	"StrSplit": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		sep := fmt.Sprintf("%v", args[1])
		parts := strings.Split(s, sep)
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, nil
	},
	"StrSubstring": func(args []any) (any, error) {
		// Rune (Unicode code point) semantics: indices count code points,
		// not bytes, so a multi-byte rune is never split into invalid
		// UTF-8. Matches SNGL's string-is-runes model (bugs.md #16).
		runes := []rune(fmt.Sprintf("%v", args[0]))
		start := ToInt(args[1])
		end := ToInt(args[2])
		if start < 0 {
			start = 0
		}
		if end > len(runes) {
			end = len(runes)
		}
		if start > end {
			return "", nil
		}
		return string(runes[start:end]), nil
	},

	// --- string/list length (native, replaces size()) ---
	"StrLength": func(args []any) (any, error) {
		// Rune count, not byte count: `"é".length` is 1 (bugs.md #16).
		return utf8.RuneCountInString(fmt.Sprintf("%v", args[0])), nil
	},
	"ListLength": func(args []any) (any, error) {
		if list, ok := args[0].([]any); ok {
			return len(list), nil
		}
		return 0, nil
	},

	// --- regex ---

	// --- color ---
	"ColorHex": func(args []any) (any, error) {
		if m, ok := args[0].(map[string]any); ok {
			r := clampByte(ToInt(m["r"]))
			g := clampByte(ToInt(m["g"]))
			b := clampByte(ToInt(m["b"]))
			a := ToInt(m["a"])
			if a == 255 {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b), nil
			}
			return fmt.Sprintf("#%02x%02x%02x%02x", r, g, b, clampByte(a)), nil
		}
		return "#000000", nil
	},

	// --- list ---
	"ListIndexOf": func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok {
			return -1, nil
		}
		target := args[1]
		for i, v := range list {
			if fmt.Sprintf("%v", v) == fmt.Sprintf("%v", target) {
				return i, nil
			}
		}
		return -1, nil
	},
	"ListJoin": func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok {
			return "", nil
		}
		sep := fmt.Sprintf("%v", args[1])
		parts := make([]string, len(list))
		for i, v := range list {
			parts[i] = fmt.Sprintf("%v", v)
		}
		return strings.Join(parts, sep), nil
	},
	"ListReverse": func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok {
			return args[0], nil
		}
		result := make([]any, len(list))
		for i, v := range list {
			result[len(list)-1-i] = v
		}
		return result, nil
	},
	"ListSlice": func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok {
			return args[0], nil
		}
		start := ToInt(args[1])
		end := ToInt(args[2])
		if start < 0 {
			start = 0
		}
		if end > len(list) {
			end = len(list)
		}
		if start > end {
			return []any{}, nil
		}
		result := make([]any, end-start)
		copy(result, list[start:end])
		return result, nil
	},
}

// colorHexToStruct converts a hex color string like "#ff0000" to a Color struct map.
func colorHexToStruct(hex string) map[string]any {
	r, g, b, a := 0, 0, 0, 255
	if len(hex) >= 7 && hex[0] == '#' {
		r = hexToByte(hex[1:3])
		g = hexToByte(hex[3:5])
		b = hexToByte(hex[5:7])
	}
	if len(hex) >= 9 {
		a = hexToByte(hex[7:9])
	}
	return map[string]any{"r": r, "g": g, "b": b, "a": a}
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func hexToByte(s string) int {
	v := 0
	for _, c := range s {
		v *= 16
		if c >= '0' && c <= '9' {
			v += int(c - '0')
		} else if c >= 'a' && c <= 'f' {
			v += int(c-'a') + 10
		} else if c >= 'A' && c <= 'F' {
			v += int(c-'A') + 10
		}
	}
	return v
}
