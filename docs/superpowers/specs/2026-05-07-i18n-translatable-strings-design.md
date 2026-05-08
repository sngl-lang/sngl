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
let label = $"Login"                                          // plain
let welcome = $"Welcome back, {user.name}!"                   // single SNGL expr
let unread = $"You have {count, plural,
    one{message},
    other{messages}}"                                         // plural with cases
let formatted = $"Created on {date, date, short}"             // ICU formatter
let combined = $"{count} new {count, plural, one{msg}, other{msgs}}"
```

The `$` prefix is the only discriminator — no lookahead needed. Inside `$"..."`, every `{...}` is an **i18n placeholder** (not a generic SNGL interpolation). A placeholder body is a comma-separated list of items. Each item is one of:

- **SNGL expression** — `count`, `user.name`, `now() + 1d`. Resolved against the local scope and bound by name (synthetic name if not a bare ident).
- **Identifier** — bare names in non-leading positions are ICU type/style keywords: `plural`, `select`, `selectordinal`, `date`, `time`, `number`, `short`, `medium`, `long`, `full`, `currency`, `percent`, etc.
- **Message expression** — `<selector>{<message>}` where selector is an identifier (`one`, `other`, `male`, …) or `=N` for explicit numeric match. The message body is itself an i18n message: literal text plus nested `{...}` placeholders.

Note: SNGL deviates from strict ICU by requiring commas **between** message-expression cases (where strict ICU uses whitespace). The runtime emits whitespace-separated form when handing the template to native ICU libraries.

`$"""..."""` (triple-quoted) is supported with the same rules.

## Architecture

```
.sngl source
  │
  ▼ (lexer — ICU-aware inside $"..." frames)
tokens: STR_START with Translatable=true, ICU_PLACEHOLDER tokens for ICU bodies, normal expr tokens otherwise
  │
  ▼ (parser → AST)
ast.InterpolationExpr{Translatable:true, Parts:[LiteralExpr|I18nPlaceholderExpr]}
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
- New token type `ICU_PLACEHOLDER` (sentinel byte to be allocated; literal is the verbatim placeholder body without the outer braces).
- When `{` is encountered while scanning translatable string content, the lexer slurps to the matching `}` (counting nested braces from message expressions) and emits a single `ICU_PLACEHOLDER` token. No SNGL expression mode is entered. The `$` prefix is the only signal needed — no lookahead, no per-`{` decision.
- For non-translatable strings, `{` continues to work as today (push interp frame, parse SNGL expression).

### AST (`ast/expr.go`)

- `InterpolationExpr.Translatable bool` (already added).
- New nodes for the placeholder body items:
  ```go
  // I18nPlaceholderExpr is an i18n placeholder body: a comma-separated list
  // of items written inside {...} in a $"..." string.
  type I18nPlaceholderExpr struct {
      Pos  Pos
      Args []I18nArg  // alternation: I18nValueArg | I18nMessageArg
  }

  // Covers both arbitrary SNGL exprs (e.g. count, user.name, now()+1d) AND
  // bare-ident ICU keywords (plural, short, currency, …). The checker
  // decides which based on position — no AST-level distinction needed since
  // a bare keyword is just an IdentExpr.
  type I18nValueArg struct { Pos Pos; Value Expr }

  // <selector>{<body>} where selector is an ident ("one") or "=N" form.
  type I18nMessageArg struct {
      Pos      Pos
      Selector string
      Body     []Expr  // alternation: *LiteralExpr | *I18nPlaceholderExpr
  }
  ```
- The `Body` of an `I18nMessageArg` is recursive: literal text plus nested placeholders. Bare `#` (ICU "current value") becomes a `LiteralExpr` with a sentinel kind, expanded during template synthesis.

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

### Placeholder Body Parser (`internal/parser/i18n.go`)

A small recursive parser invoked at AST-build time on each `ICU_PLACEHOLDER` token literal. Produces an `I18nPlaceholderExpr` from the body text.

Grammar:
```
placeholder = arg ("," arg)*
arg         = msg_expr
            | sngl_expr                           // includes bare idents
msg_expr    = (ident | "=" int) "{" message "}"
message     = (literal_text | "#" | "{" placeholder "}")*
```

Algorithm:
1. Tokenize the body, splitting on top-level commas (brace-depth aware).
2. For each item, peek for the `<sel>{...}` msg-expr shape — bare identifier or `=N` followed immediately by `{`. If matched, recurse into the message body. Otherwise feed the item text back into the SNGL expression parser via a re-entry point (`parser.ParseExpr(string) (ast.Expr, error)`) — added as a new public-internal helper. This means SNGL exprs and bare idents share the existing expression machinery; no duplicate parser logic.
3. Recursively, message bodies allow literal text + nested placeholders (same lexer rules as `$"..."` content).

