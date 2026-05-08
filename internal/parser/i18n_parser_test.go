package parser_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestParseI18nFullString(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte(`var x = $"Login"`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	if ie.Style != ast.StyleDouble {
		t.Errorf("Style = %v, want StyleDouble", ie.Style)
	}
	if len(ie.Parts) != 1 {
		t.Fatalf("Parts len = %d, want 1", len(ie.Parts))
	}
	lit, ok := ie.Parts[0].(*ast.LiteralExpr)
	if !ok {
		t.Fatalf("Part 0: got %T, want *LiteralExpr", ie.Parts[0])
	}
	if lit.Raw != "Login" {
		t.Errorf("Raw = %q, want Login", lit.Raw)
	}
}

func TestParseI18nSimpleInterpolation(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte(`var x = $"Hello {name}!"`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	// Parts: "Hello " | placeholder | "!"
	if len(ie.Parts) != 3 {
		t.Fatalf("Parts len = %d, want 3", len(ie.Parts))
	}
	ph, ok := ie.Parts[1].(*ast.I18nPlaceholderExpr)
	if !ok {
		t.Fatalf("Part 1: got %T, want *I18nPlaceholderExpr", ie.Parts[1])
	}
	ident, ok := ph.Value.(*ast.IdentExpr)
	if !ok || ident.Name != "name" {
		t.Errorf("placeholder value: got %T %v, want IdentExpr 'name'", ph.Value, ph.Value)
	}
	if ph.Type != "" {
		t.Errorf("Type = %q, want empty", ph.Type)
	}
	if len(ph.Cases) != 0 {
		t.Errorf("Cases len = %d, want 0", len(ph.Cases))
	}
}

func TestParseI18nPlural(t *testing.T) {
	src := `var x = $"You have {count, plural, one{message} other{messages}}"`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	var ph *ast.I18nPlaceholderExpr
	for _, p := range ie.Parts {
		if e, ok := p.(*ast.I18nPlaceholderExpr); ok {
			ph = e
		}
	}
	if ph == nil {
		t.Fatal("expected a placeholder with cases")
	}
	if ph.Type != "plural" {
		t.Errorf("Type = %q, want plural", ph.Type)
	}
	if len(ph.Cases) != 2 {
		t.Fatalf("Cases len = %d, want 2", len(ph.Cases))
	}
	if ph.Cases[0].Selector != "one" {
		t.Errorf("Cases[0].Selector = %q, want one", ph.Cases[0].Selector)
	}
	if ph.Cases[1].Selector != "other" {
		t.Errorf("Cases[1].Selector = %q, want other", ph.Cases[1].Selector)
	}
}

func TestParseI18nTriple(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte(`var x = $"""hello"""`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	if ie.Style != ast.StyleTriple {
		t.Errorf("Style = %v, want StyleTriple", ie.Style)
	}
	if len(ie.Parts) != 1 {
		t.Fatalf("Parts len = %d, want 1", len(ie.Parts))
	}
	lit, ok := ie.Parts[0].(*ast.LiteralExpr)
	if !ok {
		t.Fatalf("Part 0: got %T, want *LiteralExpr", ie.Parts[0])
	}
	if lit.Raw != "hello" {
		t.Errorf("Raw = %q, want hello", lit.Raw)
	}
}

func TestParseI18nEqSelector(t *testing.T) {
	src := `var x = $"{count, plural, =0{no messages} =1{one message} other{many messages}}"`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	var ph *ast.I18nPlaceholderExpr
	for _, p := range ie.Parts {
		if e, ok := p.(*ast.I18nPlaceholderExpr); ok {
			ph = e
		}
	}
	if ph == nil {
		t.Fatal("expected a placeholder with cases")
	}
	if ph.Type != "plural" {
		t.Errorf("Type = %q, want plural", ph.Type)
	}
	if len(ph.Cases) != 3 {
		t.Fatalf("Cases len = %d, want 3", len(ph.Cases))
	}
	if ph.Cases[0].Selector != "=0" {
		t.Errorf("Cases[0].Selector = %q, want =0", ph.Cases[0].Selector)
	}
	if ph.Cases[1].Selector != "=1" {
		t.Errorf("Cases[1].Selector = %q, want =1", ph.Cases[1].Selector)
	}
	if ph.Cases[2].Selector != "other" {
		t.Errorf("Cases[2].Selector = %q, want other", ph.Cases[2].Selector)
	}
}

func TestParseI18nTripleInterpolation(t *testing.T) {
	src := `var x = $"""Hello {name}!"""`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd := doc.Stmts[0].(*ast.VarDecl)
	ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
	if !ok {
		t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
	}
	if ie.Style != ast.StyleTriple {
		t.Errorf("Style = %v, want StyleTriple", ie.Style)
	}
	if len(ie.Parts) != 3 {
		t.Fatalf("Parts len = %d, want 3", len(ie.Parts))
	}
	if _, ok := ie.Parts[1].(*ast.I18nPlaceholderExpr); !ok {
		t.Errorf("Part 1: got %T, want *I18nPlaceholderExpr", ie.Parts[1])
	}
}
