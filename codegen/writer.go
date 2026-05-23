package codegen

import (
	"bytes"
	"io"

	"git.duckfam.us/jonathan/sngl/ast"
)

// CodeWriter is the streaming interface lang translators emit into. Writes
// go to a buffered body; imports are dedup'd; AST positions are recorded for
// source-map generation. Close flushes header + body + optional sidecar to
// the underlying Sink.
//
// Position marks (Mark and WriteAt) should be placed at statement or
// expression boundaries, not mid-token. Source-map renderers may splice
// synthetic newlines or directives at mark offsets; mid-token splices are
// permitted but may degrade output formatting.
//
// Close failure is sticky: once Close has returned an error, a subsequent
// Close returns nil (the file is considered abandoned, not retried).
type CodeWriter interface {
	io.Writer
	WriteAt(pos ast.Pos, p []byte) (int, error)
	Mark(pos ast.Pos)
	Import(spec ImportSpec)
	Section(name string)
	Close() error
}

// WriterOptions controls CodeWriter behavior.
type WriterOptions struct {
	// Maps enables source-map generation. When true, marks are recorded and
	// RenderSourceMap is called at Close. When false, marks are dropped and
	// no sidecar is opened.
	Maps bool

	// Source is the SNGL source filename (base name only) the writer's
	// output was generated from. Passed through to RenderHeader so the
	// generated-by comment can name the source. Empty produces no header.
	Source string
}

// headerRenderer is the minimal subset of LangTranslator the writer needs.
// Every LangTranslator satisfies it.
type headerRenderer interface {
	RenderHeader(name, source string, imports []ImportSpec) []byte
	RenderSourceMap(name string, positions []PosEntry, body []byte) SourceMapResult
}

// OpenCodeFile constructs a CodeWriter that flushes to sink under name.
// lang provides header and source-map rendering at Close.
func OpenCodeFile(sink Sink, name string, lang headerRenderer, opts WriterOptions) CodeWriter {
	return newCodeWriter(sink, name, lang, opts)
}

func newCodeWriter(sink Sink, name string, lang headerRenderer, opts WriterOptions) *codeWriter {
	return &codeWriter{
		sink:        sink,
		name:        name,
		lang:        lang,
		opts:        opts,
		importsSeen: map[ImportSpec]struct{}{},
	}
}

type codeWriter struct {
	sink        Sink
	name        string
	lang        headerRenderer
	opts        WriterOptions
	body        bytes.Buffer
	importsSeen map[ImportSpec]struct{}
	importOrder []ImportSpec
	positions   []PosEntry
	closed      bool
}

func (w *codeWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.body.Write(p)
}

func (w *codeWriter) WriteAt(pos ast.Pos, p []byte) (int, error) {
	w.Mark(pos)
	return w.Write(p)
}

func (w *codeWriter) Mark(pos ast.Pos) {
	if !w.opts.Maps {
		return
	}
	if !pos.IsValid() {
		return
	}
	w.positions = append(w.positions, PosEntry{ByteOffset: w.body.Len(), Pos: pos})
}

func (w *codeWriter) Import(spec ImportSpec) {
	if _, ok := w.importsSeen[spec]; ok {
		return
	}
	w.importsSeen[spec] = struct{}{}
	w.importOrder = append(w.importOrder, spec)
}

func (w *codeWriter) Section(name string) {
	// Optional sectioning hint; reserved for future per-language grouping.
	_ = name
}

func (w *codeWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	body := w.body.Bytes()

	var sidecar []byte
	var sidecarName string
	if w.opts.Maps {
		res := w.lang.RenderSourceMap(w.name, w.positions, body)
		if res.InlineBody != nil {
			body = res.InlineBody
		}
		if res.Sidecar != nil && res.SidecarName != "" {
			sidecar = res.Sidecar
			sidecarName = res.SidecarName
		}
	}

	header := w.lang.RenderHeader(w.name, w.opts.Source, w.importOrder)

	f, err := w.sink.Create(w.name)
	if err != nil {
		return err
	}
	if _, err := f.Write(header); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if sidecar != nil {
		sf, err := w.sink.Create(sidecarName)
		if err != nil {
			return err
		}
		if _, err := sf.Write(sidecar); err != nil {
			sf.Close()
			return err
		}
		if err := sf.Close(); err != nil {
			return err
		}
	}
	return nil
}
