package android

import (
	"io/fs"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/codegen/platform/android/i18nruntime"
	"git.duckfam.us/jonathan/sngl/ir"
)

// hasI18nCalls reports whether the IR package contains any call to an i18n
// intrinsic (i18n.tr, i18n.format, i18n.numberInt, etc.). When true the
// android platform must inject the Kotlin i18n runtime and manifest.
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
		if kotlin.IsI18nCall(qual) {
			found = true
			return true // signal stop
		}
		return false
	})
	return found
}

// i18nRuntimeFile returns the OutputFile that should be written into the
// generated module's source tree for the Kotlin i18n runtime.
// Path: app/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/I18n.kt
func i18nRuntimeFile() *codegen.OutputFile {
	path := "app/src/main/kotlin/" +
		pkgToPath(kotlin.SnglI18nKotlinPackage) +
		"/I18n.kt"
	return codegen.BytesFile(path, []byte(i18nruntime.I18nKt))
}

// i18nManifestFile reads the project-root i18n.manifest.json and returns an
// OutputFile that places it at app/src/main/assets/i18n.manifest.json.
// Returns nil when the manifest is absent.
func i18nManifestFile(cfg Config, projectFS fs.FS) *codegen.OutputFile {
	const name = "i18n.manifest.json"
	var data []byte
	// Prefer projectFS when available (in-memory builds, playground).
	if projectFS != nil {
		if b, err := fs.ReadFile(projectFS, name); err == nil {
			data = b
		}
	}
	if data == nil && cfg.ProjectDir != "" {
		if b, err := os.ReadFile(filepath.Join(cfg.ProjectDir, name)); err == nil {
			data = b
		}
	}
	if data == nil {
		return nil
	}
	return codegen.BytesFile("app/src/main/assets/"+name, data)
}

// walkPkgExprs visits every expression in the IR package calling fn for each.
// fn returns true to request early exit. Mirrors html.walkPkgExprs.
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
