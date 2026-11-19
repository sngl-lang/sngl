package checker

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/imports"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
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
	// GenerateScheme answers the files it wrote, parsed, and the notes it
	// wrote beside them: each is a warning at the import.
	GenerateScheme(s *ir.Scheme, uri string) ([]*ast.Document, []string, error)
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
		c.replayLoad("scheme:" + name)
		return s
	}
	defer c.loadHold()()
	if s, ok := libs.schemes[name]; ok {
		c.replayLoad("scheme:" + name)
		return s
	}
	defer c.recordLoad("scheme:"+name, len(c.diags))
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
		hold:          c.hold,
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
	docs, notes, err := runner.GenerateScheme(s, uri)
	if err != nil {
		c.error(imp.Pos, "import %q: %v", imp.Path, err)
		return nil
	}
	for _, n := range notes {
		c.warn(imp.Pos, "import %q: %s", imp.Path, n)
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
			case "pkgConfig":
				link.PkgConfig = strs(p.Value)
			case "library":
				if l, ok := p.Value.(*ir.Literal); ok {
					link.Library = l.Value
				} else {
					bad = true
				}
			case "sources":
				for _, name := range strs(p.Value) {
					src, err := c.readCSource(vn.Pos, name)
					if err != nil {
						c.error(vn.Pos, "c.link source %q: %v", name, err)
						continue
					}
					link.Sources = append(link.Sources, src)
				}
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
		pkg := c.declPkg()
		pkg.CLinks = append(pkg.CLinks, link)
	}
}

// readCSource reads a C file a c.link at pos ships, named relative to the
// file the link is written in: the program's own, through the filesystem its
// imports are read from, or a library package's, from the library. It is
// kept by its own name, since a language compiles every one into one
// directory, where a header among them is included by that name.
//
// A library package may name another's as `sngl:<path>`, which it then
// ships as its own: a runtime file two targets share, which only one of
// them owns. Its Path is where it is from the naming file.
func (c *checker) readCSource(pos ast.Pos, name string) (ir.CSource, error) {
	if other, ok := strings.CutPrefix(name, "sngl:"); ok && c.inLibSource() {
		full := path.Clean(other)
		if ext := path.Ext(full); ext != ".c" && ext != ".h" {
			return ir.CSource{}, fmt.Errorf("a source is a C file or a header, .c or .h")
		}
		data, err := fs.ReadFile(lib.FS, full)
		if err != nil {
			return ir.CSource{}, err
		}
		rel, err := filepath.Rel(path.Dir(pos.File), full)
		if err != nil {
			return ir.CSource{}, err
		}
		return ir.CSource{Name: path.Base(full), Path: filepath.ToSlash(rel), Text: string(data)}, nil
	}
	clean := path.Clean(name)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return ir.CSource{}, fmt.Errorf("a source sits beside the file naming it, or below it")
	}
	if ext := path.Ext(clean); ext != ".c" && ext != ".h" {
		return ir.CSource{}, fmt.Errorf("a source is a C file or a header, .c or .h")
	}
	fsys, full := c.cfg.FS, path.Join(c.cfg.PkgPath, clean)
	if c.inLibSource() {
		fsys, full = lib.FS, path.Join(path.Dir(pos.File), clean)
	}
	if fsys == nil {
		return ir.CSource{}, fmt.Errorf("this check reads no files")
	}
	data, err := fs.ReadFile(fsys, full)
	if err != nil {
		return ir.CSource{}, err
	}
	return ir.CSource{Name: path.Base(clean), Path: clean, Text: string(data)}, nil
}

