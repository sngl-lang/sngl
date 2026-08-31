package lib_test

import (
	"io/fs"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/lib"
)

// One file per package carries the package comment. Go's semantics apply when
// several do -- they are concatenated in load order -- and that order is the
// directory listing, so whichever file sorted first opened the package: eight
// files in lib/ui each began with a one-line section header, and the package
// read as "Components that show a value and do not edit it".
//
// A section header belongs on the declarations it heads, or in the package's
// own doc file. Not at the top of a file above a blank line, which is the one
// place that makes it package prose.
func TestOnePackageCommentPerPackage(t *testing.T) {
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
		if len(carrying) > 1 {
			t.Errorf("sngl:%s takes its package comment from %d files (%s); "+
				"they concatenate in an order nothing pins, so keep it in one",
				pkg, len(carrying), strings.Join(carrying, ", "))
		}
		if len(carrying) == 0 {
			t.Errorf("sngl:%s has no package comment; `sngl doc` renders it with none", pkg)
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
	for _, line := range strings.Split(src, "\n") {
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
