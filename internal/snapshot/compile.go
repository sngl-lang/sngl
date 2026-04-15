package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
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
	if err := checkDoc(doc, dir); err != nil {
		return nil, err
	}

	// Re-parse for a fresh copy to optimize (no Clone in v2).
	previewDoc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("parse preview: %w", err)
	}
	if err := optimize.Optimize(previewDoc, optimize.Config{
		Platform: platform,
		Language: lang,
	}); err != nil {
		return nil, fmt.Errorf("optimize: %w", err)
	}

	jsLang := codegen.LookupLang("js")
	htmlPlat := codegen.LookupPlatform("html")
	if jsLang == nil || htmlPlat == nil {
		return nil, fmt.Errorf("html/js codegen not registered")
	}

	resp, err := htmlPlat.Generate(&codegen.Request{
		Doc:     previewDoc,
		Lang:    jsLang,
		Options: map[string]string{"preview": "true"},
	})
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	var htmlBuf bytes.Buffer
	if _, err := resp.Files[0].WriteTo(&htmlBuf); err != nil {
		return nil, fmt.Errorf("writing HTML: %w", err)
	}
	html := htmlBuf.Bytes()

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
	_, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS(dir),
		Dir:    dir,
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return fmt.Errorf("check: %s", d.Error())
		}
	}
	return nil
}
