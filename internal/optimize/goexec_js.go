//go:build js

package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// nativeCallState mirrors the non-WASM build; only nativeFailed occurs here.
type nativeCallState int

const (
	nativeReady nativeCallState = iota
	nativePending
	nativeFailed
)

// nativeRequest is never constructed in WASM builds: nothing can be run.
type nativeRequest struct{ nativeType string }

type nativeEval struct{ order []*nativeRequest }

// requestPureGoFunc always fails in WASM builds: there is no subprocess to
// build or run, so a go:// const is unevaluable and the caller's existing
// fallback (abort, or leave the call for a Go runtime) applies unchanged.
func requestPureGoFunc(ctx *evalCtx, scheme, importPath string, f *ir.Func, args []any) (ir.Expr, nativeCallState, error) {
	return nil, nativeFailed, fmt.Errorf("compile-time Go execution not available in WASM")
}

func runNativeRequests(cache *EvalCache, dir string, reqs []*nativeRequest) error { return nil }
