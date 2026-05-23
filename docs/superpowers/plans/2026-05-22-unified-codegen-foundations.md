# Unified Code Generation — Foundations (Plan A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the new `Sink`, `CodeWriter`, `ImportSpec`, and per-language source-map renderers (Go `//line`, JS `.map`) as additive infrastructure. No platform migrations and no deletions in this plan — those land in Plan B.

**Architecture:** New types live in `codegen/sink.go`, `codegen/writer.go`, `codegen/imports.go`. `LangTranslator` gains three additive methods (`RenderHeader`, `RenderSourceMap`, optional `EmitFile`) implemented on every existing language; existing `Response`/`OutputFile` API stays intact. `Options.Maps` is wired through `Request.Maps` and passed to `OpenCodeFile`. After this plan: future platform PRs can call the new API; nothing yet does.

**Tech Stack:** Go, `text/template`, `bytes.Buffer`, `io.WriteCloser`. Source-map v3 spec (VLQ encoding) for JS.

**Spec:** `docs/superpowers/specs/2026-05-22-unified-codegen-design.md`

---

## File Structure

**New files:**

- `codegen/sink.go` — `Sink` interface + `MemSink` impl. No build tag.
- `codegen/sink_dir.go` — `DirSink` impl. Build tag `//go:build !js`.
- `codegen/sink_test.go` — unit tests for both sinks.
- `codegen/imports.go` — `ImportSpec`, `ImportKind` enum + `String()`.
- `codegen/writer.go` — `CodeWriter` interface, `codeWriter` impl, `OpenCodeFile`, `PosEntry`, `SourceMapResult`, `WriterOptions`.
- `codegen/writer_test.go` — writer behavior tests using a fake `LangTranslator`.
- `codegen/lang/golang/sourcemap.go` — Go `//line` directive renderer.
- `codegen/lang/golang/sourcemap_test.go`
- `codegen/lang/javascript/sourcemap.go` — JS v3 source-map (VLQ encoder + JSON marshal).
- `codegen/lang/javascript/sourcemap_test.go`

**Modified files:**

- `codegen/codegen.go` — add three methods to `LangTranslator`. Add `Maps bool` to `Request`.
- `codegen/lang/golang/golang.go` — implement `RenderHeader`, `RenderSourceMap`.
- `codegen/lang/javascript/javascript.go` — same.
- `codegen/lang/kotlin/kotlin.go` — stub `RenderHeader` (real impl), `RenderSourceMap` returns zero `SourceMapResult`.
- `codegen/lang/none/none.go` — stub all three.
- `cmd/sngl/compile.go`, `cmd/sngl/build.go`, `cmd/sngl/run.go` — read `Options.maps` and forward to `Request.Maps`.

---

## Task 1: Sink interface + MemSink

**Files:**
- Create: `codegen/sink.go`
- Test: `codegen/sink_test.go`

- [ ] **Step 1: Write failing test for MemSink**

```go
// codegen/sink_test.go
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
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen -run TestMemSink -v`
Expected: FAIL (`undefined: NewMemSink`).

- [ ] **Step 3: Implement Sink + MemSink**

```go
// codegen/sink.go
package codegen

import (
	"bytes"
	"io"
	"sync"
)

// Sink is a write-only filesystem abstraction. Code-generation output is
// streamed into it. Implementations decide whether name resolves to disk,
// memory, network, etc.
type Sink interface {
	// Create returns a writer for the given relative path. Forward slashes.
	// The sink is responsible for creating any parent directories.
	// Closing the returned writer commits the file.
	Create(name string) (io.WriteCloser, error)
}

// MemSink accumulates files in memory. Safe for concurrent Create calls;
// the returned writers are not.
type MemSink struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemSink() *MemSink {
	return &MemSink{files: map[string][]byte{}}
}

func (m *MemSink) Create(name string) (io.WriteCloser, error) {
	return &memWriter{sink: m, name: name}, nil
}

// Files returns a snapshot map of name → contents. Mutating the returned map
// does not affect the sink.
func (m *MemSink) Files() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.files))
	for k, v := range m.files {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

type memWriter struct {
	sink *MemSink
	name string
	buf  bytes.Buffer
	done bool
}

func (w *memWriter) Write(p []byte) (int, error) {
	if w.done {
		return 0, io.ErrClosedPipe
	}
	return w.buf.Write(p)
}

func (w *memWriter) Close() error {
	if w.done {
		return nil
	}
	w.done = true
	w.sink.mu.Lock()
	defer w.sink.mu.Unlock()
	w.sink.files[w.name] = w.buf.Bytes()
	return nil
}
```

- [ ] **Step 4: Run and verify pass**

Run: `go test ./codegen -run TestMemSink -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/sink.go codegen/sink_test.go
git commit -m "codegen: add Sink interface + MemSink

Stream-based, filesystem-abstract output. MemSink supports concurrent
Create; per-file writes are not concurrent. WASM-safe (no os deps)."
```

---

## Task 2: DirSink

