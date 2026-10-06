package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
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
func heldStyleDecls(src string, structs []*ir.StructDef) string {
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
		return fmt.Sprintf("ComposeColor(s.%s.r, s.%s.g, s.%s.b, s.%s.a)", f, f, f, f)
	}

	var b strings.Builder
	if wantMod {
		fmt.Fprintf(&b, "fun Modifier.%s(s: %s, withBackground: Boolean = true): Modifier {\n    var m = this\n", heldModifierFn, name)
		for _, p := range []struct{ field, call string }{
			{"padding", "padding(%s.dp)"},
			{"paddingTop", "padding(top = %s.dp)"},
			{"paddingBottom", "padding(bottom = %s.dp)"},
			{"paddingLeft", "padding(start = %s.dp)"},
			{"paddingRight", "padding(end = %s.dp)"},
			{"width", "width(%s.dp)"},
			{"height", "height(%s.dp)"},
		} {
			if fields[p.field] != nil {
				fmt.Fprintf(&b, "    if (%s) m = m.%s\n", set(p.field), fmt.Sprintf(p.call, mag(p.field)))
			}
		}
		if isColor("background") {
			fmt.Fprintf(&b, "    if (withBackground && %s) m = m.background(%s)\n", set("background"), compose("background"))
		}
		if fields["opacity"] != nil {
			fmt.Fprintf(&b, "    if (%s) m = m.alpha(s.opacity.toFloat())\n", set("opacity"))
		}
		b.WriteString("    return m\n}\n\n")
	}
	if wantText {
		var args []string
		if isColor("color") {
			args = append(args, fmt.Sprintf("color = if (%s) %s else ComposeColor.Unspecified", set("color"), compose("color")))
		}
		if fields["fontSize"] != nil {
			args = append(args, fmt.Sprintf("fontSize = if (%s) %s.sp else androidx.compose.ui.unit.TextUnit.Unspecified", set("fontSize"), mag("fontSize")))
		}
		if w := enumWhen("s.fontWeight", fields["fontWeight"], composeWeight, "null"); w != "" {
			args = append(args, "fontWeight = "+w)
		}
		slant := func(m string) string {
			if st := composeSlant(m); st != "" {
				return "androidx.compose.ui.text.font." + st
			}
			return ""
		}
		if st := enumWhen("s.fontStyle", fields["fontStyle"], slant, "null"); st != "" {
			args = append(args, "fontStyle = "+st)
		}
		align := func(m string) string {
			switch m {
			case "center":
				return "TextAlign.Center"
			case "right":
				return "TextAlign.End"
			}
			return ""
		}
		if a := enumWhen("s.textAlign", fields["textAlign"], align, "TextAlign.Unspecified"); a != "" {
			args = append(args, "textAlign = "+a)
		}
		fmt.Fprintf(&b, "fun %s(s: %s): TextStyle = TextStyle(\n    %s\n)\n\n", heldTextStyleFn, name, strings.Join(args, ",\n    "))
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
