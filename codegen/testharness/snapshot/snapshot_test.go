package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_textWritesGoldenOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Update: true}
	res, err := s.Assert("myFix", "view", "text/plain", []byte("hello"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if !res.Pass {
		t.Error("update mode should always pass")
	}
	want := filepath.Join(dir, "myFix.snapshots", "view.txt")
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("golden = %q", b)
	}
}

func TestStore_textPassesOnMatch(t *testing.T) {
	dir := t.TempDir()
	gd := filepath.Join(dir, "fix.snapshots")
	_ = os.MkdirAll(gd, 0o755)
	_ = os.WriteFile(filepath.Join(gd, "v.txt"), []byte("same"), 0o644)
	s := &Store{Dir: dir}
	res, err := s.Assert("fix", "v", "text/plain", []byte("same"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if !res.Pass {
		t.Errorf("expected pass, got fail: %s", res.Diff)
	}
}

func TestStore_textFailsOnMismatchWithDiff(t *testing.T) {
	dir := t.TempDir()
	gd := filepath.Join(dir, "fix.snapshots")
	_ = os.MkdirAll(gd, 0o755)
	_ = os.WriteFile(filepath.Join(gd, "v.txt"), []byte("alpha"), 0o644)
	s := &Store{Dir: dir}
	res, err := s.Assert("fix", "v", "text/plain", []byte("beta"))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if res.Pass {
		t.Error("expected fail on text mismatch")
	}
	if res.Diff == "" {
		t.Error("expected diff string on fail")
	}
}

func TestStore_unknownMimeReturnsError(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Update: true}
	if _, err := s.Assert("f", "v", "application/x-weird", []byte{0}); err == nil {
		t.Error("expected error on unknown mime")
	}
}
