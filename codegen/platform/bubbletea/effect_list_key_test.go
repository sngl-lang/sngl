package bubbletea

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A list is not a key, and the program that tries to use one does not reach
// this platform at all.
//
// `effect<T = struct {}>` constrains T to nothing, so `on` may be written as a
// list -- and `==` on one does not mean the same thing on every target. Go will
// not compile `!=` on a slice; JS compares two objects by identity, so a
// freshly built one is never the one already running. What this used to assert
// was the fallback that lived here instead: a key the targets do not compare
// alike was rebuilt on every settle, wrong the same way everywhere, and the
// claim was that the program at least ran.
//
// It does not run any more. A struct compares field by field on every target,
// so a compound key is a struct of the values and a list of them is refused
// where it is written -- see testdata/error_effect_key.sngl for the whole
// vocabulary of that rule. This is the bubbletea-shaped half: the refusal
// happens with the platform registered, before any lowering, so no backend has
// to hold an answer for a key it cannot compare.
const effectListKeySrc = `
import . "sngl:ui"

window {
    var (
        tags list<string> = ["a"]
        log list<string> = []
    )

    effect(
        on=tags,
        @mount {
            log.push("m")
        },
        @unmount {
            log.push("u")
        },
    )

    button #bump(text="+", @click {
        tags = ["a"]
    })
    text #out(value=log.join(","))
}
`

func TestAListKeyedEffectIsRefused(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(effectListKeySrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	_, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: plats,
		Languages: []ir.Language{lang},
		Targets:   []ir.StaticTarget{{Platform: "bubbletea", Language: "go"}},
	})
	var got string
	for _, d := range diags {
		if d.Severity == ir.Error {
			got = d.Msg
			break
		}
	}
	if got == "" {
		t.Fatal("a list-keyed effect checked clean")
	}
	if !strings.Contains(got, "a struct of them, not a list") {
		t.Errorf("diagnostic = %q, want it to name the struct a compound key is", got)
	}
}
