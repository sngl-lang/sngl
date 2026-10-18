package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/kotlin"
)

// codecKt is SnglCodec's source, the runtime behind sngl:json and sngl:http,
// written beside MainScreen.kt in the program's package when the program
// reaches either.
//
//go:embed codec.kt
var codecKt string

func emitCodecFile(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	if !kotlin.UsesCodec(req.Pkg) {
		return nil
	}
	src := strings.Replace(codecKt, "package PACKAGE", "package "+pkg, 1)
	return writeAndroidFile(sink, path.Join(path.Dir(mainScreen), "SnglCodec.kt"), []byte(src))
}
