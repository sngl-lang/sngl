package html

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/codegen/scheme/js"
)

// bundleNativeScript runs the rendered <script> body — which contains real
// `import * as ... from "..."` statements at the top — through esbuild and
// returns a single IIFE suitable for inlining inside <script>. Every import
// flows through the plugin against js.VirtualRoot mounted on fsys, so the
// same path serves CLI (os.DirFS) and any in-memory FS. When minify is true,
// esbuild's whitespace/identifier/syntax minification is enabled.
//
// When maps is true and the entry script contains inline SNGL position
// markers (emitted by JsIRContext.EmitPositionMarkers), the markers are
// extracted into a SNGL→JS source map, stripped from the entry, and the
// stripped entry is given an inline `//# sourceMappingURL=data:...` so
// esbuild chains the SNGL→JS map through its own JS→bundled output map.
// Esbuild's final output carries an inline SNGL→bundled source map.
func bundleNativeScript(entry string, fsys fs.FS, minify, maps bool) (string, error) {
	if maps {
		strippedEntry, mapURL, err := preBundleSourceMap(entry)
		if err == nil && mapURL != "" {
			entry = strippedEntry + "\n//# sourceMappingURL=" + mapURL + "\n"
		}
	}
	sourcemapOpt := api.SourceMapNone
	if maps {
		sourcemapOpt = api.SourceMapInline
	}
	res := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   entry,
			ResolveDir: js.VirtualRoot,
			Sourcefile: "sngl-entry.js",
			Loader:     api.LoaderJS,
		},
		Bundle:    true,
		Write:     false,
		Format:    api.FormatIIFE,
		Platform:  api.PlatformBrowser,
		Target:    api.ES2020,
		Sourcemap: sourcemapOpt,
		Loader: map[string]api.Loader{
			".ts":   api.LoaderTS,
			".tsx":  api.LoaderTSX,
			".json": api.LoaderJSON,
		},
		MinifyWhitespace:  minify,
		MinifyIdentifiers: minify,
		MinifySyntax:      minify,
		// Without minify, keep unused state/helpers in place so the emitted
		// script matches the SNGL source. Tree-shake only under minify.
		TreeShaking: ternaryTreeShaking(minify),
		Plugins:     []api.Plugin{virtFSPlugin(fsys, js.VirtualRoot)},
		LogLevel:    api.LogLevelWarning,
	})
	if err := esbuildBuildErr(res.Errors); err != nil {
		return "", err
	}
	if len(res.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild: no output")
	}
	return string(res.OutputFiles[0].Contents), nil
}

// preBundleSourceMap extracts SNGL position markers from entry, builds a
// SNGL→JS source-map v3 document, base64-encodes it as a data URL, and
// returns the marker-stripped entry plus the data URL. Returns ("", "", nil)
// when entry has no markers.
func preBundleSourceMap(entry string) (stripped, dataURL string, err error) {
	res := javascript.RenderInlineSourceMap("sngl-entry.js", []byte(entry))
	if res.Sidecar == nil {
		return "", "", nil
	}
	encoded := base64.StdEncoding.EncodeToString(res.Sidecar)
	// res.InlineBody already ends in `//# sourceMappingURL=sngl-entry.js.map\n`
	// from renderJSSourceMap; strip that since we're inlining via data URL.
	body := stripTrailingSourceMapURL(res.InlineBody)
	return string(body), "data:application/json;base64," + encoded, nil
}

// inlineSourceMapFromMarkers extracts SNGL position markers from a JS
// body, builds a SNGL→JS source map, and appends an inline
// `//# sourceMappingURL=data:...` referencing it. Returns the input
// unchanged when no markers are present.
func inlineSourceMapFromMarkers(script string) string {
	stripped, dataURL, _ := preBundleSourceMap(script)
	if dataURL == "" {
		return script
	}
	if !strings.HasSuffix(stripped, "\n") {
		stripped += "\n"
	}
	return stripped + "//# sourceMappingURL=" + dataURL + "\n"
}

