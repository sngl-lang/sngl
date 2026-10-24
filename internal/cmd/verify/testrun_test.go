package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A package keeping a test_run record is left out of the test step while the
// record names what its tests build -- its own files and those of the
// module's packages it imports -- and reported once either moves, so a
// package whose tests link a library CI lacks is compiled only by -update.
func TestARecordedTestRunIsLeftOutUntilItMoves(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module duckfam.us/sngl\n\ngo 1.24\n")
	write("go.sum", "")
	write("dep/dep.go", "package dep\n\nfunc One() int { return 1 }\n")
	write("rec/rec.go", "package rec\n\nimport \"duckfam.us/sngl/dep\"\n\nvar X = dep.One()\n")
	write("rec/rec_test.go", "package rec\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	write("plain/plain.go", "package plain\n")
	t.Chdir(root)

	pkgs := []string{"./plain", "./rec"}
	if got, err := withoutRecordedTestRuns(pkgs); err != nil || !slices.Equal(got, pkgs) {
		t.Fatalf("with no record: %v, %v; want every package", got, err)
	}
	digest, err := testRunDigest("./rec")
	if err != nil {
		t.Fatal(err)
	}
	write("rec/"+testRunRecord, digest+"\n")
	if got, err := withoutRecordedTestRuns(pkgs); err != nil || !slices.Equal(got, []string{"./plain"}) {
		t.Fatalf("with a standing record: %v, %v; want ./plain alone", got, err)
	}

	// Lines go.sum gains build nothing; go.mod fixes every version.
	write("go.sum", "example.com/x v1.0.0 h1:x=\n")
	if got, err := withoutRecordedTestRuns(pkgs); err != nil || !slices.Equal(got, []string{"./plain"}) {
		t.Fatalf("after go.sum grew: %v, %v; want ./plain alone", got, err)
	}

	// A test of a package the tests import builds nothing of theirs.
	write("dep/dep_test.go", "package dep\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\n")
	if got, err := withoutRecordedTestRuns(pkgs); err != nil || !slices.Equal(got, []string{"./plain"}) {
		t.Fatalf("after its import's test moved: %v, %v; want ./plain alone", got, err)
	}

	// A file of a package the tests import moves the digest.
	write("dep/dep.go", "package dep\n\nfunc One() int { return 2 }\n")
	if _, err := withoutRecordedTestRuns(pkgs); err == nil || !strings.Contains(err.Error(), "./rec") {
		t.Fatalf("after its import moved: %v; want ./rec reported stale", err)
	}
}
