// Package snglsettings is the runtime behind sngl:settings for a program
// compiled to Go: the place a program's entry point opens, and what the
// library reads and writes through it.
//
// Nothing is open until a program's main opens it, which `go test` never
// runs, so a test keeps its values in the library's memory.
package snglsettings

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

// Place is where values are kept, each a JSON text under a name in a scope,
// "" being the app's own.
type Place interface {
	Read(scope, name string) string
	Write(scope, name, text string)
}

var (
	mu    sync.Mutex
	place Place
)

// Open makes p where every setting is kept.
func Open(p Place) {
	mu.Lock()
	defer mu.Unlock()
	place = p
}

// Persistent reports whether a place is open.
func Persistent() bool {
	mu.Lock()
	defer mu.Unlock()
	return place != nil
}

// Read is the text stored under name in scope, or "" where none is.
func Read(scope, name string) string {
	mu.Lock()
	p := place
	mu.Unlock()
	if p == nil {
		return ""
	}
	return p.Read(scope, name)
}

// Write stores text under name in scope.
func Write(scope, name, text string) {
	mu.Lock()
	p := place
	mu.Unlock()
	if p != nil {
		p.Write(scope, name, text)
	}
}

// File keeps values in a JSON object per scope, in a directory named app
// under the user's configuration directory: settings.json for the app's own,
// <scope>.json for another. A file that cannot be read is no values, and a
// write that fails is lost: a setting is never worth stopping the program.
func File(app string) Place {
	return &file{app: app}
}

type file struct {
	mu  sync.Mutex
	app string
}

func (f *file) path(scope string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	name := "settings"
	if scope != "" {
		name = url.PathEscape(scope)
	}
	return filepath.Join(dir, f.app, name+".json")
}

func (f *file) load(path string) map[string]json.RawMessage {
	values := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &values)
	}
	return values
}

func (f *file) Read(scope, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := f.path(scope)
	if path == "" {
		return ""
	}
	return string(f.load(path)[name])
}

func (f *file) Write(scope, name, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := f.path(scope)
	if path == "" || !json.Valid([]byte(text)) {
		return
	}
	values := f.load(path)
	values[name] = json.RawMessage(text)
	b, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}
