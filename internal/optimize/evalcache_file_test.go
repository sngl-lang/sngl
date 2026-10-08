package optimize

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"duckfam.us/sngl/internal/asset"
)

// A `file:` import's path() is folded once per fold, and a package is folded
// several times over -- two probe rounds, the fold itself, and the second
// Optimize. Each one used to re-read the file and re-hash every byte of it,
// which the docs site paid eight times for a 96MB playground wasm.
func TestEvalCacheReadsAFileOnce(t *testing.T) {
	dir := t.TempDir()
	name := "asset.txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewEvalCache()
	first := c.file(dir, name)
	if first.err != nil {
		t.Fatalf("file: %v", first.err)
	}
	if want := asset.HashedName(name, []byte("first")); first.hashed != want {
		t.Errorf("hashed = %q, want %q", first.hashed, want)
	}

	if err := os.WriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := c.file(dir, name)
	if string(again.data) != "first" {
		t.Errorf("the file was read a second time: got %q", again.data)
	}
	if unsafe.SliceData(again.data) != unsafe.SliceData(first.data) {
		t.Error("two calls got two buffers; every call site's FileAsset should name one")
	}

	// Scoped to the compilation, not the process: a server that recompiles
	// gets a new cache, and must see the edit.
	if fresh := NewEvalCache().file(dir, name); string(fresh.data) != "second" {
		t.Errorf("a new cache did not re-read the file: got %q", fresh.data)
	}
}

// A missing file is an answer too, and evalFileFunc turns it into "this is not
// a constant" rather than an error -- so it must not be re-attempted per fold.
func TestEvalCacheRemembersAMissingFile(t *testing.T) {
	c := NewEvalCache()
	got := c.file(t.TempDir(), "nope.txt")
	if got.err == nil {
		t.Fatal("reading a missing file did not fail")
	}
	if again := c.file(t.TempDir(), "nope.txt"); again.err == nil {
		t.Error("the second call succeeded where the first failed")
	}
}
