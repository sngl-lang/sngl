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

### §2.4 ~10 hand-rolled `ir.Stmt` traversal switches → `ir.WalkStmts` (architectural)

At least ten independent `switch s.(type)` copies over `ir.Stmt` each re-list the
statement variants and end in `panic("unhandled stmt %T")`: `iterate.go`,
`html/rendermodel.go`, `html/placement.go`, `checker/pointsto.go`,
`checker/purity.go`, `optimize/interpret.go`, `optimize/shake.go`,
`lower/lambda.go`, `lower/normalize_method_calls.go`, `fuzz_test.go`. The
canonical `ir.WalkStmts`/`WalkExprs` (`ir/walkexprs.go`) is used in only 2 places.
**Real hazard:** each copy must be updated when a new `ir.Stmt` variant is added;
a missed one panics. Not a mechanical delete — the copies carry slightly
different per-node side effects, so this is a careful, deliberate pass.

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
