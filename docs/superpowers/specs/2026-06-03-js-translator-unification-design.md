# Unify JS expression/statement translation onto JsIRContext

**Date:** 2026-06-03
**Status:** Implemented (2026-06-03)

## Problem

The JavaScript codegen has two parallel IR-to-JS translators:

1. **Legacy** — `codegen/lang/javascript/translate_ir.go`: `translateIRExpr` /
   `translateIRMutation`, driven by `*codegen.ExprScope` (flat maps:
   `ModelFields`, `ComputedFields`, `FuncNames`, `LocalVars`, `NeededHelpers`,
   `NativeImports`). Direct recursive descent. Exposed via the
   `LangTranslator.TranslateIRExpr` / `TranslateIRMutation` interface methods.

2. **New** — `codegen/lang/javascript/ircontext.go`: `JsIRContext` with
   `EvalExpr` / `EvalStmt`, driven by `*codegen.ExprCtx` (typed `Resolve`).
   irwalk visitor pattern. Fed through `scopedJC()` + `codegen.WalkLowered` +
   `htmlTranslator` so intrinsic/visual statements are lowered to DOM mutations
   before translation. Supports source-map position markers.

In `codegen/platform/html/html.go` (which always uses the JavaScript
translator — `jsLang := codegen.LookupLang("js")`), the two paths split:

- New path: handler bodies, timer bodies, setters, synthesized slot
  vars/funcs (`translateBlockJC`, `emitSynthesizedSlots`).
- Legacy path: user-function emission (`emitJSFunc`, lines 3096/3109/3113),
  init/value translation (`exprToJS`, line 3130), and the DOM-write fallback
  in `translateHandlerStmt` (lines 2728–2747).

A prior attempt to route everything through `JsIRContext` failed because the
new path silently lacks ~10 behaviors the legacy path implements; each fix
surfaced another. The two paths also keep duplicate helper-flagging state:
`ExprScope.NeededHelpers` (read at html.go:1992) vs `ExprCtx.Helpers` (written
by the new path) — they are not wired together, so a String-helper flagged on
the new path never reaches the emit check.

## Goal

A single JS translation path: `JsIRContext` + irwalk, fed via `scopedJC()` +
`WalkLowered` + `htmlTranslator`. Delete the legacy `ExprScope`-based
`translate_ir.go`. Consolidate helper-flagging onto `ExprCtx.Helpers`. Remove
`TranslateIRExpr` / `TranslateIRMutation` from the `LangTranslator` interface.

## Scope

**In scope:** JavaScript translator + the html platform's use of it.

- Go and Kotlin keep their own `translate_ir.go` and `TranslateIRExpr` /
  `TranslateIRMutation` as **non-interface** internal functions
  (`golang/http.go` calls `t.TranslateIRMutation` directly).
- `codegen.ExprScope` and its `NeededHelpers` field stay in `codegen.go` —
  Go's legacy path still uses `NeededHelpers["nativeMustOK"]`.
- The `LangTranslator` interface loses the two methods; only the html platform
  consumed them polymorphically, and after this work html no longer does.

**Out of scope (this project):** Migrating Go/Kotlin off `ExprScope`; deleting
`ExprScope` or `NeededHelpers` from `codegen.go`.

**Follow-up, immediately after:** the same unification will be applied to the
other languages (Go, Kotlin) and platforms (bubbletea, fyne, android, gtk4).
This JS pass is the **template** for those. Design and sequence the JS work so
it generalizes: the gap-closing → helper-consolidation → route-callers →
delete-legacy → trim-interface phasing, the byte-identical parity bar, and the
characterization-test-first discipline should transfer language-by-language.
When a choice here is JS-specific vs reusable, prefer the reusable shape (e.g.
keep the `*IRContext` + irwalk + `WalkLowered` pipeline structure identical
across langs) so the later passes are mechanical. Note the interface trim
(Phase 4) still happens in *this* project: Go/Kotlin keep `TranslateIRExpr` /
`TranslateIRMutation` as concrete methods called internally, so removing them
from `LangTranslator` does not depend on the Go/Kotlin migrations — those
remove the concrete methods later, once each language's html/platform callers
are off the legacy path.

## Feature gaps to close in JsIRContext (Phase 1)

The legacy path implements these; the new path must gain each (with a
characterization test written first, asserting parity with current legacy
output):

