package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNoFmt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        string
		want       bool
		wantReason string
	}{
		{"absent", "component a {}\n", false, ""},
		{"bare", "// NOFMT\ncomponent a {}\n", true, ""},
		{"with a reason", "// NOFMT \"the layout is what this asserts\"\ncomponent a {}\n", true, "the layout is what this asserts"},
		{"anywhere in the file", "component a {}\n// NOFMT \"why\"\n", true, "why"},
		{"not a directive", "// NOFMTX \"no\"\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.sngl")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got, reason, err := ParseNoFmt(path)
			if err != nil {
				t.Fatalf("ParseNoFmt: %v", err)
			}
			if got != tc.want || reason != tc.wantReason {
				t.Errorf("ParseNoFmt = (%v, %q), want (%v, %q)", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}
