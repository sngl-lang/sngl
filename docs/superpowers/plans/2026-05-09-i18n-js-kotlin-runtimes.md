# i18n Runtimes for JavaScript and Kotlin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `$"..."` translatable strings and direct `i18n.*` calls work end-to-end on `--platform html --lang none` (browser JS) and `--platform android --lang kotlin`. Today only the Go runtime (`pkg/go/i18n/`) is wired up.

**Architecture:** Port the ICU template parser/dispatcher from `pkg/go/i18n/i18n.go` to JS and Kotlin. Delegate locale-aware formatting to the platform's native Intl APIs (`Intl.NumberFormat`, `android.icu.text.NumberFormat`, etc.). Both runtimes use string keys for plural categories — no `PluralKey` struct (Go-only quirk). Codegen lowers `i18n.PluralKey`-typed map literals to string-keyed maps at emission. Manifests inline as a `globalThis.__SNGL_I18N_MANIFEST__` JS const for browser; `assets/i18n.manifest.json` for Android.

**Tech Stack:** Go (compiler), JS (ESM, Node 22+ native test runner for unit tests, browser Intl APIs at runtime), Kotlin (Android, `android.icu.*`).

Spec: `docs/superpowers/specs/2026-05-09-i18n-js-kotlin-runtimes-design.md`.

---

## File Structure

**Created:**
- `pkg/js/i18n/i18n.js` — JS runtime (ICU parser + Intl-backed formatters)
- `pkg/js/i18n/i18n.test.js` — Node `node:test` unit tests
- `pkg/js/i18n/locale_currency.js` — locale → currency code lookup table (mirrors `currency.FromTag`)
- `pkg/kotlin/i18n/I18n.kt` — Kotlin runtime
- `pkg/kotlin/i18n/I18nTest.kt` — JUnit tests (Robolectric, run via Gradle when available)
- `testdata/i18n_browser_smoke.sngl` — fixture exercising `$"..."` for html+none
- `testdata/i18n_android_smoke.sngl` — fixture exercising `$"..."` for android+kotlin
- `cmd/sngl/testdata/compile_js_i18n.txt` — txtar golden for JS bundle
- `cmd/sngl/testdata/compile_android_i18n.txt` — txtar golden for android module

**Modified:**
- `codegen/lang/javascript/ircontext.go` — i18n intrinsic dispatch in `jsBuiltinMethodFromArgs`
- `codegen/lang/javascript/translate_ir.go` — `map<i18n.PluralKey, string>` literal lowering
- `codegen/lang/javascript/javascript.go` — `IsI18nCall`, `SnglI18nImportPath`
- `codegen/lang/kotlin/ircontext.go` — i18n intrinsic dispatch
- `codegen/lang/kotlin/translate_ir.go` — `map<i18n.PluralKey, string>` lowering
- `codegen/lang/kotlin/kotlin.go` — `IsI18nCall`, runtime package name
- `codegen/platform/html/jsbundle.go` (or equivalent) — copy/inline JS runtime + manifest prefix
- `codegen/platform/android/scaffold.go` (or equivalent) — copy Kotlin runtime + assets + Application init
- `CLAUDE.md` — list `pkg/js/i18n/` and `pkg/kotlin/i18n/` under Stdlib runtime

---

## Phase 0 — Worktree + manifest fixture

### Task 0a: Create a feature worktree

- [ ] **Step 1:** From the main repo root:

```bash
git worktree add -b i18n-js-kotlin .worktrees/i18n-js-kotlin main
cd .worktrees/i18n-js-kotlin
```

- [ ] **Step 2:** Verify the spec is present:

```bash
test -f docs/superpowers/specs/2026-05-09-i18n-js-kotlin-runtimes-design.md && echo OK
```

Expected: `OK`.

---

### Task 0b: Manifest fixture for tests

A small `i18n.manifest.json` exercising plural + select + simple substitution. Used by both runtimes' tests.

**Files:**
- Create: `testdata/i18n_test_manifest.json`

- [ ] **Step 1:** Write the manifest:

```json
{
  "Hello, {name}!": {
    "translations": {
      "fr": "Bonjour, {name} !",
      "es": "¡Hola, {name}!"
    }
  },
  "{count, plural, one{# item} other{# items}}": {
    "translations": {
      "fr": "{count, plural, one{# article} other{# articles}}"
    }
  }
}
```

- [ ] **Step 2:** Commit:

```bash
git add testdata/i18n_test_manifest.json
git commit -m "test(i18n): shared manifest fixture for JS+Kotlin runtime tests"
```

---

## Phase 1 — JavaScript runtime

### Task 1a: Locale → currency lookup helper

**Files:**
- Create: `pkg/js/i18n/locale_currency.js`

The Go runtime uses `golang.org/x/text/currency.FromTag` to pick a default currency per locale. JS has no equivalent in `Intl`, so ship a small static table. Keep entries minimal — locales we test against, plus USD fallback.

- [ ] **Step 1:** Write the module:

```js
// localeToCurrency maps a BCP-47 locale (or its base language) to an ISO-4217
// currency code. Mirrors the conventional currencies that
// golang.org/x/text/currency.FromTag would return. USD is the fallback.
const TABLE = {
  "en-US": "USD", "en-GB": "GBP", "en-AU": "AUD", "en-CA": "CAD",
  "en-NZ": "NZD",
  "fr-FR": "EUR", "fr-CA": "CAD", "fr-CH": "CHF",
  "de-DE": "EUR", "de-AT": "EUR", "de-CH": "CHF",
  "es-ES": "EUR", "es-MX": "MXN", "es-AR": "ARS",
  "it-IT": "EUR", "pt-PT": "EUR", "pt-BR": "BRL",
  "ja-JP": "JPY", "ko-KR": "KRW",
  "zh-CN": "CNY", "zh-TW": "TWD", "zh-HK": "HKD",
  "ru-RU": "RUB",
  "nl-NL": "EUR", "nl-BE": "EUR",
  "pl-PL": "PLN", "sv-SE": "SEK", "da-DK": "DKK", "fi-FI": "EUR",
  "no-NO": "NOK", "nb-NO": "NOK",
  "tr-TR": "TRY", "el-GR": "EUR", "hu-HU": "HUF",
};

const BASE = {
  en: "USD", fr: "EUR", de: "EUR", es: "EUR", it: "EUR", pt: "EUR",
  ja: "JPY", ko: "KRW", zh: "CNY", ru: "RUB", nl: "EUR", pl: "PLN",
  sv: "SEK", da: "DKK", fi: "EUR", no: "NOK", nb: "NOK",
  tr: "TRY", el: "EUR", hu: "HUF",
};

export function localeToCurrency(locale) {
  if (TABLE[locale]) return TABLE[locale];
  const base = locale.split("-")[0];
  return BASE[base] || "USD";
}
```

- [ ] **Step 2:** Commit:

```bash
git add pkg/js/i18n/locale_currency.js
git commit -m "feat(pkg/js/i18n): locale-to-currency lookup table"
```

---

### Task 1b: Skeleton runtime + manifest lookup (TDD)

**Files:**
- Create: `pkg/js/i18n/i18n.js` (skeleton)
- Create: `pkg/js/i18n/i18n.test.js`

- [ ] **Step 1: Write the failing test for manifest lookup:**

`pkg/js/i18n/i18n.test.js`:

```js
import { test } from "node:test";
import assert from "node:assert";
import { Translator } from "./i18n.js";

test("manifest lookup: exact locale", () => {
  const m = {
    "Hello, {name}!": { translations: { fr: "Bonjour, {name} !" } },
  };
  const t = new Translator(m, "fr");
  assert.strictEqual(t.tr("Hello, {name}!", "Hello, {name}!", { name: "Alice" }),
    "Bonjour, Alice !");
});

test("manifest lookup: locale fallback en-US -> en", () => {
  const m = {
    "Hello, {name}!": { translations: { en: "Hi, {name}!" } },
  };
  const t = new Translator(m, "en-US");
  assert.strictEqual(t.tr("Hello, {name}!", "Hello, {name}!", { name: "Alice" }),
    "Hi, Alice!");
});

test("manifest lookup: missing key returns inlined template", () => {
  const t = new Translator({}, "en");
  assert.strictEqual(t.tr("nope", "Inline {x}", { x: 1 }), "Inline 1");
});
```

