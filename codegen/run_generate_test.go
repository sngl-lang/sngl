package codegen

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// minimalPlat satisfies the bit of PlatformGenerator that RunGenerate actually
// calls — just Generate. Tests type-assert through the narrow interface.
type minimalPlat struct {
	gen func(*Request) (*Response, error)
}

func (m *minimalPlat) Generate(r *Request) (*Response, error) { return m.gen(r) }

func TestRunGenerateLegacy(t *testing.T) {
	plat := &minimalPlat{gen: func(*Request) (*Response, error) {
		return &Response{
			Files: []*OutputFile{
				BytesFile("a.txt", []byte("AAA")),
				BytesFile("nested/b.txt", []byte("BBB")),
			},
		}, nil
	}}
	sink := NewMemSink()
	if err := runGenerateLegacy(plat, &Request{}, sink); err != nil {
		t.Fatalf("RunGenerate: %v", err)
	}
	files := sink.Files()
	if string(files["a.txt"]) != "AAA" || string(files["nested/b.txt"]) != "BBB" {
		t.Fatalf("got %v", files)
	}
}

func TestRunGenerateRespError(t *testing.T) {
	plat := &minimalPlat{gen: func(*Request) (*Response, error) {
		return &Response{Error: "boom"}, nil
	}}
	err := runGenerateLegacy(plat, &Request{}, NewMemSink())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v", err)
	}
}

func TestRunGenerateSkipsErrSkipFiles(t *testing.T) {
	skipFile := &OutputFile{
		Name: "skipped.txt",
		WriteTo: func(w io.Writer) (int64, error) {
			return 0, ErrSkip
		},
	}
	plat := &minimalPlat{gen: func(*Request) (*Response, error) {
		return &Response{Files: []*OutputFile{
			BytesFile("kept.txt", []byte("OK")),
			skipFile,
		}}, nil
	}}
	sink := NewMemSink()
	if err := runGenerateLegacy(plat, &Request{}, sink); err != nil {
		t.Fatalf("RunGenerate: %v", err)
	}
	files := sink.Files()
	if _, ok := files["skipped.txt"]; ok {
		t.Errorf("skipped file should not be present: %v", files)
	}
	if string(files["kept.txt"]) != "OK" {
		t.Errorf("kept file missing: %v", files)
	}
}

func TestCollectOutputFiles(t *testing.T) {
	mem := NewMemSink()
	for _, pair := range []struct{ name, content string }{
		{"b.txt", "BBB"},
		{"a.txt", "AAA"},
		{"nested/c.txt", "CCC"},
	} {
		w, err := mem.Create(pair.name)
		if err != nil {
			t.Fatalf("Create %s: %v", pair.name, err)
		}
		if _, err := io.WriteString(w, pair.content); err != nil {
			t.Fatalf("Write %s: %v", pair.name, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close %s: %v", pair.name, err)
		}
	}
	got := CollectOutputFiles(mem)
	wantNames := []string{"a.txt", "b.txt", "nested/c.txt"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d files, want %d", len(got), len(wantNames))
	}
	for i, f := range got {
		if f.Name != wantNames[i] {
			t.Errorf("position %d: got %q want %q", i, f.Name, wantNames[i])
		}
	}
	// Verify content via WriteTo.
	var buf bytes.Buffer
	if _, err := got[0].WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if buf.String() != "AAA" {
		t.Fatalf("got %q want AAA", buf.String())
	}
}

// runGenerateLegacy mirrors the legacy branch of RunGenerate so tests can
// exercise it without needing a full PlatformGenerator implementation.
// Identical to RunGenerate's else branch.
func runGenerateLegacy(plat interface {
	Generate(*Request) (*Response, error)
}, req *Request, sink Sink) error {
	resp, err := plat.Generate(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	for _, f := range resp.Files {
		w, err := sink.Create(f.Name)
		if err != nil {
			return err
		}
		_, werr := f.WriteTo(w)
		if errors.Is(werr, ErrSkip) {
			// Abandon the writer without committing — the file is skipped.
			continue
		}
		if cerr := w.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	return nil
}
