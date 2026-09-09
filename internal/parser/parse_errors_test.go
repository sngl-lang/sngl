package parser_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func parseTestdataDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata")
}

// TestParseErrorDirectives evaluates every // ERROR(parse) directive in
// testdata/.
//
// Nothing did before: TestParseTestdata skips a file carrying one, the checker
// harness skips it too, and no third reader existed -- so a fixture asserting a
// parse error asserted that the file was skipped. Each directive names a
// message and sits on the line that should produce it, and both halves are
// checked here.
func TestParseErrorDirectives(t *testing.T) {
	dir := parseTestdataDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	found := 0
	for _, path := range matches {
		dirs, err := testutil.ParseDirectives(path)
		if err != nil {
			t.Fatalf("%s: directives: %v", path, err)
		}
		want := testutil.Filter(dirs, "parse")
		if len(want) == 0 {
			continue
		}
		found++
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			_, perr := parser.Parse(filepath.Base(path), src)
			if perr == nil {
				t.Fatalf("parsed without error; %d ERROR(parse) directive(s) expected one", len(want))
			}
			got := perr.Error()
			for _, d := range want {
				if !strings.Contains(got, d.Substring) {
					t.Errorf("line %d: error does not contain %q\n  got: %s", d.Pos(), d.Substring, got)
				}
				// Where the diagnostic lands is part of what the fixture
				// asserts: an error reported somewhere else is a different
				// error than the one the fixture is about.
				at := ":" + strconv.Itoa(d.Pos()) + ":"
				if d.AtCol != 0 {
					at += strconv.Itoa(d.AtCol)
				}
				if !strings.Contains(got, at) {
					t.Errorf("error is not reported at %s\n  got: %s", strings.Trim(at, ":"), got)
				}
			}
		})
	}
	if found == 0 {
		t.Fatal("no ERROR(parse) directives found; this test asserts nothing")
	}
	t.Logf("evaluated %d fixture(s)", found)
}

// A malformed parameter list must reach the caller as parse errors and nothing
// else. buildVisualOrStmt tested for a trailing assign, toggle and increment
// under one done() guard while each of the three consumed, so an exhausted
// iterator walked off the tree and the panic surfaced as
// "parser panic: index out of range" alongside the real errors.
//
// An empty entry is what exhausts it: ParamList admits a *trailing* comma, so
// `@change(,)` -- a comma with no Param before it -- leaves the recovery tree
// one element short of what the three tests read.
func TestMalformedParamListDoesNotPanic(t *testing.T) {
	src := []byte(`import . "sngl:ui"

component main ui {
    var d = ""
    input(value=d, @change(,) {
        d = ""
    })
}
`)
	_, err := parser.Parse("trailing.sngl", src)
	if err == nil {
		t.Fatal("parsed without error; a param list with no entry before its comma is not valid")
	}
	if strings.Contains(err.Error(), "parser panic") {
		t.Errorf("builder panicked: %s", err)
	}
	if !strings.Contains(err.Error(), `unexpected ")"`) {
		t.Errorf("expected the ordinary parse error, got: %s", err)
	}
}