- [ ] **Step 2: Run, expect fail:**

```bash
node --test pkg/js/i18n/i18n.test.js
```

Expected: cannot find module / `Translator is not exported`.

- [ ] **Step 3: Write minimal Translator + simple {name} substitution:**

`pkg/js/i18n/i18n.js`:

```js
// JS runtime for the SNGL i18n stdlib package.
//
// Mirrors pkg/go/i18n/i18n.go in API and ICU semantics. Differences:
//   - no PluralKey struct (string keys only)
//   - locale-aware formatting via Intl.* (browser-native)

import { localeToCurrency } from "./locale_currency.js";

export class Translator {
  constructor(manifest, locale) {
    this.manifest = manifest || {};
    this.locale = locale || "en";
    this.currency = localeToCurrency(this.locale);
  }

  // Lookup `key` in the manifest with locale-fallback (en-US -> en),
  // falling back to inlinedTemplate, then key itself.
  _lookup(key, inlinedTemplate) {
    const e = this.manifest[key];
    if (!e) return inlinedTemplate || key;
    let loc = this.locale;
    while (loc) {
      if (e.translations && e.translations[loc] !== undefined) {
        return e.translations[loc];
      }
      const i = loc.lastIndexOf("-");
      if (i < 0) break;
      loc = loc.slice(0, i);
    }
    return inlinedTemplate || key;
  }

  tr(key, inlinedTemplate, args) {
    const tmpl = this._lookup(key, inlinedTemplate);
    return formatICU(this.locale, this.currency, tmpl, args || {});
  }

  format(template, args) {
    return formatICU(this.locale, this.currency, template, args || {});
  }
}

// formatICU: minimal {name} substitution for now. Extended in Task 1c.
function formatICU(locale, currency, tmpl, args) {
  return tmpl.replace(/\{([^{},]+)\}/g, (_, name) => {
    const k = name.trim();
    return args[k] !== undefined ? String(args[k]) : "";
  });
}
```

- [ ] **Step 4: Run tests:**

```bash
node --test pkg/js/i18n/i18n.test.js
```

Expected: PASS.

- [ ] **Step 5: Commit:**

```bash
git add pkg/js/i18n/i18n.js pkg/js/i18n/i18n.test.js
git commit -m "feat(pkg/js/i18n): Translator skeleton + manifest lookup with locale fallback"
```

---

### Task 1c: Full ICU parser (apostrophe, nested placeholders, splitCases)

Port `formatICU` / `formatICUInto` / `splitPlaceholder` / `splitCases` / `splitPlaceholderArgs` from `pkg/go/i18n/i18n.go`. Same algorithm.

**Files:**
- Modify: `pkg/js/i18n/i18n.js`
- Modify: `pkg/js/i18n/i18n.test.js`

- [ ] **Step 1: Add tests for apostrophe and braces in literal runs:**

Append to `i18n.test.js`:

```js
import { Translator } from "./i18n.js";

test("apostrophe: doubled '' is literal '", () => {
  const t = new Translator({}, "en");
  assert.strictEqual(t.format("Don''t do that", {}), "Don't do that");
});

test("apostrophe: 'X' quotes metachars", () => {
  const t = new Translator({}, "en");
  assert.strictEqual(t.format("Use '{'curly braces'}' literally", {}),
    "Use {curly braces} literally");
});

test("apostrophe: bare ' is literal", () => {
  const t = new Translator({}, "en");
  assert.strictEqual(t.format("It's a fine day", {}), "It's a fine day");
});
```

- [ ] **Step 2: Run, expect fails on apostrophe cases.**

- [ ] **Step 3: Replace `formatICU` with full port:**

Replace the placeholder `formatICU` in `i18n.js` with:

```js
function formatICU(locale, currency, tmpl, args) {
  const out = [];
  formatICUInto(out, locale, currency, tmpl, args, "");
  return out.join("");
}

function formatICUInto(out, locale, currency, tmpl, args, current) {
  let i = 0;
  while (i < tmpl.length) {
    const c = tmpl[i];
    if (c === "'") {
      // Doubled '' → literal '
      if (tmpl[i + 1] === "'") {
        out.push("'");
        i += 2;
        continue;
      }
      const next = tmpl[i + 1];
      if (next === "{" || next === "}" || next === "#" || next === "|") {
        // Quoted run — copy interior literally until closing '
        i++; // consume opening '
        while (i < tmpl.length) {
          if (tmpl[i] === "'") {
            if (tmpl[i + 1] === "'") {
              out.push("'");
              i += 2;
              continue;
            }
            i++; // consume closing '
            break;
          }
          out.push(tmpl[i]);
          i++;
        }
        continue;
      }
      // Bare ' — literal
      out.push("'");
      i++;
      continue;
    }
    if (c === "#" && current !== "") {
      out.push(current);
      i++;
      continue;
    }
    if (c === "{") {
      const r = splitPlaceholder(tmpl, i);
      if (!r) {
        out.push(tmpl.slice(i));
        return;
      }
      renderPlaceholder(out, locale, currency, r.body, args, current);
      i = r.end;
      continue;
    }
    out.push(c);
    i++;
  }
}

function splitPlaceholder(tmpl, start) {
  if (tmpl[start] !== "{") return null;
  let depth = 0;
  for (let i = start; i < tmpl.length; i++) {
    const ch = tmpl[i];
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) {
        return { end: i + 1, body: tmpl.slice(start + 1, i) };
      }
    } else if (ch === "'") {
      // Skip ICU-quoted runs so braces inside don't affect depth.
      if (tmpl[i + 1] === "'") { i++; continue; }
      const next = tmpl[i + 1];
      if (next === "{" || next === "}" || next === "#" || next === "|") {
        i++; // skip opening '
        while (i < tmpl.length) {
          if (tmpl[i] === "'") {
            if (tmpl[i + 1] === "'") { i += 2; continue; }
            break;
          }
          i++;
        }
      }
    }
  }
  return null;
}

function splitPlaceholderArgs(body) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    if (c === "{") depth++;
    else if (c === "}") depth--;
    else if (c === "," && depth === 0) {
      parts.push(body.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(body.slice(start));
  return parts;
}

function splitCases(raw) {
  const pairs = [];
  let i = 0;
  while (i < raw.length) {
    while (i < raw.length && /\s/.test(raw[i])) i++;
    if (i >= raw.length) break;
    const selStart = i;
    while (i < raw.length && raw[i] !== "{") i++;
    if (i >= raw.length) break;
    const sel = raw.slice(selStart, i).trim();
    let depth = 0;
    const bodyStart = i + 1;
    for (; i < raw.length; i++) {
      if (raw[i] === "{") depth++;
      else if (raw[i] === "}") {
        depth--;
        if (depth === 0) {
          pairs.push({ selector: sel, body: raw.slice(bodyStart, i) });
          i++;
          break;
        }
      }
    }
  }
  return pairs;
}
```

- [ ] **Step 4:** Add a stub `renderPlaceholder` (full impl in Task 1d):

```js
function renderPlaceholder(out, locale, currency, body, args, _current) {
  const parts = splitPlaceholderArgs(body);
  const name = parts[0].trim();
  const val = args[name];
  if (parts.length === 1) {
    out.push(val === undefined ? "" : String(val));
    return;
  }
  // Stub — Task 1d fills in plural/select/number/date/time.
  out.push(val === undefined ? "" : String(val));
}
```

- [ ] **Step 5:** Run tests, expect PASS for all `Translator`/apostrophe tests:

```bash
node --test pkg/js/i18n/i18n.test.js
```

- [ ] **Step 6: Commit:**

```bash
git add pkg/js/i18n/i18n.js pkg/js/i18n/i18n.test.js
git commit -m "feat(pkg/js/i18n): full ICU template parser (apostrophe + nested placeholders)"
```

---

### Task 1d: renderPlaceholder — number, date, time, select, plural

**Files:**
- Modify: `pkg/js/i18n/i18n.js`
- Modify: `pkg/js/i18n/i18n.test.js`

- [ ] **Step 1: Tests:**

Append:

