# i18n Runtimes for JavaScript and Kotlin — Design

**Status:** Shipped (pkg/js/i18n/, pkg/kotlin/i18n/)
**Date:** 2026-05-09
**Goal:** Make `$"..."` translatable strings and direct `i18n.*` calls work end-to-end for `--platform html --lang none` (browser JS) and `--platform android --lang kotlin`. Today the Go runtime under `pkg/go/i18n/` is the only one wired up; the JS and Kotlin codegens emit no dispatch for i18n intrinsics, so user code that uses i18n breaks on those targets.

This is the deferred item from `2026-05-08-i18n-cleanup.md` and `2026-05-08-generics-and-iter.md` ("HTML/Android i18n runtime overrides").

## Non-goals

- Hybrid match-based i18n syntax (issue #54) — separate plan.
- Generic JVM Kotlin runtime — Android-only this round.
- Async/streaming locale data loading — manifest is static at build time.
- New ICU features — match the surface the Go runtime already exposes.

## Surface to mirror (from `pkg/go/i18n`)

The runtime needs to support exactly what `codegen/lang/golang/golang.go` dispatches:

- `i18n.tr(key, args)` — manifest lookup + ICU format
- `i18n.format(template, args)` — ICU format with no lookup
- `i18n.numberInt(n, style)` / `i18n.numberFloat(n, style)` — `decimal | percent | currency | scientific`
- `i18n.date(d, style)` / `i18n.time(t, style)` — `short | medium | long | full`
- `i18n.datetime(dt, dateStyle, timeStyle)`
- `i18n.select(value, cases)` — `{string: string}` map
- `i18n.plural(count, forms)` / `i18n.selectordinal(count, forms)` — `{PluralKey: string}` map
- `i18n.exactly(n)` — constructs a `PluralKey` for `=N` exact match
- Predeclared keys `i18n.zero/one/two/few/many/other`

ICU template features used by `formatICU`:

- Simple `{name}` substitution
- `{n, plural, =0{...} one{...} other{...}}` with `#` for current count
- `{n, selectordinal, ...}`
- `{x, select, key{...} other{...}}`
- `{n, number, style}` / `{d, date, style}` / `{t, time, style}` / `{dt, dateTime, style}`
- ICU apostrophe quoting (`''` → `'`; `'X'` literal run when X starts with `{`/`}`/`#`/`|`)

## Architecture

### JS runtime: `pkg/js/i18n/i18n.js`

Single ESM module. Exports:

```js
// Plural keys are strings on JS — Intl.PluralRules.select returns the
// CLDR keyword string directly, and exact-match keys are encoded as "=N".
// No PluralKey type needed (that struct exists in Go only because Go map
// keys need value equality).

export class Translator {
  constructor(manifest, locale) { /* ... */ }
  tr(key, inlinedTemplate, args) { /* ... */ }
  format(template, args) { /* ... */ }
  numberInt(n, style) { /* ... */ }
  numberFloat(n, style) { /* ... */ }
  date(d, style) { /* ... */ }
  time(t, style) { /* ... */ }
  datetime(dt, dateStyle, timeStyle) { /* ... */ }
  select(value, cases) { /* ... */ }
  plural(count, forms) { /* ... */ }
  selectordinal(count, forms) { /* ... */ }
}

let _default = null;
export function getTranslator() {
  if (_default === null) {
    const manifest = (typeof globalThis.__SNGL_I18N_MANIFEST__ === "object")
      ? globalThis.__SNGL_I18N_MANIFEST__ : {};
    _default = new Translator(manifest, navigator.language || "en");
  }
  return _default;
}
```

Implementation:

- ICU parser ports `formatICU`/`splitPlaceholder`/`splitCases`/`renderPlaceholder` from `pkg/go/i18n/i18n.go` directly. Same algorithm, JS strings.
- Plural categories via `new Intl.PluralRules(locale, {type})`.
- Number formatting via `new Intl.NumberFormat(locale, {style, currency})`. Currency code derived from locale by a small lookup table (USD fallback) — mirrors `currency.FromTag`.
- Date/time via `new Intl.DateTimeFormat(locale, {dateStyle, timeStyle})`.
- Manifest lookup falls back through progressively shorter locale forms, same as Go's `Manifest.Lookup`.

**Manifest delivery:** the html platform, when emitting the bundle entry, prepends:

```js
globalThis.__SNGL_I18N_MANIFEST__ = /* inlined JSON object */;
```

If no `i18n.manifest.json` is present in the project root, the prefix is omitted and `getTranslator()` defaults to an empty manifest.

### Kotlin runtime: `pkg/kotlin/i18n/I18n.kt`

Single Kotlin file. Package `us.duckfam.git.jonathan.sngl.i18n` (Java reverse-DNS of `git.duckfam.us`). Exports:

```kotlin
// Plural keys are strings on Kotlin too — android.icu.text.PluralRules.select
// returns CLDR keyword strings, and exact-match keys are encoded as "=N".
// No PluralKey type needed.

class Translator(val manifest: Manifest, val locale: Locale) {
    fun tr(key: String, inlinedTemplate: String, args: Map<String, Any?>): String
    fun format(template: String, args: Map<String, Any?>): String
    fun numberInt(n: Int, style: String): String
    fun numberFloat(n: Double, style: String): String
    fun date(d: Date, style: String): String
    fun time(t: Date, style: String): String
    fun datetime(dt: Date, dateStyle: String, timeStyle: String): String
    fun select(value: String, cases: Map<String, String>): String
    fun plural(count: Int, forms: Map<String, String>): String
    fun selectordinal(count: Int, forms: Map<String, String>): String
}

object I18n {
    @JvmStatic fun getTranslator(): Translator { /* ... */ }
}
```

Implementation:

- ICU parser ports the Go logic line-for-line.
- Plurals: `android.icu.text.PluralRules.forLocale(locale, type).select(n.toDouble())` returns the CLDR keyword string.
- Number: `android.icu.text.NumberFormat.getInstance(locale, style)` (decimal/percent/currency/scientific).
- Date/time: `android.icu.text.DateFormat.getDateInstance(style, locale)` and `getTimeInstance`.
- `getTranslator()` reads `assets/i18n.manifest.json` lazily via `Application.assets` — but generated Android code needs an `Application` reference. Convention: the android platform emits `I18n.init(context)` in the generated `Application.onCreate` (or activity init) so the static loader has access; if the user's project has no such hook, the runtime falls back to an empty manifest and logs a warning.
- Date input type: SNGL `date`/`time`/`dateTime` lower to `java.util.Date` (matching what Kotlin codegen already emits for time stdlib).

### Codegen wiring

`codegen/lang/javascript/translate_ir.go` and `codegen/lang/kotlin/translate_ir.go` each grow a `goBuiltinMethodFromArgs`-equivalent dispatcher (call it `jsBuiltinMethodFromArgs` / `ktBuiltinMethodFromArgs`). Lifted from `codegen/lang/golang/golang.go` lines 250-316.

JS emission examples:
- `i18n.tr` → `i18n.getTranslator().tr(<a0>, <a0>, <a1>)`
- `i18n.numberInt` → `i18n.getTranslator().numberInt(<a0>, <a1>)`
- `i18n.exactly(n)` → `("=" + (<a1>))` (string concat at the call site; no runtime helper needed)
- predeclared `i18n.one`/`i18n.two`/`i18n.few`/`i18n.many`/`i18n.other`/`i18n.zero` → string literals `"one"` / `"two"` / etc.

Kotlin emission examples:
- `i18n.tr` → `I18n.getTranslator().tr(<a0>, <a0>, <a1>)`
- `i18n.numberInt` → `I18n.getTranslator().numberInt(<a0>, <a1>)`
- `i18n.exactly(n)` → `("=" + (<a1>))`
- predeclared keys → string literals identical to JS

Each lang exports an `IsI18nCall` helper (parallel to Go's) so platform analyzers can decide whether to inject the import.

Map literal codegen for `map<i18n.PluralKey, string>`:
- The user-facing SNGL type stays `i18n.PluralKey` for source compatibility with the Go target.
- JS lang translator special-cases the `i18n.PluralKey` type when lowering map literals and key types: emits a plain object `{ "=0": "...", "one": "...", "other": "..." }`. The `forms` parameter of `plural`/`selectordinal` is typed as `Record<string, string>` in the runtime.
- Kotlin lang translator: same — emits `mapOf("=0" to "...", "one" to "...")` and the runtime takes `Map<String, String>`.
- Runtime lookup (both): try `forms["=" + count]` first; else `forms[Intl.PluralRules.select(count)]` / `PluralRules.select(count.toDouble())`; else `forms["other"]` (final fallback).

This sidesteps Go's PluralKey-struct trick (needed because Go map keys require value-equality and Go has no string-tagged unions). Both target runtimes get string keys natively.

### Platform wiring

**html platform** (`codegen/platform/html/`):
- Detect any i18n intrinsic call in the IR (use the new `javascript.IsI18nCall`). If present:
  1. Copy `pkg/js/i18n/i18n.js` into the output directory (or inline into the bundle).
  2. Generate the manifest prefix line in the bundle entry.
- The js translator already supports importing relative ESM modules; reuse that path. If route mode (html + Go), the existing Go path already works — no html-side change needed.

**android platform** (`codegen/platform/android/`):
- Detect i18n usage. If present:
  1. Copy `pkg/kotlin/i18n/I18n.kt` into the generated module's `src/main/kotlin/.../i18n/`.
  2. Copy `i18n.manifest.json` into `src/main/assets/`.
  3. Emit `I18n.init(this)` in the generated `Application.onCreate`.

### File inventory

**Created:**
- `pkg/js/i18n/i18n.js`
- `pkg/js/i18n/i18n.test.js` (node-runnable, uses Node 22+ built-in `Intl` and `node:test`)
- `pkg/kotlin/i18n/I18n.kt`
- `pkg/kotlin/i18n/I18nTest.kt` (JUnit; runs via Gradle when android module builds; or via Robolectric)
- `testdata/i18n_browser_smoke.sngl` (compile target = html, lang = none)
- `testdata/i18n_android_smoke.sngl` (compile target = android, lang = kotlin)
- `cmd/sngl/testdata/compile_js_i18n.txt` (txtar golden for JS bundle output)
- `cmd/sngl/testdata/compile_android_i18n.txt` (txtar golden for android module output)

**Modified:**
- `codegen/lang/javascript/translate_ir.go` — i18n intrinsic dispatch + map<PluralKey,V> emission tweak
- `codegen/lang/javascript/javascript.go` — `IsI18nCall`, import path constant
- `codegen/lang/kotlin/translate_ir.go` — i18n intrinsic dispatch
- `codegen/lang/kotlin/kotlin.go` — `IsI18nCall`, package constant
- `codegen/platform/html/*.go` — copy/inline JS runtime + manifest prefix
- `codegen/platform/android/*.go` — copy Kotlin runtime + assets + init hook
- `CLAUDE.md` — add `pkg/js/i18n/`, `pkg/kotlin/i18n/` to Stdlib Runtime section

## Tests

Mirror the Go test surface where the platforms allow:

- **JS**: `node --test pkg/js/i18n/i18n.test.js` covering Tr substitution, plural en/fr, currency en-US/fr-FR, date short/long, apostrophe quoting (`Don''t do that`).
- **Kotlin**: JUnit tests run via Gradle in the android example project. If running Gradle in CI is heavy, gate behind a build tag like the existing browser-CDP tests; for now require `go tool verify` to skip them and document in the plan.
- **Compile golden tests** (txtar) verify the generated bundle/module contains the runtime file and the manifest inline.
- **Browser smoke** (existing CDP runner under `codegen/platform/html/testing.go`) opens the generated page and asserts the rendered translated string.

## Risks / open issues

1. **PluralKey on JS/Kotlin.** Both runtimes use string keys — predeclared categories are CLDR keyword strings, exact matches are `"=N"`. Codegen translates `i18n.PluralKey`-typed SNGL values to strings at emission. Go path keeps its struct unchanged.
2. **Android `Application` injection.** If a project has no custom `Application`, the codegen needs to emit one. The android platform already does this for resource init; reuse that hook. Confirm during implementation.
3. **Bundle size impact.** The JS runtime ~10KB minified including the locale→currency map. Acceptable; only included when i18n actually used.
4. **`Intl.DateTimeFormat` style vs Go layout strings.** Native Intl produces locale-specific output, so the JS output for English may differ slightly from the Go output (e.g. `"5/8/26"` vs `"5/8/26"` matches but Intl produces `"May 8, 2026"` vs Go's `"May 8, 2026"` — same). Tests must accept locale-flexible matchers, not exact strings.
5. **`monday` quirks aren't ported.** The Go runtime uses `goodsign/monday` for non-Intl-covered locales. The JS/Kotlin runtimes rely on the platform Intl APIs which are more comprehensive than `golang.org/x/text` alone — so this is a feature improvement, not a gap.

## Self-review

- **Placeholders:** none — every section names concrete files and APIs.
- **Internal consistency:** runtime APIs in §JS and §Kotlin match the Go surface listed in §Surface. Codegen examples reference the actual runtime exports.
- **Scope:** single plan, two parallel runtime ports plus codegen wiring. Reasonable size; will split into JS-first then Kotlin-second tasks in the implementation plan.
- **Ambiguity:** the PluralKey-as-JS-Map-key issue is the one place with two possible resolutions; design picks the stringify-at-codegen approach explicitly.
