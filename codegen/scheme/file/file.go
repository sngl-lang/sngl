// Package file registers the file:// import scheme for directory-based
// static assets.
package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterScheme(&Importer{})
}

// Importer resolves file:// scheme imports for directory-based static assets.
type Importer struct{}

func (f *Importer) Scheme() string { return "file" }

func (f *Importer) Resolve(uri, dir string) (*ir.NativeImport, error) {
	relPath := strings.TrimPrefix(uri, "file://")
	absDir := filepath.Join(dir, relPath)

	// Guard against path traversal: the resolved path must stay under the
	// importing project's directory. Anything escaping (e.g. `../../etc`)
	// is rejected up front.
	cleanDir, derr := filepath.Abs(filepath.Clean(dir))
	if derr != nil {
		return nil, fmt.Errorf("file:// resolve: %w", derr)
	}
	cleanAbs, aerr := filepath.Abs(filepath.Clean(absDir))
	if aerr != nil {
		return nil, fmt.Errorf("file:// resolve: %w", aerr)
	}
	if cleanAbs != cleanDir && !strings.HasPrefix(cleanAbs, cleanDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("file:// import %q escapes project directory", relPath)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("file asset directory %q not found: %w", relPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file:// import %q is not a directory", relPath)
	}

	fileFunc := func(name string) *ir.Func {
		return &ir.Func{
			Name:    name,
			Params:  []*ir.Param{{Name: "name", Type: ir.TypString}},
			Return:  ir.TypString,
			Purity:  ir.PurityPure,
			Foreign: ir.Foreign{Path: "file", Name: name},
		}
	}
	return &ir.NativeImport{
		ImportPath: absDir,
		Funcs:      []*ir.Func{fileFunc("path"), fileFunc("contents")},
	}, nil
}