Single file, <300 LOC, no external dep. Errors reported with byte offsets so the checker can produce diagnostics anchored in the source.

### Checker (`internal/checker/expr.go`)

`inferInterpolation` extends:
- Existing: warn if `Translatable` and no literal parts.
- New: for each `I18nPlaceholderExpr`, type-check each `I18nValueArg.Value` as a regular SNGL expression. Validate that the leading argument's type matches the ICU type keyword that follows (e.g. `plural`/`selectordinal` require numeric; `select` requires string-like). Walk message bodies recursively.

### IR Conversion (`internal/checker/expr.go` `inferInterpolation`)

When `x.Translatable`, replace the current `Binary +` chain with a single `ir.Call`:

1. **Derive key** (see Key Derivation below).
2. **Synthesize the ICU template string** by walking parts and emitting an ICU-spec form (whitespace between message-expr cases) regardless of the SNGL source's comma-separated form. Concretely:
   - `LiteralExpr` parts: appended verbatim.
   - `I18nPlaceholderExpr` parts: emit `{`, then for each `I18nArg`, comma-separated:
     - `I18nValueArg{Value: *IdentExpr}`: emit the ident name (covers both bound vars and ICU keywords like `plural` / `short`).
     - `I18nValueArg{Value: complexExpr}`: emit a synthetic name `argN` (and bind the expression in step 3).
     - `I18nMessageArg`: emit `<selector>{<recursively-built-body>}` with whitespace separators between sibling message args (per ICU spec).
3. **Build the args struct** as an `ir.StructLit` mapping each name → its value expression: real-ident value args bind by their name; complex-expr value args bind by their synthetic `argN`.
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
| Lexer `$"..."` recognition | ✅ basic case | extend: in translatable strings, slurp `{...}` as opaque `ICU_PLACEHOLDER` token (no lookahead); propagate `translatable` through `interpFrame` |
| Formatter `$` round-trip | ✅ works | extend to format `I18nPlaceholderExpr` |
| Checker no-static-text warning | ✅ works | keep, add placeholder validation (type-check value args, validate ICU keyword shape) |
| `ast.I18nPlaceholderExpr` + arg variants | ❌ missing | add (`I18nValueArg`, `I18nMessageArg`) |
| Grammar | ❌ unchanged | one new terminal `icu_placeholder`; one new alternative in `InterpPart` for both `InterpStr` and `TripleInterp`; regenerate `zparser.go` |
| Placeholder body parser | ❌ missing | add `internal/parser/i18n.go` with `parser.ParseExpr` re-entry helper |
| IR conversion to `ir.Call` | ❌ still emits `Binary +` chain | rewrite to emit `ir.Call` to `i18n.tr` |
| Key derivation | ❌ uses content hash via `{0}` numbering | replace with semantic-key derivation |
| Stdlib `i18n` package | ❌ missing | add `lib/i18n.sngl` declaring `tr`, `format`, `number`, `date`, `time`, `datetime`, `plural`, `select`, `selectordinal` |
| Go stdlib impl via PkgSource | ❌ missing | add (uses `x/text` `message`, `feature/plural`, `number`, `currency`) |
| `sngl extract` | ✅ multi-format | replace with manifest-only writer + merge logic |

## Testing

- **Lexer**: golden tokens for `$"x"`, `$"x{name}"`, `$"x{count, plural, one{a} other{b}}"`, `$"""multi-line {n}"""`, mixed translatable + non.
- **Parser/format round-trip**: extend `format_test.go` cases.
- **Placeholder body parser unit tests**: valid/invalid forms (plural, select, selectordinal, date/time/number formatters), nested message bodies, `=N` selectors, recursion into messages with nested placeholders, error positions.
- **Checker**: no-static-text warning, undefined ICU name → error, valid ICU with in-scope names → no diag.
- **IR conversion**: assert `Translatable` interpolation produces an `ir.Call` to `i18n.tr` with correct key and struct args.
- **Extract command** (txtar): runs against multi-file fixture, asserts manifest content; second run preserves a hand-added translation; orphaned keys flagged.
- **Stdlib runtime** (Go): unit tests for each entry point — `tr` (manifest hit + miss + plural template across locales), `format` (literal template), `number`/`date`/`time`/`datetime` (style variants in en + at least one non-en locale), `plural`/`select`/`selectordinal` (form selection).

## Open questions

None blocking. Documented for follow-up:
- HTML and Android stdlib overrides — schedule under #22 or a separate issue.
- Manifest path / discovery convention — default to `i18n.manifest.json` at project root for now; revisit if multi-package projects need per-package manifests.
- Compile-time key validation — out of scope (#22's "type-safe identifiers" objective).
