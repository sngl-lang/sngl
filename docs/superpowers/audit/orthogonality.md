# SNGL Orthogonality Audit

Findings on places where the Platform × Language matrix is coupled, where
language- or platform-specific logic has leaked into generic stages, or where
features are unevenly implemented. Ranked roughly by severity.

---

## Severity: High

### 1. Bubbletea reaches into HTML via `LookupPlatform("html")`

`codegen/platform/bubbletea/snapshot.go:33,150`:

```go
htmlPlat := codegen.LookupPlatform("html")
hs, ok := htmlPlat.(htmlSnapshotter) // private interface { SnapshotHTML(...) }
```

Bubbletea's Snapshot pipeline renders the TUI to ANSI, converts to an
HTML page, then asks the html platform to screenshot that HTML.

**Problem.** Cross-platform direct dependency by name. No registry
abstraction. If html were not registered (WASM builds? minimal builds?)
bubbletea's Snapshot silently breaks. The `htmlSnapshotter` interface
is private to bubbletea but its implementation lives in html.

**Direction.** Move the "HTML byte-buffer → PNG" capability behind a
named, registered service (or just register the rod-based screenshotter
as a top-level `codegen.HTMLScreenshotter` interface). Bubbletea's
`SnapshotText` doesn't need html at all — only `Snapshot` does.

---

## Severity: Medium

### 3. `ExprScope` is a mutable junk-drawer that grows test-runner hacks

`codegen/codegen.go:22-65`. Test-only fields polluting the everyday
codegen scope:

- `RawFieldAccess map[string]bool` — bypasses Export-name capitalization
  for Go test files in the same package as Model.
- `MethodFields map[string]bool` — gtk4 nilable refs surfaced as methods.
- `IdentRewrites map[string]string` — "Android test path to route every
  component-level var through a hoisted state object".

**Problem.** The "production" codegen path and the "test" codegen path
share one scope struct with conditional fields. The doc comments on each
field describe a specific test-runner workaround, not a general concept.

**Direction.** Split into `ExprScope` (general) and `TestEmitScope` (or
have testlower compose its own scope decoration via a sub-interface).
This will also force the question of why test emission needs to
diverge from production emission at all.

---

### 4. Multiple optional Platform interfaces with overlapping intent

`codegen/codegen.go` declares:

- `TestRunner` (149), `TestProber` (159)
- `PreviewStyler` (192)
- `Snapshotter` (199), `TextSnapshotter` (206)
- `BatchSnapshotter` (221), `BatchTextSnapshotter` (227)
- `Runner` (234), `LangRunner` (241), `Builder` (251)
- `MutationModelEmitter` (335), `RenderModelEmitter` (345)
- `MutationCompilerFactory` (354), `RenderCompilerFactory` (360)

12 optional capability interfaces, several with batch variants.

**Problem.** Hard to discover the surface a platform must implement
to "be complete". `docs/targets.go:138-147` already has a table
generator that tries to display this — a sign the matrix is large and
sparse. `Runner` vs `LangRunner` is split because exec lives in either
the platform or the language and there's no clear rule. `Snapshotter`
vs `BatchSnapshotter` is performance plumbing leaking into the
interface boundary.

**Direction.** Collapse into 2–3 interfaces (e.g. `Testable`, `Previewable`,
`Snapshotable`) where each interface optionally takes a `Batch` request
type that defaults to looping the single-shot path. Move `*CompilerFactory`
into a `NewCompilation()` returning a struct that implements whichever
emitter the platform needs.

---

### 5. WASM extern bridge convention vs ES-import convention is encoded ad-hoc

`codegen/codegen.go:52-59` describes `BundledNativePkgs` as a JS-only
convention; html's `jsbundle.go:16-29` populates the set from
`scheme == "js"` paths. The WASM path (`html/wasmbridge.go:31-37`)
uses a *different* convention — a `window.__sngl_externs` global.

**Problem.** Two unrelated import-binding strategies (ES module
namespace import vs WASM extern bag) are decided platform-side based on
URI scheme, but the language translator emits the call sites blindly
according to `BundledNativePkgs` (and a different code path for WASM).
The decision logic for "is this a bundle target or a WASM extern?"
lives partly in html, partly in javascript's translator.

