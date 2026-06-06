package docs

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
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
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: codegen.CollectPlatforms(),
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return "", lessonErr(d.Msg)
		}
	}
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: "html", Language: "none"}); err != nil {
		return "", err
	}
	gen := codegen.LookupPlatform("html")
	lang := codegen.LookupLang("none")
	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "html"}); err != nil {
		return "", err
	}
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Pkg: pkg, Lang: lang,
		Options: codegen.OptionsFromMap(map[string]any{"preview": true}),
	}, mem); err != nil {
		return "", err
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content), nil
		}
	}
	return "", nil
}

type lessonErr string

func (e lessonErr) Error() string { return string(e) }
