package checker

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/imports"
	"duckfam.us/sngl/ir"
)

// An import scheme is served three ways: by an importer compiled into the
// compiler (go:, file:, git:, …), by a plugin the library ships at
// `sngl:x/scheme/<name>`, and by a plugin a package the import reaches
// declares with gen.scheme. The first two are built in -- a plugin may not
// take one's name, since a scheme is a dispatch key every import of the build
// shares rather than a name in scope.
//
// A package's imports resolve in two phases. Every import whose scheme is
// built in resolves first, and each package that resolves brings the schemes
// it declares and the ones its own imports reach. Then the imports left are
// resolved against those, repeatedly, until a round resolves nothing -- a
// package one plugin generates may itself declare a scheme. So which file
// imports a plugin, and where in it, does not decide what the package can
// resolve, as an alias's position does not decide where it can be used.

// SchemeTier is where the library's own plugins live: `sngl:x/scheme/<name>`
// serves `<name>:`.
const SchemeTier = "x/scheme/"

// SchemeKnower is implemented by a resolver that can say whether an importer
// compiled into the compiler serves a scheme.
type SchemeKnower interface {
	HasScheme(name string) bool
}

// SchemeRunner is implemented by a resolver that can run a plugin's handler:
// what it writes for uri, parsed, is the package the import resolves to.
type SchemeRunner interface {
	GenerateScheme(s *ir.Scheme, uri string) ([]*ast.Document, error)
}

// The runner a library plugin's handler runs through when the check has no
// resolver that runs one. internal/plugin registers it: this package cannot
// import the interpreter that runs a handler, which imports this package.
var (
	libRunnerMu sync.RWMutex
	libRunner   SchemeRunner
)

// RegisterLibraryRunner makes r what runs a library plugin's handler for a
// check whose resolver runs none. A library plugin is trusted, so nothing a
// project grants is asked.
func RegisterLibraryRunner(r SchemeRunner) {
	libRunnerMu.Lock()
	defer libRunnerMu.Unlock()
	libRunner = r
}

func libraryRunner() (SchemeRunner, bool) {
	libRunnerMu.RLock()
	defer libRunnerMu.RUnlock()
	return libRunner, libRunner != nil
}

// deferredImport is an import whose scheme nothing built in serves, waiting
// for the schemes the package's other imports bring.
type deferredImport struct {
	imp *ast.Import
	doc *ast.Document
}

// schemeBuiltIn reports whether a compiled-in importer serves name. With no
// resolver nothing the project brings resolves, and every scheme counts as
// known so the import is left as it always was -- but a scheme the library
// ships still resolves, through the library runner, since library source may
// import one (sngl:platform/gtk4 imports gir:) and is checked with no build
// around it: `sngl doc`, the LSP's library path, a target's capabilities.
func (c *checker) schemeBuiltIn(name string) bool {
	if name == "" || name == "sngl" {
		return true
	}
	// No compiled-in importer may take a name the library ships a plugin for,
	// so the library's answer stands whatever resolver the check has -- the
	// fixture harness's stub answers no scheme at all.
	if libSchemeShipped(name) {
		return false
	}
	if c.cfg.Resolver == nil {
		return true
	}
	if k, ok := c.cfg.Resolver.(SchemeKnower); ok {
		return k.HasScheme(name)
	}
	return true
}

// libSchemeShipped reports whether the library ships a plugin for name.
func libSchemeShipped(name string) bool {
	return HasPackage(SchemeTier + name)
}

