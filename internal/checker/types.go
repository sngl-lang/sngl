package checker

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Type represents a SNGL type.
type Type int

const (
	Dyn    Type = iota // dynamic/unknown type
	Bool               // bool
	Int                // int
	Float              // float
	String             // string
	List               // list ([]T)
	Option             // option<T> (nullable wrapper)
	Struct             // user-defined struct

	// Special domain types (all stored as strings)
	Color
	Date
	Time
	DateTime
	Duration
	URL
	Email
	UUID
	Regex
	Base64
	IPV4
	IPV6
	Hostname
	IDNEmail
	IDNHostname
	IRL
	IRLReference
	URLReference
	URLTemplate
	Currency
	Country2
	Country3
	CountrySubdivision
	Decimal
)

// typeNames maps Type to display string.
var typeNames = map[Type]string{
	Dyn: "dyn", Bool: "bool", Int: "int", Float: "float", String: "string",
	List: "list", Option: "option", Struct: "struct",
	Color: "color", Date: "date", Time: "time", DateTime: "dateTime",
	Duration: "duration", URL: "url", Email: "email", UUID: "uuid",
	Regex: "regex", Base64: "base64", IPV4: "ipv4", IPV6: "ipv6",
	Hostname: "hostname", IDNEmail: "idnEmail", IDNHostname: "idnHostname",
	IRL: "irl", IRLReference: "irlReference", URLReference: "urlReference",
	URLTemplate: "urlTemplate", Currency: "currency",
	Country2: "country2", Country3: "country3",
	CountrySubdivision: "countrySubdivision", Decimal: "decimal",
}

func (t Type) GoString() string { return t.String() }
func (t Type) String() string {
	if s, ok := typeNames[t]; ok {
		return s
	}
	return "unknown"
}

// specialTypes are domain types assignable to/from string.
var specialTypes = []Type{
	Color, Date, Time, DateTime, Duration,
	URL, Email, UUID, Regex, Base64,
	IPV4, IPV6, Hostname, IDNEmail, IDNHostname,
	IRL, IRLReference, URLReference, URLTemplate,
	Currency, Country2, Country3, CountrySubdivision, Decimal,
}

// TypeFromHint maps a type hint string to a Type.
func TypeFromHint(hint string) Type {
	if strings.HasPrefix(hint, "[]") || strings.HasPrefix(hint, "list:") {
		return List
	}
	if strings.HasPrefix(hint, "option:") {
		return Option
	}
	switch hint {
	case "bool":
		return Bool
	case "int":
		return Int
	case "float":
		return Float
	case "string":
		return String
	case "color":
		return Color
	case "date":
		return Date
	case "time":
		return Time
	case "dateTime":
		return DateTime
	case "duration":
		return Duration
	case "url":
		return URL
	case "email":
		return Email
	case "uuid":
		return UUID
	case "regex":
		return Regex
	case "base64":
		return Base64
	case "ipv4":
		return IPV4
	case "ipv6":
		return IPV6
	case "hostname":
		return Hostname
	case "idnEmail":
		return IDNEmail
	case "idnHostname":
		return IDNHostname
	case "irl":
		return IRL
	case "irlReference":
		return IRLReference
	case "urlReference":
		return URLReference
	case "urlTemplate":
		return URLTemplate
	case "currency":
		return Currency
	case "country2":
		return Country2
	case "country3":
		return Country3
	case "countrySubdivision":
		return CountrySubdivision
	case "decimal":
		return Decimal
	case "measurement", "length":
		return Dyn
	default:
		return Dyn
	}
}

// InferLiteralType returns the type for a Go literal value.
func InferLiteralType(v any) Type {
	switch v.(type) {
	case bool:
		return Bool
	case int:
		return Int
	case float64:
		return Float
	case string:
		return String
	case []any:
		return List
	default:
		return Dyn
	}
}

// isKnownDynHint reports whether a type hint string legitimately resolves to Dyn.
// This includes the explicit "dyn" keyword, func signatures, and unit type names
// that don't yet have a dedicated checker Type.
func isKnownDynHint(hint string) bool {
	if hint == "dyn" || hint == "measurement" || hint == "length" {
		return true
	}
	if strings.HasPrefix(hint, "func") || strings.HasPrefix(hint, "unit:") {
		return true
	}
	return false
}

// narrowNumeric returns the known numeric type when one side is numeric and
// the other is Dyn. Returns Dyn only when both sides are Dyn.
func narrowNumeric(left, right Type) Type {
	if left == Int || left == Float {
		return left
	}
	if right == Int || right == Float {
		return right
	}
	return Dyn
}

// isNumeric reports whether t is Int or Float.
func isNumeric(t Type) bool {
	return t == Int || t == Float
}

// isAssignable reports whether a value of type got can be assigned where expected is required.
func isAssignable(got, expected Type) bool {
	if got == expected {
		return true
	}
	if got == Dyn || expected == Dyn {
		return true
	}
	// Strings are assignable to special domain types and vice versa.
	for _, t := range specialTypes {
		if got == String && expected == t {
			return true
		}
		if got == t && expected == String {
			return true
		}
	}
	return false
}

var (
	colorRE    = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
)

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
	case "dateTime":
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("invalid dateTime literal %q: expected RFC 3339 format", s)
		}
	case "duration":
		if _, err := time.ParseDuration(s); err != nil {
			return fmt.Errorf("invalid duration literal %q: %w", s, err)
		}
	case "url", "urlReference", "irl", "irlReference", "urlTemplate":
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" {
			return fmt.Errorf("invalid %s literal %q: expected a URL with scheme", hint, s)
		}
	case "email":
		if _, err := mail.ParseAddress(s); err != nil {
			return fmt.Errorf("invalid email literal %q: %w", s, err)
		}
	case "uuid":
		if !uuidRE.MatchString(s) {
			return fmt.Errorf("invalid uuid literal %q: expected UUID format", s)
		}
	case "regex":
		if _, err := regexp.Compile(s); err != nil {
			return fmt.Errorf("invalid regex literal %q: %w", s, err)
		}
	case "base64":
		if _, err := base64.StdEncoding.DecodeString(s); err != nil {
			return fmt.Errorf("invalid base64 literal %q: %w", s, err)
		}
	case "ipv4":
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("invalid ipv4 literal %q: expected IPv4 address", s)
		}
	case "ipv6":
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("invalid ipv6 literal %q: expected IPv6 address", s)
		}
	case "hostname":
		if !hostnameRE.MatchString(s) {
			return fmt.Errorf("invalid hostname literal %q: expected RFC 1123 hostname", s)
		}
	case "country2":
		if len(s) != 2 || !isAllAlpha(s) {
			return fmt.Errorf("invalid country2 literal %q: expected 2-letter country code", s)
		}
	case "country3":
		if len(s) != 3 || !isAllAlpha(s) {
			return fmt.Errorf("invalid country3 literal %q: expected 3-letter country code", s)
		}
	case "currency":
		if len(s) != 3 || !isAllAlphaUpper(s) {
			return fmt.Errorf("invalid currency literal %q: expected 3-letter uppercase currency code", s)
		}
	}
	return nil
}

func isAllAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isAllAlphaUpper(s string) bool {
	for _, r := range s {
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