```js
test("number: decimal en-US", () => {
  const t = new Translator({}, "en-US");
  assert.strictEqual(t.format("Score: {n, number, decimal}", { n: 1234 }),
    "Score: 1,234");
});

test("number: percent", () => {
  const t = new Translator({}, "en-US");
  // 0.25 → "25%". Intl may emit "25%" or "25 %" depending on engine; accept both.
  const got = t.format("{p, number, percent}", { p: 0.25 });
  assert.match(got, /25\s?%/);
});

test("number: currency picks USD for en-US", () => {
  const t = new Translator({}, "en-US");
  const got = t.format("{x, number, currency}", { x: 1234 });
  assert.match(got, /\$1,234/);
});

test("date: short en-US", () => {
  const t = new Translator({}, "en-US");
  const d = new Date(Date.UTC(2026, 4, 8, 12, 0, 0));
  const got = t.format("On {d, date, short}", { d });
  // "5/8/26" or similar — accept any month/day/year combo.
  assert.match(got, /\d/);
  assert.ok(got.startsWith("On "));
});

test("select: dispatches by string value", () => {
  const t = new Translator({}, "en");
  const tmpl = "{gender, select, female{She} male{He} other{They}}";
  assert.strictEqual(t.format(tmpl, { gender: "female" }), "She");
  assert.strictEqual(t.format(tmpl, { gender: "x" }), "They");
});

test("plural: en cardinal", () => {
  const t = new Translator({}, "en");
  const tmpl = "{n, plural, =0{none} one{# item} other{# items}}";
  assert.strictEqual(t.format(tmpl, { n: 0 }), "none");
  assert.strictEqual(t.format(tmpl, { n: 1 }), "1 item");
  assert.strictEqual(t.format(tmpl, { n: 5 }), "5 items");
});

test("plural: fr cardinal (1.5 → many in some locales; en stays one)", () => {
  const t = new Translator({}, "fr");
  const tmpl = "{n, plural, one{# article} other{# articles}}";
  // French: 0 and 1 are 'one'; 2+ are 'other'
  assert.strictEqual(t.format(tmpl, { n: 0 }), "0 article");
  assert.strictEqual(t.format(tmpl, { n: 2 }), "2 articles");
});
```

- [ ] **Step 2: Run, expect failures.**

- [ ] **Step 3: Replace stub `renderPlaceholder` and add helpers:**

```js
function renderPlaceholder(out, locale, currency, body, args, _current) {
  const parts = splitPlaceholderArgs(body);
  if (parts.length === 0) return;
  const name = parts[0].trim();
  const val = args[name];
  if (parts.length === 1) {
    out.push(val === undefined ? "" : String(val));
    return;
  }
  const typ = parts[1].trim();
  switch (typ) {
    case "plural":
    case "selectordinal":
      renderPlural(out, locale, currency, val, parts.slice(2),
        typ === "selectordinal", args);
      return;
    case "select":
      renderSelect(out, locale, currency, val, parts.slice(2), args);
      return;
    case "number": {
      const style = (parts[2] || "decimal").trim();
      out.push(formatNumber(locale, currency, val, style));
      return;
    }
    case "date": {
      const style = (parts[2] || "medium").trim();
      out.push(formatDate(locale, val, style));
      return;
    }
    case "time": {
      const style = (parts[2] || "medium").trim();
      out.push(formatTime(locale, val, style));
      return;
    }
    case "dateTime": {
      const style = (parts[2] || "medium").trim();
      out.push(formatDateTime(locale, val, style, style));
      return;
    }
    default:
      out.push(val === undefined ? "" : String(val));
  }
}

function renderPlural(out, locale, currency, val, cases, ordinal, args) {
  const raw = cases.join(",").trim();
  const pairs = splitCases(raw);
  const n = toInt(val);
  // Try =N exact match first.
  for (const p of pairs) {
    if (p.selector.startsWith("=") && p.selector.slice(1) === String(n)) {
      formatICUInto(out, locale, currency, p.body, args, String(n));
      return;
    }
  }
  // CLDR keyword.
  const rules = new Intl.PluralRules(locale,
    { type: ordinal ? "ordinal" : "cardinal" });
  const cat = rules.select(n);
  for (const p of pairs) {
    if (p.selector === cat) {
      formatICUInto(out, locale, currency, p.body, args, String(n));
      return;
    }
  }
  // Fallback "other".
  for (const p of pairs) {
    if (p.selector === "other") {
      formatICUInto(out, locale, currency, p.body, args, String(n));
      return;
    }
  }
}

function renderSelect(out, locale, currency, val, cases, args) {
  const raw = cases.join(",").trim();
  const pairs = splitCases(raw);
  const sel = String(val);
  for (const p of pairs) {
    if (p.selector === sel) {
      formatICUInto(out, locale, currency, p.body, args, "");
      return;
    }
  }
  for (const p of pairs) {
    if (p.selector === "other") {
      formatICUInto(out, locale, currency, p.body, args, "");
      return;
    }
  }
}

function toInt(v) {
  if (typeof v === "number") return Math.trunc(v);
  if (typeof v === "string") {
    const n = parseInt(v, 10);
    return Number.isNaN(n) ? 0 : n;
  }
  return 0;
}

// --- Direct formatters ---

function formatNumber(locale, currency, val, style) {
  const n = typeof val === "number" ? val : Number(val);
  if (Number.isNaN(n)) return String(val);
  const opts = numberOptions(currency, style);
  return new Intl.NumberFormat(locale, opts).format(n);
}

function numberOptions(currency, style) {
  switch (style) {
    case "percent":     return { style: "percent" };
    case "currency":    return { style: "currency", currency };
    case "scientific":  return { notation: "scientific" };
    default:            return { style: "decimal" };
  }
}

function formatDate(locale, val, style) {
  const d = toDate(val);
  if (!d) return String(val);
  return new Intl.DateTimeFormat(locale, { dateStyle: dateStyleOpt(style) }).format(d);
}

function formatTime(locale, val, style) {
  const d = toDate(val);
  if (!d) return String(val);
  return new Intl.DateTimeFormat(locale, { timeStyle: dateStyleOpt(style) }).format(d);
}

function formatDateTime(locale, val, dateStyle, timeStyle) {
  const d = toDate(val);
  if (!d) return String(val);
  return new Intl.DateTimeFormat(locale, {
    dateStyle: dateStyleOpt(dateStyle),
    timeStyle: dateStyleOpt(timeStyle),
  }).format(d);
}

function dateStyleOpt(s) {
  // Intl accepts only "full"|"long"|"medium"|"short". Anything else → "medium".
  return ["full", "long", "medium", "short"].includes(s) ? s : "medium";
}

function toDate(val) {
  if (val instanceof Date) return val;
  if (typeof val === "string" || typeof val === "number") {
    const d = new Date(val);
    return Number.isNaN(d.getTime()) ? null : d;
  }
  return null;
}
```

- [ ] **Step 4: Run all tests:**

```bash
node --test pkg/js/i18n/i18n.test.js
```

Expected: PASS.

- [ ] **Step 5: Commit:**

```bash
git add pkg/js/i18n/i18n.js pkg/js/i18n/i18n.test.js
git commit -m "feat(pkg/js/i18n): renderPlaceholder + Intl-backed formatters"
```

---

### Task 1e: Direct-method API + getTranslator()

**Files:**
- Modify: `pkg/js/i18n/i18n.js`
- Modify: `pkg/js/i18n/i18n.test.js`

- [ ] **Step 1: Tests:**

```js
import { getTranslator } from "./i18n.js";

test("getTranslator: returns same instance", () => {
  // Reset by clearing the inlined manifest if any.
  globalThis.__SNGL_I18N_MANIFEST__ = {};
  const a = getTranslator();
  const b = getTranslator();
  assert.strictEqual(a, b);
});

test("Translator.numberInt direct", () => {
  const t = new Translator({}, "en-US");
  assert.strictEqual(t.numberInt(1234, "decimal"), "1,234");
});

test("Translator.select direct", () => {
  const t = new Translator({}, "en");
  assert.strictEqual(t.select("yes", { yes: "Y", other: "?" }), "Y");
  assert.strictEqual(t.select("nope", { yes: "Y", other: "?" }), "?");
});

test("Translator.plural direct with string keys", () => {
  const t = new Translator({}, "en");
  const forms = { "=0": "none", "one": "{n} item", "other": "{n} items" };
  assert.strictEqual(t.plural(0, forms), "none");
  assert.strictEqual(t.plural(1, forms), "1 item");
  assert.strictEqual(t.plural(7, forms), "7 items");
});
```