| Behavior | Legacy site | New site to add |
|---|---|---|
| int/int division → `Math.trunc(a / b)` | `translate_ir.go:49` (`isIntIR`) | `JsIRContext.Binary` |
| `IsElementRef` ident → `document.querySelector('[data-sngl-id="…"]')` | `:191` | `evalIdent` |
| native bundled-namespace alias in Select (+ `registerNativeImport`) | `:74–80` | `Select` / `evalIdent` |
| i18n PluralKey const in Select → string literal | `:66–69` | `Select` |
| `regex` builtin → `new RegExp(...)` | `:299` | `evalCall` |
| `CreateComponent` → `factoryName(comp)(props)` | `:409` | `evalNamespaceCall` |
| `bool` conversion → `Boolean(...)` | `:517` | `evalConversion` |
| PluralKey map literal → plain object, string keys | `:113` (`isPluralKeyMapType`) | `MapLit` |
| `async` lambda prefix | `:539` | `evalLambda` |
| richer literal type coverage (URL/Email/UUID/Regex/Base64/IPv4/IPv6/Hostname/Decimal; unit-suffix ordering) | `translateIRLiteral` | `evalLiteral` |

**Dispatch reconciliation (highest-risk item):** the legacy namespace /
type-method paths resolve user functions via `scope.FuncNames` (a set);
the new path scans `Ctx.Pkg.Funcs`. Pick the `Pkg.Funcs` scan (ExprCtx already
carries `Pkg`), drop JS's dependence on `ExprScope.FuncNames`, and verify
identical output across the corpus. Handle this first.

## Helper consolidation (Phase 2)

- Wire `g.ctx.Helpers = common.Helpers` at html construction (currently
  `NewExprCtx` makes a fresh map; `g.scope.NeededHelpers = common.Helpers`).
- Change the String-helper read at html.go:1992 from
  `g.scope.NeededHelpers["String"]` to `g.ctx.Helpers["String"]`.
- JS code stops writing `ExprScope.NeededHelpers`.

## Route html's three legacy sites onto the unified pipeline (Phase 3)

- **`emitJSFunc`** — keep html-side signature emission (synthetic `this`→`state`,
  `Receiver_Name` mangling, `async function` keyword). Route the **body
  statements** through `translateBlockJC` (already does `scopedJC` +
  `WalkLowered` + `htmlTranslator`). Delete the hand-rolled
  `LocalVar`/`Return`/`default→translateHandlerStmt` switch and the per-func
  `funcScope *ExprScope`.
- **`exprToJS`** — reactive branch → `g.scopedJC().EvalExpr(expr)`. Literal
  branch unchanged.
- **`translateHandlerStmt`** — delete. The `IsElementRef` DOM-write is handled
  by `htmlTranslator.OnPropAssign` inside the pipeline (`WalkLowered`
  `intrinsic_walker.go:77–81`). Remaining callers route through
  `translateBlockJC`.

## Delete legacy & trim interface (Phase 4)

- Delete `codegen/lang/javascript/translate_ir.go` legacy functions and
  `translate_ir_test.go` (after porting its cases to `ircontext_test.go`).
- Remove `javascript.Translator.TranslateIRExpr` / `TranslateIRMutation`.
- Remove `TranslateIRExpr` / `TranslateIRMutation` from the `LangTranslator`
  interface in `codegen/codegen.go`.
- Confirm Go/Kotlin still compile (their methods become non-interface internal
  funcs; `golang/http.go` calls them directly — already does).
- html stops constructing `g.scope` (`ExprScope`). Verify nothing else in html
  reads `g.scope`; remove the field if fully dead. (`TranslateIRLiteral` stays
  on the interface — `exprToJS`'s literal branch and other call sites use it.)

## Verification backbone

- **Before Phase 1:** capture golden JS output across the full `testdata/` +
  `cmd/sngl` golden suite as the parity baseline.
- **After each phase:** run `go tool verify`; diff generated JS. **Byte-identical
  output is the bar.** Any intentional diff must be explicitly called out and
  justified (the dispatch reconciliation in Phase 1 is the one place output
  could legitimately move — gate it on tests).
- **Regression fixtures:** the two recently-fixed bugs (i18n intrinsics mapped
  in `TranslateIRExpr`; bare synthesized idents) get explicit fixtures so the
  unification cannot silently reintroduce them.

## Risks

- **Dispatch reconciliation** (FuncNames vs Pkg.Funcs) — the one spot output
  could legitimately differ. Done first, test-gated.
- **`WalkLowered` on pure user-function bodies** — confirmed passthrough for
  non-intrinsic statements (`intrinsic_walker.go` default falls through
  unchanged), so routing user funcs through the pipeline is safe; the parity
  diff catches any surprise.
- **Signature emission divergence** — `emitJSFunc`'s `this`→`state` /
  `Receiver_` mangling / `async` keyword must stay on the html side; only body
  statements move. `JsIRContext.EmitFuncDef` (plain `function name(...)`) is
  NOT used for these — it lacks that mangling.
