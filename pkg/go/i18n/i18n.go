// Package i18n is the Go runtime for the SNGL i18n stdlib package.
// Generated SNGL code targeting Go imports this package via the path
// git.duckfam.us/jonathan/sngl/pkg/go/i18n.
package i18n

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

	"github.com/goodsign/monday"
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
	Currency currency.Unit // currency for "currency" style; defaults from locale, USD fallback
}

// NewTranslator constructs a Translator from a manifest and a locale
// string (BCP 47, e.g. "en", "es-MX"). On parse failure, falls back to
// English. Currency defaults to the conventional currency for the locale
// via currency.FromTag, with USD as fallback.
func NewTranslator(m Manifest, locale string) *Translator {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = language.English
	}
	cur, _ := currency.FromTag(tag)
	if cur == (currency.Unit{}) {
		cur = currency.USD
	}
	return &Translator{Manifest: m, Locale: tag, Currency: cur}
}

// Tr looks up `key` in the manifest, falls back to `inlinedTemplate`,
// formats it via ICU using `args`.
func (t *Translator) Tr(key, inlinedTemplate string, args map[string]any) string {
	tmpl := t.Manifest.Lookup(key, inlinedTemplate, t.Locale.String())
	return formatICU(t.Locale, t.Currency, tmpl, args)
}

// Format runs ICU MessageFormat on `template` with `args`, no manifest lookup.
func (t *Translator) Format(template string, args map[string]any) string {
	return formatICU(t.Locale, t.Currency, template, args)
}

// formatICU walks an ICU MessageFormat template, substituting placeholders
// with values from args. Handles simple {name} substitution and
// {n, plural, =N{...} other{...}} forms; other ICU constructs (select,
// date, number formatters) are not yet implemented and emit the literal
// template.
func formatICU(tag language.Tag, cur currency.Unit, tmpl string, args map[string]any) string {
	var out strings.Builder
	formatICUInto(&out, tag, cur, tmpl, args, "")
	return out.String()
}

// formatICUInto writes the formatted template to out. `current` is the
// currently-bound numeric value used by '#' inside plural/selectordinal
// case bodies.
func formatICUInto(out *strings.Builder, tag language.Tag, cur currency.Unit, tmpl string, args map[string]any, current string) {
	i := 0
	for i < len(tmpl) {
		c := tmpl[i]
		if c == '\'' {
			// ICU apostrophe quoting rules:
			//   ''        → literal ' (always)
			//   'X' where X starts with {, }, #, or | → literal run X
			//   bare '    → literal '
			if i+1 < len(tmpl) && tmpl[i+1] == '\'' {
				// Doubled '' → single literal apostrophe.
				out.WriteByte('\'')
				i += 2
				continue
			}
			var next byte
			if i+1 < len(tmpl) {
				next = tmpl[i+1]
			}
			if next == '{' || next == '}' || next == '#' || next == '|' {
				// 'X' quoted run — consume interior literally until closing '.
				i++ // consume opening '
				for i < len(tmpl) {
					if tmpl[i] == '\'' {
						if i+1 < len(tmpl) && tmpl[i+1] == '\'' {
							// '' inside quoted run → literal '
							out.WriteByte('\'')
							i += 2
							continue
						}
						i++ // consume closing '
						break
					}
					out.WriteByte(tmpl[i])
					i++
				}
				continue
			}
			// Bare ' not before metachar — literal apostrophe.
			out.WriteByte('\'')
			i++
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
			renderPlaceholder(out, tag, cur, ph, args, current)
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
			// Skip ICU-quoted run: '' stays in place (not a run), 'X' where X
			// starts with a metachar opens a quoted run — skip to closing '.
			// For brace-depth counting we only need to skip the interior so
			// braces inside quoted runs don't affect depth.
			if i+1 < len(tmpl) && tmpl[i+1] == '\'' {
				i++ // just skip the doubled ' (second ' will be seen next iter)
			} else {
				var next byte
				if i+1 < len(tmpl) {
					next = tmpl[i+1]
				}
				if next == '{' || next == '}' || next == '#' || next == '|' {
					i++ // skip opening '
					for i < len(tmpl) {
						if tmpl[i] == '\'' {
							if i+1 < len(tmpl) && tmpl[i+1] == '\'' {
								i += 2
								continue
							}
							break // closing '
						}
						i++
					}
				}
				// bare ' — nothing extra to skip
			}
		}
	}
	return 0, "", false
}