// reportUnlinkedCNativeCalls refuses a call of a program's `#[c.native]`
// function when no c.link reaches the package making it.
//
// A cgo preamble declares a C identifier only through a header, and the
// headers a Go target includes for a program are its reached c.links
// (ir.ReachedCLinks). With none, the call compiled through to the Go
// toolchain, which answered `undefined: C` or "could not determine what C.abs
// refers to" with no position in the program. A library's declaration is
// exempt: gtk4's cairo functions and what `gir:` generates name headers its
// own preamble includes. Declaring one is not refused, since a declaration
// nothing calls reaches no preamble.
func (c *checker) reportUnlinkedCNativeCalls() {
	if c.inLibSource() || c.pkg.Library || len(ir.ReachedCLinks(c.pkg)) > 0 {
		return
	}
	_ = ir.Walk(c.pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.AST == nil || call.Func == nil {
			return nil
		}
		fn := call.Func
		if fn.Foreign.Scheme != "c" || (fn.Pkg != nil && fn.Pkg.Library) {
			return nil
		}
		c.error(call.AST.Pos, "C function %q is called, but no c.link names the header that declares it: write c.link(include=…) from sngl:x/c, or import a c: package", fn.Foreign.Name)
		return nil
	})
}

// checkCDeclarations judges pkg's C declarations once every mark has run:
// casts, callbacks and C structs. A program's are judged after its check and
// a library's as it loads.
func (c *checker) checkCDeclarations(pkg *ir.Package) {
	c.checkCCasts(pkg)
	c.checkCFields(pkg)
	c.checkCCallbacks(pkg)
	c.checkCStructs(pkg)
}

// checkCCasts refuses a `#[c.native(…, cast)]` function whose return type is
// not the C type its name spells: the conversion is written to that type, so
// a declaration naming another would hand C a value of the wrong one. Read
// once every mark has run, since the return type's own mark may run after the
// cast's.
func (c *checker) checkCCasts(pkg *ir.Package) {
	for _, fn := range pkg.Funcs {
		if !fn.IsCCast() || fn.AST == nil {
			continue
		}
		// A node is what the platform holds as its handle, which a cast to
		// one makes: gtk4's `_asBack` makes a GtkWidget a Back.
		if fn.Return != nil && fn.Return.Kind == ir.TypeComponent {
			continue
		}
		if got := ir.CTypeOf(fn.Return); !got.Equal(fn.Foreign.C) {
			c.error(fn.AST.Pos, "#[c.native(%q, cast)] converts to %s, so %s returns a #[c.native(%q)] struct, not %s", fn.Foreign.Name, fn.Foreign.C, fn.Name, fn.Foreign.C.String(), fn.Return)
		}
	}
}

// checkCFields judges each `#[c.native("Struct.member", field)]` function:
// its first parameter is the handle the member is read through, a
// #[c.native] struct whose C type is a pointer to Struct.
func (c *checker) checkCFields(pkg *ir.Package) {
	for _, fn := range pkg.Funcs {
		if !fn.Foreign.Field || fn.Foreign.Path != ir.CPath || fn.AST == nil {
			continue
		}
		if len(fn.Params) == 0 {
			c.error(fn.AST.Pos, "#[c.native(%q, field)] reads a member of the struct its first parameter points to, and %s takes none", fn.Foreign.Name, fn.Name)
			continue
		}
		st, _, _ := strings.Cut(fn.Foreign.Name, ".")
		// A pointer to a const one reads as well: a member is only read.
		want := &ir.CType{Name: st, Ptr: 1}
		if t := fn.Params[0].Type; ir.CStructOf(t) != nil || ir.CTypeOf(t) == nil || ir.CTypeOf(t).Name != st || ir.CTypeOf(t).Ptr != 1 {
			c.error(fn.AST.Pos, "#[c.native(%q, field)] reads a member of a %s, so its first parameter is a #[c.native(%q)] handle, not %s", fn.Foreign.Name, st, want.String(), t)
		}
	}
}

