package html

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"

	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
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
	walkPkgExprs(pkg, func(e ir.Expr) bool {
		c, ok := e.(*ir.Call)
		if !ok || c.Func == nil {
			return false
		}
		qual := c.Func.Receiver + "." + c.Func.Name
		if javascript.IsI18nCall(qual) || javascript.IsIntlIntrinsic(c.Func.Intrinsic) {
			found = true
			return true // signal stop
		}
		return false
	})
	return found
}

// walkPkgExprs visits every expression in the IR package, calling fn for each.
// fn returns true to request an early exit. This is a standalone, minimal
// walker that covers the node kinds present in compiled SNGL IR.
func walkPkgExprs(pkg *ir.Package, fn func(ir.Expr) bool) {
	done := false

	var visitExpr func(e ir.Expr)
	var visitStmts func(stmts []ir.Stmt)
	var visitFunc func(f *ir.Func)
	var visitVar func(v *ir.Var)

	visitExpr = func(e ir.Expr) {
		if done || e == nil {
			return
		}
		if fn(e) {
			done = true
			return
		}
		switch x := e.(type) {
		case *ir.Binary:
			visitExpr(x.Left)
			visitExpr(x.Right)
		case *ir.Unary:
			visitExpr(x.Operand)
		case *ir.Ternary:
			visitExpr(x.Cond)
			visitExpr(x.Then)
			visitExpr(x.Else)
		case *ir.Call:
			visitExpr(x.Receiver)
			for _, a := range x.Args {
				visitExpr(a.Value)
			}
		case *ir.Conversion:
			visitExpr(x.Operand)
		case *ir.Select:
			visitExpr(x.Operand)
		case *ir.Index:
			visitExpr(x.Operand)
			visitExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				visitExpr(el)
			}
		case *ir.MapLitIR:
			for _, kv := range x.Entries {
				visitExpr(kv.Key)
				visitExpr(kv.Value)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				visitExpr(f.Value)
			}
		case *ir.Lambda:
			if x.Func != nil {
				visitFunc(x.Func)
			}
		case *ir.Spread:
			visitExpr(x.Operand)
		}
	}

	visitStmt := func(s ir.Stmt) {
		if done || s == nil {
			return
		}
		switch n := s.(type) {
		case *ir.NodeInst:
			for _, p := range n.Props {
				visitExpr(p.Value)
			}
			for _, h := range n.Handlers {
				visitFunc(h.Func)
			}
			visitStmts(n.Children)
		case *ir.CallStmt:
			if n.Call != nil {
				if fn(n.Call) {
					done = true
					return
				}
				visitExpr(n.Call.Receiver)
				for _, a := range n.Call.Args {
					visitExpr(a.Value)
				}
			}
		case *ir.Assign:
			visitExpr(n.Target)
			visitExpr(n.Value)
		case *ir.Toggle:
			visitExpr(n.Target)
		case *ir.Emit:
			for _, a := range n.Args {
				visitExpr(a.Value)
			}
		case *ir.LocalVar:
			visitExpr(n.Init)
		case *ir.Return:
			visitExpr(n.Value)
		case *ir.If:
			visitExpr(n.Cond)
			visitStmts(n.Body)
			visitStmts(n.Else)
		case *ir.For:
			visitExpr(n.Iter)
			visitStmts(n.Body)
			visitStmts(n.Else)
		case *ir.SlotInst:
			visitStmts(n.Children)
		case *ir.PlatformFilter:
			visitStmts(n.Body)
		case *ir.ErrorBoundary:
			visitStmts(n.Children)
		case *ir.Window:
			visitExpr(n.Href)
			visitExpr(n.Title)
			visitExpr(n.Favicon)
			for _, v := range n.Vars {
				visitVar(v)
			}
			for _, f := range n.Funcs {
				visitFunc(f)
			}
			visitStmts(n.Body)
		}
	}

	visitStmts = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if done {
				return
			}
			visitStmt(s)
		}
	}

	visitFunc = func(f *ir.Func) {
		if done || f == nil {
			return
		}
		for _, p := range f.Params {
			if p.Default != nil {
				visitExpr(p.Default)
			}
		}
		visitStmts(f.Block)
	}

	visitVar = func(v *ir.Var) {
		if done || v == nil {
			return
		}
		visitExpr(v.Init)
		for _, h := range v.Handlers {
			visitFunc(h.Func)
		}
	}

	// Walk all roots: consts, vars (with handlers), funcs, components, timers, windows.
	for _, v := range pkg.Consts {
		visitExpr(v.Init)
	}
	for _, v := range pkg.Vars {
		visitExpr(v.Init)
		for _, h := range v.Handlers {
			visitFunc(h.Func)
		}
	}
	for _, f := range pkg.Funcs {
		visitFunc(f)
	}
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			visitExpr(v.Init)
			for _, h := range v.Handlers {
				visitFunc(h.Func)
			}
		}
		for _, f := range c.Funcs {
			visitFunc(f)
		}
		for _, t := range c.Timers {
			if t.Interval != nil {
				visitExpr(t.Interval)
			}
			if t.Enabled != nil {
				visitExpr(t.Enabled)
			}
			visitFunc(t.Handler)
		}
		visitStmts(c.Body)
	}
	for _, t := range pkg.Timers {
		if t.Interval != nil {
			visitExpr(t.Interval)
		}
		if t.Enabled != nil {
			visitExpr(t.Enabled)
		}
		visitFunc(t.Handler)
	}
	for _, w := range pkg.Windows {
		visitExpr(w.Href)
		visitExpr(w.Title)
		visitExpr(w.Favicon)
		for _, v := range w.Vars {
			visitExpr(v.Init)
			for _, h := range v.Handlers {
				visitFunc(h.Func)
			}
		}
		for _, f := range w.Funcs {
			visitFunc(f)
		}
		if w.ErrorHandler != nil {
			visitFunc(w.ErrorHandler.Func)
		}
		visitStmts(w.Body)
	}
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
				Contents:   "export { getTranslator, _resetTranslator, Translator, defaultLocale } from \"./i18n.js\";\n",
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

// i18nManifestJSON reads the project-root i18n.manifest.json and returns its
// raw JSON bytes. Returns nil when the file is absent.
func i18nManifestJSON(projectDir string, projectFS fs.FS) []byte {
	const name = "i18n.manifest.json"
	// Prefer projectFS when available (in-memory builds, playground).
	if projectFS != nil {
		data, err := fs.ReadFile(projectFS, name)
		if err == nil {
			return data
		}
	}
	if projectDir != "" {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err == nil {
			return data
		}
	}
	return nil
}

// i18nManifestJS returns the JS statement that initialises
// globalThis.__SNGL_I18N_MANIFEST__, or "" if no manifest is present.
// The returned string includes a trailing newline.
func i18nManifestJS(projectDir string, projectFS fs.FS) string {
	raw := i18nManifestJSON(projectDir, projectFS)
	if raw == nil {
		return ""
	}
	// Re-marshal to compact form so arbitrary whitespace in the source file
	// doesn't bloat the inline statement.
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Malformed manifest: skip silently; the runtime defaults to empty.
		return ""
	}
	compact, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("globalThis.__SNGL_I18N_MANIFEST__ = %s;\n", compact)
}
