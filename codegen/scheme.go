package codegen

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
)

// SchemeImporter resolves a scheme-based import URI (e.g., "go://pkg/path")
// into SNGL-compatible declarations. Language plugins register importers for
// their native source formats.
type SchemeImporter interface {
	Scheme() string // "go", "ts", "proto", etc.
	Resolve(uri, dir string) (*ast.NativeDecls, error)
}

var (
	schemeMu sync.RWMutex
	schemes  = map[string]SchemeImporter{}
)

// RegisterScheme registers a scheme importer. Panics on duplicate.
func RegisterScheme(s SchemeImporter) {
	schemeMu.Lock()
	defer schemeMu.Unlock()
	name := s.Scheme()
	if _, ok := schemes[name]; ok {
		panic("codegen: duplicate scheme registration: " + name)
	}
	schemes[name] = s
}

// LookupScheme returns the importer for the given scheme, or nil.
func LookupScheme(scheme string) SchemeImporter {
	schemeMu.RLock()
	defer schemeMu.RUnlock()
	return schemes[scheme]
}

// Schemes returns the names of all registered schemes.
func Schemes() []string {
	schemeMu.RLock()
	defer schemeMu.RUnlock()
	names := make([]string, 0, len(schemes))
	for name := range schemes {
		names = append(names, name)
	}
	return names
}
