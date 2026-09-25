package checker

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"git.duckfam.us/jonathan/sngl/ir"
)

// fakeTarget stands in for a platform that serves its own library package the
// way gtk4 does: PackageFS is what providedDocs reads and parses.
type fakeTarget struct {
	name  string
	fsys  fs.FS
	reads *int
}

func (t *fakeTarget) PlatformIdentifier() string { return t.name }
func (t *fakeTarget) Description() string        { return "fake" }
func (t *fakeTarget) PackageFS() fs.FS {
	*t.reads++
	return t.fsys
}

func newFakeChecker(t *testing.T) (*checker, *int) {
	t.Helper()
	reads := 0
	target := &fakeTarget{
		name:  "faketarget",
		reads: &reads,
		fsys: fstest.MapFS{
			"fake.sngl": &fstest.MapFile{Data: []byte("struct Fake {\n    n int\n}\n")},
		},
	}
	return &checker{cfg: &Config{Platforms: []ir.Platform{target}}}, &reads
}

// One check must parse a target's package once and hand every reader the same
// ASTs. providedDocs is reached from several places — hasLibPkg on each
// lookup, targetPkgScope, libDocs, libPkg, mergeTargetExtensions — and used to
// re-read and re-parse on every one of them. For gtk4 that is 148KB of
// synthesized widget source, about 26 times per check.
func TestProvidedDocs_ParsedOncePerCheck(t *testing.T) {
	c, reads := newFakeChecker(t)

	first := c.providedDocs("platform/faketarget")
	second := c.providedDocs("platform/faketarget")

	if *reads != 1 {
		t.Errorf("PackageFS read %d times in one check, want 1", *reads)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("got %d and %d docs, want 1 each", len(first), len(second))
	}
	if first[0] != second[0] {
		t.Error("two reads within one check returned different *ast.Document pointers")
	}
}

// A parse is shared across checks for the same bytes, and only for them.
// Nothing after the parser writes to an AST (TestProvidedDocsSurviveBuilds, in
// the root package, holds the compiler to that), so a second check reading the
// same source can take the first one's documents. A target reconfigured to
// serve a different package under the same file name -- gtk4 against another
// GIR -- gets a parse of what it now serves.
func TestProvidedDocs_SharedAcrossChecksBySource(t *testing.T) {
	c1, _ := newFakeChecker(t)
	c2, _ := newFakeChecker(t)

	first := c1.providedDocs("platform/faketarget")
	second := c2.providedDocs("platform/faketarget")

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("got %d and %d docs, want 1 each", len(first), len(second))
	}
	if first[0] != second[0] {
		t.Error("two checks of the same source parsed it twice")
	}

	c3, _ := newFakeChecker(t)
	target := c3.cfg.Platforms[0].(*fakeTarget)
	target.fsys = fstest.MapFS{
		"fake.sngl": &fstest.MapFile{Data: []byte("struct Other {\n    n int\n}\n")},
	}
	third := c3.providedDocs("platform/faketarget")
	if len(third) != 1 {
		t.Fatalf("got %d docs, want 1", len(third))
	}
	if third[0] == first[0] {
		t.Error("a target serving different source under the same name got the old parse")
	}
}

// The returned slice is a copy, so a caller that reorders or overwrites it
// cannot disturb the next reader in the same check.
func TestProvidedDocs_CallerCannotMutateTheCache(t *testing.T) {
	c, _ := newFakeChecker(t)

	got := c.providedDocs("platform/faketarget")
	want := got[0]
	got[0] = nil

	if again := c.providedDocs("platform/faketarget"); again[0] != want {
		t.Error("mutating the returned slice changed what the cache holds")
	}
}
