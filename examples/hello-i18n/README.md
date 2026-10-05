# hello-i18n

Hello-world app exercising the i18n runtime: gendered `select`, count-based `plural`, locale-aware currency, and locale-aware number formatting. Manifest covers English (default), French, and Spanish.

The single rendered sentence

> Ms. Alice is bringing 1 guest to the party.

becomes

- (fr) Mme Alice amène 1 invité à la fête.
- (es) Sra. Alice trae 1 invitado a la fiesta.

…and changes plural form when you increment the guest counter.

## Run on `html` (browser)

```bash
sngl generate --platform html --out out/web examples/hello-i18n/
```

Outputs `out/web/index.html` with the JS runtime bundled inline. Open the HTML file in a browser. Locale picks up from `navigator.language` — change your browser language to `fr` or `es` to see the translations.

The manifest is inlined as `globalThis.__SNGL_I18N_MANIFEST__` at the top of the bundle.

## Run on `android`

```bash
sngl generate --platform android --lang kotlin --opt main=true --out out/android examples/hello-i18n/
cd out/android && ./gradlew :app:installDebug
```

The Kotlin runtime (`I18n.kt`) lands at `app/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/`, and the manifest at `app/src/main/assets/i18n.manifest.json`. `MainActivity.onCreate` calls `I18n.init(this)` before rendering.

Locale follows `Locale.getDefault()` — change device language to French/Spanish to see the translations.

## What the app does

- **Name** — a text input bound to `name`.
- **Honorific** — three buttons over `female | male | other`. Drives the `select` arm of the sentence.
- **Guests** — `+` / `-` buttons. Drives the `plural` arm with `=0`, `one`, `other` cases.
- **Sentence** — single ICU template combining all three; translators get the full string and can re-order around grammar.
- **Ticket price** — fixed `1234`, locale-formatted as currency (USD in en-US, EUR in fr-FR / es-ES).
- **Party date** — a `time.date`, locale-formatted in long form.

## Source shape

Every translatable string is a `$"..."` literal written directly where it is
shown, such as `ui.text(value=$"Guests")`. A literal that reads state (`name`,
`gender`, `guests`) re-renders when that state changes, like any other
interpolation.
