package htmlutil

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ir"
)

// ExprToStaticValueIR extracts a static literal value from an IR expression,
// returning "" for non-literals.
func ExprToStaticValueIR(e ir.Expr) string {
	if sl, ok := e.(*ir.StructLit); ok {
		if css, ok := colorStructToCSS(sl); ok {
			return css
		}
		return ""
	}
	// An enum member is static: `display` is typed `Display`, so `"flex"`
	// reaches here as the member rather than as the string that was written.
	// CSS spells a multi-word keyword kebab-case and SNGL spells an
	// identifier camelCase, so `inlineBlock` is `inline-block` — the member
	// is the value, not the text of the value.
	if id, ok := e.(*ir.Ident); ok && id.Member != "" {
		return kebabCase(id.Member)
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit == nil {
		return ""
	}
	return irLiteralStaticValue(lit)
}

// ColorExprToCSS renders a color struct-literal expression to a CSS color
// string (`#rrggbb` or `rgba(...)`). Returns ok=false when e isn't a color
// struct shape. Exported for non-html backends (e.g. bubbletea's lipgloss
// color args) that lower `#rrggbb` style literals the same way.
func ColorExprToCSS(e ir.Expr) (string, bool) {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return "", false
	}
	return colorStructToCSS(sl)
}

// colorStructToCSS renders a `color{r,g,b,a}` struct literal — the lowered
// form of a `#rrggbb[aa]` literal (see checker.lowerHexLiteral) — to a CSS
// color string: `#rrggbb` when fully opaque, otherwise `rgba(r,g,b,a)`.
// Detection is structural (fields r,g,b[,a] of static ints) because constant
// folding drops the StructLit's Type/Def, so ir.IsColorStruct can't be relied
// on by codegen. Returns ok=false when the literal isn't a color shape.
func colorStructToCSS(sl *ir.StructLit) (string, bool) {
	if sl.Def == nil && sl.Type == nil {
		// Untyped struct: only treat as color when shaped exactly like one.
		if len(sl.Fields) < 3 || len(sl.Fields) > 4 {
			return "", false
		}
	} else if !ir.IsColorStruct(sl.Type) && !(sl.Def != nil && sl.Def.Name == "color") {
		return "", false
	}
	ch := map[string]int{"a": 255}
	for _, f := range sl.Fields {
		switch f.Name {
		case "r", "g", "b", "a":
		default:
			return "", false
		}
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit == nil {
			return "", false
		}
		n, err := strconv.Atoi(strings.TrimSpace(lit.Value))
		if err != nil {
			return "", false
		}
		ch[f.Name] = n
	}
	r, rok := ch["r"]
	g, gok := ch["g"]
	b, bok := ch["b"]
	if !rok || !gok || !bok {
		return "", false
	}
	if a := ch["a"]; a < 255 {
		return fmt.Sprintf("rgba(%d,%d,%d,%g)", r, g, b, float64(a)/255), true
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b), true
}

func irLiteralStaticValue(lit *ir.Literal) string {
	if lit == nil {
		return ""
	}
	if css, ok := UnitLiteralToCSS(lit); ok {
		return css
	}
	if lit.Type == nil {
		return lit.Value
	}
	switch lit.Type.Kind {
	case ir.TypeString:
		raw := lit.Value
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) && len(raw) >= 6 {
			return raw[3 : len(raw)-3]
		}
		if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '`') && raw[len(raw)-1] == raw[0] {
			return raw[1 : len(raw)-1]
		}
		return raw
	case ir.TypeNull:
		return ""
	}
	return lit.Value
}

// kebabCase rewrites a camelCase identifier as the hyphenated keyword CSS
// spells it with: `rowReverse` → `row-reverse`. A single-word member is
// unchanged, which is every member of every enum CSS did not need two words
// for.
func kebabCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
