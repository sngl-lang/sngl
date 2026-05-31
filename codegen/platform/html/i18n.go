package html

import (
	"fmt"
	"io/fs"
	"sync"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"

	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html/i18nruntime"
	"git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	"git.duckfam.us/jonathan/sngl/ir"
)

// hasI18nCalls reports whether the IR package contains any call to an i18n
// intrinsic (i18n.tr, i18n.format, i18n.numberInt, etc.). When true the html
// platform must inject the JS i18n runtime into the generated bundle.
func hasI18nCalls(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}

	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		c, ok := e.(*ir.Call)
		if !ok {
			return false
		}
		if snglI18n.IsCall(c) {
			found = true
			return true // signal stop
		}
		return false
	})
	return found
}

// i18nRuntimeSnippet returns a self-contained JS snippet that declares a
// module-scoped `const i18n` object exposing `getTranslator` (and
// `_resetTranslator` for tests). The snippet is produced by bundling
// i18n.js+locale_currency.js through esbuild with GlobalName="i18n" so that
// the result is a single IIFE assignment `var i18n = (()=>{...})()`. The
// bundled snippet is cached after the first call.
//
// The snippet is inlined at the top of the emitted <script> block so that
// the generated `i18n.getTranslator().tr(...)` calls resolve without
// requiring the main script to go through a separate esbuild pass.
func i18nRuntimeSnippet() (string, error) {
	i18nSnippetOnce.Do(func() {
		runtimeFS := fstest.MapFS{
			"i18n.js":            &fstest.MapFile{Data: []byte(i18nruntime.I18nJS)},
			"locale_currency.js": &fstest.MapFile{Data: []byte(i18nruntime.LocaleCurrencyJS)},
		}
		res := api.Build(api.BuildOptions{
			Stdin: &api.StdinOptions{
				// Re-export getTranslator (used by i18n.getTranslator().tr(...)
				// calls) plus the direct formatters that the javascript
				// translator emits as i18n.<fn>(...) for `$"..."` interpolation
				// and direct i18n.* calls (codegen/lang/javascript/javascript.go).
				// Without these, e.g. i18n.translate is tree-shaken out of the
				// bundle and a $"...{plural}..." in a handler throws at runtime.
				// (i18n.js exports `select` via `export { _selectImpl as select }`,
				// so the bare name resolves here.)
				Contents:   "export { getTranslator, _resetTranslator, Translator, defaultLocale, translate, format, numberInt, numberFloat, date, time, datetime, plural, selectordinal, select } from \"./i18n.js\";\n",
				ResolveDir: js.VirtualRoot,
				Sourcefile: "i18n-entry.js",
				Loader:     api.LoaderJS,
			},
			Bundle:     true,
			Write:      false,
			Format:     api.FormatIIFE,
			GlobalName: "i18n",
			Platform:   api.PlatformBrowser,
			Target:     api.ES2020,
			Plugins:    []api.Plugin{virtFSPlugin(runtimeFS, js.VirtualRoot)},
			LogLevel:   api.LogLevelWarning,
		})
		if err := esbuildBuildErr(res.Errors); err != nil {
			i18nSnippetErr = err
			return
		}
		if len(res.OutputFiles) == 0 {
			i18nSnippetErr = fmt.Errorf("esbuild i18n bundle: no output")
			return
		}
		i18nSnippetVal = string(res.OutputFiles[0].Contents)
	})
	return i18nSnippetVal, i18nSnippetErr
}

var (
	i18nSnippetOnce sync.Once
	i18nSnippetVal  string
	i18nSnippetErr  error
)

// i18nManifestJS returns the JS statement that initialises
// globalThis.__SNGL_I18N_MANIFEST__, or "" if no manifest is present.
// The returned string includes a trailing newline. Reads via the
// shared codegen/i18n loader so html and the Go-desktop platforms
// resolve the same project-root manifest the same way.
func i18nManifestJS(projectDir string, projectFS fs.FS) string {
	compact, err := snglI18n.LoadManifest(projectFS, projectDir)
	if err != nil || compact == nil {
		// Malformed manifest or no manifest: skip silently; the
		// runtime defaults to empty.
		return ""
	}
	return fmt.Sprintf("globalThis.__SNGL_I18N_MANIFEST__ = %s;\n", compact)
}
