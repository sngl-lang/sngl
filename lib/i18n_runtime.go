package lib

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// ManifestEntry mirrors one entry in i18n.manifest.json.
type ManifestEntry struct {
	Translations map[string]string `json:"translations"`
}

// Manifest maps ICU template keys to their translations.
type Manifest map[string]ManifestEntry

// LoadManifest reads i18n.manifest.json from the given filesystem.
// Returns an empty manifest (no error) if the file does not exist.
func LoadManifest(fsys fs.FS, path string) (Manifest, error) {
	data, err := fs.ReadFile(fsys, path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = Manifest{}
	}
	return m, nil
}

// LoadManifestFromFile loads from the OS filesystem; returns empty
// manifest if the file is missing.
func LoadManifestFromFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = Manifest{}
	}
	return m, nil
}

// Lookup returns the translated string for the given key + locale.
// Falls back through:
//  1. exact-locale translation in the manifest entry
//  2. progressively shorter locale forms (e.g. "es-MX" → "es")
//  3. inlinedTemplate (the template string the lowering passed in)
//  4. key itself (final fallback)
func (m Manifest) Lookup(key, inlinedTemplate, locale string) string {
	e, ok := m[key]
	if !ok {
		if inlinedTemplate != "" {
			return inlinedTemplate
		}
		return key
	}
	// Try locale, then progressively shorter forms.
	for loc := locale; loc != ""; {
		if v, ok := e.Translations[loc]; ok {
			return v
		}
		i := strings.LastIndex(loc, "-")
		if i < 0 {
			break
		}
		loc = loc[:i]
	}
	if inlinedTemplate != "" {
		return inlinedTemplate
	}
	return key
}

// Translator pairs a manifest with an active locale and provides
// translation + ICU formatting.
type Translator struct {
	Manifest Manifest
	Locale   language.Tag
}

// NewTranslator constructs a Translator from a manifest and a locale
// string (BCP 47, e.g. "en", "es-MX"). On parse failure, falls back to
// English.
func NewTranslator(m Manifest, locale string) *Translator {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = language.English
	}
	return &Translator{Manifest: m, Locale: tag}
}

// Tr looks up `key` in the manifest, falls back to `inlinedTemplate`,
// formats it via ICU using `args`.
func (t *Translator) Tr(key, inlinedTemplate string, args map[string]any) string {
	tmpl := t.Manifest.Lookup(key, inlinedTemplate, t.Locale.String())
	return formatICU(t.Locale, tmpl, args)
}

// Format runs ICU MessageFormat on `template` with `args`, no manifest lookup.
func (t *Translator) Format(template string, args map[string]any) string {
	return formatICU(t.Locale, template, args)
}

// formatICU walks an ICU MessageFormat template, substituting placeholders
// with values from args. Handles simple {name} substitution and
// {n, plural, =N{...} other{...}} forms; other ICU constructs (select,
// date, number formatters) are not yet implemented and emit the literal
// template.
func formatICU(tag language.Tag, tmpl string, args map[string]any) string {
	var out strings.Builder
	formatICUInto(&out, tag, tmpl, args, "")
	return out.String()
}

// formatICUInto writes the formatted template to out. `current` is the
// currently-bound numeric value used by '#' inside plural/selectordinal
// case bodies.
func formatICUInto(out *strings.Builder, tag language.Tag, tmpl string, args map[string]any, current string) {
	i := 0
	for i < len(tmpl) {
		c := tmpl[i]
		if c == '\'' {
			// ICU escape: '...' suppresses metacharacter interpretation.
			// Consume up to the next closing quote, emitting the interior literally.
			i++
			for i < len(tmpl) && tmpl[i] != '\'' {
				out.WriteByte(tmpl[i])
				i++
			}
			if i < len(tmpl) {
				i++ // consume closing '
			}
			continue
		}
		if c == '#' && current != "" {
			out.WriteString(current)
			i++
			continue
		}
		if c == '{' {
			end, ph, ok := splitPlaceholder(tmpl, i)
			if !ok {
				// Malformed; emit literally and stop.
				out.WriteString(tmpl[i:])
				return
			}
			renderPlaceholder(out, tag, ph, args, current)
			i = end
			continue
		}
		out.WriteByte(c)
		i++
	}
}

