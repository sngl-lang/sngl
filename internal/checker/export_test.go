package checker

import (
	"maps"
	"slices"

	"duckfam.us/sngl/ir"
)

// SharedLibrary is one library cache that several checks run against, as
// every check will once a package is checked once per process.
type SharedLibrary struct{ libs *libCache }

func NewSharedLibrary() *SharedLibrary {
	var cfg *Config
	return &SharedLibrary{libs: cfg.libCache()}
}

// Use makes the check cfg configures load its library into s, and find there
// what an earlier check loaded.
func (s *SharedLibrary) Use(cfg *Config) { cfg.libs = s.libs }

// Packages is every library package s holds, by path.
func (s *SharedLibrary) Packages() []*ir.Package {
	var pkgs []*ir.Package
	for _, path := range slices.Sorted(maps.Keys(s.libs.pkgs)) {
		pkgs = append(pkgs, s.libs.pkgs[path])
	}
	return pkgs
}
