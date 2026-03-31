package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
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
	return files, nil
}

// parseSNGL parses a .sngl file.
func parseSNGL(filename string, r io.Reader) (*ast.Document, error) {
	return snglparser.Parse(filename, r)
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

// mergeInto merges definitions from src into dst.
func mergeInto(dst, src *ast.Document) {
	dst.Components = append(dst.Components, src.Components...)
	dst.Structs = append(dst.Structs, src.Structs...)
	dst.Enums = append(dst.Enums, src.Enums...)
	dst.Functions = append(dst.Functions, src.Functions...)
	dst.Units = append(dst.Units, src.Units...)
	dst.Styles = append(dst.Styles, src.Styles...)
	dst.StyleDefs = append(dst.StyleDefs, src.StyleDefs...)
	dst.Timers = append(dst.Timers, src.Timers...)
	dst.Imports = append(dst.Imports, src.Imports...)
	dst.Consts = append(dst.Consts, src.Consts...)
	dst.Data = append(dst.Data, src.Data...)
	dst.Computeds = append(dst.Computeds, src.Computeds...)
	if src.App != nil && dst.App == nil {
		dst.App = src.App
	}
	if len(src.Outputs) > 0 && len(dst.Outputs) == 0 {
		dst.Outputs = src.Outputs
	}
}

// validateOutputs checks that output declarations reference valid lang/platform
// pairs and that the platform supports the language.
func validateOutputs(doc *ast.Document) error {
	for _, out := range doc.Outputs {
		lang := codegen.LookupLang(out.Lang)
		if lang == nil {
			return fmt.Errorf("%s: unknown language %q (available: %v)", out.Pos, out.Lang, codegen.Langs())
		}
		plat := codegen.LookupPlatform(out.Platform)
		if plat == nil {
			return fmt.Errorf("%s: unknown platform %q (available: %v)", out.Pos, out.Platform, codegen.Platforms())
		}
		if !slices.Contains(plat.SupportedLangs(), out.Lang) {
			return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", out.Pos, out.Platform, out.Lang, plat.SupportedLangs())
		}
	}
	return nil
}

// defaultSchemeResolver returns a SchemeResolver that delegates to registered
// codegen scheme importers.
func defaultSchemeResolver() checker.SchemeResolver {
	return func(scheme, uri, dir string) (*ast.NativeDecls, error) {
		imp := codegen.LookupScheme(scheme)
		if imp == nil {
			return nil, fmt.Errorf("unknown import scheme %q", scheme)
		}
		return imp.Resolve(uri, dir)
	}
}
