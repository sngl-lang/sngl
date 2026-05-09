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