**Direction.** Lift the binding-strategy decision into the IR as a
property on `*ir.NativeImport` (`BindingKind: Esm | WasmExtern | CgoCgo`).
Then the language translator emits a call site based on the IR
property; the platform doesn't need to thread `BundledNativePkgs`
through scope.

---

### 6. The `c` C-FFI receiver is conflated with the Go test-component receiver

`codegen/lang/golang/ircontext.go:400`:

```go
if n.Func.NativePkg == "C" && !strings.HasPrefix(name, "C.") { ... }
```

and `codegen/platform/gtk4/intrinsic_translator.go:155`:

```go
return &ir.Func{NativePkg: "C", NativeName: nativeName, Name: nativeName}
```

Meanwhile `kotlin/testlower.go` uses `id.Name == "c"` as the test
component receiver — same single-character identifier with very
different meaning.

**Problem.** The `"C"` magic string for cgo/CCompiler is encoded as a
`NativePkg` value; if any future stdlib package gets the import alias
`C` it will collide. The kotlin test convention silently shadows.

**Direction (updated 2026-05-23).** Collapse `NativePkg` (and the
other parallel native-tracking fields) into a single `Native any`
smuggle slot on `ir.NativeImport` / `ir.Func`. The language or
platform importer constructs the IR object and owns the concrete
type behind `Native`. Codegen exposes a "standard" C-calling-
convention struct for any C-FFI consumer (cgo, gtk4 intrinsics,
future C bindings) to share, but it's just one of many possible
inhabitants of `Native` — not privileged in the IR. This avoids the
`Kind` enum, lets new schemes self-describe, and frees `"C"` as a
magic string. Held pending design pass; depends on #5 reaching the
same conclusion via a different angle (BindingKind on
NativeImport).

---

### 7. `Caps.NoContext` is conflated with i18n-locale threading ✅ PARTIAL (2026-05-23) — full split deferred

`Caps.NoContext` replaced by two flags that better describe what's
actually being lowered:

- **`StructComponents`** — components compile to structs with methods
  rather than functions/closures. User-declared `context #foo` blocks
  must lower into hidden Vars on each component in Reach(ctx) and
  hidden Params on each user func in Reach(ctx).
- **`StdlibContextParam`** — threads a hidden trailing param through
  every stdlib func in Reach(ctx). Even closure-based component
  targets need this because stdlib funcs (i18n.tr et al.) live
  outside any user closure scope.

`passNoContext` renamed to `passContext`; runs when either flag is
set. Today every former `NoContext` setter sets both flags so
codegen output is unchanged. The split exists so a future migration
to function-shaped JS components (or a context.Context-style
runtime) can flip just one flag off — see "Future ideas" below.

**Remaining open work.** The full split is only latent: every former
`NoContext` setter still sets both `StructComponents` and
`StdlibContextParam`, and `codegen/platform/html/html.go` still forces
`StructComponents` for JS (re-injecting `__ctx_locale` for i18n) rather
than faking context via closures. Flipping one flag off requires the
function-shaped-JS-components migration in F1.

---

### 8. PlatformExtensionBody is platform-specific by string

`internal/lower/platform_extension.go:23` is a generic lowering pass
keyed by the platform identifier string in `Options.Platform`. Stdlib
components register `platform <p> { ... }` bodies in the checker, the
pass picks the matching one.

**Problem.** This is fine for the lowering pass, but it forces every
stdlib component to either (a) be platform-agnostic or (b) declare a
body for every platform. There's no fallback chain, no "html-like"
grouping, no shared body across multiple platforms. Adding a new
platform requires touching every stdlib component that supports the
others.

**Direction.** Introduce platform "families" (e.g. `gowidget` for
{fyne, gtk4, bubbletea}, `web` for {html}) and let `platform <family>`
match any member. Or allow component-body inheritance.

---

## Severity: Low / Cosmetic

### 9. Stub `Description()` strings differ in capitalization

