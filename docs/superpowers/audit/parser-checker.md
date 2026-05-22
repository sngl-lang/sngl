# Parser & Checker Audit — 2026-05-22

Audit of parser and type-checker for inconsistencies, bugs, and UX gaps. Findings ranked: BUG > INCONSISTENCY > UX. Reproduce repros from `/home/jonathan/src/git.duckfam.us/jonathan/sngl` with `go run ./cmd/sngl check <file>`.

---

## BUG

### 1. `->` arrow in func decls causes parser panic with garbage error

**Files:** `internal/parser/lexer.go:272`, `internal/parser/token.go:43`, `internal/parser/sngl.ebnf:30`
The lexer still emits an `ARROW` token for `->`, but the grammar (sngl.ebnf) has zero rules referencing it. Result: any use of the legacy `func foo() -> Type {}` syntax — common in stale docs and likely user muscle memory — produces a parser panic that the recover catches and reports as:

```
arrow_test.sngl: runtime error: index out of range [9] with length 9
```

Per CLAUDE.md the `->` form is being phased out; the lexer should either delete the token entirely (so `->` lexes as `-` followed by `>`) or recognise it and emit a targeted diagnostic ("`-> Type` is not valid; drop the arrow or use `=> Expr`"). The current "index out of range" panic message reaches end users.
**Severity:** BUG (panic + useless message on a known stale syntax).
Repro: `func add(x int, y int) -> int { return x + y }`

### 2. Mutually recursive struct types fail in both orderings

**Files:** `internal/checker/checker.go:264-265`, `internal/checker/resolve.go:255-289`
`registerStruct` calls `buildStructDef` which calls `resolveTypeRequired` on every field type *immediately* during pass1's source-order walk. So:

```
struct A { b B }
struct B { a A }
```

fails with `unknown type "B"`. Swapping the order fails with `unknown type "A"`. Pass1 must first register name-only shells for all `StructDef`/`EnumDef`/`UnitDef`/`Component`, then field-resolve in a second sub-pass. The fixture `testdata/checker_mutual_recursion.sngl` only exercises function mutual recursion, not types — coverage gap.
**Severity:** BUG (no workaround for mutually referencing types).

### 3. List spread (`[...xs, 3]`) typechecks the spread as an element

**Files:** `internal/checker/expr.go:1462-1480`
`inferListLit` blindly calls `checkExprExpecting(e, elemExpected)` on every element; spread `*ast.SpreadExpr` (parsed correctly per `ast/expr.go:243`) falls through and is typed as `list<int>`, then the literal infers `list<list<int>>`:

```
var a list<int> = [1, 2]
var b list<int> = [...a, 3]   // error: cannot initialize list<int> with list<list<int>>
```

The function must special-case `*ast.SpreadExpr`, validate that the operand is a `list<T>` matching the element type, and flatten in IR. Same code path likely affects function-call spread (`f(...xs)`) — that case also fails (`spread_func.sngl` → "cannot pass list<int> as int").
**Severity:** BUG (no working spread syntax in lists or calls).

### 4. Field/method access on `string` (and likely other primitives) for unknown name silently returns `dyn`

**Files:** `internal/checker/expr.go` (SelectExpr branch)

```
var x = "hi"
var y = x.foo   // checks OK, y typed `dyn`
```

The fixture `testdata/error_selector_unknown_method_string.sngl` documents the method-form bug; the field-form has no fixture. Both should produce `no field "foo" on type string`. Compounded by the silent `dyn` infection — downstream uses don't error either.
**Severity:** BUG (loss of type safety).

### 5. Empty map literal `{}` against non-string-keyed map type emits wrong-shape error

**Files:** `internal/checker/expr.go:1419-1428`

```
var m map<float, int> = {}   // "ident-keyed literal does not match map<float,...>"
```

`reinterpretStructAsMap` runs on every anon struct lit against a map type, even when there are zero fields. It should short-circuit when `len(x.Fields) == 0` and return an empty `MapLitIR` typed at the expected map type, mirroring `inferMapLit`'s empty path at line 1493.
**Severity:** BUG (correct empty map syntax rejected with misleading message).

### 6. Bare enum-member name in `const` initializer not resolved against expected enum type

**Files:** `internal/checker/checker.go:572-624` (`registerConsts`, `nonConstRef`)

```
enum Color { red, green, blue }
const c Color = red   // error: const initializer forward-references "red"
```

Equivalent `var c Color = red` works. The cause: `nonConstRef` walks the AST before any expected-type / enum-member resolution; it doesn't know `red` is `Color.red`. Either make `nonConstRef` aware of enum member shorthand against `spec.Type`, or move the const-initializer-purity check to run on the IR after `checkExprExpecting` has performed enum-bare-name resolution.
**Severity:** BUG (asymmetry between var and const init, blocking idiomatic constant declarations).

