# Go + Kotlin Translator Unification (Super Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the legacy `ExprScope`-based `translate_ir.go` in BOTH the golang and kotlin languages, routing every caller through `GoIRContext` / `KtIRContext` (irwalk visitor), then **delete `codegen.ExprScope` entirely** by folding its remaining fields into `codegen.ExprCtx`. One context type, one translation path per language — the same modernization the JS unification did, finished across the codebase.

**Architecture:** The JS unification (see `docs/superpowers/specs/2026-06-03-js-translator-unification-design.md`) established the template: all IR→target translation goes through a `*IRContext` (irwalk `Renderer`); platforms feed it via `WalkLowered` + an `IntrinsicTranslator`; `ExprScope` is removed. golang/kotlin already have feature-rich `GoIRContext`/`KtIRContext` (the platforms — fyne, gtk4, bubbletea, android — use them exclusively). The ONLY remaining legacy users are: golang `http.go` (HTTP route action handlers) and `testlower.go`; kotlin `testlower.go` and `translate_ir_test.go`. These rely on `ExprScope`'s test/http-only fields (`ContextVar`, `RawFieldAccess`, `MethodFields`, `IdentRewrites`). We move those fields onto `ExprCtx`, teach the `*IRContext`s to honor them, migrate the callers, delete both `translate_ir.go` files and `ExprScope`.

**Tech Stack:** Go; SNGL compiler under `codegen/`. Verification is `go test` (assertion + golden); a per-language parity harness mirrors the JS `TestParityGolden`.

**Reference:** JS template spec/plan at `docs/superpowers/specs/2026-06-03-js-translator-unification-design.md` and `docs/superpowers/plans/2026-06-03-js-translator-unification.md`.

**Working location:** Directly on `main` (project convention — no worktree). Commit frequently.

**Build/test commands:**
- `go build ./...`
- `go test ./codegen/lang/golang/... ./codegen/lang/kotlin/...`
- Platform sanity: `go test ./codegen/platform/fyne/ ./codegen/platform/gtk4/`
- Do NOT run `go tool verify` — its `go generate` step hits a pre-existing, unrelated golang-codegen bug (generated `model.go` missing a `strings` import; `bubbletea`/`android` `TestFixtures` fail identically at baseline). Use targeted `go test`.

**Survey findings this plan is built on:**
- `GoIRContext` is nearly complete; one real gap — `Binary` doesn't handle multi-base-unit struct math (legacy `translateMultiBaseUnitBinary`). It already collects imports structurally via `RequireImport`.
- `KtIRContext` is feature-complete and gap-free; android uses it exclusively. Kotlin import tracking is a deferred no-op (unchanged by this plan).
- legacy callers: golang `http.go:104` (`t.TranslateIRMutation`, scope has `ContextVar`), golang `testlower.go` (uses `RawFieldAccess`/`MethodFields`), kotlin `testlower.go:320,325` (empty `ExprScope`), kotlin `translate_ir_test.go`.
- The `LangTranslator` interface already lost `TranslateIRExpr`/`TranslateIRMutation` (JS phase). golang/kotlin keep them as **concrete** methods only because their own legacy code calls them; both go away here.

---

## File Structure

**Modified:**
- `codegen/exprctx.go` — `ExprCtx` gains `ContextVar`, `RawFieldAccess`, `MethodFields`, `IdentRewrites`; `Clone` copies them.
- `codegen/lang/golang/ircontext.go` — `Binary` gains multi-base-unit expansion; `evalIdent`/`Select` honor `RawFieldAccess`/`MethodFields`/`IdentRewrites`/`ContextVar` (porting legacy behaviors).
- `codegen/lang/golang/http.go` — build a `GoIRContext` from an `ExprCtx{ContextVar:"r.Context()"}`; replace `t.TranslateIRMutation`.
- `codegen/lang/golang/testlower.go` — build `ExprCtx` + `GoIRContext`; replace `translateIRExpr`/`translateIRMutation`.
- `codegen/lang/golang/golang.go` — remove concrete `TranslateIRExpr`/`TranslateIRMutation`.
- `codegen/lang/kotlin/ircontext.go` — honor `IdentRewrites`/`RawFieldAccess`/`MethodFields` if kotlin testlower needs them (verify; kotlin may need fewer).
- `codegen/lang/kotlin/testlower.go` — route through `KtIRContext`.
- `codegen/lang/kotlin/kotlin.go` — remove concrete `TranslateIRExpr`/`TranslateIRMutation`.
- `codegen/codegen.go` — delete the `ExprScope` type (last).

