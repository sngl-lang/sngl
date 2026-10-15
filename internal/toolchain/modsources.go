package toolchain

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const modulePath = "duckfam.us/sngl"

// HashModuleImports writes into h the source of every package of this module
// that the Go files among files import, transitively: what a build of them
// reads from the checkout rather than from the files themselves, so a record
// keyed on files alone would outlive an edit to a runtime such as gtk4rt.
func HashModuleImports(h io.Writer, files map[string][]byte) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	pending := map[string]bool{}
	for name, src := range files {
		if filepath.Ext(name) == ".go" {
			addModuleImports(name, src, pending)
		}
	}
	hashed := map[string]bool{}
	for len(pending) > 0 {
		var next []string
		for p := range pending {
			if !hashed[p] {
				next = append(next, p)
			}
		}
		clear(pending)
		slices.Sort(next)
		for _, p := range next {
			hashed[p] = true
			if err := hashPackage(h, root, p, pending); err != nil {
				return err
			}
		}
	}
	return nil
}

func hashPackage(h io.Writer, root, p string, imports map[string]bool) error {
	dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(p, modulePath)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("hashing %s: %w", p, err)
	}
	fmt.Fprintf(h, "%d\x00%s", len(p), p)
	for _, e := range entries {
		name := e.Name()
		switch filepath.Ext(name) {
		case ".go", ".c", ".h":
		default:
			continue
		}
		if e.IsDir() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d\x00%s%d\x00", len(name), name, len(src))
		h.Write(src)
		if filepath.Ext(name) == ".go" {
			addModuleImports(name, src, imports)
		}
	}
	return nil
}

func addModuleImports(name string, src []byte, imports map[string]bool) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		return
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
			imports[p] = true
		}
	}
}

// moduleRoot is the checkout this process runs in: the nearest directory
// above the working directory whose go.mod declares this module.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(data), "module "+modulePath+"\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod declaring " + modulePath + " above the working directory")
		}
		dir = parent
	}
}
