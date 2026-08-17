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

// FSAwareScheme is implemented by SchemeImporters that can resolve against
// an arbitrary io/fs.FS instead of the OS filesystem rooted at dir. Callers
// should prefer ResolveFS via type assertion when an FS is available; the
// classic Resolve(uri, dir) path remains for schemes that haven't migrated.
//
// TODO: collapse SchemeImporter.Resolve once all native schemes adopt this.
type FSAwareScheme interface {
	SchemeImporter
	ResolveFS(uri string, fsys fs.FS, dir string) (*ir.NativeImport, error)
}

// FSSchemeImporter resolves a scheme-based import to a filesystem of SNGL sources.
// Used for remote SNGL libraries (git://, http://) that provide .sngl files
// rather than native language declarations.
type FSSchemeImporter interface {
	Scheme() string
	ResolveFS(uri, dir string) (fs.FS, error)
}

// FSSchemeUpdater is an optional capability an FSSchemeImporter may implement
// so `sngl pkg update` can drop the cache entry for a URI, re-fetch, and
// report the integrity hash suitable for paste back into the `#fragment`.
type FSSchemeUpdater interface {
	Refresh(uri, dir string) (newHash string, err error)
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
