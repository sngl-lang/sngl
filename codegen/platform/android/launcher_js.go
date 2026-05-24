//go:build js

package android

// findTestAgentPath is a stub for js/wasm builds where the real
// launcher (with exec/network deps) is excluded by build tag. Android
// codegen isn't reachable from the wasm playground, so this never
// runs in practice — it exists only to satisfy the compiler when
// android.go is compiled under js/wasm.
func findTestAgentPath() (string, error) {
	return "", nil
}
