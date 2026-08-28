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

// A positional argument names either a place on disk or a package, and the two
// do not reach the pipeline the same way. A path is parsed into a document the
// caller then checks; `sngl://<uri>` names a library package the checker builds
// itself, memoized, under the lib-source rules that permit the
// `sngl://internal/` imports a platform package writes. Re-checking that source
// as an ordinary document would reject its own imports, and for a package whose
// declarations are synthesized from the host (gtk4's widgets, from the
// installed GIR) there is no file to re-read in the first place.
//
// So the resolved form carries the built package rather than a document for the
// caller to check, and the commands agree on one resolver instead of each
// learning about schemes separately.

// snglInput is a resolved positional argument.
type snglInput struct {
	// Path is the argument as written, for diagnostics.
	Path string
	// URI is the library package name — "std", "platforms/gtk4" — when Path
	// used the sngl scheme. Empty for a filesystem path.
	URI string
	// Docs is the package's source: what lib/ embeds plus what a target
	// synthesizes. Only set for a URI.
	Docs []*ast.Document
	// Pkg is the loaded package, only set for a URI. Its diagnostics are in
	// Diags — a caller reporting on the package wants them, one reading it
	// does not.
	Pkg   *ir.Package
	Diags []ir.Diagnostic
}

// libraryURI reports the library package a positional argument names, if any.
// A non-sngl scheme is rejected here rather than silently falling through to
// os.Stat, which is how it used to surface ("no such file or directory" for a
// URI that never was a path).
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

// resolveInput resolves a positional argument to either a library package or
// nothing, in which case the argument is a filesystem path and the caller
// handles it as before.
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

// Document merges the package's files into one, the way parseDir merges a
// directory: same package, so the declarations belong together, and only the
// first file's imports are kept because each file's are its own.
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

// Err returns the first error diagnostic of the package's load, or nil.
func (in *snglInput) Err() error {
	for _, d := range in.Diags {
		if d.Severity == ir.Error {
			return d
		}
	}
	return nil
}

// resolveInputs resolves every argument, splitting them into the library
// packages and the leftover filesystem paths. Commands that take many
// arguments (check, fmt, generate) mix the two freely.
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

// parsePathInput parses a filesystem path — a directory as a package, a file
// with no siblings merged — and returns it with the directory its imports
// resolve against.
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
