package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// A package under an ignored directory is not the repository's: verify
// neither builds nor counts it. One written but not yet added is, and so is
// the root package; what ./... skips is skipped.
func TestGoPackagesWalksWhatGitTracks(t *testing.T) {
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
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	write(".gitignore", "tmp/\n")
	write("go.mod", "module example.com/root\n\ngo 1.21\n")
	write("root.go", "package root\n")
	write("a/a.go", "package a\n")
	write("a/b/b.go", "package b\n")
	write("tmp/serve/main.go", "package main\n")
	write("a/testdata/x.go", "package x\n")
	write("_skip/s.go", "package s\n")
	git("add", ".gitignore", "go.mod", "root.go", "a/a.go", "a/testdata/x.go", "_skip/s.go")

	got, err := goPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "./a", "./a/b"}
	if !slices.Equal(got, want) {
		t.Errorf("goPackages = %q, want %q", got, want)
	}
}

// The packages -update reruns are those whose tests declare the flag, which
// is what holding records means here.
func TestRecordPackagesDeclareUpdate(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"rec/rec_test.go":   "package rec\n\nimport \"flag\"\n\nvar update = flag.Bool(\"update\", false, \"\")\n",
		"plain/p_test.go":   "package plain\n",
		"none/none.go":      "package none\n",
		"rec/other_test.go": "package rec\n",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := []string{filepath.Join(root, "none"), filepath.Join(root, "plain"), filepath.Join(root, "rec")}
	got := recordPackages(pkgs)
	if want := pkgs[2:]; !slices.Equal(got, want) {
		t.Errorf("recordPackages = %q, want %q", got, want)
	}
}

// A file written, added or removed while the tests ran is what a failure
// reports as the tree changing under the run; one left alone is not.
func TestChangedFilesSeesEditsDuringTheRun(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	write(".gitignore", "scratch\n")
	write("kept.go", "package a\n")
	write("edited.go", "package a\n")
	write("removed.go", "package a\n")
	before, err := treeState(root)
	if err != nil {
		t.Fatal(err)
	}
	write("edited.go", "package a // an edit\n")
	write("added.go", "package a\n")
	write("scratch", "ignored\n")
	if err := os.Remove(filepath.Join(root, "removed.go")); err != nil {
		t.Fatal(err)
	}
	after, err := treeState(root)
	if err != nil {
		t.Fatal(err)
	}
	got := changedFiles(before, after)
	if want := []string{"added.go", "edited.go", "removed.go"}; !slices.Equal(got, want) {
		t.Errorf("changedFiles = %q, want %q", got, want)
	}
}
