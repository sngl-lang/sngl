package checker

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
)

// MutationType is the opaque type returned by mutation functions (set, toggle, etc.).
var MutationType = cel.OpaqueType("sngl.Mutation")

// Opaque special types for domain values.
var (
	ColorType    = cel.OpaqueType("sngl.Color")
	DateType     = cel.OpaqueType("sngl.Date")
	TimeType     = cel.OpaqueType("sngl.Time")
	DateTimeType = cel.OpaqueType("sngl.DateTime")
	DurationType = cel.OpaqueType("sngl.Duration")
)

// specialTypes is the set of opaque types that strings are assignable to.
var specialTypes = []*cel.Type{ColorType, DateType, TimeType, DateTimeType, DurationType}

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
		return ColorType
	case "date":
		return DateType
	case "time":
		return TimeType
	case "datetime":
		return DateTimeType
	case "duration":
		return DurationType
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

// isAssignable reports whether a value of type got can be assigned where expected is required.
func isAssignable(got, expected *cel.Type) bool {
	if got.IsEquivalentType(expected) {
		return true
	}
	if got == cel.DynType || expected == cel.DynType {
		return true
	}
	// Strings are assignable to special domain types and vice versa.
	for _, t := range specialTypes {
		if got.IsEquivalentType(cel.StringType) && expected.IsEquivalentType(t) {
			return true
		}
		if got.IsEquivalentType(t) && expected.IsEquivalentType(cel.StringType) {
			return true
		}
	}
	return false
}

var colorRE = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// validateSpecialLiteral checks that a literal value is valid for a special type hint.
func validateSpecialLiteral(hint string, value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("expected string literal for %s type", hint)
	}
	switch hint {
	case "color":
		if !colorRE.MatchString(s) {
			return fmt.Errorf("invalid color literal %q: expected #RGB, #RRGGBB, or #RRGGBBAA", s)
		}
	case "date":
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return fmt.Errorf("invalid date literal %q: expected YYYY-MM-DD", s)
		}
	case "time":
		if _, err := time.Parse("15:04", s); err != nil {
			if _, err2 := time.Parse("15:04:05", s); err2 != nil {
				return fmt.Errorf("invalid time literal %q: expected HH:MM or HH:MM:SS", s)
			}
		}
	case "datetime":
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("invalid datetime literal %q: expected RFC 3339 format", s)
		}
	case "duration":
		if _, err := time.ParseDuration(s); err != nil {
			return fmt.Errorf("invalid duration literal %q: %w", s, err)
		}
	}
	return nil
}