// renderPlaceholder dispatches one ICU placeholder.
func renderPlaceholder(out *strings.Builder, tag language.Tag, cur currency.Unit, body string, args map[string]any, _ string) {
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
		renderPlural(out, tag, cur, val, parts[2:], typ == "selectordinal", args)
	case "select":
		renderSelect(out, tag, cur, val, parts[2:], args)
	case "number":
		style := "decimal"
		if len(parts) >= 3 {
			style = strings.TrimSpace(parts[2])
		}
		renderNumber(out, tag, cur, val, style)
	case "date":
		style := "medium"
		if len(parts) >= 3 {
			style = strings.TrimSpace(parts[2])
		}
		renderDate(out, tag, val, style)
	case "time":
		style := "medium"
		if len(parts) >= 3 {
			style = strings.TrimSpace(parts[2])
		}
		renderTime(out, tag, val, style)
	case "dateTime":
		style := "medium"
		if len(parts) >= 3 {
			style = strings.TrimSpace(parts[2])
		}
		renderDateTime(out, tag, val, style)
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
func renderPlural(out *strings.Builder, tag language.Tag, cur currency.Unit, val any, cases []string, ordinal bool, args map[string]any) {
	raw := strings.TrimSpace(strings.Join(cases, ","))
	pairs := splitCases(raw)
	n, _ := toInt(val)
	nStr := strconv.Itoa(n)
	// Try exact match first.
	for _, p := range pairs {
		if strings.HasPrefix(p.selector, "=") {
			if p.selector[1:] == nStr {
				formatICUInto(out, tag, cur, p.body, args, nStr)
				return
			}
		}
	}
	// Try CLDR keyword. golang.org/x/text/feature/plural panics on
	// negative n; CLDR categories are defined on the absolute value
	// (a -1 guest still reads as "1 guest" in the "one" form, etc.).
	absN := n
	if absN < 0 {
		absN = -absN
	}
	var form plural.Form
	if ordinal {
		form = plural.Ordinal.MatchPlural(tag, absN, 0, 0, 0, 0)
	} else {
		form = plural.Cardinal.MatchPlural(tag, absN, 0, 0, 0, 0)
	}
	keyword := pluralFormKeyword(form)
	for _, p := range pairs {
		if p.selector == keyword {
			formatICUInto(out, tag, cur, p.body, args, nStr)
			return
		}
	}
	// Fall back to "other".
	for _, p := range pairs {
		if p.selector == "other" {
			formatICUInto(out, tag, cur, p.body, args, nStr)
			return
		}
	}
}

func renderSelect(out *strings.Builder, tag language.Tag, cur currency.Unit, val any, cases []string, args map[string]any) {
	raw := strings.TrimSpace(strings.Join(cases, ","))
	pairs := splitCases(raw)
	sel := fmt.Sprintf("%v", val)
	for _, p := range pairs {
		if p.selector == sel {
			formatICUInto(out, tag, cur, p.body, args, "")
			return
		}
	}
	for _, p := range pairs {
		if p.selector == "other" {
			formatICUInto(out, tag, cur, p.body, args, "")
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
	var b strings.Builder
	renderNumber(&b, t.Locale, t.Currency, n, style)
	return b.String()
}

// NumberFloat formats a float for the translator's locale.
func (t *Translator) NumberFloat(n float64, style string) string {
	var b strings.Builder
	renderNumber(&b, t.Locale, t.Currency, n, style)
	return b.String()
}

// Date formats a time.Time as a date.
// Style: "short", "medium" (default), "long", "full".
func (t *Translator) Date(d time.Time, style string) string {
	var b strings.Builder
	renderDate(&b, t.Locale, d, style)
	return b.String()
}

// Time formats a time.Time as a time-of-day.
func (t *Translator) Time(d time.Time, style string) string {
	var b strings.Builder
	renderTime(&b, t.Locale, d, style)
	return b.String()
}

// Datetime formats a time.Time as date + time.
func (t *Translator) Datetime(d time.Time, dateStyle, timeStyle string) string {
	var b strings.Builder
	renderDateTime2(&b, t.Locale, d, dateStyle, timeStyle)
	return b.String()
}

// renderNumber writes a locale-aware number to out.
func renderNumber(out *strings.Builder, tag language.Tag, cur currency.Unit, val any, style string) {
	p := message.NewPrinter(tag)
	switch v := val.(type) {
	case int:
		switch style {
		case "percent":
			out.WriteString(p.Sprint(number.Percent(float64(v) / 100.0)))
		case "currency":
			out.WriteString(p.Sprint(cur.Amount(int64(v))))
		case "scientific":
			out.WriteString(p.Sprintf("%e", float64(v)))
		default:
			out.WriteString(p.Sprintf("%d", v))
		}
	case float64:
		switch style {
		case "percent":
			out.WriteString(p.Sprint(number.Percent(v)))
		case "currency":
			out.WriteString(p.Sprint(cur.Amount(v)))
		case "scientific":
			out.WriteString(p.Sprintf("%e", v))
		default:
			out.WriteString(p.Sprintf("%g", v))
		}
	case float32:
		f := float64(v)
		switch style {
		case "percent":
			out.WriteString(p.Sprint(number.Percent(f)))
		case "currency":
			out.WriteString(p.Sprint(cur.Amount(f)))
		case "scientific":
			out.WriteString(p.Sprintf("%e", f))
		default:
			out.WriteString(p.Sprintf("%g", f))
		}
	default:
		fmt.Fprintf(out, "%v", val)
	}
}

// renderDate writes a locale-aware date to out.
func renderDate(out *strings.Builder, tag language.Tag, val any, style string) {
	if t, ok := val.(time.Time); ok {
		out.WriteString(monday.Format(t, dateLayout(style), localeFor(tag)))
		return
	}
	fmt.Fprintf(out, "%v", val)
}

// renderTime writes a locale-aware time-of-day to out.
func renderTime(out *strings.Builder, tag language.Tag, val any, style string) {
	if t, ok := val.(time.Time); ok {
		out.WriteString(monday.Format(t, timeLayout(style), localeFor(tag)))
		return
	}
	fmt.Fprintf(out, "%v", val)
}

// renderDateTime writes a locale-aware date+time to out using a single style
// for both parts. Used by the {x, dateTime, style} ICU placeholder form.
func renderDateTime(out *strings.Builder, tag language.Tag, val any, style string) {
	if t, ok := val.(time.Time); ok {
		layout := dateLayout(style) + " " + timeLayout(style)
		out.WriteString(monday.Format(t, layout, localeFor(tag)))
		return
	}
	fmt.Fprintf(out, "%v", val)
}

// renderDateTime2 writes a locale-aware date+time using separate styles for
// date and time. Used by Translator.Datetime which accepts two style params.
func renderDateTime2(out *strings.Builder, tag language.Tag, d time.Time, dateStyle, timeStyle string) {
	layout := dateLayout(dateStyle) + " " + timeLayout(timeStyle)
	out.WriteString(monday.Format(d, layout, localeFor(tag)))
}

// dateLayout maps an ICU style name to a Go time-layout string. Locale
// substitutions for month/day names are handled by monday.Format.
func dateLayout(style string) string {
	switch style {
	case "short":
		return "1/2/06"
	case "long":
		return "January 2, 2006"
	case "full":
		return "Monday, January 2, 2006"
	}
	return "Jan 2, 2006" // medium
}

// timeLayout maps an ICU style name to a Go time-layout string.
func timeLayout(style string) string {
	switch style {
	case "short":
		return "3:04 PM"
	case "long", "full":
		return "3:04:05 PM MST"
	}
	return "3:04:05 PM" // medium
}

// localeFor maps a language.Tag to a monday.Locale. Unknown tags fall
// back to en_US.
func localeFor(tag language.Tag) monday.Locale {
	base, _ := tag.Base()
	region, _ := tag.Region()
	composite := base.String() + "_" + region.String()
	switch composite {
	case "en_GB":
		return monday.LocaleEnGB
	case "fr_CA":
		return monday.LocaleFrCA
	case "fr_FR":
		return monday.LocaleFrFR
	case "de_DE":
		return monday.LocaleDeDE
	case "ja_JP":
		return monday.LocaleJaJP
	case "es_ES":
		return monday.LocaleEsES
	case "it_IT":
		return monday.LocaleItIT
	case "pt_PT":
		return monday.LocalePtPT
	case "pt_BR":
		return monday.LocalePtBR
	case "zh_CN":
		return monday.LocaleZhCN
	case "zh_TW":
		return monday.LocaleZhTW
	case "zh_HK":
		return monday.LocaleZhHK
	case "ko_KR":
		return monday.LocaleKoKR
	case "ru_RU":
		return monday.LocaleRuRU
	case "nl_NL":
		return monday.LocaleNlNL
	case "nl_BE":
		return monday.LocaleNlBE
	case "pl_PL":
		return monday.LocalePlPL
	case "sv_SE":
		return monday.LocaleSvSE
	case "da_DK":
		return monday.LocaleDaDK
	case "fi_FI":
		return monday.LocaleFiFI
	case "tr_TR":
		return monday.LocaleTrTR
	case "el_GR":
		return monday.LocaleElGR
	case "hu_HU":
		return monday.LocaleHuHU
	case "ro_RO":
		return monday.LocaleRoRO
	case "uk_UA":
		return monday.LocaleUkUA
	case "bg_BG":
		return monday.LocaleBgBG
	case "id_ID":
		return monday.LocaleIdID
	case "cs_CZ":
		return monday.LocaleCsCZ
	case "sk_SK":
		return monday.LocaleSkSK
	case "hr_HR":
		return monday.LocaleHrHR
	case "sl_SI":
		return monday.LocaleSlSI
	case "lt_LT":
		return monday.LocaleLtLT
	case "lv_LV":
		return monday.LocaleLvLV
	case "et_EE":
		return monday.LocaleEtEE
	case "th_TH":
		return monday.LocaleThTH
	case "nb_NO":
		return monday.LocaleNbNO
	case "nn_NO":
		return monday.LocaleNnNO
	case "ca_ES":
		return monday.LocaleCaES
	case "kk_KZ":
		return monday.LocaleKkKZ
	case "uz_UZ":
		return monday.LocaleUzUZ
	}
	// Fall back by base language alone.
	switch base.String() {
	case "fr":
		return monday.LocaleFrFR
	case "de":
		return monday.LocaleDeDE
	case "ja":
		return monday.LocaleJaJP
	case "es":
		return monday.LocaleEsES
	case "it":
		return monday.LocaleItIT
	case "pt":
		return monday.LocalePtPT
	case "zh":
		return monday.LocaleZhCN
	case "ko":
		return monday.LocaleKoKR
	case "ru":
		return monday.LocaleRuRU
	case "nl":
		return monday.LocaleNlNL
	case "pl":
		return monday.LocalePlPL
	case "sv":
		return monday.LocaleSvSE
	case "da":
		return monday.LocaleDaDK
	case "fi":
		return monday.LocaleFiFI
	case "tr":
		return monday.LocaleTrTR
	case "el":
		return monday.LocaleElGR
	case "hu":
		return monday.LocaleHuHU
	case "ro":
		return monday.LocaleRoRO
	case "uk":
		return monday.LocaleUkUA
	case "bg":
		return monday.LocaleBgBG
	case "id":
		return monday.LocaleIdID
	case "cs":
		return monday.LocaleCsCZ
	case "sk":
		return monday.LocaleSkSK
	case "hr":
		return monday.LocaleHrHR
	case "sl":
		return monday.LocaleSlSI
	case "lt":
		return monday.LocaleLtLT
	case "lv":
		return monday.LocaleLvLV
	case "et":
		return monday.LocaleEtEE
	case "th":
		return monday.LocaleThTH
	case "nb":
		return monday.LocaleNbNO
	case "nn":
		return monday.LocaleNnNO
	case "ca":
		return monday.LocaleCaES
	case "kk":
		return monday.LocaleKkKZ
	case "uz":
		return monday.LocaleUzUZ
	}
	return monday.LocaleEnUS
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
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
	}
	cat := plural.Cardinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
	if msg, ok := forms[keywordToPluralKey(cat)]; ok {
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
	}
	if msg, ok := forms[PluralOther]; ok {
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
	}
	return ""
}

// Selectordinal is like Plural but uses ordinal CLDR rules.
func (t *Translator) Selectordinal(count int, forms map[PluralKey]string) string {
	if msg, ok := forms[Exactly(count)]; ok {
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
	}
	cat := plural.Ordinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
	if msg, ok := forms[keywordToPluralKey(cat)]; ok {
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
	}
	if msg, ok := forms[PluralOther]; ok {
		return formatICU(t.Locale, t.Currency, msg, map[string]any{"#": count, "n": count})
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
	translatorOnce    sync.Once
	defaultTranslator *Translator
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

// DefaultLocale resolves the process-startup locale from environment variables,
// with BCP-47 normalisation and "en-US" fallback.
//
// Read order: LC_ALL, LC_MESSAGES, LANG. Strips encoding suffix (".UTF-8") and
// modifier suffix ("@euro"); normalises POSIX underscore separators to BCP-47
// hyphens (e.g. "en_US.UTF-8" → "en-US"). Falls back to "en-US" when no
// locale variable is set.
//
// This is the Go runtime implementation of the SNGL stdlib i18n.defaultLocale()
// function. Generated code calls i18n.DefaultLocale() directly.
func DefaultLocale() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if i := strings.IndexAny(v, ".@"); i >= 0 {
				v = v[:i]
			}
			return strings.ReplaceAll(v, "_", "-")
		}
	}
	return "en-US"
}
