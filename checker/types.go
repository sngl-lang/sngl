package checker

import (
	"strings"

	"github.com/google/cel-go/cel"
)

// MutationType is the opaque type returned by mutation functions (set, toggle, etc.).
var MutationType = cel.OpaqueType("sngl.Mutation")

// TypeHintToCelType maps SNGL type hint strings to CEL types.
func TypeHintToCelType(hint string) *cel.Type {
	if strings.HasPrefix(hint, "[]") || strings.HasPrefix(hint, "list:") {
		return cel.ListType(cel.DynType)
	}
	switch hint {
	case "bool":
		return cel.BoolType
	case "int":
		return cel.IntType
	case "float":
		return cel.DoubleType
	case "string":
		return cel.StringType
	case "color":
		return cel.StringType
	case "length":
		return cel.DynType
	default:
		return cel.DynType
	}
}

// InferLiteralType returns the CEL type for a Go literal value.
func InferLiteralType(v any) *cel.Type {
	switch v.(type) {
	case bool:
		return cel.BoolType
	case int:
		return cel.IntType
	case float64:
		return cel.DoubleType
	case string:
		return cel.StringType
	default:
		return cel.DynType
	}
}

// StylePropertyTypes maps style property names (without "style." prefix) to their expected CEL types.
var StylePropertyTypes = map[string]*cel.Type{
	// Sizing
	"width":        cel.DynType,
	"height":       cel.DynType,
	"min-width":    cel.DynType,
	"min-height":   cel.DynType,
	"max-width":    cel.DynType,
	"max-height":   cel.DynType,
	"aspect-ratio": cel.DoubleType,

	// Padding
	"padding":        cel.DynType,
	"padding-top":    cel.DynType,
	"padding-right":  cel.DynType,
	"padding-bottom": cel.DynType,
	"padding-left":   cel.DynType,
	"padding-x":      cel.DynType,
	"padding-y":      cel.DynType,

	// Margin
	"margin":        cel.DynType,
	"margin-top":    cel.DynType,
	"margin-right":  cel.DynType,
	"margin-bottom": cel.DynType,
	"margin-left":   cel.DynType,
	"margin-x":      cel.DynType,
	"margin-y":      cel.DynType,

	// Flex
	"flex":        cel.DoubleType,
	"flex-grow":   cel.DoubleType,
	"flex-shrink": cel.DoubleType,
	"flex-basis":  cel.DynType,
	"align-self":  cel.StringType,

	// Positioning
	"position": cel.StringType,
	"top":      cel.DynType,
	"right":    cel.DynType,
	"bottom":   cel.DynType,
	"left":     cel.DynType,
	"z-index":  cel.IntType,

	// Container Layout
	"gap":             cel.DynType,
	"row-gap":         cel.DynType,
	"column-gap":      cel.DynType,
	"align-items":     cel.StringType,
	"justify-content": cel.StringType,
	"flex-wrap":       cel.StringType,

	// Visual Properties
	"background":    cel.StringType,
	"border-color":  cel.StringType,
	"border-width":  cel.DynType,
	"border-radius": cel.DynType,
	"opacity":       cel.DoubleType,
	"overflow":      cel.StringType,

	// Text-specific style properties
	"color":         cel.StringType,
	"font-size":     cel.DoubleType,
	"font-weight":   cel.StringType,
	"font-style":    cel.StringType,
	"font-family":   cel.StringType,
	"text-align":    cel.StringType,
	"line-height":   cel.DoubleType,
	"text-overflow": cel.StringType,
	"max-lines":     cel.IntType,

	// Input-specific style properties
	"placeholder-color": cel.StringType,
}
