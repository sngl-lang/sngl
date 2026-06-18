//go:build js

package html

import "fmt"

// SnapshotHTML is a stub for js/wasm builds where the rod-based browser
// machinery is excluded by build tag. Snapshotting is never reachable from the
// wasm playground; this exists only to satisfy the compiler.
func (g *Generator) SnapshotHTML(_ []byte, _, _ int) ([]byte, error) {
	return nil, fmt.Errorf("SnapshotHTML not supported in js/wasm builds")
}
