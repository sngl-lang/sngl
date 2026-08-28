package android

import (
	"embed"
	"io/fs"
)

// sngdir is this platform's own library package, `sngl://platforms/android`.
// See codegen/platform/html/packagefs.go for why a plugin carries its own
// source rather than the standard library holding it.
//
//go:embed sngl/*.sngl
var sngdir embed.FS

// PackageFS implements the interface the checker reads a target's own library
// package through.
func (g *Generator) PackageFS() fs.FS {
	sub, err := fs.Sub(sngdir, "sngl")
	if err != nil {
		return nil
	}
	return sub
}
