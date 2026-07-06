# Parser & Checker Audit — 2026-05-22

Audit of parser and type-checker for inconsistencies, bugs, and UX gaps. Findings ranked: BUG > INCONSISTENCY > UX. Reproduce repros from `/home/jonathan/src/git.duckfam.us/jonathan/sngl` with `go run ./cmd/sngl check <file>`.

---

## BUG

### 1. Mutually recursive struct types fail in both orderings — ✅ RESOLVED (2026-07-06)

> Fixed: pass1 now registers every top-level type as a name shell first
> (`registerStructShell` for structs — name + type params, no fields;
> enums/units full; components after the shells so their prop/children types
> resolve), then resolves struct fields in a sub-pass (`resolveStructBody`)
> once all shells exist. Guards: `two_shell_pass1_test.go`
> (`TestStructForwardReference`, `TestStructMutualRecursion`,
> `TestStructSelfRecursion`). Method-signature forward references remain a
> pre-existing edge case (buildFunc resolves eagerly) — out of scope here.

**Files:** `internal/checker/checker.go:264-265`, `internal/checker/resolve.go:255-289`
`registerStruct` calls `buildStructDef` which calls `resolveTypeRequired` on every field type *immediately* during pass1's source-order walk. So:

```
struct A { b B }
struct B { a A }
```

fails with `unknown type "B"`. Swapping the order fails with `unknown type "A"`. Pass1 must first register name-only shells for all `StructDef`/`EnumDef`/`UnitDef`/`Component`, then field-resolve in a second sub-pass. The fixture `testdata/checker_mutual_recursion.sngl` only exercises function mutual recursion, not types — coverage gap.
**Severity:** BUG (no workaround for mutually referencing types).

### 2. List spread (`[...xs, 3]`) typechecks the spread as an element ⚠️ PARTIAL (2026-06-07)

Struct spread in function call arg lists (`f(...s)`) and component prop lists is now supported (`feat(checker): expand ...struct in function call arg lists` / `feat(checker): expand ...struct in component prop lists`). Error fixtures for type/duplicate violations added (`test: add missing spread error fixtures`). **Still open:** list literal spread `[...xs, 3]` — `inferListLit` does not handle `*ast.SpreadExpr`; and call spread with `list<T>` operand `f(...xs)` — the new spread path requires a struct operand.

**Files:** `internal/checker/expr.go:1462-1480`
`inferListLit` blindly calls `checkExprExpecting(e, elemExpected)` on every element; spread `*ast.SpreadExpr` (parsed correctly per `ast/expr.go:243`) falls through and is typed as `list<int>`, then the literal infers `list<list<int>>`:

```
var a list<int> = [1, 2]
var b list<int> = [...a, 3]   // error: cannot initialize list<int> with list<list<int>>
```

The function must special-case `*ast.SpreadExpr`, validate that the operand is a `list<T>` matching the element type, and flatten in IR. Same code path likely affects function-call spread (`f(...xs)`) — that case also fails (`spread_func.sngl` → "cannot pass list<int> as int").
**Severity:** BUG (no working spread syntax in lists or calls).

### 3. Field/method access on `string` (and likely other primitives) for unknown name silently returns `dyn`

**Files:** `internal/checker/expr.go` (SelectExpr branch)

```
var x = "hi"
var y = x.foo   // checks OK, y typed `dyn`
```

The fixture `testdata/error_selector_unknown_method_string.sngl` documents the method-form bug; the field-form has no fixture. Both should produce `no field "foo" on type string`. Compounded by the silent `dyn` infection — downstream uses don't error either.
**Severity:** BUG (loss of type safety).

### 4. Empty map literal `{}` against non-string-keyed map type emits wrong-shape error

**Files:** `internal/checker/expr.go:1419-1428`

```
var m map<float, int> = {}   // "ident-keyed literal does not match map<float,...>"
```

`reinterpretStructAsMap` runs on every anon struct lit against a map type, even when there are zero fields. It should short-circuit when `len(x.Fields) == 0` and return an empty `MapLitIR` typed at the expected map type, mirroring `inferMapLit`'s empty path at line 1493.
**Severity:** BUG (correct empty map syntax rejected with misleading message).

### 5. Bare enum-member name in `const` initializer not resolved against expected enum type — ✅ RESOLVED (2026-07-06)

