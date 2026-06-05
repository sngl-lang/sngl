# Front/Back-End Placement + Server-Side Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decide front/back-end placement of SNGL code by transitive import language (with `html.frontend`/`html.backend` overrides, GitLab #27), and execute back-end handlers as server-side HTTP routes (PRG + server-side re-render + per-session state) via the language-agnostic `HTTPCompiler` seam.

**Architecture:** A new html placement pass classifies each expression/handler frontend|backend (default: backend iff it transitively uses a non-`js://` import; `html.frontend`/`html.backend` intrinsics override). Back-end handlers/bindings become a language-agnostic per-route model (HTML skeleton + IR-expr holes, state vars, logical mutation IR) that the target language compiles into server code (`State` struct, session store, `renderRoute`, GET/POST handlers) through its `*IRContext`. The html platform stays language-agnostic; Go is the only server language implemented in v1.

**Tech Stack:** Go; SNGL compiler under `codegen/`, `internal/checker/`, `lib/`. Verification: `go test` (assertion + golden) and a CDP browser test.

**Reference spec:** `docs/superpowers/specs/2026-06-04-impure-imports-server-actions-design.md`. Issue #27.

**Working location:** Directly on `main` (project convention — no worktree). Commit frequently.

**Build/test commands:**
- `go build ./...`
- `go test ./codegen/... ./internal/checker/...`
- Single fixture check: `go run ./cmd/sngl dump checked <file>`
- For a `go://`-importing fixture, build the CLI to a temp bin and run there (the fixture has its own `go.mod`): `go build -o /tmp/snglbin ./cmd/sngl` then `/tmp/snglbin generate --platform html --lang go -o out <fixture>`.
- Do NOT run `go tool verify` (pre-existing unrelated golang `go generate` `strings`-import bug; `bubbletea`/`android` `TestFixtures` fail/hang at baseline — not this work). Use targeted `go test`.

---

## File Structure