**Created:**
- `codegen/lang/golang/parity_golden_test.go` + `testdata/parity/*.{sngl,golden}` — golang route-mode + test-lower output baseline.
- `codegen/lang/kotlin/parity_golden_test.go` + `testdata/parity/*.{sngl,golden}` — kotlin test-lower output baseline.

**Deleted (final phases):**
- `codegen/lang/golang/translate_ir.go`, `codegen/lang/golang/translate_ir_test.go`
- `codegen/lang/kotlin/translate_ir.go`, `codegen/lang/kotlin/translate_ir_test.go`
- `codegen.ExprScope` (type + all fields).

---

## Phase 0: Parity baselines

Capture byte-identical baselines of the outputs that the legacy paths produce, BEFORE touching them. These are the parity bars.

### Task 0.1: Identify the legacy-path outputs

- [ ] **Step 1: Enumerate what the legacy paths emit**

Run:

```bash
grep -rn "TranslateIRMutation\|translateIRExpr\|translateIRMutation" codegen/lang/golang codegen/lang/kotlin | grep -v "_test.go" | grep -v "func "
```

Expected: golang `http.go:104`, golang `testlower.go` (several), kotlin `testlower.go:320,325`. These produce: (a) golang HTTP action-handler bodies (route mode), (b) golang generated test files, (c) kotlin generated test files.

- [ ] **Step 2: Find the existing harnesses that exercise them**

```bash
ls codegen/lang/golang/testlower_test.go codegen/lang/kotlin/testlower_test.go
grep -rln "Action\|CompileHTTP\|route" codegen/lang/golang/*_test.go cmd/sngl/testdata/*.txt
```

Note the helpers/fixtures that drive test-lowering and route mode (e.g. `LowerTestFile`, html `--lang go` golden in `cmd/sngl/testdata`).

### Task 0.2: Golang parity harness

**Files:**
- Create: `codegen/lang/golang/parity_golden_test.go`
- Create: `codegen/lang/golang/testdata/parity/*.sngl` (+ `.golden`)

- [ ] **Step 1: Curated fixtures** covering the legacy surface: a component with test funcs that (a) read `c.field` raw (RawFieldAccess), (b) call `c.method()` (MethodFields), (c) do unit-struct arithmetic (the `Binary` gap — e.g. two `Measurement` values added), plus an HTTP route with a POST action mutating state (ContextVar path). Model fixtures on existing `testdata/*.sngl`; verify each with `go run ./cmd/sngl dump checked <file>`.

- [ ] **Step 2: Harness** mirroring `codegen/platform/html/parity_golden_test.go`: for each fixture, run it through the relevant legacy producer (test-lower for the component; route-mode for the window) and diff a committed `.golden`. `SNGL_UPDATE_GOLDEN=1` regenerates. Reuse `LowerTestFile`/route helpers found in 0.1.

```bash
SNGL_UPDATE_GOLDEN=1 go test ./codegen/lang/golang/ -run TestParityGolden
go test ./codegen/lang/golang/ -run TestParityGolden -v   # must PASS without update
go build ./...
```

- [ ] **Step 3: Commit** fixtures + harness + goldens.

```bash
git add codegen/lang/golang/parity_golden_test.go codegen/lang/golang/testdata/parity/
git commit -m "test(golang): parity baseline for translator unification"
```

### Task 0.3: Kotlin parity harness

Same as 0.2 for kotlin (test-lower only — kotlin has no route mode). Fixtures: a component with test funcs exercising method calls, field reads, i18n calls (the kotlin legacy cases in `translate_ir_test.go`). Commit.

**After every later task: the relevant `TestParityGolden` MUST stay green and byte-identical. A diff means a real behavioral change — fix the emission, never regenerate goldens unless the change is intended and justified.**

---

## Phase 1: ExprCtx absorbs ExprScope's remaining fields

