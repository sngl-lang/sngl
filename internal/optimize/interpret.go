package optimize

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// maxInterpDepth bounds recursion in the interpreter adapter. Excess
// depth makes the fold fail silently — the original ir.Call is preserved.
const maxInterpDepth = 256

// irFromValue converts a Go-side runtime value (as produced by the
// interp package) back into an IR expression. Primitives go through
// irLiteral; maps become StructLits; slices become ListLits.
//
// When typ is non-nil and represents a known struct type, the returned
// StructLit carries Type+Def matching it. Otherwise, the StructLit is
// "naked" (Def=nil); downstream consumers that need the Def must look
// it up themselves.
func irFromValue(val any, typ *ir.Type) ir.Expr {
	switch v := val.(type) {
	case nil:
		return &ir.Literal{Type: ir.TypNull, Raw: "null"}
	case bool, int, float64, string:
		return irLiteral(v, typ)
	case map[string]any:
		fields := make([]ir.FieldInit, 0, len(v))
		for _, k := range sortedMapKeys(v) {
			fields = append(fields, ir.FieldInit{
				Name:  k,
				Value: irFromValue(v[k], nil),
			})
		}
		sl := &ir.StructLit{
			Type:   typ,
			Fields: fields,
		}
		if typ != nil && typ.Kind == ir.TypeStruct {
			if sd, ok := typ.Decl.(*ir.StructDef); ok {
				sl.Def = sd
			}
		}
		return sl
	case []any:
		elems := make([]ir.Expr, 0, len(v))
		for _, e := range v {
			elems = append(elems, irFromValue(e, nil))
		}
		return &ir.ListLit{Type: typ, Elems: elems}
	}
	return nil
}

// sortedMapKeys returns the keys of m sorted alphabetically.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
