package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Any status but 0 — not ignored, no git, not a repo — reads as not-ignored,
// so the `...` walk still works outside a repository.
func gitIgnored(path string) bool {
	return exec.Command("git", "check-ignore", "-q", path).Run() == nil
}

func discoverFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var files []string
	for _, arg := range args {
		// Mirrors `go test ./...`, skipped directories included.
		if strings.HasSuffix(arg, "/...") || arg == "..." || arg == "./..." {
			root := strings.TrimSuffix(arg, "/...")
			if root == "" || root == "." {
				root = "."
			}
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if path == root {
						return nil
					}
					if shouldSkipWalkDir(d.Name()) || gitIgnored(path) {
						return filepath.SkipDir
					}
					return nil
				}
				if isSNGLFile(path) && !gitIgnored(path) {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		if !info.IsDir() {
			files = append(files, arg)
			continue
		}
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && isSNGLFile(path) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
	}
	slog.Info("discover", "files", len(files))
	for _, f := range files {
		slog.Debug("discovered", "file", f)
	}
	return files, nil
}

func parseSNGL(filename string, r io.Reader) (*ast.Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return parser.Parse(filename, data)
}

func isSNGLFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".sngl")
}

// Mirrors `go test ./...`: testdata, plus anything beginning with `.` or `_`.
func shouldSkipWalkDir(name string) bool {
	if name == "testdata" {
		return true
	}
	if name == "" {
		return false
	}
	c := name[0]
	return c == '.' || c == '_'
}

// A unit is one thing a command works on. Commands differ in what they do with
// one — check it, generate from it, run its tests — and no longer in how they
// decide what one is: every command calls resolveUnits.
//
// They used to each answer that question for themselves, and disagreed.
// `sngl generate` merged a file's siblings, `sngl test` deliberately never did,
// and `sngl check` read every file alone — so the same package's declarations
// were in scope for one command and undefined for the next.
type unit struct {
	// dir is what imports and generated paths resolve against.
	dir string
	// name identifies the unit in diagnostics: the file, or the directory.
	name string
	// files are the .sngl files the command was asked about here, in discovery
	// order. A package reads its whole directory whatever this holds; this is
	// what it reports on.
	files []string
	// solo marks a file read without its siblings: one the command line named,
	// or one from a directory that is not a package.
	solo bool
}

// headline is the file a unit is named after in generated output.
func (u unit) headline() string {
	if len(u.files) > 0 {
		return u.files[0]
	}
	return u.name
}

// doc reads the unit into the single document the checker takes.
func (u unit) doc() (*ast.Document, error) {
	if !u.solo {
		return parseDir(u.dir)
	}
	f, err := os.Open(u.name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSNGL(u.name, f)
}

// resolveUnits turns command-line arguments into the units to work on:
//
//   - a file named on the command line is its own unit — naming it is the
//     request, and a corpus of one-file programs in one directory relies on it;
//   - a directory is a package, and all of its .sngl files are read together;
//   - a directory whose files declare more than one `component main` is not a
//     package but a collection of programs, and each file is its own unit.
func resolveUnits(args []string) ([]unit, error) {
	files, err := discoverFiles(args)
	if err != nil {
		return nil, err
	}
	explicit := explicitFileSet(args)

	var grouped []unit
	index := map[string]int{}
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		if explicit[abs] {
			grouped = append(grouped, unit{dir: filepath.Dir(f), name: f, files: []string{f}, solo: true})
			continue
		}
		dir := filepath.Dir(f)
		i, seen := index[dir]
		if !seen {
			i = len(grouped)
			index[dir] = i
			grouped = append(grouped, unit{dir: dir, name: dir})
		}
		grouped[i].files = append(grouped[i].files, f)
	}

	var out []unit
	for _, u := range grouped {
		if u.solo || isPackageDir(u.dir) {
			out = append(out, u)
			continue
		}
		for _, f := range u.files {
			out = append(out, unit{dir: u.dir, name: f, files: []string{f}, solo: true})
		}
	}
	return out, nil
}

// isPackageDir reports whether a directory's files form one package.
//
// A package declares at most one `component main` — the checker says so — and
// a directory with several is a corpus of one-file programs rather than a
// package. Reading it as one would report every fixture after the first as a
// duplicate declaration, which is true and useless.
//
// A file that does not parse is not counted: a corpus of deliberately broken
// fixtures is still a corpus.
func isPackageDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	mains := 0
	for _, e := range entries {
		if e.IsDir() || !isSNGLFile(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		doc, err := parseSNGL(path, f)
		f.Close()
		if err != nil {
			continue
		}
		for _, stmt := range doc.Stmts {
			if c, ok := stmt.(*ast.ComponentDecl); ok && c.Name == "main" {
				mains++
			}
		}
		if mains > 1 {
			return false
		}
	}
	return true
}

