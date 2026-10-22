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
