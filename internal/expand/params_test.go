package expand_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/ir"
)

func msgs(diags []ir.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Msg)
	}
	return out
}

// Every mark on a parameter is refused: no macro may consume one yet, so a
// resolvable macro is as much an error as an unknown one.
func TestExpandPre_ParamAttrRefused(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"func param, real macro",
			"import \"sngl://draw\"\nfunc f(#[draw.shape] a int) => a",
			"#[draw.shape] cannot mark a parameter",
		},
		{
			"component prop, real macro",
			"import \"sngl://draw\"\ncomponent c(#[draw.shape] a int) {}",
			"#[draw.shape] cannot mark a parameter",
		},
		{
			"binding prop",
			"import \"sngl://draw\"\ncomponent c(#[draw.shape] :a int) {}",
			"#[draw.shape] cannot mark a parameter",
		},
		{
			"lambda param nested in a component body",
			"import \"sngl://draw\"\ncomponent c() {\n    var f = func(#[draw.shape] a int) => a\n}",
			"#[draw.shape] cannot mark a parameter",
		},
		{
			"method param in a struct body",
			"import \"sngl://draw\"\nstruct S {\n    func m(#[draw.shape] a int) => a\n}",
			"#[draw.shape] cannot mark a parameter",
		},
		// The resolution failure keeps its statement-position wording.
		{
			"unknown macro package",
			"func f(#[nosuch.mark] a int) => a",
			`unknown macro package "nosuch"`,
		},
		{
			"known package, unknown macro",
			"import \"sngl://draw\"\nfunc f(#[draw.nosuch] a int) => a",
			`unknown macro "nosuch" in package "draw"`,
		},
		{
			"unqualified, no dot-imported macro package",
			"func f(#[nosuch] a int) => a",
			`unknown macro "nosuch": no dot-imported macro package declares it`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseDoc(t, tt.src)
			diags := expand.ExpandPre([]*ast.Document{doc})
			if len(diags) != 1 {
				t.Fatalf("diagnostics = %v, want exactly 1", msgs(diags))
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("diagnostic = %q, want it to contain %q", diags[0].Msg, tt.want)
			}
			if diags[0].Severity != ir.Error {
				t.Errorf("severity = %v, want Error", diags[0].Severity)
			}
		})
	}
}

// A parameter with no mark stays silent, and a marked one does not stop the
// rest of the list from being reported.
func TestExpandPre_ParamAttrsReportEachMark(t *testing.T) {
	doc := parseDoc(t, "import \"sngl://draw\"\nfunc f(a int, #[draw.shape] b int, #[draw.shape] c int) => a")
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 2 {
		t.Fatalf("diagnostics = %v, want 2", msgs(diags))
	}
}

// A mark inside a disabled declaration is in code that never compiles.
func TestExpandPre_ParamAttrInDisabledDecl(t *testing.T) {
	doc := parseDoc(t, "import \"sngl://draw\"\n/- func f(#[draw.shape] a int) => a")
	if diags := expand.ExpandPre([]*ast.Document{doc}); len(diags) != 0 {
		t.Fatalf("diagnostics = %v, want none", msgs(diags))
	}
}
