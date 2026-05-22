//go:build !js

package codegen

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDirSink(t *testing.T) {
	root := t.TempDir()
	s := NewDirSink(root)
	w, err := s.Create("nested/dir/file.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := io.WriteString(w, "content"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "nested", "dir", "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "content" {
		t.Fatalf("got %q", got)
	}
}