// checkCCallbacks judges each function taking a callback or marking a
// context (sngl:x/c's #[context]). A C function calls a function pointer and
// cannot call a closure, so one taking a `func` hands a `void *` back to it,
// which the translator fills with what finds the closure: the function marks
// that parameter, takes one callback for it, and the callback's every
// parameter and result is a value C has a type for.
func (c *checker) checkCCallbacks(pkg *ir.Package) {
	for _, fn := range pkg.Funcs {
		if fn.AST == nil {
			continue
		}
		skip := len(fn.Params) - len(fn.AST.Params.Params)
		pos := func(i int) ast.Pos {
			if j := i - skip; j >= 0 && j < len(fn.AST.Params.Params) {
				return fn.AST.Params.Params[j].Pos
			}
			return fn.AST.Pos
		}
		var callbacks, contexts, releases, helds, statics []int
		for i, p := range fn.Params {
			if p.CAlias.IsCHeld() {
				helds = append(helds, i)
			}
			if p.COuts && !p.CStatic {
				c.error(pos(i), "callback %q is marked #[c.outs], which a callback C calls with no context has: mark it #[c.static] as well", p.Name)
			}
			switch {
			case p.CStatic:
				statics = append(statics, i)
			case p.CContext:
				contexts = append(contexts, i)
			case p.CRelease:
				releases = append(releases, i)
			case p.Type != nil && p.Type.Kind == ir.TypeFunc:
				callbacks = append(callbacks, i)
			}
		}
		c.checkCFrees(fn, pos)
		if fn.Foreign.Path != ir.CPath || fn.IsCCast() {
			for _, i := range contexts {
				c.error(pos(i), "#[c.context] marks what a #[c.native] function hands its callback, and %s is not a C function", fn.Name)
			}
			for _, i := range releases {
				c.error(pos(i), "#[c.release] marks what a #[c.native] function calls to release its callback, and %s is not a C function", fn.Name)
			}
			for _, i := range statics {
				c.error(pos(i), "#[c.static] marks a callback a #[c.native] function takes, and %s is not a C function", fn.Name)
			}
			continue
		}
		for _, i := range statics {
			p := fn.Params[i]
			if why := cStaticRefusal(p); why != "" {
				c.error(pos(i), "callback %q %s", p.Name, why)
			}
		}
		c.checkCHeld(fn, pos, helds, releases, callbacks)
		for _, i := range releases {
			p := fn.Params[i]
			switch {
			case ir.CTypeOf(p.Type) == nil || ir.CStructOf(p.Type) != nil:
				c.error(pos(i), "parameter %q takes a C function: declare it c.pointer, or a #[c.native] struct naming the C function's own spelling of one, not %s", p.Name, p.Type)
			case len(contexts) == 0 && len(helds) == 0:
				c.error(pos(i), "parameter %q releases a callback's context, and %s marks no #[c.context] (nor takes a c.held value)", p.Name, fn.Name)
			case i != releases[0]:
				c.error(pos(i), "C function %q marks a second release, %q, and C releases its one context once", fn.Foreign.Name, p.Name)
			}
		}
		if len(releases) > 0 {
			for _, i := range callbacks {
				if fn.Params[i].Transfer == ir.TransferRetains {
					c.error(pos(i), "callback %q is held until C calls %q, which #[c.release] says, so it is not #[retains] as well", fn.Params[i].Name, fn.Params[releases[0]].Name)
				}
			}
		}
		// A string result is read back as the `char *` C answers; a wide
		// string, which an alias of string spells, is not read back.
		if fn.AST.ReturnType != nil {
			if a := c.cAliasOf(fn.AST.ReturnType, fn.Return); a != nil && a.Type != nil && a.Type.Kind == ir.TypeString {
				c.error(fn.AST.Pos, "C function %q answers a %s, and a string C answers is read as a `char *`: declare it c.pointer and copy it in C", fn.Foreign.Name, a.Name)
			}
		}
		for i, p := range fn.Params {
			// NULL is a pointer's: an option crosses as one where its alias
			// is a string, and a number or bool has none to be.
			if p.Type != nil && p.Type.Kind == ir.TypeOption && p.CAlias != nil && !p.CAlias.IsCHeld() && p.CAlias.Type != nil && p.CAlias.Type.Kind != ir.TypeString {
				c.error(pos(i), "parameter %q is an option of %s, and C has no NULL %s: an option crosses to C as a pointer, a string's", p.Name, p.CAlias.Name, p.CAlias.Name)
			}
			if p.Type != nil && p.Type.Kind == ir.TypeRef && !cFieldValue(p.Type.Elems[0], p.CAlias) && !ir.IsCPointerOption(p.Type.Elems[0]) && p.CFrees == "" {
				c.error(pos(i), "parameter %q is a %s, and a ref a C function fills is to an int, float or bool, a #[c.native] alias of one, a C struct, an option of a C pointer handle, or a string marked #[c.frees]", p.Name, p.Type)
			}
		}
		for _, i := range contexts {
			if p := fn.Params[i]; ir.CTypeOf(p.Type) == nil || ir.CStructOf(p.Type) != nil {
				c.error(pos(i), "parameter %q is a callback's context, a C pointer: declare it c.pointer, or a #[c.native] struct naming the C function's own spelling of one, not %s", p.Name, p.Type)
			}
		}
		switch {
		case len(callbacks) == 0 && len(contexts) > 0:
			c.error(pos(contexts[0]), "parameter %q is a callback's context, and %s takes no func for it to be handed to", fn.Params[contexts[0]].Name, fn.Name)
		case len(callbacks) > 0 && len(contexts) == 0:
			c.error(pos(callbacks[0]), "C function %q takes %q, a func, which C can call only through a function handed the closure back: mark the `void *` it hands its callback #[c.context]", fn.Foreign.Name, fn.Params[callbacks[0]].Name)
		case len(callbacks) > 1:
			c.error(pos(callbacks[1]), "C function %q takes a second func, %q, and C hands its one context to one callback", fn.Foreign.Name, fn.Params[callbacks[1]].Name)
		case len(contexts) > 1:
			c.error(pos(contexts[1]), "C function %q marks a second context, %q, and takes one callback to hand one to", fn.Foreign.Name, fn.Params[contexts[1]].Name)
		}
		for _, i := range callbacks {
			p := fn.Params[i]
			if why := cCallbackRefusal(p.Type.Sig); why != "" {
				c.error(pos(i), "callback %q %s: a callback crosses an int, float, bool or #[c.native] handle (a struct declaring no fields), which C has a type for", p.Name, why)
			}
			if j := i - skip; j >= 0 && j < len(fn.AST.Params.Params) {
				c.checkCallbackAliases(p.Name, fn.AST.Params.Params[j].Type, pos(i))
			}
		}
	}
}

