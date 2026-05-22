package codegen

import (
	"fmt"
	"io"
	"sync"
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
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	if string(s.Files()["a.txt"]) != "second" {
		t.Fatalf("want overwrite, got %q", s.Files()["a.txt"])
	}
}

func TestMemSinkConcurrentCreate(t *testing.T) {
	s := NewMemSink()
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("f%02d.txt", i)
			w, err := s.Create(name)
			if err != nil {
				t.Errorf("Create %s: %v", name, err)
				return
			}
			if _, err := io.WriteString(w, name); err != nil {
				t.Errorf("Write %s: %v", name, err)
			}
			if err := w.Close(); err != nil {
				t.Errorf("Close %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()
	files := s.Files()
	if len(files) != n {
		t.Fatalf("got %d files, want %d", len(files), n)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		if string(files[name]) != name {
			t.Errorf("file %s content mismatch: %q", name, files[name])
		}
	}
}

func TestMemSinkWriteAfterCloseFails(t *testing.T) {
	s := NewMemSink()
	w, _ := s.Create("x")
	w.Close()
	_, err := io.WriteString(w, "late")
	if err != io.ErrClosedPipe {
		t.Fatalf("got %v want io.ErrClosedPipe", err)
	}
}
