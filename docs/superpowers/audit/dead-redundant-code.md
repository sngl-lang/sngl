# Dead / Redundant / Duplicated Code Audit — 2026-08-17

Whole-tree audit for dead code, copy-paste duplication, and divergent-behaviour
bugs. The bulk landed on branch `cleanup/dead-redundant-code` (MR !10): ~2,050
LOC of verified-dead code removed across 49 files, the sized-cast const bug
fixed, and the four identical `internal/lower` statement-walkers unified into one
`rewriteStmtExprs`. Completed items have been pruned from this report; what
remains below is the **outstanding consolidation work** plus a reusable caveat.

Resolved and removed from this doc: §0.1 (the `nonConstCallRef` sized-cast bug —
fixed with a shared `isBuiltinTypeName` predicate + fixture), all §1 dead-code
deletions, and §2.1 (the lower-walker unification). §0.2 (a suspected Kotlin
`PluralKey`-map gap) was investigated and is **not a bug** — Kotlin lowers
`i18n.<key>` to a string literal in `Select` before `MapLit` runs, so
`mapOf("one" to …)` is already correct (see `TestKtIRContext_PluralKeyMapLit`).

---

## Remaining: REDUNDANT / DUPLICATED — consolidate (follow-up)

**§2.2, §2.3, and the identical-helper parts of §2.5 landed in MR !11**, each
placed in its proper layer rather than a shared grab-bag:

- **`ir`** (`ir/predicates.go`): `IsNullToFuncConv`, `IsErrorRaiseFunc`, `StmtPos`
  — pure IR queries, alongside the existing `IsColorStruct`/`IsDateStruct` family.
  This also absorbed the checker's own byte-identical `isRaiseFunc`, which a
  codegen home couldn't reach.
- **`codegen/i18n`**: `PluralKeyConstString` (JS+Kotlin).
- **`codegen`** (`modelref.go`): `ModelFieldRef`/`IdentBareName` — the `m.` model-
  receiver convention.
- **`codegen/lang/golang`** (`irhelpers.go`): `FuncReturnGoType`/`VarGoType` —
  genuinely Go-type rendering, shared by the Go-emitting platforms.

`isLocalRef` (fyne/gtk4) was intentionally left — a method differing only in
receiver type, so unifying adds more indirection than it saves.

**Latent gap surfaced:** `StmtPos` only exists because `ir.Stmt` has no uniform
`Pos()` accessor; adding one (and repointing `StmtPos`) is a small follow-up.

What remains:

### §2.4 hand-rolled `ir.Stmt` traversal switches → the shared IR visitor (in progress)

Investigation showed this is **not** a mechanical repoint: the canonical walker
was package-rooted, read-only, and stop-all, while the ~10 hand-rolled copies
variously need subtree roots, per-branch pruning, mutation, enclosing-function
context, or type traversal. Doing it as a project instead:

**Done — visitor generalized + read-only consumers migrated:**
- `ir/walkexprs.go`: added `WalkAction { Continue, SkipChildren, Stop }` (prune
  vs. stop, which the old `bool` conflated) and subtree entry points
  `InspectPackage`/`InspectFunc`/`InspectStmts`/`InspectExpr` over the one
  scaffold. Old `Walk`/`WalkExprs`/`WalkStmts` kept as bool adapters.
- Migrated: `html/placement.go` (prune semantics → `SkipChildren`),
  `html/rendermodel.go` `exprIsReactive`, `iterate.go` `collectWindows`,
  `checker/purity.go` `analyzeEffects` (also fixes a latent bug — it had no
  default case and silently skipped unknown stmt kinds).

**Deliberately left custom (the visitor can't serve these without changing
behaviour):**
- `checker/pointsto.go` — context-*sensitive*: binds `SlotReturnKey(w.fn)`, which
  needs the enclosing function the context-free visitor doesn't expose.
- `optimize/shake.go` — also walks `ir.Type` (not just stmts/exprs) to collect
  symbols for DCE; the visitor doesn't traverse types, and a wrong result
  silently drops live code.
- `html/rendermodel.go` `renderBuilder.walkStmt` (threads a `path`),
  `iterate.go` `walkVisual` (threads `depth`) — state-threading builders, not
  queries.
- `optimize/interpret.go` — an interpreter dispatch, not a traversal.

**Phase 2 — done, with a finding:** a separate "mutation walker" abstraction
turned out **not** to be warranted.
- `lower/normalize_method_calls.go` — migrated. Its only mutation is injecting a
  receiver into a Call *in place* (not node replacement), so it uses the existing
  visitor with a mutating Expr callback; `error` is captured in state and
  surfaced via `Stop`. Also dropped two dead struct fields (`pkg`, `currentComp`).
- `lower/lambda.go` — left as-is. Its statement traversal already routes through
  `walkPackage`; what remains hand-rolled (`liftLambdas`/`rewriteExpr`) is
  context-dependent node *replacement* (pushing capture frames as it descends).
  A generic rewrite walker wouldn't remove that complexity — the hard part is the
  frame/capture management, not the traversal — so forcing it through one would
  add indirection at real risk to closure correctness. `analyzeCaptures` (a
  read-only capture analysis) is a lower-value candidate that could migrate later.

**Also:** `ir.WalkExprs`/`WalkStmts` are marked `//go:fix inline` (Deprecated) so
`go fix` can rewrite the remaining bool-adapter call sites to the `Inspect*` API
automatically.

### §2.5 Minor overlaps (LOW) — remaining

- `codegen/platform/html/html.go` `literalToJS` keeps its own scalar-literal
  switch overlapping `javascript.translateIRLiteral` — a second source of truth
  (drift risk), while `exprToJS` already delegates to `TranslateIRLiteral`. Not
  identical, so it needs a careful look rather than a mechanical hoist.
- `ir/convert.go` `formatFloat` vs `optimize/consteval.go` `floatToStr` — different quirks, not trivially unifiable.

---

## Caveat for future dead-code sweeps

`deadcode` cannot see through SNGL `go://` imports. Anything in `docs/`,
`docs/lookup/`, `internal/docsite/`, and `examples/*/session.go` it flags is very
likely reached from `.sngl` (`website.sngl`, `internal/docui/*.sngl`) — verify
against `.sngl` usage before deleting. Other false-positive classes for this repo:
`pkg/{go,js,kotlin}/*` runtime packages (imported by *generated* code, never the
compiler), `internal/playground` (WASM/js target), and the `sngl.go` public API.
