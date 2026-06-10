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
	if main := ctx.MainComponent(); main != nil {
		gc = gc.ForComponent(main)
	}

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
	for _, fn := range allFuncs {
		if codegen.IsComputed(fn) {
			emitGoLibIRComputed(&discard, fn, gc)
		}
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

	// Pass 2: emit actual code.
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
			continue
		}
		emitGoLibIRFunc(&b, fn, gc)
	}
	for _, fn := range allFuncs {
		if codegen.IsComputed(fn) {
			emitGoLibIRComputed(&b, fn, gc)
		}
	}

	return []byte(b.String())
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

func emitGoLibIRComputed(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	goName := exportName(fn.Name)
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := gc.EvalExpr(ret.Value)
			fmt.Fprintf(b, "\nfunc %s() string {\n\treturn %s\n}\n", goName, body)
			return
		}
	}
}
