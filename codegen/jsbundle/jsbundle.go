// Package jsbundle holds the parts of SNGL's esbuild integration that more
// than one caller needs: how a file's extension picks a loader, and how a
// build's messages become one Go error.
//
// The callers differ in the resolver they hand esbuild, which is why only
// these two pieces are shared. The html platform bundles a page from an
// in-memory tree through a plugin, because there is no directory to point at.
// The const evaluator bundles a program that already sits on disk and lets
// esbuild's own resolver find its imports, because that resolver is node's:
// the checker's resolver answers with types — a `.d.ts` describes a module
// without being one — and a program that has to run needs the code.
package jsbundle

import (
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// LoaderFor picks the loader for a path by extension. A `.d.ts` is loaded as
// TypeScript so that a build reaching one gets an empty module rather than a
// syntax error.
func LoaderFor(p string) api.Loader {
	switch {
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".d.ts"):
		return api.LoaderTS
	case strings.HasSuffix(p, ".tsx"):
		return api.LoaderTSX
	case strings.HasSuffix(p, ".jsx"):
		return api.LoaderJSX
	case strings.HasSuffix(p, ".json"):
		return api.LoaderJSON
	}
	return api.LoaderJS
}

// Loaders is the extension-to-loader map for a build that resolves paths
// itself, rather than through a plugin that calls LoaderFor per file.
func Loaders() map[string]api.Loader {
	return map[string]api.Loader{
		".ts":   api.LoaderTS,
		".tsx":  api.LoaderTSX,
		".jsx":  api.LoaderJSX,
		".json": api.LoaderJSON,
	}
}

// Err turns esbuild's messages into one error, keeping the first in full and
// counting the rest. The first message is the one that names what could not be
// resolved or parsed, which is what a person needs to act on.
func Err(errs []api.Message) error {
	if len(errs) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("esbuild: ")
	e := errs[0]
	if e.Location != nil {
		fmt.Fprintf(&b, "%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
	}
	b.WriteString(e.Text)
	if len(errs) > 1 {
		fmt.Fprintf(&b, " (+%d more)", len(errs)-1)
	}
	return fmt.Errorf("%s", b.String())
}
