package codegen

import "git.duckfam.us/jonathan/sngl/ast"

// PosEntry records that the byte at ByteOffset in the body corresponds to
// the AST source location Pos.
type PosEntry struct {
	ByteOffset int
	Pos        ast.Pos
}

// SourceMapResult is returned by LangTranslator.RenderSourceMap.
//
// If InlineBody is non-nil, the writer uses it in place of the original body
// (e.g. Go splices //line directives into the body).
// If Sidecar is non-nil, the writer opens SidecarName via the Sink and writes
// Sidecar to it.
type SourceMapResult struct {
	InlineBody  []byte
	Sidecar     []byte
	SidecarName string
}
