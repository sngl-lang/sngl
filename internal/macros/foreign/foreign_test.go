package foreign

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func expandOne(t *testing.T, src string) (ast.Stmt, []ir.Diagnostic) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(doc.Stmts) == 0 {
		t.Fatalf("no statements after expand")
	}
	return doc.Stmts[len(doc.Stmts)-1], diags
}

func firstError(diags []ir.Diagnostic) string {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return d.Msg
		}
	}
	return ""
}

func TestMarksStructAndField(t *testing.T) {
	stmt, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api#Entry")]
struct Entry {
    #[foreign("Title")]
    title string
}`)
	if msg := firstError(diags); msg != "" {
		t.Fatalf("unexpected diagnostic: %s", msg)
	}
	sd, ok := stmt.(*ast.StructDef)
	if !ok {
		t.Fatalf("got %T, want *ast.StructDef", stmt)
	}
	if want := (ast.ForeignMark{Scheme: "js", Pkg: "example.com/api", Name: "Entry"}); sd.Foreign != want {
		t.Errorf("struct Foreign = %+v, want %+v", sd.Foreign, want)
	}
	fields := sd.Fields()
	if len(fields) != 1 {
		t.Fatalf("got %d fields, want 1 (the attribute wrapper should be unwrapped)", len(fields))
	}
	// A bare name is the field form: there is no package a field belongs to.
	if want := (ast.ForeignMark{Name: "Title"}); fields[0].Foreign != want {
		t.Errorf("field Foreign = %+v, want %+v", fields[0].Foreign, want)
	}
}

func TestRefusesSeveralNames(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

struct Entry {
    #[foreign("Title")]
    title, subtitle string
}`)
	if msg := firstError(diags); !strings.Contains(msg, "2 names") {
		t.Errorf("diagnostic = %q, want it to refuse two names at once", msg)
	}
}

func TestRefusesMalformedRef(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api")]
struct Entry {
    title string
}`)
	if msg := firstError(diags); !strings.Contains(msg, "scheme://package#Name") {
		t.Errorf("diagnostic = %q, want the expected spelling", msg)
	}
}
