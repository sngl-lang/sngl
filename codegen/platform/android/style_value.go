package android

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/ir"
)

// A Style held in state -- a var, a ternary, a list element -- reaches a node
// as a value rather than a literal, so NodeStyleFieldsOf has no fields to read
// and the node used to render unstyled. It is read at run time instead, by two
// helpers generated from the Style declaration: snglStyle for the Modifier
// half (composeModifier's fields) and snglTextStyle for the TextStyle half
// (textStyleExpr's). A field at its default is unset, as __merge_Style
// already reads it.

const (
	heldModifierFn  = "snglStyle"
	heldTextStyleFn = "snglTextStyle"
)

// heldStyle returns n's style prop when it is a Style value and not a
// literal.
func heldStyle(n *ir.NodeInst, prop string) ir.Expr {
	v := codegen.NodeProp(n, prop)
	if v == nil {
		return nil
	}
	if _, ok := v.(*ir.StructLit); ok {
		return nil
	}
	if !isStyleType(v.ExprType()) {
		return nil
	}
	return v
}

// heldStyleDecls renders the two helpers for whichever of them src calls.
func heldStyleDecls(kc *kotlin.KtIRContext, src string, structs []*ir.StructDef) string {
	wantMod := strings.Contains(src, "."+heldModifierFn+"(")
	wantText := strings.Contains(src, heldTextStyleFn+"(")
	if !wantMod && !wantText {
		return ""
	}
	var sd *ir.StructDef
	for _, s := range structs {
		if isStyleType(&ir.Type{Kind: ir.TypeStruct, Decl: s}) {
			sd = s
			break
		}
	}
	if sd == nil {
		return ""
	}
	fields := map[string]*ir.Type{}
	for _, f := range sd.Fields {
		fields[f.Name] = f.Type
	}
	name := exportName(sd.Name)
	set := func(f string) string { return fmt.Sprintf("s.%s != %s", f, kotlin.KtFieldZero(fields[f])) }
	mag := func(f string) string {
		if t := fields[f]; t != nil && t.Kind == ir.TypeUnit {
			if u := ir.UnitDeclOf(t); u != nil && !u.IsSingleBase() {
				return "s." + f + ".px"
			}
		}
		return "s." + f
	}
	isColor := func(f string) bool { return fields[f] != nil && ir.IsColorStruct(fields[f]) }
	compose := func(f string) string {
		return fmt.Sprintf("%s(s.%s.r, s.%s.g, s.%s.b, s.%s.a)", composeColor(kc), f, f, f, f)
	}

	var b strings.Builder
	if wantMod {
		modifier := kc.Name(ktModifier)
		fmt.Fprintf(&b, "fun %s.%s(s: %s, withBackground: Boolean = true): %s {\n    var m = this\n", modifier, heldModifierFn, name, modifier)
		padding, width, height := kc.Name(ktPadding), kc.Name(ktWidth), kc.Name(ktHeight)
		for _, p := range []struct{ field, fn, arg string }{
			{"padding", padding, ""},
			{"paddingTop", padding, "top = "},
			{"paddingBottom", padding, "bottom = "},
			{"paddingLeft", padding, "start = "},
			{"paddingRight", padding, "end = "},
			{"width", width, ""},
			{"height", height, ""},
		} {
			if fields[p.field] != nil {
				fmt.Fprintf(&b, "    if (%s) m = m.%s(%s%s)\n", set(p.field), p.fn, p.arg, dp(kc, mag(p.field)))
			}
		}
		if isColor("background") {
			fmt.Fprintf(&b, "    if (withBackground && %s) m = m.%s(%s)\n", set("background"), kc.Name(ktBackground), compose("background"))
		}
		if fields["opacity"] != nil {
			fmt.Fprintf(&b, "    if (%s) m = m.%s(s.opacity.toFloat())\n", set("opacity"), kc.Name(ktAlpha))
		}
		b.WriteString("    return m\n}\n\n")
	}
	if wantText {
		var args []string
		if isColor("color") {
			args = append(args, fmt.Sprintf("color = if (%s) %s else %s.Unspecified", set("color"), compose("color"), composeColor(kc)))
		}
		if fields["fontSize"] != nil {
			args = append(args, fmt.Sprintf("fontSize = if (%s) %s else %s.Unspecified", set("fontSize"), sp(kc, mag("fontSize")), kc.Name(ktTextUnit)))
		}
		weight := func(m string) string {
			if w := composeWeight(m); w != "" {
				return kc.Name(ktFontWeight) + "." + w
			}
			return ""
		}
		if w := enumWhen("s.fontWeight", fields["fontWeight"], weight, "null"); w != "" {
			args = append(args, "fontWeight = "+w)
		}
		slant := func(m string) string {
			if st := composeSlant(m); st != "" {
				return kc.Name(ktFontStyle) + "." + st
			}
			return ""
		}
		if st := enumWhen("s.fontStyle", fields["fontStyle"], slant, "null"); st != "" {
			args = append(args, "fontStyle = "+st)
		}
		align := func(m string) string {
			switch m {
			case "center":
				return kc.Name(ktTextAlign) + ".Center"
			case "right":
				return kc.Name(ktTextAlign) + ".End"
			}
			return ""
		}
		if a := enumWhen("s.textAlign", fields["textAlign"], align, kc.Name(ktTextAlign)+".Unspecified"); a != "" {
			args = append(args, "textAlign = "+a)
		}
		textStyle := kc.Name(ktTextStyle)
		fmt.Fprintf(&b, "fun %s(s: %s): %s = %s(\n    %s\n)\n\n", heldTextStyleFn, name, textStyle, textStyle, strings.Join(args, ",\n    "))
	}
	return b.String()
}

// enumWhen maps an enum-typed field through to, leaving its first member --
// its zero, and so unset -- and any member to has no answer for at otherwise.
func enumWhen(operand string, t *ir.Type, to func(string) string, otherwise string) string {
	if t == nil || t.Kind != ir.TypeEnum {
		return ""
	}
	ed, ok := t.Decl.(*ir.EnumDef)
	if !ok || len(ed.Members) == 0 {
		return ""
	}
	var arms []string
	for _, m := range ed.Members[1:] {
		if v := to(m.Name); v != "" {
			arms = append(arms, kotlin.EnumName(ed)+"."+kotlin.EnumEntry(m.Name)+" -> "+v)
		}
	}
	if len(arms) == 0 {
		return ""
	}
	return "when (" + operand + ") { " + strings.Join(arms, "; ") + "; else -> " + otherwise + " }"
}