Add the test/http-only fields to `ExprCtx` so callers can build one context type, and teach the `*IRContext`s to honor them (porting the legacy `translateIRIdent`/`Select`/native-context behaviors).

### Task 1.1: Add fields to ExprCtx

**Files:** `codegen/exprctx.go`

- [ ] **Step 1: Add the fields + Clone coverage**

```go
// In ExprCtx struct, after EventVar:
	ContextVar     string            // expr for native context args (e.g. "r.Context()")
	RawFieldAccess map[string]bool   // idents whose Select bypasses method/getter lowering (test recvs)
	MethodFields   map[string]bool   // idents whose recv.<field> is a zero-arg method call
	IdentRewrites  map[string]string // bare ident → replacement, regardless of scope
```

Update `Clone()` to carry `ContextVar` (scalar) and `maps.Clone` the three maps (nil-safe). Update `NewExprCtx` if it should pre-allocate (leave nil — honor-sites must nil-check).

- [ ] **Step 2: Build + existing tests green** (`go build ./... && go test ./codegen/...` for the non-platform packages). No behavior change yet (nothing reads the new fields).

- [ ] **Step 3: Commit** `feat(codegen): ExprCtx carries ContextVar/RawFieldAccess/MethodFields/IdentRewrites`.

### Task 1.2: GoIRContext honors the new fields (test-first)

**Files:** `codegen/lang/golang/ircontext.go`, `ircontext_test.go`

Port each legacy behavior from `golang/translate_ir.go` `translateIRIdent`/`translateIRSelect`/native-call-context into `GoIRContext`, reading from `gc.Ctx`. For EACH (IdentRewrites, RawFieldAccess, MethodFields, ContextVar-as-native-context-arg):

