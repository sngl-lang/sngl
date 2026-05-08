package lib

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/currency"
	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
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

// --- Direct formatters ---

// NumberInt formats an integer for the translator's locale.
// Style: "decimal" (default), "percent", "currency", "scientific".
func (t *Translator) NumberInt(n int, style string) string {
	p := message.NewPrinter(t.Locale)
	switch style {
	case "percent":
		return p.Sprint(number.Percent(float64(n) / 100.0))
	case "currency":
		return p.Sprint(currency.USD.Amount(int64(n)))
	case "scientific":
		return p.Sprintf("%e", float64(n))
	default:
		return p.Sprintf("%d", n)
	}
}

// NumberFloat formats a float for the translator's locale.
func (t *Translator) NumberFloat(n float64, style string) string {
	p := message.NewPrinter(t.Locale)
	switch style {
	case "percent":
		return p.Sprint(number.Percent(n))
	case "currency":
		return p.Sprint(currency.USD.Amount(n))
	case "scientific":
		return p.Sprintf("%e", n)
	default:
		return p.Sprintf("%g", n)
	}
}

// Date formats a time.Time as a date.
// Style: "short", "medium" (default), "long", "full".
func (t *Translator) Date(d time.Time, style string) string {
	return d.Format(layoutFor(style, true, false))
}

// Time formats a time.Time as a time-of-day.
func (t *Translator) Time(d time.Time, style string) string {
	return d.Format(layoutFor(style, false, true))
}

// Datetime formats a time.Time as date + time.
func (t *Translator) Datetime(d time.Time, dateStyle, timeStyle string) string {
	return d.Format(layoutFor(dateStyle, true, false) + " " + layoutFor(timeStyle, false, true))
}

// layoutFor picks a Go time layout for the given ICU-style identifier.
// Locale-aware variants (full month names in non-en) require x/text date
// formatting which isn't implemented here yet; this returns en-style
// layouts as a baseline that callers can override later.
func layoutFor(style string, dateOnly, timeOnly bool) string {
	switch style {
	case "short":
		if dateOnly {
			return "1/2/06"
		}
		if timeOnly {
			return "3:04 PM"
		}
	case "long":
		if dateOnly {
			return "January 2, 2006"
		}
		if timeOnly {
			return "3:04:05 PM MST"
		}
	case "full":
		if dateOnly {
			return "Monday, January 2, 2006"
		}
		if timeOnly {
			return "3:04:05 PM MST"
		}
	}
	// medium (default)
	if dateOnly {
		return "Jan 2, 2006"
	}
	if timeOnly {
		return "3:04:05 PM"
	}
	return time.RFC3339
}

// --- Direct selectors ---

// PluralKey is the runtime form of i18n.PluralKey from lib/i18n.sngl.
// When Exact is true, N is the literal integer matched by =N. When Exact
// is false, N is a sentinel index into the CLDR keyword list (matching
// the SNGL constants i18n.zero=0 / one=1 / two=2 / few=3 / many=4 /
// other=5).
type PluralKey struct {
	N     int
	Exact bool
}

// Sentinel CLDR keyword keys. The N values match the predeclared SNGL
// constants in lib/i18n.sngl.
var (
	PluralZero  = PluralKey{N: 0, Exact: false}
	PluralOne   = PluralKey{N: 1, Exact: false}
	PluralTwo   = PluralKey{N: 2, Exact: false}
	PluralFew   = PluralKey{N: 3, Exact: false}
	PluralMany  = PluralKey{N: 4, Exact: false}
	PluralOther = PluralKey{N: 5, Exact: false}
)

// Exactly constructs an =N exact-match key.
func Exactly(n int) PluralKey {
	return PluralKey{N: n, Exact: true}
}

// Plural selects a form by CLDR rule. Forms maps PluralKey to a message
// template. Exact matches win first; CLDR keywords second; PluralOther
// last as final fallback.
func (t *Translator) Plural(count int, forms map[PluralKey]string) string {
	if msg, ok := forms[Exactly(count)]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	cat := plural.Cardinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
	if msg, ok := forms[keywordToPluralKey(cat)]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	if msg, ok := forms[PluralOther]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	return ""
}

// Selectordinal is like Plural but uses ordinal CLDR rules.
func (t *Translator) Selectordinal(count int, forms map[PluralKey]string) string {
	if msg, ok := forms[Exactly(count)]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	cat := plural.Ordinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
	if msg, ok := forms[keywordToPluralKey(cat)]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	if msg, ok := forms[PluralOther]; ok {
		return formatICU(t.Locale, msg, map[string]any{"#": count, "n": count})
	}
	return ""
}

// Select dispatches by string value. Falls back to "other" on miss.
func (t *Translator) Select(value string, cases map[string]string) string {
	if msg, ok := cases[value]; ok {
		return msg
	}
	if msg, ok := cases["other"]; ok {
		return msg
	}
	return ""
}

func keywordToPluralKey(cat plural.Form) PluralKey {
	switch cat {
	case plural.Zero:
		return PluralZero
	case plural.One:
		return PluralOne
	case plural.Two:
		return PluralTwo
	case plural.Few:
		return PluralFew
	case plural.Many:
		return PluralMany
	}
	return PluralOther
}

// --- Process-wide default Translator ---

var (
	translatorOnce      sync.Once
	defaultTranslator   *Translator
)

// GetTranslator returns the process-wide Translator, lazily initialised
// from i18n.manifest.json (working directory) and the LC_ALL/LC_MESSAGES/LANG
// env vars. The first call may incur file I/O; subsequent calls are cheap.
func GetTranslator() *Translator {
	translatorOnce.Do(func() {
		locale := pickLocale()
		m, _ := LoadManifestFromFile("i18n.manifest.json")
		defaultTranslator = NewTranslator(m, locale)
	})
	return defaultTranslator
}

// pickLocale extracts the active locale from LC_ALL / LC_MESSAGES / LANG
// in priority order. Falls back to "en" if none are set.
// Trims charset suffixes (".UTF-8") and modifier suffixes ("@euro");
// converts "_" separators to "-" so Go BCP-47 parsers accept the result.
func pickLocale() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			// Strip .codeset and @modifier
			for _, sep := range []string{".", "@"} {
				if i := strings.Index(v, sep); i >= 0 {
					v = v[:i]
				}
			}
			v = strings.ReplaceAll(v, "_", "-")
			return v
		}
	}
	return "en"
}
