// Package playground provides the SNGL compiler API for browser usage.
// Functions are designed to be called from JavaScript via WASM bindings.
package playground

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing/fstest"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
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

// optionsForTarget returns a clone of the platform options the user declared
// in their `output { ... <plat>(...) ... }` block, or an empty StructLit if
// no matching target was declared. Lets the playground honor flags like
// `minify=true` from the source.
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

// playgroundDir is the synthetic OS directory the playground reports
// for diagnostics and ProjectDir-keyed plumbing. Resolution itself
// only consults the in-memory FS, so the value is mostly cosmetic.
const playgroundDir = "/playground"

// parseSource interprets the editor buffer as a txtar archive. The
// archive's comment is treated as the main `playground.sngl` file; any
// `-- name --` sections become sibling files in an in-memory FS so
// `js:./lib`-style imports can resolve. Bare source (no `-- name --`
// markers) is the degenerate single-file case.
func parseSource(source string) (mainSrc []byte, fsys fs.FS) {
	arc := txtar.Parse([]byte(source))
	main := arc.Comment
	if len(main) == 0 && len(arc.Files) == 0 {
		main = []byte(source)
	}
	mfs := fstest.MapFS{
		"playground.sngl": &fstest.MapFile{Data: main},
	}
	for _, f := range arc.Files {
		mfs[f.Name] = &fstest.MapFile{Data: f.Data}
	}
	return main, mfs
}

// playgroundResolver is the in-memory analogue of cmd/sngl.cliResolver.
// It serves directory-style imports out of fsys and dispatches scheme
// imports through registered FSAwareScheme implementations.
type playgroundResolver struct {
	fsys fs.FS
}

func (r *playgroundResolver) Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error) {
	entries, err := fs.ReadDir(fsys, importPath)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", importPath, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		path := importPath + "/" + e.Name()
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (r *playgroundResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	imp := codegen.LookupScheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("scheme %q not supported in playground", scheme)
	}
	if fsa, ok := imp.(codegen.FSAwareScheme); ok && r.fsys != nil {
		return fsa.ResolveFS(uri, r.fsys, dir)
	}
	return nil, fmt.Errorf("scheme %q requires the OS filesystem; not supported in playground", scheme)
}

func (r *playgroundResolver) ResolveSchemeFS(scheme, _, _ string) ([]*ast.Document, fs.FS, error) {
	// FS-only schemes (git://, http://) need network/disk access we
	// don't provide in-browser. Return nil, nil, nil so the checker
	// falls through to ResolveScheme.
	if codegen.LookupFSScheme(scheme) == nil {
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("scheme %q not supported in playground", scheme)
}

func newCheckerConfig(fsys fs.FS, isMain bool) *checker.Config {
	return &checker.Config{
		FS:        fsys,
		Dir:       playgroundDir,
		IsMain:    isMain,
		Resolver:  &playgroundResolver{fsys: fsys},
		Platforms: codegen.CollectPlatforms(),
		Languages: codegen.CollectLangs(),
	}
}

// Compile parses, checks, optimizes, and generates HTML from SNGL source.
// Returns JSON: {"html": "...", "error": "..."}
func Compile(source string) string {
	result := map[string]any{"html": "", "error": ""}

	main, fsys := parseSource(source)
	doc, err := parser.Parse("playground.sngl", main)
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}

	pkg, diags := checker.Check(doc, newCheckerConfig(fsys, true))
	if len(diags) > 0 && diags[0].Severity == ir.Error {
		err = fmt.Errorf("%s", diags[0].Msg)
	}
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}

	compileOptCfg := &optimize.Config{
		Platform: "html", Language: "none",
	}
	if err := optimize.Optimize(pkg, compileOptCfg); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("none")
	if gen == nil || lang == nil {
		result["error"] = "html/none codegen not registered"
		return jsonStr(result)
	}
	compileFeats, err := codegen.CapsFor("none", "html")
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	compileCaps := compileFeats
	if err := lower.Lower(pkg, compileCaps, lower.Options{Platform: "html"}); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	if compileCaps != lower.NoLowering() {
		if err := optimize.Optimize(pkg, compileOptCfg); err != nil {
			result["error"] = err.Error()
			return jsonStr(result)
		}
	}

	opts := optionsForTarget(pkg, "html")
	codegen.SetOptionField(opts, "preview", true)
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lang,
		Options:   opts,
		ProjectFS: fsys,
	}, mem); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			result["html"] = string(content)
			break
		}
	}
	return jsonStr(result)
}

// Format parses SNGL source and returns formatted source.
// Returns JSON: {"source": "...", "error": "..."}
func Format(source string) string {
	result := map[string]any{"source": "", "error": ""}
	main, _ := parseSource(source)
	doc, err := parser.Parse("playground.sngl", main)
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

	main, _ := parseSource(source)
	doc, err := parser.Parse("playground.sngl", main)
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

	main, fsys := parseSource(source)
	doc, err := parser.Parse("playground.sngl", main)
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}

	pkg, diags := checker.Check(doc, newCheckerConfig(fsys, true))
	if len(diags) > 0 && diags[0].Severity == ir.Error {
		err = fmt.Errorf("%s", diags[0].Msg)
	}
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}

	genOptCfg := &optimize.Config{
		Platform: platform, Language: lang,
	}
	if err := optimize.Optimize(pkg, genOptCfg); err != nil {
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
	genFeats, err := codegen.CapsFor(lang, platform)
	if err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	genCaps := genFeats
	if err := lower.Lower(pkg, genCaps, lower.Options{Platform: platform}); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	if genCaps != lower.NoLowering() {
		if err := optimize.Optimize(pkg, genOptCfg); err != nil {
			result["error"] = err.Error()
			return jsonStr(result)
		}
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lt,
		Options:   optionsForTarget(pkg, platform),
		ProjectFS: fsys,
	}, mem); err != nil {
		result["error"] = err.Error()
		return jsonStr(result)
	}
	var files []any
	for name, content := range mem.Files() {
		files = append(files, map[string]any{
			"name":    name,
			"content": string(content),
		})
	}
	result["files"] = files
	return jsonStr(result)
}

// Diagnostics returns LSP diagnostics for SNGL source as JSON.
func Diagnostics(source string) string {
	main, _ := parseSource(source)
	doc, diags := lspcore.Analyze(string(main), "playground.sngl", nil, "", nil)
	if doc != nil {
		h := sha256.Sum256(main)
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
	main, _ := parseSource(source)
	doc, _ := cachedParse(string(main))
	items := lspcore.Complete(string(main), doc, line, col)

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
	main, _ := parseSource(source)
	doc, _ := cachedParse(string(main))
	content := lspcore.Hover(string(main), doc, line, col)
	result := map[string]any{"content": content}
	return jsonStr(result)
}

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

func jsonStr(m map[string]any) string {
	data, _ := json.Marshal(m)
	return string(data)
}
