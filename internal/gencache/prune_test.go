package gencache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// touch writes an empty file at path, last used age ago.
func touch(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	os.Chtimes(path, when, when)
}

// A store holds more than its entries: a memo per compiler that has opened
// it, and whatever temporary file a write that crashed left behind. Both are
// pruned -- a memo by the age an entry goes at, a temporary once it is far
// older than any write takes -- and what is still in use stays.
func TestPruneRemovesSideFiles(t *testing.T) {
	dir := t.TempDir()
	day := 24 * time.Hour
	keep := []string{
		filepath.Join(dir, "ab", "live.sngl"),
		filepath.Join(dir, "ab", ".tmp-writing"),
		filepath.Join(dir, "compiler", "current"),
		filepath.Join(dir, "compiler", "current.tmp42"),
	}
	gone := []string{
		filepath.Join(dir, "ab", ".tmp-crashed"),
		filepath.Join(dir, "compiler", "superseded"),
		filepath.Join(dir, "compiler", "crashed.tmp7"),
	}
	for _, p := range keep {
		touch(t, p, time.Minute)
	}
	touch(t, gone[0], 2*tmpGrace)
	touch(t, gone[1], maxAge+day)
	touch(t, gone[2], 2*tmpGrace)

	prune(dir, maxBytes, maxAge)
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("pruned %s, which is in use", p)
		}
	}
	for _, p := range gone {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("kept %s", p)
		}
	}
}

// A memo that answers is in use, so it is not aged out from under the
// compiler reading it.
func TestCompilerMemoHitIsUse(t *testing.T) {
	memoDir := t.TempDir()
	id := compilerID(memoDir)
	entries, _ := os.ReadDir(memoDir)
	if id == "" || len(entries) != 1 {
		t.Fatalf("compilerID = %q with %d memo files, want an id and one", id, len(entries))
	}
	memo := filepath.Join(memoDir, entries[0].Name())
	old := time.Now().Add(-maxAge)
	os.Chtimes(memo, old, old)
	if again := compilerID(memoDir); again != id {
		t.Fatalf("the memo answered %q, want %q", again, id)
	}
	if info, err := os.Stat(memo); err != nil || time.Since(info.ModTime()) > time.Hour {
		t.Errorf("a memo that answered was not marked used")
	}
}

// Opening a store identifies nothing: a process that never asks it anything
// -- an evaluator binary that reads no generated file -- does not hash its
// own executable to find that out.
func TestOpenHashesNothing(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if _, err := os.Stat(filepath.Join(dir, "compiler")); err == nil {
		t.Fatal("Open identified the compiler before any request")
	}
	s.key(Request{Producer: "p"})
	if _, err := os.Stat(filepath.Join(dir, "compiler")); err != nil {
		t.Errorf("a request did not identify the compiler: %v", err)
	}
}
