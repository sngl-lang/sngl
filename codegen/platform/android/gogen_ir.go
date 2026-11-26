package android

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// emitGo produces output files for the go+android target using IR.
func (c *compilation) emitGo(req *codegen.Request, sink codegen.Sink) error {
	cfg := c.cfg
	ctx := c.ctx
	// A canvas is drawn by this platform's Compose overrides, and under
	// `--lang go` the draw function is emitted into golib, where `drawRect`
	// and `Offset` are undefined and the Compose import path is not a Go
	// package. Reported here rather than written out: before the shapes became
	// overrides the same combination panicked in the Go backend on an
	// untranslated canvas intrinsic, so this is a loud failure staying loud.
	if name := firstCanvasName(ctx.Canvases); name != "" {
		return ir.Decline("android", "android draws a canvas through Compose, which is Kotlin: build this program with --lang kotlin, or remove the canvas (%s)", name)
	}
	// The same reasoning for sngl:ui/nav: a stack is a Compose NavHost, and
	// under `--lang go` the funcs a `go` may be written in are emitted into
	// golib, where there is no NavController to call.
	if nav, _ := collectAndroidNav(ctx); nav != nil && len(nav.stacks) > 0 {
		return ir.Decline("android", "%s: android renders sngl:ui/nav through a Compose NavHost, which is Kotlin: build this program with --lang kotlin", nodeAt(nav.stacks[0].node))
	}
	src, err := compileIR(ctx, cfg)
	if err != nil {
		return err
	}

	goSrc := emitGoLibIR(ctx)
	goMod := fmt.Appendf(nil, "module golib\n\ngo %s\n", cfg.GoVersion)

	// When --lang=go, MainScreen.kt is still Kotlin source — look up its lang
	// explicitly so its header is rendered by the Kotlin translator.
	ktLang := codegen.LookupLang("kotlin")
	opts := codegen.FileOptions{Source: req.Source, Platform: "android", Maps: req.Maps}

	if !cfg.Main {
		if err := src.write(sink, "MainScreen.kt", ktLang, opts); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, "golib/golib.go", req.Lang, opts, goSrc); err != nil {
			return err
		}
		if err := writeAndroidFile(sink, "golib/go.mod", goMod); err != nil {
			return err
		}
	} else if cfg.UseGradle() {
		pkgPath := pkgToPath(cfg.Package)
		if err := src.write(sink, "app/src/main/java/"+pkgPath+"/MainScreen.kt", ktLang, opts); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, "golib/golib.go", req.Lang, opts, goSrc); err != nil {
			return err
		}
		if err := writeAndroidFile(sink, "golib/go.mod", goMod); err != nil {
			return err
		}
		for _, f := range scaffoldFiles(cfg, false, false, permissions(req.Pkg), false, "", false) {
			if err := writeOutputFile(sink, f); err != nil {
				return err
			}
		}
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				if err := writeOutputFileAs(sink, "app/src/main/"+f.Name, f); err != nil {
					return err
				}
			}
		}
	} else {
		if err := src.write(sink, "MainScreen.kt", ktLang, opts); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, "golib/golib.go", req.Lang, opts, goSrc); err != nil {
			return err
		}
		if err := writeAndroidFile(sink, "golib/go.mod", goMod); err != nil {
			return err
		}
		for _, f := range directBuildFiles(cfg, false, false, permissions(req.Pkg), false) {
			if err := writeOutputFile(sink, f); err != nil {
				return err
			}
		}
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				if err := writeOutputFile(sink, f); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// emitGoLibIR generates the Go source for the golib module using IR.
func emitGoLibIR(ctx *codegen.CodegenCtx) []byte {
	gc := golang.NewIRContext(ctx.ExprCtx)
	if main := ctx.RootDecl(); main != nil {
		gc = gc.ForComponent(main)
	}
	// go-lib funcs are emitted as free, exported package-level functions, so
	// calls among them (including recursion) must render as `Fib(...)`, not
	// `m.fib(...)` — there is no Model receiver in this module.
	gc.FreeFuncScope = true

	allFuncs := ctx.AllFuncs()
	golib := goLib(allFuncs)

	// Pass 1: dry-run all function bodies through gc to collect imports.
	// RequireImport is idempotent, so re-running in pass 2 is harmless.
	var discard strings.Builder
	for _, fn := range allFuncs {
		if !golib[fn] {
			continue
		}
		emitGoLibIRFunc(&discard, fn, gc)
	}
	var consts strings.Builder
	for _, c := range goLibConsts(ctx.Pkg, allFuncs, golib) {
		fmt.Fprintf(&consts, "\nvar %s %s = %s\n", c.Name, golang.IRTypeToGo(c.Type), golang.LowerVarInit(c, gc))
	}

	// Build output: package clause, conditional import block, then code.
	var b strings.Builder
	b.WriteString("package golib\n")
	if imports := gc.Imports(); len(imports) > 0 {
		b.WriteString("\nimport (\n")
		for _, p := range imports {
			fmt.Fprintf(&b, "\t%q\n", p)
		}
		b.WriteString(")\n")
	}
	b.WriteString(consts.String())

	// Pass 2: emit actual code. A computed is skipped by the same filter and
	// deliberately has no pass of its own: this module holds free functions and
	// no state, so a computed that reads a var would render it through a Model
	// receiver that does not exist here, and one that reads none is folded to a
	// constant before it arrives. The Kotlin side renders them from Compose
	// state.
	for _, fn := range allFuncs {
		if !golib[fn] {
			continue
		}
		emitGoLibIRFunc(&b, fn, gc)
	}

	return []byte(b.String())
}