// checkCFrees judges sngl:x/c's #[frees] on fn: it marks a C function
// answering a string or an option of one, and a ref[string] parameter of
// one, which C fills.
func (c *checker) checkCFrees(fn *ir.Func, pos func(int) ast.Pos) {
	native := fn.Foreign.Path == ir.CPath && !fn.IsCCast()
	if fn.CFrees != "" {
		r := fn.Return
		if r != nil && r.Kind == ir.TypeOption && len(r.Elems) == 1 {
			r = r.Elems[0]
		}
		switch {
		case !native:
			c.error(fn.AST.Pos, "#[c.frees] marks a #[c.native] function's string result, and %s is not a C function", fn.Name)
		case r == nil || r.Kind != ir.TypeString:
			answers := "nothing"
			if fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
				answers = fn.Return.String()
			}
			c.error(fn.AST.Pos, "#[c.frees(%q)] frees the string %s answers, and it answers %s", fn.CFrees, fn.Name, answers)
		}
	}
	for i, p := range fn.Params {
		if p.CFrees == "" {
			continue
		}
		switch {
		case !native:
			c.error(pos(i), "#[c.frees] marks a #[c.native] function's ref[string] parameter, and %s is not a C function", fn.Name)
		case p.Type == nil || p.Type.Kind != ir.TypeRef || p.Type.Elems[0].Kind != ir.TypeString:
			c.error(pos(i), "parameter %q is marked #[c.frees(%q)], which frees a string C stores through a ref[string], and it is a %s", p.Name, p.CFrees, p.Type)
		}
	}
}

