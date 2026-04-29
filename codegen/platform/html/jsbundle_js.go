//go:build js

package html

import "git.duckfam.us/jonathan/sngl/ir"

// bundleNativeScript stub for WASM builds (internal/playground). The
// playground doesn't run js:// scenarios, so passing the entry through
// untouched is acceptable — and keeps esbuild out of the JS/WASM build
// graph.
func bundleNativeScript(entry, _ string) (string, error) {
	return entry, nil
}

// collectBundledNativePkgs stub: under js/wasm builds we never bundle
// js:// imports, so no package is marked as bundled.
func collectBundledNativePkgs(_ *ir.Package) map[string]bool {
	return nil
}
