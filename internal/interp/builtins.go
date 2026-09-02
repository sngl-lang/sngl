package interp

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.duckfam.us/jonathan/sngl/internal/opeval"
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

// An id with no entry is one the interpreter cannot evaluate; whether that is
// an error depends on whether the declaration carries a usable body.
var intrinsics = map[string]nativeFunc{
	// --- int ---
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
		return math.Floor(toFloat(args[0])), nil
	},
	"float.ceil": func(args []any) (any, error) {
		return math.Ceil(toFloat(args[0])), nil
	},
	"float.round": func(args []any) (any, error) {
		return math.Round(toFloat(args[0])), nil
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
	"string.length": func(args []any) (any, error) {
		// Rune count, not byte count: `"é".length` is 1 (bugs.md #16).
		return utf8.RuneCountInString(fmt.Sprintf("%v", args[0])), nil
	},
	// A SNGL map is a map[string]any here, keyed by the string form of the
	// key, which is what mapMethodResult already assumed.
	"map.length": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("map.length: want a map, got %T", args[0])
		}
		return len(m), nil
	},
	"map.keys": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("map.keys: want a map, got %T", args[0])
		}
		out := make([]any, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out, nil
	},
	"map.values": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("map.values: want a map, got %T", args[0])
		}
		out := make([]any, 0, len(m))
		for _, v := range m {
			out = append(out, v)
		}
		return out, nil
	},
	"map.contains": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok || len(args) != 2 {
			return nil, fmt.Errorf("map.contains: want (map, key), got (%T, %d args)", args[0], len(args))
		}
		_, found := m[fmt.Sprintf("%v", args[1])]
		return found, nil
	},
	"map.get": func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok || len(args) != 3 {
			return nil, fmt.Errorf("map.get: want (map, key, default), got (%T, %d args)", args[0], len(args))
		}
		if v, found := m[fmt.Sprintf("%v", args[1])]; found {
			return v, nil
		}
		return args[2], nil
	},
	"list.length": func(args []any) (any, error) {
		if list, ok := args[0].([]any); ok {
			return len(list), nil
		}
		return 0, nil
	},

	// --- color ---
	"color.hex": func(args []any) (any, error) {
		if m, ok := args[0].(*Struct); ok {
			field := func(name string) any { v, _ := m.Get(name); return v }
			r := clampByte(ToInt(field("r")))
			g := clampByte(ToInt(field("g")))
			b := clampByte(ToInt(field("b")))
			a := ToInt(field("a"))
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

	// --- seq (sngl:seq) ---
	// An iter<int> a program holds is the numbers themselves; the counting
	// loop a backend emits instead is a codegen shortcut for the loop head,
	// so both answers come from opeval.Sequence and cannot disagree.
	"seq.count": func(args []any) (any, error) {
		return opeval.Sequence(0, ToInt(args[0]), 1), nil
	},
	"seq.range": func(args []any) (any, error) {
		return opeval.Sequence(ToInt(args[0]), ToInt(args[1]), 1), nil
	},
	"seq.step": func(args []any) (any, error) {
		return opeval.Sequence(ToInt(args[0]), ToInt(args[1]), ToInt(args[2])), nil
	},
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

// filter and map are assigned here rather than in the literal above: they
// call back into the interpreter through LambdaValue, and a composite literal
// that references it is an initialization cycle.
func init() {
	intrinsics["list.filter"] = func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok || len(args) != 2 {
			return nil, fmt.Errorf("filter: want (list, lambda), got (%T, %d args)", args[0], len(args))
		}
		lv, ok := args[1].(*LambdaValue)
		if !ok {
			return nil, fmt.Errorf("filter requires a lambda, got %T", args[1])
		}
		out := []any{}
		for _, item := range list {
			v, err := lv.Call([]any{item})
			if err != nil {
				return nil, err
			}
			if b, ok := v.(bool); ok && b {
				out = append(out, item)
			}
		}
		return out, nil
	}
	intrinsics["list.map"] = func(args []any) (any, error) {
		list, ok := args[0].([]any)
		if !ok || len(args) != 2 {
			return nil, fmt.Errorf("map: want (list, lambda), got (%T, %d args)", args[0], len(args))
		}
		lv, ok := args[1].(*LambdaValue)
		if !ok {
			return nil, fmt.Errorf("map requires a lambda, got %T", args[1])
		}
		out := make([]any, len(list))
		for i, item := range list {
			v, err := lv.Call([]any{item})
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
}
