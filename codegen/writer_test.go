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

func TestCodeWriterImportDedup(t *testing.T) {
	sink := NewMemSink()
	var seen []ImportSpec
	lang := &fakeLang{
		header: func(name string, imports []ImportSpec) []byte {
			seen = imports
			return nil
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{})
	w.Import(ImportSpec{Path: "fmt", Kind: ImportNative})
	w.Import(ImportSpec{Path: "time", Kind: ImportNative})
	w.Import(ImportSpec{Path: "fmt", Kind: ImportNative}) // dup
	w.Import(ImportSpec{Path: "os", Kind: ImportNative})
	w.Close()
	if len(seen) != 3 {
		t.Fatalf("expected 3 imports got %d: %v", len(seen), seen)
	}
	wantOrder := []string{"fmt", "time", "os"}
	for i, s := range seen {
		if s.Path != wantOrder[i] {
			t.Errorf("position %d: got %q want %q", i, s.Path, wantOrder[i])
		}
	}
}

func TestCodeWriterImportAliasDistinct(t *testing.T) {
	// Same path with different aliases are distinct imports.
	sink := NewMemSink()
	var seen []ImportSpec
	lang := &fakeLang{
		header: func(name string, imports []ImportSpec) []byte {
			seen = imports
			return nil
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{})
	w.Import(ImportSpec{Path: "fmt", Kind: ImportNative})
	w.Import(ImportSpec{Path: "fmt", Alias: "stdfmt", Kind: ImportNative})
	w.Close()
	if len(seen) != 2 {
		t.Fatalf("expected 2 distinct imports got %d: %v", len(seen), seen)
	}
}

func TestCodeWriterPositionsRecordedWhenMapsOn(t *testing.T) {
	sink := NewMemSink()
	var seenPos []PosEntry
	lang := &fakeLang{
		srcmap: func(name string, pos []PosEntry, body []byte) SourceMapResult {
			seenPos = pos
			return SourceMapResult{}
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{Maps: true})
	io.WriteString(w, "AAA")
	w.Mark(ast.Pos{File: "f.sngl", Line: 5, Column: 1})
	io.WriteString(w, "BBB")
	w.WriteAt(ast.Pos{File: "f.sngl", Line: 6, Column: 1}, []byte("CCC"))
	w.Close()
	if len(seenPos) != 2 {
		t.Fatalf("got %d positions: %+v", len(seenPos), seenPos)
	}
	if seenPos[0].ByteOffset != 3 || seenPos[0].Pos.Line != 5 {
		t.Errorf("pos[0]: %+v", seenPos[0])
	}
	if seenPos[1].ByteOffset != 6 || seenPos[1].Pos.Line != 6 {
		t.Errorf("pos[1]: %+v", seenPos[1])
	}
}

func TestCodeWriterPositionsDroppedWhenMapsOff(t *testing.T) {
	sink := NewMemSink()
	calls := 0
	lang := &fakeLang{
		srcmap: func(name string, pos []PosEntry, body []byte) SourceMapResult {
			calls++
			return SourceMapResult{}
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{Maps: false})
	w.Mark(ast.Pos{File: "f.sngl", Line: 5, Column: 1})
	io.WriteString(w, "X")
	w.Close()
	if calls != 0 {
		t.Fatalf("RenderSourceMap should not be called when Maps off")
	}
}

func TestCodeWriterSidecarWritten(t *testing.T) {
	sink := NewMemSink()
	lang := &fakeLang{
		srcmap: func(name string, pos []PosEntry, body []byte) SourceMapResult {
			return SourceMapResult{Sidecar: []byte("SIDECAR"), SidecarName: "out.txt.map"}
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{Maps: true})
	io.WriteString(w, "B")
	w.Close()
	if string(sink.Files()["out.txt.map"]) != "SIDECAR" {
		t.Fatalf("sidecar missing: %v", sink.Files())
	}
}

func TestCodeWriterInlineBodyReplacement(t *testing.T) {
	sink := NewMemSink()
	lang := &fakeLang{
		srcmap: func(name string, pos []PosEntry, body []byte) SourceMapResult {
			return SourceMapResult{InlineBody: []byte("REWRITTEN")}
		},
	}
	w := openCodeFileFake(sink, "out.txt", lang, WriterOptions{Maps: true})
	io.WriteString(w, "ORIGINAL")
	w.Close()
	if string(sink.Files()["out.txt"]) != "REWRITTEN" {
		t.Fatalf("got %q", sink.Files()["out.txt"])
	}
}

func TestCodeWriterWriteAfterCloseFails(t *testing.T) {
	sink := NewMemSink()
	w := openCodeFileFake(sink, "out.txt", &fakeLang{}, WriterOptions{})
	w.Close()
	_, err := io.WriteString(w, "late")
	if err != io.ErrClosedPipe {
		t.Fatalf("got %v want io.ErrClosedPipe", err)
	}
}

// errSink fails Create with a fixed error, used to drive Close-failure paths.
type errSink struct{ err error }

func (e *errSink) Create(name string) (io.WriteCloser, error) { return nil, e.err }

func TestCodeWriterCloseErrorSticky(t *testing.T) {
	sentinel := io.ErrUnexpectedEOF
	w := openCodeFileFake(&errSink{err: sentinel}, "out.txt", &fakeLang{}, WriterOptions{})
	io.WriteString(w, "x")
	if err := w.Close(); err != sentinel {
		t.Fatalf("first Close: got %v want %v", err, sentinel)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: got %v want nil (sticky-success documented)", err)
	}
}
