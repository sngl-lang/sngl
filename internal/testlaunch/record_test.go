package testlaunch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

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

// The golden harness rewrites a component fixture's golden too, perhaps since
// the records' harness read the archive, so the records are written beside
// the golden as it is on disk.
func TestWriteArchiveKeepsWhatTheGoldenHarnessWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txtar")
	read := &txtar.Archive{Files: []txtar.File{{Name: "out/none/html/index.html", Data: []byte("old\n")}}}
	now := &txtar.Archive{Files: []txtar.File{{Name: "out/none/html/index.html", Data: []byte("new\n")}}}
	if err := os.WriteFile(path, txtar.Format(now), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := Record{Digest: "sha256:00", Results: []*codegen.TestResult{{Desc: "x", Passed: true}}}
	if err := WriteArchive(path, read, read.Files, map[string]Record{"testrun/k/1": rec}); err != nil {
		t.Fatal(err)
	}
	got, err := txtar.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || string(got.Files[0].Data) != "new\n" || got.Files[1].Name != "testrun/k/1" {
		t.Errorf("archive = %s", txtar.Format(got))
	}
}

// A launch that failed is the host's failure, not the program's answer, so
// nothing is recorded for a later run to replay.
func TestAFailedLaunchIsNotRecorded(t *testing.T) {
	gen := t.TempDir()
	rr := NewRecords(map[string]Record{}, true, false)
	_, err := rr.answer("k", gen, filepath.Join(t.TempDir(), "none"), func() ([]*codegen.TestResult, error) {
		return nil, errors.New("wait for agent: the page did not connect")
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to record") {
		t.Errorf("err = %v, want a refusal to record", err)
	}
	if len(rr.Written()) != 0 {
		t.Errorf("recorded %v", rr.Written())
	}
}
