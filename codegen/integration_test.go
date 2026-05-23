package codegen_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript" // register js translator
)

func TestWriterEndToEnd_Go(t *testing.T) {
	sink := codegen.NewMemSink()
	tr := &golang.Translator{}
	w := codegen.OpenCodeFile(sink, "model.go", tr, codegen.WriterOptions{Maps: true, Source: "foo.sngl", Platform: "test"})

	w.Import(codegen.ImportSpec{Path: "fmt", Kind: codegen.ImportNative})
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 3, Column: 1}, []byte("package main\n\nfunc main() {\n"))
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 8, Column: 1}, []byte("\tfmt.Println(\"hi\")\n"))
	w.Write([]byte("}\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := string(sink.Files()["model.go"])
	if !strings.Contains(got, "//line foo.sngl:3") {
		t.Errorf("missing first //line directive:\n%s", got)
	}
	if !strings.Contains(got, "//line foo.sngl:8") {
		t.Errorf("missing second //line directive:\n%s", got)
	}
	if !strings.Contains(got, "DO NOT EDIT") {
		t.Errorf("missing generated header:\n%s", got)
	}
}

func TestWriterEndToEnd_JS(t *testing.T) {
	sink := codegen.NewMemSink()
	tr := codegen.LookupLang("js")
	if tr == nil {
		t.Skip("js translator not registered")
	}
	w := codegen.OpenCodeFile(sink, "out.js", tr, codegen.WriterOptions{Maps: true})
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 1, Column: 1}, []byte("console.log(1);\n"))
	w.WriteAt(ast.Pos{File: "foo.sngl", Line: 2, Column: 1}, []byte("console.log(2);\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	files := sink.Files()
	if _, ok := files["out.js.map"]; !ok {
		t.Fatalf("sidecar missing: %v", files)
	}
	if !strings.Contains(string(files["out.js"]), "sourceMappingURL=out.js.map") {
		t.Errorf("inline sourceMappingURL footer missing:\n%s", files["out.js"])
	}
}
