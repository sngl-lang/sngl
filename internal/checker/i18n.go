package checker

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// templateBuilder walks an I18nInterpExpr to produce the canonical ICU
// MessageFormat string used at runtime, alongside the (name, expr) bindings
// for the placeholder values. SNGL exprs that are not bare identifiers
// become synthetic argN names; the args slice tracks bindings.
type templateBuilder struct {
	sb   []byte
	args []bindArg
	next int
}

type bindArg struct {
	name string
	expr ast.Expr
}

func (b *templateBuilder) addBareName(name string, expr ast.Expr) string {
	b.args = append(b.args, bindArg{name: name, expr: expr})
	return name
}

func (b *templateBuilder) addSynth(expr ast.Expr) string {
	name := fmt.Sprintf("arg%d", b.next)
	b.next++
	b.args = append(b.args, bindArg{name: name, expr: expr})
	return name
}

func (b *templateBuilder) writeParts(parts []ast.Expr) {
	for _, p := range parts {
		switch v := p.(type) {
		case *ast.LiteralExpr:
			b.sb = append(b.sb, v.Raw...)
		case *ast.I18nPlaceholderExpr:
			b.writePlaceholder(v)
		}
	}
}

func (b *templateBuilder) writePlaceholder(ph *ast.I18nPlaceholderExpr) {
	b.sb = append(b.sb, '{')
	var name string
	if id, ok := ph.Value.(*ast.IdentExpr); ok {
		name = b.addBareName(id.Name, ph.Value)
	} else {
		name = b.addSynth(ph.Value)
	}
	b.sb = append(b.sb, name...)
	if ph.Type != "" {
		b.sb = append(b.sb, ',', ' ')
		b.sb = append(b.sb, ph.Type...)
	}
	if len(ph.Cases) > 0 {
		b.sb = append(b.sb, ',', ' ')
		for i, c := range ph.Cases {
			if i > 0 {
				b.sb = append(b.sb, ' ')
			}
			b.sb = append(b.sb, c.Selector...)
			b.sb = append(b.sb, '{')
			b.writeParts(c.Body)
			b.sb = append(b.sb, '}')
		}
	}
	b.sb = append(b.sb, '}')
}

// icuTypeKeywords lists valid second-position identifiers in placeholders.
var icuTypeKeywords = map[string]struct{}{
	"plural": {}, "selectordinal": {}, "select": {},
	"date": {}, "time": {}, "number": {},
}

// pluralSelectors are the valid CLDR keyword selectors for plural/selectordinal.
var pluralSelectors = map[string]struct{}{
	"zero": {}, "one": {}, "two": {}, "few": {}, "many": {}, "other": {},
}

