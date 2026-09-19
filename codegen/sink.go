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

// BoilerplateSink is implemented by a Sink that wants to know which of its
// files the program had no part in. A platform that renders a project
// scaffold says so; one that emits only the program need not implement or
// call it, which is why it is an optional interface rather than a second
// method on Sink.
//
// The distinction is the golden harness's: a fixture commits the files worth
// reading and lets a digest stand for the rest, and without this the rest
// would be a per-platform list of names in the harness -- a list that has to
// agree with what the platform actually writes, and silently stops agreeing.
type BoilerplateSink interface {
	Sink
	// MarkBoilerplate records that name is scaffold rather than generated
	// from the program. Called before or after Create, in any order.
	MarkBoilerplate(name string)
}

// MarkBoilerplate tells sink that name is scaffold, when the sink cares.
func MarkBoilerplate(sink Sink, name string) {
	if bs, ok := sink.(BoilerplateSink); ok {
		bs.MarkBoilerplate(name)
	}
}

// MemSink accumulates files in memory. Safe for concurrent Create calls;
// the returned writers are not.
type MemSink struct {
	mu          sync.Mutex
	files       map[string][]byte
	boilerplate map[string]bool
}

// NewMemSink returns an empty MemSink.
func NewMemSink() *MemSink {
	return &MemSink{files: map[string][]byte{}, boilerplate: map[string]bool{}}
}

// MarkBoilerplate implements BoilerplateSink.
func (m *MemSink) MarkBoilerplate(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.boilerplate[name] = true
}

// Boilerplate returns the names marked as scaffold.
func (m *MemSink) Boilerplate() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.boilerplate))
	for k := range m.boilerplate {
		out[k] = true
	}
	return out
}

// Create returns a writer for the given relative path. Closing the writer
// commits the buffered content to the sink, overwriting any previous value.
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
	sink   *MemSink
	name   string
	buf    bytes.Buffer
	closed bool
}

func (w *memWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.buf.Write(p)
}

func (w *memWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.sink.mu.Lock()
	defer w.sink.mu.Unlock()
	w.sink.files[w.name] = w.buf.Bytes()
	return nil
}
