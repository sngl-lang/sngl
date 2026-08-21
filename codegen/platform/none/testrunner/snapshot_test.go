package testrunner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// buildSnapshotPkg parses+checks src and points SourcePath at a temp fixture
// so the snapshot runner resolves goldens under <tmp>/<base>.snapshots/.
func buildSnapshotPkg(t *testing.T, dir, base, src string) (results func() error) {
	t.Helper()
	fixture := filepath.Join(dir, base)
	doc, err := parser.Parse(fixture, []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{Dir: dir, IsMain: true})
	pkg.SourcePath = fixture
	return func() error {
		rs, err := testrunner.Run(pkg)
		if err != nil {
			return err
		}
		for _, r := range rs {
			if !r.Passed {
				return &runFail{r.Desc, r.Error}
			}
		}
		return nil
	}
}

type runFail struct{ desc, msg string }

func (e *runFail) Error() string { return e.desc + ": " + e.msg }

const snapshotSrc = `import . "sngl://std"
component counter {
	var count = 0
	vbox {
		text(value="Count: {count}")
		button(text="+", @click { count += 1 })
		if count > 0 {
			text(value="positive")
		} else {
			text(value="zero")
		}
	}
}

func testSnap(t Test, c counter) {
	c.count += 3
	t.snapshot("x")
}
`

func TestSnapshotWriteThenCompare(t *testing.T) {
	dir := t.TempDir()
	base := "snap.sngl"

	// First run with UPDATE writes the golden and passes.
	t.Setenv("SNGL_UPDATE_SNAPSHOTS", "1")
	if err := buildSnapshotPkg(t, dir, base, snapshotSrc)(); err != nil {
		t.Fatalf("update run: %v", err)
	}

	golden := filepath.Join(dir, base+".snapshots", "x.sngl")
	got, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden not written: %v", err)
	}
	want := "vbox() {\n    text(value=\"Count: 3\")\n    button(text=\"+\", @click)\n    text(value=\"positive\")\n}\n"
	if string(got) != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Second run (compare mode) against the same state passes.
	t.Setenv("SNGL_UPDATE_SNAPSHOTS", "0")
	if err := buildSnapshotPkg(t, dir, base, snapshotSrc)(); err != nil {
		t.Fatalf("compare run: %v", err)
	}
}

func TestSnapshotMutatedStateFails(t *testing.T) {
	dir := t.TempDir()
	base := "snap.sngl"

	// Write a golden for count += 3.
	t.Setenv("SNGL_UPDATE_SNAPSHOTS", "1")
	if err := buildSnapshotPkg(t, dir, base, snapshotSrc)(); err != nil {
		t.Fatalf("update run: %v", err)
	}

	// Compare a mutated source (count += 7 → "Count: 7") against the golden.
	mutated := strings.Replace(snapshotSrc, "c.count += 3", "c.count += 7", 1)
	t.Setenv("SNGL_UPDATE_SNAPSHOTS", "0")
	err := buildSnapshotPkg(t, dir, base, mutated)()
	if err == nil {
		t.Fatal("expected snapshot mismatch failure, got pass")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("expected snapshot mismatch, got: %v", err)
	}
}
