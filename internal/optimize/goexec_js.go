//go:build js

package optimize

import "fmt"

// execPureGoFunc is a no-op in WASM builds.
func execPureGoFunc(dir, importPath, nativeType string, paramTypes []string, returnType string, args []any) (any, error) {
	return nil, fmt.Errorf("compile-time Go execution not available in WASM")
}