- [ ] **Step 2: Add direct methods + getTranslator:**

```js
// Append to Translator class:
//
//   numberInt(n, style)    → formatNumber(this.locale, this.currency, n, style)
//   numberFloat(n, style)  → same
//   date(d, style)         → formatDate(this.locale, d, style)
//   time(t, style)         → formatTime(this.locale, t, style)
//   datetime(dt, ds, ts)   → formatDateTime(this.locale, dt, ds, ts)
//   select(value, cases)   → cases[value] ?? cases.other ?? ""
//   plural(count, forms)   → see below
//   selectordinal(...)     → same w/ ordinal=true
```

In `i18n.js`, extend the class:

```js
  numberInt(n, style)   { return formatNumber(this.locale, this.currency, n, style); }
  numberFloat(n, style) { return formatNumber(this.locale, this.currency, n, style); }
  date(d, style)        { return formatDate(this.locale, d, style); }
  time(t, style)        { return formatTime(this.locale, t, style); }
  datetime(dt, ds, ts)  { return formatDateTime(this.locale, dt, ds, ts); }

  select(value, cases) {
    if (cases[value] !== undefined) {
      return formatICU(this.locale, this.currency, cases[value], {});
    }
    if (cases.other !== undefined) {
      return formatICU(this.locale, this.currency, cases.other, {});
    }
    return "";
  }

  plural(count, forms)        { return this._plural(count, forms, false); }
  selectordinal(count, forms) { return this._plural(count, forms, true); }

  _plural(count, forms, ordinal) {
    const args = { "#": count, n: count };
    const exact = forms["=" + count];
    if (exact !== undefined) return formatICU(this.locale, this.currency, exact, args);
    const cat = new Intl.PluralRules(this.locale,
      { type: ordinal ? "ordinal" : "cardinal" }).select(count);
    if (forms[cat] !== undefined) return formatICU(this.locale, this.currency, forms[cat], args);
    if (forms.other !== undefined) return formatICU(this.locale, this.currency, forms.other, args);
    return "";
  }
```

Add module-level:

```js
let _default = null;
export function getTranslator() {
  if (_default !== null) return _default;
  const manifest = (typeof globalThis.__SNGL_I18N_MANIFEST__ === "object"
                    && globalThis.__SNGL_I18N_MANIFEST__ !== null)
    ? globalThis.__SNGL_I18N_MANIFEST__ : {};
  const locale = (typeof navigator !== "undefined" && navigator.language)
    ? navigator.language : "en";
  _default = new Translator(manifest, locale);
  return _default;
}

// For tests: reset the cached translator.
export function _resetTranslator() { _default = null; }
```

- [ ] **Step 3: Run, expect PASS.**

- [ ] **Step 4: Commit:**

```bash
git add pkg/js/i18n/i18n.js pkg/js/i18n/i18n.test.js
git commit -m "feat(pkg/js/i18n): direct formatter methods + getTranslator()"
```

---

## Phase 2 — JavaScript codegen wiring

### Task 2a: jsBuiltinMethodFromArgs i18n cases

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go`
- Modify: `codegen/lang/javascript/javascript.go`

- [ ] **Step 1: Add `IsI18nCall` + import-path constant:**

In `codegen/lang/javascript/javascript.go`, add (or wherever public exports live):

```go
// SnglI18nImportPath is the JS module specifier the generated bundle uses
// to import the i18n runtime. The html platform copies pkg/js/i18n/ into
// the output as a sibling of the entry bundle so this relative path
// resolves at runtime.
const SnglI18nImportPath = "./i18n/i18n.js"

// IsI18nCall reports whether a qualified method name is an i18n stdlib call.
// Mirrors the Go translator's helper of the same name.
func IsI18nCall(qualName string) bool {
	switch qualName {
	case "i18n.tr", "i18n.format",
		"i18n.numberInt", "i18n.numberFloat",
		"i18n.date", "i18n.time", "i18n.datetime",
		"i18n.select",
		"i18n.plural", "i18n.selectordinal", "i18n.exactly":
		return true
	}
	return false
}
```

- [ ] **Step 2: Add cases inside `jsBuiltinMethodFromArgs`** in `codegen/lang/javascript/ircontext.go` (insert after the existing `Alert.confirm` case, before `return ""`):

```go
		// i18n — all calls delegate to the JS runtime.
		case "i18n.tr":
			return "i18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
		case "i18n.format":
			return "i18n.getTranslator().format(" + a(0) + ", " + a(1) + ")"
		case "i18n.numberInt":
			return "i18n.getTranslator().numberInt(" + a(0) + ", " + a(1) + ")"
		case "i18n.numberFloat":
			return "i18n.getTranslator().numberFloat(" + a(0) + ", " + a(1) + ")"
		case "i18n.date":
			return "i18n.getTranslator().date(" + a(0) + ", " + a(1) + ")"
		case "i18n.time":
			return "i18n.getTranslator().time(" + a(0) + ", " + a(1) + ")"
		case "i18n.datetime":
			return "i18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
		case "i18n.select":
			return "i18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
		case "i18n.plural":
			// Namespace-call shape: a(0) is "i18n", real args at a(1), a(2).
			return "i18n.getTranslator().plural(" + a(1) + ", " + a(2) + ")"
		case "i18n.selectordinal":
			return "i18n.getTranslator().selectordinal(" + a(1) + ", " + a(2) + ")"
		case "i18n.exactly":
			return "(\"=\" + (" + a(1) + "))"
