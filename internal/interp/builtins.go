package interp

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// nativeMethod looks up a native Go implementation for a stdlib method.
// Returns the result and true if a native implementation exists, or (nil, false) otherwise.
func nativeMethod(qualName string, args []any) (any, bool, error) {
	fn, ok := nativeMethods[qualName]
	if !ok {
		return nil, false, nil
	}
	result, err := fn(args)
	return result, true, err
}

type nativeFunc func(args []any) (any, error)

var nativeMethods = map[string]nativeFunc{
	// --- int ---
	"int.min": func(args []any) (any, error) {
		a, b := ToInt(args[0]), ToInt(args[1])
		if a < b {
			return a, nil
		}
		return b, nil
	},
	"int.max": func(args []any) (any, error) {
		a, b := ToInt(args[0]), ToInt(args[1])
		if a > b {
			return a, nil
		}
		return b, nil
	},
	"int.abs": func(args []any) (any, error) {
		x := ToInt(args[0])
		if x < 0 {
			return -x, nil
		}
		return x, nil
	},
	"int.clamp": func(args []any) (any, error) {
		x, lo, hi := ToInt(args[0]), ToInt(args[1]), ToInt(args[2])
		if x < lo {
			return lo, nil
		}
		if x > hi {
			return hi, nil
		}
		return x, nil
	},
	// --- float ---
	"float.min": func(args []any) (any, error) {
		a, b := toFloat(args[0]), toFloat(args[1])
		if a < b {
			return a, nil
		}
		return b, nil
	},
	"float.max": func(args []any) (any, error) {
		a, b := toFloat(args[0]), toFloat(args[1])
		if a > b {
			return a, nil
		}
		return b, nil
	},
	"float.abs": func(args []any) (any, error) {
		return math.Abs(toFloat(args[0])), nil
	},
	"float.clamp": func(args []any) (any, error) {
		x, lo, hi := toFloat(args[0]), toFloat(args[1]), toFloat(args[2])
		if x < lo {
			return lo, nil
		}
		if x > hi {
			return hi, nil
		}
		return x, nil
	},
	"int.parse": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		base := ToInt(args[1])
		v, err := strconv.ParseInt(s, base, 64)
		if err != nil {
			return nil, fmt.Errorf("int.parse(%q, %d): %w", s, base, err)
		}
		return int(v), nil
	},
	// --- float math (accurate native implementations) ---
	"float.floor": func(args []any) (any, error) {
		return int(math.Floor(toFloat(args[0]))), nil
	},
	"float.ceil": func(args []any) (any, error) {
		return int(math.Ceil(toFloat(args[0]))), nil
	},
	"float.round": func(args []any) (any, error) {
		return int(math.Round(toFloat(args[0]))), nil
	},
	"float.sqrt": func(args []any) (any, error) {
		return math.Sqrt(toFloat(args[0])), nil
	},
	"float.pow": func(args []any) (any, error) {
		return math.Pow(toFloat(args[0]), toFloat(args[1])), nil
	},
	"float.sin": func(args []any) (any, error) {
		return math.Sin(toFloat(args[0])), nil
	},
	"float.cos": func(args []any) (any, error) {
		return math.Cos(toFloat(args[0])), nil
	},
	"float.tan": func(args []any) (any, error) {
		return math.Tan(toFloat(args[0])), nil
	},
	"float.asin": func(args []any) (any, error) {
		return math.Asin(toFloat(args[0])), nil
	},
	"float.acos": func(args []any) (any, error) {
		return math.Acos(toFloat(args[0])), nil
	},
	"float.atan": func(args []any) (any, error) {
		return math.Atan(toFloat(args[0])), nil
	},
	"float.atan2": func(args []any) (any, error) {
		return math.Atan2(toFloat(args[0]), toFloat(args[1])), nil
	},

	// --- string ---
	"string.upper": func(args []any) (any, error) {
		return strings.ToUpper(fmt.Sprintf("%v", args[0])), nil
	},
	"string.lower": func(args []any) (any, error) {
		return strings.ToLower(fmt.Sprintf("%v", args[0])), nil
	},
	"string.trim": func(args []any) (any, error) {
		return strings.TrimSpace(fmt.Sprintf("%v", args[0])), nil
	},
	"string.replace": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		old := fmt.Sprintf("%v", args[1])
		new := fmt.Sprintf("%v", args[2])
		return strings.ReplaceAll(s, old, new), nil
	},
	"string.indexOf": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		sub := fmt.Sprintf("%v", args[1])
		return strings.Index(s, sub), nil
	},
	"string.contains": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		sub := fmt.Sprintf("%v", args[1])
		return strings.Contains(s, sub), nil
	},
	"string.startsWith": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		prefix := fmt.Sprintf("%v", args[1])
		return strings.HasPrefix(s, prefix), nil
	},
	"string.endsWith": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		suffix := fmt.Sprintf("%v", args[1])
		return strings.HasSuffix(s, suffix), nil
	},
	"string.split": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		sep := fmt.Sprintf("%v", args[1])
		parts := strings.Split(s, sep)
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, nil
	},
	"string.substring": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		start := ToInt(args[1])
		end := ToInt(args[2])
		if start < 0 {
			start = 0
		}
		if end > len(s) {
			end = len(s)
		}
		if start > end {
			return "", nil
		}
		return s[start:end], nil
	},

	// --- string/list length (native, replaces size()) ---
	"string.length": func(args []any) (any, error) {
		return len(fmt.Sprintf("%v", args[0])), nil
	},
	"list.length": func(args []any) (any, error) {
		if list, ok := args[0].([]any); ok {
			return len(list), nil
		}
		return 0, nil
	},

	// --- regex ---
	"regex.matches": func(args []any) (any, error) {
		re, ok := args[0].(*regexp.Regexp)
		if !ok {
			return false, fmt.Errorf("regex.matches: first argument must be a regex, got %T", args[0])
		}
		s := fmt.Sprintf("%v", args[1])
		return re.MatchString(s), nil
	},
	"regex.find": func(args []any) (any, error) {
		re, ok := args[0].(*regexp.Regexp)
		if !ok {
			return "", fmt.Errorf("regex.find: first argument must be a regex, got %T", args[0])
		}
		s := fmt.Sprintf("%v", args[1])
		return re.FindString(s), nil
	},

	// --- color ---
	"color.rgb": func(args []any) (any, error) {
		return map[string]any{
			"r": clampByte(ToInt(args[0])),
			"g": clampByte(ToInt(args[1])),
			"b": clampByte(ToInt(args[2])),
			"a": 255,
		}, nil
	},
	"color.rgba": func(args []any) (any, error) {
		return map[string]any{
			"r": clampByte(ToInt(args[0])),
			"g": clampByte(ToInt(args[1])),
			"b": clampByte(ToInt(args[2])),
			"a": clampByte(ToInt(args[3])),
		}, nil
	},
	"color.opacity": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("color.opacity requires a color, got %T", args[0])
		}
		return map[string]any{
			"r": m["r"],
			"g": m["g"],
			"b": m["b"],
			"a": clampByte(ToInt(args[1])),
		}, nil
	},
	"color.lighten": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("color.lighten requires a color, got %T", args[0])
		}
		pct := toFloat(args[1])
		r := ToInt(m["r"])
		g := ToInt(m["g"])
		b := ToInt(m["b"])
		return map[string]any{
			"r": clampByte(r + int(float64(255-r)*pct)),
			"g": clampByte(g + int(float64(255-g)*pct)),
			"b": clampByte(b + int(float64(255-b)*pct)),
			"a": m["a"],
		}, nil
	},
	"color.darken": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("color.darken requires a color, got %T", args[0])
		}
		pct := toFloat(args[1])
		return map[string]any{
			"r": int(toFloat(m["r"]) * (1.0 - pct)),
			"g": int(toFloat(m["g"]) * (1.0 - pct)),
			"b": int(toFloat(m["b"]) * (1.0 - pct)),
			"a": m["a"],
		}, nil
	},
	"color.hex": func(args []any) (any, error) {
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
	"list.contains": func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok {
			return false, nil
		}
		target := args[1]
		for _, v := range list {
			if fmt.Sprintf("%v", v) == fmt.Sprintf("%v", target) {
				return true, nil
			}
		}
		return false, nil
	},
	"list.indexOf": func(args []any) (any, error) {
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
	"list.join": func(args []any) (any, error) {
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
	"list.reverse": func(args []any) (any, error) {
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
	"list.slice": func(args []any) (any, error) {
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
