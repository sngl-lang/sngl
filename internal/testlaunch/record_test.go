package testlaunch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
)

// Which launches each mode reruns: -update only what moved, so refreshing
// one target does not rerun every other launch a script makes, and -verify all
// of them, since a toolchain or browser upgrade moves no digest.
func TestRecordsRerunWhatEachModeAsks(t *testing.T) {
	gen := t.TempDir()
	if err := os.WriteFile(filepath.Join(gen, "index.html"), []byte("<p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	snaps := filepath.Join(t.TempDir(), "none")
	pass := []*codegen.TestResult{{Desc: "x", Passed: true}}
	seed := NewRecords(map[string]Record{}, true, false)
	if _, err := seed.answer("k", gen, snaps, func() ([]*codegen.TestResult, error) { return pass, nil }); err != nil {
		t.Fatal(err)
	}
	have := seed.Written()

	for _, tc := range []struct {
		name           string
		update, verify bool
		result         []*codegen.TestResult
		launches       int
		err            string
	}{
		{"update replays a record whose inputs held", true, false, pass, 0, ""},
		{"verify reruns it", false, true, pass, 1, ""},
		{"verify fails a rerun that disagrees", false, true, []*codegen.TestResult{{Desc: "x"}}, 1, "disagrees with the record"},
		{"update with verify rewrites it", true, true, []*codegen.TestResult{{Desc: "x"}}, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launches := 0
			rr := NewRecords(have, tc.update, tc.verify)
			_, err := rr.answer("k", gen, snaps, func() ([]*codegen.TestResult, error) {
				launches++
				return tc.result, nil
			})
			if launches != tc.launches {
				t.Errorf("launched %d times, want %d", launches, tc.launches)
			}
			if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Errorf("err = %v, want %q", err, tc.err)
			}
		})
	}
}