> Fixed: top-level const values are checked in a deferred sub-pass
> (`checkPendingConstInits`) that runs `checkExprExpecting` first (resolving
> the bare enum member against the declared type) and then judges const-ness on
> the resolved IR via `ir.IsConst` — which now recognizes a bare enum-member
> Ident (`Member != ""`). `nonConstRef` is consulted only to phrase the error
> when the value is genuinely non-const. Guard:
> `two_shell_pass1_test.go::TestConstBareEnumMember`.

**Files:** `internal/checker/checker.go:572-624` (`registerConsts`, `nonConstRef`)

```
enum Color { red, green, blue }
const c Color = red   // error: const initializer forward-references "red"
```

Equivalent `var c Color = red` works. The cause: `nonConstRef` walks the AST before any expected-type / enum-member resolution; it doesn't know `red` is `Color.red`. Either make `nonConstRef` aware of enum member shorthand against `spec.Type`, or move the const-initializer-purity check to run on the IR after `checkExprExpecting` has performed enum-bare-name resolution.
**Severity:** BUG (asymmetry between var and const init, blocking idiomatic constant declarations).

### 6. Multiple parser productions panic on benign inputs

**Files:** `internal/parser/build.go`, `internal/parser/parse.go:42-48`
Beyond the `->` case, at least one other input panics:

```
(c == 0 ? a : b) = 5      // "parser panic: runtime error: index out of range [117] with length 117"
```

The deferred `recover()` in `Parse` catches the panic but the user-facing message ("index out of range [N] with length N") is useless. Either fix the builder's iterator bounds-checking (likely in `build.go` where `tokenAt`/iterator advance happens after a parse error tree has gaps) or wrap the recover with "internal parser bug, please report" plus dump the original source span.
**Severity:** BUG (compiler crash messages reach users).

### 7. Integer literal overflow is not detected anywhere

**Files:** `internal/parser/lexer.go`, `internal/checker/expr.go`

```
var x int = 99999999999999999999
```

