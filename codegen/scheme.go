package codegen

import (
	"fmt"
	"io/fs"
	"sync"

	"duckfam.us/sngl/ir"
)

// SchemeImporter resolves a scheme-based import URI (e.g., "go:pkg/path")
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

// SchemeSession is implemented by a SchemeImporter that can hand out a copy
// scoped to one compilation. A registered importer is a process-wide singleton,
// so anything it remembers outlives every compilation in the process — wrong
// for a server that recompiles (sngl preview, sngl doc --browse), which must
// see a Go file edited between two compilations. A caller that has a
// compilation to scope to resolves through NewSession() and drops the result
// with the compilation; a caller that does not gets the singleton, which
// remembers nothing.
type SchemeSession interface {
	SchemeImporter
	NewSession() SchemeImporter
}

// FSSchemeImporter resolves a scheme-based import to a filesystem of SNGL sources.
// Used for remote SNGL libraries (git://, http://) that provide .sngl files
// rather than native language declarations.
type FSSchemeImporter interface {
	Scheme() string
	ResolveFS(uri, dir string) (fs.FS, error)
}

// ProjectFSScheme is implemented by an FSSchemeImporter whose source is a file
// of the project being built rather than something it fetches. `md:` is the
// one: the markdown it reads is checked in beside the program, so it has to be
// read through the same filesystem the program was -- a golden test's archive,
// the playground's in-memory files -- and not off disk.
//
// The plain ResolveFS stays the fallback for a caller that has no FS.
type ProjectFSScheme interface {
	FSSchemeImporter
	ResolveProjectFS(uri string, fsys fs.FS, dir string) (fs.FS, error)
}

// NetGate asks whether a fetch may contact host. A nil gate refuses every
// host.
type NetGate func(host string) error

// NetworkScheme is an FSSchemeImporter that fetches over the network what its
// cache does not already hold. A build resolves one through ResolveFSNet,
// which asks gate before contacting any host -- a redirect's included -- and
// asks nothing for a package already in the cache, since that touches no
// network. Its plain ResolveFS is ResolveFSNet with a nil gate.
type NetworkScheme interface {
	FSSchemeImporter
	ResolveFSNet(uri, dir string, gate NetGate) (fs.FS, error)
}

// AskNet is gate's answer for host, where nil refuses.
func AskNet(gate NetGate, host string) error {
	if gate == nil {
		return fmt.Errorf("fetching from %s is not allowed here", host)
	}
	return gate(host)
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
