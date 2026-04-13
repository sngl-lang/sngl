package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	RegisterScheme(&FileImporter{})
}

// FileImporter resolves file:// scheme imports for directory-based static assets.
type FileImporter struct{}

func (f *FileImporter) Scheme() string { return "file" }

func (f *FileImporter) Resolve(uri, dir string) (*NativeDecls, error) {
	relPath := strings.TrimPrefix(uri, "file://")
	absDir := filepath.Join(dir, relPath)

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("file asset directory %q not found: %w", relPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file:// import %q is not a directory", relPath)
	}

	return &NativeDecls{
		ImportPath: absDir,
		Vars: []NativeVar{
			{
				Name:       "path",
				Type:       "string",
				NativePkg:  "file",
				NativeType: "path",
				IsFunc:     true,
				Pure:       true,
			},
			{
				Name:       "contents",
				Type:       "string",
				NativePkg:  "file",
				NativeType: "contents",
				IsFunc:     true,
				Pure:       true,
			},
		},
	}, nil
}
