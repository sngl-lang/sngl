package kotlin

import (
	"testing"
)

func TestExportName(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"count", "count"},
		{"myField", "myField"},
	}
	for _, tt := range tests {
		got := tr.ExportName(tt.in)
		if got != tt.want {
			t.Errorf("ExportName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