// A directory is one compilation unit: every .sngl file in it merges into one
// document.
func parseDir(dir string) (*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var doc *ast.Document
	for _, e := range entries {
		if e.IsDir() || !isSNGLFile(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		sibling, err := parseSNGL(path, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if doc == nil {
			doc = sibling
			continue
		}
		mergeInto(doc, sibling)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: no .sngl files found", dir)
	}
	return doc, nil
}

// Imports come along. They stay file-scoped -- the checker groups a merged
// document's statements back by the file they were parsed from and gives each
// group its own import scope -- so an alias one file binds is still invisible
// to its siblings. Dropping them here instead meant a sibling could only use
// what the file named on the command line had imported.
func mergeInto(dst, src *ast.Document) {
	dst.Stmts = append(dst.Stmts, src.Stmts...)
}

func validateOutputs(pkg *ir.Package) error {
	for _, out := range pkg.Outputs {
		var pos ast.Pos
		if out.AST != nil {
			pos = out.AST.Pos
		}
		lang := codegen.LookupLang(out.Lang)
		if lang == nil {
			return fmt.Errorf("%s: unknown language %q (available: %v)", pos, out.Lang, codegen.Langs())
		}
		plat := codegen.LookupPlatform(out.Platform)
		if plat == nil {
			return fmt.Errorf("%s: unknown platform %q (available: %v)", pos, out.Platform, codegen.Platforms())
		}
		if !slices.Contains(plat.SupportedLangs(), out.Lang) {
			return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", pos, out.Platform, out.Lang, plat.SupportedLangs())
		}
	}
	return nil
}

// targets are the compile targets a caller selected itself, from
// `--platform`/`--lang`; passing none leaves the document's own `output` blocks
// to name them. Either way a target's library package is loaded as though the
// document had imported it, so its overrides are checked here and its failures
// belong to this build.
func checkDoc(doc *ast.Document, dir string, isMain bool, targets ...ir.StaticTarget) (*ir.Package, error) {
	langs, plats := collectTargets()
	fsys := os.DirFS(dir)
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        fsys,
		Dir:       dir,
		IsMain:    isMain,
		Resolver:  &cliResolver{rootDir: dir, fsys: fsys},
		Languages: langs,
		Platforms: plats,
		Targets:   targets,
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

// newCLIResolver builds the resolver a check of dir uses. One is built per
// check (see checkDoc), which is the lifetime any scheme session it opens
// inherits.
func newCLIResolver(dir string) *cliResolver {
	return &cliResolver{rootDir: dir, fsys: os.DirFS(dir)}
}

// One is built per check (see checkDoc), which is the lifetime any scheme
// session it opens inherits.
type cliResolver struct {
	rootDir string
	fsys    fs.FS

	mu       sync.Mutex
	sessions map[string]codegen.SchemeImporter
}

func (r *cliResolver) Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error) {
	// Relative imports that escape the FS root (e.g. `../docui`) can't be
	// served by io/fs, so fall back to direct filesystem reads.
	if strings.Contains(importPath, "..") && r.rootDir != "" {
		abs := filepath.Clean(filepath.Join(r.rootDir, importPath))
		return resolveImportFromDir(abs)
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

func resolveImportFromDir(dir string) ([]*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", dir, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// scheme returns the importer to resolve through: a session when the scheme
// offers one, so that whatever it caches lives exactly as long as this check.
func (r *cliResolver) scheme(name string) codegen.SchemeImporter {
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

func (r *cliResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	imp := r.scheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("unknown import scheme %q", scheme)
	}
	// A native import can be the slowest single thing in a check (go: runs
	// the Go loader), and its cost is invisible in the "check" phase timing.
	start := time.Now()
	defer func() { slog.Info("resolve scheme", "uri", uri, "duration", time.Since(start)) }()
	if fsa, ok := imp.(codegen.FSAwareScheme); ok && r.fsys != nil {
		return fsa.ResolveFS(uri, r.fsys, dir)
	}
	return imp.Resolve(uri, dir)
}

// The FS is returned alongside the documents so nested imports within the
// package resolve against it. (nil, nil, nil) means the scheme has no FS
// importer registered.
func (r *cliResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
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

// Marks the args naming a file, so sibling merging can be skipped for them.
// Directory and `...` args are NOT marked: the caller decides whether walked
// descendants are standalone (test mode) or a merged package (build).
func explicitFileSet(args []string) map[string]bool {
	m := make(map[string]bool)
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err == nil && !info.IsDir() {
			abs, _ := filepath.Abs(arg)
			m[abs] = true
		}
	}
	return m
}

func resolveLangPlat(cliLang, cliPlat string) (string, string, error) {
	if cliPlat != "" && cliLang == "" {
		plat := codegen.LookupPlatform(cliPlat)
		if plat == nil {
			return "", "", fmt.Errorf("unknown platform %q (available: %v)", cliPlat, codegen.Platforms())
		}
		langs := plat.SupportedLangs()
		if len(langs) == 0 {
			return "", "", fmt.Errorf("platform %q has no supported languages", cliPlat)
		}
		return langs[0], cliPlat, nil
	}
	if cliLang != "" && cliPlat == "" {
		return "", "", fmt.Errorf("--platform is required when --lang is specified")
	}
	return cliLang, cliPlat, nil
}

func collectTargets() ([]ir.Language, []ir.Platform) {
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
