package checker

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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
			raw := v.Raw
			if len(parts) == 1 {
				// A whole triple-quoted string carries the indentation it was
				// written under; a segment of an interpolated one carries only
				// part of it, and the whole is what dedent is defined over.
				style, _ := ast.StringStyleOf(v.Kind)
				raw = ast.Dedent(raw, style)
			}
			b.sb = append(b.sb, icuText(raw)...)
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
	if ph.Style != "" {
		b.sb = append(b.sb, ',', ' ')
		b.sb = append(b.sb, ph.Style...)
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
	"date": {}, "time": {}, "dateTime": {}, "number": {},
}

// dateStyles enumerates the valid styles for date/time/dateTime placeholders.
var dateStyles = map[string]struct{}{
	"short":  {},
	"medium": {},
	"long":   {},
	"full":   {},
}

// numberStyles enumerates the valid styles for number placeholders.
var numberStyles = map[string]struct{}{
	"decimal":    {},
	"percent":    {},
	"currency":   {},
	"scientific": {},
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
		if ph.Style != "" {
			c.error(ph.Pos, "%s placeholder cannot take a style argument", ph.Type)
		}
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
		if ph.Style != "" {
			c.error(ph.Pos, "%s placeholder cannot take a style argument", ph.Type)
		}
		// Allow string, dyn, or any enum type. For enums we also validate
		// that case selectors correspond to declared members and that the
		// match is exhaustive (or carries an `other` fallback).
		var enumDecl *ir.EnumDef
		if vT != nil {
			switch vT.Kind {
			case ir.TypeString, ir.TypeDyn:
			case ir.TypeEnum:
				if d, ok := vT.Decl.(*ir.EnumDef); ok {
					enumDecl = d
				}
			default:
				c.error(ph.Pos, "select placeholder requires string or enum value, got %s", vT)
			}
		}
		seen := make(map[string]bool)
		hasOther := false
		for _, ca := range ph.Cases {
			if ca.Selector == "" {
				c.error(ca.Pos, "select case has empty selector")
			}
			if ca.Selector == "other" {
				hasOther = true
			} else if enumDecl != nil {
				known := false
				for _, m := range enumDecl.Members {
					if m.Name == ca.Selector {
						known = true
						break
					}
				}
				if !known {
					c.error(ca.Pos, "select case %q is not a member of enum %s", ca.Selector, enumDecl.Name)
				}
			}
			seen[ca.Selector] = true
			for _, p := range ca.Body {
				if pp, ok := p.(*ast.I18nPlaceholderExpr); ok {
					c.checkI18nPlaceholder(pp)
				}
			}
		}
		if enumDecl != nil && !hasOther {
			var missing []string
			for _, m := range enumDecl.Members {
				if !seen[m.Name] {
					missing = append(missing, m.Name)
				}
			}
			if len(missing) > 0 {
				c.error(ph.Pos, "non-exhaustive select on enum %s: missing case(s) %v (or add `other`)", enumDecl.Name, missing)
			}
		}
	case "date", "time", "dateTime":
		if vT != nil {
			ok := vT.Kind == ir.TypeDyn ||
				ir.IsDateStruct(vT) || ir.IsTimeStruct(vT) || ir.IsDateTimeStruct(vT)
			if !ok {
				c.error(ph.Pos, "%s placeholder requires date/time value, got %s", ph.Type, vT)
			}
		}
		if ph.Style != "" {
			if _, ok := dateStyles[ph.Style]; !ok {
				c.error(ph.Pos, "invalid date/time style %q (expected short, medium, long, or full)", ph.Style)
			}
		}
	case "number":
		if vT != nil && vT.Kind != ir.TypeInt && vT.Kind != ir.TypeFloat && vT.Kind != ir.TypeDyn {
			c.error(ph.Pos, "number placeholder requires numeric value, got %s", vT)
		}
		if ph.Style != "" {
			if _, ok := numberStyles[ph.Style]; !ok {
				c.error(ph.Pos, "invalid number style %q (expected decimal, percent, currency, or scientific)", ph.Style)
			}
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

	// G2: resolve i18n.trInline and emit ir.Call. trInline carries both
	// the canonical key (= the synthesized template, for manifest lookup)
	// and the inlined template (for fallback when the manifest misses).
	trFn, trNS := c.lookupI18nTrInline(x.Pos)
	if trFn == nil {
		// Already errored; return a string-typed placeholder so type-checking
		// can continue without cascading failures.
		return &ir.Literal{Type: TypString, Value: ""}
	}

	// Build the args map<string, dyn>: placeholder name → checked value expression.
	// Value is the decoded value; language codegen (e.g. evalLiteral) applies
	// target-language quoting.
	var entries []ir.MapEntry
	for _, a := range b.args {
		entries = append(entries, ir.MapEntry{
			Key:   &ir.Literal{Type: TypString, Value: a.name},
			Value: c.checkExpr(a.expr),
		})
	}
	argsMap := &ir.MapLitIR{
		Type:    ir.MapOf(TypString, TypDyn),
		Entries: entries,
	}

	return &ir.Call{
		Type: TypString,
		Func: trFn,
		// The namespace the call was reached through. trInline is a package
		// function, so the alias at the call site is what names it — and a
		// synthesized call has to carry what a written one would.
		Receiver: i18nReceiver(c, trNS),
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: TypString, Value: template}},
			{Value: &ir.Literal{Type: TypString, Value: template}},
			{Value: argsMap},
		},
	}
}

