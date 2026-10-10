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
	return &SharedLibrary{libs: newLibCache()}
}

// Use makes the check cfg configures load its library into s, and find there
// what an earlier check loaded.
func (s *SharedLibrary) Use(cfg *Config) { cfg.libs = s.libs }

// Packages is every library package s holds: those it holds by path, each
// library plugin's, what they import, and the package each declaration they
// hold says declared it. The last two are how the gir: widgets
// sngl:platform/gtk4 imports are reached, which name a package of their own.
func (s *SharedLibrary) Packages() []*ir.Package {
	var pkgs []*ir.Package
	seen := map[*ir.Package]bool{}
	add := func(p *ir.Package) {
		if p != nil && !seen[p] {
			seen[p] = true
			pkgs = append(pkgs, p)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(s.libs.pkgs)) {
		add(s.libs.pkgs[path])
	}
	for _, name := range slices.Sorted(maps.Keys(s.libs.schemes)) {
		if scheme := s.libs.schemes[name]; scheme != nil {
			add(scheme.Pkg)
		}
	}
	for i := 0; i < len(pkgs); i++ {
		p := pkgs[i]
		for _, imp := range p.Imports {
			add(imp.Pkg)
		}
		for _, d := range p.Components {
			add(d.Pkg)
		}
		for _, d := range p.Funcs {
			add(d.Pkg)
		}
		for _, d := range p.Structs {
			add(d.Pkg)
		}
		for _, d := range p.Enums {
			add(d.Pkg)
		}
		for _, d := range p.Units {
			add(d.Pkg)
		}
		for _, d := range p.Consts {
			add(d.Pkg)
		}
		for _, d := range p.Vars {
			add(d.Pkg)
		}
	}
	return pkgs
}

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