// checkCHeld judges what fn, a C function, says of sngl:x/c's `held`: a
// parameter written `c.held[T]` hands C a copy of a T the program keeps
// until C calls the function its `#[c.release]` parameter is handed, so the
// function marks one, takes no callback that release would be ambiguous
// with, and holds one value; and a result written with it is
// `option[c.held[T]]`, since C answers NULL where it holds nothing. A T that
// is owned is refused either way: what C holds, no lifetime of the
// compiler's releases.
func (c *checker) checkCHeld(fn *ir.Func, pos func(int) ast.Pos, helds, releases, callbacks []int) {
	for k, i := range helds {
		p := fn.Params[i]
		switch {
		case p.Type != nil && p.Type.Kind == ir.TypeOption:
			c.error(pos(i), "parameter %q is an option of a c.held value, which crosses as the pointer to a copy C holds: declare a C function taking an option[c.pointer] for NULL", p.Name)
		case ir.Owned(p.Type):
			c.error(pos(i), "parameter %q holds a %s, which is owned, and what C holds no lifetime of the program's releases", p.Name, p.Type)
		case k > 0:
			c.error(pos(i), "C function %q takes a second c.held value, %q, and releases one", fn.Foreign.Name, p.Name)
		case len(callbacks) > 0:
			c.error(pos(i), "C function %q takes a c.held value, %q, and a callback, %q, and its one release would let go of one of them", fn.Foreign.Name, p.Name, fn.Params[callbacks[0]].Name)
		case len(releases) == 0:
			c.error(pos(i), "parameter %q is a copy C holds until it calls the function a #[c.release] parameter is handed, and %s marks none", p.Name, fn.Name)
		}
	}
	if fn.ReturnCAlias.IsCHeld() {
		at := fn.AST.Pos
		if nt, ok := fn.AST.ReturnType.(*ast.NamedType); ok {
			at = nt.Pos
		}
		switch {
		case fn.Return == nil || fn.Return.Kind != ir.TypeOption:
			c.error(at, "C function %q answers a c.held value, and answers NULL where it holds none: declare it option[c.held[%s]]", fn.Foreign.Name, fn.Return)
		case ir.Owned(fn.Return.Elems[0]):
			c.error(at, "C function %q answers a held %s, which is owned, and what C holds no lifetime of the program's releases", fn.Foreign.Name, fn.Return.Elems[0])
		}
	}
}

// checkCallbackAliases refuses an alias of float in a callback's
// signature that is not C's double: the dispatcher a translator writes
// takes and answers a float as a double, the signature's aliases expanded,
// and C passing a 4-byte float where a double is read hands it garbage.
func (c *checker) checkCallbackAliases(name string, te ast.TypeExpr, at ast.Pos) {
	ft, ok := te.(*ast.FuncType)
	if !ok {
		return
	}
	check := func(te ast.TypeExpr) {
		if te == nil {
			return
		}
		a := c.cAliasOf(te, nil)
		if a != nil && a.Type != nil && a.Type.Kind == ir.TypeFloat && a.C != nil && a.C.Name != "double" {
			c.error(at, "callback %q crosses a %s, C's %s, and a callback's float crosses as a double: write float", name, a.Name, a.C.Name)
		}
	}
	for _, p := range ft.Params {
		check(p.Type)
	}
	check(ft.Return)
}

// cStaticRefusal says what in p, a callback C calls with no context
// (sngl:x/c's #[static]), cannot cross, or "". It takes what any callback
// does and a string, which is copied as C calls; marked #[c.outs] it
// answers a struct of scalars, each written through a pointer C hands it.
func cStaticRefusal(p *ir.Param) string {
	sig := p.Type.Sig
	if sig.Color == ir.ColorAsync {
		return "is async, and C waits for its callback to return"
	}
	for _, sp := range sig.Params {
		if !cCallbackValue(sp.Type) && sp.Type.Kind != ir.TypeString {
			return fmt.Sprintf("takes a %s: a static callback crosses an int, float, bool, string or #[c.native] handle", sp.Type)
		}
	}
	r := sig.Return
	if p.COuts {
		sd, _ := r.Decl.(*ir.StructDef)
		if r.Kind != ir.TypeStruct || sd == nil || len(sd.Fields) == 0 || ir.CTypeOf(r) != nil {
			return fmt.Sprintf("is marked #[c.outs] and answers %s: what C reads through the pointers it hands the callback is a struct of the program's, a field for each", typeOrNothing(r))
		}
		for _, f := range sd.Fields {
			switch f.Type.Kind {
			case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			default:
				return fmt.Sprintf("is marked #[c.outs] and answers a %s whose field %q is a %s: C reads an int, float or bool through a pointer", r, f.Name, f.Type)
			}
		}
		return ""
	}
	if r != nil && r.Kind != ir.TypeVoid && !cCallbackValue(r) {
		return fmt.Sprintf("answers a %s: a static callback answers an int, float, bool or #[c.native] handle, or through pointers where it is marked #[c.outs]", r)
	}
	return ""
}

