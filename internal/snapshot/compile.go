package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CompilePreviewHTML compiles a .sngl file to HTML for the given platform and language.
// The HTML includes preview CSS if the target platform implements PreviewStyler.
func CompilePreviewHTML(sourceFile, platform, lang string) ([]byte, error) {
	doc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	pkg, err := checkAndReturn(doc, filepath.Dir(sourceFile), nil)
	if err != nil {
		return nil, err
	}
	return compilePreviewHTMLDoc(pkg, platform, lang)
}

// compilePreviewHTMLDoc is CompilePreviewHTML from the checked package on, for
// a caller that already has one -- a package read from several files has no
// single source file to re-read.
func compilePreviewHTMLDoc(pkg *ir.Package, platform, lang string) ([]byte, error) {
	optCfg := &optimize.Config{
		Platform: platform,
		Language: lang,
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return nil, fmt.Errorf("optimize: %w", err)
	}
	noneLang := codegen.LookupLang("none")
	htmlPlat := codegen.LookupPlatform("html")
	if noneLang == nil || htmlPlat == nil {
		return nil, fmt.Errorf("html/none codegen not registered")
	}
	caps := htmlPlat.Capabilities(noneLang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "html"}); err != nil {
		return nil, fmt.Errorf("lower: %w", err)
	}
	if caps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return nil, fmt.Errorf("optimize2: %w", err)
		}
	}
	previewDoc := ir.Convert(pkg)

	mem := codegen.NewMemSink()
	if err := htmlPlat.Generate(&codegen.Request{
		Doc:     previewDoc,
		Pkg:     pkg,
		Lang:    noneLang,
		Options: codegen.OptionsFromMap(map[string]any{"preview": true}),
	}, mem); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	// The HTML platform emits a single HTML file in preview mode. Find it.
	var html []byte
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			html = content
			break
		}
	}
	if html == nil {
		return nil, fmt.Errorf("preview: no HTML file emitted")
	}

	if platform != "html" {
		plat := codegen.LookupPlatform(platform)
		if styler, ok := plat.(codegen.PreviewStyler); ok {
			css := styler.PreviewCSS()
			injection := fmt.Sprintf("<style>%s</style>\n</head>", css)
			html = []byte(strings.Replace(string(html), "</head>", injection, 1))
		}
	}

	return html, nil
}

// CheckOutputs parses and type-checks a .sngl file, returning the checker
// output targets. In v2 the output directives live in the checker Package, not
// the AST.
func CheckOutputs(sourceFile string) ([]*ir.Output, error) {
	doc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(sourceFile)
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS(dir),
		Dir:    dir,
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, fmt.Errorf("check: %s", d.Error())
		}
	}
	return pkg.Outputs, nil
}

// ParseSNGL parses a .sngl file and returns the AST document.
func ParseSNGL(filename string) (*ast.Document, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return parser.Parse(filename, src)
}

// checkAndReturn type-checks a document and returns the package or the first error.
func checkAndReturn(doc *ast.Document, dir string, resolver checker.ImportResolver) (*ir.Package, error) {
	// Register all available languages and platforms so the checker can merge
	// each stdlib component's platform extension body (e.g. the bubbletea
	// `Layout`/`Widget` primitives that back vbox/input/etc). Without these the
	// stdlib components keep no platform body, lowering inlines nothing, and the
	// generated View renders empty — every snapshot comes out blank.
	langs, plats := registeredTargets()
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        os.DirFS(dir),
		Dir:       dir,
		IsMain:    true,
		Resolver:  resolver,
		Languages: langs,
		Platforms: plats,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, fmt.Errorf("check: %s", d.Error())
		}
	}
	return pkg, nil
}

// registeredTargets returns every language and platform currently registered in
// the codegen registry, for passing to checker.Config so platform extension
// bodies are merged. Mirrors the CLI's collectTargets.
func registeredTargets() ([]ir.Language, []ir.Platform) {
	var langs []ir.Language
	for _, name := range codegen.Langs() {
		if l := codegen.LookupLang(name); l != nil {
			langs = append(langs, l)
		}
	}
	var plats []ir.Platform
	for _, name := range codegen.Platforms() {
		if p := codegen.LookupPlatform(name); p != nil {
			plats = append(plats, p)
		}
	}
	return langs, plats
}
