package none

import (
	"embed"
	"io/fs"
)

// snglsrc is this language's own library package, `sngl:language/none`. Every
// target carries one, so what a target accepts is a declaration rather than a
// silence; see codegen/lang/golang/packagefs.go.
//
//go:embed *.sngl
var snglsrc embed.FS

// PackageFS implements the interface the checker reads a target's own library
// package through.
func (t *Translator) PackageFS() fs.FS { return snglsrc }
