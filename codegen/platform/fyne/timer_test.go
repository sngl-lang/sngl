package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// A component with a timer must emit the fyne timer runtime (Ticker + Start/Stop
// methods). Regression: AnalyzeCommon never populated CommonAnalysis.Timers, so
// MutationModel platforms that read info.Timers (fyne) emitted no timer runtime
// at all — the interval never fired.
func TestTimerEmitsTickerRuntime(t *testing.T) {
	src := `
component main {
    var seconds = 0
    var running = false
    timer(interval=1000ms, enabled=running, @tick { seconds += 1 })
    vbox {
        text(value="{seconds} s")
        button(text="Start", @click { running!! })
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	if err := lower.Lower(pkg, lower.Caps{NoReactivity: true, NoDeclarative: true}, lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := string(mem.Files()["model.go"])

	for _, snippet := range []string{
		"timer0Ticker *time.Ticker",
		"func (m *Model) StartTimers()",
		"time.NewTicker(1000 * time.Millisecond)",
		"m.seconds += 1",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("generated model.go missing timer snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}
}