### 7. Multiple parser productions panic on benign inputs

**Files:** `internal/parser/build.go`, `internal/parser/parse.go:42-48`
Beyond the `->` case, at least one other input panics:

```
(c == 0 ? a : b) = 5      // "parser panic: runtime error: index out of range [117] with length 117"
```

The deferred `recover()` in `Parse` catches the panic but the user-facing message ("index out of range [N] with length N") is useless. Either fix the builder's iterator bounds-checking (likely in `build.go` where `tokenAt`/iterator advance happens after a parse error tree has gaps) or wrap the recover with "internal parser bug, please report" plus dump the original source span.
**Severity:** BUG (compiler crash messages reach users).

### 8. Integer literal overflow is not detected anywhere

**Files:** `internal/parser/lexer.go`, `internal/checker/expr.go`

```
var x int = 99999999999999999999
```

typechecks `ok`. Fixture `testdata/error_integer_overflow.sngl` claims this should be `ERROR(parse) "invalid integer literal"`, but neither the parser nor the checker performs `strconv.ParseInt` on `INT` literals. The fixture is also never enforced (see #15).
**Severity:** BUG (silent overflow into IR; depending on codegen path may produce wrong runtime values).

### 9. Duplicate struct field and function parameter names accepted silently

**Files:** `internal/checker/resolve.go:255-289` (`buildStructDef`), `internal/checker/resolve.go` (param resolution)

```
struct P { x int; y int; x bool }  // ok
func f(x int, x int) => x          // ok
```

Both should be hard errors. Currently the second `x` field/param silently overrides or is appended, leaving an inconsistent IR.
**Severity:** BUG (incoherent IR for malformed input).

### 10. Block-bodied func without explicit `return` and non-void return type accepted

**Files:** `internal/checker/checker.go`, `internal/checker/expr.go`

```
func mustReturn() int {
  var x = 1
}    // ok
```

No "missing return" diagnostic. Code generators must then synthesize a default, which has been a recurring bug source.
**Severity:** BUG (control-flow analysis missing).

---

## INCONSISTENCY

### 11. `func name(params) ReturnType => expr` rejected — no expression-body form with explicit return type

**Files:** `internal/parser/sngl.ebnf:306-309`
Grammar: `FuncBodyTail = fat_arrow Expr | [ Type ] StmtBlock`. The return type can attach only to the block form. So users can't write `func id<T>(x T) T => x`; they must drop the annotation and rely on inference, or rewrite as a block. This is undocumented in CLAUDE.md (which says the two valid forms are `func name(params) [Type] { ... }` and `func name(params) => expr` — leaving the question "may `=> expr` carry a return type?" implicit). Same constraint on `FuncLit` (line 521). Either accept `[Type] fat_arrow Expr` or document the restriction loudly.
**Severity:** INCONSISTENCY (expressiveness gap between the two forms; also blocks the natural generic-method-with-explicit-return signature).
Repro: `func double(x int) int => x * 2`

### 12. `else if` not supported in the grammar

**Files:** `internal/parser/sngl.ebnf:397`
`IfNode = kw_if CondExpr StmtBlock [ kw_else StmtBlock ]`. Only `else { ... }` is permitted; `else if` requires `else { if ... }`. Universal expected feature; absence forces verbose nesting.
**Severity:** INCONSISTENCY / UX (universally expected from C-family syntax).
Repro: `if x == 0 { } else if x == 1 { }`

### 13. Bare lambda `x => expr` rejected — only `func(x) => expr`

**Files:** `internal/parser/sngl.ebnf:481-502` (`PrimaryExpr` includes `FuncLit` but no bare-arrow alternative)

```
xs.map(x => x*2)     // parse error
xs.map(func(x) => x*2)  // ok
```

Per memory `feedback_test_api_use_event_form.md` the author has clear stylistic preferences; bare-lambda may be intentionally absent. But the absence is asymmetric with most modern UI DSLs and the verbose form makes generic-collection code noisy. If intentional, document the decision in CLAUDE.md; if not, add `Lambda = ident fat_arrow Expr | lparen ParamList rparen fat_arrow Expr` to `PrimaryExpr`.
**Severity:** INCONSISTENCY / UX.

### 14. Anonymous types in `var T` position work; in `const ... = T{...}` (expression position) do not

**Files:** `internal/parser/sngl.ebnf:481-502` (PrimaryExpr does not include kw_struct / kw_enum / kw_unit)
Per memory `sngl_anon_types.md`, "struct/enum/unit all valid anonymously in type position; named only at declaration." Parser/checker agrees in type position (verified: `var c enum {...}`, `var c unit {...}`, `var c struct {...}` all OK). But declaration like `const Color = enum { red, green, blue }` is rejected because `enum {…}` is not a `PrimaryExpr`. That's likely correct ("named at declaration"), but the parse error is huge and unhelpful:

```
unexpected "enum", expected "FuncLit", "AnonStructLit", "PrimaryExpr", "PostfixExpr", "UnaryExpr", "MulExpr", … (40+ alternatives)
```

**Severity:** INCONSISTENCY / UX (intended restriction, awful error).

### 15. `ERROR(parse)` directives in `testdata/*.sngl` are silently skipped — not asserted

**Files:** `internal/parser/build_test.go:817-829`, `internal/checker/checker_test.go:1172-1175,1265,1382`, `internal/optimize/optimize_test.go:553`
Every test that walks `testdata/` skips files with `ERROR(parse)` directives instead of asserting them. So fixtures like `error_integer_overflow.sngl`, `error_unterminated_string.sngl`, `error_bad_interpolation.sngl` (and ~30 others — `grep -l ERROR(parse) testdata/*.sngl | wc -l`) are dead — they neither verify the error message nor protect against parse-time regressions. Either run them through `parser.Parse` and assert the directive line/substring matches a reported error, or remove them.
**Severity:** INCONSISTENCY (silent test coverage hole; documented bugs in fixtures stay broken indefinitely).

### 16. Known const-forward-reference bug documented in fixture, never fixed

**Files:** `testdata/error_const_forward_ref.sngl`, `internal/checker/checker.go:572-611`
The fixture's own comment explains the bug:

> Bug: const forward references work in backward order but not forward order. […] The diagnostic also misleads — it labels B as "non-const" when the real reason is that B is unregistered at this point.
> Fix: pass1 should register `*ir.Var` shells for *all* consts before checking initializers (analogous to type shells in #2). The existing `nonConstRef` should then either operate on the IR after registration, or be removed in favor of the deferred const-purity check (`constAsserts`) already implemented for `const(expr)`.
> **Severity:** INCONSISTENCY (acknowledged bug, persistent).

### 17. `func` mutual recursion works but `struct` mutual recursion doesn't — same compiler, different rules

**Files:** `internal/checker/checker.go:232-301`
Functions are pre-registered (pass1 collects signatures, pass2 walks bodies). Structs/enums/units are registered *with their field types resolved inline*. Either both should pre-register or neither. See #2.
**Severity:** INCONSISTENCY.

### 18. Stale comment in `ast/expr.go:119` references the old `->` arrow form

**Files:** `ast/expr.go:119-124`

```go
// FuncType is a function type: func(int, string) -> bool.
```

Per CLAUDE.md `->` is being phased out and is only reserved for type expressions, but even there the grammar has zero arrow rules. Update to `func(int, string) bool` to match the actual syntax.
**Severity:** INCONSISTENCY (doc rot, low impact).

### 19. `ARROW` token is dead code throughout parser

**Files:** `internal/parser/token.go:43`, `internal/parser/lexer.go:270-273`, `internal/parser/sngl.ebnf:30`
`ARROW` is enumerated, lexed, and listed in the grammar comments — but no production uses it. Either delete entirely (the lexer would then split `->` into `MINUS GT`, which is also an invalid sequence and yields a clean "unexpected '-' before '>'" error) or wire it into a deprecation error path. Currently it's just landmines (see #1).
**Severity:** INCONSISTENCY (dead code with side effects).

---

## UX

### 20. Parser errors dump the entire FIRST set on every unexpected token

**Files:** `internal/parser/error_format.go`, egg-generated `zparser.go`
Sample:

```
unexpected "=>", expected "AnonStructLit", "PlatformNode", "ForNode", "IfNode",
"StatementPrimary", "VisualOrStmt", "ComponentDecl", "FuncDecl", "VarDecl",
"ConstDecl", "UnitDecl", "EnumDecl", "StructDecl", "ImportDecl", "I18nTriple",
"I18nInterpStr", "TripleInterp", "InterpStr", measurement literal,
"triple_start", "triple_full", "str_start", string, "//", ";", "}", string,
"(", "[", "{", "var", "unit", "struct", "return", "platform", "import", "if",
"func", "for", "enum", "const", "component", number, identifier, …
```

This is unreadable. `prettifyParseError` already exists (see `parse.go:73`); collapse non-terminal expectations into a category ("a statement" instead of every Stmt alternative) and cap to 5-7 concrete tokens.
**Severity:** UX (every parse error is a wall of text).

### 21. Argument-arity errors carry no source position

**Files:** `internal/checker/expr.go` (call-checking path)

```
genericid.sngl: expected 1 arguments, got 0
```

No `file:line:col` prefix; `CallExpr.Pos` should be threaded into the diagnostic. The same issue likely affects other terse error sites — grep for `c.error(…, "expected …", …)` calls that pass `nil` or omit a position. Pluralisation ("1 arguments" → "1 argument") is also off.
**Severity:** UX.

### 22. `StructFieldLit` and `Arg` carry no overall `Pos`, only `NamePos`

**Files:** `ast/expr.go:205-210, 340-344`
Both nodes have `NamePos` but no top-level `Pos`. For positional args / spread fields where `Name` is empty, errors fall back to the parent's position, which is imprecise. Add `Pos` to both for finer span reporting.
**Severity:** UX.

### 23. `IfStmt.Else` is a `StmtBlock` with no `ElsePos`; no way to report errors at the `else` keyword

**Files:** `ast/ast.go:316-322`
Combined with #12, this hides position info for else-branch diagnostics. Adding `ElsePos Pos` would help LSP and diagnostics.
**Severity:** UX.

### 24. `IncDecStmt` is statement-only, with a confusing parse error in expression position

**Files:** `internal/parser/sngl.ebnf:368-370`

```
var b = a++   // → "unexpected '++', expected …<huge list>"
```

The error doesn't mention that `++`/`--` are statement-only. Worth either accepting and erroring in the checker with a clear message, or catching the post-expression `++` in the parser and emitting `++ may only be used as a statement, not in an expression`.
**Severity:** UX.

### 25. `nonConstRef` returns sentinel names like `"<function call>"` that leak through diagnostics in some paths

**Files:** `internal/checker/checker.go:580-590, 626-693`
The sentinel-prefix check (`!strings.HasPrefix(name, "<")`) is a kludge; refactor to return an enum `{Const, ForwardRef, NonConstFunc, NonConstUnknown}` plus the name, so each case emits a structured diagnostic without string-prefix sniffing.
**Severity:** UX (maintainability; latent risk of bad messages if a new sentinel is added).

### 26. `Comment` carries `Inline bool` but the field is never set (`TODO: detect inline comments`)

**Files:** `internal/parser/build.go:171`
Formatter cannot distinguish trailing inline comments from preceding-line comments. Round-trip formatting may relocate them. Either implement the detection (compare comment line to preceding token line) or drop the field.
**Severity:** UX (formatter fidelity).

### 27. Implicit-conversion materialization is per-call-site, not centralized

**Files:** `internal/checker/coerce.go`, `internal/checker/checker.go:601-604` (`wrapIfNeeded`)
Per memory `feedback_explicit_conversions.md`, every implicit conversion must materialize as `ir.Conversion`. The code mostly does this via `adaptLiteralZero` + `wrapIfNeeded`, but the calls are sprinkled across `registerConsts`, `registerVars`, return checking, etc. A grep for `IsAssignableTo` shows ~20 sites; only ~half are followed by a `wrapIfNeeded`. Audit each site to confirm a Conversion is materialised when source and target differ — likely several silent paths (e.g. enum-bare-name → enum value, int → float in arithmetic, color literal → Color struct) skip the wrap.
**Severity:** UX (architectural drift; visible in optimizer / codegen as "where did the conversion go?").

### 28. Stdlib functions declared with `=>` and no explicit return type silently propagate `TypDyn` into user code

**Files:** `internal/checker/stdlib.go`, `internal/checker/checker_test.go:1244-1247`
`TestNoSilentDynInferred` whitelist documents this:

> Calls through these stdlib receivers produce TypDyn today because the stdlib's `=>` funcs omit explicit return annotations.
> This is the same issue as #11 from the other direction — without explicit return types on `=>` funcs, every stdlib generic method poisons inference. Either annotate the stdlib (blocked by #11's grammar gap) or run a return-type inference sub-pass before user pass2.
> **Severity:** UX (silent `dyn` is the documented anti-goal).

---

## Cross-cutting recommendations

1. **Promote pass1 to type-shell-only** (fixes #2, #6, #16, #17): walk all decls and register name+TypeParams shells; resolve field types, member values, and const initializers in a sub-pass1b after every shell is visible.
2. **Treat `->` as an unambiguous diagnostic** (fixes #1, #19): delete `ARROW` from the lexer; the resulting "unexpected '-' before '>'" is already clearer than the panic, or replace with a custom lexer error "the `->` arrow is no longer used; use `=> Expr` for expression bodies or drop the arrow entirely".
3. **Activate `ERROR(parse)` directive assertions** (fixes #15, surfaces #8): one test loop in `internal/parser/build_test.go` that runs `parser.Parse` and matches each parse directive against the returned diagnostics; promote silent skips to real failures.
4. **Audit list/call spread end-to-end** (#3): add `[...xs, 3]`, `f(...xs)`, struct spread, and map spread to a single fixture; ensure the IR contains a `Spread` node that codegen knows about.
5. **Collapse parser error first-sets** (#20): wrap egg's `expected:` list with a category mapping (Stmt → "a statement", Expr → "an expression") before formatting.
