package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A target package's components and overrides are inlined into every program
// that renders them, so each must be const: written, or inherited from a const
// base. And being const, each render is held to the const rule, reported where
// the target wrote it. This is what passInlinePure's strict mode used to find
// out at lowering, with no position.
func TestTargetPackageComponentsAreConst(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{{
		name: "override not const",
		src: `import sngl "sngl:ui"

component sngl.text[tgtstub.platform] {
    sngl.vbox {}
}
`,
		want: "override sngl.text[tgtstub] in a target package must be const",
	}, {
		name: "component not const",
		src: `import sngl "sngl:ui"

component Wrap(value string) sngl.node {
    sngl.text(value=value)
}
`,
		want: "component Wrap in a target package must be const",
	}, {
		name: "const render reads state",
		src: `import sngl "sngl:ui"

const component Wrap() sngl.node {
    var count int = 0
    sngl.text(value=string(count))
}
`,
		want: `const component Wrap renders var "count"`,
	}, {
		name: "clean",
		src: `import sngl "sngl:ui"

const component Wrap(value string) sngl.node {
    sngl.text(value=value)
}

const component sngl.text[tgtstub.platform] {
    sngl.vbox {}
}
`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			stub, err := parser.Parse("tgtstub.sngl", []byte(tc.src))
			if err != nil {
				t.Fatalf("stub parse: %v", err)
			}
			doc, err := parser.Parse("main.sngl", []byte("import . \"sngl:ui\"\nimport _ \"sngl:platform/tgtstub\"\n\ncomponent main node {\n    text(value=\"hi\")\n}\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, diags := checker.Check(doc, &checker.Config{
				IsMain:     true,
				Platforms:  []ir.Platform{tgtStubPlatform{}},
				LibSources: map[string][]*ast.Document{"platform/tgtstub": {stub}},
			})
			var errs []string
			for _, d := range diags {
				if d.Severity == ir.Error {
					errs = append(errs, d.Error())
				}
			}
			if tc.want == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			for _, e := range errs {
				if strings.Contains(e, tc.want) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.want, errs)
		})
	}
}
