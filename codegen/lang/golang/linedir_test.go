package golang

import (
	"os"
	"path/filepath"
	"testing"
)

// The Go compiler resolves a relative `//line` path against the directory of
// the file carrying the directive, not the working directory the build ran
// from. So a directive naming the SNGL source the way the compiler was
// invoked points at nothing once the .go file sits in the output directory —
// which is what lineDirPath rewrites.
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
				// An absolute path resolves on the machine that built it
				// and nowhere else, so golden output must never carry one.
				t.Fatalf("lineDirPath returned an absolute path %q", got)
			}
			if _, err := os.Stat(filepath.Join(outDir, got)); err != nil {
				t.Errorf("//line %s does not resolve from the emitted file's directory: %v", got, err)
			}
		})
	}
}

// With no output directory known (playground, LSP preview) the path stays
// exactly as the compiler saw it rather than being resolved against the cwd.
func TestLineDirPath_NoBaseLeavesPathAlone(t *testing.T) {
	if got := lineDirPath("app.sngl", ""); got != "app.sngl" {
		t.Errorf("lineDirPath(%q, \"\") = %q, want it unchanged", "app.sngl", got)
	}
}
