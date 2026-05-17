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

  numberInt(n, style)   { return formatNumber(this.locale, this.currency, n, style); }
  numberFloat(n, style) { return formatNumber(this.locale, this.currency, n, style); }
  date(d, style)        { return formatDate(this.locale, d, style); }
  time(t, style)        { return formatTime(this.locale, t, style); }
  datetime(dt, ds, ts)  { return formatDateTime(this.locale, dt, ds, ts); }

  select(value, cases) {
    // cases may be a JS Map (from SNGL map<string, string>) or a plain
    // object (e.g. when called from user JS). Handle both.
    if (cases instanceof Map) {
      if (cases.has(value)) return cases.get(value);
      if (cases.has("other")) return cases.get("other");
      return "";
    }
    if (cases[value] !== undefined) return cases[value];
    if (cases.other !== undefined) return cases.other;
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
}

// --- ICU template parser ---

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

// argGet retrieves a named argument from either a Map or a plain object.
// SNGL lowers i18n args as Map for most target langs but as plain objects
// in some codegen contexts; this helper handles both.
function argGet(args, name) {
  if (args instanceof Map) return args.get(name);
  return args[name];
}

function renderPlaceholder(out, locale, currency, body, args, current) {
  const parts = splitPlaceholderArgs(body);
  if (parts.length === 0) return;
  const name = parts[0].trim();
  const val = argGet(args, name);
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

// --- Module-level singleton ---

// defaultLocale returns the process-startup BCP-47 locale string.
// In browser contexts, reads navigator.language (or navigator.languages[0])
// and falls back to "en-US" if unavailable.
// This is the JS runtime implementation of the SNGL stdlib i18n.defaultLocale().
export function defaultLocale() {
  if (typeof navigator !== "undefined") {
    const lang = (navigator.languages && navigator.languages.length > 0)
      ? navigator.languages[0]
      : navigator.language;
    if (lang) return lang;
  }
  return "en-US";
}

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
export function _resetTranslator() { _default = null; _translatorByLocale.clear(); }

// --- Per-locale entry points ---
//
// Each function below routes to a locale-keyed Translator. After NoContext +
// InlinePure rewrites i18n.* wrapper calls into direct intl.* intrinsic
// calls, the generated code targets these exports with the active locale
// threaded as the first argument.

const _translatorByLocale = new Map();

function _translatorFor(locale) {
  if (!locale) locale = defaultLocale();
  let t = _translatorByLocale.get(locale);
  if (t) return t;
  const manifest = (typeof globalThis.__SNGL_I18N_MANIFEST__ === "object"
                    && globalThis.__SNGL_I18N_MANIFEST__ !== null)
    ? globalThis.__SNGL_I18N_MANIFEST__ : {};
  t = new Translator(manifest, locale);
  _translatorByLocale.set(locale, t);
  return t;
}

export function translate(locale, key, inlinedTemplate, args) {
  return _translatorFor(locale).tr(key, inlinedTemplate, args || {});
}

export function format(locale, template, args) {
  return _translatorFor(locale).format(template, args || {});
}

export function numberInt(locale, n, style) {
  return _translatorFor(locale).numberInt(n, style);
}

export function numberFloat(locale, n, style) {
  return _translatorFor(locale).numberFloat(n, style);
}

export function date(locale, d, style) {
  return _translatorFor(locale).date(d, style);
}

export function time(locale, t, style) {
  return _translatorFor(locale).time(t, style);
}

export function datetime(locale, dt, dateStyle, timeStyle) {
  return _translatorFor(locale).datetime(dt, dateStyle, timeStyle);
}

// Renamed from `select` to avoid clashing with the SQL-ish reserved word
// in some toolchains; the codegen emits `i18n.select` so the export name
// must stay as-is.
const _selectImpl = function(locale, value, cases) {
  return _translatorFor(locale).select(value, cases);
};
export { _selectImpl as select };

export function plural(locale, count, forms) {
  return _translatorFor(locale).plural(count, forms);
}

export function selectordinal(locale, count, forms) {
  return _translatorFor(locale).selectordinal(count, forms);
}
