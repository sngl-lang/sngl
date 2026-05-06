package lower

import (
	"testing"
)

func TestCaps_Merge(t *testing.T) {
	tests := []struct {
		name string
		a, b Caps
		want Caps
	}{
		{
			name: "both zero",
			a:    Caps{},
			b:    Caps{},
			want: Caps{},
		},
		{
			name: "left enables NoToggle",
			a:    Caps{NoToggle: true},
			b:    Caps{},
			want: Caps{NoToggle: true},
		},
		{
			name: "right enables NoToggle",
			a:    Caps{},
			b:    Caps{NoToggle: true},
			want: Caps{NoToggle: true},
		},
		{
			name: "both enable NoReactivity",
			a:    Caps{NoReactivity: true},
			b:    Caps{NoReactivity: true},
			want: Caps{NoReactivity: true},
		},
		{
			name: "different flags from each side OR together",
			a:    Caps{NoToggle: true, NoTernary: true},
			b:    Caps{NoEnum: true, NoUnit: true},
			want: Caps{NoToggle: true, NoTernary: true, NoEnum: true, NoUnit: true},
		},
		{
			name: "all flags",
			a:    Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoRef: true, NoUnit: true, NoEnum: true, NoAsyncReactive: true},
			b:    Caps{NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true},
			want: Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoRef: true, NoUnit: true, NoEnum: true, NoAsyncReactive: true, NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Merge(tt.b); got != tt.want {
				t.Errorf("Merge(%+v, %+v) = %+v; want %+v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCaps_String(t *testing.T) {
	tests := []struct {
		name string
		c    Caps
		want string
	}{
		{name: "empty", c: Caps{}, want: ""},
		{name: "single", c: Caps{NoToggle: true}, want: "NoToggle"},
		{name: "multiple in pass-execution order", c: Caps{NoToggle: true, NoReactivity: true, NoEnum: true}, want: "NoEnum,NoToggle,NoReactivity"},
		{name: "all", c: Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoRef: true, NoUnit: true, NoEnum: true, NoAsyncReactive: true, NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true}, want: "NoUnit,NoEnum,NoTernary,NoAsyncReactive,NoComputed,NoLambda,NoRef,NoToggle,NoReactivity,NoTimer,NoDeclarative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.String(); got != tt.want {
				t.Errorf("String() = %q; want %q", got, tt.want)
			}
		})
	}
}
