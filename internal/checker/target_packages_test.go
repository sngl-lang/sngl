package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A target's library package is loaded as though the document had written
// `import _ "sngl:platform/<it>"`, and loading it checks the overrides it
// declares. So whether a package loaded is observable as whether a fault inside
// one of its overrides is reported -- which is the point of loading it, and the
// only thing that distinguishes "loaded" from "named in an import statement".
//
// tgtstub's override is deliberately broken: its body reads a name nothing
// declares. Every case below is the same document checked three ways, and the
// diagnostic is present exactly when the package is in the target set.
const tgtStubSource = `import sngl "sngl:ui"

component sngl.text[tgtstub.platform] {
    sngl.text(value=noSuchIdentifierAnywhere)
}
`

type tgtStubPlatform struct{}

func (tgtStubPlatform) PlatformIdentifier() string { return "tgtstub" }
func (tgtStubPlatform) Description() string        { return "target-set test stub" }

func tgtStubConfig(t *testing.T, targets ...ir.StaticTarget) *checker.Config {
	t.Helper()
	doc, err := parser.Parse("tgtstub.sngl", []byte(tgtStubSource))
	if err != nil {
		t.Fatalf("stub parse: %v", err)
	}
	return &checker.Config{
		IsMain:     true,
		Platforms:  []ir.Platform{tgtStubPlatform{}},
		Targets:    targets,
		LibSources: map[string][]*ast.Document{"platform/tgtstub": {doc}},
	}
}

// brokenOverrideReported checks src and reports whether the stub's override
// was checked, which happens only when its package is in the target set.
func brokenOverrideReported(t *testing.T, src string, targets ...ir.StaticTarget) bool {
	t.Helper()
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, tgtStubConfig(t, targets...))
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "noSuchIdentifierAnywhere") {
			return true
		}
	}
	return false
}

const plainMain = `import . "sngl:ui"

output {
    none { html }
}

component main ui {
    text(value="hi")
}
`

const importsStub = `import . "sngl:ui"
import _ "sngl:platform/tgtstub"

output {
    none { html }
}

component main ui {
    text(value="hi")
}
`

func TestTargetPackages(t *testing.T) {
	t.Run("a target the caller named is loaded", func(t *testing.T) {
		if !brokenOverrideReported(t, plainMain, ir.StaticTarget{Platform: "tgtstub"}) {
			t.Error("building for tgtstub did not check its overrides")
		}
	})

	t.Run("a platform nobody named stays out of it", func(t *testing.T) {
		// The document declares html and names tgtstub nowhere, so tgtstub's
		// faults are not this build's. Before targeting, every registered
		// platform merged and this reported.
		if brokenOverrideReported(t, plainMain, ir.StaticTarget{Platform: "html"}) {
			t.Error("an html build checked tgtstub's overrides")
		}
	})

	t.Run("a flag overrides what output declares", func(t *testing.T) {
		// The direction that discriminates: the document declares tgtstub and
		// the caller names html. Overriding means tgtstub is not in the set, so
		// its faults are not reported. Adding the two together instead would
		// load it and report -- which is what this catches.
		const declaresStub = `import . "sngl:ui"

output {
    none { tgtstub }
}

component main ui {
    text(value="hi")
}
`
		if brokenOverrideReported(t, declaresStub, ir.StaticTarget{Platform: "html"}) {
			t.Error("the declared output was added to the named target rather than overridden by it")
		}
		// And with nothing named, the declared output is what loads.
		if !brokenOverrideReported(t, declaresStub) {
			t.Error("the declared output did not load when the caller named nothing")
		}
	})

	t.Run("an explicit import is not overridden by a flag", func(t *testing.T) {
		// Building for html, which would leave tgtstub out -- except the
		// program imported it, which is it asking to be held to those rules
		// without naming one of that platform's declarations.
		if !brokenOverrideReported(t, importsStub, ir.StaticTarget{Platform: "html"}) {
			t.Error("an explicit import did not load the platform's package")
		}
	})

	t.Run("a replace decides which package an import names", func(t *testing.T) {
		// registerImport resolves `=>` before it looks at the scheme, so this
		// has to as well: an import redirected at a target package names it,
		// and one redirected away from a target package does not.
		const intoStub = `import . "sngl:ui"
import _ "sngl:platform/nowhere" => "sngl:platform/tgtstub"

component main ui {
    text(value="hi")
}
`
		if !brokenOverrideReported(t, intoStub, ir.StaticTarget{Platform: "html"}) {
			t.Error("an import replaced with a target package did not load it")
		}

		const awayFromStub = `import . "sngl:ui"
import _ "sngl:platform/tgtstub" => "sngl:ui"

component main ui {
    text(value="hi")
}
`
		if brokenOverrideReported(t, awayFromStub, ir.StaticTarget{Platform: "html"}) {
			t.Error("an import replaced away from a target package still loaded it")
		}
	})

	t.Run("naming nothing loads everything", func(t *testing.T) {
		// No flag, no output block, no import: there is no build to restrict
		// to, so every registered platform loads -- what a bare check wants.
		const noOutput = "import . \"sngl:ui\"\n\ncomponent main {\n    text(value=\"hi\")\n}\n"
		if !brokenOverrideReported(t, noOutput) {
			t.Error("a check naming no target did not load the registered platforms")
		}
	})
}
