package goldentest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/tools/txtar"
)

// The html component fixtures keep their browser runs in the same archive and
// rewrite it with testlaunch.WriteArchive, which puts them last. A golden
// rewrite keeps them out of the package's source, leaves them last, and takes
// them from the file as it is when it writes, since the other harness may have
// rewritten them since the archive was read.
func TestGoldenRewriteKeepsTheTestRunRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txtar")
	run := txtar.File{Name: "testrun/html/none/f.sngl/main/1", Data: []byte("sha256:00\n{}\n")}
	arc := &txtar.Archive{Files: []txtar.File{
		{Name: "app.sngl", Data: []byte("component main {}\n")},
		{Name: "out/none/html/index.html", Data: []byte("old\n")},
		{Name: "run/none/html", Data: []byte("pass sha256:00\n")},
		run,
	}}
	src, _, _, err := split(arc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src[run.Name]; ok {
		t.Errorf("split read %s as source", run.Name)
	}

	rerun := txtar.File{Name: run.Name, Data: []byte("sha256:01\n{}\n")}
	onDisk := &txtar.Archive{Files: append(slices.Clone(arc.Files[:3]), rerun)}
	if err := os.WriteFile(path, txtar.Format(onDisk), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGolden(t, path, arc,
		map[string][]byte{"out/none/html/index.html": []byte("new\n")}, nil,
		map[string]record{"run/none/html": {status: "pass", digest: "sha256:01"}})
	got, err := txtar.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got.Files {
		names = append(names, f.Name)
	}
	want := []string{"app.sngl", "out/none/html/index.html", "run/none/html", run.Name}
	if !slices.Equal(names, want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	if !slices.Equal(got.Files[3].Data, rerun.Data) {
		t.Errorf("%s = %q, want the record on disk, %q", run.Name, got.Files[3].Data, rerun.Data)
	}
}
