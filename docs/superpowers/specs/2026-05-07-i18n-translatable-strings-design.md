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
- Type-checker validation (no-static-text warning, ICU placeholder type/case validation)
- IR-conversion lowering to `i18n.tr(template, args)` calls — no IR-level translatable concept
- New `map<K, V>` generic type
- Stdlib `i18n` package declaration and Go reference implementation (covers bubbletea/fyne/gtk4) — manifest **lookup** only, populated externally

Out of scope (deferred to #22 or later):
- `sngl extract` — manifest generation tooling
- `$identifier` source form (e.g. `$btn_submit`)
- `sngl i18n-sync` (source-to-ID rewrite)
- `sngl i18n-rename`
- Compile-time validation that keys exist in the manifest ("type-safe identifiers")
- Semantic key derivation (component name + node id + slug). Without extract there's no need; #21 uses the ICU template as the key.
- HTML and Android stdlib overrides (framework supports them; impls land later)

## Syntax

```sngl
let label = $"Login"                                          // plain
let welcome = $"Welcome back, {user.name}!"                   // single SNGL expr
let unread = $"You have {count, plural,
    one{message}
    other{messages}}"                                         // plural with cases
let formatted = $"Created on {date, date, short}"             // ICU formatter
let combined = $"{count} new {count, plural, one{msg} other{msgs}}"
```

The `$` prefix is the only discriminator — no lookahead needed. Inside `$"..."`, every `{...}` is an **i18n placeholder** with the fixed shape:

```
{ <expr> [ , <type> [ , <body> ] ] }
```

- **`<expr>`** — required. Any SNGL expression. Resolved against the local scope and bound by name (synthetic name if not a bare ident). For `{user.name}` this is the only field.
- **`<type>`** — optional, plain identifier. ICU type/style keyword: `plural`, `select`, `selectordinal`, `date`, `time`, `number`, `short`, `medium`, `long`, `full`, `currency`, `percent`, `decimal`, `scientific`, …
- **`<body>`** — optional, only when `<type>` is `plural`/`select`/`selectordinal`. A space-separated sequence of message cases:
  ```
  <selector> { <message> }
  ```
  Selector is an identifier (`one`, `two`, `few`, `many`, `other`, `zero`, plus `male`/`female`/etc. for `select`) or `=N` for an explicit numeric match. The message body is recursively an i18n message — literal text plus nested `{...}` placeholders.

The ICU spec uses whitespace between cases (`one{a} other{b}`); SNGL follows the spec — no commas between cases.

`$"""..."""` (triple-quoted) is supported with the same rules.

## Architecture

```
.sngl source
  │
  ▼ (lexer — emits I18N_STR_* tokens inside $"...")
i18n_str_start, i18n_str_resume, i18n_str_end, i18n_str_full, …
  │
  ▼ (parser → AST)
ast.I18nInterpExpr{Parts:[LiteralExpr|I18nPlaceholderExpr{Value, Type, Cases}]}
  │
  ▼ (checker)
warns no-static-text; type-checks Value; validates Type keyword + Cases against ICU shape
  │
  ▼ (IR conversion)
desugars to ir.Call{Func: i18n.tr, Args: [templateLit, mapLit{"name_N": expr_N, ...}]}
  │
  ▼ (codegen — unchanged)
emits ordinary call expression in the target language
```

The AST node carries the translatable marker through formatting (so `$"..."` round-trips). The IR-conversion step erases the concept by emitting a regular call. Codegen sees an ordinary call and emits whatever the target language's call expression is.

## Components

### Lexer (`internal/parser/lexer.go`)

The lexer emits **distinct token types** for `$"..."` strings rather than flagging existing string tokens. This lets the grammar stay disjoint between regular and translatable interpolations, with no conditional behavior on a `Translatable` flag:

| Regular | Translatable |
|---|---|
| `STR_START` | `I18N_STR_START` |
| `STR_RESUME` | `I18N_STR_RESUME` |
| `STR_END` | `I18N_STR_END` |
| `TRIPLE_START` | `I18N_TRIPLE_START` |
| `TRIPLE_END` | `I18N_TRIPLE_END` |
| `STR_FULL` | `I18N_STR_FULL` |
| `TRIPLE_FULL` | `I18N_TRIPLE_FULL` |

Plus three new tokens for case bodies: `I18N_CASE_FULL` (literal-only `{text}` body), `I18N_CASE_START` (`{text` opening of a body with nested placeholders), `I18N_CASE_END` (`text}` closing). These let the grammar treat case bodies symmetrically to outer i18n strings.

`interpFrame` gains an `i18n bool` so resume tokens know which token type to emit.

Inside an i18n string segment, the lexer scans literal text exactly as today; on `{` it pushes an interp frame and emits the segment-end token (`I18N_STR_START` / `I18N_STR_RESUME`). Inside the placeholder, normal SNGL tokens apply — the user-typed grammar `<expr>, <type>, <body>` parses with existing `Expr`, `comma`, `ident`, plus the new MsgFormatBody productions.

For message-case bodies (`one { ... }`), the lexer treats the inner `{` `}` as a fresh i18n string segment frame: literal text + nested placeholders, terminating at the matching `}` (which becomes `I18N_STR_END`-style for case bodies — see grammar below for the exact terminal).

The original `Token.Translatable` flag added in the unilateral implementation goes away.

### AST (`ast/expr.go`)

A separate top-level node for translatable interpolations (no `Translatable` flag on `InterpolationExpr` — drop that from the unilateral implementation). The placeholder shape mirrors the syntax: at most three fields, no slice.

```go
// I18nInterpExpr is a $"..." or $"""..."""  string. Parts alternate between
// *LiteralExpr (string segments) and *I18nPlaceholderExpr (one per {...}).
type I18nInterpExpr struct {
    Pos   Pos
    Parts []Expr        // alternation of *LiteralExpr | *I18nPlaceholderExpr
    Style StringStyle   // StyleDouble or StyleTriple
}

// I18nPlaceholderExpr is the {<expr>, <type>, <body>} form.
type I18nPlaceholderExpr struct {
    Pos   Pos
    Value Expr           // required
    Type  string         // "" if absent (no comma-ident)
    Cases []I18nCase     // nil if no body; else one or more cases
}

// One case in a plural/select/selectordinal body: <selector> { <message> }.
type I18nCase struct {
    Pos      Pos
    Selector string       // "one", "other", "=0", "male", …
    Body     []Expr       // alternation of *LiteralExpr | *I18nPlaceholderExpr
}
```

Bare `#` (ICU "current value" inside a plural case) becomes a `LiteralExpr` with a sentinel kind, expanded during template synthesis.

Existing `InterpolationExpr` is untouched — it stays for non-translatable strings only. The `Translatable bool` field is removed.

### Grammar (`internal/parser/sngl.ebnf` + regenerated `zparser.go`)

New productions for translatable strings, parallel to existing `InterpStr` / `TripleInterp`:

```
I18nInterpStr = i18n_str_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_str_end .
I18nTriple    = i18n_triple_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_triple_end .

# Also a non-interpolated form for $"text" / $"""text""" with no placeholders:
I18nFull      = i18n_str_full | i18n_triple_full .

# {<expr>, <type>, <body>}
I18nPlaceholder = Expr [ comma ident [ comma MsgFormatBody ] ] .

# Space-separated cases per ICU spec (no commas between).
MsgFormatBody = MsgCase { MsgCase } .
MsgCase       = Selector MsgBody .
Selector      = ident | eq int_lit .

# Case body: literal text + nested placeholders. Lexer emits the same shape
# as outer i18n strings — full when no placeholders, start/resume/end otherwise.
# The opening "{" and closing "}" of the case body are consumed by these tokens.
MsgBody       = i18n_case_full
              | i18n_case_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_case_end .
```

Wired into the existing `PrimaryExpr` alternative list alongside `InterpStr` / `TripleInterp` / `str_full` / `triple_full`. Requires regenerating `zparser.go`.

**Implementation note:** the `egg` tool is not currently in the repo's `go tool` registry. Either add it as a tool dependency or hand-edit `zparser.go` (the changes are mechanical and reviewable: a few new terminals, one new alternative in `PrimaryExpr`, the new productions above).

### AST builders (`internal/parser/build.go`)

`buildI18nInterpStr`, `buildI18nTriple`, `buildI18nFull`, `buildI18nPlaceholder`, `buildMsgFormatBody`, `buildMsgCase` — straightforward AST construction from the parse tree. Recursive case-body construction reuses `buildI18nInterpStr`'s loop logic for `MsgBody` since both produce the same `[]Expr` alternating shape.

No standalone "ICU body parser" — the SNGL grammar covers the entire syntax, including message cases.

### Checker (`internal/checker/expr.go`)

New `inferI18nInterp` dispatch for `*I18nInterpExpr`:
- Warn if no `*LiteralExpr` part has any non-whitespace text (the "no static text" warning, applied to the entire i18n string including all case bodies).
- For each `*I18nPlaceholderExpr`:
  - Type-check `Value` as a regular SNGL expression.
  - Validate `Type` against the known ICU keyword set; if `plural`/`selectordinal`, require `Value`'s inferred type to be numeric; if `select`, string-like; if `date`/`time`/`datetime`, a date/time type; etc.
  - For each `Cases[*]`: validate the selector matches the type (`one`/`other` for plural, `=N` numeric, free idents for `select`); recurse into the body.

### IR Conversion (`internal/checker/expr.go`)

Lower `*I18nInterpExpr` to a single `ir.Call` to `i18n.tr`:

1. **Synthesize the ICU template string** by walking `Parts` and recursing through cases, emitting strict ICU-spec form (whitespace between cases). Concretely:
   - `*LiteralExpr` parts: appended verbatim.
   - `*I18nPlaceholderExpr` parts: emit `{ <name> [ , <type> [ , <case1> { <body1> } <case2> { <body2> } … ] ] }` where `<name>` is either the bare ident name (when `Value` is `*IdentExpr`) or a synthetic `argN` (otherwise). Recurse on each case body.
2. **Build the args map** as an `ir.MapLit` of type `map<string, dyn>` mapping each name → its value expression: bare-ident value args bind by their name; complex-expr value args bind by their synthetic `argN`. Map literals are expressions like `{"name": <expr>, "argN": <expr>}`.
3. **Emit** `ir.Call{Func: <i18n.tr resolved>, Args: [ir.Literal{template}, mapLit]}`. The template string serves as both the lookup key into the manifest and the inline fallback when the manifest has no entry.

The IR has no translatable concept. Downstream phases (optimizer, codegen) see only an ordinary call.

### New Type: `map<K, V>`

The `select` family wants a key→value mapping with static types. Time to add the long-pending `map<K, V>` generic type. Minimal scope for #21:

- New `ir.TypeMap` kind with two `Elems` (key and value types).
- Type-expression parsing: `map<string, string>`, `map<PluralKey, string>` slot into the existing generic-type-args path (parallel to `list<T>`).
- **Key types**: any type with structural equality. Primitives (string, int, bool) and structs whose fields are themselves comparable. The checker rejects non-comparable key types (e.g. `map<func, string>`).
- Map literal syntax: Same `=` separator as struct literals; checker disambiguates struct vs map by expected type. If all keys are bare identifiers the parser produces a `StructExpr`; any non-identifier key (string literal, int, enum member, etc.) forces a `MapLit` at parse time. Anonymous literals with no expected type are a check error.
- Lookup: `m[k]` reuses `IndexExpr`, returning `V`. Missing-key returns the zero value (Go-map semantics). Tightening to `option<V>` lookup is deferred to a follow-up — out of scope here.
- Methods on `map.*`: `length`, `keys`, `values`, `contains(k)`, `get(k, default)`. Just enough for stdlib and i18n.
- No iteration syntax in `for` yet — defer to a later issue.
- **Codegen** by target language:
  - **Go**: `map[K]V` natively; struct keys work because Go composites of comparable fields are comparable.
  - **JS**: emit a `Map` (not a plain object) so non-string keys are honored. Map literals lower to `new Map([[k, v], ...])`. Equality for struct keys requires either a stable serialization strategy or wrapping struct keys in a primitive token — defer the choice to JS codegen impl, but pick one.
  - **Kotlin**: `Map<K, V>` with data-class structural equality; SNGL structs already lower to data classes so keys work.

This is a non-trivial expansion of #21 but the user explicitly scoped it in. If implementation cost balloons, candidates to defer to a follow-up:
- Map methods beyond `length` and indexing
- Tightening lookup return to `option<V>`

### Stdlib (`lib/i18n.sngl`)

```sngl
package i18n

// --- Translation entry points ---

// Translate `key` against the loaded manifest, formatting via ICU. `args`
// maps ICU placeholder names to values. Returns the translated string for
// the active locale, falling back to the manifest's `original` template,
// then to the key itself. Lowering target for $"..." literals.
func tr(key string, args map<string, dyn> = {}) string

// Format a literal ICU template. No manifest lookup.
func format(template string, args map<string, dyn> = {}) string

// --- Direct formatters ---

// Style values for numbers: "decimal" (default), "percent", "currency",
// "scientific".
func numberInt(n int, style string = "decimal") string
func numberFloat(n float, style string = "decimal") string

// Style values for date/time: "short", "medium" (default), "long", "full".
func date(d date, style string = "medium") string
func time(t time, style string = "medium") string
func datetime(dt dateTime, dateStyle string = "medium", timeStyle string = "medium") string

// --- Direct selectors ---

// Key for plural / selectordinal cases. When `exact` is true, `n` is the
// literal integer matched by the =N form. When `exact` is false, `n` is a
// sentinel index identifying a CLDR keyword (zero/one/two/few/many/other);
// callers reach these via the predeclared `i18n.zero` / `i18n.one` / … /
// `i18n.other` constants below rather than constructing the struct directly.
struct PluralKey { n int; exact bool }

// CLDR keyword constants. Sentinel `n` values are an implementation detail
// (matched by the runtime against CLDR rules). Treat these as opaque keys.
const zero  PluralKey = PluralKey{n: 0, exact: false}
const one   PluralKey = PluralKey{n: 1, exact: false}
const two   PluralKey = PluralKey{n: 2, exact: false}
const few   PluralKey = PluralKey{n: 3, exact: false}
const many  PluralKey = PluralKey{n: 4, exact: false}
const other PluralKey = PluralKey{n: 5, exact: false}

// Construct an exact-match key (=N form). Example: i18n.exactly(0) is the
// key matching exactly zero, distinct from i18n.zero (the CLDR "zero" rule).
func exactly(n int) PluralKey

// Cardinal plural selection. Example:
//     i18n.plural(n, {
//         i18n.exactly(0): "no files",
//         i18n.one:        "1 file",
//         i18n.other:      "{n} files",
//     })
func plural(count int, forms map<PluralKey, string>) string

// Ordinal plural selection (1st, 2nd, 3rd, …). Same key shape as plural.
func selectordinal(count int, forms map<PluralKey, string>) string

// Free-form value-based selection. Keys are arbitrary strings.
//     i18n.select(gender, {"male": "he", "female": "she", "other": "they"})
func select(value string, cases map<string, string>) string
```

Default Go implementation injected via `PkgSource` override:
- Loads `i18n.manifest.json` at process start (path configurable, default: working dir or embedded asset).
- Detects locale via `LC_ALL`, `LC_MESSAGES`, `LANG` (in order; falls back to `en`).
- All ICU formatting (template substitution, plural/select dispatch, number/date formatting) goes through `golang.org/x/text` (`message`, `feature/plural`, `number`, `currency`).
- Direct formatters (`number`, `date`, `plural`, etc.) are thin wrappers that build a one-shot ICU template internally and dispatch to the same formatter.

HTML and Android per-platform stdlib overrides not implemented in #21 (framework supports them, follow-up issue). For HTML the natural backend is `Intl.NumberFormat` / `Intl.DateTimeFormat` / `Intl.PluralRules` plus a small ICU MessageFormat shim; for Android, `android.icu.text.MessageFormat` and friends.

**Locale management** is deferred: there's no `setLocale`/`locale` getter in #21. Each platform's stdlib impl reads from its conventional source (env vars on Go, `navigator.language` on HTML, `Locale.getDefault()` on Android). Per-request or test-time locale override can be added as a follow-up without breaking the API surface above.

### Manifest schema (read-only in #21)

The runtime loads `i18n.manifest.json` at startup if present. Keys are ICU template strings (matching what the lowering passes to `i18n.tr`). Schema:

```json
{
  "Submit Query": {
    "translations": {
      "es": "Enviar consulta",
      "fr": "Soumettre la requête"
    }
  },
  "You have {count, plural, one{message} other{messages}}": {
    "translations": {
      "es": "Tienes {count, plural, one{mensaje} other{mensajes}}"
    }
  }
}
```

#21 only **loads and looks up** this file — generation is deferred to #22's `i18n-sync` (or to hand-authoring). When the manifest is missing or has no entry for a key, the runtime falls back to the inlined template that lowering passes as the first arg to `i18n.tr`. Apps without translations work transparently.

The schema is intentionally minimal. #22 may extend it (e.g. add `original`, `context`, switch to stable IDs as keys); the runtime can stay forward-compatible by ignoring unknown fields and using the key directly when no schema-rich entry is found.

## What changes from the current implementation

The unilateral implementation in commit history covers parser flag + checker warning + a multi-format extractor. It needs to be extended/revised:

| Component | Current state | Needs |
|---|---|---|
| `Token.Translatable` flag | ✅ added | **remove** — replaced by distinct `I18N_*` token types |
| `InterpolationExpr.Translatable` flag | ✅ added | **remove** — replaced by separate `I18nInterpExpr` node |
| Lexer `$"..."` recognition | ✅ basic case | rewrite: emit `I18N_STR_*` tokens with i18n-aware frame state including case-body sub-frames |
| Formatter `$` round-trip | ✅ basic case | rewrite to format `I18nInterpExpr`/`I18nPlaceholderExpr`/`I18nCase` |
| Checker no-static-text warning | ✅ works | port to `inferI18nInterp`; add placeholder type/case validation against ICU keyword set |
| `ast.I18nInterpExpr`, `I18nPlaceholderExpr`, `I18nCase` | ❌ missing | add |
| Grammar | ❌ unchanged | new terminals (i18n string boundaries); new productions `I18nInterpStr`, `I18nTriple`, `I18nFull`, `I18nPlaceholder`, `MsgFormatBody`, `MsgCase`, `Selector`; new alternative in `PrimaryExpr`; regenerate `zparser.go` |
| AST builders | ❌ missing | add `buildI18n*` in `internal/parser/build.go` |
| IR conversion to `ir.Call` | ❌ still emits `Binary +` chain for translatable | new `inferI18nInterp` emitting `ir.Call` to `i18n.tr` with synthesized ICU template as first arg |
| `map<K, V>` generic type | ❌ missing | add `ir.TypeMap`, type-expr parsing, map literal `{"k": v}` syntax (struct keys supported), `IndexExpr` lowering for maps, methods (`length`, `keys`, `values`, `contains`, `get`), per-language codegen |
| Stdlib `i18n` package | ❌ missing | add `lib/i18n.sngl` declaring `tr`, `format`, `numberInt`, `numberFloat`, `date`, `time`, `datetime`, `plural`, `selectordinal`, `select`, `PluralKey` struct, `zero`/`one`/`two`/`few`/`many`/`other` constants, `exactly(n)` constructor |
| Go stdlib impl via PkgSource | ❌ missing | add (uses `x/text` `message`, `feature/plural`, `number`, `currency`); manifest loader reads `i18n.manifest.json` if present |
| `sngl extract` | ✅ multi-format | **delete** — extraction is out of scope for #21 |

## Testing

- **Lexer**: golden tokens for `$"x"`, `$"x{name}"`, `$"x{count, plural, one{a} other{b}}"`, `$"""multi-line {n}"""`, mixed translatable + non.
- **Parser/format round-trip**: extend `format_test.go` cases.
- **Grammar/parser unit tests**: round-trip for plain `$"..."`, single-arg placeholder, two-arg formatter (`{date, date, short}`), three-arg plural/select/selectordinal with multiple cases including `=N` selectors, nested placeholders inside case bodies, triple-quoted form.
- **Checker**: no-static-text warning, undefined ICU name → error, valid ICU with in-scope names → no diag.
- **IR conversion**: assert `*I18nInterpExpr` produces an `ir.Call` to `i18n.tr` with the synthesized ICU template as the first arg and the correct map-typed args bundle.
- **Manifest loading** (Go runtime): runtime resolves manifest hits, manifest misses (falls back to inlined template), and missing-manifest case (uses inlined template directly).
- **`map<K, V>` unit tests**: type-expr parsing, literal construction (string keys, int keys, struct keys), indexing, methods, rejection of non-comparable key types, codegen round-trip per language including struct-keyed maps with `i18n.PluralKey`.
- **Stdlib runtime** (Go): unit tests for each entry point — `tr` (manifest hit + miss + plural template across locales), `format` (literal template), `numberInt`/`numberFloat`/`date`/`time`/`datetime` (style variants in en + at least one non-en locale), `plural`/`select`/`selectordinal` (form selection across locales).

## Open questions

None blocking. Documented for follow-up:
- HTML and Android stdlib overrides — follow-up issues.
- Manifest path / discovery convention — default to `i18n.manifest.json` at project root for now; revisit if multi-package projects need per-package manifests.
- Manifest population — until #22's `i18n-sync` lands, manifests are hand-authored. Without one, the runtime uses the inlined ICU template as the displayed string (i.e. apps without translations Just Work).
- Compile-time key validation — out of scope (#22's "type-safe identifiers" objective).
