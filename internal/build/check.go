package build

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CheckConfig is what a check of one package needs beyond the document.
type CheckConfig struct {
	// Dir is what imports and generated paths resolve against.
	Dir string
	// FS is what imports are read through. Nil reads Dir off disk.
	FS fs.FS
	// Resolver resolves directory and scheme imports. Nil builds one over FS.
	Resolver checker.ImportResolver
	IsMain   bool
	// Targets are the compile targets a caller selected itself, from
	// `--platform`/`--lang`; passing none leaves the document's own `output`
	// blocks to name them. Either way a target's library package is loaded as
	// though the document had imported it, so its overrides are checked here
	// and its failures belong to this build.
	Targets []ir.StaticTarget
}

// Check type-checks doc against every registered language and platform.
//
// The package is returned even on error, so a caller that wants to report more
// than the first diagnostic has something to walk.
func Check(doc *ast.Document, cfg CheckConfig) (*ir.Package, error) {
	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}
	fsys := cfg.FS
	if fsys == nil {
		fsys = os.DirFS(dir)
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = &Resolver{FS: fsys}
	}
	langs, plats := RegisteredTargets()
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        fsys,
		Dir:       dir,
		IsMain:    cfg.IsMain,
		Resolver:  resolver,
		Languages: langs,
		Platforms: plats,
		Targets:   cfg.Targets,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			if os.Getenv("SNGL_DEBUG_CHECK") != "" {
				for _, d2 := range diags {
					fmt.Fprintf(os.Stderr, "CHECK DIAG: %s\n", d2.Error())
				}
			}
			return pkg, d
		}
	}
	return pkg, nil
}

// RegisteredTargets is every language and platform the binary registered.
func RegisteredTargets() ([]ir.Language, []ir.Platform) {
	var langs []ir.Language
	for _, name := range codegen.Langs() {
		if l := codegen.LookupLang(name); l != nil {
			langs = append(langs, l)
		}
	}
	var plats []ir.Platform
	for _, name := range codegen.Platforms() {
		if p := codegen.LookupPlatform(name); p != nil {
			plats = append(plats, p)
		}
	}
	return langs, plats
}

// ParsePackageFS merges every .sngl file at the root of fsys into the single
// document the checker takes, in name order.
//
// Name order rather than readdir order because a merged document's declaration
// positions end up in generated output, and a golden that depends on the
// filesystem's ordering is a golden that fails on someone else's machine.
func ParsePackageFS(fsys fs.FS) (*ast.Document, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var doc *ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sngl") {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		sibling, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if doc == nil {
			doc = sibling
			continue
		}
		MergeInto(doc, sibling)
	}
	if doc == nil {
		return nil, fmt.Errorf("no .sngl files found")
	}
	return doc, nil
}

// MergeInto appends src's statements to dst.
//
// Imports come along. They stay file-scoped -- the checker groups a merged
// document's statements back by the file they were parsed from and gives each
// group its own import scope -- so an alias one file binds is still invisible
// to its siblings. Dropping them here instead meant a sibling could only use
// what the file named on the command line had imported.
func MergeInto(dst, src *ast.Document) {
	dst.Stmts = append(dst.Stmts, src.Stmts...)
}

// NewResolver builds the resolver a check of dir uses. One is built per check,
// which is the lifetime any scheme session it opens inherits.
func NewResolver(dir string) *Resolver {
	return &Resolver{RootDir: dir, FS: os.DirFS(dir)}
}

// NewFSResolver serves a package's imports out of fsys. rootDir stays empty,
// so a relative import escaping the FS root is an error rather than a read off
// the real filesystem.
func NewFSResolver(fsys fs.FS) *Resolver {
	return &Resolver{FS: fsys}
}

// Resolver resolves a package's imports. One is built per check (see Check),
// which is the lifetime any scheme session it opens inherits.
//
// A zero RootDir means a relative import escaping the FS root is an error
// rather than a read off the real filesystem, which is what an in-memory
// package wants.
type Resolver struct {
	RootDir string
	FS      fs.FS

	mu       sync.Mutex
	sessions map[string]codegen.SchemeImporter
}

func (r *Resolver) Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error) {
	// Relative imports that escape the FS root (e.g. `../docui`) can't be
	// served by io/fs, so fall back to direct filesystem reads.
	if strings.Contains(importPath, "..") && r.RootDir != "" {
		abs := filepath.Clean(filepath.Join(r.RootDir, importPath))
		return ResolveImportFromDir(abs)
	}
	// A sibling package is ordinarily written "./geom", which io/fs rejects:
	// fs.ValidPath has no "./" prefix. Clean gives the form ReadDir accepts.
	dir := path.Clean(importPath)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", importPath, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		p := path.Join(dir, e.Name())
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func ResolveImportFromDir(dir string) ([]*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", dir, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// scheme returns the importer to resolve through: a session when the scheme
// offers one, so that whatever it caches lives exactly as long as this check.
func (r *Resolver) scheme(name string) codegen.SchemeImporter {
	imp := codegen.LookupScheme(name)
	sess, ok := imp.(codegen.SchemeSession)
	if !ok {
		return imp
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]codegen.SchemeImporter{}
	}
	if got, ok := r.sessions[name]; ok {
		return got
	}
	open := sess.NewSession()
	r.sessions[name] = open
	return open
}

func (r *Resolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	imp := r.scheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("unknown import scheme %q", scheme)
	}
	// A native import can be the slowest single thing in a check (go: runs
	// the Go loader), and its cost is invisible in the "check" phase timing.
	start := time.Now()
	defer func() { slog.Info("resolve scheme", "uri", uri, "duration", time.Since(start)) }()
	if fsa, ok := imp.(codegen.FSAwareScheme); ok && r.FS != nil {
		return fsa.ResolveFS(uri, r.FS, dir)
	}
	return imp.Resolve(uri, dir)
}

// The FS is returned alongside the documents so nested imports within the
// package resolve against it. (nil, nil, nil) means the scheme has no FS
// importer registered.
func (r *Resolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	imp := codegen.LookupFSScheme(scheme)
	if imp == nil {
		return nil, nil, nil
	}
	fsys, err := imp.ResolveFS(uri, dir)
	if err != nil {
		return nil, nil, err
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("reading scheme FS root: %w", err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		docs = append(docs, doc)
	}
	return docs, fsys, nil
}
