//go:build js && wasm

package main

import (
	"encoding/json"
	"strings"
	"syscall/js"

	"git.duckfam.us/jonathan/sngl/checker"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/optimize"
	"git.duckfam.us/jonathan/sngl/snglparser"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func main() {
	js.Global().Set("snglCompile", js.FuncOf(compile))
	js.Global().Set("snglAST", js.FuncOf(astDump))
	select {}
}

func compile(this js.Value, args []js.Value) any {
	result := map[string]any{"html": "", "error": ""}
	if len(args) == 0 {
		result["error"] = "no source provided"
		return toJSObject(result)
	}
	source := args[0].String()

	doc, err := snglparser.Parse("playground.sngl", strings.NewReader(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	if err := checker.Check(doc, ""); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	clone := doc.Clone()
	optimize.Optimize(clone, optimize.Config{
		Platform: "html",
		Language: "js",
	})

	gen := codegen.LookupPlatform("html")
	if gen == nil {
		result["error"] = "html platform not registered"
		return toJSObject(result)
	}

	lang := codegen.LookupLang("js")
	if lang == nil {
		result["error"] = "js language not registered"
		return toJSObject(result)
	}

	resp, err := gen.Generate(&codegen.Request{
		Doc:     clone,
		Lang:    lang,
		Options: map[string]string{"preview": "true"},
	})
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	if resp.Error != "" {
		result["error"] = resp.Error
		return toJSObject(result)
	}

	for _, f := range resp.Files {
		if strings.HasSuffix(f.Name, ".html") {
			result["html"] = string(f.Content)
			break
		}
	}
	return toJSObject(result)
}

func astDump(this js.Value, args []js.Value) any {
	result := map[string]any{"ast": "", "error": ""}
	if len(args) == 0 {
		result["error"] = "no source provided"
		return toJSObject(result)
	}
	source := args[0].String()

	doc, err := snglparser.Parse("playground.sngl", strings.NewReader(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	result["ast"] = string(data)
	return toJSObject(result)
}

func toJSObject(m map[string]any) js.Value {
	obj := js.Global().Get("Object").New()
	for k, v := range m {
		obj.Set(k, v)
	}
	return obj
}
