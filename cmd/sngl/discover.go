package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