// libraryScheme is the library's plugin for name, checked once per build. Nil
// when the library ships none.
func (c *checker) libraryScheme(name string) *ir.Scheme {
	if !libSchemeShipped(name) {
		return nil
	}
	libs := c.libs
	if s, ok := libs.schemes[name]; ok {
		return s
	}
	libs.schemes[name] = nil // a plugin reaching its own scheme finds none
	uri := "sngl:" + SchemeTier + name
	docs := PackageDocsFor(SchemeTier + name)
	pkg, diags := CheckPackage(docs, &Config{
		Dir:           c.cfg.Dir,
		Resolver:      c.cfg.Resolver,
		Languages:     c.cfg.Languages,
		Platforms:     c.cfg.Platforms,
		Targets:       c.targets,
		LibSources:    c.cfg.LibSources,
		libs:          libs,
		generating:    c.generating,
		libraryScheme: name,
		noTargets:     true,
	})
	for _, d := range diags {
		// The library's own source failing to check is the compiler's bug;
		// say so where the import is rather than nowhere.
		if d.Severity == ir.Error {
			c.diags = append(c.diags, d)
		}
	}
	pkg.Origin = &ir.PackageOrigin{URI: uri, Docs: docs}
	var found *ir.Scheme
	for _, s := range pkg.Schemes {
		s.Library = true
		if s.Name == name {
			found = s
		}
	}
	libs.schemes[name] = found
	return found
}

// reachedSchemes is every scheme a package reaches through its imports: the
// ones each imported package declares, and the ones its own imports reach.
func reachedSchemes(imps []*ir.Import, seen map[*ir.Package]bool, out *[]*ir.Scheme) {
	for _, imp := range imps {
		if imp.Pkg == nil || seen[imp.Pkg] {
			continue
		}
		seen[imp.Pkg] = true
		*out = append(*out, imp.Pkg.Schemes...)
		reachedSchemes(imp.Pkg.Imports, seen, out)
	}
}

// schemesInScope is what this package's imports have brought so far, by name.
// Two plugins declaring one name is reported at the import that brought the
// second, once.
func (c *checker) schemesInScope() map[string]*ir.Scheme {
	byName := map[string]*ir.Scheme{}
	seen := map[*ir.Package]bool{}
	for _, imp := range c.declPkg().Imports {
		var reached []*ir.Scheme
		reachedSchemes([]*ir.Import{imp}, seen, &reached)
		for _, s := range reached {
			prev, ok := byName[s.Name]
			if !ok {
				byName[s.Name] = s
				continue
			}
			if prev == s || c.schemeClashes[s] {
				continue
			}
			c.schemeClashes[s] = true
			if imp.AST != nil {
				c.error(imp.AST.Pos, "import %q brings a second scheme %q: it is declared at %s and at %s", imp.Path, s.Name, prev.Pos, s.Pos)
			}
		}
	}
	return byName
}

// pluginScheme is the plugin that serves name for this package, or nil.
func (c *checker) pluginScheme(name string) *ir.Scheme {
	if c.schemeBuiltIn(name) {
		return nil
	}
	if s := c.libraryScheme(name); s != nil {
		return s
	}
	return c.schemesInScope()[name]
}

// deferImport reports whether imp waits for the second phase: its scheme is
// served by neither the compiler nor the library.
func (c *checker) deferImport(imp *ast.Import, scheme string) bool {
	if c.resolvingDeferred || c.schemeBuiltIn(scheme) || libSchemeShipped(scheme) {
		return false
	}
	c.deferred = append(c.deferred, deferredImport{imp: imp, doc: c.doc})
	return true
}

// resolveDeferredImports is the second phase: each import waiting on a scheme
// resolves once an import of this package brings it, repeatedly, since what
// resolves may bring more. What is left is reported once the package's own
// schemes are known (reportUnresolvedSchemes).
func (c *checker) resolveDeferredImports() {
	for progress := true; progress && len(c.deferred) > 0; {
		progress = false
		inScope := c.schemesInScope()
		var left []deferredImport
		for _, d := range c.deferred {
			scheme, _ := ParseScheme(c.importTarget(d.imp))
			if inScope[scheme] == nil {
				left = append(left, d)
				continue
			}
			c.resumeFile(d.doc)
			c.resolvingDeferred = true
			c.registerImport(d.imp)
			c.resolvingDeferred = false
			progress = true
		}
		c.deferred = left
	}
	// Still bound, so a use of the alias is one more diagnostic about the
	// import rather than an undefined name.
	c.unresolved = c.deferred
	c.deferred = nil
	for _, d := range c.unresolved {
		c.resumeFile(d.doc)
		c.resolvingDeferred = true
		c.unresolvedImport = true
		c.registerImport(d.imp)
		c.unresolvedImport = false
		c.resolvingDeferred = false
	}
}