// i18nNamespace is the name the i18n package binds by default.
const i18nNamespace = "i18n"

// i18nImportPath is the package $"..." lowers into a call on.
const i18nImportPath = "sngl:i18n"

// i18nReceiver names the namespace the call was reached through, or nil when
// a dot import put the function in scope unqualified.
func i18nReceiver(c *checker, ns string) ir.Expr {
	if ns == "" {
		return nil
	}
	return &ir.Ident{Name: ns, Sym: c.namespaceNamed(ns)}
}

func (c *checker) namespaceNamed(name string) ir.Symbol {
	if sym, ok := c.scope.Lookup(name); ok {
		return sym
	}
	return nil
}

// lookupI18nTrInline resolves the i18n.trInline stdlib function — the
// target of $"..." interpolation lowering. Returns nil and emits an error
// diagnostic if not found.
//
// i18n is an ordinary package, not an internal invariant, so a
// file that writes $"..." without importing it lands here, and the message
// names the import rather than a compiler file.
func (c *checker) lookupI18nTrInline(pos ast.Pos) (*ir.Func, string) {
	// By the path rather than the alias: the import names the package, and
	// what the file chose to call it is its own business.
	for _, imp := range c.pkg.Imports {
		if imp == nil || imp.Path != i18nImportPath || imp.Pkg == nil {
			continue
		}
		sym, ok := imp.Pkg.Symbols.Root.LookupLocal("trInline")
		if !ok {
			continue
		}
		fn, isFn := sym.(*ir.Func)
		if !isFn {
			continue
		}
		// A dot import lifts the name into the file, so the call has no
		// namespace to qualify it with. "." is how that import records itself.
		alias := imp.Alias
		if alias == "." {
			alias = ""
		} else if alias == "" {
			alias = i18nNamespace
		}
		return fn, alias
	}
	c.error(pos, `a $"..." string is a call to i18n.tr, and %s is not imported: `+
		`add import %s %q`,
		i18nImportPath, i18nNamespace, i18nImportPath)
	return nil, ""
}

// icuText turns a literal part's source spelling into ICU MessageFormat text.
// The two syntaxes overlap: SNGL's escapes are decoded here, ICU's apostrophe
// quoting is copied through for the runtime formatter to decode. A character
// an escape stood for is re-quoted for ICU when it is a metachar there — `\{`
// means a literal brace, and unquoted it would open a placeholder at runtime.
func icuText(raw string) string {
	if !strings.ContainsRune(raw, '\\') {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			b.WriteByte(raw[i])
			continue
		}
		n := 2
		if raw[i+1] == 'x' && i+4 <= len(raw) {
			n = 4
		}
		for _, r := range ast.UnescapeString(raw[i:i+n], ast.StyleDouble) {
			switch r {
			case '{', '}', '#', '|', '\'':
				b.WriteByte('\'')
				b.WriteRune(r)
				b.WriteByte('\'')
			default:
				b.WriteRune(r)
			}
		}
		i += n - 1
	}
	return b.String()
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
