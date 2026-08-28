package golang

import (
	"embed"
	"io/fs"
)

// snglsrc is this language's own library package, `sngl://languages/go`. A
// language declares its foreign-type surface the way a platform declares its
// widgets; see codegen/platform/html/packagefs.go.
//
//go:embed *.sngl
var snglsrc embed.FS

// PackageFS implements the interface the checker reads a target's own library
// package through.
func (t *Translator) PackageFS() fs.FS { return snglsrc }
