// Package file registers the file: import scheme for directory-based
// static assets.
package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.RegisterScheme(&Importer{})
}

// Importer resolves file: scheme imports for directory-based static assets.
type Importer struct{}

func (f *Importer) Scheme() string { return "file" }

func (f *Importer) Resolve(uri, dir string) (*ir.NativeImport, error) {
	relPath := uri
	absDir := filepath.Join(dir, relPath)

	// Guard against path traversal: the resolved path must stay under the
	// importing project's directory. Anything escaping (e.g. `../../etc`)
	// is rejected up front.
	cleanDir, derr := filepath.Abs(filepath.Clean(dir))
	if derr != nil {
		return nil, fmt.Errorf("file: resolve: %w", derr)
	}
	cleanAbs, aerr := filepath.Abs(filepath.Clean(absDir))
	if aerr != nil {
		return nil, fmt.Errorf("file: resolve: %w", aerr)
	}
	if cleanAbs != cleanDir && !strings.HasPrefix(cleanAbs, cleanDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("file: import %q escapes project directory", relPath)
	}

	// Through an os.Root on the project directory, so a symlink inside it
	// that points out of it escapes as surely as `..` would.
	root, err := os.OpenRoot(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("file: resolve: %w", err)
	}
	defer root.Close()
	rel, err := filepath.Rel(cleanDir, cleanAbs)
	if err != nil {
		return nil, fmt.Errorf("file: resolve: %w", err)
	}
	info, err := root.Stat(rel)
	if err != nil {
		if strings.Contains(err.Error(), "path escapes") {
			return nil, fmt.Errorf("file: import %q escapes project directory through a symlink", relPath)
		}
		return nil, fmt.Errorf("file asset directory %q not found: %w", relPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file: import %q is not a directory", relPath)
	}

	fileFunc := func(name string) *ir.Func {
		return &ir.Func{
			Name:    name,
			Params:  []*ir.Param{{Name: "name", Type: ir.TypString}},
			Return:  ir.TypString,
			Purity:  ir.PurityPure,
			Const:   true,
			Foreign: ir.Foreign{Path: "file", Name: name},
		}
	}
	// names lists the directory rather than naming one file in it, so a
	// directory's contents can reach a pure function as an argument: read
	// here, at build time, where every compile reads it again, instead of by
	// the function while it runs, where nothing records that it did.
	names := &ir.Func{
		Name:    "names",
		Params:  []*ir.Param{{Name: "pattern", Type: ir.TypString}},
		Return:  &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}},
		Purity:  ir.PurityPure,
		Const:   true,
		Foreign: ir.Foreign{Path: "file", Name: "names"},
	}
	return &ir.NativeImport{
		ImportPath: absDir,
		Funcs:      []*ir.Func{fileFunc("path"), fileFunc("contents"), names},
	}, nil
}
