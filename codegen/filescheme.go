package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

func init() {
	RegisterScheme(&FileImporter{})
}

// FileImporter resolves file:// scheme imports for directory-based static assets.
// The directory becomes a namespace with pure functions:
//   - path(name) → output URL path for the file (marks it for copying to output)
//   - contents(name) → literal string contents of the file
//
// File access uses os.DirFS to root operations within the imported directory,
// preventing directory traversal. This also provides a consistent fs.FS interface
// that can be swapped for remote schemes later.
type FileImporter struct{}

func (f *FileImporter) Scheme() string { return "file" }

func (f *FileImporter) Resolve(uri, dir string) (*ast.NativeDecls, error) {
	relPath := strings.TrimPrefix(uri, "file://")
	absDir := filepath.Join(dir, relPath)

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("file asset directory %q not found: %w", relPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file:// import %q is not a directory", relPath)
	}

	return &ast.NativeDecls{
		ImportPath: absDir, // absolute path to directory (used as DirFS root)
		Data: []*ast.Data{
			{
				Name:       "path",
				Extern:     true,
				Purity:     ast.PurityPure,
				IsFunc:     true,
				ParamTypes: []string{"string"},
				ReturnType: "string",
				Init:       ast.Expr{TypeHint: "func:string~string"},
				Resolved: &ast.TypeInfo{
					Type:       "string",
					NativePkg:  "file",
					NativeType: "path",
				},
			},
			{
				Name:       "contents",
				Extern:     true,
				Purity:     ast.PurityPure,
				IsFunc:     true,
				ParamTypes: []string{"string"},
				ReturnType: "string",
				Init:       ast.Expr{TypeHint: "func:string~string"},
				Resolved: &ast.TypeInfo{
					Type:       "string",
					NativePkg:  "file",
					NativeType: "contents",
				},
			},
		},
	}, nil
}
