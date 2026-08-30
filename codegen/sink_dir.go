package codegen

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DirSink writes files into Root on the local filesystem. Parent directories
// are created on demand. Compiles under WASM (the whole codegen tree is built
// to wasm so go: const-evaluation can run), but is never invoked there —
// wasm const-eval only calls pure data functions; use MemSink for any actual
// output. Do not add a //go:build !js constraint here: the host-only snapshot/
// run/launcher code across the platform packages references NewDirSink without
// js guards, so gating it breaks the wasm build and blanks the whole docs site.
type DirSink struct {
	Root string
}

// NewDirSink returns a DirSink rooted at root.
func NewDirSink(root string) *DirSink { return &DirSink{Root: root} }

// Create returns a writer for a file at the given slash-separated relative
// path under Root. Parent directories are created with mode 0o755.
func (d *DirSink) Create(name string) (io.WriteCloser, error) {
	clean := filepath.FromSlash(strings.TrimPrefix(name, "/"))
	full := filepath.Join(d.Root, clean)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	return os.Create(full)
}
