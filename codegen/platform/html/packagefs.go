package html

import (
	"embed"
	"io/fs"
)

// snglsrc is this platform's own library package, `sngl:platform/html`.
//
// It lives here rather than under lib/ because it is this plugin's, not the
// standard library's: a platform is the thing that knows what it declares, and
// the checker asks each registered one for its source rather than reading a
// directory whose layout it has to agree with. That is what lets a plugin
// outside this repository -- eventually one answering over an RPC rather than
// from an embed -- deliver a package the same way, and it is why gtk4 can serve
// declarations it generates from the host's introspection data without the
// checker knowing that is unusual.
//
//go:embed *.sngl
var snglsrc embed.FS

// PackageFS implements the interface the checker reads a target's own library
// package through.
func (g *Generator) PackageFS() fs.FS { return snglsrc }
