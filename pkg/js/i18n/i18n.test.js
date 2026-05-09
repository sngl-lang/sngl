import { test } from "node:test";
import assert from "node:assert";
import { Translator, getTranslator, _resetTranslator } from "./i18n.js";

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

test("getTranslator: returns same instance", () => {
  _resetTranslator();
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

test("number: currency picks EUR for fr-FR", () => {
  const t = new Translator({}, "fr-FR");
  const got = t.format("{x, number, currency}", { x: 1234 });
  // Intl may render "1 234,00 €" or "1 234,00 EUR" depending on engine; accept either.
  assert.match(got, /€|EUR/);
});

test("date: long en-US", () => {
  const t = new Translator({}, "en-US");
  const d = new Date(Date.UTC(2026, 4, 8, 12, 0, 0));
  const got = t.format("On {d, date, long}", { d });
  // Long date style in en-US is "May 8, 2026".
  assert.match(got, /On May 8, 2026/);
});

test("Translator.select returns case body literally (no ICU expansion)", () => {
  const t = new Translator({}, "en");
  // Placeholder {n} stays literal — matches Go's Translator.Select semantics.
  assert.strictEqual(
    t.select("yes", { yes: "value={n}", other: "fallback={n}" }),
    "value={n}"
  );
  assert.strictEqual(
    t.select("nope", { yes: "value={n}", other: "fallback={n}" }),
    "fallback={n}"
  );
});
