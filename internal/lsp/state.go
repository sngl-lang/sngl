package lsp

import (
	"sync"

	"duckfam.us/sngl/ast"
)

// fileState holds the current state for an open file.
type fileState struct {
	URI     string
	Content string
	Version int
	Doc     *ast.Document // last successful parse
}

// workspace tracks all open files.
type workspace struct {
	mu    sync.Mutex
	files map[string]*fileState
}

func newWorkspace() *workspace {
	return &workspace{
		files: make(map[string]*fileState),
	}
}

func (w *workspace) open(uri string, content string, version int) *fileState {
	w.mu.Lock()
	defer w.mu.Unlock()
	fs := &fileState{URI: uri, Content: content, Version: version}
	w.files[uri] = fs
	return fs
}

func (w *workspace) update(uri string, content string, version int) *fileState {
	w.mu.Lock()
	defer w.mu.Unlock()
	fs, ok := w.files[uri]
	if !ok {
		fs = &fileState{URI: uri}
		w.files[uri] = fs
	}
	fs.Content = content
	fs.Version = version
	return fs
}

func (w *workspace) get(uri string) *fileState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.files[uri]
}

func (w *workspace) close(uri string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.files, uri)
}
