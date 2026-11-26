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

// grpcKt is SnglGrpc's source, the transport behind sngl:grpc, and the
// protobuf writer and reader SnglProto's helpers are written over.
//
//go:embed grpc.kt
var grpcKt string

func emitCodecFile(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	if !kotlin.UsesCodec(req.Pkg) {
		return nil
	}
	dir := path.Dir(mainScreen)
	src := strings.Replace(codecKt, "package PACKAGE", "package "+pkg, 1)
	if err := writeAndroidFile(sink, path.Join(dir, "SnglCodec.kt"), []byte(src)); err != nil {
		return err
	}
	if !kotlin.UsesGRPC(req.Pkg) {
		return nil
	}
	src = strings.Replace(grpcKt, "package PACKAGE", "package "+pkg, 1)
	if err := writeAndroidFile(sink, path.Join(dir, "SnglGrpc.kt"), []byte(src)); err != nil {
		return err
	}
	return writeAndroidFile(sink, path.Join(dir, "SnglProto.kt"), []byte(kotlin.ProtoHelpers(req.Pkg, pkg)))
}
