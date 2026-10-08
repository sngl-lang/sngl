// Package markdown registers the md: import scheme, which turns a markdown
// file into a SNGL package holding one component.
package markdown

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"

	"duckfam.us/sngl/codegen"
)

func init() {
	codegen.RegisterFSScheme(&Importer{})
}

// Importer resolves `import doc "md:./getting_started.md"`. The URI is a path
// relative to the importing package's directory; what comes back is a
// synthetic filesystem holding one generated `.sngl` file, which the checker
// then parses and checks like any other package.
//
// A directory, `md:./docs/`, is a site: see convertDir.
//
// An FS importer rather than a native one (the seam `go:` and `file:` use)
// because what a document becomes is SNGL rather than typed declarations: the
// generated package can be dumped and read, which is what makes a bad import
// debuggable and the round-trip assertion possible.
type Importer struct{}

func (m *Importer) Scheme() string { return "md" }

func (m *Importer) ResolveFS(uri, dir string) (fs.FS, error) {
	p, err := resolvePath(uri, dir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return convertDir(os.DirFS(p), uri)
	}
	if err := checkSuffix(uri); err != nil {
		return nil, err
	}
	src, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("md: reading %q: %w", uri, err)
	}
	return convertTo(src, filepath.Base(p))
}

// ResolveProjectFS reads the document through the filesystem the program is
// being checked against. That is the path every ordinary build takes -- the
// markdown is a file of the project, not something this scheme fetches -- and
// it is what lets an in-memory package (a golden archive, the playground)
// import one at all.
func (m *Importer) ResolveProjectFS(uri string, fsys fs.FS, _ string) (fs.FS, error) {
	p, err := fsPath(uri)
	if err != nil {
		return nil, err
	}
	if info, err := fs.Stat(fsys, p); err == nil && info.IsDir() {
		sub, err := fs.Sub(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("md: %q: %w", uri, err)
		}
		return convertDir(sub, uri)
	}
	if err := checkSuffix(uri); err != nil {
		return nil, err
	}
	src, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, fmt.Errorf("md: reading %q: %w", uri, err)
	}
	return convertTo(src, path.Base(p))
}

// The package is one file whatever the document held: what a markdown file
// declares is one component, and a second file would have nothing to hold.
func convertTo(src []byte, name string) (fs.FS, error) {
	out, err := Convert(src, name)
	if err != nil {
		return nil, err
	}
	return fstest.MapFS{"document.sngl": &fstest.MapFile{Data: []byte(out)}}, nil
}

// fsPath turns an import URI into a path an io/fs accepts, refusing one that
// leaves the project -- an io/fs has no way to express a parent of its root,
// so this is the same guard `file:` makes against an absolute path.
func fsPath(uri string) (string, error) {
	if uri == "" {
		return "", fmt.Errorf("md: import needs a path")
	}
	p := path.Clean(uri)
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return "", fmt.Errorf("md: import %q escapes project directory", uri)
	}
	return p, nil
}

// resolvePath joins uri onto dir and refuses anything that leaves it, the
// same guard `file:` applies: an import is part of the project being built.
func resolvePath(uri, dir string) (string, error) {
	if uri == "" {
		return "", fmt.Errorf("md: import needs a path")
	}
	cleanDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("md: resolve: %w", err)
	}
	abs, err := filepath.Abs(filepath.Clean(filepath.Join(dir, uri)))
	if err != nil {
		return "", fmt.Errorf("md: resolve: %w", err)
	}
	if !strings.HasPrefix(abs, cleanDir+string(filepath.Separator)) {
		return "", fmt.Errorf("md: import %q escapes project directory", uri)
	}
	return abs, nil
}

// A markdown file is what this scheme reads, and the extension is the only
// thing that says so before it is parsed -- an `md:` import of a `.sngl` is a
// mistake worth naming rather than a document with no blocks in it.
func checkSuffix(uri string) error {
	switch {
	case uri == "":
		return fmt.Errorf("md: import needs a path")
	case strings.HasSuffix(uri, ".md"), strings.HasSuffix(uri, ".markdown"):
		return nil
	}
	return fmt.Errorf("md: import %q is not a markdown file", uri)
}