// goLibConsts are the package consts a go-lib func reads. Only those: the
// module declares no types, so a const of a struct type nothing here reads
// would name one it lacks.
func goLibConsts(pkg *ir.Package, funcs []*ir.Func, golib map[*ir.Func]bool) []*ir.Var {
	read := map[*ir.Var]bool{}
	for _, fn := range funcs {
		if !golib[fn] {
			continue
		}
		_ = ir.Walk(fn, func(n ir.Node) error {
			if id, ok := n.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok && v.IsConst {
					read[v] = true
				}
			}
			return nil
		})
	}
	var out []*ir.Var
	for _, c := range pkg.Consts {
		if read[c] && c.Init != nil {
			out = append(out, c)
		}
	}
	return out
}

func emitGoLibIRFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if !codegen.HasBody(fn) {
		return
	}
	// Go-lib funcs are free, exported functions. Defer signature/body emission
	// to the Go language driver's EmitFuncDef rather than re-implementing it.
	fnCopy := *fn
	fnCopy.Name = exportName(fn.Name)
	b.WriteByte('\n')
	for _, line := range gc.EmitFuncDef(&fnCopy) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// firstCanvasName is what to call any one of the program's drawings, or "".
// Which one does not matter: the caller only names it in a diagnostic.
func firstCanvasName(draws *codegen.CanvasDraws) string {
	all := draws.All()
	if len(all) == 0 {
		return ""
	}
	return all[0].Name
}

// composeSide reports whether fn is this platform package's own: one that
// describes Compose -- what a Style becomes, a closure written in a Row's
// scope -- which is Kotlin under any `--lang`, where a program's funcs are
// golib's under `--lang go`.
func composeSide(fn *ir.Func) bool {
	return fn.PkgPath() == "sngl:platform/android"
}

// goLib is the funcs emitted into the gomobile module: free functions of the
// program's own that return a value. The module declares no types and holds no
// state, and gomobile binds only a basic signature, so a func is one only when
// its signature is basic, nothing in its body has a type the module would have
// to declare, it reads no var but a const, it raises nothing -- a Go panic
// through gomobile ends the process, where Kotlin's own throw reaches the
// boundary -- and every func it calls is one too.
// The Kotlin side emits every other func itself, which is why both sides ask
// this one set.
func goLib(funcs []*ir.Func) map[*ir.Func]bool {
	in := map[*ir.Func]bool{}
	for _, fn := range funcs {
		if goLibCandidate(fn) {
			in[fn] = true
		}
	}
	// Narrowed to a fixpoint: a func calling one that left is out too.
	for changed := true; changed; {
		changed = false
		for fn := range in {
			if !goLibBody(fn, in) {
				delete(in, fn)
				changed = true
			}
		}
	}
	return in
}

func goLibCandidate(fn *ir.Func) bool {
	if fn.IsTest || fn.IsMethod() || codegen.IsComputed(fn) || fn.Return == nil || composeSide(fn) || !codegen.HasBody(fn) || fn.CanError {
		return false
	}
	if !goMobileBasic(fn.Return) {
		return false
	}
	for _, p := range fn.Params {
		if !goMobileBasic(p.Type) {
			return false
		}
	}
	return true
}

// goMobileBasic is a type gomobile binds across to Kotlin.
func goMobileBasic(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString:
		return true
	}
	return false
}

// declaresType is whether t, or a type inside it, is one the module would
// have to declare.
func declaresType(t *ir.Type) bool {
	return t != nil && t.Any(func(t *ir.Type) bool {
		switch t.Kind {
		case ir.TypeStruct, ir.TypeEnum, ir.TypeUnit, ir.TypeComponent, ir.TypeInstance, ir.TypeNative, ir.TypeAny, ir.TypeTypeParam:
			return true
		}
		return t.Decl != nil
	})
}

func goLibBody(fn *ir.Func, in map[*ir.Func]bool) bool {
	ok := true
	_ = ir.Walk(fn, func(n ir.Node) error {
		switch n := n.(type) {
		case *ir.LocalVar:
			if declaresType(n.Type) {
				ok = false
			}
		case *ir.Ident:
			if v, isVar := n.Sym.(*ir.Var); isVar && !v.IsConst {
				ok = false
			}
		case *ir.Call:
			if callee := n.Func; callee != nil && callee != fn && callee.Pkg == fn.Pkg && callee.Foreign.Name == "" && codegen.HasBody(callee) && !in[callee] {
				ok = false
			}
		}
		if e, isExpr := n.(ir.Expr); isExpr && declaresType(e.ExprType()) {
			ok = false
		}
		if !ok {
			return ir.SkipAll
		}
		return nil
	})
	return ok
}
