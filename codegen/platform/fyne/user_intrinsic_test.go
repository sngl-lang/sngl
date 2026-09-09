package fyne

import (
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// A fyne widget written directly in user code must build its widget — issue
// #120.
//
// The bug was that the platform's one primitive carried
// `#[intrinsic("fyne:widget")]`, an id no Go code matched, while the blueprint
// table was keyed by stdlib component name. `fyne.widget(...)` therefore
// resolved to no entry, OnCreateNode returned nothing, and the node was
// dropped — but the surrounding AppendChild was still emitted, leaving
// `m.__n0.Add(m.__n1)` with `__n1` neither declared nor a Model field. Exit
// code 0, output that does not compile.
//
// What closes it is that a widget carries its own construction: the node the
// pre-scan found a Spec for is the node the emitter builds, wherever it was
// written. fyne.Label and fyne.Button are ordinary components in this
// platform's package, so reaching for one from user code is the same act as
// the stdlib override doing it.
func TestUserWrittenIntrinsicBuildsItsWidget(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:platform/fyne"

component main ui {
    vbox {
        fyne.Label(text="direct label")
        fyne.Button(text="direct button")
    }
}
`
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := string(mem.Files()["model.go"])

	for _, want := range []string{
		"widget.NewLabel(",
		`SetText("direct label")`,
		"widget.NewButton(",
		`SetText("direct button")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- generated ---\n%s", want, out)
		}
	}

	// The bug's signature: every widget id an Add() names must have been
	// assigned somewhere. A dropped node leaves the Add behind.
	added := regexp.MustCompile(`\.Add\((m\.__n\d+|__n\d+)\)`)
	for _, m := range added.FindAllStringSubmatch(out, -1) {
		ref := m[1]
		if !strings.Contains(out, ref+" = ") && !strings.Contains(out, ref+" := ") {
			t.Errorf("%s is added to a container but never created — issue #120\n--- generated ---\n%s", ref, out)
		}
	}
	if !added.MatchString(out) {
		t.Fatalf("fixture produced no Add() call, so it asserts nothing:\n%s", out)
	}
}