- [ ] **Step 1: Read the legacy behavior** in `golang/translate_ir.go` (exact output for each field's effect).
- [ ] **Step 2: Write a failing characterization test** in `ircontext_test.go` asserting `GoIRContext` produces that exact output when the corresponding `Ctx` field is set.
- [ ] **Step 3: Implement** the branch in `evalIdent`/`Select`/`evalCall` reading `gc.Ctx.<field>`.
- [ ] **Step 4: Pass + parity green** (`go test ./codegen/lang/golang/...`).
- [ ] **Step 5: Commit** per behavior (e.g. `feat(golang): GoIRContext honors RawFieldAccess`).

(Repeat the 5 steps per field. Keep each commit small/bisectable, mirroring JS Phase 1.)

### Task 1.3: KtIRContext honors the needed fields

Kotlin testlower uses an EMPTY `ExprScope{}`, so it may need only `IdentRewrites`/method-field handling, if anything.

- [ ] **Step 1: Diff** what kotlin `translate_ir.go` `translateIRIdent`/`translateIRTypeMethodCall` do that `KtIRContext` doesn't, for the shapes testlower routes (calls, method calls).
- [ ] **Step 2:** If a gap exists, add a failing test + implement (as 1.2). If none (KtIRContext already covers the empty-scope cases), record that and skip.
- [ ] **Step 3: Commit** if changed.

---

## Phase 2: Golang port

### Task 2.1: Close the multi-base-unit Binary gap (test-first)

**Files:** `codegen/lang/golang/ircontext.go` (`Binary`), `ircontext_test.go`

- [ ] **Step 1: Failing test** — build an `*ir.Binary` adding two multi-base-unit struct operands; assert `GoIRContext.Binary` emits the component-wise struct literal (match legacy `translateMultiBaseUnitBinary` exactly: `Unit{Field: l.Field op r.Field, ...}`, and plain `(l op r)` for `==`/`!=`).

```bash
go test ./codegen/lang/golang/ -run TestGoBinary_MultiBaseUnit -v   # FAIL
```

- [ ] **Step 2: Implement** — in `Binary`, before the default, replicate `translateMultiBaseUnitBinary` using `n.Left`/`n.Right` for type checks and the passed `left`/`right` strings for operands. The helpers (`multiBaseUnitOperand`, `isMultiBaseUnitType`, `UnitBases`, `ExportName`, `irBinaryOp`/`binaryOpStr`) are package free-functions — reuse them. Reconcile `binaryOpStr` vs `irBinaryOp` so output is byte-identical to legacy.

- [ ] **Step 3: Pass + parity green.** Commit `feat(golang): multi-base-unit binary expansion in GoIRContext.Binary`.

### Task 2.2: Migrate http.go to GoIRContext

**Files:** `codegen/lang/golang/http.go`

- [ ] **Step 1: Implement** — in `writeRouteHandler`, replace `scope := &codegen.ExprScope{ContextVar:"r.Context()"}` + `t.TranslateIRMutation(s, scope)` with:

```go
	ctx := codegen.NewExprCtx(req.Pkg) // or the pkg available on req; see HTTPRequest fields
	ctx.ContextVar = "r.Context()"
	gc := golang /*self*/ .NewIRContext(ctx)
	...
	for _, line := range gc.EvalStmt(s) { ... }
```

Confirm `HTTPRequest` exposes the package/analysis needed to build an `ExprCtx`; if not, thread it. Collect imports via `gc.Imports()` (replacing/augmenting the existing `collectGoImports` + `nativeMustOK` handling — note GoIRContext wraps error-returning calls inline via `maybeWrapErrorReturn`, so the `nativeMustOK` helper path may become unnecessary; verify against parity golden).

- [ ] **Step 2: Parity golden (route mode) byte-identical** + `go build ./...`. The route fixture from 0.2 guards this. Investigate any diff (esp. the `nativeMustOK` vs inline-wrap difference — if output legitimately changes, this is the one place to call it out and justify, then regenerate that golden with explicit note).

- [ ] **Step 3: Commit** `refactor(golang): route http.go action handlers through GoIRContext`.

### Task 2.3: Migrate testlower.go to GoIRContext

**Files:** `codegen/lang/golang/testlower.go`

- [ ] **Step 1: Implement** — replace the `ExprScope{LocalVars, RawFieldAccess, MethodFields}` construction with `codegen.NewExprCtx(pkg)` + set `RawFieldAccess`/`MethodFields`/`Locals` on it, and replace `translateIRExpr`/`translateIRMutation`/`lowerTestStmt` internals to drive a `GoIRContext` (`gc.EvalExpr`/`gc.EvalStmt`). Keep the test-specific lowering (assert/event-trigger/setContext desugaring) in testlower; only the expression/statement rendering moves to `GoIRContext`. Bind params/loop vars via `gc.WithLocal`.

- [ ] **Step 2: testlower_test.go + parity golden byte-identical** (`go test ./codegen/lang/golang/...`). Fix emission to match goldens.

- [ ] **Step 3: Commit** `refactor(golang): route test lowering through GoIRContext`.

### Task 2.4: Delete golang legacy

**Files:** `codegen/lang/golang/translate_ir.go`, `translate_ir_test.go`, `golang.go`

- [ ] **Step 1: Confirm no remaining refs**

```bash
grep -rn "translateIRExpr\|translateIRMutation\|TranslateIRMutation\|TranslateIRExpr" codegen/lang/golang/ | grep -v "_test.go"
```

Port any surviving helper still referenced (e.g. `translateIRLiteral` if `TranslateIRLiteral` delegates to it) into a kept file (`jshelpers.go` analog or inline), exactly as JS did.

- [ ] **Step 2: Delete** `git rm codegen/lang/golang/translate_ir.go codegen/lang/golang/translate_ir_test.go` (port any still-needed legacy-only test cases to `ircontext_test.go` first). Remove `golang.Translator.TranslateIRExpr`/`TranslateIRMutation` methods (keep `TranslateIRLiteral`).

- [ ] **Step 3:** `go build ./... && go test ./codegen/lang/golang/... ./codegen/platform/fyne/ ./codegen/platform/gtk4/`. All green.

- [ ] **Step 4: Commit** `refactor(golang): delete legacy translate_ir.go path`.

---

## Phase 3: Kotlin port

### Task 3.1: Migrate kotlin testlower.go to KtIRContext

**Files:** `codegen/lang/kotlin/testlower.go`

- [ ] **Step 1: Implement** — replace the two `translateIRExpr(e, &codegen.ExprScope{})` fallbacks (lines ~320, 325) with a `KtIRContext` built from a minimal `codegen.NewExprCtx(pkg)` (plus any field Task 1.3 found necessary). Route Call/fallback shapes through `kc.EvalExpr`.

- [ ] **Step 2: testlower_test.go + kotlin parity golden byte-identical.** Commit `refactor(kotlin): route test lowering through KtIRContext`.

### Task 3.2: Port/replace kotlin legacy tests

**Files:** `codegen/lang/kotlin/translate_ir_test.go`, `ircontext_test.go`

- [ ] **Step 1:** For each behavior asserted in `translate_ir_test.go` (i18n tr/plural, namespace/type-method calls) not already covered in `ircontext_test.go`, add the `KtIRContext` equivalent test.
- [ ] **Step 2:** Run `go test ./codegen/lang/kotlin/...` — ported tests pass (these behaviors exist on the modern path). Commit `test(kotlin): port legacy translate_ir behaviors to KtIRContext tests`.

### Task 3.3: Delete kotlin legacy

**Files:** `codegen/lang/kotlin/translate_ir.go`, `translate_ir_test.go`, `kotlin.go`

- [ ] **Step 1: Confirm no refs** (as 2.4), port any still-needed helper (`translateIRLiteral`).
- [ ] **Step 2: Delete** the two files; remove `kotlin.Translator.TranslateIRExpr`/`TranslateIRMutation` (keep `TranslateIRLiteral`).
- [ ] **Step 3:** `go build ./... && go test ./codegen/lang/kotlin/... ./codegen/platform/android/...` (android may be slow/environmental — at minimum the kotlin package + `go build` must be green; note any pre-existing android failure).
- [ ] **Step 4: Commit** `refactor(kotlin): delete legacy translate_ir.go path`.

---

## Phase 4: Delete ExprScope

### Task 4.1: Remove the type

**Files:** `codegen/codegen.go`, plus `none.Translator` if it still references it.

- [ ] **Step 1: Confirm zero references**

```bash
grep -rn "ExprScope" codegen/ --include=*.go | grep -v "_test.go"
```

Expected: none outside the definition. If any test files still construct `ExprScope`, migrate them to `ExprCtx`.

- [ ] **Step 2: Delete** the `ExprScope` struct (and the `NeededHelpers` comment references). Remove any now-dead helpers it uniquely supported.

- [ ] **Step 3:** `go build ./...` succeeds (compiles every platform — proves no caller remains).

- [ ] **Step 4: Commit** `refactor(codegen): delete ExprScope; ExprCtx is the sole translation context`.

### Task 4.2: Final verification

- [ ] **Step 1:** `go build ./...`; `go test ./codegen/lang/... ./codegen/platform/fyne/ ./codegen/platform/gtk4/ ./codegen/`; both `TestParityGolden`s byte-identical. (html unaffected but run it too.)
- [ ] **Step 2:** `grep -rn "translate_ir\|ExprScope" codegen/ --include=*.go | grep -v _test.go` → empty.
- [ ] **Step 3:** Update the JS spec's "Follow-up" note (or add a short note in this plan) marking the cross-language port done. Commit.

---

## Self-review notes (for the executor)

- **Parity bar:** the two `TestParityGolden` harnesses + `testlower_test.go` + fyne/gtk4 suites are the byte-identical guard. The one expected-legitimate output change is golang route mode IF the `nativeMustOK` helper is replaced by GoIRContext's inline error-wrap (Task 2.2) — call it out explicitly and regenerate only that golden with justification; everything else must be byte-identical.
- **Highest risk:** Task 2.3 (golang testlower's RawFieldAccess/MethodFields semantics) and Task 2.2 (http.go ContextVar + import/error-helper reconciliation). Run the full golang package + fyne/gtk4 after each.
- **Platforms are already modern** (ExprCtx + *IRContext); this plan must not change their output — fyne/gtk4/bubbletea/android suites green throughout proves it.
- **Order matters:** ExprCtx fields (Phase 1) before migrating callers (Phases 2–3) before deleting ExprScope (Phase 4).
- **Not in scope:** routing bubbletea/android through WalkLowered+IntrinsicTranslator (they render directly and that's fine); adding kotlin import tracking (deferred no-op); any RenderModel `Requires` mechanism (JS-helper-specific).
