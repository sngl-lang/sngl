# Dead / Redundant / Duplicated Code Audit — 2026-08-17

Whole-tree audit for dead code, copy-paste duplication, and divergent-behavior
bugs. Goal: reduce maintenance burden and kill bugs where parallel code paths
have drifted. Every finding is grounded in `file:line` and was verified with a
repo-wide `rg` (including `*_test.go`, `//go:build js` playground, interface
satisfaction, and `go://` SNGL imports) — not just the raw `deadcode` output.

**Method.** `golang.org/x/tools/cmd/deadcode -test ./...` produced 239 candidates;
each was hand-verified. Known false-positive classes were excluded: the `sngl.go`
public API (stable surface per CLAUDE.md), `pkg/{go,js,kotlin}/*` runtime packages
(imported by *generated* code, never the compiler), `internal/playground` (WASM/js
target), and — the big one — most of `docs/`, `docs/lookup/`, `internal/docsite/`
which are reachable only through `go://` imports in `website.sngl` /
`internal/docui/*.sngl` that the Go analyzer cannot see through.

`go vet ./...` is **clean**.

## Status (2026-08-17)

Landed on branch `cleanup/dead-redundant-code`:

- **§0.1** — fixed (`isBuiltinTypeName` predicate + regression fixture
  `testdata/checker_const_sized_cast.sngl`).
- **§0.2** — investigated: **not a bug.** Kotlin's `Select` lowers `i18n.one`
  directly to the string literal `"one"` (ircontext.go), so by the time `MapLit`
  runs the keys are already strings and `mapOf("one" to …)` is correct — confirmed
  by the passing `TestKtIRContext_PluralKeyMapLit`. Kotlin simply took a different
  (valid) approach than JS; `isPluralKeyMapType` was genuinely dead and is removed
  under §1.3.
- **§1 (all dead code)** — removed. ~2,050 LOC across 49 files. Full build, vet,
  WASM, docsgen and `go test ./...` pass.
- **§2.1** — done (four lower walkers → shared `rewriteStmtExprs`).

Deliberately deferred to a focused follow-up (introduce cross-package coupling /
call-site churn for modest LOC; kept out to keep this PR a clean removal):
**§2.2** (desktop helper hoisting), **§2.3** (language-translator predicate
hoisting), **§2.5** minor overlaps, and **§2.4** (the ~10 hand-rolled `ir.Stmt`
walkers → `ir.WalkStmts`) — the last is explicitly a careful separate pass since
the copies carry slightly different per-node side effects.

## Removable-LOC summary

| Area | Dead LOC | Redundant/consolidatable LOC |
|---|---:|---:|
| `codegen/` root (`compat.go`, `treewalk.go`, `deps.go`, `model.go`, `iterate.go`, …) | ~620 | (10× stmt-walker copies — architectural) |
| `codegen/platform/fyne` + `gtk4` | ~620 | ~90 |
| `codegen/lang/{golang,javascript,kotlin}` | ~277 | ~50 |
| `codegen/platform/html` + `internal/htmlutil` | ~357 | (`literalToJS` overlap) |
| front-end (`checker`/`parser`/`ir`/`optimize`/`expand`) | ~140 | — |
| back-end (`lower`/`lsp`/`docs`/`cmd`/`testutil`) | ~332 | ~250 (4 identical lower walkers) |
| **Total** | **≈ 2,350 dead** | **≈ 400 consolidatable** |

---

## 0. BUGS from divergence (fix first — correctness, not cleanup)

### 0.1 `nonConstCallRef` rejects sized/temporal casts in component & block consts — **FIXED**

`internal/checker/checker.go:894` `nonConstCallRef` hardcodes the conversion-name
whitelist in two hand-maintained lists that have drifted from the canonical set:

- `checker.go:909` (IdentExpr): `"int","float","string","bool"` only.
- `checker.go:916` (SelectExpr namespace): `+ "list","color","ref"`.

Any conversion **not** in the list falls through to `return "<function call>"`
(checker.go:931), which the caller treats as "references non-const". The canonical
conversion set (`expr.go:763-812` `inferBuiltinConversion`, `resolve.go:55-100`)
additionally includes `int8/16/32/64`, `uint8/16/32/64`, `float32/64`, `duration`,
`date`, `time`, `dateTime` — all added on `feat/sized-numeric-types`, this list
never updated.

`registerConsts` (checker.go:659) and `checkComponentConsts` (checker.go:1045)
call `nonConstRef` **without** the `!ir.IsConst(initExpr)` guard that masks the
top-level deferred path (checker.go:779), so the divergence is path-dependent.

