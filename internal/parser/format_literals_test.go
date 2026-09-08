package parser_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	. "git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// stringLiterals lists the spelling of every string literal in a document, in
// walk order.
func stringLiterals(doc *ast.Document) []string {
	var out []string
	lspcore.WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if _, isString := ast.StringStyleOf(lit.Kind); isString {
			out = append(out, lit.Raw)
		}
	})
	return out
}

// A format must not rewrite a string. The AST holds a literal's spelling as
// written and only the checker decodes it, so the formatter reprints the raw.
func assertLiteralsSurviveFormat(t *testing.T, name, src string) {
	t.Helper()
	doc, err := Parse(name, []byte(src))
	if err != nil {
		return // parse failures are another test's business
	}
	formatted := Format(doc)
	reparsed, err := Parse(name, []byte(formatted))
	if err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, formatted)
		return
	}
	before, after := stringLiterals(doc), stringLiterals(reparsed)
	if len(before) != len(after) {
		t.Errorf("format changed the literal count: %d before, %d after", len(before), len(after))
		return
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("format rewrote a string literal:\nbefore: %q\nafter:  %q", before[i], after[i])
		}
	}
}

func TestFormatPreservesTestdataLiterals(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			assertLiteralsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}

func TestFormatPreservesDocLiterals(t *testing.T) {
	for s := range testutil.DocSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			assertLiteralsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}

// commentTexts lists every comment in a document, each tagged with what
// encloses it: the statement lists, a struct, enum or unit body, a parameter
// list, an i18n placeholder's cases. The tag is what makes a comment that
// merely moved distinguishable from one that stayed -- a text-only list
// compares equal when a body's comment is relocated to the document tail,
// since the order survives the move.
func commentTexts(doc *ast.Document) []string {
	var out []string
	var stmts func([]ast.Stmt)
	var expr func(ast.Expr)
	owner := "document"
	take := func(cs ...*ast.Comment) {
		for _, c := range cs {
			if c != nil {
				out = append(out, owner+"|"+c.Text)
			}
		}
	}
	within := func(label string, f func()) {
		saved := owner
		owner = label
		f()
		owner = saved
	}
	props := func(pl ast.PropList) {
		for _, p := range pl.Props {
			switch v := p.(type) {
			case ast.Param:
				take(v.Leading...)
				take(v.Trailing)
			case ast.EventDecl:
				take(v.Leading...)
				take(v.Trailing)
			}
		}
	}
	params := func(pl ast.ParamList) {
		for _, p := range pl.Params {
			take(p.Leading...)
			take(p.Trailing)
		}
	}
	expr = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.I18nInterpExpr:
			for _, p := range x.Parts {
				expr(p)
			}
		case *ast.I18nPlaceholderExpr:
			for _, c := range x.Cases {
				take(c.Leading...)
				for _, p := range c.Body {
					expr(p)
				}
			}
		}
	}
	stmts = func(list []ast.Stmt) {
		for _, s := range list {
			switch x := s.(type) {
			case *ast.Comment:
				take(x)
			case *ast.StructDef:
				within("struct "+x.Name, func() {
					for _, item := range x.Body {
						if c, ok := item.(*ast.Comment); ok {
							take(c)
						}
					}
				})
			case *ast.EnumDef:
				within("enum "+x.Name, func() {
					for _, item := range x.Body {
						if c, ok := item.(*ast.Comment); ok {
							take(c)
						}
					}
				})
			case *ast.UnitDef:
				within("unit "+x.Name, func() {
					for _, item := range x.Body {
						if c, ok := item.(*ast.Comment); ok {
							take(c)
						}
					}
				})
			case *ast.ComponentDecl:
				within("component "+x.Name, func() {
					props(x.Props)
					stmts(x.Body.Stmts)
				})
			case *ast.FuncDef:
				within("func "+x.Name, func() {
					params(x.Params)
					stmts(x.Block.Stmts)
				})
			case *ast.VisualNode:
				stmts(x.Block.Stmts)
				for _, a := range x.Args.Args {
					switch v := a.(type) {
					case ast.Arg:
						expr(v.Value)
					case ast.EventHandler:
						stmts(v.Body.Stmts)
					}
				}
			case *ast.IfStmt:
				stmts(x.Body.Stmts)
				stmts(x.Else.Stmts)
			case *ast.ForStmt:
				stmts(x.Body.Stmts)
				stmts(x.Else.Stmts)
			case *ast.VarDecl:
				for _, sp := range x.Specs {
					expr(sp.Default)
					for _, h := range sp.Handlers {
						stmts(h.Body.Stmts)
					}
				}
			case *ast.ConstDecl:
				for _, sp := range x.Specs {
					expr(sp.Default)
				}
			}
		}
	}
	stmts(doc.Stmts)
	return out
}

// A format must not move a comment away from what it describes. Comments used
// to survive only in statement lists — one written on a struct field, in a
// parameter list or among an i18n message's cases was hoisted out to the
// nearest enclosing statement list, which for a `// ERROR(check)` directive
// means it stops naming the line it was written on.
func assertCommentsSurviveFormat(t *testing.T, name, src string) {
	t.Helper()
	doc, err := Parse(name, []byte(src))
	if err != nil {
		return
	}
	formatted := Format(doc)
	reparsed, err := Parse(name, []byte(formatted))
	if err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, formatted)
		return
	}
	before, after := commentTexts(doc), commentTexts(reparsed)
	if !slices.Equal(before, after) {
		t.Errorf("format moved or dropped a comment:\nbefore: %q\nafter:  %q", before, after)
	}
}

// Every fixture is written the way `sngl fmt` writes it, so that a formatting
// change has to be looked at rather than discovered later as drift. A fixture
// carrying an ERROR(parse) directive is excluded: there is nothing to format.
// One whose exact layout is the thing under test opts out with
// `// NOFMT "reason"`, which this test then holds to that claim.
func TestTestdataIsFormatted(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			doc, err := Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %s", err)
			}
			got := Format(doc)
			switch {
			case s.NoFmt && got == s.Source:
				t.Errorf("NOFMT is stale (%s): the fixture is formatted, so drop the directive", s.NoFmtReason)
			case !s.NoFmt && got != s.Source:
				t.Errorf("fixture is not formatted; run `sngl fmt testdata`:\n%s", firstDiff(s.Source, got))
			}
			assertCommentsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}

// Formatting twice must be formatting once.
func TestFormatIsIdempotent(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			doc, err := Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			once := Format(doc)
			again, err := Parse(s.Filename, []byte(once))
			if err != nil {
				t.Fatalf("formatted output does not re-parse: %v", err)
			}
			if twice := Format(again); twice != once {
				t.Errorf("second format differs from the first:\n%s", firstDiff(once, twice))
			}
		})
	}
}

func TestFormatPreservesDocComments(t *testing.T) {
	for s := range testutil.DocSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			assertCommentsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}

// firstDiff reports the first line the two differ on, with a little context.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(wl), len(gl)) {
		w, g := "", ""
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n  have: %q\n  want: %q", i+1, w, g)
		}
	}
	return ""
}
