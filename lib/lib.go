// Package lib embeds the SNGL standard library source so it can be read at
// runtime without depending on the filesystem layout.
//
// Each subdirectory is one importable package: lib/<path> is `sngl://<path>`.
// Nothing enumerates them in Go — add a directory and it exists.
//
//	builtin/  ambient built-ins, dot-imported into every file implicitly
//	std/      the standard library, brought in by an explicit import
package lib

import (
	"embed"
	"sort"
)

//go:embed builtin/*.sngl std/*.sngl
var FS embed.FS

// Packages returns the embedded package paths, sorted. "builtin" sorting ahead
// of "std" also happens to be load order, though loadStdlib groups
// declarations by kind before registering them, so file order is not
// significant.
func Packages() []string {
	entries, err := FS.ReadDir(".")
	if err != nil {
		panic("sngl: reading embedded stdlib root: " + err.Error())
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