// stripTrailingSourceMapURL removes a `//# sourceMappingURL=...` line from
// the end of b (with optional trailing newline). renderJSSourceMap appends
// such a line referencing a sidecar file; for inline-bundling we replace
// that reference with a base64 data URL appended by bundleNativeScript.
func stripTrailingSourceMapURL(b []byte) []byte {
	s := string(b)
	idx := strings.LastIndex(s, "\n//# sourceMappingURL=")
	if idx < 0 {
		return b
	}
	return []byte(s[:idx])
}

// maybeMinifyCSS returns src verbatim when minify is false, or the esbuild-
// minified form otherwise. Used at <style> block call sites so the branching
// stays out of the renderer.
func maybeMinifyCSS(src string, minify bool) (string, error) {
	if !minify {
		return src, nil
	}
	return minifyCSS(src)
}

// minifyCSS runs a CSS string through esbuild's Transform with minify flags
// enabled. Used for inline <style> block bodies under minify=true.
func minifyCSS(src string) (string, error) {
	res := api.Transform(src, api.TransformOptions{
		Loader:            api.LoaderCSS,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LogLevel:          api.LogLevelWarning,
	})
	if err := esbuildBuildErr(res.Errors); err != nil {
		return "", err
	}
	return string(res.Code), nil
}

func ternaryTreeShaking(on bool) api.TreeShaking {
	if on {
		return api.TreeShakingTrue
	}
	return api.TreeShakingFalse
}

func esbuildBuildErr(errs []api.Message) error {
	if len(errs) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("esbuild: ")
	e := errs[0]
	if e.Location != nil {
		fmt.Fprintf(&b, "%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
	}
	b.WriteString(e.Text)
	if len(errs) > 1 {
		fmt.Fprintf(&b, " (+%d more)", len(errs)-1)
	}
	return fmt.Errorf("%s", b.String())
}

// virtFSPlugin builds an esbuild plugin that resolves every import against
// fsys instead of the OS. virtRoot is the synthetic absolute path under
// which fsys is mounted; resolved paths returned to esbuild are all under
// it so the runtime cache and Importer chains stay self-consistent.
func virtFSPlugin(fsys fs.FS, virtRoot string) api.Plugin {
	const namespace = "sngl-virt"
	return api.Plugin{
		Name: "sngl-virtfs",
		Setup: func(pb api.PluginBuild) {
			pb.OnResolve(api.OnResolveOptions{Filter: ".*"},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					// Importer carries the resolved virtual path of the
					// file doing the import. Stdin entry has no importer;
					// anchor against virtRoot's synthesized entry.
					containing := args.Importer
					if containing == "" || args.Namespace == "" {
						containing = path.Join(virtRoot, "sngl-entry.js")
					}
					resolved, err := js.ResolveSpec(fsys, virtRoot, args.Path, containing)
					if err != nil {
						return api.OnResolveResult{}, err
					}
					return api.OnResolveResult{Path: resolved, Namespace: namespace}, nil
				})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: namespace},
				func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					rel, ok := js.StripVirtRoot(args.Path, virtRoot)
					if !ok {
						return api.OnLoadResult{}, fmt.Errorf("virtFS: path outside root: %s", args.Path)
					}
					data, err := fs.ReadFile(fsys, rel)
					if err != nil {
						return api.OnLoadResult{}, err
					}
					contents := string(data)
					loader := loaderFor(args.Path)
					return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
				})
		},
	}
}

func loaderFor(p string) api.Loader {
	switch {
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".d.ts"):
		return api.LoaderTS
	case strings.HasSuffix(p, ".tsx"):
		return api.LoaderTSX
	case strings.HasSuffix(p, ".jsx"):
		return api.LoaderJSX
	case strings.HasSuffix(p, ".json"):
		return api.LoaderJSON
	}
	return api.LoaderJS
}
