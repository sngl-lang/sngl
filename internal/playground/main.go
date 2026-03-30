//go:build js && wasm

package main

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"syscall/js"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func main() {
	js.Global().Set("snglCompile", js.FuncOf(compile))
	js.Global().Set("snglAST", js.FuncOf(astDump))
	js.Global().Set("snglTargets", js.FuncOf(targets))
	js.Global().Set("snglGenerate", js.FuncOf(generate))
	js.Global().Set("snglDiagnostics", js.FuncOf(diagnostics))
	js.Global().Set("snglComplete", js.FuncOf(complete))
	js.Global().Set("snglHover", js.FuncOf(hover))
	select {}
}

// --- doc cache ---

var (
	cacheMu   sync.Mutex
	cacheHash [32]byte
	cacheDoc  *ast.Document
)

func cachedParse(source string) (*ast.Document, error) {
	h := sha256.Sum256([]byte(source))
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if h == cacheHash && cacheDoc != nil {
		return cacheDoc, nil
	}
	doc, err := snglparser.Parse("playground.sngl", strings.NewReader(source))
	if doc != nil {
		cacheHash = h
		cacheDoc = doc
	}
	return doc, err
}

// --- original functions ---

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

	if err := checker.Check(doc, "", nil, nil, nil, true); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	clone := doc.Clone()
	if err := optimize.Optimize(clone, optimize.Config{
		Platform: "html",
		Language: "js",
	}); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

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

// --- new functions ---

func targets(this js.Value, args []js.Value) any {
	var list []any
	for _, platName := range codegen.Platforms() {
		plat := codegen.LookupPlatform(platName)
		if plat == nil {
			continue
		}
		for _, langName := range plat.SupportedLangs() {
			list = append(list, map[string]any{
				"platform": platName,
				"lang":     langName,
			})
		}
	}
	return toJSValue(list)
}

func generate(this js.Value, args []js.Value) any {
	result := map[string]any{"files": nil, "error": ""}
	if len(args) < 3 {
		result["error"] = "usage: snglGenerate(source, platform, lang)"
		return toJSObject(result)
	}
	source := args[0].String()
	platName := args[1].String()
	langName := args[2].String()

	doc, err := snglparser.Parse("playground.sngl", strings.NewReader(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	if err := checker.Check(doc, "", nil, nil, nil, true); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	clone := doc.Clone()
	if err := optimize.Optimize(clone, optimize.Config{
		Platform: platName,
		Language: langName,
	}); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	gen := codegen.LookupPlatform(platName)
	if gen == nil {
		result["error"] = "unknown platform: " + platName
		return toJSObject(result)
	}
	lang := codegen.LookupLang(langName)
	if lang == nil {
		result["error"] = "unknown lang: " + langName
		return toJSObject(result)
	}

	resp, err := gen.Generate(&codegen.Request{
		Doc:  clone,
		Lang: lang,
	})
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	if resp.Error != "" {
		result["error"] = resp.Error
		return toJSObject(result)
	}

	var files []any
	for _, f := range resp.Files {
		files = append(files, map[string]any{
			"name":    f.Name,
			"content": string(f.Content),
		})
	}
	result["files"] = files
	return toJSValue(result)
}

func diagnostics(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return toJSValue([]any{})
	}
	source := args[0].String()

	doc, diags := lspcore.Analyze(source, "playground.sngl", "", nil)

	// Update cache for completion/hover
	if doc != nil {
		h := sha256.Sum256([]byte(source))
		cacheMu.Lock()
		cacheHash = h
		cacheDoc = doc
		cacheMu.Unlock()
	}

	var out []any
	for _, d := range diags {
		out = append(out, map[string]any{
			"line":     d.Range.Start.Line,
			"col":      d.Range.Start.Character,
			"endLine":  d.Range.End.Line,
			"endCol":   d.Range.End.Character,
			"severity": int(d.Severity),
			"message":  d.Message,
		})
	}
	return toJSValue(out)
}

func complete(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return toJSValue([]any{})
	}
	source := args[0].String()
	line := args[1].Int()
	col := args[2].Int()

	doc, _ := cachedParse(source)
	items := lspcore.Complete(source, doc, line, col)

	var out []any
	for _, item := range items {
		out = append(out, map[string]any{
			"label":      item.Label,
			"kind":       item.Kind,
			"detail":     item.Detail,
			"insertText": item.InsertText,
		})
	}
	return toJSValue(out)
}

func hover(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return toJSObject(map[string]any{"content": ""})
	}
	source := args[0].String()
	line := args[1].Int()
	col := args[2].Int()

	doc, _ := cachedParse(source)
	content := lspcore.Hover(source, doc, line, col)

	return toJSObject(map[string]any{"content": content})
}

// --- helpers ---

func toJSObject(m map[string]any) js.Value {
	obj := js.Global().Get("Object").New()
	for k, v := range m {
		obj.Set(k, v)
	}
	return obj
}

// toJSValue recursively converts Go values to JS values.
func toJSValue(v any) js.Value {
	switch val := v.(type) {
	case nil:
		return js.Null()
	case bool:
		return js.ValueOf(val)
	case int:
		return js.ValueOf(val)
	case float64:
		return js.ValueOf(val)
	case string:
		return js.ValueOf(val)
	case map[string]any:
		obj := js.Global().Get("Object").New()
		for k, v := range val {
			obj.Set(k, toJSValue(v))
		}
		return obj
	case []any:
		arr := js.Global().Get("Array").New(len(val))
		for i, v := range val {
			arr.SetIndex(i, toJSValue(v))
		}
		return arr
	default:
		return js.ValueOf(v)
	}
}
