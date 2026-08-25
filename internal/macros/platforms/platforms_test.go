package platforms

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

func TestMarksStruct(t *testing.T) {
	stmt, diags := expandOne(t, `import . "sngl://platforms"

#[options]
struct Knobs {
    gadget string = ""
}`)
	if msg := firstError(diags); msg != "" {
		t.Fatalf("unexpected diagnostic: %s", msg)
	}
	sd, ok := stmt.(*ast.StructDef)
	if !ok {
		t.Fatalf("got %T, want *ast.StructDef", stmt)
	}
	if !sd.Options {
		t.Error("Options = false, want the mark recorded")
	}
}

// The alias is an ordinary file-scope binding, so a qualified import spells
// the mark through it.
func TestMarksThroughAlias(t *testing.T) {
	stmt, diags := expandOne(t, `import p "sngl://platforms"

#[p.options]
struct Knobs {
    gadget string = ""
}`)
	if msg := firstError(diags); msg != "" {
		t.Fatalf("unexpected diagnostic: %s", msg)
	}
	if sd := stmt.(*ast.StructDef); !sd.Options {
		t.Error("Options = false, want the mark recorded")
	}
}

// An option is a named, typed field with a default, so a struct is the only
// form the mark has a design for.
func TestRefusesNonStruct(t *testing.T) {
	for _, decl := range []string{
		"enum E {\n    a\n    b\n}",
		"const c = 1",
		"var v = 1",
		"func f() => 1",
		"component main {\n}",
	} {
		_, diags := expandOne(t, "import . \"sngl://platforms\"\n\n#[options]\n"+decl)
		if msg := firstError(diags); !strings.Contains(msg, "cannot mark") {
			t.Errorf("%q: diagnostic = %q, want the form refused", decl, msg)
		}
	}
}

// The mark says what the declaration is, and a declaration is one thing.
func TestRefusesSecondMark(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://platforms"

#[options]
#[options]
struct Knobs {
    gadget string = ""
}`)
	if msg := firstError(diags); !strings.Contains(msg, "already marked") {
		t.Errorf("diagnostic = %q, want the second mark refused", msg)
	}
}

// The mark is not ambient: it resolves only where the package is imported.
func TestRequiresImport(t *testing.T) {
	_, diags := expandOne(t, `#[options]
struct Knobs {
    gadget string = ""
}`)
	if msg := firstError(diags); !strings.Contains(msg, "unknown macro") {
		t.Errorf("diagnostic = %q, want the unimported mark refused", msg)
	}
}
