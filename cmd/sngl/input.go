package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A `sngl:<uri>` argument carries the package the checker built, not a
// document for the caller to check: lib source is checked under rules that
// permit the `sngl:internal/` imports a platform package writes, so
// re-checking it as an ordinary document would reject its own imports — and a
// package synthesized from the host (gtk4's widgets, from the installed GIR)
// has no file to re-read at all.
type snglInput struct {
	Path string
	// URI is the library package name — "std", "platform/gtk4" — when Path
	// used the sngl scheme. Empty for a filesystem path.
	URI string
	// Docs, Pkg and Diags are set only for a URI. A caller reporting on the
	// package wants Diags; one merely reading it does not.
	Docs  []*ast.Document
	Pkg   *ir.Package
	Diags []ir.Diagnostic
}

// A non-sngl scheme is rejected here rather than falling through to os.Stat,
// which reported a URI that never was a path as a missing file.
func libraryURI(arg string) (string, error) {
	scheme, uri := checker.ParseScheme(arg)
	switch scheme {
	case "":
		return "", nil
	case "sngl":
		return uri, nil
	default:
		return "", fmt.Errorf("%s: only the sngl scheme names a source package; %q imports a foreign one", arg, scheme)
	}
}

// A nil result means the argument is a filesystem path, not a package.
func resolveInput(arg string) (*snglInput, error) {
	uri, err := libraryURI(arg)
	if err != nil {
		return nil, err
	}
	if uri == "" {
		return nil, nil
	}

	docs := checker.PackageSource(uri)
	pkg, diags := checker.CheckLibPackage(uri)
	if pkg == nil {
		return nil, fmt.Errorf("unknown package %q (have: %s)", arg, strings.Join(checker.Packages(), ", "))
	}
	slog.Info("load", "package", arg, "files", len(docs))
	return &snglInput{Path: arg, URI: uri, Docs: docs, Pkg: pkg, Diags: diags}, nil
}

// Document merges the package's files the way parseDir merges a directory:
// only the first file's imports are kept, since each file's are its own.
func (in *snglInput) Document() *ast.Document {
	doc := &ast.Document{}
	for i, d := range in.Docs {
		if i == 0 {
			doc.Stmts = append(doc.Stmts, d.Stmts...)
			continue
		}
		mergeInto(doc, d)
	}
	return doc
}

func (in *snglInput) Err() error {
	for _, d := range in.Diags {
		if d.Severity == ir.Error {
			return d
		}
	}
	return nil
}

func resolveInputs(args []string) (libs []*snglInput, paths []string, err error) {
	for _, arg := range args {
		in, err := resolveInput(arg)
		if err != nil {
			return nil, nil, err
		}
		if in == nil {
			paths = append(paths, arg)
			continue
		}
		libs = append(libs, in)
	}
	return libs, paths, nil
}

func parsePathInput(target string) (*ast.Document, string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, "", err
	}

	start := time.Now()
	if info.IsDir() {
		doc, err := parseDir(target)
		if err != nil {
			return nil, "", err
		}
		slog.Info("parse", "dir", target, "duration", time.Since(start))
		return doc, target, nil
	}

	f, err := os.Open(target)
	if err != nil {
		return nil, "", err
	}
	doc, err := parseSNGL(target, f)
	f.Close()
	if err != nil {
		return nil, "", err
	}
	slog.Info("parse", "file", target, "duration", time.Since(start))
	return doc, filepath.Dir(target), nil
}
