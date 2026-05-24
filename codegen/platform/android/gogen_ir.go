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
		for _, f := range scaffoldFiles(cfg, false, false) {
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

	var b strings.Builder
	b.WriteString("package golib\n")

	// Collect all funcs
	allFuncs := ctx.Pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}

	// Check for needed imports
	needMath := false
	needStrings := false
	for _, fn := range allFuncs {
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn || fn.Receiver != "" {
			continue
		}
		src := irFuncBodyString(fn, gc)
		if strings.Contains(src, "math.") {
			needMath = true
		}
		if strings.Contains(src, "strings.") {
			needStrings = true
		}
	}
	if needMath || needStrings {
		b.WriteString("\nimport (\n")
		if needMath {
			b.WriteString("\t\"math\"\n")
		}
		if needStrings {
			b.WriteString("\t\"strings\"\n")
		}
		b.WriteString(")\n")
	}

	// Emit exported functions
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
			continue
		}
		emitGoLibIRFunc(&b, fn, gc)
	}

	// Emit computed functions
	for _, fn := range allFuncs {
		if codegen.IsComputed(fn) {
			emitGoLibIRComputed(&b, fn, gc)
		}
	}

	return []byte(b.String())
}

func emitGoLibIRFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name + " " + golang.IRTypeToGo(p.Type)
	}
	paramStr := strings.Join(params, ", ")
	retType := golang.IRTypeToGo(fn.Return)
	goName := exportName(fn.Name)

	localGC := gc
	for _, p := range fn.Params {
		localGC = localGC.WithLocal(p.Name)
	}

	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := localGC.EvalExpr(ret.Value)
			fmt.Fprintf(b, "\nfunc %s(%s) %s {\n\treturn %s\n}\n", goName, paramStr, retType, body)
			return
		}
	}

	if len(fn.Block) > 0 {
		fmt.Fprintf(b, "\nfunc %s(%s) %s {\n", goName, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localGC.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t%s\n", line)
			}
		}
		b.WriteString("}\n")
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

func irFuncBodyString(fn *ir.Func, gc *golang.GoIRContext) string {
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			return gc.EvalExpr(ret.Value)
		}
	}
	return ""
}
