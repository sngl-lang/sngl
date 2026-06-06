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

	dir := filepath.Dir(sourceFile)
	pkg, err := checkAndReturn(doc, dir)
	if err != nil {
		return nil, err
	}

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

// PlatformsForFile returns the platform names from a .sngl file's output block.
func PlatformsForFile(sourceFile string) []string {
	outputs, err := CheckOutputs(sourceFile)
	if err != nil {
		return nil
	}
	var platforms []string
	for _, o := range outputs {
		platforms = append(platforms, o.Platform)
	}
	return platforms
}

// ParseSNGL parses a .sngl file and returns the AST document.
func ParseSNGL(filename string) (*ast.Document, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return parser.Parse(filename, src)
}

// checkDoc type-checks a document and returns the first error diagnostic, if any.
func checkDoc(doc *ast.Document, dir string) error {
	_, err := checkAndReturn(doc, dir)
	return err
}

// checkAndReturn type-checks a document and returns the package or the first error.
func checkAndReturn(doc *ast.Document, dir string) (*ir.Package, error) {
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
	return pkg, nil
}
