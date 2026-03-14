package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
	"git.duckfam.us/jonathan/sngl/snglparser"
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

// parseSNGL dispatches to the correct parser based on file extension.
func parseSNGL(filename string, r io.Reader) (*ast.Document, error) {
	if strings.HasSuffix(strings.ToLower(filename), ".sngl.kdl") {
		return parser.Parse(filename, r)
	}
	return snglparser.Parse(filename, r)
}

func isSNGLFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".sngl.kdl") || strings.HasSuffix(lower, ".sngl")
}
