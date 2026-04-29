//go:build js

package html

import (
	"io/fs"

	"git.duckfam.us/jonathan/sngl/ir"
)

// bundleNativeScript stub for WASM builds (internal/playground). The
// playground doesn't run a JS bundler in-browser yet — pass the entry
// through untouched so esbuild stays out of the JS/WASM build graph.
func bundleNativeScript(entry string, _ fs.FS, _ string) (string, error) {
	return entry, nil
}

// collectBundledNativePkgs stub: under js/wasm builds we never bundle
// js:// imports, so no package is marked as bundled.
func collectBundledNativePkgs(_ *ir.Package) map[string]bool {
	return nil
}
