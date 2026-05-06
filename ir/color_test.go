package ir

import "testing"

func TestColor_String(t *testing.T) {
	tests := []struct {
		c    Color
		want string
	}{
		{ColorSync, "Sync"},
		{ColorAsync, "Async"},
		{ColorParam, "Param"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Color(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestFuncSig_IsPoly_DefaultsFalse(t *testing.T) {
	// Zero-value FuncSig should not be poly.
	s := &FuncSig{}
	if s.IsPoly() {
		t.Error("zero-value FuncSig.IsPoly() = true, want false")
	}
	if s.Color != ColorSync {
		t.Errorf("zero-value FuncSig.Color = %v, want ColorSync", s.Color)
	}

	// Setting Color to ColorParam makes it poly.
	s.Color = ColorParam
	if !s.IsPoly() {
		t.Error("FuncSig{Color: ColorParam}.IsPoly() = false, want true")
	}

	// Async is not poly.
	s.Color = ColorAsync
	if s.IsPoly() {
		t.Error("FuncSig{Color: ColorAsync}.IsPoly() = true, want false")
	}
}
