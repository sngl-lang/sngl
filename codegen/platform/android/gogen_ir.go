package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
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
		return fmt.Errorf("android draws a canvas through Compose, which is Kotlin: build this program with --lang kotlin, or remove the canvas (%s)", name)
	}
	// The same reasoning for sngl:ui/nav: a stack is a Compose NavHost, and
	// under `--lang go` the funcs a `go` may be written in are emitted into
	// golib, where there is no NavController to call.
	if nav, _ := collectAndroidNav(ctx); nav != nil && len(nav.stacks) > 0 {
		return fmt.Errorf("%s: android renders sngl:ui/nav through a Compose NavHost, which is Kotlin: build this program with --lang kotlin", nodeAt(nav.stacks[0].node))
	}
	src, err := CompileIR(ctx, cfg)
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
		if err := writeAndroidSourceFile(sink, "MainScreen.kt", ktLang, opts, src); err != nil {
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
		if err := writeAndroidSourceFile(sink, "app/src/main/java/"+pkgPath+"/MainScreen.kt", ktLang, opts, src); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, "golib/golib.go", req.Lang, opts, goSrc); err != nil {
			return err
		}
		if err := writeAndroidFile(sink, "golib/go.mod", goMod); err != nil {
			return err
		}
		for _, f := range scaffoldFiles(cfg, false, false, "", false) {
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
		if err := writeAndroidSourceFile(sink, "MainScreen.kt", ktLang, opts, src); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, "golib/golib.go", req.Lang, opts, goSrc); err != nil {
			return err
		}
		if err := writeAndroidFile(sink, "golib/go.mod", goMod); err != nil {
			return err
		}
		for _, f := range directBuildFiles(cfg, false, false) {
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

	// Pass 1: dry-run all function bodies through gc to collect imports.
	// RequireImport is idempotent, so re-running in pass 2 is harmless.
	var discard strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
			continue
		}
		emitGoLibIRFunc(&discard, fn, gc)
	}
	var consts strings.Builder
	for _, c := range goLibConsts(ctx.Pkg, allFuncs) {
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
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
			continue
		}
		emitGoLibIRFunc(&b, fn, gc)
	}

	return []byte(b.String())
}

// goLibConsts are the package consts a go-lib func reads. Only those: the
// module declares no types, so a const of a struct type nothing here reads
// would name one it lacks.
func goLibConsts(pkg *ir.Package, funcs []*ir.Func) []*ir.Var {
	read := map[*ir.Var]bool{}
	for _, fn := range funcs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
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
	if len(fn.Block) == 0 {
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