typechecks `ok`. Fixture `testdata/error_integer_overflow.sngl` claims this should be `ERROR(parse) "invalid integer literal"`, but neither the parser nor the checker performs `strconv.ParseInt` on `INT` literals. The fixture is also never enforced (see #14).
**Severity:** BUG (silent overflow into IR; depending on codegen path may produce wrong runtime values).

### 8. Duplicate struct field and function parameter names accepted silently

**Files:** `internal/checker/resolve.go:255-289` (`buildStructDef`), `internal/checker/resolve.go` (param resolution)

```
struct P { x int; y int; x bool }  // ok
func f(x int, x int) => x          // ok
```

Both should be hard errors. Currently the second `x` field/param silently overrides or is appended, leaving an inconsistent IR.
**Severity:** BUG (incoherent IR for malformed input).

### 9. Block-bodied func without explicit `return` and non-void return type accepted

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

### 10. `func name(params) ReturnType => expr` rejected — no expression-body form with explicit return type

**Files:** `internal/parser/sngl.ebnf:306-309`
Grammar: `FuncBodyTail = fat_arrow Expr | [ Type ] StmtBlock`. The return type can attach only to the block form. So users can't write `func id<T>(x T) T => x`; they must drop the annotation and rely on inference, or rewrite as a block. This is undocumented in CLAUDE.md (which says the two valid forms are `func name(params) [Type] { ... }` and `func name(params) => expr` — leaving the question "may `=> expr` carry a return type?" implicit). Same constraint on `FuncLit` (line 521). Either accept `[Type] fat_arrow Expr` or document the restriction loudly.
**Severity:** INCONSISTENCY (expressiveness gap between the two forms; also blocks the natural generic-method-with-explicit-return signature).
Repro: `func double(x int) int => x * 2`

### 11. `else if` not supported in the grammar

**Files:** `internal/parser/sngl.ebnf:397`
`IfNode = kw_if CondExpr StmtBlock [ kw_else StmtBlock ]`. Only `else { ... }` is permitted; `else if` requires `else { if ... }`. Universal expected feature; absence forces verbose nesting.
**Severity:** INCONSISTENCY / UX (universally expected from C-family syntax).
Repro: `if x == 0 { } else if x == 1 { }`

### 12. Bare lambda `x => expr` rejected — only `func(x) => expr`

**Files:** `internal/parser/sngl.ebnf:481-502` (`PrimaryExpr` includes `FuncLit` but no bare-arrow alternative)

```
xs.map(x => x*2)     // parse error
xs.map(func(x) => x*2)  // ok
```

Per memory `feedback_test_api_use_event_form.md` the author has clear stylistic preferences; bare-lambda may be intentionally absent. But the absence is asymmetric with most modern UI DSLs and the verbose form makes generic-collection code noisy. If intentional, document the decision in CLAUDE.md; if not, add `Lambda = ident fat_arrow Expr | lparen ParamList rparen fat_arrow Expr` to `PrimaryExpr`.
**Severity:** INCONSISTENCY / UX.

### 13. Anonymous types in `var T` position work; in `const ... = T{...}` (expression position) do not

**Files:** `internal/parser/sngl.ebnf:481-502` (PrimaryExpr does not include kw_struct / kw_enum / kw_unit)
Per memory `sngl_anon_types.md`, "struct/enum/unit all valid anonymously in type position; named only at declaration." Parser/checker agrees in type position (verified: `var c enum {...}`, `var c unit {...}`, `var c struct {...}` all OK). But declaration like `const Color = enum { red, green, blue }` is rejected because `enum {…}` is not a `PrimaryExpr`. That's likely correct ("named at declaration"), but the parse error is huge and unhelpful:

```
unexpected "enum", expected "FuncLit", "AnonStructLit", "PrimaryExpr", "PostfixExpr", "UnaryExpr", "MulExpr", … (40+ alternatives)
```

**Severity:** INCONSISTENCY / UX (intended restriction, awful error).

### 14. `ERROR(parse)` directives in `testdata/*.sngl` are silently skipped — not asserted

**Files:** `internal/parser/build_test.go:817-829`, `internal/checker/checker_test.go:1172-1175,1265,1382`, `internal/optimize/optimize_test.go:553`
Every test that walks `testdata/` skips files with `ERROR(parse)` directives instead of asserting them. So fixtures like `error_integer_overflow.sngl`, `error_unterminated_string.sngl`, `error_bad_interpolation.sngl` (and ~30 others — `grep -l ERROR(parse) testdata/*.sngl | wc -l`) are dead — they neither verify the error message nor protect against parse-time regressions. Either run them through `parser.Parse` and assert the directive line/substring matches a reported error, or remove them.
**Severity:** INCONSISTENCY (silent test coverage hole; documented bugs in fixtures stay broken indefinitely).

### 15. Known const-forward-reference bug documented in fixture, never fixed — ✅ RESOLVED (2026-07-06)

> Fixed exactly as the fixture's own note prescribed: pass1 registers `*ir.Var`
> shells for all top-level consts (`registerConstShells`) before any initializer
> is checked, and the deferred `checkPendingConstInits` validates const-ness on
> the resolved IR. The documenting fixture was converted from an ERROR fixture
> to a passing one (`testdata/const_forward_ref.sngl`). See also #5.

### 16. `func` mutual recursion works but `struct` mutual recursion doesn't — same compiler, different rules — ✅ RESOLVED (2026-07-06)

> Fixed with #1: structs are now pre-registered as name+type-param shells like
> functions, and field types resolve in a later sub-pass, so both follow the
> same shell-then-body rule.

### 17. Stale comment in `ast/expr.go:119` references the old `->` arrow form

**Files:** `ast/expr.go:119-124`

```go
// FuncType is a function type: func(int, string) -> bool.
```

Per CLAUDE.md `->` is being phased out and is only reserved for type expressions, but even there the grammar has zero arrow rules. Update to `func(int, string) bool` to match the actual syntax.
**Severity:** INCONSISTENCY (doc rot, low impact).

---

## UX

### 18. Parser errors dump the entire FIRST set on every unexpected token

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

### 19. Argument-arity errors carry no source position

**Files:** `internal/checker/expr.go` (call-checking path)

```
genericid.sngl: expected 1 arguments, got 0
```

No `file:line:col` prefix; `CallExpr.Pos` should be threaded into the diagnostic. The same issue likely affects other terse error sites — grep for `c.error(…, "expected …", …)` calls that pass `nil` or omit a position. Pluralisation ("1 arguments" → "1 argument") is also off.
**Severity:** UX.

### 20. `StructFieldLit` and `Arg` carry no overall `Pos`, only `NamePos`

**Files:** `ast/expr.go:205-210, 340-344`
Both nodes have `NamePos` but no top-level `Pos`. For positional args / spread fields where `Name` is empty, errors fall back to the parent's position, which is imprecise. Add `Pos` to both for finer span reporting.
**Severity:** UX.

### 21. `IfStmt.Else` is a `StmtBlock` with no `ElsePos`; no way to report errors at the `else` keyword

**Files:** `ast/ast.go:316-322`
Combined with #11 (`else if` not supported), this hides position info for else-branch diagnostics. Adding `ElsePos Pos` would help LSP and diagnostics.
**Severity:** UX.

### 22. `IncDecStmt` is statement-only, with a confusing parse error in expression position

**Files:** `internal/parser/sngl.ebnf:368-370`

```
var b = a++   // → "unexpected '++', expected …<huge list>"
```

The error doesn't mention that `++`/`--` are statement-only. Worth either accepting and erroring in the checker with a clear message, or catching the post-expression `++` in the parser and emitting `++ may only be used as a statement, not in an expression`.
**Severity:** UX.

### 23. `nonConstRef` returns sentinel names like `"<function call>"` that leak through diagnostics in some paths

**Files:** `internal/checker/checker.go:580-590, 626-693`
The sentinel-prefix check (`!strings.HasPrefix(name, "<")`) is a kludge; refactor to return an enum `{Const, ForwardRef, NonConstFunc, NonConstUnknown}` plus the name, so each case emits a structured diagnostic without string-prefix sniffing.
**Severity:** UX (maintainability; latent risk of bad messages if a new sentinel is added).

### 24. `Comment` carries `Inline bool` but the field is never set (`TODO: detect inline comments`)

**Files:** `internal/parser/build.go:171`
Formatter cannot distinguish trailing inline comments from preceding-line comments. Round-trip formatting may relocate them. Either implement the detection (compare comment line to preceding token line) or drop the field.
**Severity:** UX (formatter fidelity).

### 25. Implicit-conversion materialization is per-call-site, not centralized

**Files:** `internal/checker/coerce.go`, `internal/checker/checker.go:601-604` (`wrapIfNeeded`)
Per memory `feedback_explicit_conversions.md`, every implicit conversion must materialize as `ir.Conversion`. The code mostly does this via `adaptLiteralZero` + `wrapIfNeeded`, but the calls are sprinkled across `registerConsts`, `registerVars`, return checking, etc. A grep for `IsAssignableTo` shows ~20 sites; only ~half are followed by a `wrapIfNeeded`. Audit each site to confirm a Conversion is materialised when source and target differ — likely several silent paths (e.g. enum-bare-name → enum value, int → float in arithmetic, color literal → Color struct) skip the wrap.
**Severity:** UX (architectural drift; visible in optimizer / codegen as "where did the conversion go?").

### 26. Stdlib functions declared with `=>` and no explicit return type silently propagate `TypDyn` into user code

**Files:** `internal/checker/stdlib.go`, `internal/checker/checker_test.go:1244-1247`
`TestNoSilentDynInferred` whitelist documents this:

> Calls through these stdlib receivers produce TypDyn today because the stdlib's `=>` funcs omit explicit return annotations.
> This is the same issue as #10 from the other direction — without explicit return types on `=>` funcs, every stdlib generic method poisons inference. Either annotate the stdlib (blocked by #10's grammar gap) or run a return-type inference sub-pass before user pass2.
> **Severity:** UX (silent `dyn` is the documented anti-goal).

---

## Cross-cutting recommendations

1. **Promote pass1 to type-shell-only** (fixes #1, #5, #15, #16): walk all decls and register name+TypeParams shells; resolve field types, member values, and const initializers in a sub-pass1b after every shell is visible.
2. **Activate `ERROR(parse)` directive assertions** (fixes #14, surfaces #7): one test loop in `internal/parser/build_test.go` that runs `parser.Parse` and matches each parse directive against the returned diagnostics; promote silent skips to real failures.
3. **Audit list/call spread end-to-end** (#2): add `[...xs, 3]`, `f(...xs)`, struct spread, and map spread to a single fixture; ensure the IR contains a `Spread` node that codegen knows about.
4. **Collapse parser error first-sets** (#18): wrap egg's `expected:` list with a category mapping (Stmt → "a statement", Expr → "an expression") before formatting.
