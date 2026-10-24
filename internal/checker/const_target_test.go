package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A target package's own components are inlined into every program that
// renders them, so each must be const, and a const render is held to the const
// rule where the target wrote it. An override is a body like a program's: it
// may hold state, and is held to the rule only where it or its base says
// const.
func TestTargetPackageComponentsAreConst(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{{
		name: "override holds state",
		src: `import sngl "sngl:ui"

component sngl.text[tgtstub.platform] {
    var count int = 0
    sngl.vbox {
        sngl.button(text=string(count), @click { count = count + 1 })
    }
}
`,
	}, {
		name: "const override reads state",
		src: `import sngl "sngl:ui"

const component sngl.text[tgtstub.platform] {
    var count int = 0
    sngl.button(text=string(count))
}
`,
		want: `const component sngl.text[tgtstub] renders var "count"`,
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
				LibSources: map[string][]*ast.Document{"platform/tgtstub": {stub, targetNodeDoc(t, "platform/tgtstub")}},
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
