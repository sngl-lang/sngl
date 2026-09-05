package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDir_StableAcrossCalls(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	first, release, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	release()

	second, release2, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer release2()

	if first != second {
		t.Errorf("same key gave different dirs:\n  %s\n  %s", first, second)
	}
}

func TestBuildDir_DistinctKeys(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	a, relA, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer relA()
	b, relB, err := BuildDir("unit", "beta")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer relB()

	if a == b {
		t.Errorf("distinct keys shared dir %s", a)
	}
}

// A second holder of the same key must get its own directory, so two
// concurrent builds never write over each other.
func TestBuildDir_ConcurrentHoldersGetSeparateSlots(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	held, relHeld, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer relHeld()

	other, relOther, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer relOther()

	if held == other {
		t.Errorf("second holder reused the locked dir %s", held)
	}
}

// The path is reused but its contents are not: stale output from a
// previous build must never leak into the next one.
func TestBuildDir_EmptiesContents(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	dir, release, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release()

	again, release2, err := BuildDir("unit", "alpha")
	if err != nil {
		t.Fatalf("BuildDir: %v", err)
	}
	defer release2()

	if _, err := os.Stat(filepath.Join(again, "stale.go")); !os.IsNotExist(err) {
		t.Errorf("stale.go survived into the reused dir (err = %v)", err)
	}
}
