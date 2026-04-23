package codegen

import (
	"io/fs"
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// SchemeImporter resolves a scheme-based import URI (e.g., "go://pkg/path")
// into fully-typed SNGL IR declarations. Language plugins register importers
// for their native source formats.
type SchemeImporter interface {
	Scheme() string // "go", "ts", "proto", etc.
	Resolve(uri, dir string) (*ir.NativeImport, error)
}

// FSSchemeImporter resolves a scheme-based import to a filesystem of SNGL sources.
// Used for remote SNGL libraries (git://, http://) that provide .sngl files
// rather than native language declarations.
type FSSchemeImporter interface {
	Scheme() string
	ResolveFS(uri, dir string) (fs.FS, error)
}

var (
	schemeMu  sync.RWMutex
	schemes   = map[string]SchemeImporter{}
	fsSchemes = map[string]FSSchemeImporter{}
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

// RegisterFSScheme registers a filesystem scheme importer. Panics on duplicate.
func RegisterFSScheme(s FSSchemeImporter) {
	schemeMu.Lock()
	defer schemeMu.Unlock()
	name := s.Scheme()
	if _, ok := fsSchemes[name]; ok {
		panic("codegen: duplicate FS scheme registration: " + name)
	}
	fsSchemes[name] = s
}

// LookupScheme returns the importer for the given scheme, or nil.
func LookupScheme(scheme string) SchemeImporter {
	schemeMu.RLock()
	defer schemeMu.RUnlock()
	return schemes[scheme]
}

// LookupFSScheme returns the FS importer for the given scheme, or nil.
func LookupFSScheme(scheme string) FSSchemeImporter {
	schemeMu.RLock()
	defer schemeMu.RUnlock()
	return fsSchemes[scheme]
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
