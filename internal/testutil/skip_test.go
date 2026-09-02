package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkipCodegen(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        string
		want       bool
		wantReason string
		wantErr    bool
	}{
		{name: "absent", src: "component a {}\n"},
		{
			name:       "with a reason",
			src:        "// SKIP(codegen) \"waiting on the effect lowering\"\ncomponent a {}\n",
			want:       true,
			wantReason: "waiting on the effect lowering",
		},
		{
			name:       "anywhere in the file",
			src:        "component a {}\n// SKIP(codegen) \"why\"\n",
			want:       true,
			wantReason: "why",
		},
		// A malformed directive is an error rather than a comment that skips
		// nothing: silently running the fixture is the crash it was written to
		// avoid.
		{name: "no phase", src: "// SKIP \"why\"\n", wantErr: true},
		{name: "no reason", src: "// SKIP(codegen)\ncomponent a {}\n", wantErr: true},
		{name: "empty reason", src: "// SKIP(codegen) \"\"\n", wantErr: true},
		{name: "unknown phase", src: "// SKIP(check) \"why\"\n", wantErr: true},
		{name: "not a directive", src: "// SKIPX \"no\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.sngl")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got, reason, err := ParseSkipCodegen(path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseSkipCodegen error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want || reason != tc.wantReason {
				t.Errorf("ParseSkipCodegen = (%v, %q), want (%v, %q)", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}
