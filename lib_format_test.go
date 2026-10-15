package sngl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/parser"
)

// Every .sngl the compiler ships is written the way `sngl fmt` writes it,
// which `testdata/` has always been held to and the library was not.
//
// The gap is not cosmetic. A formatter defect in a construct only library
// source uses shows up as a diff here and nowhere else: the marks are that
// construct, and two of their defects -- a comment between two marks moved
// below the declaration, and a mark's argument list joined onto one line --
// were found by hand while writing them rather than by anything that runs.
//
// A file whose exact layout is the thing under test would need an opt-out, as
// a fixture's `// NOFMT` is. None does yet, which is why there is none.
func TestLibrarySourceIsFormatted(t *testing.T) {
	var files []string
	for _, root := range []string{"lib", "codegen/lang", "codegen/platform"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".sngl") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no library source found; the walk is looking in the wrong place")
	}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := parser.Parse(path, src)
			if err != nil {
				t.Fatalf("does not parse: %v", err)
			}
			if got := parser.Format(doc); got != string(src) {
				t.Errorf("not formatted; run `sngl fmt %s`\n%s", path, firstDiff(string(src), got))
			}
		})
	}
}

// firstDiff names the first line the two differ on, which is all a caller
// needs to go and look.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) && i < len(g); i++ {
		if w[i] != g[i] {
			return "line " + itoa(i+1) + ":\n  have: " + w[i] + "\n  want: " + g[i]
		}
	}
	return "the files differ in length: " + itoa(len(w)) + " lines against " + itoa(len(g))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
