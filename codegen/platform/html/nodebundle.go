//go:build !js

package html

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/evanw/esbuild/pkg/api"
)

// collectBundledNativePkgs returns the set of native package paths whose
// calls should be routed through the esbuild bundle (instead of the
// WASM-extern bridge). Today: every `node://` import.
func collectBundledNativePkgs(pkg *ir.Package) map[string]bool {
	if pkg == nil {
		return nil
	}
	out := map[string]bool{}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, _ := codegen.SplitScheme(imp.AST.Path)
		if scheme != "node" {
			continue
		}
		if imp.Native.ImportPath != "" {
			out[imp.Native.ImportPath] = true
		}
	}
	return out
}

// bundleNativeScript runs the rendered <script> body — which contains real
// `import * as ... from "..."` statements at the top — through esbuild and
// returns a single IIFE suitable for inlining inside <script>. Tree-shaking
// follows the namespace member access in the script body, so unused exports
// from imported `node://` modules are dropped.
//
// projectDir is used as esbuild's resolve dir for the synthesized stdin
// entry, so relative specifiers (`./lib`) resolve against the user's
// project root.
func bundleNativeScript(entry, projectDir string) (string, error) {
	res := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   entry,
			ResolveDir: projectDir,
			Sourcefile: "sngl-entry.js",
			Loader:     api.LoaderJS,
		},
		Bundle:   true,
		Write:    false,
		Format:   api.FormatIIFE,
		Platform: api.PlatformBrowser,
		Target:   api.ES2020,
		Loader: map[string]api.Loader{
			".ts":   api.LoaderTS,
			".tsx":  api.LoaderTSX,
			".json": api.LoaderJSON,
		},
		LogLevel: api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		var b strings.Builder
		b.WriteString("esbuild: ")
		e := res.Errors[0]
		if e.Location != nil {
			fmt.Fprintf(&b, "%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
		}
		b.WriteString(e.Text)
		if len(res.Errors) > 1 {
			fmt.Fprintf(&b, " (+%d more)", len(res.Errors)-1)
		}
		return "", fmt.Errorf("%s", b.String())
	}
	if len(res.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild: no output")
	}
	return string(res.OutputFiles[0].Contents), nil
}
