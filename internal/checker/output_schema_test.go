package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// optStubPlatform and optStubLang are a target pair whose packages are handed
// to the checker through Config.LibSources, so a fixture can say what no real
// target would: a decoy struct beside the node that is the schema.
type optStubPlatform struct{}

func (optStubPlatform) PlatformIdentifier() string { return "optstub" }
func (optStubPlatform) Description() string        { return "output-schema test stub" }
func (optStubPlatform) Resolve(string) ir.Symbol   { return nil }

type optStubLang struct{}

func (optStubLang) LanguageIdentifier() string { return "optlang" }
func (optStubLang) Description() string        { return "output-schema test stub" }
func (optStubLang) Resolve(string) ir.Symbol   { return nil }

// The decoy is a struct called Options, which is what a target's schema used
// to be found by. It declares a field no option should answer to.
const optStubPlatformSource = `
import build "sngl:build"
import gen "sngl:x/gen"

struct Options {
    decoy string = ""
}

#[gen.name("optstub")]
component platform(gadget string, knob string = "fallback") build.platform {}
`

const optStubLangSource = `
import build "sngl:build"
import gen "sngl:x/gen"

#[gen.name("optlang")]
component language(lever string, platforms ...component build.platform) build.language {}
`

// A declared default is part of the schema, so it reaches the record a build
// reads without being written at the call site. Merging a target's schema with
// what the call site wrote by hand is what prop defaults replaced.
func TestADeclaredDefaultReachesTheOptions(t *testing.T) {
	src := withStd("output {\n    optlang {\n        optstub(gadget=\"g\")\n    }\n}\n\ncomponent main node {\n    text(value=\"hi\")\n}\n")
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, optStubConfig(t))
	if len(pkg.Outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(pkg.Outputs))
	}
	var got string
	for _, f := range pkg.Outputs[0].Options.Fields {
		if f.Name != "knob" {
			continue
		}
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			t.Fatalf("knob = %T, want a literal", f.Value)
		}
		got = lit.Value
	}
	if got != "fallback" {
		t.Errorf("knob = %q, want the declared default", got)
	}
}

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
			"platform/optstub": {pdoc},
			"language/optlang": {ldoc},
		},
	}
}

func checkOptStub(t *testing.T, output string) []string {
	t.Helper()
	src := withStd(output + "\ncomponent main node {\n    text(value=\"hi\")\n}\n")
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

// A target node's props are its option schema, and each level of the tree
// carries its own: the language's on the language, the platform's on the
// platform.
func TestTargetNodePropsAreTheSchema(t *testing.T) {
	errs := checkOptStub(t, `output {
    optlang(lever="l") {
        optstub(gadget="g")
    }
}`)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// A struct called Options is an ordinary declaration now, so its fields are
// not options.
func TestStructNamedOptionsIsNotTheSchema(t *testing.T) {
	errs := checkOptStub(t, `output {
    optlang {
        optstub(decoy="d")
    }
}`)
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "decoy") {
		t.Errorf("errors = %v, want the decoy struct's field rejected", errs)
	}
}

// An option belongs to the level that declares it: the language's written on
// the platform is an unknown prop, not a merge.
func TestOptionMustBeWrittenAtItsOwnLevel(t *testing.T) {
	errs := checkOptStub(t, `output {
    optlang {
        optstub(lever="l")
    }
}`)
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "lever") {
		t.Errorf("errors = %v, want the language option rejected on the platform", errs)
	}
}
