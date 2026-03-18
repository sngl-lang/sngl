package bubbletea

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// buildStyleExpr builds a Go expression that creates a lipgloss.Style from SNGL style properties.
// styleAttrs are inline style.xxx=yyy attributes, styleBlock is from @style { ... }.
func buildStyleExpr(styleAttrs, styleBlock map[string]ast.Expr, ec *exprContext, scaleFactor int) string {
	var chain []string
	chain = append(chain, "lipgloss.NewStyle()")

	// Merge styleBlock first, then styleAttrs (attrs override block)
	merged := make(map[string]ast.Expr)
	for k, v := range styleBlock {
		merged[k] = v
	}
	for k, v := range styleAttrs {
		merged[k] = v
	}

	for prop, expr := range merged {
		if call := styleCall(prop, expr, ec, scaleFactor); call != "" {
			chain = append(chain, call)
		}
	}

	return strings.Join(chain, ".\n")
}

func styleCall(prop string, expr ast.Expr, ec *exprContext, scaleFactor int) string {
	val := exprToGoValue(expr, ec)

	switch prop {
	// Padding
	case "padding":
		return fmt.Sprintf("Padding(%s)", scaleVal(val, scaleFactor))
	case "paddingTop":
		return fmt.Sprintf("PaddingTop(%s)", scaleVal(val, scaleFactor))
	case "paddingRight":
		return fmt.Sprintf("PaddingRight(%s)", scaleVal(val, scaleFactor))
	case "paddingBottom":
		return fmt.Sprintf("PaddingBottom(%s)", scaleVal(val, scaleFactor))
	case "paddingLeft":
		return fmt.Sprintf("PaddingLeft(%s)", scaleVal(val, scaleFactor))

	// Margin
	case "margin":
		return fmt.Sprintf("Margin(%s)", scaleVal(val, scaleFactor))
	case "marginTop":
		return fmt.Sprintf("MarginTop(%s)", scaleVal(val, scaleFactor))
	case "marginRight":
		return fmt.Sprintf("MarginRight(%s)", scaleVal(val, scaleFactor))
	case "marginBottom":
		return fmt.Sprintf("MarginBottom(%s)", scaleVal(val, scaleFactor))
	case "marginLeft":
		return fmt.Sprintf("MarginLeft(%s)", scaleVal(val, scaleFactor))

	// Sizing
	case "width":
		return fmt.Sprintf("Width(%s)", scaleVal(val, scaleFactor))
	case "height":
		return fmt.Sprintf("Height(%s)", scaleVal(val, scaleFactor))
	case "maxWidth":
		return fmt.Sprintf("MaxWidth(%s)", scaleVal(val, scaleFactor))
	case "maxHeight":
		return fmt.Sprintf("MaxHeight(%s)", scaleVal(val, scaleFactor))

	// Colors
	case "color":
		return fmt.Sprintf("Foreground(lipgloss.Color(%s))", val)
	case "background":
		return fmt.Sprintf("Background(lipgloss.Color(%s))", val)

	// Font
	case "fontWeight":
		if val == `"bold"` {
			return "Bold(true)"
		}
	case "fontStyle":
		if val == `"italic"` {
			return "Italic(true)"
		}

	// Alignment
	case "textAlign":
		switch val {
		case `"center"`:
			return "AlignHorizontal(lipgloss.Center)"
		case `"right"`:
			return "AlignHorizontal(lipgloss.Right)"
		case `"left"`:
			return "AlignHorizontal(lipgloss.Left)"
		}

	// Border
	case "borderWidth":
		return "Border(lipgloss.NormalBorder())"
	case "borderColor":
		return fmt.Sprintf("BorderForeground(lipgloss.Color(%s))", val)

	// Opacity
	case "opacity":
		return "Faint(true)"
	}

	// Silently ignore unsupported properties
	return ""
}

// exprToGoValue converts an ast.Expr to a Go value string.
func exprToGoValue(expr ast.Expr, ec *exprContext) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	if expr.SNGL != nil && ec != nil {
		return ec.translateExpr(expr.SNGL)
	}
	return `""`
}

// scaleVal wraps a numeric value expression with pixel-to-cell scaling.
// For literal ints, it computes the scaled value at compile time.
func scaleVal(val string, scaleFactor int) string {
	// Try to parse as integer literal for compile-time scaling
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err == nil {
		if n == 0 {
			return "0"
		}
		scaled := n / scaleFactor
		if scaled < 1 {
			scaled = 1
		}
		return fmt.Sprintf("%d", scaled)
	}
	// Dynamic expression — scale at runtime
	return fmt.Sprintf("max(1, %s / %d)", val, scaleFactor)
}
