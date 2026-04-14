package android

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// generateGo produces output files for the go+android target: a Kotlin
// Compose UI that calls into a Go module via gomobile bind, plus the Go
// module source containing exported user functions and computeds.
func (g *Generator) generateGo(req *codegen.Request) (*codegen.Response, error) {
	cfg := g.configFromRequest(req)
	cfg.GoLib = true

	// Generate the Kotlin UI (same Compose output, but computeds/funcs call Golib)
	src, err := Compile(req.Doc, cfg)
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}

	if h := codegen.Header("android", req.Source, "// ", ""); h != "" {
		src = append([]byte(h), src...)
	}

	// Generate the Go module
	goSrc := emitGoLib(req.Doc, req.Lang)
	if h := codegen.Header("android", req.Source, "// ", ""); h != "" {
		goSrc = append([]byte(h), goSrc...)
	}

	resp := &codegen.Response{}

	if !cfg.GenerateMain {
		resp.Files = []*codegen.OutputFile{
			codegen.BytesFile("MainScreen.kt", src),
			codegen.BytesFile("golib/golib.go", goSrc),
			codegen.BytesFile("golib/go.mod", []byte("module golib\n\ngo 1.23\n")),
		}
	} else if cfg.Gradle {
		pkgPath := pkgToPath(cfg.Package)
		resp.Files = append(resp.Files, codegen.BytesFile(
			"app/src/main/java/"+pkgPath+"/MainScreen.kt", src,
		))
		resp.Files = append(resp.Files, codegen.BytesFile("golib/golib.go", goSrc))
		resp.Files = append(resp.Files, codegen.BytesFile("golib/go.mod", []byte("module golib\n\ngo 1.23\n")))
		resp.Files = append(resp.Files, scaffoldFiles(cfg)...)
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				f.Name = "app/src/main/" + f.Name
			}
			resp.Files = append(resp.Files, iconRes...)
		}
	} else {
		resp.Files = append(resp.Files, codegen.BytesFile("MainScreen.kt", src))
		resp.Files = append(resp.Files, codegen.BytesFile("golib/golib.go", goSrc))
		resp.Files = append(resp.Files, codegen.BytesFile("golib/go.mod", []byte("module golib\n\ngo 1.23\n")))
		resp.Files = append(resp.Files, directBuildFiles(cfg)...)
		if iconRes, err := iconFiles(cfg); err == nil {
			resp.Files = append(resp.Files, iconRes...)
		}
	}

	return resp, nil
}

// emitGoLib generates the Go source file for the golib module, containing
// exported functions compiled from SNGL func and computed declarations.
func emitGoLib(doc *ast.Document, lang codegen.LangTranslator) []byte {
	var b strings.Builder
	b.WriteString("package golib\n")

	// Collect imports needed by generated code
	needMath := false
	needStrings := false
	for _, fn := range docFuncs(doc) {
		if fn.ReturnType == nil || strings.Contains(fn.Name, ".") {
			continue
		}
		src := formatFuncBody(fn, lang)
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
	scope := &codegen.ExprScope{
		LocalVars: map[string]bool{},
	}
	for _, fn := range docFuncs(doc) {
		if fn.ReturnType == nil || strings.Contains(fn.Name, ".") {
			continue
		}
		emitGoLibFunc(&b, fn, lang, scope)
	}

	// Emit exported computed functions (zero-arg expression-form)
	for _, fn := range docFuncs(doc) {
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			emitGoLibComputed(&b, fn, lang, scope)
		}
	}

	return []byte(b.String())
}

func emitGoLibFunc(b *strings.Builder, fn *ast.FuncDef, lang codegen.LangTranslator, scope *codegen.ExprScope) {
	params := make([]string, len(fn.Params.Params))
	for i, p := range fn.Params.Params {
		params[i] = p.Name + " " + lang.TypeToNative(codegen.ExprTypeHint(p.Type))
	}
	paramStr := strings.Join(params, ", ")
	retType := lang.TypeToNative(codegen.ExprTypeHint(fn.ReturnType))
	goName := goExportName(fn.Name)

	for _, p := range fn.Params.Params {
		scope.LocalVars[p.Name] = true
	}
	defer func() {
		for _, p := range fn.Params.Params {
			delete(scope.LocalVars, p.Name)
		}
	}()

	if fn.Body != nil {
		body := lang.TranslateExpr(fn.Body, scope)
		fmt.Fprintf(b, "\nfunc %s(%s) %s {\n\treturn %s\n}\n", goName, paramStr, retType, body)
	} else if len(fn.Block.Stmts) > 0 {
		fmt.Fprintf(b, "\nfunc %s(%s) %s {\n", goName, paramStr, retType)
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				scope.LocalVars[s.Name] = true
				val := lang.TranslateExpr(s.Init, scope)
				fmt.Fprintf(b, "\t%s := %s\n", s.Name, val)
			case *ast.ReturnStmt:
				if s.Value != nil {
					ret := lang.TranslateExpr(s.Value, scope)
					fmt.Fprintf(b, "\treturn %s\n", ret)
				} else {
					b.WriteString("\treturn\n")
				}
			default:
				stmts := lang.TranslateMutation(stmt, scope)
				for _, line := range stmts {
					fmt.Fprintf(b, "\t%s\n", line)
				}
			}
		}
		for _, stmt := range fn.Block.Stmts {
			if s, ok := stmt.(*ast.VarStmt); ok {
				delete(scope.LocalVars, s.Name)
			}
		}
		b.WriteString("}\n")
	}
}

func emitGoLibComputed(b *strings.Builder, fn *ast.FuncDef, lang codegen.LangTranslator, scope *codegen.ExprScope) {
	if fn.Body == nil {
		return
	}
	goName := goExportName(fn.Name)
	body := lang.TranslateExpr(fn.Body, scope)
	fmt.Fprintf(b, "\nfunc %s() string {\n\treturn %s\n}\n", goName, body)
}

func formatFuncBody(fn *ast.FuncDef, lang codegen.LangTranslator) string {
	scope := &codegen.ExprScope{LocalVars: map[string]bool{}}
	if fn.Body != nil {
		return lang.TranslateExpr(fn.Body, scope)
	}
	return ""
}

// goExportName capitalizes the first letter for Go export.
func goExportName(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
