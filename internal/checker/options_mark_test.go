package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// optStubPlatform and optStubLang are a target pair whose option schemas are
// handed to the checker through Config.LibSources, so a fixture can say what
// no lib/ package would: an options struct not called Options, next to a
// struct called Options that is not one.
type optStubPlatform struct{}

func (optStubPlatform) PlatformIdentifier() string { return "optstub" }
func (optStubPlatform) Description() string        { return "options-mark test stub" }
func (optStubPlatform) Resolve(string) ir.Symbol   { return nil }

type optStubLang struct{}

func (optStubLang) LanguageIdentifier() string { return "optlang" }
func (optStubLang) Description() string        { return "options-mark test stub" }
func (optStubLang) Resolve(string) ir.Symbol   { return nil }

// The marked struct is named Knobs and the decoy is named Options, so a lookup
// that still matched the name would find exactly the wrong one.
const optStubPlatformSource = `
import . "sngl://platforms"

#[options]
struct Knobs {
    gadget string = ""
}

struct Options {
    decoy string = ""
}
`

const optStubLangSource = `
import . "sngl://platforms"

#[options]
struct Dials {
    lever string = ""
}
`

func optStubConfig(t *testing.T) *checker.Config {
	t.Helper()
	parse := func(name, src string) *ast.Document {
		doc, err := parser.Parse(name, []byte(src))
		if err != nil {
			t.Fatalf("%s parse: %v", name, err)
		}
		return doc
	}
	pdoc := parse("optstub.sngl", optStubPlatformSource)
	ldoc := parse("optlang.sngl", optStubLangSource)
	return &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{optStubPlatform{}},
		Languages: []ir.Language{optStubLang{}},
		LibSources: map[string][]*ast.Document{
			"platforms/optstub": {pdoc},
			"languages/optlang": {ldoc},
		},
	}
}

func checkOptStub(t *testing.T, output string) []string {
	t.Helper()
	src := withStd(output + "\ncomponent main {\n    text(value=\"hi\")\n}\n")
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, optStubConfig(t))
	var errs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d.Msg)
		}
	}
	return errs
}

// The mark, not the name, is what an options lookup keys on: a field of the
// marked struct is a valid option under either the platform's or the
// language's schema.
func TestOptionsMarkedStructIsTheSchema(t *testing.T) {
	errs := checkOptStub(t, `output {
    optlang {
        optstub(gadget="g", lever="l")
    }
}`)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// The decoy is a struct called Options with no mark, so its fields are not
// options.
func TestOptionsUnmarkedStructIsNotTheSchema(t *testing.T) {
	errs := checkOptStub(t, `output {
    optlang {
        optstub(decoy="d")
    }
}`)
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, `unknown option "decoy"`) {
		t.Errorf("errors = %v, want the unmarked struct's field rejected", errs)
	}
}
