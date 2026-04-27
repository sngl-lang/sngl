// Package node implements the `node://` import scheme. It resolves
// JavaScript / TypeScript modules using Node-style module resolution
// (node_modules walk, package.json main/exports/types, directory
// index.* lookup) and bridges their declared types into SNGL's IR via
// github.com/sngl-lang/typescript-go.
//
// Three URI shapes:
//
//	node://foo          — node_modules: foo/package.json#types|main
//	node://./foo        — directory: ./foo/index.{ts,tsx,d.ts,js,mjs,cjs}
//	node://./foo.json   — JSON literal: shape-infer the parsed object
package node

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterScheme(&NodeImporter{})
}

// NodeImporter resolves node:// scheme imports.
type NodeImporter struct{}

func (n *NodeImporter) Scheme() string { return "node" }

func (n *NodeImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	spec := strings.TrimPrefix(uri, "node://")
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("node scheme requires a module path (e.g. node://lodash or node://./foo)")
	}

	resolved, err := resolveNodeSpec(spec, dir)
	if err != nil {
		return nil, fmt.Errorf("resolving node://%s: %w", spec, err)
	}

	if strings.HasSuffix(resolved.path, ".json") {
		return loadJSON(spec, resolved.path)
	}
	return loadTypeScript(spec, resolved)
}
