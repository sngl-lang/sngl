package docs

import (
	"bytes"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestTutorialLessonsCompile exercises the same pipeline the playground runs
// on every lesson so we catch runtime-visible breakage at build time.
func TestTutorialLessonsCompile(t *testing.T) {
	for _, s := range Tutorial() {
		for _, l := range s.Lessons {
			html, err := compileLesson(l.Code)
			if err != nil {
				t.Errorf("lesson %q: %v", l.Slug, err)
				continue
			}
			if html == "" {
				t.Errorf("lesson %q compiled but produced no html", l.Slug)
			}
		}
	}
}

func compileLesson(source string) (string, error) {
	doc, err := parser.Parse("lesson.sngl", []byte(source))
	if err != nil {
		return "", err
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return "", lessonErr(d.Msg)
		}
	}
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: "html", Language: "js"}); err != nil {
		return "", err
	}
	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("js")
	resp, err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lang,
		Options: map[string]string{"preview": "true"},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", lessonErr(resp.Error)
	}
	for _, f := range resp.Files {
		if strings.HasSuffix(f.Name, ".html") {
			var buf bytes.Buffer
			f.WriteTo(&buf)
			return buf.String(), nil
		}
	}
	return "", nil
}

type lessonErr string

func (e lessonErr) Error() string { return string(e) }
