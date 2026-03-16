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

	"github.com/google/cel-go/cel"
)

// MutationType is the opaque type returned by mutation functions (set, toggle, etc.).
var MutationType = cel.OpaqueType("sngl.Mutation")

// Opaque special types for domain values.
var (
	ColorType              = cel.OpaqueType("sngl.Color")
	DateType               = cel.OpaqueType("sngl.Date")
	TimeType               = cel.OpaqueType("sngl.Time")
	DateTimeType           = cel.OpaqueType("sngl.DateTime")
	DurationType           = cel.OpaqueType("sngl.Duration")
	URLType                = cel.OpaqueType("sngl.URL")
	EmailType              = cel.OpaqueType("sngl.Email")
	UUIDType               = cel.OpaqueType("sngl.UUID")
	RegexType              = cel.OpaqueType("sngl.Regex")
	Base64Type             = cel.OpaqueType("sngl.Base64")
	IPV4Type               = cel.OpaqueType("sngl.IPV4")
	IPV6Type               = cel.OpaqueType("sngl.IPV6")
	HostnameType           = cel.OpaqueType("sngl.Hostname")
	IDNEmailType           = cel.OpaqueType("sngl.IDNEmail")
	IDNHostnameType        = cel.OpaqueType("sngl.IDNHostname")
	IRLType                = cel.OpaqueType("sngl.IRL")
	IRLReferenceType       = cel.OpaqueType("sngl.IRLReference")
	URLReferenceType       = cel.OpaqueType("sngl.URLReference")
	URLTemplateType        = cel.OpaqueType("sngl.URLTemplate")
	CurrencyType           = cel.OpaqueType("sngl.Currency")
	Country2Type           = cel.OpaqueType("sngl.Country2")
	Country3Type           = cel.OpaqueType("sngl.Country3")
	CountrySubdivisionType = cel.OpaqueType("sngl.CountrySubdivision")
	DecimalType            = cel.OpaqueType("sngl.Decimal")
)

// specialTypes is the set of opaque types that strings are assignable to.
var specialTypes = []*cel.Type{
	ColorType, DateType, TimeType, DateTimeType, DurationType,
	URLType, EmailType, UUIDType, RegexType, Base64Type,
	IPV4Type, IPV6Type, HostnameType, IDNEmailType, IDNHostnameType,
	IRLType, IRLReferenceType, URLReferenceType, URLTemplateType,
	CurrencyType, Country2Type, Country3Type, CountrySubdivisionType, DecimalType,
}

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
	case "dateTime":
		return DateTimeType
	case "duration":
		return DurationType
	case "url":
		return URLType
	case "email":
		return EmailType
	case "uuid":
		return UUIDType
	case "regex":
		return RegexType
	case "base64":
		return Base64Type
	case "ipv4":
		return IPV4Type
	case "ipv6":
		return IPV6Type
	case "hostname":
		return HostnameType
	case "idnEmail":
		return IDNEmailType
	case "idnHostname":
		return IDNHostnameType
	case "irl":
		return IRLType
	case "irlReference":
		return IRLReferenceType
	case "urlReference":
		return URLReferenceType
	case "urlTemplate":
		return URLTemplateType
	case "currency":
		return CurrencyType
	case "country2":
		return Country2Type
	case "country3":
		return Country3Type
	case "countrySubdivision":
		return CountrySubdivisionType
	case "decimal":
		return DecimalType
	case "measurement", "length":
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