**Files:**
- Create: `codegen/sink_dir.go`
- Modify: `codegen/sink_test.go`

- [ ] **Step 1: Write failing test for DirSink**

Append to `codegen/sink_test.go`:

```go
!js

package p
func TestDirSink(t *testing.T) {
	root := t.TempDir()
	s := NewDirSink(root)
	w, err := s.Create("nested/dir/file.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	io.WriteString(w, "content")
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
```

Add to imports at top of `sink_test.go`:

```go
import (
	"io"
	"os"
	"path/filepath"
	"testing"
)
```

Also wrap the existing `TestMemSink*` functions with `//go:build !js` only if `os`/`filepath` adds were required — but MemSink tests don't need them, so split: put `TestDirSink` in its own file `codegen/sink_dir_test.go` build-tagged `!js`.

Revise — create `codegen/sink_dir_test.go` separately:

```go
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
	io.WriteString(w, "content")
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
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen -run TestDirSink -v`
Expected: FAIL (`undefined: NewDirSink`).

- [ ] **Step 3: Implement DirSink**

```go
// codegen/sink_dir.go
//go:build !js

package codegen

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DirSink writes files into Root on the local filesystem. Parent directories
// are created on demand. Not available on WASM (use MemSink there).
type DirSink struct {
	Root string
}

func NewDirSink(root string) *DirSink { return &DirSink{Root: root} }

func (d *DirSink) Create(name string) (io.WriteCloser, error) {
	clean := filepath.FromSlash(strings.TrimPrefix(name, "/"))
	full := filepath.Join(d.Root, clean)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	return os.Create(full)
}
```

- [ ] **Step 4: Run and verify pass**

Run: `go test ./codegen -run TestDirSink -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/sink_dir.go codegen/sink_dir_test.go
git commit -m "codegen: add DirSink

Disk-backed Sink. Build-tagged !js so WASM playground only sees MemSink."
```

---

## Task 3: ImportSpec + ImportKind

**Files:**
- Create: `codegen/imports.go`
- Create: `codegen/imports_test.go`

- [ ] **Step 1: Write failing tests**

```go
// codegen/imports_test.go
package codegen

import "testing"

func TestImportSpecComparable(t *testing.T) {
	// Used as map key in CodeWriter; must be comparable.
	a := ImportSpec{Path: "fmt", Kind: ImportNative}
	b := ImportSpec{Path: "fmt", Kind: ImportNative}
	m := map[ImportSpec]struct{}{a: {}}
	if _, ok := m[b]; !ok {
		t.Fatal("equal specs should hash to same key")
	}
}

func TestImportKindString(t *testing.T) {
	cases := []struct {
		k    ImportKind
		want string
	}{
		{ImportNative, "native"},
		{ImportStdlibRuntime, "stdlib-runtime"},
		{ImportCgo, "cgo"},
		{ImportEsModule, "esmodule"},
		{ImportWasmExtern, "wasm-extern"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("ImportKind(%d).String() = %q want %q", c.k, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen -run TestImport -v`
Expected: FAIL (`undefined: ImportSpec`).

- [ ] **Step 3: Implement**

```go
// codegen/imports.go
package codegen

// ImportSpec describes a single import an emitted file needs. The lang
// translator decides how to render the import line; the writer dedups by
// value and preserves insertion order.
type ImportSpec struct {
	Path  string
	Alias string
	Kind  ImportKind
}

// ImportKind tells the lang translator how to render an ImportSpec.
type ImportKind int

const (
	ImportNative        ImportKind = iota // language-native import
	ImportStdlibRuntime                   // sngl pkg/<lang>/<name>
	ImportCgo                             // C header via cgo preamble
	ImportEsModule                        // ES module (JS bundler input)
	ImportWasmExtern                      // WASM extern bridge
)

func (k ImportKind) String() string {
	switch k {
	case ImportNative:
		return "native"
	case ImportStdlibRuntime:
		return "stdlib-runtime"
	case ImportCgo:
		return "cgo"
	case ImportEsModule:
		return "esmodule"
	case ImportWasmExtern:
		return "wasm-extern"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 4: Run and verify pass**

Run: `go test ./codegen -run TestImport -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/imports.go codegen/imports_test.go
git commit -m "codegen: add ImportSpec + ImportKind

Value-typed, comparable so CodeWriter can dedup by map key."
```

---

## Task 4: PosEntry + SourceMapResult types

**Files:**
- Create: `codegen/sourcemap.go`

- [ ] **Step 1: Implement (no test — pure types)**

```go
// codegen/sourcemap.go
package codegen

import "git.duckfam.us/jonathan/sngl/ast"

// PosEntry records that the byte at ByteOffset in the body corresponds to
// the AST source location Pos.
type PosEntry struct {
	ByteOffset int
	Pos        ast.Pos
}

