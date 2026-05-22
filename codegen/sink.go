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

// NewMemSink returns an empty MemSink.
func NewMemSink() *MemSink {
	return &MemSink{files: map[string][]byte{}}
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