**Created:**
- `lib/html.sngl` — `html.frontend<T>` / `html.backend<T>` stdlib identity directives.
- `codegen/platform/html/placement.go` — placement analysis (expr/handler → frontend|backend; import-scheme lookup; directive recognition; build-error guards).
- `codegen/platform/html/placement_test.go`.
- `codegen/platform/html/rendermodel.go` — build the language-agnostic per-route RenderModel (skeleton + holes), StateVars, ServerActions from the reactive analysis.
- `codegen/lang/golang/httprender.go` — Go emission of `State`, session store, `renderRoute`, GET/POST handlers from the RenderModel (kept separate from `http.go`'s route scaffolding).
- `codegen/lang/golang/testdata/parity/server_action/` — fixture (+ `go.mod`, `api/api.go`) and golden `server.go`.
- `pkg/go/session/session.go` — tiny per-session store runtime imported by generated servers.

**Modified:**
- `internal/checker/stdlib.go` (or the intrinsic table) — register `html.frontend`/`html.backend` as intrinsics so they survive inlining and carry identity semantics.
- `codegen/codegen.go` — widen `HTTPRoute`/`HTTPAction`/`HTTPRequest`: add `RenderModel`/`Hole`/`StateVar`, refine `HTTPAction.Mutations`; retire the baked `RenderHTML` callback.
- `codegen/platform/html/routes.go` — use placement to gate `collectActions`; build the RenderModel; the non-`HTTPCompiler`-target guard.
- `codegen/platform/html/html.go` / `wasmbridge.go` — gate `collectWASMPackages` on frontend-forced usage (WASM becomes opt-in).
- `codegen/lang/golang/http.go` — call into `httprender.go`; drop the old broken action-body emission.

**Deleted:** none.

---

## Phase 0: Baselines

### Task 0.1: No-backend route parity baseline

Guards that pages with **no** back-end placement keep byte-identical route output.

**Files:**
- Create: `codegen/lang/golang/testdata/parity/route_client_only/` (`app.sngl`, `go.mod` if needed) + `server.golden`
- Reuse: `codegen/lang/golang/parity_golden_test.go` (existing harness from the prior project)

- [ ] **Step 1: Add a client-only route fixture** — a `window`/route whose handlers are pure-client (SNGL state mutation only, no `go://`), e.g. a counter button `@click { count = count + 1 }` and `text(value="count {count}")`. Verify `go run ./cmd/sngl dump checked <app.sngl>` is clean.

- [ ] **Step 2: Generate + commit the baseline golden**

Run: `SNGL_UPDATE_GOLDEN=1 go test ./codegen/lang/golang/ -run TestParityGolden` then confirm `go test ./codegen/lang/golang/ -run TestParityGolden` passes without update.

- [ ] **Step 3: Commit**

```bash
git add codegen/lang/golang/testdata/parity/route_client_only/
git commit -m "test(golang): client-only route parity baseline"
```

**After every later task: `TestParityGolden` must stay byte-identical for `route_client_only` (no-backend output unchanged). The existing `route_post_action` golden will change once the bug is fixed (Phase 4) — that change is the point, and is asserted by a new test there.**

---

## Phase 1: `html.frontend` / `html.backend` directives

### Task 1.1: Declare the directives in stdlib

**Files:**
- Create: `lib/html.sngl`
- Test: `internal/checker/` (add to an existing stdlib/checker test file, e.g. `checker_test.go`)

- [ ] **Step 1: Write the failing test** — that the checker resolves `html.frontend`/`html.backend` as generic identity funcs (arg type flows through).

```go
func TestHtmlPlacementDirectivesResolve(t *testing.T) {
	src := `
component main {
    var n = 0
    text(value="v {html.frontend(n)}")
    text(value="w {html.backend(n)}")
}`
	pkg, diags := checkSource(t, src) // use the package's existing check helper
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Error())
		}
	}
	_ = pkg
}
```

(Use whatever in-package helper checks a source string; mirror an existing checker test's setup.)

- [ ] **Step 2: Run → fails** (unknown `html` namespace / func).

`go test ./internal/checker/ -run TestHtmlPlacementDirectivesResolve -v` → FAIL.

- [ ] **Step 3: Create `lib/html.sngl`**

```sngl
// Placement directives (GitLab #27). Identity functions that instruct the html
// code generator where the wrapped expression runs. No effect on value or type.

// Force v to run client-side (front-end). For code imported from non-js://
// languages this compiles the dependency to a front-end WASM asset instead of a
// server call. No effect for static-site builds. Build fails if the logic cannot
// run in the browser or depends on server-side values.
func html.frontend<T>(v T) => v

// Force v to run server-side (back-end). A func value is generated as an HTTP
// route; a constant expression is generated as a lazy-loaded static file.
func html.backend<T>(v T) => v
```

Confirm `lib/html.sngl` is picked up by the embed in `lib/lib.go` (it embeds `*.sngl`; verify the glob covers it — if `lib.go` lists files explicitly, add it).

- [ ] **Step 4: Run → passes.** `go test ./internal/checker/ -run TestHtmlPlacementDirectivesResolve -v` → PASS. Also `go build ./...`.

- [ ] **Step 5: Commit** `feat(stdlib): html.frontend/html.backend placement directives`.

### Task 1.2: Make the directives intrinsics that survive inlining

The directives are identity expression-body funcs, so `InlinePure` would fold `html.frontend(v)` → `v`, erasing the directive before the html platform sees it. Mark them as intrinsics (`HtmlFrontend`/`HtmlBackend`) so the call node is preserved and recognizable, and ensure non-html targets treat them as pass-through identity (emit just `v`).

**Files:**
- Modify: `internal/checker/stdlib.go` (intrinsic assignment) and/or `ir/intrinsics.go` (intrinsic table)
- Test: `internal/checker/` + `codegen/lang/javascript/ircontext_test.go` (pass-through)

- [ ] **Step 1: Failing test** — a checked `html.frontend(x)` call retains `Func.Intrinsic == "HtmlFrontend"` and is NOT inlined/folded away after optimize+lower.

```go
func TestHtmlDirectiveIsPreservedIntrinsic(t *testing.T) {
	src := `component main { var n = 0  text(value="x {html.frontend(n)}") }`
	pkg := checkOptimizeLower(t, src) // parse→check→optimize→lower helper
	found := false
	walkCalls(pkg, func(c *ir.Call) {
		if c.Func != nil && c.Func.Intrinsic == "HtmlFrontend" {
			found = true
		}
	})
	if !found {
		t.Fatal("html.frontend call was erased; must survive as an intrinsic for placement analysis")
	}
}
```

(Reuse/adapt an existing optimize+lower test helper and an IR call-walker; if none, a minimal recursive walk over `pkg` exprs.)

- [ ] **Step 2: Run → fails** (directive inlined away / no intrinsic id).

- [ ] **Step 3: Implement** — register `html.frontend`→`HtmlFrontend`, `html.backend`→`HtmlBackend` in the intrinsic mapping the checker uses (`stdlib.go` sets `fn.Intrinsic = id` for matched stdlib funcs; add these two ids). Ensure the optimizer/inliner does NOT inline funcs with a non-empty `Intrinsic` (verify InlinePure already skips intrinsics; if not, add the guard). Mark them `Purity: PurityPure` so they don't block folding of their *argument* but the call node itself stays (the intrinsic identity is what's preserved).

- [ ] **Step 4: Pass-through for non-html targets** — add an intrinsic emitter (or inline rule) so JS/Go/Kotlin render `html.frontend(v)`/`html.backend(v)` as just the translated `v` (identity). Add a JS `ircontext_test.go` case: `EvalExpr(html.frontend(x))` → `x`'s translation. Register via `codegen.RegisterIntrinsic(lang, "HtmlFrontend", identityEmitter)` for each lang (emitter returns `translate(args[0])`).

- [ ] **Step 5: Run → passes**; `go test ./internal/checker/ ./codegen/lang/...`; `go build ./...`.

- [ ] **Step 6: Commit** `feat(codegen): html placement directives as preserved identity intrinsics`.

---

## Phase 2: Placement analysis (html, language-agnostic)

### Task 2.1: Import-scheme lookup

**Files:**
- Create: `codegen/platform/html/placement.go`
- Test: `codegen/platform/html/placement_test.go`

- [ ] **Step 1: Failing test**

```go
func TestFuncImportScheme(t *testing.T) {
	// go:// func
	goFn := &ir.Func{NativePkg: "example.com/api"}
	if s := funcImportScheme(pkgWithImport(t, "go", "example.com/api"), goFn); s != "go" {
		t.Fatalf("got %q want go", s)
	}
}
```

(Build a tiny `*ir.Package` whose `Imports` has a `go://` native import matching the func's `NativePkg`. Look at how existing code maps a `Func.NativePkg` back to its import scheme — `codegen.SplitScheme(imp.AST.Path)` / `isBundledImport` in `codegen/lang/javascript/jshelpers.go` is the reference.)

- [ ] **Step 2–4: Implement `funcImportScheme(pkg, fn) string`** — find the `ir.Import` whose `Native.ImportPath == fn.NativePkg`, return its scheme via `codegen.SplitScheme(imp.AST.Path)`. Returns "" for non-native funcs. Make the test pass.

- [ ] **Step 5: Commit** `feat(html): import-scheme lookup for placement`.

### Task 2.2: Expression placement classifier

**Files:** `codegen/platform/html/placement.go` (+ test)

- [ ] **Step 1: Failing tests** for the rules:

```go
func TestExprPlacement(t *testing.T) {
	// bare go:// call → backend
	// bare js:// call → frontend
	// SNGL-only expr → frontend
	// html.frontend(go_call) → frontend
	// html.backend(sngl_expr) → backend
}
```

Write each as a separate assertion building the IR (a `*ir.Call` to a `go://`/`js://` func; an `html.frontend`/`html.backend` wrapper carrying `Func.Intrinsic`).

- [ ] **Step 2: Run → fails.**

- [ ] **Step 3: Implement `exprPlacement(pkg, e) Placement`** (`Placement` = `frontend|backend`):
  - If `e` is a call to `Func.Intrinsic == "HtmlFrontend"` → `frontend` (then recurse into arg only to validate feasibility in Task 6).
  - If `e` is a call to `Func.Intrinsic == "HtmlBackend"` → `backend`.
  - Else: walk `e`; if any subexpression is a call to a func with `funcImportScheme(pkg, fn) != "" && != "js"` → `backend`; else `frontend`.
  - Innermost directive wins for its subtree; outside a directive, the default rule applies.

- [ ] **Step 4: Pass.** `go test ./codegen/platform/html/ -run TestExprPlacement`.

- [ ] **Step 5: Commit** `feat(html): expression placement classifier`.

### Task 2.3: Handler placement

**Files:** `codegen/platform/html/placement.go` (+ test)

- [ ] **Step 1: Failing test** — a handler whose body calls a `go://` func → `backend`; a handler with only local mutations → `frontend`; `html.frontend`-wrapped `go://` call in the body → `frontend`.

- [ ] **Step 2–4: Implement `handlerPlacement(pkg, fn *ir.Func) Placement`** — `backend` iff any statement/expr in `fn.Block` has `exprPlacement == backend`; else `frontend`. Make tests pass.

- [ ] **Step 5: Commit** `feat(html): handler placement classification`.

---

## Phase 3: Widen the HTTPCompiler seam + build the RenderModel

### Task 3.1: RenderModel / Hole / StateVar types

**Files:** `codegen/codegen.go` (+ a focused test in `codegen/`)

- [ ] **Step 1: Add the types**

```go
// StateVar is a component state field surfaced to the server State struct.
type StateVar struct {
	Name string
	Type *ir.Type
}

// HoleKind classifies a dynamic slot in a server-rendered page.
type HoleKind int

const (
	HoleText HoleKind = iota // interpolate Expr (string-coerced)
	HoleAttr                 // attribute value = Expr
	HoleIf                   // reactive if: Cond + Then/Else skeletons
	HoleFor                  // reactive for: Iter + element skeleton
)

// Hole is a dynamic insertion point in a route's HTML skeleton.
type Hole struct {
	Kind HoleKind
	Expr ir.Expr      // text/attr/if-cond/for-iter expression (language-agnostic)
	Attr string       // attribute name (HoleAttr)
	Key  string       // loop var (HoleFor)
	Then *RenderModel // HoleIf/HoleFor nested
	Else *RenderModel // HoleIf
}

// RenderModel is a static HTML skeleton interleaved with holes. Chunks[i] is
// emitted, then Holes[i] (if present), alternating. len(Chunks) == len(Holes)+1.
type RenderModel struct {
	Chunks []string
	Holes  []Hole
}
```

Add to `HTTPRoute`: `Render *RenderModel` and `StateVars []StateVar`. Refine `HTTPAction.Mutations` doc to "logical state mutations only (no DOM/visual statements)". Mark `HTTPRequest.RenderHTML` deprecated (keep temporarily for compile; remove in 3.2 once unused).

- [ ] **Step 2: Build green.** `go build ./...`; `go test ./codegen/`.

- [ ] **Step 3: Commit** `feat(codegen): RenderModel/Hole/StateVar on the HTTP seam`.

### Task 3.2: Build the RenderModel + ServerActions + StateVars in html

**Files:**
- Create: `codegen/platform/html/rendermodel.go` (+ test)
- Modify: `codegen/platform/html/routes.go`

- [ ] **Step 1: Failing test** — for a route with one text binding reading state and one backend `@click`, `buildRenderModel(route)` returns a `RenderModel` whose single `Hole` is `HoleText` carrying the `count` expr, and the route's `StateVars` includes `{count, int}`, and `collectActions` yields one `ServerAction` whose `Mutations` is the logical `count = api.Persist(count+1)` (NO `__n0.*` DOM statement).

- [ ] **Step 2: Run → fails.**

- [ ] **Step 3: Implement**
  - `buildRenderModel`: walk the route window's visual tree; emit static HTML into `Chunks`; for each reactive binding (the same set the existing `CommonAnalysis`/updater logic identifies) push a `Hole` with its `ir.Expr`; for backend handlers, emit the `<form method="post" action="<path>"><input type="hidden" name="_action" value="N">…</form>` wrapper into the static chunks. Reuse the existing static-render walk (`renderIRStmt`) but split at binding points instead of baking values.
  - `StateVars`: from the component's state vars (name + `ir.Type`).
  - `collectActions`: gate on `handlerPlacement == backend` (Task 2.3). Set `Mutations` to the handler's **logical** body — the state-assignment statements, excluding visual/DOM-patch statements (filter by statement shape: keep `ir.Assign`/`ir.CallStmt` to state/`go://`; drop assigns whose target is an `IsElementRef` Select). Confirm against the test that no `__n0` statement remains.

- [ ] **Step 4: Pass.** `go test ./codegen/platform/html/ -run 'TestBuildRenderModel|TestCollectActions'`.

- [ ] **Step 5: Wire routes.go** to populate `route.Render`, `route.StateVars`, and the purity-free `Actions`; remove the `RenderHTML` callback usage. Build green.

- [ ] **Step 6: Commit** `feat(html): build language-agnostic RenderModel + server actions`.

---

## Phase 4: Go server emission

### Task 4.1: Session store runtime

**Files:**
- Create: `pkg/go/session/session.go` (+ `session_test.go`)

- [ ] **Step 1: Failing test** — `Store.Get(w, r)` returns a stable `*T`-backing map entry across requests with the same cookie, and distinct entries for distinct cookies.

- [ ] **Step 2–4: Implement** a minimal generic-free store: a `Store` with `sync.Mutex`, `map[string]any`, cookie name `sngl_session`, `Get(w, r) (id string)` that sets the cookie if absent and returns the id; generated code keys its own `map[string]*State` by id (or the store holds `map[string]*State` via a small generated wrapper). Keep it tiny and pluggable (an interface `SessionStore` with `Load(id)`, `Save(id, v)`).

- [ ] **Step 5: Commit** `feat(pkg/go): per-session store runtime for html backend`.

### Task 4.2: State struct + renderRoute emission

**Files:**
- Create: `codegen/lang/golang/httprender.go` (+ test)

- [ ] **Step 1: Failing test** — `emitState(route)` produces `type appState struct { Count int }` (field per StateVar, Go type via GoIRContext type mapping); `emitRenderRoute(route)` produces a `func ... (s *appState) string` whose body builds the page from `RenderModel.Chunks` and fills the one `HoleText` with the GoIRContext translation of the `count` expr (e.g. `"count " + fmt.Sprint(s.Count)` or the project's coercion), reading `s.Count`. Assert the generated text contains `s.Count` and no `__n0`.

- [ ] **Step 2: Run → fails.**

- [ ] **Step 3: Implement**
  - `emitState`: for each `StateVar`, emit `Name` (exported) + GoIRContext IR→Go type (`IRTypeToGo`).
  - `emitRenderRoute`: build a Go function that `strings.Builder`-concatenates `Chunks[i]` then, for each `Hole`, the GoIRContext-translated expression. Construct a `GoIRContext` whose `ExprCtx` resolves the route's state vars to `s.<Field>` (set `RawFieldAccess`/state resolution so idents render `s.Count`; reuse the field-access machinery added in the prior project, or set the receiver appropriately). Text holes coerce to string (reuse the project's int→string form — check how the client path renders `String(...)`; Go equivalent `fmt.Sprint`/strconv, matching whatever the existing GoIRContext conversion emits). `HoleIf`/`HoleFor` recurse.

- [ ] **Step 4: Pass.** `go test ./codegen/lang/golang/ -run 'TestEmitState|TestEmitRenderRoute'`.

- [ ] **Step 5: Commit** `feat(golang): emit State struct + renderRoute from RenderModel`.

### Task 4.3: GET/POST handlers (PRG) + wire CompileHTTP

**Files:**
- Modify: `codegen/lang/golang/http.go`, `codegen/lang/golang/httprender.go`

- [ ] **Step 1: Update the `server_action` parity fixture** (`codegen/lang/golang/testdata/parity/server_action/`, copy from `route_post_action`) and write/extend a test asserting the generated `server.go`: contains `appState`, `renderRoute`, a GET handler that loads session + writes `renderRoute(s)`, a POST handler that loads session, runs `s.Count = api.Persist(s.Count + 1)`, saves, `http.Redirect(..., 303)`; and does NOT contain undefined `count`/`__n0` or unimported `fmt`. Additionally the test must `go build` the emitted package (use the fixture `go.mod` + a temp build, like the existing route test does).

- [ ] **Step 2: Run → fails** (old broken body).

- [ ] **Step 3: Implement** in `http.go`/`httprender.go`:
  - GET handler: `id := store.Get(w, r); s := load(id); w.Write([]byte(renderRoute(s)))`.
  - POST handler: `id := store.Get(w, r); s := load(id); switch r.FormValue("_action") { case "0": <GoIRContext.EvalStmt(action.Mutations) against s> }; save(id, s); http.Redirect(w, r, route.Path, http.StatusSeeOther)`.
  - Build the `GoIRContext` from an `ExprCtx` resolving state idents to `s.<Field>` (same context as renderRoute). Imports via `gc.Imports()` plus `net/http` and the session pkg.
  - Remove the old `ExprScope`/raw-block action emission.

- [ ] **Step 4: Pass + the emitted Go builds.** `go test ./codegen/lang/golang/ -run TestServerAction`.

- [ ] **Step 5: Regenerate any affected goldens deliberately** — the `server_action` golden is new; `route_client_only` must remain byte-identical. Run full `go test ./codegen/lang/golang/ -run TestParityGolden`.

- [ ] **Step 6: Commit** `feat(golang): server-side action handlers via PRG + session`.

---

## Phase 5: Frontend WASM opt-in

### Task 5.1: Gate client WASM on frontend placement

**Files:** `codegen/platform/html/html.go`, `codegen/platform/html/wasmbridge.go` (+ test)

- [ ] **Step 1: Failing test** — a fixture with a bare `go://` call (no directive) emits **no** WASM (`collectWASMPackages` returns empty / no `<script>` WASM loader), while `html.frontend(go_call(...))` emits the WASM loader + extern bridge.

- [ ] **Step 2: Run → fails** (today WASM is emitted unconditionally for `go://` usage).

- [ ] **Step 3: Implement** — change `collectWASMPackages` (and the extern-bridge collection) to include a `go://` package only for funcs used in a **frontend** placement (i.e. under an `html.frontend` wrapper, per Task 2.2). Bare/backend `go://` usage no longer pulls WASM.

- [ ] **Step 4: Pass.** `go test ./codegen/platform/html/ -run TestFrontendWasmOptIn`. Confirm `route_client_only` + `server_action` goldens unaffected.

- [ ] **Step 5: Commit** `feat(html): client WASM is opt-in via html.frontend`.

---

## Phase 6: Guards / errors

### Task 6.1: Backend on a non-HTTPCompiler target → build error

**Files:** `codegen/platform/html/routes.go` or `html.go` (+ test)

- [ ] **Step 1: Failing test** — `--lang none` (static mode) with a backend handler (`go://` call) → `Generate` returns an error mentioning the window and that it needs a server.

- [ ] **Step 2–4: Implement** — in the static-mode path, if any route has a backend handler/binding (placement analysis) and the lang is not an `HTTPCompiler`, return a clear error. Make the test pass.

- [ ] **Step 5: Commit** `feat(html): error when backend code targets a serverless build`.

### Task 6.2: `html.backend(const-expr)` → not-yet-implemented error

**Files:** `codegen/platform/html/placement.go` or rendermodel (+ test)

- [ ] **Step 1: Failing test** — `html.backend(<const expression>)` (not a func/handler) → clear "const→file backend not yet implemented" build error.

- [ ] **Step 2–4: Implement** the guard (detect `html.backend` wrapping a non-func, non-handler constant expr). Make it pass.

- [ ] **Step 5: Commit** `feat(html): explicit error for deferred const-backend`.

### Task 6.3: `html.frontend` on a server-only value → build error

**Files:** `codegen/platform/html/placement.go` (+ test)

- [ ] **Step 1: Failing test** — `html.frontend(v)` where `v` transitively depends on a backend-only value (e.g. wraps a value produced by a backend handler/route) → clear build error.

- [ ] **Step 2–4: Implement** — when classifying an `html.frontend` wrapper, if its subtree contains a non-`js://` import that cannot be WASM-compiled standalone OR depends on server-only state, error. (v1: error if the wrapped subtree references a non-`js://` import that is also used in a backend context, or references server-only state; keep the rule simple and tested.) Make it pass.

- [ ] **Step 5: Commit** `feat(html): error on impossible html.frontend placement`.

---

## Phase 7: End-to-end + final verification

### Task 7.1: CDP end-to-end

**Files:** `codegen/platform/html/server_action_browser_test.go` (build-tagged `!js`, mirror existing `*_browser_test.go`)

- [ ] **Step 1: Write the test** — build the `server_action` fixture's server, run it, load the page (CDP/go-rod), assert initial render shows `count 0`; submit the form; assert re-render shows `count 1`; with a second cookie jar, assert an independent counter (per-session isolation).

- [ ] **Step 2: Run → passes** (or is skipped if no browser; ensure it runs in this env, mirroring existing browser tests). Fix until green.

- [ ] **Step 3: Commit** `test(html): e2e server action PRG + per-session isolation`.

### Task 7.2: Final verification

- [ ] **Step 1:** `go build ./...`; `go test ./codegen/... ./internal/checker/...`; both `TestParityGolden`s (`route_client_only` byte-identical; `server_action` correct); fyne/gtk4 green (they use GoIRContext): `go test ./codegen/platform/fyne/ ./codegen/platform/gtk4/`.

- [ ] **Step 2:** Generate the original `route_post_action`-style example via the CLI and confirm the emitted `server.go` **compiles** and has no undefined `count`/`__n0`/`fmt` (the original bug, fixed).

- [ ] **Step 3:** Update the spec status to Implemented; mention issue #27 can be closed. Commit.

---

## Self-review notes (for the executor)

- **Parity bar:** `route_client_only` byte-identical throughout; `server_action` is the new correct output; fyne/gtk4 green proves the shared GoIRContext/seam changes didn't disturb other Go consumers.
- **Highest risk:** (a) keeping the directives alive through optimize/inline (Task 1.2 — verify InlinePure skips intrinsics); (b) the RenderModel split of static-vs-hole in `buildRenderModel` (Task 3.2) reusing the existing render walk; (c) resolving state idents to `s.<Field>` in `renderRoute`/POST (Task 4.2/4.3) — reuse the field-access/`ExprCtx` machinery from the prior translator-unification work.
- **Language-agnostic invariant:** nothing in `codegen/platform/html/` may emit Go/session/render specifics — only RenderModel/StateVars/ServerActions cross the seam. Grep the html package for `"net/http"`, `"s.Count"`, session strings → must be absent.
- **Coercion parity:** text-hole string coercion in Go (`renderRoute`) should match the client renderer's semantics for the same binding; pin it with the golden and the `go build` of the emitted server.
- **Not in scope:** const→JSON lazy backend; non-Go server languages; htmx fragments; WASM tree-shaking for mixed packages.