// validPluralSelector checks "zero"/"one"/.../"other" or "=N" form.
func validPluralSelector(s string) bool {
	if _, ok := pluralSelectors[s]; ok {
		return true
	}
	if len(s) > 1 && s[0] == '=' {
		for _, r := range s[1:] {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// checkI18nPlaceholder type-checks Value and validates Type/Cases.
func (c *checker) checkI18nPlaceholder(ph *ast.I18nPlaceholderExpr) {
	valExpr := c.checkExpr(ph.Value)
	vT := exprType(valExpr)
	if ph.Type == "" {
		return
	}
	if _, ok := icuTypeKeywords[ph.Type]; !ok {
		c.error(ph.Pos, "unknown ICU type %q in placeholder", ph.Type)
		return
	}
	switch ph.Type {
	case "plural", "selectordinal":
		if vT != nil && vT.Kind != ir.TypeInt && vT.Kind != ir.TypeFloat && vT.Kind != ir.TypeDyn {
			c.error(ph.Pos, "%s placeholder requires numeric value, got %s", ph.Type, vT)
		}
		for _, ca := range ph.Cases {
			if !validPluralSelector(ca.Selector) {
				c.error(ca.Pos, "invalid plural selector %q", ca.Selector)
			}
			for _, p := range ca.Body {
				if pp, ok := p.(*ast.I18nPlaceholderExpr); ok {
					c.checkI18nPlaceholder(pp)
				}
			}
		}
	case "select":
		if vT != nil && vT.Kind != ir.TypeString && vT.Kind != ir.TypeDyn {
			c.error(ph.Pos, "select placeholder requires string value, got %s", vT)
		}
		for _, ca := range ph.Cases {
			if ca.Selector == "" {
				c.error(ca.Pos, "select case has empty selector")
			}
			for _, p := range ca.Body {
				if pp, ok := p.(*ast.I18nPlaceholderExpr); ok {
					c.checkI18nPlaceholder(pp)
				}
			}
		}
	case "date", "time":
		if vT != nil {
			ok := false
			switch vT.Kind {
			case ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDyn:
				ok = true
			}
			if !ok {
				c.error(ph.Pos, "%s placeholder requires date/time value, got %s", ph.Type, vT)
			}
		}
	case "number":
		if vT != nil && vT.Kind != ir.TypeInt && vT.Kind != ir.TypeFloat && vT.Kind != ir.TypeDyn {
			c.error(ph.Pos, "number placeholder requires numeric value, got %s", vT)
		}
	}
}

// inferI18nInterp checks a translatable string and lowers it to an
// ir.Call to i18n.tr. F1 emits the no-static-text warning. F2 validates
// ICU placeholder types/cases. G1 synthesizes the ICU template. G2
// (this code) emits the call.
func (c *checker) inferI18nInterp(x *ast.I18nInterpExpr) ir.Expr {
	// F1: static-text warning.
	if !hasStaticText(x.Parts) {
		c.warn(x.Pos, "translatable string contains no static text; translators will have nothing to translate")
	}

	// F2: validate ICU placeholder types and cases.
	for _, p := range x.Parts {
		if ph, ok := p.(*ast.I18nPlaceholderExpr); ok {
			c.checkI18nPlaceholder(ph)
		}
	}

	// G1: synthesize ICU template and collect arg bindings.
	b := &templateBuilder{}
	b.writeParts(x.Parts)
	template := string(b.sb)

	// G2: resolve i18n.tr and emit ir.Call.
	trFn := c.lookupI18nTr(x.Pos)
	if trFn == nil {
		// Already errored; return a string-typed placeholder so type-checking
		// can continue without cascading failures.
		return &ir.Literal{Type: TypString, Raw: `""`}
	}

	// Build the args map<string, dyn>: placeholder name → checked value expression.
	var entries []ir.MapEntry
	for _, a := range b.args {
		entries = append(entries, ir.MapEntry{
			Key:   &ir.Literal{Type: TypString, Raw: fmt.Sprintf("%q", a.name)},
			Value: c.checkExpr(a.expr),
		})
	}
	argsMap := &ir.MapLitIR{
		Type:    MapOf(TypString, TypDyn),
		Entries: entries,
	}

	return &ir.Call{
		Type: TypString,
		Func: trFn,
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: TypString, Raw: fmt.Sprintf("%q", template)}},
			{Value: argsMap},
		},
	}
}

// lookupI18nTr resolves the i18n.tr stdlib function from the symbol table.
// Returns nil and emits an error diagnostic if not found.
func (c *checker) lookupI18nTr(pos ast.Pos) *ir.Func {
	fn, ok := c.symtab.LookupMethod("i18n", "tr")
	if !ok {
		c.error(pos, "i18n.tr is not in scope; ensure lib/i18n.sngl is loaded")
		return nil
	}
	return fn
}

// hasStaticText reports whether any part contains literal non-whitespace text,
// recursing through case bodies.
func hasStaticText(parts []ast.Expr) bool {
	for _, p := range parts {
		switch v := p.(type) {
		case *ast.LiteralExpr:
			if hasNonWhitespace(v.Raw) {
				return true
			}
		case *ast.I18nPlaceholderExpr:
			for _, ca := range v.Cases {
				if hasStaticText(ca.Body) {
					return true
				}
			}
		}
	}
	return false
}

func hasNonWhitespace(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}
