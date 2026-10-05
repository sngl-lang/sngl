//go:build js

package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/internal/trust"
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

// requestPureNativeFunc always fails in WASM builds: there is no subprocess to
// build or run, so a scheme-import const is unevaluable and the caller's
// existing fallback (abort, or leave the call for a runtime that can make it)
// applies unchanged.
func requestPureNativeFunc(ctx *evalCtx, scheme, importPath string, f *ir.Func, args []any) (ir.Expr, nativeCallState, error) {
	return nil, nativeFailed, fmt.Errorf("compile-time execution not available in WASM")
}

func runNativeRequests(cache *EvalCache, policy *trust.Policy, dir string, types ir.NativeDecls, reqs []*nativeRequest) map[string]error {
	return nil
}