func typeOrNothing(t *ir.Type) string {
	if t == nil || t.Kind == ir.TypeVoid {
		return "nothing"
	}
	return "a " + t.String()
}

// checkCStaticArgs refuses an argument for a `#[c.static]` callback that is
// not a function of a package by name: C calls it with nothing to find a
// closure by, so a lambda's captures and a method's receiver have nowhere to
// come from.
func (c *checker) checkCStaticArgs() {
	_ = ir.Walk(c.pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.AST == nil || call.Func == nil {
			return nil
		}
		for _, p := range call.Func.Params {
			if !p.CStatic {
				continue
			}
			arg := call.ArgFor(p.Name)
			if arg == nil {
				continue
			}
			if StaticCallee(arg) == nil {
				c.error(call.AST.Pos, "callback %q of C function %q is called with no context, so its argument names a function of a package: a lambda or a method has nothing to find what it captured by", p.Name, call.Func.Foreign.Name)
			}
		}
		return nil
	})
}

// StaticCallee is the function e names where e is one a `#[c.static]`
// callback may be handed: a package's function, no method and no lambda.
func StaticCallee(e ir.Expr) *ir.Func {
	return ir.StaticCallee(e)
}

// cCallbackRefusal says what in sig no C callback can carry, or "".
func cCallbackRefusal(sig *ir.FuncSig) string {
	if sig.Color == ir.ColorAsync {
		return "is async, and C waits for its callback to return"
	}
	for _, p := range sig.Params {
		if !cCallbackValue(p.Type) {
			return fmt.Sprintf("takes a %s", p.Type)
		}
	}
	if r := sig.Return; r != nil && r.Kind != ir.TypeVoid && !cCallbackValue(r) {
		return fmt.Sprintf("answers a %s", r)
	}
	return ""
}

func cCallbackValue(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return true
	}
	return ir.CTypeOf(t) != nil && ir.CStructOf(t) == nil
}

// cFieldValue reports whether a C struct's field, or what a C function's
// `ref` fills, may be t: a scalar, as itself or a typedef's alias, or a C
// struct in turn.
func cFieldValue(t *ir.Type, alias *ir.Alias) bool {
	if t == nil {
		return false
	}
	if alias != nil && alias.C != nil && alias.C.Ptr > 0 {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return true
	}
	return ir.CStructOf(t) != nil
}

// checkCStructs refuses a C struct's field (ir.StructDef.IsCStruct) holding
// what is not a C scalar or a C struct: a string, a list or a pointer would
// cross as memory one side owns, which a platform's C runtime handles.
func (c *checker) checkCStructs(pkg *ir.Package) {
	for _, sd := range pkg.Structs {
		if !sd.IsCStruct() || sd.AST == nil {
			continue
		}
		pos := map[string]ast.Pos{}
		for _, f := range sd.AST.Fields() {
			for i, n := range f.Names {
				pos[n] = f.Pos
				if i < len(f.NamePositions) {
					pos[n] = f.NamePositions[i]
				}
			}
		}
		for _, f := range sd.Fields {
			if !cFieldValue(f.Type, f.CAlias) {
				c.error(pos[f.Name], "field %q of %s, the C struct %s, is a %s: a C struct's field is an int, float or bool, a #[c.native] alias of one, or a C struct", f.Name, sd.Name, sd.Foreign.C, f.Type)
			}
		}
	}
}
