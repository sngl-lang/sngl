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

#[foreign("js://example.com/api", "Entry")]
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
	if want := (ast.ForeignMark{Scheme: "js", Path: "example.com/api", Name: "Entry"}); sd.Foreign != want {
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

// The mark's own shape is all it rejects: a mark with no arguments names
// nothing, so there is no name to set and nothing to proceed with.
func TestRefusesNoArguments(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

#[foreign()]
struct Entry {
    title string
}`)
	if msg := firstError(diags); !strings.Contains(msg, "at least 1 argument") {
		t.Errorf("diagnostic = %q, want the arity", msg)
	}
}

// A flag is a closed set, so a misspelled one is reported with the
// alternatives rather than silently doing nothing.
func TestRefusesUnknownFlag(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api", "add", puer)]
func add(a int, b int) => a + b`)
	msg := firstError(diags)
	if !strings.Contains(msg, `"puer"`) || !strings.Contains(msg, "pure, async") {
		t.Errorf("diagnostic = %q, want the alternatives", msg)
	}
}

// The flags state what a call costs. A declaration with no call to describe
// would carry them to no effect, so it refuses them instead.
func TestRefusesFlagsOnNonFunc(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api", "Entry", pure)]
struct Entry {
    title string
}`)
	if msg := firstError(diags); !strings.Contains(msg, "describes a call") {
		t.Errorf("diagnostic = %q, want the flag refused", msg)
	}
}

// The flags reach the mark, which is what carries them to the IR.
func TestMarksFuncFlags(t *testing.T) {
	stmt, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api", "add", pure, async)]
func add(a int, b int) => a + b`)
	if msg := firstError(diags); msg != "" {
		t.Fatalf("unexpected diagnostic: %s", msg)
	}
	fd, ok := stmt.(*ast.FuncDef)
	if !ok {
		t.Fatalf("got %T, want *ast.FuncDef", stmt)
	}
	want := ast.ForeignMark{Scheme: "js", Path: "example.com/api", Name: "add", Pure: true, Async: true}
	if fd.Foreign != want {
		t.Errorf("Foreign = %+v, want %+v", fd.Foreign, want)
	}
}

// The mark says what a declaration is outside SNGL, and a declaration is one
// thing, so a second mark is an error rather than an overwrite.
func TestRefusesSecondMark(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api", "Entry")]
#[foreign("js://example.com/other", "Other")]
struct Entry {
    title string
}`)
	if msg := firstError(diags); !strings.Contains(msg, "already marked") {
		t.Errorf("diagnostic = %q, want the second mark refused", msg)
	}
}

// A struct, its fields and a function are the forms the feature specifies.
// The rest refuse the mark rather than carry it to a backend that would do
// something unspecified with it.
func TestRefusesUnspecifiedForms(t *testing.T) {
	for _, decl := range []string{
		"enum E { a\n    b\n}",
		"const c = 1",
		"var v = 1",
	} {
		_, diags := expandOne(t, `import . "sngl://std"

#[foreign("js://example.com/api", "X")]
`+strings.ReplaceAll(decl, "\\n", "\n"))
		if msg := firstError(diags); !strings.Contains(msg, "cannot mark") {
			t.Errorf("%s: diagnostic = %q, want the form refused", decl, msg)
		}
	}
}