// importTarget is the URL an import resolves: its own replace, the package's
// replace map, or its path.
func (c *checker) importTarget(imp *ast.Import) string {
	if imp.Replace != "" {
		return imp.Replace
	}
	if mapped, ok := c.replaces[imp.Path]; ok {
		return mapped
	}
	return imp.Path
}

// reportUnresolvedSchemes reports each import no scheme resolved: one this
// package declares itself, which a plugin may not use, or one nothing serves.
func (c *checker) reportUnresolvedSchemes() {
	own := map[string]*ir.Scheme{}
	for _, s := range c.pkg.Schemes {
		own[s.Name] = s
	}
	for _, d := range c.unresolved {
		scheme, _ := ParseScheme(c.importTarget(d.imp))
		if c.importCycle {
			continue
		}
		if s := own[scheme]; s != nil {
			c.error(d.imp.Pos, "import %q uses the scheme %q this package declares at %s: a plugin may not use a scheme it declares", d.imp.Path, scheme, s.Pos)
			continue
		}
		known := slices.Sorted(func(yield func(string) bool) {
			for name := range c.schemesInScope() {
				if !yield(name) {
					return
				}
			}
		})
		msg := fmt.Sprintf("import %q: unknown import scheme %q", d.imp.Path, scheme)
		if len(known) > 0 {
			msg += fmt.Sprintf(" (this package's imports declare %s)", strings.Join(known, ", "))
		} else {
			msg += ": no built-in importer serves it, and no package this one imports declares it with gen.scheme"
		}
		c.error(d.imp.Pos, "%s", msg)
	}
}

// checkGenSchemes checks each gen.scheme pass1 found at the root of a file and
// takes it out of the package: it is read, not rendered.
func (c *checker) checkGenSchemes() {
	seen := map[string]*ir.Scheme{}
	for _, vn := range c.genSchemes {
		c.enterFileOf(vn.Pos)
		c.pushScope()
		st := c.checkVisualNodeIR(vn)
		c.popScope()
		ni, ok := st.(*ir.NodeInst)
		if !ok {
			continue
		}
		s := &ir.Scheme{Pos: vn.Pos, Pkg: c.pkg}
		for _, p := range ni.Props {
			if p.Name != "name" {
				continue
			}
			lit, ok := p.Value.(*ir.Literal)
			if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeString {
				c.error(vn.Pos, "a scheme's name is written as a string literal")
				continue
			}
			s.Name = lit.Value
		}
		for i := range ni.Handlers {
			if ni.Handlers[i].Name == "generate" {
				s.Handler = &ni.Handlers[i]
			}
		}
		switch {
		case s.Name == "":
			if !c.hasErrorAt(vn.Pos) {
				c.error(vn.Pos, "a scheme needs a name")
			}
			continue
		case !imports.IsSchemeName(s.Name):
			c.error(vn.Pos, "%q is not a scheme name: a letter, then letters, digits, '+', '-' or '.'", s.Name)
			continue
		case s.Handler == nil:
			c.error(vn.Pos, "scheme %q writes nothing: it needs an @generate handler", s.Name)
			continue
		case s.Name == "sngl" || c.cfg.Resolver != nil && c.schemeBuiltIn(s.Name):
			c.error(vn.Pos, "scheme %q is built into the compiler, and a plugin may not take its name", s.Name)
			continue
		case libSchemeShipped(s.Name) && c.cfg.libraryScheme != s.Name:
			c.error(vn.Pos, "scheme %q is shipped by the library (sngl:%s%s), and a plugin may not take its name", s.Name, SchemeTier, s.Name)
			continue
		}
		if prev := seen[s.Name]; prev != nil {
			c.error(vn.Pos, "scheme %q is declared twice in this package: first at %s", s.Name, prev.Pos)
			continue
		}
		seen[s.Name] = s
		c.pkg.Schemes = append(c.pkg.Schemes, s)
	}
}

