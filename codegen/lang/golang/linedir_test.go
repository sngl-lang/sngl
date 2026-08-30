package golang

import (
	"os"
	"path/filepath"
	"testing"
)

// gc resolves a relative `//line` path against the directory of the file
// carrying the directive, so the path has to be relative to the output.
func TestLineDirPath_ResolvesFromEmittedFilesDirectory(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	outDir := filepath.Join(root, "out")
	for _, d := range []string{srcDir, outDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srcPath := filepath.Join(srcDir, "app.sngl")
	if err := os.WriteFile(srcPath, []byte("component main {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relToCwd, err := filepath.Rel(cwd, srcPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		file string
	}{
		{"absolute", srcPath},
		{"relative-to-cwd", relToCwd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := lineDirPath(tc.file, outDir)
			if filepath.IsAbs(got) {
				t.Fatalf("lineDirPath returned an absolute path %q", got)
			}
			if _, err := os.Stat(filepath.Join(outDir, got)); err != nil {
				t.Errorf("//line %s does not resolve from the emitted file's directory: %v", got, err)
			}
		})
	}
}

// With no output directory known (playground, LSP preview) the path stays
// as the compiler saw it.
func TestLineDirPath_NoBaseLeavesPathAlone(t *testing.T) {
	if got := lineDirPath("app.sngl", ""); got != "app.sngl" {
		t.Errorf("lineDirPath(%q, \"\") = %q, want it unchanged", "app.sngl", got)
	}
}
