// Package js implements the `js://` import scheme. It resolves
// JavaScript / TypeScript modules using Node-style module resolution
// (node_modules walk, package.json main/exports/types, directory
// index.* lookup) and bridges their declared types into SNGL's IR via
// github.com/sngl-lang/typescript-go.
//
// Three URI shapes:
//
//	js://foo          — node_modules: foo/package.json#types|main
//	js://./foo        — directory: ./foo/index.{ts,tsx,d.ts,js,mjs,cjs}
//	js://./foo.json   — JSON literal: shape-infer the parsed object
package js

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterScheme(&JSImporter{})
}

// JSImporter resolves js:// scheme imports.
type JSImporter struct{}

func (j *JSImporter) Scheme() string { return "js" }

func (j *JSImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	spec := strings.TrimPrefix(uri, "js://")
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("js scheme requires a module path (e.g. js://lodash or js://./foo)")
	}

	resolved, err := resolveJSSpec(spec, dir)
	if err != nil {
		return nil, fmt.Errorf("resolving js://%s: %w", spec, err)
	}

	if strings.HasSuffix(resolved.path, ".json") {
		return loadJSON(spec, resolved.path)
	}
	return loadTypeScript(spec, resolved)
}