// hasErrorAt reports whether an error is already reported at pos.
func (c *checker) hasErrorAt(pos ast.Pos) bool {
	for _, d := range c.diags {
		if d.Severity == ir.Error && d.Pos == pos {
			return true
		}
	}
	return false
}

// generateScheme runs s for uri and parses what it writes. A plugin whose
// output imports its own scheme again, however indirectly, is a cycle.
func (c *checker) generateScheme(imp *ast.Import, s *ir.Scheme, uri string) []*ast.Document {
	runner, ok := c.cfg.Resolver.(SchemeRunner)
	if !ok && s.Library {
		runner, ok = libraryRunner()
	}
	if !ok {
		c.error(imp.Pos, "import %q: nothing here runs a plugin's handler", imp.Path)
		return nil
	}
	key := s.Name + ":" + uri
	if c.generating[key] {
		c.error(imp.Pos, "import cycle detected: %q is imported by the package its own scheme generates", imp.Path)
		return nil
	}
	c.generating[key] = true
	defer delete(c.generating, key)
	docs, err := runner.GenerateScheme(s, uri)
	if err != nil {
		c.error(imp.Pos, "import %q: %v", imp.Path, err)
		return nil
	}
	if len(docs) == 0 {
		c.error(imp.Pos, "import %q: scheme %q wrote no file", imp.Path, s.Name)
	}
	return docs
}

// checkCLinks checks each c.link pass1 found at the root of a file into the
// package's CLinks: what a cgo preamble includes and links for it.
func (c *checker) checkCLinks() {
	for _, vn := range c.cLinks {
		c.enterFileOf(vn.Pos)
		c.pushScope()
		st := c.checkVisualNodeIR(vn)
		c.popScope()
		ni, ok := st.(*ir.NodeInst)
		if !ok {
			continue
		}
		link := &ir.CLink{}
		bad := false
		strs := func(e ir.Expr) []string {
			lit, ok := e.(*ir.ListLit)
			if !ok {
				bad = true
				return nil
			}
			var out []string
			for _, el := range lit.Elems {
				l, ok := el.(*ir.Literal)
				if !ok {
					bad = true
					continue
				}
				out = append(out, l.Value)
			}
			return out
		}
		for _, p := range ni.Props {
			switch p.Name {
			case "include":
				if l, ok := p.Value.(*ir.Literal); ok {
					link.Include = l.Value
				} else {
					bad = true
				}
			case "system":
				// true and false are the builtin consts of those names.
				switch v := p.Value.(type) {
				case *ir.Literal:
					link.System = v.Value == "true"
				case *ir.Ident:
					if sym, ok := v.Sym.(*ir.Var); ok && sym.IsConst && (v.Name == "true" || v.Name == "false") {
						link.System = v.Name == "true"
					} else {
						bad = true
					}
				default:
					bad = true
				}
			case "cflags":
				link.CFlags = strs(p.Value)
			case "ldflags":
				link.LDFlags = strs(p.Value)
			}
		}
		if bad {
			c.error(vn.Pos, "a c.link is written with literals: a cgo preamble is fixed before anything runs")
			continue
		}
		if link.Include == "" {
			c.error(vn.Pos, "a c.link names the header it includes")
			continue
		}
		c.pkg.CLinks = append(c.pkg.CLinks, link)
	}
}
