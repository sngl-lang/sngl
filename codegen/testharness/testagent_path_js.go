//go:build js

package testharness

// LangTestagentPath is a stub for js/wasm builds where the real
// implementation (with filesystem walk + env discovery) is excluded
// by build tag. Test launchers aren't reachable from the wasm
// playground, so this never runs in practice — exists only to satisfy
// the compiler.
func LangTestagentPath(_ string) (string, error) {
	return "", nil
}
