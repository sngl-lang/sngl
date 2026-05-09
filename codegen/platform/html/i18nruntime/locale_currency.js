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
