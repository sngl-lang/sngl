# i18n Translatable Strings — Design

**Issue:** [#21 — i18n](https://git.duckfam.us/jonathan/sngl/-/issues/21)
**Follows:** issue #22 (i18n-sync / i18n-rename / `$identifier` form) builds on this
**Date:** 2026-05-07

## Problem

SNGL targets multiple GUI platforms but has no native way to mark UI text as translatable. Developers currently bridge SNGL strings to platform i18n systems (Android `strings.xml`, gettext, etc.) by hand. We want a first-class translatable-string syntax that:

- Marks UI text for extraction (`$"Login"`)
- Supports interpolation with full ICU MessageFormat semantics (plurals, select, formatters)
- Lowers to a stdlib call that defers to platform-native ICU libraries
- Produces a manifest file translators can fill in

## Scope

In scope for #21:
- Parser and AST support for `$"..."` and `$"""..."""`, including ICU placeholder syntax
- Type-checker validation (no-static-text warning, ICU placeholder name resolution)
- IR-conversion lowering to `i18n.tr(key, args)` calls — no IR-level translatable concept
- `sngl extract` command that produces `i18n.manifest.json`
- Stdlib `i18n.tr` declaration and Go reference implementation (covers bubbletea/fyne/gtk4)

Out of scope (deferred to #22):
- `$identifier` source form (e.g. `$btn_submit`)
- `sngl i18n-sync` (source-to-ID rewrite)
- `sngl i18n-rename`
- Compile-time validation that keys exist in the manifest ("type-safe identifiers")
- HTML and Android stdlib overrides (framework supports them; impls land later)

## Syntax

```sngl
let label = $"Login"                                  // plain
let welcome = $"Welcome back, {user.name}!"           // SNGL expr placeholder
let unread = $"You have {count} new {count, plural,
    one{message}
    other{messages}}"                                 // ICU plural
let formatted = $"Created on {date, date, short}"     // ICU formatter
```

Inside `$"..."`, `{...}` is one of:
- A bare identifier or SNGL expression (existing interpolation behavior, preserved)
- An ICU placeholder: detected by lookahead — if the content begins with `<ident>,` (identifier followed by comma), it's parsed as opaque ICU until balanced `}`

Disambiguation rule is the comma after the first identifier — unambiguous because SNGL expressions never have a top-level comma in this position.

`$"""..."""` (triple-quoted) is supported with the same rules.

## Architecture

```
.sngl source
  │
  ▼ (lexer — ICU-aware inside $"..." frames)
tokens: STR_START with Translatable=true, ICU_PLACEHOLDER tokens for ICU bodies, normal expr tokens otherwise
  │
  ▼ (parser → AST)
ast.InterpolationExpr{Translatable:true, Parts:[LiteralExpr|Expr|ICUPlaceholderExpr]}
  │
  ▼ (checker)
warns no-static-text; validates ICU placeholder names resolve in scope; validates ICU template parses
  │
  ▼ (IR conversion)
desugars to ir.Call{Func: i18n.tr, Args: [keyLit, structLit{name_N: expr_N, ...}]}
  │
  ▼ (codegen — unchanged)
emits ordinary call expression in the target language
```

The AST node carries the translatable marker through formatting (so `$"..."` round-trips). The IR-conversion step erases the concept by emitting a regular call. Codegen sees an ordinary call and emits whatever the target language's call expression is.

## Components

### Lexer (`internal/parser/lexer.go`)

- `Token.Translatable bool` — set on `STR_FULL`, `TRIPLE_FULL`, `STR_START`, `TRIPLE_START` when the literal opens with `$`.
- `interpFrame.translatable bool` — propagates the flag through resume tokens so the lexer knows it's still inside a translatable string.
- New token type `ICU_PLACEHOLDER` (sentinel byte to be allocated; literal is the verbatim ICU body without the outer braces).
- New routine `scanInterpolationOpening`: when `{` is encountered inside a translatable string content, peek for `<ident> WS* ,`. If matched, slurp until balanced `}` and emit `ICU_PLACEHOLDER`. Otherwise emit `LBRACE` and let the existing expression scanner take over.

### AST (`ast/expr.go`)

- `InterpolationExpr.Translatable bool` (already added).
- New `ICUPlaceholderExpr` node:
  ```go
  type ICUPlaceholderExpr struct {
      Pos   Pos
      Body  string   // verbatim text between { and }, e.g. "count, plural, one{...} other{...}"
      Names []string // identifier names referenced (extracted by minimal ICU parser)
  }
  ```

### Grammar (`internal/parser/sngl.ebnf` + regenerated `zparser.go`)

The existing `InterpStr` grammar:
```
InterpStr = str_start Expr { str_resume Expr } str_end .
```
extends to allow `ICU_PLACEHOLDER` where `Expr` appears:
```
InterpStr = str_start InterpPart { str_resume InterpPart } str_end .
InterpPart = Expr | icu_placeholder .
```

Same for `TripleInterp`. Requires regenerating `zparser.go` via the `egg` tool.

**Implementation note:** the `egg` tool is not currently in the repo's `go tool` registry. Either add it as a tool dependency or hand-edit `zparser.go` (reviewable since the change is mechanical: one new alternative in two productions).

### Minimal ICU Parser (`internal/parser/icu.go`)

A small recursive parser. Just enough to:
- Validate the body parses as ICU MessageFormat (placeholder, plural, select, formatter)
- Extract the set of identifier names referenced (top-level and nested)
- Surface syntax errors with positions

Grammar handled (subset of full ICU):
```
placeholder = ident                                    // {name}
            | ident "," type                           // {date, date}
            | ident "," type "," style                 // {date, date, short}
            | ident "," ("plural"|"selectordinal"|"select") "," cases
cases       = case+
case        = (selector | "=" int) "{" message "}"
selector    = "zero" | "one" | "two" | "few" | "many" | "other" | ident
message     = (literal | "#" | "{" placeholder "}")*
```

Single file, <300 LOC, no external dep. Errors reported with byte offsets so the checker can produce diagnostics.

### Checker (`internal/checker/expr.go`)

`inferInterpolation` extends:
- Existing: warn if `Translatable` and no literal parts.
- New: for each `ICUPlaceholderExpr`, parse the body; if invalid, emit error diagnostic. For each extracted name, look up in the local scope and validate as a SNGL identifier with appropriate type (string for plain placeholders, int for plural/select-ordinal selectors, anything for `select`).

### IR Conversion (`internal/checker/expr.go` `inferInterpolation`)

When `x.Translatable`, replace the current `Binary +` chain with a single `ir.Call`:

1. **Derive key** (see Key Derivation below).
2. **Build ICU template** (`template`) by concatenating parts:
   - `LiteralExpr` parts: appended verbatim
   - SNGL expression parts (e.g. `{user.name}`): replaced with `{argN}` (synthetic name); the value expression bound to that name in the args struct
   - `ICUPlaceholderExpr` parts: appended as `{` + body + `}`; their names contributed directly to the args struct
3. **Build args struct** as an `ir.StructLit` mapping each name → its value expression. For ICU placeholder names, the value is an `ir.Ident` resolved against the local scope. For synthetic names from SNGL exprs, the value is the IR'd expression.
4. **Emit** `ir.Call{Func: <i18n.tr resolved>, Args: [ir.Literal{key}, structLit]}`.

The IR has no translatable concept. Downstream phases (optimizer, codegen) see only an ordinary call.

### Key Derivation

Walked during IR conversion since the AST visitor already tracks enclosing scopes:

1. Collect from the path to the literal: the nearest `ComponentDecl.Name` and the nearest `VisualNode.ID` (if set).
2. If both present: `<component>.<node_id>` (e.g. `login.submit`).
3. If only component: `<component>.<slug-of-template>` where slug is lowercase-alphanum-underscore truncated to ~30 chars.
4. If neither: just the slug.
5. On collision within a single extract pass, append `_2`, `_3`, ….

Examples:
- `component login { Button #submit (text = $"Submit Query") }` → `login.submit`
- `component login { Text(text = $"Hello, {user.name}!") }` → `login.hello_user_name`
- top-level `var x = $"Welcome"` → `welcome`

### Stdlib (`lib/i18n.sngl`)

```sngl
package i18n

// --- Translation entry points ---

// Translate `key` against the loaded manifest, formatting via ICU. `args` is
// a struct literal binding ICU placeholder names to values. Returns the
// translated string for the active locale, falling back to the manifest's
// `original` template, then to the key itself. This is the lowering target
// for $"..." literals.
func tr(key string, args = {}) -> string

// Format a literal ICU template with `args`. No manifest lookup — useful for
// one-off locale-aware formatting where translation isn't needed.
func format(template string, args = {}) -> string

// --- Direct formatters (for code paths that don't need ICU template syntax) ---

// `style`: "decimal" (default), "percent", "currency", "scientific".
func number(n any, style = "decimal") -> string

// `style`: "short", "medium" (default), "long", "full".
func date(d any, style = "medium") -> string
func time(t any, style = "medium") -> string
func datetime(dt any, dateStyle = "medium", timeStyle = "medium") -> string

// --- Direct selectors (for ICU-style branching outside a $"..." literal) ---

// `forms` is a struct literal with selector keys. Plural forms accept CLDR
// keywords (zero/one/two/few/many/other) and explicit `=N` matches.
// Example: i18n.plural(n, {one: "1 file", other: "{n} files"})
func plural(count int, forms = {}) -> string

// `cases` is a struct literal mapping selector values to messages.
// Example: i18n.select(gender, {male: "he", female: "she", other: "they"})
func select(value string, cases = {}) -> string

// Ordinal plural rules (1st, 2nd, 3rd, …).
func selectordinal(count int, forms = {}) -> string
```

Default Go implementation injected via `PkgSource` override:
- Loads `i18n.manifest.json` at process start (path configurable, default: working dir or embedded asset).
- Detects locale via `LC_ALL`, `LC_MESSAGES`, `LANG` (in order; falls back to `en`).
- All ICU formatting (template substitution, plural/select dispatch, number/date formatting) goes through `golang.org/x/text` (`message`, `feature/plural`, `number`, `currency`).
- Direct formatters (`number`, `date`, `plural`, etc.) are thin wrappers that build a one-shot ICU template internally and dispatch to the same formatter.

HTML and Android per-platform stdlib overrides not implemented in #21 (framework supports them, follow-up issue). For HTML the natural backend is `Intl.NumberFormat` / `Intl.DateTimeFormat` / `Intl.PluralRules` plus a small ICU MessageFormat shim; for Android, `android.icu.text.MessageFormat` and friends.

**Locale management** is deferred: there's no `setLocale`/`locale` getter in #21. Each platform's stdlib impl reads from its conventional source (env vars on Go, `navigator.language` on HTML, `Locale.getDefault()` on Android). Per-request or test-time locale override can be added as a follow-up without breaking the API surface above.

### `sngl extract` Command (`cmd/sngl/extract.go`)

Behavior change from current implementation:
- Walks AST, derives keys via the rule above, computes the ICU template per literal.
- Reads existing `i18n.manifest.json` at the project root if present; merges:
  - **New keys** added with empty `translations: {}`.
  - **Existing keys** preserve their `translations` map; `original`/`template`/`context` updated from current source.
  - **Orphaned keys** (in manifest but not in source) flagged `"orphaned": true` (kept, not deleted).
- Writes to `i18n.manifest.json` (or `--out` override).

Manifest schema:

```json
{
  "login.submit": {
    "original": "Submit Query",
    "template": "Submit Query",
    "context": "src/main.sngl:42",
    "translations": {
      "es": "Enviar consulta",
      "fr": "Soumettre la requête"
    }
  },
  "inbox.unread": {
    "original": "You have {count} new {count, plural, one{message} other{messages}}",
    "template": "You have {count} new {count, plural, one{message} other{messages}}",
    "context": "src/inbox.sngl:17",
    "translations": {}
  }
}
```

Drop the `--out-format json|pot|arb` flag from the current implementation. Manifest is the only format.

## What changes from the current implementation

The unilateral implementation in commit history covers parser flag + checker warning + a multi-format extractor. It needs to be extended/revised:

| Component | Current state | Needs |
|---|---|---|
| `Token.Translatable` | ✅ added | keep |
| `InterpolationExpr.Translatable` | ✅ added | keep |
| Lexer `$"..."` recognition | ✅ basic case | extend with ICU-aware `{` handling and `interpFrame.translatable` propagation |
| Formatter `$` round-trip | ✅ works | keep |
| Checker no-static-text warning | ✅ works | keep, add ICU placeholder validation |
| `ast.ICUPlaceholderExpr` | ❌ missing | add |
| Grammar | ❌ unchanged | extend `InterpPart`; regenerate zparser |
| Minimal ICU parser | ❌ missing | add |
| IR conversion to `ir.Call` | ❌ still emits `Binary +` chain | rewrite to emit `ir.Call` to `i18n.tr` |
| Key derivation | ❌ uses content hash via `{0}` numbering | replace with semantic-key derivation |
| Stdlib `i18n` package | ❌ missing | add `lib/i18n.sngl` declaring `tr`, `format`, `number`, `date`, `time`, `datetime`, `plural`, `select`, `selectordinal` |
| Go stdlib impl via PkgSource | ❌ missing | add (uses `x/text` `message`, `feature/plural`, `number`, `currency`) |
| `sngl extract` | ✅ multi-format | replace with manifest-only writer + merge logic |

## Testing

- **Lexer**: golden tokens for `$"x"`, `$"x{name}"`, `$"x{count, plural, one{a} other{b}}"`, `$"""multi-line {n}"""`, mixed translatable + non.
- **Parser/format round-trip**: extend `format_test.go` cases.
- **ICU parser unit tests**: valid/invalid forms, name extraction.
- **Checker**: no-static-text warning, undefined ICU name → error, valid ICU with in-scope names → no diag.
- **IR conversion**: assert `Translatable` interpolation produces an `ir.Call` to `i18n.tr` with correct key and struct args.
- **Extract command** (txtar): runs against multi-file fixture, asserts manifest content; second run preserves a hand-added translation; orphaned keys flagged.
- **Stdlib runtime** (Go): unit tests for each entry point — `tr` (manifest hit + miss + plural template across locales), `format` (literal template), `number`/`date`/`time`/`datetime` (style variants in en + at least one non-en locale), `plural`/`select`/`selectordinal` (form selection).

## Open questions

None blocking. Documented for follow-up:
- HTML and Android stdlib overrides — schedule under #22 or a separate issue.
- Manifest path / discovery convention — default to `i18n.manifest.json` at project root for now; revisit if multi-package projects need per-package manifests.
- Compile-time key validation — out of scope (#22's "type-safe identifiers" objective).