// splitPlaceholder finds the matching close brace for the '{' at start.
// Returns the index AFTER the close brace, the placeholder body (without
// outer braces), and true on success.
func splitPlaceholder(tmpl string, start int) (end int, body string, ok bool) {
	if start >= len(tmpl) || tmpl[start] != '{' {
		return 0, "", false
	}
	depth := 0
	for i := start; i < len(tmpl); i++ {
		switch tmpl[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, tmpl[start+1 : i], true
			}
		case '\'':
			// Skip until next '.
			i++
			for i < len(tmpl) && tmpl[i] != '\'' {
				i++
			}
		}
	}
	return 0, "", false
}

// renderPlaceholder dispatches one ICU placeholder.
func renderPlaceholder(out *strings.Builder, tag language.Tag, body string, args map[string]any, _ string) {
	parts := splitPlaceholderArgs(body)
	if len(parts) == 0 {
		return
	}
	name := strings.TrimSpace(parts[0])
	val := args[name]
	if len(parts) == 1 {
		// {name} — simple substitution
		fmt.Fprintf(out, "%v", val)
		return
	}
	typ := strings.TrimSpace(parts[1])
	switch typ {
	case "plural", "selectordinal":
		renderPlural(out, tag, val, parts[2:], typ == "selectordinal", args)
	case "select":
		renderSelect(out, tag, val, parts[2:], args)
	default:
		// Unknown type; emit raw value.
		fmt.Fprintf(out, "%v", val)
	}
}

// splitPlaceholderArgs splits the body of a placeholder at top-level commas,
// counting brace depth so commas inside {...} aren't separators.
func splitPlaceholderArgs(body string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, body[start:])
	return parts
}

// renderPlural handles {n, plural, =0{...} one{...} other{...}}.
// cases is the body after the type (everything from "=0{...}" onward),
// which may be a single comma-merged string. We re-join and splitCases.
func renderPlural(out *strings.Builder, tag language.Tag, val any, cases []string, ordinal bool, args map[string]any) {
	raw := strings.TrimSpace(strings.Join(cases, ","))
	pairs := splitCases(raw)
	n, _ := toInt(val)
	nStr := strconv.Itoa(n)
	// Try exact match first.
	for _, p := range pairs {
		if strings.HasPrefix(p.selector, "=") {
			if p.selector[1:] == nStr {
				formatICUInto(out, tag, p.body, args, nStr)
				return
			}
		}
	}
	// Try CLDR keyword.
	var form plural.Form
	if ordinal {
		form = plural.Ordinal.MatchPlural(tag, n, 0, 0, 0, 0)
	} else {
		form = plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0)
	}
	keyword := pluralFormKeyword(form)
	for _, p := range pairs {
		if p.selector == keyword {
			formatICUInto(out, tag, p.body, args, nStr)
			return
		}
	}
	// Fall back to "other".
	for _, p := range pairs {
		if p.selector == "other" {
			formatICUInto(out, tag, p.body, args, nStr)
			return
		}
	}
}

func renderSelect(out *strings.Builder, tag language.Tag, val any, cases []string, args map[string]any) {
	raw := strings.TrimSpace(strings.Join(cases, ","))
	pairs := splitCases(raw)
	sel := fmt.Sprintf("%v", val)
	for _, p := range pairs {
		if p.selector == sel {
			formatICUInto(out, tag, p.body, args, "")
			return
		}
	}
	for _, p := range pairs {
		if p.selector == "other" {
			formatICUInto(out, tag, p.body, args, "")
			return
		}
	}
}

type casePair struct {
	selector, body string
}

// splitCases parses "=0{none} one{a} other{b}" into individual cases.
func splitCases(raw string) []casePair {
	var pairs []casePair
	i := 0
	for i < len(raw) {
		// Skip whitespace.
		for i < len(raw) && isICUWhitespace(raw[i]) {
			i++
		}
		if i >= len(raw) {
			break
		}
		// Read selector until '{'.
		selStart := i
		for i < len(raw) && raw[i] != '{' {
			i++
		}
		if i >= len(raw) {
			break
		}
		sel := strings.TrimSpace(raw[selStart:i])
		// Find matching close brace.
		depth := 0
		bodyStart := i + 1
		for ; i < len(raw); i++ {
			if raw[i] == '{' {
				depth++
			} else if raw[i] == '}' {
				depth--
				if depth == 0 {
					pairs = append(pairs, casePair{selector: sel, body: raw[bodyStart:i]})
					i++
					break
				}
			}
		}
	}
	return pairs
}

func isICUWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}

func pluralFormKeyword(f plural.Form) string {
	switch f {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	}
	return "other"
}
