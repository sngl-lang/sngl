package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func discoverFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var files []string
	for _, arg := range args {
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

// parseSNGL parses a .sngl file.
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

// parseDir parses all .sngl files in a directory and merges them into a single
// document. This implements package-level semantics: all definitions in the
// directory become part of the same compilation unit.
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

// mergeDir merges all sibling .sngl files in the same directory as filename
// into doc. This implements package-level merging: all definitions in sibling
// files become part of the same compilation unit.
func mergeDir(doc *ast.Document, filename string) *ast.Document {
	dir := filepath.Dir(filename)
	base := filepath.Base(filename)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return doc
	}

	for _, e := range entries {
		if e.IsDir() || !isSNGLFile(e.Name()) || e.Name() == base {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sibling, err := parseSNGL(path, f)
		f.Close()
		if err != nil {
			continue
		}
		mergeInto(doc, sibling)
	}

	return doc
}

// mergeInto merges definitions from src into dst, excluding imports.
// Each file's imports are file-scoped and do not leak into siblings.
func mergeInto(dst, src *ast.Document) {
	for _, stmt := range src.Stmts {
		if _, isImport := stmt.(*ast.Import); isImport {
			continue
		}
		dst.Stmts = append(dst.Stmts, stmt)
	}
}

// validateOutputs checks that output declarations reference valid lang/platform
// pairs and that the platform supports the language.
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

// checkDoc type-checks a parsed document. Returns an error if any diagnostics are errors.
func checkDoc(doc *ast.Document, dir string, isMain bool) (*ir.Package, error) {
	langs, plats := collectTargets()
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        os.DirFS(dir),
		Dir:       dir,
		IsMain:    isMain,
		Resolver:  &cliResolver{rootDir: dir},
		Languages: langs,
		Platforms: plats,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return pkg, d
		}
	}
	return pkg, nil
}

// cliResolver implements checker.ImportResolver using registered codegen schemes.
type cliResolver struct {
	rootDir string
}

func (r *cliResolver) Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error) {
	// Relative imports that escape the FS root (e.g. `../docui`) can't be
	// served by io/fs, so fall back to direct filesystem reads.
	if strings.Contains(importPath, "..") && r.rootDir != "" {
		abs := filepath.Clean(filepath.Join(r.rootDir, importPath))
		return resolveImportFromDir(abs)
	}
	entries, err := fs.ReadDir(fsys, importPath)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", importPath, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		path := importPath + "/" + e.Name()
		data, err := fs.ReadFile(fsys, path)
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

func (r *cliResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	imp := codegen.LookupScheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("unknown import scheme %q", scheme)
	}
	return imp.Resolve(uri, dir)
}

// explicitFileSet returns the absolute paths of args that are regular files
// (not directories). Used to skip sibling merging when a specific file is passed.
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

// resolveLangPlat fills in a default language when only --platform is given.
// When --platform is set and --lang is omitted, the first supported language
// for that platform is used. --lang without --platform is still an error.
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

// collectTargets gathers registered languages and platforms as checker targets.
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
