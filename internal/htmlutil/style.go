// Package htmlutil provides shared HTML utilities for platforms that render HTML
// (both client-side html and server-side http platforms).
package htmlutil

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// BuildCSSStyle builds a complete inline CSS style string from a visual node's
// style fields.
func BuildCSSStyle(vn *ast.VisualNode) string {
	merged := vn.StyleFields()
	if len(merged) == 0 {
		return ""
	}
	var parts []string
	for prop, expr := range merged {
		if css := StylePropToCSS(prop, expr); css != "" {
			parts = append(parts, css)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// StylePropToCSS converts a SNGL style property and expression to a CSS
// declaration string. Returns "" if the value cannot be statically resolved.
// Some properties (paddingX, marginX, maxLines, borderWidth) expand to
// multiple CSS declarations.
func StylePropToCSS(prop string, expr ast.Expr) string {
	val := ExprToStaticValue(expr)
	if val == "" {
		return ""
	}

	// Handle multi-declaration special cases first.
	switch prop {
	case "paddingX":
		px := addPx(val)
		return fmt.Sprintf("padding-left:%s;padding-right:%s", px, px)
	case "paddingY":
		px := addPx(val)
		return fmt.Sprintf("padding-top:%s;padding-bottom:%s", px, px)
	case "marginX":
		px := addPx(val)
		return fmt.Sprintf("margin-left:%s;margin-right:%s", px, px)
	case "marginY":
		px := addPx(val)
		return fmt.Sprintf("margin-top:%s;margin-bottom:%s", px, px)
	case "borderWidth":
		return fmt.Sprintf("border-width:%s;border-style:solid", addPx(val))
	case "maxLines":
		return fmt.Sprintf("-webkit-line-clamp:%s;overflow:hidden;display:-webkit-box;-webkit-box-orient:vertical", val)
	}

	cssProp := PropToCSS(prop)
	if cssProp == "" {
		return ""
	}
	if IsNumeric(val) && IsSizeProp(cssProp) {
		val += "px"
	}
	return cssProp + ":" + val
}

// PropToCSS maps a SNGL style property name to its CSS equivalent.
func PropToCSS(prop string) string {
	switch prop {
	case "padding":
		return "padding"
	case "paddingTop":
		return "padding-top"
	case "paddingRight":
		return "padding-right"
	case "paddingBottom":
		return "padding-bottom"
	case "paddingLeft":
		return "padding-left"
	case "margin":
		return "margin"
	case "marginTop":
		return "margin-top"
	case "marginRight":
		return "margin-right"
	case "marginBottom":
		return "margin-bottom"
	case "marginLeft":
		return "margin-left"
	case "width":
		return "width"
	case "height":
		return "height"
	case "maxWidth":
		return "max-width"
	case "maxHeight":
		return "max-height"
	case "gap":
		return "gap"
	case "color":
		return "color"
	case "background":
		return "background-color"
	case "fontSize":
		return "font-size"
	case "fontWeight":
		return "font-weight"
	case "fontStyle":
		return "font-style"
	case "textAlign":
		return "text-align"
	case "borderRadius":
		return "border-radius"
	case "borderColor":
		return "border-color"
	case "opacity":
		return "opacity"
	case "flex":
		return "flex"
	case "display":
		return "display"
	case "flexDirection":
		return "flex-direction"
	case "position":
		return "position"
	case "top":
		return "top"
	case "bottom":
		return "bottom"
	case "left":
		return "left"
	case "right":
		return "right"
	case "overflow":
		return "overflow"
	case "zIndex":
		return "z-index"
	case "alignItems":
		return "align-items"
	case "justifyContent":
		return "justify-content"
	case "border":
		return "border"
	case "listStyleType":
		return "list-style-type"
	default:
		return ""
	}
}

// AppendCSS appends a CSS property:value to an existing style string.
func AppendCSS(existing, prop, value string) string {
	entry := prop + ":" + value
	if existing == "" {
		return entry
	}
	return existing + ";" + entry
}

// IsNumeric reports whether s consists only of digits, dots, and hyphens.
func IsNumeric(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}
	return len(s) > 0
}

// IsSizeProp reports whether a CSS property should have "px" appended to
// numeric values.
func IsSizeProp(cssProp string) bool {
	switch cssProp {
	case "padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"width", "height", "max-width", "max-height", "gap",
		"font-size", "border-width", "border-radius", "top", "bottom", "left", "right":
		return true
	}
	return false
}

func addPx(val string) string {
	if IsNumeric(val) {
		return val + "px"
	}
	return val
}
