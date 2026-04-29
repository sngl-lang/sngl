// Package js implements the `js://` import scheme. It resolves
// JavaScript / TypeScript modules using typescript-go's module
// resolver, served over an io/fs.FS so the same code path works for
// the on-disk CLI build and the in-memory playground.
//
// Three URI shapes:
//
//	js://foo          — node_modules: foo/package.json#types|main
//	js://./foo        — directory: ./foo/index.{ts,tsx,d.ts,js,mjs,cjs}
//	js://./foo.json   — JSON literal: shape-infer the parsed object
package js

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/sngl-lang/typescript-go/snglts"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterScheme(&JSImporter{})
}

// JSImporter resolves js:// scheme imports.
type JSImporter struct{}

// VirtualRoot is the synthetic absolute root used for typescript-go's
// path-bookkeeping when feeding it an io/fs.FS. The resolver works in
// absolute paths; io/fs is unrooted; we pick a fixed string here.
// Exposed so the html platform's esbuild plugin can mount its FS at
// the same root and stay consistent with the checker.
const VirtualRoot = "/sngl"

func (j *JSImporter) Scheme() string { return "js" }

// Resolve provides the legacy OS-rooted path. New callers should prefer
// ResolveFS so behaviour is identical between CLI and playground.
func (j *JSImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	return j.ResolveFS(uri, os.DirFS(dir), dir)
}

// ResolveFS resolves a js:// import against the provided io/fs.FS using
// typescript-go's real module resolver. dir is the OS directory the FS
// was rooted at (or "" for in-memory FS) and is plumbed through purely
// for diagnostic messages — resolution itself only consults fsys.
func (j *JSImporter) ResolveFS(uri string, fsys fs.FS, _ string) (*ir.NativeImport, error) {
	spec := strings.TrimSpace(strings.TrimPrefix(uri, "js://"))
	if spec == "" {
		return nil, fmt.Errorf("js scheme requires a module path (e.g. js://lodash or js://./foo)")
	}

	containing := path.Join(VirtualRoot, "__sngl_entry__.ts")
	abs, err := ResolveSpec(fsys, VirtualRoot, spec, containing)
	if err != nil {
		return nil, fmt.Errorf("resolving js://%s: %w", spec, err)
	}

	rel, ok := StripVirtRoot(abs, VirtualRoot)
	if !ok {
		return nil, fmt.Errorf("resolving js://%s: resolver returned out-of-root path %q", spec, abs)
	}

	if strings.HasSuffix(rel, ".json") {
		return loadJSON(spec, abs, rel, fsys)
	}
	return loadTypeScript(spec, abs, rel, fsys)
}

// ResolveSpec uses typescript-go's module resolver to turn a bare or
// relative module specifier into an absolute virtual path under root,
// served by fsys. containingFile is the resolved absolute path of the
// importer (or a synthesized entry) that relative resolution anchors
// against.
//
// Exposed for the html platform's esbuild plugin so the bundler shares
// the checker's resolver — `./lib` means the same thing in both places.
func ResolveSpec(fsys fs.FS, root, spec, containingFile string) (string, error) {
	adapter := newVFSAdapter(fsys, root)
	h := &host{fsys: adapter, cwd: root}
	r := snglts.NewResolver(h, &snglts.CompilerOptions{}, "", "")
	res, _ := r.ResolveModuleName(spec, containingFile, snglts.ResolutionModeNone, nil)
	if res == nil || res.ResolvedFileName == "" {
		return "", fmt.Errorf("not found: %s", spec)
	}
	return res.ResolvedFileName, nil
}

// StripVirtRoot turns an absolute virtual path under root into the
// matching io/fs.FS-relative slash path. Returns ("", false) for paths
// outside root.
func StripVirtRoot(p, root string) (string, bool) {
	a := newVFSAdapter(nil, root)
	return a.strip(p)
}
