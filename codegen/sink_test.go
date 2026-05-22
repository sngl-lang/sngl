package codegen

import (
	"io"
	"testing"
)

func TestMemSinkCreateAndRead(t *testing.T) {
	s := NewMemSink()
	w, err := s.Create("foo/bar.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := io.WriteString(w, "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	files := s.Files()
	got, ok := files["foo/bar.txt"]
	if !ok {
		t.Fatalf("file not present: %v", files)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestMemSinkOverwrite(t *testing.T) {
	s := NewMemSink()
	for _, content := range []string{"first", "second"} {
		w, err := s.Create("a.txt")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
		w.Close()
	}
	if string(s.Files()["a.txt"]) != "second" {
		t.Fatalf("want overwrite, got %q", s.Files()["a.txt"])
	}
}
