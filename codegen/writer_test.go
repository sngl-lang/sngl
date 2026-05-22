package codegen

import (
	"io"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

// fakeLang is a minimal stand-in for the header/sourcemap subset of
// LangTranslator. The writer uses only those two methods.
type fakeLang struct {
	header func(name string, imports []ImportSpec) []byte
	srcmap func(name string, pos []PosEntry, body []byte) SourceMapResult
}

func (f *fakeLang) RenderHeader(name string, imports []ImportSpec) []byte {
	if f.header != nil {
		return f.header(name, imports)
	}
	return nil
}

func (f *fakeLang) RenderSourceMap(name string, pos []PosEntry, body []byte) SourceMapResult {
	if f.srcmap != nil {
		return f.srcmap(name, pos, body)
	}
	return SourceMapResult{}
}

func TestCodeWriterBasicWrite(t *testing.T) {
	sink := NewMemSink()
	w := openCodeFileFake(sink, "out.txt", &fakeLang{}, WriterOptions{})
	io.WriteString(w, "hello world")
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := string(sink.Files()["out.txt"])
	if !strings.Contains(got, "hello world") {
		t.Fatalf("got %q", got)
	}
}

func TestCodeWriterHeaderBeforeBody(t *testing.T) {
	sink := NewMemSink()
	lang := &fakeLang{
		header: func(name string, imports []ImportSpec) []byte {
			return []byte("// HEADER\n")
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{})
	io.WriteString(w, "BODY")
	w.Close()
	got := string(sink.Files()["out.txt"])
	if got != "// HEADER\nBODY" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeWriterDoubleCloseSafe(t *testing.T) {
	sink := NewMemSink()
	w := openCodeFileFake(sink, "out.txt", &fakeLang{}, WriterOptions{})
	io.WriteString(w, "x")
	if err := w.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// Helper using the internal interface so tests can pass a fake instead of a
// full LangTranslator.
func openCodeFileFake(sink Sink, name string, lang headerRenderer, opts WriterOptions) CodeWriter {
	return newCodeWriter(sink, name, lang, opts)
}

// Keep ast import live; used by later tests.
var _ = ast.Pos{}
