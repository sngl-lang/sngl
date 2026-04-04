package testrunner

import (
	"fmt"
	"math"
	"regexp"
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
	"string.substring": func(args []any) (any, error) {
		s := fmt.Sprintf("%v", args[0])
		start := toInt(args[1])
		end := toInt(args[2])
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
	"regex.test": func(args []any) (any, error) {
		re, ok := args[0].(*regexp.Regexp)
		if !ok {
			return false, fmt.Errorf("regex.test: first argument must be a regex, got %T", args[0])
		}
		s := fmt.Sprintf("%v", args[1])
		return re.MatchString(s), nil
	},
	"regex.match": func(args []any) (any, error) {
		re, ok := args[0].(*regexp.Regexp)
		if !ok {
			return "", fmt.Errorf("regex.match: first argument must be a regex, got %T", args[0])
		}
		s := fmt.Sprintf("%v", args[1])
		return re.FindString(s), nil
	},

	// --- color ---
	"color.hex": func(args []any) (any, error) {
		if m, ok := args[0].(map[string]any); ok {
			r := clampByte(toInt(m["r"]))
			g := clampByte(toInt(m["g"]))
			b := clampByte(toInt(m["b"]))
			a := toInt(m["a"])
			if a == 255 {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b), nil
			}
			return fmt.Sprintf("#%02x%02x%02x%02x", r, g, b, clampByte(a)), nil
		}
		return "#000000", nil
	},

	// --- list ---
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
		start := toInt(args[1])
		end := toInt(args[2])
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

// colorLightenDarken adjusts a hex color by a percentage.
func colorLightenDarken(hex string, pct float64, lighten bool) string {
	if len(hex) < 7 || hex[0] != '#' {
		return hex
	}
	r := hexToByte(hex[1:3])
	g := hexToByte(hex[3:5])
	b := hexToByte(hex[5:7])

	if lighten {
		r = r + int(float64(255-r)*pct)
		g = g + int(float64(255-g)*pct)
		b = b + int(float64(255-b)*pct)
	} else {
		r = int(float64(r) * (1 - pct))
		g = int(float64(g) * (1 - pct))
		b = int(float64(b) * (1 - pct))
	}
	suffix := ""
	if len(hex) == 9 {
		suffix = hex[7:9]
	}
	return fmt.Sprintf("#%02x%02x%02x%s", clampByte(r), clampByte(g), clampByte(b), suffix)
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
