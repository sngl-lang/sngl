package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	RegisterScheme(&FileImporter{})
}

// FileImporter resolves file:// scheme imports for directory-based static assets.
type FileImporter struct{}

func (f *FileImporter) Scheme() string { return "file" }

func (f *FileImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	relPath := strings.TrimPrefix(uri, "file://")
	absDir := filepath.Join(dir, relPath)

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("file asset directory %q not found: %w", relPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file:// import %q is not a directory", relPath)
	}

	fileFunc := func(name string) *ir.Func {
		return &ir.Func{
			Name:       name,
			Params:     []*ir.Param{{Name: "name", Type: ir.TypString}},
			Return:     ir.TypString,
			Purity:     ir.PurityPure,
			NativePkg:  "file",
			NativeName: name,
		}
	}
	return &ir.NativeImport{
		ImportPath: absDir,
		Funcs:      []*ir.Func{fileFunc("path"), fileFunc("contents")},
	}, nil
}
