package lib_test

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/lib"
)

// A package's prose lives in its doc.sngl and nowhere else. Go's semantics --
// every file's package comment counts, concatenated in load order -- cannot
// say which order, and the blank line that makes a run of comments package
// prose rather than a doc comment on the declaration below is easy to leave in
// by accident: eight files in lib/ui each opened with a one-line section
// header, and the package read as "Components that show a value and do not
// edit it".
//
// So a section header belongs on the declarations it heads, and the package's
// own prose belongs in doc.sngl. `docs/lookup`'s stdlibPackageDocs reads it
// from there, so a header left above a blank line in another file is now
// silently dropped rather than silently published -- which is why this test
// asks the question from both sides.
func TestOnePackageCommentPerPackage(t *testing.T) {
	const docFile = "doc.sngl"
	checked := 0
	for _, pkg := range lib.Packages() {
		entries, err := lib.FS.ReadDir(pkg)
		if err != nil {
			t.Fatalf("reading lib/%s: %v", pkg, err)
		}
		var carrying []string
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			data, err := fs.ReadFile(lib.FS, pkg+"/"+e.Name())
			if err != nil {
				t.Fatalf("reading lib/%s/%s: %v", pkg, e.Name(), err)
			}
			if hasPackageComment(string(data)) {
				carrying = append(carrying, e.Name())
			}
		}
		checked++
		if !slices.Contains(carrying, docFile) {
			t.Errorf("sngl:%s has no package comment in %s; `sngl doc` renders it with none", pkg, docFile)
		}
		if stray := slices.DeleteFunc(carrying, func(n string) bool { return n == docFile }); len(stray) > 0 {
			t.Errorf("lib/%s: %s open with a comment run above a blank line, which reads as "+
				"package prose and is not published; put a header on the declarations it heads, "+
				"or move it into %s",
				pkg, strings.Join(stray, ", "), docFile)
		}
	}
	if checked == 0 {
		t.Fatal("no library packages walked")
	}
}

// A package comment is a run of line comments at the top of a file, separated
// from what follows by a blank line. Without the blank line it documents the
// declaration below instead.
func hasPackageComment(src string) bool {
	run := false
	for line := range strings.SplitSeq(src, "\n") {
		switch {
		case strings.HasPrefix(line, "//"):
			run = true
		case strings.TrimSpace(line) == "":
			return run
		default:
			return false
		}
	}
	return run
}
