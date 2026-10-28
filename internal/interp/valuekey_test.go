package interp

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func valueKey(t *testing.T, v any) (string, bool) {
	t.Helper()
	b, ok := AppendValueKey(nil, v, func(any) {})
	return string(b), ok
}

// A struct a field holds is spelled by its contents: formatted with %v it was
// its address, so a Style holding a color never matched another, and a reused
// address could match a different one.
func TestValueKeyNestedStructByContents(t *testing.T) {
	def := &ir.StructDef{Name: "color"}
	style := func() any {
		return &Struct{Fields: []Field{{Name: "fg", Value: &Struct{Def: def, Fields: []Field{{Name: "r", Value: 1.0}}}}}}
	}
	a, okA := valueKey(t, style())
	b, okB := valueKey(t, style())
	if !okA || !okB || a != b {
		t.Fatalf("equal nested structs keyed %q (%v) and %q (%v)", a, okA, b, okB)
	}
}

func TestValueKeyDistinguishesTypes(t *testing.T) {
	seen := map[string]any{}
	for _, v := range []any{1, 1.0, "1", uint64(1), true, nil, []any{1}, map[string]any{"1": 1}, []any{}, ""} {
		k, ok := valueKey(t, v)
		if !ok {
			t.Fatalf("%#v has no key", v)
		}
		if prev, dup := seen[k]; dup {
			t.Errorf("%#v and %#v share key %q", prev, v, k)
		}
		seen[k] = v
	}
}

func TestValueKeyRefusesWhatIsNoConstant(t *testing.T) {
	for _, v := range []any{
		&LambdaValue{},
		&Struct{Handle: &Handle{}},
		[]any{1, &LambdaValue{}},
		map[string]any{"f": &LambdaValue{}},
	} {
		if k, ok := valueKey(t, v); ok {
			t.Errorf("%#v keyed as %q", v, k)
		}
	}
}
