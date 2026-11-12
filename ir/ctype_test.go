package ir

import "testing"

// A builtin type C spells in several words is held as one spelling, the one
// cgo names, so `short int` is no type a translator cannot write; one no
// SNGL value crosses as, `long double`, is refused.
func TestParseCTypeBuiltins(t *testing.T) {
	for in, want := range map[string]string{
		"short int":              "short",
		"signed short":           "short",
		"long int":               "long",
		"signed int":             "int",
		"signed":                 "int",
		"unsigned":               "unsigned int",
		"unsigned short int":     "unsigned short",
		"long long int":          "long long",
		"unsigned long long int": "unsigned long long",
		"signed char":            "signed char",
		"const char *":           "const char *",
		"cairo_t *":              "cairo_t *",
	} {
		ct, err := ParseCType(in)
		if err != nil {
			t.Errorf("ParseCType(%q): %v", in, err)
			continue
		}
		if got := ct.String(); got != want {
			t.Errorf("ParseCType(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"long double", "int int", "short char", "struct foo", "unsigned float"} {
		if ct, err := ParseCType(in); err == nil {
			t.Errorf("ParseCType(%q) = %q, want an error", in, ct)
		}
	}
}
