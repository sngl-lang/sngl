package fyne

import (
	"embed"
	"io/fs"
)

// snglsrc is this platform's own library package, `sngl:platform/fyne`.
// See codegen/platform/html/packagefs.go for why a plugin carries its own
// source rather than the standard library holding it.
//
//go:embed *.sngl
var snglsrc embed.FS

// PackageFS implements the interface the checker reads a target's own library
// package through.
func (g *Generator) PackageFS() fs.FS { return snglsrc }
