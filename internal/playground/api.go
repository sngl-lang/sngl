// Package playground provides the SNGL compiler API for browser usage.
// Functions are designed to be called from JavaScript via WASM bindings.
package playground

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/android"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
	"git.duckfam.us/jonathan/sngl/ir"
)

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

// Compile parses, checks, optimizes, and generates HTML from SNGL source.
// Returns JSON: {"html": "...", "error": "..."}
func Compile(source string) string {
	result := map[string]any{"html": "", "error": ""}

	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
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
		return jsonStr(result)
	}

	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: "html", Language: "none",
	}); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("none")
	if gen == nil || lang == nil {
		result["error"] = "html/none codegen not registered"
		return jsonStr(result)
	}

	resp, err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lang,
		Options: codegen.OptionsFromMap(map[string]any{"preview": true}),
	})
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	if resp.Error != "" {
		result["error"] = resp.Error
		return jsonStr(result)
	}

	for _, f := range resp.Files {
		if strings.HasSuffix(f.Name, ".html") {
			var buf bytes.Buffer
			f.WriteTo(&buf)
			result["html"] = buf.String()
			break
		}
	}
	return jsonStr(result)
}

// Format parses SNGL source and returns formatted source.
// Returns JSON: {"source": "...", "error": "..."}
func Format(source string) string {
	result := map[string]any{"source": "", "error": ""}
	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	result["source"] = parser.Format(doc)
	return jsonStr(result)
}

// ASTDump parses SNGL source and returns the AST as JSON.
// Returns JSON: {"ast": "...", "error": "..."}
func ASTDump(source string) string {
	result := map[string]any{"ast": "", "error": ""}

	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	result["ast"] = string(data)
	return jsonStr(result)
}

// Targets returns available platform/language combinations as JSON.
func Targets() string {
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
	data, _ := json.Marshal(list)
	return string(data)
}

// Generate compiles SNGL source for a specific platform/language.
// Returns JSON: {"files": [...], "error": "..."}
func Generate(source, platform, lang string) string {
	result := map[string]any{"files": nil, "error": ""}

	doc, err := parser.Parse("playground.sngl", []byte(source))
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
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
		return jsonStr(result)
	}

	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: platform, Language: lang,
	}); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	gen := codegen.LookupPlatform(platform)
	if gen == nil {
		result["error"] = "unknown platform: " + platform
		return jsonStr(result)
	}
	lt := codegen.LookupLang(lang)
	if lt == nil {
		result["error"] = "unknown lang: " + lang
		return jsonStr(result)
	}

	resp, err := gen.Generate(&codegen.Request{Pkg: pkg, Lang: lt})
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	if resp.Error != "" {
		result["error"] = resp.Error
		return jsonStr(result)
	}

	var files []any
	for _, f := range resp.Files {
		var buf bytes.Buffer
		f.WriteTo(&buf)
		files = append(files, map[string]any{
			"name":    f.Name,
			"content": buf.String(),
		})
	}
	result["files"] = files
	return jsonStr(result)
}

// Diagnostics returns LSP diagnostics for SNGL source as JSON.
func Diagnostics(source string) string {
	doc, diags := lspcore.Analyze(source, "playground.sngl", nil, "", nil)
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
	data, _ := json.Marshal(out)
	return string(data)
}

// Complete returns LSP completions at the given position as JSON.
func Complete(source string, line, col int) string {
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
	data, _ := json.Marshal(out)
	return string(data)
}

// Hover returns LSP hover content at the given position as JSON.
func Hover(source string, line, col int) string {
	doc, _ := cachedParse(source)
	content := lspcore.Hover(source, doc, line, col)
	result := map[string]any{"content": content}
	return jsonStr(result)
}

func jsonStr(m map[string]any) string {
	data, _ := json.Marshal(m)
	return string(data)
}