// SourceMapResult is returned by LangTranslator.RenderSourceMap.
//
// If InlineBody is non-nil, the writer uses it in place of the original body
// (e.g. Go splices //line directives into the body).
// If Sidecar is non-nil, the writer opens SidecarName via the Sink and writes
// Sidecar to it.
type SourceMapResult struct {
	InlineBody  []byte
	Sidecar     []byte
	SidecarName string
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./codegen`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add codegen/sourcemap.go
git commit -m "codegen: add PosEntry + SourceMapResult"
```

---

## Task 5: LangTranslator additive methods (interface change)

**Files:**
- Modify: `codegen/codegen.go`
- Modify: `codegen/lang/golang/golang.go`
- Modify: `codegen/lang/javascript/javascript.go`
- Modify: `codegen/lang/kotlin/kotlin.go`
- Modify: `codegen/lang/none/none.go`

- [ ] **Step 1: Add the three new methods to the LangTranslator interface**

In `codegen/codegen.go`, locate the `LangTranslator` interface (currently at line ~98). Append:

```go
	// RenderHeader returns the file header bytes — the generated-by comment
	// plus rendered import lines. Called once at CodeWriter.Close. Imports
	// are in insertion order; the renderer may sort or filter.
	RenderHeader(name string, imports []ImportSpec) []byte

	// RenderSourceMap optionally rewrites the body to embed source-mapping
	// information (Go //line directives) or produces a sidecar (JS .map).
	// Called by CodeWriter.Close only when source maps are enabled.
	// Returning a zero SourceMapResult means "no changes".
	RenderSourceMap(name string, positions []PosEntry, body []byte) SourceMapResult
```

(The dead `WriteExpr`/`WriteStmt`/`WriteType`/`Eval` stubs stay for now — Plan B deletes them.)

- [ ] **Step 2: Implement stubs on every language (so codebase still compiles)**

In `codegen/lang/golang/golang.go`, append to the `Translator` impl:

```go
func (t *Translator) RenderHeader(name string, imports []codegen.ImportSpec) []byte {
	// Full implementation lands in Task 9. Stub returns the existing
	// generated-by header, no imports.
	return []byte(codegen.Header("go", name, "// ", ""))
}

func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	// Full impl in Task 9.
	return codegen.SourceMapResult{}
}
```

In `codegen/lang/javascript/javascript.go`:

```go
func (t *Translator) RenderHeader(name string, imports []codegen.ImportSpec) []byte {
	return []byte(codegen.Header("js", name, "// ", ""))
}

func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return codegen.SourceMapResult{}
}
```

In `codegen/lang/kotlin/kotlin.go`:

```go
func (t *Translator) RenderHeader(name string, imports []codegen.ImportSpec) []byte {
	return []byte(codegen.Header("kt", name, "// ", ""))
}

func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	// SMAP support deferred — spec section 7.
	return codegen.SourceMapResult{}
}
```

In `codegen/lang/none/none.go`:

```go
func (t *Translator) RenderHeader(name string, imports []codegen.ImportSpec) []byte { return nil }
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return codegen.SourceMapResult{}
}
```

(Check each file for the exact `Translator` type name. If `none` uses `noneTranslator` or similar, match.)

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 4: Verify existing tests still pass**

Run: `go test ./codegen/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/codegen.go codegen/lang/golang/golang.go codegen/lang/javascript/javascript.go codegen/lang/kotlin/kotlin.go codegen/lang/none/none.go
git commit -m "codegen: add RenderHeader + RenderSourceMap to LangTranslator

Additive: every language gets stub impls. Real Go/JS impls land in
Task 9 and Task 11. Kotlin SMAP deferred per spec."
```

---

## Task 6: CodeWriter — body buffer + basic writes

**Files:**
- Create: `codegen/writer.go`
- Create: `codegen/writer_test.go`

- [ ] **Step 1: Write failing test for basic Write + Close**

```go
// codegen/writer_test.go
package codegen

import (
	"io"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

// fakeLang is a minimal LangTranslator stand-in for writer tests. It is NOT
// the real interface (which requires many methods); we cast via a private
// subset interface the writer actually uses.
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

// Helper that uses the internal interface so we can test before LangTranslator
// stubs are wired through Task 5. Bridges fakeLang into codeWriter directly.
func openCodeFileFake(sink Sink, name string, lang headerRenderer, opts WriterOptions) CodeWriter {
	return newCodeWriter(sink, name, lang, opts)
}

// ast.Pos referenced to keep import; unused now.
var _ = ast.Pos{}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen -run TestCodeWriter -v`
Expected: FAIL (`undefined: WriterOptions`, etc.).

- [ ] **Step 3: Implement CodeWriter (write + header + close path)**

```go
// codegen/writer.go
package codegen

import (
	"bytes"
	"io"

	"git.duckfam.us/jonathan/sngl/ast"
)

// CodeWriter is the streaming interface lang translators emit into.
// Writes go to a buffered body. Imports are dedup'd. AST positions are
// recorded for source-map generation. Close flushes header + body + sidecar
// to the underlying Sink.
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
	// Maps enables source-map generation: position tracking on, and
	// RenderSourceMap is called at Close. When false, marks are dropped
	// and no sidecar is opened.
	Maps bool
}

// headerRenderer is the minimal subset of LangTranslator the writer uses.
// LangTranslator embeds these methods (added in Task 5).
type headerRenderer interface {
	RenderHeader(name string, imports []ImportSpec) []byte
	RenderSourceMap(name string, positions []PosEntry, body []byte) SourceMapResult
}

// OpenCodeFile is the public constructor. lang must implement the two header /
// sourcemap methods (every LangTranslator does after Task 5).
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
	// Optional logical sectioning hint. No-op for now; reserved for future
	// per-language header grouping.
	_ = name
}

func (w *codeWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	body := w.body.Bytes()

	// Source-map pass first — Go may rewrite the body to splice //line.
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

	header := w.lang.RenderHeader(w.name, w.importOrder)

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
```

- [ ] **Step 4: Run and verify pass**

Run: `go test ./codegen -run TestCodeWriter -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/writer.go codegen/writer_test.go
git commit -m "codegen: add CodeWriter — body buffer + header flush

Writes to bytes.Buffer; Close flushes header + body to Sink via lang
translator's RenderHeader. Source-map path wired but unused until
per-lang RenderSourceMap impls land."
```

---

## Task 7: CodeWriter — Import dedup + ordering

**Files:**
- Modify: `codegen/writer_test.go`

- [ ] **Step 1: Add failing test for import dedup + ordering**

Append to `codegen/writer_test.go`:

```go
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
```

- [ ] **Step 2: Run and verify pass**

Run: `go test ./codegen -run TestCodeWriterImport -v`
Expected: PASS (impl already covers this — ImportSpec is a comparable value used as map key directly).

- [ ] **Step 3: Commit**

```bash
git add codegen/writer_test.go
git commit -m "codegen: test CodeWriter import dedup + insertion order"
```

---

## Task 8: CodeWriter — position tracking gated on Maps option

**Files:**
- Modify: `codegen/writer_test.go`

- [ ] **Step 1: Add failing test for position recording**

Append:

```go
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
```

- [ ] **Step 2: Run and verify pass**

Run: `go test ./codegen -run TestCodeWriter -v`
Expected: PASS (implementation in Task 6 already covers this; tests pin behavior).

- [ ] **Step 3: Commit**

```bash
git add codegen/writer_test.go
git commit -m "codegen: test CodeWriter Maps gating + sidecar + inline body"
```

---

## Task 9: Go //line directive renderer

**Files:**
- Create: `codegen/lang/golang/sourcemap.go`
- Create: `codegen/lang/golang/sourcemap_test.go`
- Modify: `codegen/lang/golang/golang.go`

- [ ] **Step 1: Write failing test**

```go
// codegen/lang/golang/sourcemap_test.go
package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func TestRenderGoSourceMap_InsertsLineDirectives(t *testing.T) {
	body := []byte("first()\nsecond()\nthird()\n")
	// Mark byte 0 as f.sngl:10, byte 16 (start of "third()") as f.sngl:20.
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 10, Column: 1}},
		{ByteOffset: 16, Pos: ast.Pos{File: "f.sngl", Line: 20, Column: 1}},
	}
	out := renderGoSourceMap("model.go", positions, body)
	if out.Sidecar != nil {
		t.Errorf("Go should not emit a sidecar, got %d bytes", len(out.Sidecar))
	}
	if out.InlineBody == nil {
		t.Fatal("InlineBody must be set")
	}
	got := string(out.InlineBody)
	if !strings.Contains(got, "//line f.sngl:10\n") {
		t.Errorf("missing first //line directive: %q", got)
	}
	if !strings.Contains(got, "//line f.sngl:20\n") {
		t.Errorf("missing second //line directive: %q", got)
	}
	// Directive comes BEFORE the source bytes it covers.
	idx := strings.Index(got, "first()")
	dirIdx := strings.Index(got, "//line f.sngl:10")
	if dirIdx > idx {
		t.Errorf("first //line directive must precede its code; dirIdx=%d codeIdx=%d", dirIdx, idx)
	}
}

func TestRenderGoSourceMap_NoPositionsReturnsZero(t *testing.T) {
	out := renderGoSourceMap("model.go", nil, []byte("x"))
	if out.InlineBody != nil || out.Sidecar != nil {
		t.Fatalf("expected zero result, got %+v", out)
	}
}

func TestRenderGoSourceMap_CollapsesAdjacentSameLine(t *testing.T) {
	body := []byte("aaaabbbb")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 5}},
		{ByteOffset: 4, Pos: ast.Pos{File: "f.sngl", Line: 5}}, // same line, no new directive
	}
	out := renderGoSourceMap("model.go", positions, body)
	count := strings.Count(string(out.InlineBody), "//line ")
	if count != 1 {
		t.Errorf("expected 1 //line directive, got %d: %s", count, out.InlineBody)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen/lang/golang -run TestRenderGoSourceMap -v`
Expected: FAIL (`undefined: renderGoSourceMap`).

- [ ] **Step 3: Implement**

```go
// codegen/lang/golang/sourcemap.go
package golang

import (
	"bytes"
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// renderGoSourceMap splices //line directives into body at positions where
// the source line changes. Go's compiler reads //line to attribute compile
// errors and panics to the original source.
//
// A //line directive at column 1 of a line attributes the NEXT line to the
// given source. We emit them on their own line just before the byte offset
// they apply to.
func renderGoSourceMap(_ string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	if len(positions) == 0 {
		return codegen.SourceMapResult{}
	}

	var out bytes.Buffer
	out.Grow(len(body) + 64*len(positions))

	cursor := 0
	var lastFile string
	var lastLine int

	for _, p := range positions {
		if p.ByteOffset < cursor || p.ByteOffset > len(body) {
			continue
		}
		if !p.Pos.IsValid() {
			continue
		}
		if p.Pos.File == lastFile && p.Pos.Line == lastLine {
			continue
		}
		// Write body up to this position.
		out.Write(body[cursor:p.ByteOffset])
		// If the previous byte was not a newline, insert one so the
		// //line directive starts at column 1.
		if out.Len() > 0 {
			last := out.Bytes()[out.Len()-1]
			if last != '\n' {
				out.WriteByte('\n')
			}
		}
		fmt.Fprintf(&out, "//line %s:%d\n", p.Pos.File, p.Pos.Line)
		cursor = p.ByteOffset
		lastFile = p.Pos.File
		lastLine = p.Pos.Line
	}
	out.Write(body[cursor:])
	return codegen.SourceMapResult{InlineBody: out.Bytes()}
}
```

- [ ] **Step 4: Wire into Translator.RenderSourceMap**

In `codegen/lang/golang/golang.go`, replace the stub from Task 5:

```go
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return renderGoSourceMap(name, positions, body)
}
```

- [ ] **Step 5: Run and verify pass**

Run: `go test ./codegen/lang/golang -run TestRenderGoSourceMap -v`
Expected: PASS.

- [ ] **Step 6: Run full suite for safety**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/lang/golang/sourcemap.go codegen/lang/golang/sourcemap_test.go codegen/lang/golang/golang.go
git commit -m "lang/go: //line source-map renderer

Splices //line file:line directives at line-change points in the
generated body. Go compiler attributes errors/panics to original
SNGL source."
```

---

## Task 10: JS source-map v3 — VLQ encoder

**Files:**
- Create: `codegen/lang/javascript/vlq.go`
- Create: `codegen/lang/javascript/vlq_test.go`

- [ ] **Step 1: Write failing tests for VLQ encoder**

Source-map v3 uses base64 VLQ. Spec values:
- `0` → `"A"`, `1` → `"C"`, `-1` → `"D"`, `16` → `"gB"`, `123` → `"2H"`.

```go
// codegen/lang/javascript/vlq_test.go
package javascript

import "testing"

func TestEncodeVLQ(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "A"},
		{1, "C"},
		{-1, "D"},
		{2, "E"},
		{-2, "F"},
		{16, "gB"},
		{-16, "hB"},
		{123, "2H"},
		{-123, "3H"},
		{456, "wc"},
	}
	for _, c := range cases {
		if got := encodeVLQ(c.in); got != c.want {
			t.Errorf("encodeVLQ(%d) = %q want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen/lang/javascript -run TestEncodeVLQ -v`
Expected: FAIL.

- [ ] **Step 3: Implement VLQ**

```go
// codegen/lang/javascript/vlq.go
package javascript

import "strings"

// Base64 VLQ encoding for source-map v3.
//
// A VLQ digit is 5 bits of value + 1 continuation bit. The lowest bit of the
// first digit is the sign bit (1 = negative). Digits are base64-encoded
// using the standard source-map alphabet.

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeVLQ(v int) string {
	var u uint32
	if v < 0 {
		u = (uint32(-v) << 1) | 1
	} else {
		u = uint32(v) << 1
	}
	var sb strings.Builder
	for {
		digit := u & 0x1F
		u >>= 5
		if u > 0 {
			digit |= 0x20 // continuation
		}
		sb.WriteByte(b64Alphabet[digit])
		if u == 0 {
			break
		}
	}
	return sb.String()
}
```

- [ ] **Step 4: Run and verify pass**

Run: `go test ./codegen/lang/javascript -run TestEncodeVLQ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/vlq.go codegen/lang/javascript/vlq_test.go
git commit -m "lang/js: base64 VLQ encoder for source-map v3"
```

---

## Task 11: JS source-map v3 renderer

**Files:**
- Create: `codegen/lang/javascript/sourcemap.go`
- Create: `codegen/lang/javascript/sourcemap_test.go`
- Modify: `codegen/lang/javascript/javascript.go`

- [ ] **Step 1: Write failing test**

```go
// codegen/lang/javascript/sourcemap_test.go
package javascript

import (
	"encoding/json"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func TestRenderJSSourceMap_Basic(t *testing.T) {
	body := []byte("first;\nsecond;\nthird;\n")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 10, Column: 1}},
		{ByteOffset: 7, Pos: ast.Pos{File: "f.sngl", Line: 11, Column: 1}},
		{ByteOffset: 15, Pos: ast.Pos{File: "f.sngl", Line: 12, Column: 1}},
	}
	res := renderJSSourceMap("model.js", positions, body)
	if res.SidecarName != "model.js.map" {
		t.Errorf("SidecarName=%q want model.js.map", res.SidecarName)
	}
	if res.Sidecar == nil {
		t.Fatal("Sidecar must be set")
	}
	// Inline body must end with sourceMappingURL comment.
	if !strings.HasSuffix(string(res.InlineBody), "//# sourceMappingURL=model.js.map\n") {
		t.Errorf("missing sourceMappingURL footer:\n%s", res.InlineBody)
	}
	// Parse sidecar JSON.
	var m struct {
		Version  int      `json:"version"`
		File     string   `json:"file"`
		Sources  []string `json:"sources"`
		Names    []string `json:"names"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatalf("sidecar not valid JSON: %v\n%s", err, res.Sidecar)
	}
	if m.Version != 3 {
		t.Errorf("version=%d want 3", m.Version)
	}
	if m.File != "model.js" {
		t.Errorf("file=%q want model.js", m.File)
	}
	if len(m.Sources) != 1 || m.Sources[0] != "f.sngl" {
		t.Errorf("sources=%v want [f.sngl]", m.Sources)
	}
	if m.Mappings == "" {
		t.Errorf("empty mappings")
	}
}

func TestRenderJSSourceMap_NoPositionsReturnsZero(t *testing.T) {
	res := renderJSSourceMap("x.js", nil, []byte("foo"))
	if res.InlineBody != nil || res.Sidecar != nil {
		t.Fatalf("expected zero, got %+v", res)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./codegen/lang/javascript -run TestRenderJSSourceMap -v`
Expected: FAIL.

- [ ] **Step 3: Implement renderer**

```go
// codegen/lang/javascript/sourcemap.go
package javascript

import (
	"bytes"
	"encoding/json"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// renderJSSourceMap produces a source-map v3 sidecar and appends a
// sourceMappingURL footer to the body.
//
// Mappings are organized one segment per output line. Each segment is five
// VLQ-encoded integers (deltas from the previous segment):
//
//	0: generated column
//	1: source-file index
//	2: source line
//	3: source column
//	4: name index (omitted; we don't emit name mappings)
//
// We emit at most one mapping per generated line — the first PosEntry whose
// byte offset falls in that line. Finer-grained column maps would require
// per-token marking, which we don't have today.
func renderJSSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	if len(positions) == 0 {
		return codegen.SourceMapResult{}
	}

	// Collect unique source files (preserve insertion order).
	srcIdx := map[string]int{}
	var sources []string
	for _, p := range positions {
		if _, ok := srcIdx[p.Pos.File]; !ok {
			srcIdx[p.Pos.File] = len(sources)
			sources = append(sources, p.Pos.File)
		}
	}

	// Build a map from generated-line index → first PosEntry on that line.
	lineStart := computeLineStarts(body) // byte offset of start of each line
	type seg struct {
		genCol  int
		srcIdx  int
		srcLine int // 0-based
		srcCol  int // 0-based
	}
	perLine := make([]*seg, len(lineStart))
	for _, p := range positions {
		if !p.Pos.IsValid() {
			continue
		}
		gl, gc := genLineCol(lineStart, p.ByteOffset)
		if perLine[gl] != nil {
			continue
		}
		perLine[gl] = &seg{
			genCol:  gc,
			srcIdx:  srcIdx[p.Pos.File],
			srcLine: p.Pos.Line - 1,
			srcCol:  max0(p.Pos.Column - 1),
		}
	}

	// Encode mappings.
	var mb strings.Builder
	var prevGenCol, prevSrcIdx, prevSrcLine, prevSrcCol int
	for i, s := range perLine {
		if i > 0 {
			mb.WriteByte(';')
			prevGenCol = 0 // gen column resets per line
		}
		if s == nil {
			continue
		}
		mb.WriteString(encodeVLQ(s.genCol - prevGenCol))
		mb.WriteString(encodeVLQ(s.srcIdx - prevSrcIdx))
		mb.WriteString(encodeVLQ(s.srcLine - prevSrcLine))
		mb.WriteString(encodeVLQ(s.srcCol - prevSrcCol))
		prevGenCol = s.genCol
		prevSrcIdx = s.srcIdx
		prevSrcLine = s.srcLine
		prevSrcCol = s.srcCol
	}

	doc := struct {
		Version    int      `json:"version"`
		File       string   `json:"file"`
		SourceRoot string   `json:"sourceRoot,omitempty"`
		Sources    []string `json:"sources"`
		Names      []string `json:"names"`
		Mappings   string   `json:"mappings"`
	}{
		Version:  3,
		File:     name,
		Sources:  sources,
		Names:    []string{},
		Mappings: mb.String(),
	}
	sidecar, _ := json.Marshal(doc)

	var inline bytes.Buffer
	inline.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		inline.WriteByte('\n')
	}
	inline.WriteString("//# sourceMappingURL=")
	inline.WriteString(name)
	inline.WriteString(".map\n")

	return codegen.SourceMapResult{
		InlineBody:  inline.Bytes(),
		Sidecar:     sidecar,
		SidecarName: name + ".map",
	}
}

func computeLineStarts(body []byte) []int {
	starts := []int{0}
	for i, b := range body {
		if b == '\n' && i+1 <= len(body) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func genLineCol(lineStart []int, off int) (line, col int) {
	// Binary search.
	lo, hi := 0, len(lineStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStart[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, off - lineStart[lo]
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
```

- [ ] **Step 4: Wire into Translator.RenderSourceMap**

In `codegen/lang/javascript/javascript.go`, replace the Task 5 stub:

```go
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return renderJSSourceMap(name, positions, body)
}
```

- [ ] **Step 5: Run and verify pass**

Run: `go test ./codegen/lang/javascript -run TestRenderJSSourceMap -v`
Expected: PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/lang/javascript/sourcemap.go codegen/lang/javascript/sourcemap_test.go codegen/lang/javascript/javascript.go
git commit -m "lang/js: source-map v3 renderer

Emits sidecar .map (v3 schema, VLQ-encoded mappings, one segment per
generated line) and appends //# sourceMappingURL footer to the body."
```

---

## Task 12: Wire `Options.maps` into `Request.Maps`

**Files:**
- Modify: `codegen/codegen.go`
- Modify: `cmd/sngl/compile.go`
- Modify: `cmd/sngl/build.go`
- Modify: `cmd/sngl/run.go`

- [ ] **Step 1: Add Maps to Request**

In `codegen/codegen.go`, locate `type Request struct` (around line 454). Add:

```go
	// Maps enables source-map generation. The platform forwards this to
	// OpenCodeFile's WriterOptions. Sourced from Options.maps in the
	// .sngl output() block.
	Maps bool
```

- [ ] **Step 2: Add helper to read maps from Options struct**

Append to `codegen/codegen.go` (or a new `codegen/options_maps.go` if you prefer):

```go
// ReadMapsOption returns the value of the top-level "maps" field from a merged
// options struct. Returns false when the field is absent or not a bool.
func ReadMapsOption(opts *ir.StructLit) bool {
	if opts == nil {
		return false
	}
	for _, f := range opts.Fields {
		if f.Name == "maps" {
			if lit, ok := f.Value.(*ir.Literal); ok && lit.Kind == ir.LitBool {
				return lit.Bool
			}
		}
	}
	return false
}
```

(Field/method names: verify against `ir/literal.go` — adjust `LitBool` and `.Bool` to match the IR's literal shape. Use `grep -n "LitBool\|LitKind" ir/*.go` to confirm. If `ir.StructLit` uses different field iteration, follow its existing pattern.)

- [ ] **Step 3: Wire into CLI pipelines**

In `cmd/sngl/compile.go`, locate where `Request` is constructed (search `&codegen.Request{`). Add `Maps: codegen.ReadMapsOption(mergedOpts)` to the struct literal. The variable holding merged options is the `ir.StructLit` passed as `Options:`.

Repeat in `cmd/sngl/build.go` and `cmd/sngl/run.go`.

- [ ] **Step 4: Add a CLI script test**

Create `cmd/sngl/testdata/compile_maps_option.txt`:

```
# Maps option compiles cleanly when set.
sngl compile main.sngl
exists app/main.js

-- main.sngl --
output {
    lang: "js"
    platform: "html"
    maps: true
}

window Home {
    text "hello"
}
```

(Adapt to actual project conventions — check an existing `compile_*.txt` for the right paths/expectations. The point is just to assert `maps: true` parses + compiles without error. Source-map sidecar files won't appear yet — no platform calls `OpenCodeFile` yet.)

- [ ] **Step 5: Build + run tests**

Run: `go build ./...`
Run: `go test ./cmd/sngl -run TestScript -v -count=1`
Expected: build clean, script test passes.

- [ ] **Step 6: Commit**

```bash
git add codegen/codegen.go cmd/sngl/compile.go cmd/sngl/build.go cmd/sngl/run.go cmd/sngl/testdata/compile_maps_option.txt
git commit -m "codegen+cli: thread Options.maps into Request.Maps

ReadMapsOption pulls the bool out of the merged options struct.
CLI compile/build/run all forward it. No platform reads it yet —
Plan B wires Generate(req, sink) and uses Request.Maps."
```

---

## Task 13: Integration test — writer + Go translator end-to-end

**Files:**
- Create: `codegen/integration_test.go`

- [ ] **Step 1: Write the test**

```go
// codegen/integration_test.go
package codegen_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
)

func TestWriterEndToEnd_Go(t *testing.T) {
	sink := codegen.NewMemSink()
	tr := &golang.Translator{}
	w := codegen.OpenCodeFile(sink, "model.go", tr, codegen.WriterOptions{Maps: true})

	w.Import(codegen.ImportSpec{Path: "fmt", Kind: codegen.ImportNative})
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 3, Column: 1}, []byte("package main\n\nfunc main() {\n"))
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 8, Column: 1}, []byte("\tfmt.Println(\"hi\")\n"))
	w.Write([]byte("}\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := string(sink.Files()["model.go"])
	if !strings.Contains(got, "//line foo.sngl:3") {
		t.Errorf("missing first //line:\n%s", got)
	}
	if !strings.Contains(got, "//line foo.sngl:8") {
		t.Errorf("missing second //line:\n%s", got)
	}
	// Header (generated-by comment) renders for Go.
	if !strings.Contains(got, "DO NOT EDIT") {
		t.Errorf("missing generated header:\n%s", got)
	}
}

func TestWriterEndToEnd_JS(t *testing.T) {
	// Same idea, JS sidecar.
	sink := codegen.NewMemSink()
	// Resolve the JS translator from the registry to avoid an import cycle
	// if javascript pulls codegen.
	tr := codegen.LookupLang("js")
	if tr == nil {
		t.Skip("js translator not registered")
	}
	w := codegen.OpenCodeFile(sink, "out.js", tr, codegen.WriterOptions{Maps: true})
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 1, Column: 1}, []byte("console.log(1);\n"))
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 2, Column: 1}, []byte("console.log(2);\n"))
	w.Close()

	files := sink.Files()
	if _, ok := files["out.js.map"]; !ok {
		t.Fatalf("sidecar missing: %v", files)
	}
	if !strings.Contains(string(files["out.js"]), "sourceMappingURL=out.js.map") {
		t.Errorf("inline footer missing:\n%s", files["out.js"])
	}
}
```

(The `_test` package + `LookupLang` import avoids the platform/lang blank-import dependency. Adjust the registration path: real registration happens via `codegen/lang/languages.go` which is blank-imported in `cmd/sngl/main.go`. The test file may need a blank import: `_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"`.)

- [ ] **Step 2: Run**

Run: `go test ./codegen -run TestWriterEndToEnd -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add codegen/integration_test.go
git commit -m "codegen: integration test for writer + Go/JS sourcemaps"
```

---

## Task 14: Update spec to reflect implemented state

**Files:**
- Modify: `docs/superpowers/specs/2026-05-22-unified-codegen-design.md`

- [ ] **Step 1: Mark Plan A items implemented**

At the top of the spec (under Status), change `**Status:** Draft` to `**Status:** Plan A implemented; Plan B (migrations + cutover) pending.`

Append to "Migration plan" section a checkbox list of which steps are done:

```markdown

### Plan A status (2026-05-22)

- [x] Sink + MemSink + DirSink
- [x] ImportSpec + ImportKind
- [x] CodeWriter + position tracking
- [x] LangTranslator.RenderHeader / RenderSourceMap on every lang
- [x] Go //line renderer
- [x] JS v3 source-map renderer
- [x] Options.maps wired into Request.Maps
- [ ] Generate(req, sink) signature change — Plan B
- [ ] Per-platform migration — Plan B
- [ ] OutputFile/Response deletion — Plan B
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-22-unified-codegen-design.md
git commit -m "spec: mark Plan A foundations complete"
```

---

## Self-Review

**Spec coverage:**

- §Architecture/Sink → Tasks 1, 2.
- §CodeWriter → Tasks 6, 7, 8, 13.
- §ImportSpec → Tasks 3.
- §Source maps per lang → Tasks 9 (Go), 10–11 (JS). Kotlin stub in Task 5.
- §Gating: maps option → Tasks 5 (writer flag), 12 (CLI wiring).
- §LangTranslator changes (additive part) → Task 5. Deletion of dead stubs ⇒ Plan B.
- §PlatformGenerator changes → **deferred to Plan B** (signature change requires all platforms migrated first).
- §Import collection migration → **deferred to Plan B** (per-platform PR).
- §Position-marker call sites → **deferred to Plan B** (translator-internal change rides with platform migration).

Two spec sections explicitly defer; called out at the top of this plan.

**Placeholder scan:**
- "Verify against `ir/literal.go`" in Task 12 Step 2 — acceptable because the actual literal-field shape is small but worth confirming. Engineer runs the listed grep. Not a TODO; it's an instruction.
- Adapt-to-conventions in Task 12 Step 4 — script-test path style varies by codebase; engineer follows existing fixtures. Acceptable specificity.

**Type consistency:**
- `headerRenderer` interface in writer.go matches the two methods added to `LangTranslator` in Task 5. ✓
- `WriterOptions{Maps bool}` consistent across Tasks 6, 8, 12, 13. ✓
- `ImportSpec` used as map key throughout — confirmed comparable (Task 3). ✓
- `PosEntry{ByteOffset, Pos}` used in Tasks 4, 6, 8, 9, 11, 13. ✓
- `SourceMapResult{InlineBody, Sidecar, SidecarName}` consistent. ✓

Clean.
