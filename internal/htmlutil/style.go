// Package htmlutil provides shared HTML utilities for platforms that render HTML
// (both client-side html and server-side http platforms).
package htmlutil

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// BuildCSSStyleIR builds an inline CSS style string from a slice of IR args
// (typically ir.NodeInst.Props). A `style={...}` struct prop has its fields
// flattened into individual CSS declarations.
func BuildCSSStyleIR(props []ir.Arg) string {
	var parts []string
	for _, p := range props {
		if p.Name == "" {
			continue
		}
		if p.Name == "style" {
			if sl, ok := p.Value.(*ir.StructLit); ok {
				for _, f := range sl.Fields {
					if f.Name == "" || f.Value == nil {
						continue
					}
					if css := StylePropToCSSIR(f.Name, f.Value); css != "" {
						parts = append(parts, css)
					}
				}
				continue
			}
		}
		if css := StylePropToCSSIR(p.Name, p.Value); css != "" {
			parts = append(parts, css)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// StylePropToCSSIR converts a SNGL style property and IR expression to a CSS
// declaration string, mirroring StylePropToCSS.
func StylePropToCSSIR(prop string, expr ir.Expr) string {
	val := ExprToStaticValueIR(expr)
	return stylePropCSS(prop, val)
}

// stylePropCSS is the shared prop→CSS mapping driven by a pre-extracted
// static value string.
func stylePropCSS(prop, val string) string {
	if val == "" {
		return ""
	}
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

// StyleDecl is one CSS declaration a style field writes. Value is the fixed
// text when Dynamic is false; when true the field's value goes there.
type StyleDecl struct {
	Name    string
	Value   string
	Dynamic bool
}

// StylePropDecls is the declarations a style field whose value is only known
// at run time writes: the same mapping stylePropCSS applies to a static one,
// with the value left as a hole.
func StylePropDecls(prop string) []StyleDecl {
	const hole = "\x00"
	var out []StyleDecl
	for decl := range strings.SplitSeq(stylePropCSS(prop, hole), ";") {
		name, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		out = append(out, StyleDecl{Name: name, Value: val, Dynamic: val == hole})
	}
	return out
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
	case "minWidth":
		return "min-width"
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
	case "fontFamily":
		return "font-family"
	case "fontSize":
		return "font-size"
	case "fontWeight":
		return "font-weight"
	case "fontStyle":
		return "font-style"
	case "textAlign":
		return "text-align"
	case "whiteSpace":
		// The member is the value, kebab-cased by ExprToStaticValueIR, so
		// `preWrap` is already `pre-wrap` by the time it gets here.
		return "white-space"
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
	case "borderBottom":
		return "border-bottom"
	case "borderLeft":
		return "border-left"
	case "borderCollapse":
		return "border-collapse"
	case "borderStyle":
		return "border-style"
	case "cursor":
		return "cursor"
	case "listStyleType":
		return "list-style-type"
	default:
		return ""
	}
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
		"width", "height", "min-width", "max-width", "max-height", "gap",
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
