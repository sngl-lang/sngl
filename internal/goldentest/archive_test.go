package goldentest

import (
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/tools/txtar"
)

// The html component fixtures keep their browser runs in the same archive,
// and rewrite it with testlaunch.WriteArchive, which puts them last. A golden
// rewrite has to leave them there, or the two harnesses reorder each other's
// files on every -update, and has to keep them out of the package's source.
func TestGoldenRewriteKeepsTestRunRecordsLast(t *testing.T) {
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
		t.Errorf("files = %v, want %v", names, want)
	}
	for _, f := range got.Files {
		if f.Name == run.Name && !slices.Equal(f.Data, run.Data) {
			t.Errorf("%s changed to %q", run.Name, f.Data)
		}
	}
}
