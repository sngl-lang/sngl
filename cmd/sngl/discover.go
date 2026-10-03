package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Any status but 0 — not ignored, no git, not a repo — reads as not-ignored,
// so the `...` walk still works outside a repository.
func gitIgnored(path string) bool {
	return exec.Command("git", "check-ignore", "-q", path).Run() == nil
}

func discoverFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var files []string
	for _, arg := range args {
		// Mirrors `go test ./...`, skipped directories included.
		if strings.HasSuffix(arg, "/...") || arg == "..." || arg == "./..." {
			root := strings.TrimSuffix(arg, "/...")
			if root == "" || root == "." {
				root = "."
			}
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if path == root {
						return nil
					}
					if shouldSkipWalkDir(d.Name()) || gitIgnored(path) {
						return filepath.SkipDir
					}
					return nil
				}
				if isSNGLFile(path) && !gitIgnored(path) {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			continue
		}
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

// Mirrors `go test ./...`: testdata, plus anything beginning with `.` or `_`.
func shouldSkipWalkDir(name string) bool {
	if name == "testdata" {
		return true
	}
	if name == "" {
		return false
	}
	c := name[0]
	return c == '.' || c == '_'
}

// A unit is one thing a command works on. Commands differ in what they do with
// one — check it, generate from it, run its tests — and no longer in how they
// decide what one is: every command calls resolveUnits.
//
// They used to each answer that question for themselves, and disagreed.
// `sngl generate` merged a file's siblings, `sngl test` deliberately never did,
// and `sngl check` read every file alone — so the same package's declarations
// were in scope for one command and undefined for the next.
type unit struct {
	// dir is what imports and generated paths resolve against.
	dir string
	// name identifies the unit in diagnostics: the file, or the directory.
	name string
	// files are the .sngl files the command was asked about here, in discovery
	// order. A package reads its whole directory whatever this holds; this is
	// what it reports on.
	files []string
	// solo marks a file read without its siblings: one the command line named.
	solo bool
}

// headline is the file a unit is named after in generated output.
func (u unit) headline() string {
	if len(u.files) > 0 {
		return u.files[0]
	}
	return u.name
}

// doc reads the unit into the single document the checker takes.
func (u unit) doc() (*ast.Document, error) {
	if !u.solo {
		return parseDir(u.dir)
	}
	f, err := os.Open(u.name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSNGL(u.name, f)
}

// resolveUnits turns command-line arguments into the units to work on:
//
//   - a file named on the command line is its own unit — naming it is the
//     request, and a corpus of one-file programs in one directory relies on it;
//   - a directory is a package, and all of its .sngl files are read together.
func resolveUnits(args []string) ([]unit, error) {
	files, err := discoverFiles(args)
	if err != nil {
		return nil, err
	}
	explicit := explicitFileSet(args)

	var grouped []unit
	index := map[string]int{}
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		if explicit[abs] {
			grouped = append(grouped, unit{dir: filepath.Dir(f), name: f, files: []string{f}, solo: true})
			continue
		}
		dir := filepath.Dir(f)
		i, seen := index[dir]
		if !seen {
			i = len(grouped)
			index[dir] = i
			grouped = append(grouped, unit{dir: dir, name: dir})
		}
		grouped[i].files = append(grouped[i].files, f)
	}

	return grouped, nil
}

// A directory is one compilation unit: every .sngl file in it merges into one
// document.
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
		build.MergeInto(doc, sibling)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: no .sngl files found", dir)
	}
	return doc, nil
}

// checkDoc type-checks one unit. The check itself is internal/build's, so
// the CLI and the golden harness put the same package in front of the same
// checker configuration.
func checkDoc(doc *ast.Document, dir string, isMain bool, targets ...ir.StaticTarget) (*ir.Package, error) {
	return build.Check(doc, build.CheckConfig{
		Dir:      dir,
		Resolver: build.NewResolver(dir),
		IsMain:   isMain,
		Targets:  targets,
		Warn:     func(d ir.Diagnostic) { fmt.Fprintln(os.Stderr, "warning: "+d.Error()) },
	})
}

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