```

- [ ] **Step 3: Build:**

```bash
go build ./codegen/lang/javascript/...
```

Expected: clean.

- [ ] **Step 4: Add a JS-codegen unit test exercising `i18n.tr`:**

In `codegen/lang/javascript/translate_ir_test.go` (or a new `i18n_test.go` if the file doesn't allow easy extension), append a test that builds a tiny IR with an `IntrinsicCall{Name: "i18n.tr"}` and asserts the emitted JS contains `i18n.getTranslator().tr(`. Match the existing test style — read the file first:

```bash
grep -n "func Test" codegen/lang/javascript/translate_ir_test.go | head
```

Pick a similar test (e.g. an `Alert.toast` IR test) as a template if one exists; if not, add a minimal new one:

```go
func TestEmitI18nTr(t *testing.T) {
	// Build an IR call: i18n.tr("Hello, {name}!", {name: "Alice"})
	// (use the existing helpers in this file; if there are none yet, build
	// directly with ir.IntrinsicCall + ir.Literal.)
	call := &ir.IntrinsicCall{
		Name: "i18n.tr",
		Args: []ir.Expr{
			&ir.Literal{Kind: ir.StringLit, Value: "Hello, {name}!"},
			&ir.MapLitIR{ /* ... empty or one entry ... */ },
		},
	}
	got := translateExpr(call) // adapt to actual translator entry-point name
	if !strings.Contains(got, "i18n.getTranslator().tr(") {
		t.Errorf("got %q, want call to i18n.getTranslator().tr", got)
	}
}
```

If the local test conventions differ, mirror them. Run:

```bash
go test ./codegen/lang/javascript/ -run TestEmitI18nTr -v
```

Expected: PASS.

- [ ] **Step 5: Commit:**

```bash
git add codegen/lang/javascript/
git commit -m "feat(codegen-js): wire i18n.* intrinsics + IsI18nCall helper"
```

---

### Task 2b: Lower `map<i18n.PluralKey, string>` to string-keyed object

The checker types `forms` as `map<i18n.PluralKey, string>`. The JS translator must emit a plain object with stringified keys instead of `new Map`.

**Files:**
- Modify: `codegen/lang/javascript/translate_ir.go`
- Test: `codegen/lang/javascript/translate_ir_test.go`

- [ ] **Step 1: Find the existing map literal lowering:**

```bash
grep -n "MapLitIR\|MapLit\|mapLit" codegen/lang/javascript/translate_ir.go
```

- [ ] **Step 2: Add a special case for PluralKey-typed maps:**

Inside the lowering, before the generic `new Map(...)` emission:

```go
// Special case: map<i18n.PluralKey, V> lowers to a plain object with
// stringified keys. The JS runtime accepts string keys exclusively;
// PluralKey only exists in the Go runtime where map keys need value
// equality.
if isPluralKeyMapType(m.Type) {
	var b strings.Builder
	b.WriteString("{")
	for i, kv := range m.Entries {
		if i > 0 {
			b.WriteString(", ")
		}
		// kv.Key is already a string-typed expression at this point —
		// emission of `i18n.exactly(n)` produced "(\"=\" + n)" and
		// predeclared keys were lowered to string literals upstream.
		b.WriteString("[")
		b.WriteString(translateExpr(kv.Key))
		b.WriteString("]: ")
		b.WriteString(translateExpr(kv.Value))
	}
	b.WriteString("}")
	return b.String()
}
```

Adapt to the actual function/type names — read the existing `MapLitIR` lowering and match style.

Add the type-check helper:

```go
func isPluralKeyMapType(t *ir.Type) bool {
	if t == nil || t.Name != "map" || len(t.TypeArgs) < 1 {
		return false
	}
	k := t.TypeArgs[0]
	return k != nil && k.Name == "PluralKey" && k.Pkg == "i18n"
}
```

- [ ] **Step 3: Tests** — add a test that builds an IR `MapLitIR` with `Type` set to `map<i18n.PluralKey, string>` and entries `{i18n.one → "..."}`, asserts the emitted JS is `{["one"]: "..."}` (or `{"one": "..."}`).

- [ ] **Step 4: Lower predeclared `i18n.zero/one/two/few/many/other` constants to string literals:**

These are declared in `lib/i18n.sngl` as `const i18n.one i18n.PluralKey = ...`. The JS codegen needs to recognize them. Investigate:

```bash
grep -n "i18n.one\|i18n.zero\|PluralKey" lib/i18n.sngl
grep -rn "PluralOne\|i18n\.one" codegen/lang/golang/ | head
```

In Go, the constant emission produces `i18n.PluralOne`. For JS, change emission of these specific qualified names to string literals: `"one"`, `"two"`, etc. The right hook is wherever qualified-const references are translated — likely in the same `IntrinsicCall` / `Select` lowering. Look for `"i18n.one"` as a qualified name and translate to a string literal.

Add a helper in `codegen/lang/javascript/ircontext.go`:

```go
// jsI18nConstString returns the JS string literal for a predeclared
// i18n.PluralKey constant. Returns "" for non-matches.
func jsI18nConstString(qual string) string {
	switch qual {
	case "i18n.zero":  return `"zero"`
	case "i18n.one":   return `"one"`
	case "i18n.two":   return `"two"`
	case "i18n.few":   return `"few"`
	case "i18n.many":  return `"many"`
	case "i18n.other": return `"other"`
	}
	return ""
}
```

Wire it where qualified constants get lowered (likely a `Select` or `Ident` translator path).

- [ ] **Step 5:** Run JS codegen tests:

```bash
go test ./codegen/lang/javascript/...
```

Expected: PASS.

- [ ] **Step 6: Commit:**

```bash
git add codegen/lang/javascript/
git commit -m "feat(codegen-js): lower map<i18n.PluralKey, V> to string-keyed object"
```

---

## Phase 3 — html platform integration

### Task 3a: Detect i18n usage and copy the runtime

**Files:**
- Modify: `codegen/platform/html/jsbundle.go` (or whichever file emits JS imports — investigate)

- [ ] **Step 1: Find the bundle-emission entry point:**

```bash
grep -n "import\|\.js" codegen/platform/html/jsbundle.go | head -30
ls codegen/platform/html/internal/
```

- [ ] **Step 2: Add a pass that walks the IR for i18n calls:**

```go
// usesI18n reports whether the document calls any i18n intrinsic.
// When true, the platform copies the JS runtime + manifest into the bundle.
func usesI18n(doc *ir.Document) bool {
	found := false
	ir.WalkExprs(doc, func(e ir.Expr) {
		if found { return }
		if c, ok := e.(*ir.IntrinsicCall); ok && javascript.IsI18nCall(c.Name) {
			found = true
		}
	})
	return found
}
```

(Use the existing IR-walking utility if `ir.WalkExprs` doesn't exist — investigate `ir/` for a walker.)

- [ ] **Step 3: When `usesI18n(doc)` is true, copy `pkg/js/i18n/i18n.js` and `pkg/js/i18n/locale_currency.js` into the output dir, and prepend a manifest-init line to the bundle entry.**

Embed the runtime files via `//go:embed`:

```go
//go:embed pkg_js_i18n/i18n.js pkg_js_i18n/locale_currency.js
var i18nRuntimeFS embed.FS
```

Or symlink/copy at build time. Simpler: read from disk relative to the source tree at compile time:

```go
import _ "embed"

//go:embed all:i18n_runtime
var i18nRuntimeFS embed.FS
```

Investigate how other runtime files are shipped (e.g., is there a precedent for embedding `lib/`?). Check:

```bash
grep -rn "go:embed" codegen/platform/html/ codegen/platform/
```

Whatever pattern exists, follow it. If none exists, add an `embed.FS` here.

- [ ] **Step 4: Emit the manifest prefix:**

Read `i18n.manifest.json` from the project root (next to the user's source). If present, JSON-encode it and prepend:

```js
globalThis.__SNGL_I18N_MANIFEST__ = <inlined JSON>;
```

If absent, omit the prefix.

- [ ] **Step 5: Emit an `import * as i18n from "./i18n/i18n.js";` at the top of the bundle entry** (or however the JS lang's import statements are typically emitted — match the existing pattern for stdlib imports).

- [ ] **Step 6: Test:**

Add `cmd/sngl/testdata/compile_js_i18n.txt` (txtar):

```
sngl compile --platform html --lang none src/main.sngl
exists out/index.html
exists out/i18n/i18n.js
exists out/i18n/locale_currency.js

-- src/main.sngl --
window {
  body {
    Text { value = $"Hello, world!" }
  }
}

-- src/i18n.manifest.json --
{
  "Hello, world!": { "translations": { "fr": "Bonjour, le monde !" } }
}
```

Run:

```bash
go test ./cmd/sngl/ -run "TestScript/compile_js_i18n" -v
```

If the txtar harness can't yet check file contents, expand the assertion later.

- [ ] **Step 7: Commit:**

```bash
git add codegen/platform/html/ cmd/sngl/testdata/compile_js_i18n.txt
git commit -m "feat(html): copy JS i18n runtime + inline manifest when used"
```

---

### Task 3b: Browser smoke test

**Files:**
- Create: `testdata/i18n_browser_smoke.sngl`

- [ ] **Step 1: Write the fixture:**

```sngl
window {
  model count: int = 1
  body {
    Text { value = $"You have {count, plural, one{# item} other{# items}}" }
    Button { label = "Add"; onClick = { count = count + 1 } }
  }
}
```

- [ ] **Step 2: Add an html browser test that loads the page, asserts initial text, clicks the button, and asserts re-rendered plural form** — mirror the existing `funcvar_browser_test.go` or `async_browser_test.go` patterns.

- [ ] **Step 3: Run:**

```bash
go test ./codegen/platform/html/ -run "TestI18nBrowserSmoke" -v
```

Expected: PASS — page renders "1 item", click renders "2 items".

- [ ] **Step 4: Commit:**

```bash
git add codegen/platform/html/i18n_browser_test.go testdata/i18n_browser_smoke.sngl
git commit -m "test(html): browser smoke test for $\"...\" plural rendering"
```

---

## Phase 4 — Kotlin runtime

### Task 4a: Skeleton + manifest lookup

**Files:**
- Create: `pkg/kotlin/i18n/I18n.kt`
- Create: `pkg/kotlin/i18n/I18nTest.kt`

The runtime uses `android.icu.*` APIs. Tests run via Robolectric (so the `android.icu` classpath is available without an emulator). Gate the JUnit run behind a build tag if Robolectric isn't already in the project's test deps.

- [ ] **Step 1: Write the skeleton:**

```kotlin
// File: pkg/kotlin/i18n/I18n.kt
package us.duckfam.git.jonathan.sngl.i18n

import android.content.Context
import android.icu.text.DateFormat
import android.icu.text.NumberFormat
import android.icu.text.PluralRules
import org.json.JSONObject
import java.util.Date
import java.util.Locale

typealias Manifest = Map<String, Map<String, String>>

class Translator(val manifest: Manifest, val locale: Locale) {
    private val currencyCode: String = localeToCurrency(locale)

    private fun lookup(key: String, inlinedTemplate: String): String {
        val entry = manifest[key] ?: return inlinedTemplate.ifEmpty { key }
        var loc = locale.toLanguageTag()
        while (loc.isNotEmpty()) {
            entry[loc]?.let { return it }
            val i = loc.lastIndexOf('-')
            if (i < 0) break
            loc = loc.substring(0, i)
        }
        return inlinedTemplate.ifEmpty { key }
    }

    fun tr(key: String, inlinedTemplate: String, args: Map<String, Any?>): String =
        formatICU(locale, currencyCode, lookup(key, inlinedTemplate), args)

    fun format(template: String, args: Map<String, Any?>): String =
        formatICU(locale, currencyCode, template, args)

    fun numberInt(n: Int, style: String): String =
        formatNumber(locale, currencyCode, n.toDouble(), style)

    fun numberFloat(n: Double, style: String): String =
        formatNumber(locale, currencyCode, n, style)

    fun date(d: Date, style: String): String =
        DateFormat.getDateInstance(dateStyleConst(style), locale).format(d)

    fun time(t: Date, style: String): String =
        DateFormat.getTimeInstance(dateStyleConst(style), locale).format(t)

    fun datetime(dt: Date, dateStyle: String, timeStyle: String): String =
        DateFormat.getDateTimeInstance(
            dateStyleConst(dateStyle), dateStyleConst(timeStyle), locale
        ).format(dt)

    fun select(value: String, cases: Map<String, String>): String {
        val tmpl = cases[value] ?: cases["other"] ?: return ""
        return formatICU(locale, currencyCode, tmpl, emptyMap())
    }

    fun plural(count: Int, forms: Map<String, String>): String =
        pluralImpl(count, forms, false)

    fun selectordinal(count: Int, forms: Map<String, String>): String =
        pluralImpl(count, forms, true)

    private fun pluralImpl(count: Int, forms: Map<String, String>, ordinal: Boolean): String {
        val args = mapOf<String, Any?>("#" to count, "n" to count)
        forms["=$count"]?.let { return formatICU(locale, currencyCode, it, args) }
        val type = if (ordinal) PluralRules.PluralType.ORDINAL else PluralRules.PluralType.CARDINAL
        val cat = PluralRules.forLocale(locale, type).select(count.toDouble())
        forms[cat]?.let { return formatICU(locale, currencyCode, it, args) }
        forms["other"]?.let { return formatICU(locale, currencyCode, it, args) }
        return ""
    }
}

// (formatICU, formatNumber, splitPlaceholder, etc. — Phase 4b)

object I18n {
    @Volatile private var default: Translator? = null

    @JvmStatic
    fun getTranslator(): Translator {
        default?.let { return it }
        synchronized(this) {
            default?.let { return it }
            val t = Translator(emptyMap(), Locale.getDefault())
            default = t
            return t
        }
    }

    /** Called from generated Application.onCreate to load assets/i18n.manifest.json. */
    @JvmStatic
    fun init(ctx: Context) {
        val manifest = loadManifest(ctx)
        synchronized(this) {
            default = Translator(manifest, Locale.getDefault())
        }
    }
}

private fun loadManifest(ctx: Context): Manifest {
    return try {
        val json = ctx.assets.open("i18n.manifest.json").bufferedReader().use { it.readText() }
        val root = JSONObject(json)
        val out = mutableMapOf<String, Map<String, String>>()
        for (key in root.keys()) {
            val entry = root.getJSONObject(key)
            val translations = entry.optJSONObject("translations") ?: continue
            val per = mutableMapOf<String, String>()
            for (loc in translations.keys()) per[loc] = translations.getString(loc)
            out[key] = per
        }
        out
    } catch (e: Exception) {
        emptyMap()
    }
}

private fun dateStyleConst(style: String): Int = when (style) {
    "short" -> DateFormat.SHORT
    "long"  -> DateFormat.LONG
    "full"  -> DateFormat.FULL
    else    -> DateFormat.MEDIUM
}

private fun localeToCurrency(locale: Locale): String {
    return try {
        java.util.Currency.getInstance(locale).currencyCode
    } catch (e: Exception) {
        "USD"
    }
}

private fun formatNumber(locale: Locale, currency: String, n: Double, style: String): String {
    val nf = when (style) {
        "percent"    -> NumberFormat.getPercentInstance(locale).also {
            // SNGL convention: percent input is 0..1.
        }
        "currency"   -> NumberFormat.getCurrencyInstance(locale).also {
            it.currency = android.icu.util.Currency.getInstance(currency)
        }
        "scientific" -> NumberFormat.getScientificInstance(locale)
        else         -> NumberFormat.getInstance(locale)
    }
    return nf.format(n)
}
```

- [ ] **Step 2: Skip ICU template parser for now — emit raw value for placeholders.** The Phase 4b task ports the full parser. Add a stub:

```kotlin
private fun formatICU(locale: Locale, currency: String, tmpl: String, args: Map<String, Any?>): String {
    // Stub — Phase 4b ports the full parser.
    return tmpl
}
```

- [ ] **Step 3: Verify the file compiles:**

If the project has Kotlin tooling configured already (check `examples/` or any `.gradle.kts`), build it. Otherwise, defer compile validation to Phase 5 when codegen integration tests build a real Android module.

- [ ] **Step 4: Commit:**

```bash
git add pkg/kotlin/i18n/
git commit -m "feat(pkg/kotlin/i18n): Translator skeleton + manifest lookup + Intl-backed formatters"
```

---

### Task 4b: Port ICU parser to Kotlin

**Files:**
- Modify: `pkg/kotlin/i18n/I18n.kt`
- Modify: `pkg/kotlin/i18n/I18nTest.kt`

- [ ] **Step 1: Write tests** (Robolectric-based):

```kotlin
@RunWith(RobolectricTestRunner::class)
class I18nTest {
    @Test fun `apostrophe doubled is literal`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Don't do that", t.format("Don''t do that", emptyMap()))
    }

    @Test fun `apostrophe quotes braces`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Use {literal}", t.format("Use '{'literal'}'", emptyMap()))
    }

    @Test fun `simple substitution`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Hello, Alice!",
            t.format("Hello, {name}!", mapOf("name" to "Alice")))
    }

    @Test fun `plural en cardinal`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{n, plural, =0{none} one{# item} other{# items}}"
        assertEquals("none", t.format(tmpl, mapOf("n" to 0)))
        assertEquals("1 item", t.format(tmpl, mapOf("n" to 1)))
        assertEquals("5 items", t.format(tmpl, mapOf("n" to 5)))
    }

    @Test fun `select dispatch`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{g, select, female{She} male{He} other{They}}"
        assertEquals("She", t.format(tmpl, mapOf("g" to "female")))
        assertEquals("They", t.format(tmpl, mapOf("g" to "x")))
    }

    @Test fun `direct plural with string keys`() {
        val t = Translator(emptyMap(), Locale.US)
        val forms = mapOf("=0" to "none", "one" to "{n} item", "other" to "{n} items")
        assertEquals("none", t.plural(0, forms))
        assertEquals("1 item", t.plural(1, forms))
        assertEquals("7 items", t.plural(7, forms))
    }
}
```

- [ ] **Step 2: Replace `formatICU` stub** with full port (same algorithm as Phase 1c/d, transcribed to Kotlin):

```kotlin
private fun formatICU(locale: Locale, currency: String, tmpl: String, args: Map<String, Any?>): String {
    val out = StringBuilder()
    formatICUInto(out, locale, currency, tmpl, args, "")
    return out.toString()
}

private fun formatICUInto(out: StringBuilder, locale: Locale, currency: String,
                          tmpl: String, args: Map<String, Any?>, current: String) {
    var i = 0
    while (i < tmpl.length) {
        val c = tmpl[i]
        if (c == '\'') {
            if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                out.append('\''); i += 2; continue
            }
            val next = if (i + 1 < tmpl.length) tmpl[i + 1] else ' '
            if (next == '{' || next == '}' || next == '#' || next == '|') {
                i++ // opening '
                while (i < tmpl.length) {
                    if (tmpl[i] == '\'') {
                        if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                            out.append('\''); i += 2; continue
                        }
                        i++; break
                    }
                    out.append(tmpl[i]); i++
                }
                continue
            }
            out.append('\''); i++; continue
        }
        if (c == '#' && current.isNotEmpty()) {
            out.append(current); i++; continue
        }
        if (c == '{') {
            val r = splitPlaceholder(tmpl, i)
            if (r == null) {
                out.append(tmpl.substring(i)); return
            }
            renderPlaceholder(out, locale, currency, r.body, args, current)
            i = r.end; continue
        }
        out.append(c); i++
    }
}

private data class PhBody(val end: Int, val body: String)

private fun splitPlaceholder(tmpl: String, start: Int): PhBody? {
    if (start >= tmpl.length || tmpl[start] != '{') return null
    var depth = 0
    var i = start
    while (i < tmpl.length) {
        when (tmpl[i]) {
            '{' -> depth++
            '}' -> { depth--; if (depth == 0) return PhBody(i + 1, tmpl.substring(start + 1, i)) }
            '\'' -> {
                if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') { i++ }
                else {
                    val next = if (i + 1 < tmpl.length) tmpl[i + 1] else ' '
                    if (next == '{' || next == '}' || next == '#' || next == '|') {
                        i++
                        while (i < tmpl.length) {
                            if (tmpl[i] == '\'') {
                                if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') { i += 2; continue }
                                break
                            }
                            i++
                        }
                    }
                }
            }
        }
        i++
    }
    return null
}

private fun splitPlaceholderArgs(body: String): List<String> {
    val parts = mutableListOf<String>()
    var depth = 0; var start = 0
    for (i in body.indices) {
        when (body[i]) {
            '{' -> depth++
            '}' -> depth--
            ',' -> if (depth == 0) { parts.add(body.substring(start, i)); start = i + 1 }
        }
    }
    parts.add(body.substring(start))
    return parts
}

private data class CasePair(val selector: String, val body: String)

private fun splitCases(raw: String): List<CasePair> {
    val pairs = mutableListOf<CasePair>()
    var i = 0
    while (i < raw.length) {
        while (i < raw.length && raw[i].isWhitespace()) i++
        if (i >= raw.length) break
        val selStart = i
        while (i < raw.length && raw[i] != '{') i++
        if (i >= raw.length) break
        val sel = raw.substring(selStart, i).trim()
        var depth = 0
        val bodyStart = i + 1
        while (i < raw.length) {
            if (raw[i] == '{') depth++
            else if (raw[i] == '}') {
                depth--
                if (depth == 0) {
                    pairs.add(CasePair(sel, raw.substring(bodyStart, i)))
                    i++; break
                }
            }
            i++
        }
    }
    return pairs
}

private fun renderPlaceholder(out: StringBuilder, locale: Locale, currency: String,
                              body: String, args: Map<String, Any?>, current: String) {
    val parts = splitPlaceholderArgs(body)
    if (parts.isEmpty()) return
    val name = parts[0].trim()
    val v = args[name]
    if (parts.size == 1) {
        out.append(v?.toString() ?: ""); return
    }
    val typ = parts[1].trim()
    when (typ) {
        "plural", "selectordinal" ->
            renderPlural(out, locale, currency, v, parts.drop(2), typ == "selectordinal", args)
        "select" ->
            renderSelect(out, locale, currency, v, parts.drop(2), args)
        "number" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "decimal"
            out.append(formatNumber(locale, currency, toDouble(v), style))
        }
        "date" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(DateFormat.getDateInstance(dateStyleConst(style), locale).format(it))
            } ?: out.append(v?.toString() ?: "")
        }
        "time" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(DateFormat.getTimeInstance(dateStyleConst(style), locale).format(it))
            } ?: out.append(v?.toString() ?: "")
        }
        "dateTime" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(DateFormat.getDateTimeInstance(
                    dateStyleConst(style), dateStyleConst(style), locale).format(it))
            } ?: out.append(v?.toString() ?: "")
        }
        else -> out.append(v?.toString() ?: "")
    }
}

private fun renderPlural(out: StringBuilder, locale: Locale, currency: String, v: Any?,
                         cases: List<String>, ordinal: Boolean, args: Map<String, Any?>) {
    val raw = cases.joinToString(",").trim()
    val pairs = splitCases(raw)
    val n = toInt(v)
    pairs.firstOrNull { it.selector.startsWith("=") && it.selector.substring(1) == n.toString() }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
    val type = if (ordinal) PluralRules.PluralType.ORDINAL else PluralRules.PluralType.CARDINAL
    val cat = PluralRules.forLocale(locale, type).select(n.toDouble())
    pairs.firstOrNull { it.selector == cat }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
    pairs.firstOrNull { it.selector == "other" }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
}

private fun renderSelect(out: StringBuilder, locale: Locale, currency: String, v: Any?,
                         cases: List<String>, args: Map<String, Any?>) {
    val raw = cases.joinToString(",").trim()
    val pairs = splitCases(raw)
    val sel = v?.toString() ?: ""
    pairs.firstOrNull { it.selector == sel }
        ?.let { formatICUInto(out, locale, currency, it.body, args, ""); return }
    pairs.firstOrNull { it.selector == "other" }
        ?.let { formatICUInto(out, locale, currency, it.body, args, ""); return }
}

private fun toInt(v: Any?): Int = when (v) {
    is Int -> v; is Long -> v.toInt(); is Double -> v.toInt(); is Float -> v.toInt()
    is String -> v.toIntOrNull() ?: 0
    else -> 0
}

private fun toDouble(v: Any?): Double = when (v) {
    is Int -> v.toDouble(); is Long -> v.toDouble(); is Double -> v; is Float -> v.toDouble()
    is String -> v.toDoubleOrNull() ?: 0.0
    else -> 0.0
}
```

- [ ] **Step 2: Run tests:**

If Robolectric is set up:

```bash
cd examples/<some-android-project> && ./gradlew :test --tests "*I18nTest*"
```

If not, defer test execution until Phase 5 when an example app exists. Note in the commit message that tests are pending Robolectric wiring.

- [ ] **Step 3: Commit:**

```bash
git add pkg/kotlin/i18n/I18n.kt pkg/kotlin/i18n/I18nTest.kt
git commit -m "feat(pkg/kotlin/i18n): full ICU parser + plural/select/number/date dispatch"
```

---

## Phase 5 — Kotlin codegen wiring

### Task 5a: kotlinBuiltinMethodFromArgs i18n cases

**Files:**
- Modify: `codegen/lang/kotlin/ircontext.go`
- Modify: `codegen/lang/kotlin/kotlin.go`

- [ ] **Step 1: Add `IsI18nCall` and runtime package constant** in `codegen/lang/kotlin/kotlin.go`:

```go
const SnglI18nKotlinPackage = "us.duckfam.git.jonathan.sngl.i18n"

func IsI18nCall(qualName string) bool {
	switch qualName {
	case "i18n.tr", "i18n.format",
		"i18n.numberInt", "i18n.numberFloat",
		"i18n.date", "i18n.time", "i18n.datetime",
		"i18n.select",
		"i18n.plural", "i18n.selectordinal", "i18n.exactly":
		return true
	}
	return false
}
```

- [ ] **Step 2: Add cases inside `kotlinBuiltinMethodFromArgs`** in `codegen/lang/kotlin/ircontext.go`:

```go
		case "i18n.tr":
			return "I18n.getTranslator().tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
		case "i18n.format":
			return "I18n.getTranslator().format(" + a(0) + ", " + a(1) + ")"
		case "i18n.numberInt":
			return "I18n.getTranslator().numberInt(" + a(0) + ", " + a(1) + ")"
		case "i18n.numberFloat":
			return "I18n.getTranslator().numberFloat(" + a(0) + ", " + a(1) + ")"
		case "i18n.date":
			return "I18n.getTranslator().date(" + a(0) + ", " + a(1) + ")"
		case "i18n.time":
			return "I18n.getTranslator().time(" + a(0) + ", " + a(1) + ")"
		case "i18n.datetime":
			return "I18n.getTranslator().datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
		case "i18n.select":
			return "I18n.getTranslator().select(" + a(0) + ", " + a(1) + ")"
		case "i18n.plural":
			return "I18n.getTranslator().plural(" + a(1) + ", " + a(2) + ")"
		case "i18n.selectordinal":
			return "I18n.getTranslator().selectordinal(" + a(1) + ", " + a(2) + ")"
		case "i18n.exactly":
			return "(\"=\" + (" + a(1) + "))"
```

- [ ] **Step 3: Build:**

```bash
go build ./codegen/lang/kotlin/...
```

- [ ] **Step 4: Add a translator unit test mirroring the JS one** (use a similar pattern from the existing kotlin tests).

- [ ] **Step 5: Commit:**

```bash
git add codegen/lang/kotlin/
git commit -m "feat(codegen-kotlin): wire i18n.* intrinsics + IsI18nCall helper"
```

---

### Task 5b: Lower `map<i18n.PluralKey, string>` to `Map<String, String>`

Same shape as Task 2b but for Kotlin. The Kotlin map literal already lowers `map<K, V>` to `mapOf(...)`; the change is to translate `i18n.PluralKey` keys to strings.

**Files:**
- Modify: `codegen/lang/kotlin/translate_ir.go`
- Modify: `codegen/lang/kotlin/ircontext.go`

- [ ] **Step 1: Lower predeclared `i18n.zero`/`one`/etc. to string literals:**

```go
func kotlinI18nConstString(qual string) string {
	switch qual {
	case "i18n.zero":  return `"zero"`
	case "i18n.one":   return `"one"`
	case "i18n.two":   return `"two"`
	case "i18n.few":   return `"few"`
	case "i18n.many":  return `"many"`
	case "i18n.other": return `"other"`
	}
	return ""
}
```

Wire into the qualified-const lowering path.

- [ ] **Step 2: Override the IR type for `i18n.PluralKey` to `String`** during Kotlin type emission. Find `IRTypeToKotlin` (or equivalent):

```bash
grep -n "IRTypeTo\|kotlinType\|toKotlin" codegen/lang/kotlin/*.go
```

Add:

```go
// i18n.PluralKey on Kotlin lowers to String — see runtime design.
if t.Pkg == "i18n" && t.Name == "PluralKey" {
    return "String"
}
```

- [ ] **Step 3: Tests + commit:**

```bash
go test ./codegen/lang/kotlin/...
git add codegen/lang/kotlin/
git commit -m "feat(codegen-kotlin): lower i18n.PluralKey to String"
```

---

## Phase 6 — android platform integration

### Task 6a: Copy Kotlin runtime + manifest asset

**Files:**
- Modify: `codegen/platform/android/scaffold.go` (or wherever the android module is materialized)

- [ ] **Step 1: Find the scaffold entry point:**

```bash
grep -n "scaffold\|writeFile\|Mkdir\|Application" codegen/platform/android/*.go | head
```

- [ ] **Step 2: Detect i18n usage** (mirror Task 3a):

```go
import "git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"

func usesI18n(doc *ir.Document) bool {
    found := false
    ir.WalkExprs(doc, func(e ir.Expr) {
        if found { return }
        if c, ok := e.(*ir.IntrinsicCall); ok && kotlin.IsI18nCall(c.Name) {
            found = true
        }
    })
    return found
}
```

- [ ] **Step 3: When true:**
  1. Embed `pkg/kotlin/i18n/I18n.kt` and write it to `<module>/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/I18n.kt`.
  2. If a project-root `i18n.manifest.json` exists, copy it to `<module>/src/main/assets/i18n.manifest.json`.
  3. Inject `I18n.init(this)` into the generated `Application.onCreate` (or, if the project has no Application, generate one — investigate the existing scaffold for whether a custom Application is already emitted).

- [ ] **Step 4: Test** — add `cmd/sngl/testdata/compile_android_i18n.txt` (txtar) verifying the generated module contains the runtime file and the manifest asset.

- [ ] **Step 5: Commit:**

```bash
git add codegen/platform/android/ cmd/sngl/testdata/compile_android_i18n.txt
git commit -m "feat(android): copy Kotlin i18n runtime + manifest asset + Application init"
```

---

### Task 6b: Android smoke test

**Files:**
- Create: `testdata/i18n_android_smoke.sngl`

- [ ] **Step 1: Same fixture as Task 3b but compiled for android+kotlin.** Run via the existing android compiler test harness (do not require a device — just verify the module builds via `gradle :assembleDebug` if the test runner is wired for that, otherwise just compile-check via `kotlinc`).

- [ ] **Step 2: Commit.**

---

## Phase 7 — Documentation + final sweep

### Task 7: Update CLAUDE.md and final verification

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update the runtime-packages section** in `CLAUDE.md` (it currently mentions only `pkg/go/i18n/`):

Replace the existing list with:

```markdown
- `pkg/go/i18n/` — Go runtime backing the `i18n` SNGL stdlib package (manifest loader, ICU formatter, date/number formatters wrapping `golang.org/x/text` and `github.com/goodsign/monday`).
- `pkg/js/i18n/` — JavaScript runtime: ICU template parser plus `Intl.NumberFormat`/`DateTimeFormat`/`PluralRules`. Manifest inlined as `globalThis.__SNGL_I18N_MANIFEST__` by the html platform.
- `pkg/kotlin/i18n/` — Kotlin/Android runtime: ICU parser plus `android.icu.text.*`. Manifest loaded from `assets/i18n.manifest.json` via `I18n.init(context)` in `Application.onCreate`.
```

- [ ] **Step 2: Final verify:**

```bash
go tool verify
node --test pkg/js/i18n/i18n.test.js
```

(Robolectric Kotlin tests, if not yet runnable in this repo, are gated; flag in commit message.)

- [ ] **Step 3: Commit:**

```bash
git add CLAUDE.md
git commit -m "docs: pkg/js/i18n and pkg/kotlin/i18n in runtime-packages list"
```

- [ ] **Step 4: PR-ready review** — `git log main..HEAD` should show ~14 focused commits across phases 0-7.

---

## Self-review

### Spec coverage

| Spec section | Task |
|---|---|
| §JS runtime: Translator + manifest | 1b, 1c, 1d, 1e |
| §JS runtime: getTranslator + globalThis | 1e |
| §JS runtime: locale → currency | 1a |
| §Kotlin runtime: Translator | 4a, 4b |
| §Kotlin runtime: I18n.init + assets | 4a |
| §Codegen wiring (JS) | 2a, 2b |
| §Codegen wiring (Kotlin) | 5a, 5b |
| §Map literal lowering (string keys) | 2b, 5b |
| §Platform wiring (html) | 3a |
| §Platform wiring (android) | 6a |
| §Tests (browser smoke) | 3b |
| §Tests (android smoke) | 6b |
| §CLAUDE.md docs | 7 |

No spec section is uncovered.

### Placeholder scan

Phrases like "investigate", "find the entry point", and "match the existing pattern" appear in Tasks 2a/2b/3a/5b/6a where the precise hook depends on local conventions the engineer must read. These are real instructions to read existing code, not lazy placeholders — the alternative would be to guess at internal helper names that may not exist.

### Type consistency

- `Translator` exposes the same methods on JS and Kotlin: `tr`, `format`, `numberInt`, `numberFloat`, `date`, `time`, `datetime`, `select`, `plural`, `selectordinal`. Codegen emission strings in Tasks 2a/5a match these names exactly.
- Plural keys are strings on both targets: `"=N"` for exact, CLDR keyword for category.
- `IsI18nCall` exported from both `codegen/lang/javascript` and `codegen/lang/kotlin`, with identical case lists.

### Risk notes

- **Task 3a / 6a** — embedding the runtime files via `//go:embed`. If the existing platforms ship their runtime templates differently (e.g. read from disk relative to the Go module root), follow that pattern. Investigate before committing.
- **Task 4b tests** — Robolectric may not be set up. If so, run only `kotlinc`-level compile checks in this plan; integration tests deferred to a follow-up.
- **Bundle inlining vs. file copy** — Task 3a default is to copy `pkg/js/i18n/*` into the output directory and emit a relative ESM import. If the html platform already inlines all JS into a single file, do the same here (bundle the runtime into the entry script).
