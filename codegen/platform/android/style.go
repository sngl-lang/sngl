package android

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// buildModifierExpr builds a Kotlin Modifier chain from SNGL style properties.
func buildModifierExpr(styleAttrs, styleBlock map[string]ast.Expr, ec *exprContext) string {
	merged := make(map[string]ast.Expr)
	maps.Copy(merged, styleBlock)
	maps.Copy(merged, styleAttrs)

	var chain []string
	for prop, expr := range merged {
		if call := modifierCall(prop, expr, ec); call != "" {
			chain = append(chain, call)
		}
	}

	if len(chain) == 0 {
		return "Modifier"
	}
	return "Modifier\n" + strings.Join(chain, "\n")
}

// buildTextStyleExpr builds a Kotlin TextStyle expression from text-related SNGL style properties.
func buildTextStyleExpr(styleAttrs, styleBlock map[string]ast.Expr, ec *exprContext) string {
	merged := make(map[string]ast.Expr)
	maps.Copy(merged, styleBlock)
	maps.Copy(merged, styleAttrs)

	var params []string
	for prop, expr := range merged {
		if param := textStyleParam(prop, expr, ec); param != "" {
			params = append(params, param)
		}
	}
	if len(params) == 0 {
		return ""
	}
	return "TextStyle(" + strings.Join(params, ", ") + ")"
}

func modifierCall(prop string, expr ast.Expr, ec *exprContext) string {
	val := exprToKtValue(expr, ec)
	indent := "    "

	switch prop {
	// Padding
	case "padding":
		return indent + ".padding(" + toDp(val) + ")"
	case "paddingTop":
		return indent + ".padding(top = " + toDp(val) + ")"
	case "paddingRight", "paddingEnd":
		return indent + ".padding(end = " + toDp(val) + ")"
	case "paddingBottom":
		return indent + ".padding(bottom = " + toDp(val) + ")"
	case "paddingLeft", "paddingStart":
		return indent + ".padding(start = " + toDp(val) + ")"
	case "paddingX":
		return indent + ".padding(horizontal = " + toDp(val) + ")"
	case "paddingY":
		return indent + ".padding(vertical = " + toDp(val) + ")"

	// Margin (Compose has no margin; use padding on wrapping element)
	// We map it to padding since there's no better option
	case "margin":
		return indent + ".padding(" + toDp(val) + ")"
	case "marginTop":
		return indent + ".padding(top = " + toDp(val) + ")"
	case "marginRight":
		return indent + ".padding(end = " + toDp(val) + ")"
	case "marginBottom":
		return indent + ".padding(bottom = " + toDp(val) + ")"
	case "marginLeft":
		return indent + ".padding(start = " + toDp(val) + ")"

	// Sizing
	case "width":
		return indent + ".width(" + toDp(val) + ")"
	case "height":
		return indent + ".height(" + toDp(val) + ")"
	case "minWidth":
		return indent + ".widthIn(min = " + toDp(val) + ")"
	case "maxWidth":
		return indent + ".widthIn(max = " + toDp(val) + ")"
	case "minHeight":
		return indent + ".heightIn(min = " + toDp(val) + ")"
	case "maxHeight":
		return indent + ".heightIn(max = " + toDp(val) + ")"

	// Background
	case "background":
		return indent + ".background(" + toColor(val) + ")"

	// Border
	case "borderWidth":
		return indent + ".border(" + toDp(val) + ", MaterialTheme.colorScheme.outline)"
	case "borderRadius":
		return indent + ".clip(androidx.compose.foundation.shape.RoundedCornerShape(" + toDp(val) + "))"

	// Opacity
	case "opacity":
		return indent + ".alpha(" + val + "f)"

	// Flex
	case "flex", "flexGrow":
		return indent + ".weight(" + val + "f)"

	// Skip properties that map to TextStyle or layout arrangements
	case "color", "fontSize", "fontWeight", "fontStyle", "textAlign",
		"lineHeight", "fontFamily", "textOverflow", "maxLines",
		"gap", "rowGap", "columnGap", "alignItems", "justifyContent":
		return ""
	}

	return ""
}

func textStyleParam(prop string, expr ast.Expr, ec *exprContext) string {
	val := exprToKtValue(expr, ec)

	switch prop {
	case "color":
		return "color = " + toColor(val)
	case "fontSize":
		return "fontSize = " + toSp(val)
	case "fontWeight":
		switch val {
		case `"bold"`:
			return "fontWeight = FontWeight.Bold"
		case `"normal"`:
			return "fontWeight = FontWeight.Normal"
		case `"light"`:
			return "fontWeight = FontWeight.Light"
		default:
			return "fontWeight = FontWeight.Normal"
		}
	case "fontStyle":
		if val == `"italic"` {
			return "fontStyle = androidx.compose.ui.text.font.FontStyle.Italic"
		}
	case "textAlign":
		switch val {
		case `"center"`:
			return "textAlign = TextAlign.Center"
		case `"right"`, `"end"`:
			return "textAlign = TextAlign.End"
		case `"left"`, `"start"`:
			return "textAlign = TextAlign.Start"
		}
	case "lineHeight":
		return "lineHeight = " + toSp(val)
	}
	return ""
}

// toDp converts a value to Compose dp units.
func toDp(val string) string {
	// If it's a numeric literal, add .dp
	if isNumericLiteral(val) {
		return val + ".dp"
	}
	return "(" + val + ").dp"
}

// toSp converts a value to Compose sp units.
func toSp(val string) string {
	if isNumericLiteral(val) {
		return val + ".sp"
	}
	return "(" + val + ").sp"
}

// toColor converts a SNGL color string to a Compose Color.
func toColor(val string) string {
	// Strip quotes to check if it's a hex color
	unquoted := strings.Trim(val, `"`)
	if strings.HasPrefix(unquoted, "#") {
		hex := strings.TrimPrefix(unquoted, "#")
		switch len(hex) {
		case 6:
			return fmt.Sprintf("Color(0xFF%s)", strings.ToUpper(hex))
		case 8:
			return fmt.Sprintf("Color(0x%s)", strings.ToUpper(hex))
		}
	}
	return "Color.Unspecified"
}

func isNumericLiteral(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '.' || (c == '-' && i == 0) {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
