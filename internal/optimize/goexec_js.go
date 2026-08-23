//go:build js

package optimize

import "fmt"

// execPureGoFunc is a no-op in WASM builds.
func execPureGoFunc(ctx *evalCtx, importPath, nativeType string, args []any) (any, error) {
	return nil, fmt.Errorf("compile-time Go execution not available in WASM")
}

// CloseEvaluators is a no-op in WASM builds: nothing was ever started.
func CloseEvaluators() {}
