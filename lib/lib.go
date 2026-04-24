// Package lib embeds the SNGL standard library source files so they can be
// read at runtime without depending on the filesystem layout.
package lib

import "embed"

//go:embed *.sngl
var FS embed.FS
