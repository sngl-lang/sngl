//go:build js

package html

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// emitTestagentFiles is a stub for js/wasm builds where the real
// implementation (with filesystem access for copying pkg/js/testagent
// assets) is excluded by build tag. The html testagent path is
// unreachable from the wasm playground, so this never runs in
// practice — exists only to satisfy the compiler when html.go is
// compiled under js/wasm.
func emitTestagentFiles(_ codegen.Sink, _ *ir.Package) error {
	return nil
}
