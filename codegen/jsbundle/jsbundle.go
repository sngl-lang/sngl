// Package jsbundle holds the parts of SNGL's esbuild integration that more
// than one caller needs: how a file's extension picks a loader, and how a
// build's messages become one Go error.
package jsbundle

import (
	"fmt"
	"maps"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// loaders is the one extension-to-loader mapping. A `.d.ts` needs no entry of
// its own: path.Ext reads it as `.ts`, and loading it as TypeScript is what
// makes a build that reaches one produce an empty module rather than a syntax
// error.
var loaders = map[string]api.Loader{
	".ts":   api.LoaderTS,
	".tsx":  api.LoaderTSX,
	".jsx":  api.LoaderJSX,
	".json": api.LoaderJSON,
}

// LoaderFor picks the loader for a path by extension.
func LoaderFor(p string) api.Loader {
	if l, ok := loaders[path.Ext(p)]; ok {
		return l
	}
	return api.LoaderJS
}

// Loaders is the mapping itself, for a build that resolves paths through
// esbuild rather than through a plugin calling LoaderFor per file.
func Loaders() map[string]api.Loader {
	return maps.Clone(loaders)
}

// Err turns esbuild's messages into one error, keeping the first in full and
// counting the rest.
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