**Reproduced** (`sngl check`):
```sngl nocheck
component Main {
  const A = int32(5)     // ERROR: const initializer references non-const "<function call>"
  const B = int(5)       // OK
  const D = float32(1.5) // ERROR (same)
}
```
Top-level `const A = int32(5)` escapes the bug (masked by the 779 guard) —
making it path-dependent and easy to miss.

**Fix:** derive the const-safe-conversion check from the *same* predicate
`inferBuiltinConversion` switches on, not three hand-copied string lists.
Severity: wrong-output (rejects valid programs). Confidence: **HIGH (verified)**.

### 0.2 Kotlin `map<i18n.PluralKey, V>` special-case never ported from JS — **INVESTIGATED, NOT A BUG**

`codegen/lang/kotlin/ircontext.go:1013` `isPluralKeyMapType` exists (doc: "lowers
such maps to `mapOf()` with string keys") but is **never called** — Kotlin's
`MapLit` (ircontext.go:154) emits `mapOf(k to v)` ignoring the map type. The JS
twin (`js/jshelpers.go:82`) **is** wired into `MapLit` (js/ircontext.go:104) to
emit computed string keys for `map<i18n.PluralKey,V>`. The dead Kotlin helper is
the smoking gun that the JS PluralKey-map special-case was never ported to Kotlin,
which is string-keyed at the i18n runtime. Add a `map<i18n.PluralKey, string>`
fixture under `--lang kotlin` to confirm latent vs. live. Confidence: **LOW-MED**.

---

## 1. DEAD CODE — safe to delete (checkbox = done)

All HIGH confidence unless noted. Verified 0 live references.

### 1.1 `codegen/` root — ~620 LOC

- [x] **`codegen/compat.go` — delete 30 of 32 funcs (~330 LOC).** Abandoned AST
  compatibility shim; IR pipeline superseded it. Dead: `DocVarDecls`,
  `DocConstDecls`, `DocFuncDefs`, `DocStructDefs`, `DocEnumDefs`, `DocComponents`,
  `DocUnitDefs`, `DocImports`, `DocBodyStmts`, `FindMainComponent`, `VnProps`,
  `VnProp`, `VnEvents`, `VnEvent`, `VnHasEvents`, `VnChildren`, `VnChildNodes`,
  `ExprLiteralBool`, `ExprLiteralInt`, `ExprLiteralFloat`, `ExprIsLiteral`,
  `ExprLiteralAny`, `ExprIsReactive`, `CallFuncName`, `CallArgs`, `CallArgCount`,
  `CompHasChildren`, `CompBodyStmts`, `ExprTypeHint`, `ResolveComponentBody`
  (also a no-op stub that computes then discards), `VnStyleFields`.
  **KEEP:** `ExprLiteralString` (used by fyne/blueprint.go ×3), `CompParams`
  (testharness/promote.go:95).
- [x] **`codegen/treewalk.go:10-125` — delete `TreeWalker`/`NodeVisitor` framework (~100 LOC).**
  Zero users. `TreeWalker.WalkStmts` is unrelated to the live `ir.WalkStmts`.
  Symbols: `NodeVisitor`, `TreeWalker` + `walkStmt`/`walkNodeInst`/`expandComponent`/
  `SlotChildren`/`WalkStmts`, `VisualNodeName`, `maxComponentDepth`. **KEEP `FindComponent`** (testharness/promote.go:20).
- [x] **`codegen/deps.go` + `codegen/model.go` — dead "affected-updaters" island (~55 LOC), remove as one unit.**
  `deps.go`: `FindAffected`, `Dependent` iface, `DepTracker.ExpandMutated`,
  `MutatedFieldsExpr`, `FindRootIdent`. `model.go`: `MutationModel.AffectedUpdaters`,
  `Updater.DepVars`. **KEEP** the `DepTracker`/`MutatedFields`/`ExprDeps` core (heavily used).
- [x] **`codegen/iterate.go` — 7 dead collectors (~110 LOC):** `AllComponents`,
  `collectReachableComponents`, `AllNonMainComponents`, `CollectNodes`,
  `CollectNodesByName`, `NodeHasHandlers`, `IRLiteralInt` (siblings `IRLiteralString/Bool/Any` live).
- [x] **`codegen/registry.go:42` `CollectLanguages`** (~15 LOC) — 0 refs; twin `CollectPlatforms` is used.
- [x] **`codegen/scheme.go:87` `Schemes`** (~9 LOC) — 0 refs.
- [x] **`codegen/namer.go:34` `Namer.Count`** (~3 LOC) — 0 refs.

### 1.2 `codegen/platform/fyne` + `gtk4` — ~620 LOC

- [x] **`codegen/platform/fyne/view_ir.go` — delete the entire `irViewContext` type + 24 methods + 6 helpers (~600 LOC).**
  Abandoned/superseded reimplementation; the live path is `newFyneTranslator` +
  `codegen.WalkLowered`. Nothing constructs `irViewContext{` in fyne. Dead helpers:
  `substituteTemplate`, `blueprintUsesChildren`, `containsChildrenToken`, `hasInit`,
  `resolveGoFn`, `ctorHasProp`. **KEEP the two live structs in the same file:**
  `irWidgetField` (line 14), `entrySyncRec` (line 63) — used by `compiler_ir.go`.
- [x] **`codegen/platform/fyne/compiler_ir.go:46` `irAnalysis.depTracker`** — dies with the above (sole caller was `irViewContext.exprDeps`).
- [x] **`codegen/platform/gtk4/gtk4.go:38` `pickPrimaryConstructor`** (~4 LOC) — wrapper, live code calls `pickPrimaryConstructorInfo` directly.
- [x] **`codegen/platform/gtk4/view_ir.go:51` `userNodeID`** (~10 LOC) — 0 refs.
- [x] **`codegen/platform/gtk4/view_ir.go:95` `gtkSetter`** (~4 LOC) — comment says "Kept for callers"; there are none (live: `gtkSetterFor`).

### 1.3 `codegen/lang` — ~277 LOC

- [x] **`codegen/lang/golang/helpers.go` — AST-era leftovers (~175 LOC):**
  `astLiteralToGo`, `LiteralToGo`, `UnexportName`, `InferGoType`, `SnglNodeGoType`,
  `ExternFuncGoType`, `NeedsTimeType`. Migration residue; live path is
  `translateIRLiteral`/`IRTypeToGo`. **KEEP** `TypeHintToGo`, `ExportName`,
  `ZeroValueGo`, `ComponentRenderMethod` (used by fyne/gtk4/bubbletea).
- [x] **`codegen/lang/javascript/javascript.go:88` + `kotlin/kotlin.go:32` `IsIntlIntrinsic`** — dead in both; one-line delegates to the real `codegen/i18n.IsIntrinsic`.
- [x] **`codegen/lang/javascript/javascript.go:208` `isIntNode`** (~20 LOC) — dead AST helper.
- [x] **`codegen/lang/kotlin/ircontext.go:1013` `isPluralKeyMapType`** (~12 LOC) — never called (see bug 0.2).
- [x] **`codegen/lang/kotlin/testlower.go:45` `LowerTestFunc`** (~33 LOC) — no caller; `LowerTestFile` inlines the lowering.
- [x] **`codegen/lang/golang/testlower.go:25` `LowerTestFunc`** (~29 LOC, MED) — dead in production; kept alive only by `golang/testlower_test.go`. Delete with `TestLowerTestFunc_trivialAssert`.

### 1.4 `codegen/platform/html` + `internal/htmlutil` — ~357 LOC

- [x] **`internal/htmlutil` — delete the whole AST-based helper cluster (~114 LOC), drops the `ast` import:**
  helpers.go — `ExprToStaticValue`, `LiteralRawToString`, `LiteralToString`,
  `StaticStringArg`, `StaticBoolArg`, `unquote`, `IsSelfClosing`; style.go —
  `BuildCSSStyle`, `StylePropToCSS`, `AppendCSS`. Each has a live IR twin
  (`BuildCSSStyleIR`, `ExprToStaticValueIR`, `irLiteralStaticValue`, …). **KEEP** the IR roots.
- [x] **`codegen/platform/html/routes.go:359-500` — dead duplicate reachability walker (~142 LOC):**
  `fnCallsTarget`, `stmtsCallTarget`, `stmtCallsTarget`, `exprCallsTarget` — only
  self-referential; superseded by `handlerPlacement`→`exprPlacement` (placement.go).
  `collectActions` decides backend-vs-frontend via `handlerPlacement`, ignoring these.
- [x] **`codegen/platform/html/html.go`** — `isFunction` (1472), `complexLiteralToJS` (3281), `extractSetTarget` (3394) (~30 LOC).
- [x] **`codegen/platform/html/promote.go` — whole file dead (~13 LOC):** `PromoteComponent` ("preserved for backward compatibility"), 0 callers.
- [x] **`codegen/platform/html/intrinsic_translator.go:86` `identBareName`** — dead *in html* (fyne/gtk4 copies live; see 2.2).
- [x] **`codegen/platform/android`** — `i18n.go:43` `i18nRuntimeFile`, `i18n.go:54` `i18nManifestFile` (superseded by `emit*` sink variants), `scaffold.go:145` `scaffoldTestFiles` (superseded by `scaffoldFiles(..., testMode=true)`) (~52 LOC).

### 1.5 front-end (`checker`/`parser`/`ir`/`optimize`/`expand`) — ~140 LOC

- [x] **`internal/expand` — the whole "post-expand" subsystem is unwired (~40-50 LOC):**
  `post.go:6` `ExpandPost` (a no-op), `registry.go:33` `RegisterPost` + `PostHandler`
  + `postRegs` (0 registrations, never read), and the `sngl.go:44` `sngl.ExpandPost`
  public wrapper (0 callers). The *pre*-expand macro path (`expand.Expand`) is live — keep it.
- [x] `internal/checker/i18n.go:295` `lookupI18nTr` — superseded by `lookupI18nTrInline`.
- [x] `internal/checker/types.go:103` `MapOf` — wrapper; callers use `ir.MapOf` directly.
- [x] `internal/optimize/consteval.go:913` `toStr` — 0 refs (siblings `intToStr`/`floatToStr` live).
- [x] `internal/parser/format.go:96` `formatter.flushBlank` — 0 refs.
- [x] `internal/parser/build.go` — `nodeIter.peek` (51), `shiftIdx` (69), `childSlice` (94), `builder.pos` (99), and the whole **`exprStmt` type + `StmtPos`** (300-305, never instantiated).
- [x] `ir/convert.go:921` `stringLit` — 0 refs.
- [x] `ir/types.go:490,496` `DateType()` / `TimeType()` accessors — 0 refs; backing fields `stdlibDateType`/`stdlibTimeType` set-but-never-read (only `DateTimeType()` is used).
- [x] test helpers: `checker/pointsto_test.go:15` `funcTypeNoArgs`, `checker/build_test.go:634` `identNameTest` (MED).

### 1.6 back-end (`lower`/`lsp`/`docs`/`cmd`/`testutil`) — ~332 LOC

- [x] `cmd/sngl/doc.go` — `renderValueDoc` (657), `listTopics` (959) (~60 LOC).
- [x] `internal/docbrowser/data.go:13` `Run()` — wrapper; callers use `RunWithDir`/`Handler`.
- [x] `internal/lspcore/hover.go:696` `walkVisualNodes` (~30 LOC).
- [x] `internal/lsp/position.go` — effectively an empty file (`package lsp` only); real code is `lspcore/position.go`.
- [x] `internal/snapshot/compile.go` — `checkDoc` (133, unexported), `PlatformsForFile` (111, exported, MED-HIGH).
- [x] **`docs/docs.go` vestigial workspace-scan cluster (~55 LOC, MED):** `Lookup` (408) → `Components` (370) → `parseDir` (429), plus `SetWorkspaceDir`/`workspaceDir` (30/26) whose stored value is read only by dead `Components`. (Verified dead in both Go *and* `.sngl`.) `StdlibLookup` is the live variant — keep it.
- [x] `docs/lookup/cache.go:36` `InvalidateCache` — 0 callers (Go or `.sngl`).
- [x] **`internal/testutil` superseded assertion API (~150 LOC)** — live path is `TestdataSamples`/`DocSamples` + `AssertDiagnostics`. Dead: `sample.go` `Samples` (84), `Sample.{AssertErrors,AssertFolds,WriteBack}`; `testutil.go` `AssertFolds`/`assertFolds`, `parseLiteralValue`, `findExprAtLine`, `searchVarSpecs`, `AssertErrors`/`assertErrors`, `RunFixtures`.

---

## 2. REDUNDANT / DUPLICATED — consolidate

### 2.1 Four byte-identical statement-rewrite walkers in `internal/lower` — **VERIFIED identical (~249 LOC)**

`rewriteEnumStmts` (enum.go:147), `inlineComputedStmts` (computed.go:187),
`flattenSpreadStmts` (flatten_struct_spread.go:254), `rewriteUnitStmts`
(unit.go:107) are **character-for-character identical modulo the function name**
(confirmed via normalized diff). All four have signature
`func(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt` and walk the IR
statement tree applying `rewrite` to every leaf expr. Collapse to one shared
`rewriteStmtExprs(stmts, rewrite)` beside `walk.go`. Removes ~249 LOC. **HIGH.**

### 2.2 Byte-identical helpers triplicated across the Go desktop generators (~90 LOC)

- `irFuncReturnType` — fyne/compiler_ir.go:813, gtk4:1256, bubbletea:1323 (identical).
- `irVarGoType` — fyne/compiler_ir.go:801, gtk4:1240, bubbletea:1309 (identical).
- `identBareName` — fyne/intrinsic_translator.go:442, gtk4:615, html:86 (html's is dead — see 1.4).
- `modelFieldRef` — fyne/intrinsic_translator.go:90, gtk4:181 (identical).
- `isLocalRef` — fyne/gtk4 intrinsic_translator.go:71 (differ only in receiver type).

Good candidates for a shared `codegen/lang/golang` helper.

### 2.3 Identical IR-level predicates copy-pasted across the 3 language translators (~50 LOC)

Pure-IR (no language-specific output), byte-identical — hoist into shared codegen:
- `isNullToFuncConv{,JS,Kt}` — golang:1063, js:635, kotlin:571.
- `is{Go}RaiseFunc`/`jsIsRaiseFunc`/`ktIsRaiseFunc` — golang:974, js:299, kotlin:459.
- `jsI18nConstString` / `kotlinI18nConstString` — js:866, kotlin:992 (identical; golang's `i18nPluralKeyGoName` intentionally differs).
- `isPluralKeyMapType` — js/jshelpers.go:82 vs kotlin/ircontext.go:1013 (kotlin copy also dead).

### 2.4 ~10 hand-rolled `ir.Stmt` traversal switches (architectural, MED)

At least ten independent `switch s.(type)` copies over `ir.Stmt` each re-list the
statement variants and end in `panic("unhandled stmt %T")`: `treewalk.go` (dead),
`iterate.go`, `deps.go`, `html/rendermodel.go`, `html/placement.go`,
`checker/pointsto.go`, `checker/purity.go`, `optimize/interpret.go`,
`optimize/shake.go`, `lower/lambda.go`, `lower/normalize_method_calls.go`,
`fuzz_test.go`. The canonical `ir.WalkStmts`/`WalkExprs` (`ir/walkexprs.go`) is used
in only 2 places. **Real hazard:** each copy must be updated when a new `ir.Stmt`
variant is added; a missed one panics. Not a mechanical delete (per-node side
effects differ) — worth a deliberate consolidation pass.

### 2.5 Minor overlaps (LOW)

- `codegen/platform/html/html.go:3244` `literalToJS` keeps its own scalar-literal
  switch overlapping `javascript.translateIRLiteral` — a second source of truth
  (drift risk), while `exprToJS` already delegates to `TranslateIRLiteral`.
- `codegen/lang/{golang,javascript}/stmtpos.go` `stmtIRPos` — byte-identical (only package + comment differ); hoistable to a shared helper.
- `docs/docs.go:584` `firstSentence` duplicates `docs/lookup/lookup.go:741` `FirstSentence` — delegate to the exported one.
- `ir/convert.go:925` `formatFloat` vs `optimize/consteval.go:924` `floatToStr` — different quirks, not trivially unifiable.

---

## 3. Other concerns noted in passing

- **`codegen/registry.go` asymmetry** — `CollectPlatforms` is used but
  `CollectLanguages` is dead. The checker's `Languages` config is presumably
  populated elsewhere; worth confirming that's intended, not an oversight.
- **`ir/types.go` set-but-never-read fields** — `stdlibDateType`, `stdlibTimeType`
  are assigned but only reachable through the dead accessors (1.5); remove the
  fields with the accessors.
- **False-positive caveat for future audits:** `deadcode` cannot see through SNGL
  `go://` imports. Anything in `docs/`, `docs/lookup/`, `internal/docsite/`, and
  `examples/*/session.go` that it flags is very likely reached from `.sngl`
  (`website.sngl`, `internal/docui/*.sngl`) — verify against `.sngl` usage before
  deleting. This audit already did so; the survivors above are the genuine ones.

---

## Suggested execution order

1. **Bug 0.1** (`nonConstCallRef`) — real correctness bug, small fix, add a
   `testdata/*.sngl` fixture with a sized/duration/date const cast.
2. **Fixture for bug 0.2** (Kotlin PluralKey map) — confirm latent vs. live.
3. **Pure deletions (§1)** — mechanical, low risk, each independently landable.
   Biggest single wins: fyne `irViewContext` (~600), `compat.go` (~330),
   html `routes.go` walker (~142) + `htmlutil` AST cluster (~114), testutil (~150).
4. **§2.1** four lower walkers → one helper (~249 LOC, trivially safe).
5. **§2.2/2.3** desktop + language helper hoists (mechanical).
6. **§2.4** the `ir.WalkStmts` consolidation — deliberate, do last.
