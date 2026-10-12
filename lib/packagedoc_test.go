package lib_test

import (
	"io/fs"
	"strings"
	"testing"

	"duckfam.us/sngl/lib"
)

// A package's prose lives in its doc.sngl and nowhere else
// (checker.PackageDocFile): a leading comment in any other file is an
// ordinary comment about that file. So every library package needs one there,
// or `sngl doc` and the docs site render it with no description.
func TestEveryPackageHasADocComment(t *testing.T) {
	const docFile = "doc.sngl"
	checked := 0
	for _, pkg := range lib.Packages() {
		checked++
		data, err := fs.ReadFile(lib.FS, pkg+"/"+docFile)
		if err != nil || !hasPackageComment(string(data)) {
			t.Errorf("sngl:%s has no package comment in %s; `sngl doc` renders it with none", pkg, docFile)
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
