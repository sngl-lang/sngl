package codegen

import (
	"io/fs"
	"sync"

)

// NativeDecls holds native language declarations resolved from a scheme import.
// Language-specific importers (go://, ts://) populate this so that the
// checker can type-check references to external symbols.
type NativeDecls struct {
	ImportPath string
	Funcs      []NativeFunc
	Vars       []NativeVar
	Structs    []NativeStruct
	Enums      []NativeEnum
}

// NativeFunc describes a function from a native import.
type NativeFunc struct {
	Name       string
	ParamTypes []string
	ReturnType string
	NativePkg  string // original native package (e.g., "file")
	NativeType string // original native name
	Pure       bool
}

// NativeVar describes a variable or constant from a native import.
type NativeVar struct {
	Name       string
	Type       string
	NativePkg  string
	NativeType string
	IsFunc     bool // extern function (callable)
	Pure       bool
}

// NativeStruct describes a struct type from a native import.
type NativeStruct struct {
	Name   string
	Fields []NativeField
}

// NativeField describes a field in a native struct.
type NativeField struct {
	Name string
	Type string
}

// NativeEnum describes an enum type from a native import.
type NativeEnum struct {
	Name   string
	Values []string
}

// SchemeImporter resolves a scheme-based import URI (e.g., "go://pkg/path")
// into SNGL-compatible declarations. Language plugins register importers for
// their native source formats.
type SchemeImporter interface {
	Scheme() string // "go", "ts", "proto", etc.
	Resolve(uri, dir string) (*NativeDecls, error)
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
