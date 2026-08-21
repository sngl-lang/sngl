// Package lib embeds the SNGL standard library source so it can be read at
// runtime without depending on the filesystem layout.
//
// Each subdirectory is one importable package: lib/<path> is `sngl://<path>`.
// Nothing enumerates them in Go — add a directory and it exists.
//
//	builtin/  ambient built-ins, dot-imported into every file implicitly
//	draw/     2D shapes and the canvas, plus the `shape` mark
//	std/      the standard library, brought in by an explicit import
package lib

import (
	"embed"
	"slices"
	"sort"
)

//go:embed builtin/*.sngl draw/*.sngl std/*.sngl
var FS embed.FS

// Packages returns the embedded package paths, sorted. Order is not load
// order: packages import each other and are loaded on demand.
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

// HasPackage reports whether name is an embedded package directory.
func HasPackage(name string) bool {
	return slices.Contains(Packages(), name)
}