`codegen/lang/golang/golang.go:34`: "Generate Go source..."
`codegen/lang/javascript/javascript.go:22`: "Generate JavaScript source."
`codegen/lang/kotlin/kotlin.go:???`: similar.

`html/html.go:47` is two sentences with no period. Pure presentation
inconsistency.

---

### 10. `intrinsic_translator.go` files duplicate the pattern but only three platforms use it

Present in `html`, `fyne`, `gtk4`. Bubbletea and Android use a
different walker (RenderModel via `BuildRenderModel`). The
`IntrinsicTranslator` interface (`codegen/intrinsic_walker.go:20`) is
the "mutation model" walker; the render model has no equivalent
language-agnostic interface.

**Direction.** Either give `RenderModelEmitter` an analogous walker so
all five platforms share the same IR-walking shape, or document why
the two models can't unify.

---

### 12. Two platforms hard-code Go via `LangRunner` while two go through `lang.LangRunner`

`fyne/run.go:11`, `bubbletea/run.go:11`, `gtk4/run.go:11`, `html/run.go:17`
all do `codegen.LookupLang(cfg.Lang)` and then call `lang.RunDir(...)`
via a type-assert to `codegen.LangRunner`. But `android/run.go` ignores
this entirely and runs `gradlew` directly via exec — even when
`--lang go`. The android-go path is a separate code path.

**Direction.** Decide whether `Runner` (platform) or `LangRunner`
(language) is canonical. `cmd/sngl/run.go:140` already merges
capabilities; pick one entry point.

---

### 13. `none` platform has no `IsLanguageSupported` semantics

`codegen/platform/none/none.go:22`: returns `false` for every language,
yet `RunTests` accepts any LangTranslator (and the test runner is
interpreter-based — independent of lang). Tests that target
`platform=none` work but the model says they shouldn't.

**Direction.** Either make `none` truly lang-agnostic by removing the
language requirement from its API, or have it accept `lang=none` (the
`none.Translator` exists in `codegen/lang/none/none.go`) as the only
supported lang and use it.

---

### 14. `Translator.Capabilities()` on `none` lang returns `lower.Caps{}` — no lowering

`codegen/lang/none/none.go:28`: `Capabilities() lower.Caps { return lower.Caps{} }`.

The `none` lang isn't used by any platform other than html-static and
the `none` platform's interpreter. Yet it claims to consume *everything*
in raw form. In practice the html platform's `Caps` declaration
dominates, so this is moot — but a future consumer pairing `lang=none`
with `platform=none` would skip every lowering pass.

**Direction.** Match `Caps` to what the interpreter actually consumes
(probably all lowerings should still apply pre-interp for consistency).

---

### 15. `EmitCHeader` is the only `CCompiler` interface user

`codegen/codegen.go:323` defines `CCompiler.EmitCHeader`. Only golang
implements it (`ccompiler.go`). It's called from
`fyne/compiler_ir.go:557` and `gtk4/compiler_ir.go` to gate cgo
preamble emission.

**Problem.** Single-method interface that only ever has one implementor
and two callers. Could be a method on the golang translator directly,
called via type-assert at call sites. Or, better, made part of a
broader "native-import emission" interface alongside the WASM path.

---

### 17. `html.html.go` is 4000+ lines and mixes everything

`codegen/platform/html/html.go` has 4000+ lines (line refs above hit
both 532, 763, 3153). It does: option parsing, asset copy, stylesheet
URL rewriting, WASM loader emission, route detection, JS bundling
prep, i18n inlining, native-import alias generation, and per-window
emit. This is the largest single file in the platform tree by a wide
margin.

**Direction.** Already partially split (`i18n.go`, `jsbundle.go`,
`wasmbridge.go`, `routes.go`, `path.go`, `promote.go`). The remaining
giant file still holds the orchestration — break the emit loop into
its own file.

---

### 18. `BatchSnapshotter` adoption is uneven

Only `android/batchsnapshot.go` declares `var _ codegen.BatchSnapshotter`.
Bubbletea has `BatchSnapshot` and `BatchSnapshotText` methods but no
interface assertion. Fyne and html don't implement batching at all.

