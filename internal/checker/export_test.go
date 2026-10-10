package checker

import (
	"fmt"
	"maps"
	"slices"

	"duckfam.us/sngl/ir"
)

// SharedLibrary is one library cache that several checks run against, as
// every check will once a package is checked once per process.
type SharedLibrary struct{ libs *libCache }

func NewSharedLibrary() *SharedLibrary {
	libs := newLibCache()
	libs.shared = true
	return &SharedLibrary{libs: libs}
}

// Use makes the check cfg configures load its library into s, and find there
// what an earlier check loaded.
func (s *SharedLibrary) Use(cfg *Config) { cfg.libs = s.libs }

func (s *SharedLibrary) Packages() []*ir.Package { return s.libs.packages() }

// Tables is each entry of the cache's own tables, a value named by its
// address. A load adds entries; nothing else should change one.
func (s *SharedLibrary) Tables() map[string]string {
	t := map[string]string{}
	for k, v := range s.libs.pkgs {
		t["pkgs "+k] = fmt.Sprintf("%p", v)
	}
	for k, v := range s.libs.schemes {
		t["schemes "+k] = fmt.Sprintf("%p", v)
	}
	for k, v := range s.libs.loading {
		t["loading "+k] = fmt.Sprint(v)
	}
	for k, v := range s.libs.extended {
		t["extended "+k] = fmt.Sprint(v)
	}
	for k, comps := range s.libs.rendersByName {
		for comp, v := range comps {
			t[fmt.Sprintf("rendersByName %s %p", k, comp)] = fmt.Sprint(v)
		}
	}
	for k, v := range s.libs.roles {
		t["roles "+string(k)] = fmt.Sprintf("%p", v)
	}
	for k, v := range s.libs.macroSigs {
		t[fmt.Sprintf("macroSigs %p", k)] = fmt.Sprint(v)
	}
	return t
}

// Sourceless is every package s holds by a path no source serves.
func (s *SharedLibrary) Sourceless() []string {
	var out []string
	for _, path := range slices.Sorted(maps.Keys(s.libs.pkgs)) {
		if len(PackageDocsFor(path)) == 0 {
			out = append(out, path)
		}
	}
	return out
}
