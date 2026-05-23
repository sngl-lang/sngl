//go:build js && wasm

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"syscall/js"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/android"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func main() {
	js.Global().Set("snglCompile", js.FuncOf(compile))
	js.Global().Set("snglFormat", js.FuncOf(format))
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
	doc, err := parser.Parse("playground.sngl", []byte(source))
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

	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: codegen.CollectPlatforms(),
	})
	if len(diags) > 0 && diags[0].Severity == ir.Error {
		err = fmt.Errorf("%s", diags[0].Msg)
	}
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	compileOptCfg := &optimize.Config{
		Platform: "html",
		Language: "none",
	}
	if err := optimize.Optimize(pkg, compileOptCfg); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	gen := codegen.LookupPlatform("html")
	if gen == nil {
		result["error"] = "html platform not registered"
		return toJSObject(result)
	}

	lang := codegen.LookupLang("none")
	if lang == nil {
		result["error"] = "none language not registered"
		return toJSObject(result)
	}

	compileCaps := gen.Capabilities().Merge(lang.Capabilities())
	if err := lower.Lower(pkg, compileCaps, lower.Options{Platform: "html"}); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	if compileCaps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, compileOptCfg); err != nil {
			result["error"] = err.Error()
			return toJSObject(result)
		}
	}

	opts := optionsForTarget(pkg, "html")
	codegen.SetOptionField(opts, "preview", true)
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: opts,
	}, mem); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			result["html"] = string(content)
			break
		}
	}
	return toJSObject(result)
}

func format(this js.Value, args []js.Value) any {
	result := map[string]any{"source": "", "error": ""}
	if len(args) == 0 {
		result["error"] = "no source provided"
		return toJSObject(result)
	}
	source := args[0].String()
	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	result["source"] = parser.Format(doc)
	return toJSObject(result)
}

func astDump(this js.Value, args []js.Value) any {
	result := map[string]any{"ast": "", "error": ""}
	if len(args) == 0 {
		result["error"] = "no source provided"
		return toJSObject(result)
	}
	source := args[0].String()

	doc, err := parser.Parse("playground.sngl", []byte(source))
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

	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: codegen.CollectPlatforms(),
	})
	if len(diags) > 0 && diags[0].Severity == ir.Error {
		err = fmt.Errorf("%s", diags[0].Msg)
	}
	if err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}

	genOptCfg := &optimize.Config{
		Platform: platName,
		Language: langName,
	}
	if err := optimize.Optimize(pkg, genOptCfg); err != nil {
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

	genCaps := gen.Capabilities().Merge(lang.Capabilities())
	if err := lower.Lower(pkg, genCaps, lower.Options{Platform: platName}); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	if genCaps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, genOptCfg); err != nil {
			result["error"] = err.Error()
			return toJSObject(result)
		}
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: optionsForTarget(pkg, platName),
	}, mem); err != nil {
		result["error"] = err.Error()
		return toJSObject(result)
	}
	var files []any
	for name, content := range mem.Files() {
		files = append(files, map[string]any{
			"name":    name,
			"content": string(content),
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

	doc, diags := lspcore.Analyze(source, "playground.sngl", nil, "", nil)

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

// optionsForTarget clones the user's `output { ... <plat>(...) ... }` options
// for the given platform, or returns an empty StructLit if no matching target
// was declared. Lets the playground honor flags like `minify=true`.
func optionsForTarget(pkg *ir.Package, platform string) *ir.StructLit {
	if pkg == nil {
		return &ir.StructLit{}
	}
	for _, o := range pkg.Outputs {
		if o.Platform != platform || o.Options == nil {
			continue
		}
		out := &ir.StructLit{Type: o.Options.Type, Def: o.Options.Def}
		out.Fields = append(out.Fields, o.Options.Fields...)
		return out
	}
	return &ir.StructLit{}
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