**Problem.** docsgen (`go tool docsgen`) calls per-doc Snapshot for
the platforms without batch support, paying repeated build cost.

---

### 19. `Pkg().Document` returned from each lang/platform parses its own embedded `.sngl`

`codegen/lang/golang/golang.go:22-26`, `codegen/platform/android/android.go:22-29`,
etc.: each `init()` parses its `*.sngl` source and panics on error. The
boilerplate is identical across all 7 platforms and 3 languages.

**Direction.** A `codegen.MustParse(name, fs.FS) []*ast.Document` helper
that all `init()`s use.

---

### 20. CLAUDE.md says `--lang none` is the default for html, but the static-mode check is by string

`codegen/platform/html/html.go:82,294`:

```go
if req.Lang.LanguageIdentifier() == "none" { ... }
staticMode := req.Lang.LanguageIdentifier() == "none"
```

The decision to be static vs server is "is the lang name the literal
string 'none'". An HTTPCompiler-capable lang automatically activates
routing; a non-HTTPCompiler non-none lang errors. Static mode is
"absence of HTTPCompiler" essentially, but it's spelled positively by
string name.

**Direction.** Treat "static mode" as "lang doesn't implement
HTTPCompiler" — already mostly the predicate. The string check is
redundant.

---

## Future Ideas

These extend the audit fixes above. Not blocking; record here so the
direction isn't forgotten.

### F1. Migrate JS components from struct-shape to function-shape

The `StructComponents` lowering (item #7) is currently set by every
backend, including html (JS). JS components conceptually CAN compile
to factory functions whose nested child components close over the
parent's locals — that would let user-declared contexts ride along as
closure-captured variables without the hidden-Var rewrite.

The blocker is html.go's emit path, which has been built around the
post-`passNoContext` IR shape (synthesized `__ctx_*` Vars on every
component in Reach). Migrating means either teaching html.go to read
`ContextRead`/`ContextProvider` directly or keeping the lowering on
but giving it a closure-friendly output. Either way it's a project,
not a refactor.

Until then, html keeps `StructComponents: true` for parity with the
Go-desktop platforms and pays the same per-component-field cost
that StdlibContextParam alone would not require.

### F2. context.Context as a SNGL lowering target

Once Go/Kotlin keep struct components (or move away from them), it's
worth exploring a lowering pass that introduces a generic context
storage type — `context.Context`-shaped, or a SNGL-defined
`Ctx<T>` — and threads it as a first argument through Reach(ctx)
funcs and component methods. One hidden param replaces every
per-context hidden param/Var.

Pros: smaller ABI surface as the number of contexts grows; aligns
with Go's idiomatic propagation pattern.

Cons: every read becomes a `ctx.Value(key)` lookup instead of a
direct field/local read; deopts on hot paths unless the lowering
pass specializes; per-call-site overhead.

A pass-time SNGL-defined `Ctx` keyed by intrinsic ID might be the
right shape — avoids `any` boxing while keeping the type-safety
SNGL already gives. Held until measurement shows the per-context
fanout is actually a problem.

---

## Cross-Cutting Observations

- The compiler has good separation through `lower.Caps`, `ir.*`,
  `codegen.AnalyzeCommon`. The platform/language matrix is the place
  where it falls down: every Go-emitting platform reimplements the
  same import bookkeeping, the same i18n detection, the same `goImports`
  map.
- Test emission is the second-worst offender: per-lang `testlower`
  files with different conventions, plus a `RawFieldAccess`/`MethodFields`/
  `IdentRewrites` flotilla on `ExprScope` that exists only to support
  test runners.
- The "Interface fragmentation" issue (12 optional interfaces on
  PlatformGenerator) reflects real capability variation but obscures
  what a "complete" platform looks like. A capability matrix table
  generated from the registry would help.
- `none` platform + `none` lang form a usable interpreter path but are
  marked as if they support nothing. The runner is in
  `internal/interp` (not audited here) and bypasses codegen entirely.
