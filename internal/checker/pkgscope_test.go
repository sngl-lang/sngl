package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// parsePkg parses one package's files, named so a diagnostic says which file a
// declaration came from.
func parsePkg(t *testing.T, srcs ...string) []*ast.Document {
	t.Helper()
	docs := make([]*ast.Document, 0, len(srcs))
	for i, src := range srcs {
		name := string(rune('a'+i)) + ".sngl"
		doc, err := parser.Parse(name, []byte(src))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		docs = append(docs, doc)
	}
	return docs
}

func pkgCheckErrors(docs []*ast.Document) []string {
	_, diags := checker.CheckPackage(docs, &checker.Config{IsMain: true})
	var out []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			out = append(out, d.Error())
		}
	}
	return out
}

// TestRedeclaredAcrossFiles covers every declaration kind that binds a
// package-wide name. A method is not one of them: its name lives under its
// receiver, and declareMethod rules on it there.
func TestRedeclaredAcrossFiles(t *testing.T) {
	cases := []struct{ kind, a, b, want string }{
		{"struct", "struct N {\n    x int\n}", "struct N {\n    y int\n}", ""},
		{"enum", "enum N { p, q }", "enum N { r, s }", ""},
		{"unit", "unit N { px }", "unit N { em }", ""},
		{"func", "func N() => 1", "func N() => 2", ""},
		{"const", "const N = 1", "const N = 2", ""},
		{"component", "component N node {\n    text(value=\"a\")\n}", "component N node {\n    text(value=\"b\")\n}", ""},
		// A top-level var is caught before claimTopLevel sees it, by the
		// package-scope lookup registerVars does for the component-body case.
		{"var", "var N int = 1", "var N int = 2", `duplicate declaration of "N"`},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			const head = "import . \"sngl:ui\"\n\n"
			errs := pkgCheckErrors(parsePkg(t, head+tc.a+"\n", head+tc.b+"\n"))
			if len(errs) != 1 {
				t.Fatalf("want one diagnostic, got %d: %v", len(errs), errs)
			}
			got := errs[0]
			wants := []string{"b.sngl:", `"N" redeclared in this package`, "previous declaration at a.sngl:"}
			if tc.want != "" {
				wants = []string{"b.sngl:", tc.want}
			}
			for _, want := range wants {
				if !strings.Contains(got, want) {
					t.Errorf("diagnostic %q does not contain %q", got, want)
				}
			}
		})
	}
}

// TestCrossFileDeclarationsResolve is the other half: a sibling file's
// declaration is visible, and reporting a redeclaration must not cost that.
func TestCrossFileDeclarationsResolve(t *testing.T) {
	errs := pkgCheckErrors(parsePkg(t,
		"struct Style {\n    x int\n}\n",
		"import . \"sngl:ui\"\n\nfunc widthOf(s Style) int {\n    return s.x\n}\n\ncomponent main node {\n    text(value=string(widthOf(Style{x = 7})))\n}\n",
	))
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
}

// TestShadowingSurvivesPackageScope: a declaration shadowing a dot-imported
// name is one declaration plus an import, in whichever file each is written.
func TestShadowingSurvivesPackageScope(t *testing.T) {
	errs := pkgCheckErrors(parsePkg(t,
		"import . \"sngl:ui\"\n\ncomponent text(value string) node {\n    button(text=value)\n}\n",
		"import . \"sngl:ui\"\n\ncomponent main node {\n    text(value=\"mine\")\n}\n",
	))
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
}

// TestRejectedFileClaimReservesNothing: a declaration whose file-scope claim
// fails binds nothing, so it must not hold the package name against a sibling
// that binds fine. Asserts the whole diagnostic set — a regression shows up as
// a second and third diagnostic, not a wrong first one, which is why this is
// not a `sngl check` fixture: the CLI prints only the first.
func TestRejectedFileClaimReservesNothing(t *testing.T) {
	errs := pkgCheckErrors(parsePkg(t,
		"import N \"sngl:ui\"\n\nstruct N {\n    x int\n}\n",
		"import . \"sngl:ui\"\n\nstruct N {\n    y int\n}\n\nfunc useN(s N) int {\n    return s.y\n}\n",
	))
	if len(errs) != 1 {
		t.Fatalf("want one diagnostic, got %d: %v", len(errs), errs)
	}
	for _, want := range []string{"a.sngl:3:1:", `"N" is already bound at file scope by an import alias`} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("diagnostic %q does not contain %q", errs[0], want)
		}
	}
}
