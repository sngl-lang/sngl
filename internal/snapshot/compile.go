package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// CompilePreviewHTML compiles a .sngl file to HTML for the given platform and language.
// The HTML includes preview CSS if the target platform implements PreviewStyler.
func CompilePreviewHTML(sourceFile, platform, lang string) ([]byte, error) {
	doc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	if err := checker.Check(doc, filepath.Dir(sourceFile), checker.DefaultResolver(), nil, nil, true); err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}

	previewDoc := doc.Clone()
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

	html := resp.Files[0].Content

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

// ParseOutputs parses a .sngl file and returns its output targets.
func ParseOutputs(sourceFile string) ([]*ast.Output, error) {
	doc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, err
	}
	return doc.Outputs, nil
}

func ParseSNGL(filename string) (*ast.Document, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return snglparser.Parse(filename, f)
}
