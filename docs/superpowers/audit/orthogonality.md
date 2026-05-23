# SNGL Orthogonality Audit

Findings on places where the Platform × Language matrix is coupled, where
language- or platform-specific logic has leaked into generic stages, or where
features are unevenly implemented. Ranked roughly by severity.

---

## Severity: High

### 1. Three independent i18n-call IR walkers (duplicate logic) ✅ RESOLVED (2026-05-23)

Collapsed onto the new shared `ir.WalkExprs` (item #17) + the new
shared `codegen/i18n.IsCall` (item #12). html/i18n.go and
android/i18n.go's `hasI18nCalls` are now four-line wrappers around
`ir.WalkExprs`. golang's `PackageUsesI18n` likewise — the old
`funcUsesI18n` / `stmtUsesI18n` / `exprUsesI18n` trio (~170 LOC) is
deleted. The Go-side `i18n.zero`/`one`/`other` plural-key Select
detection stays in `PackageUsesI18n` because it's a Go-runtime-
specific check; everything else flows through the shared helpers.

---



- `codegen/lang/golang/golang.go:447` (`PackageUsesI18n`, with helpers
  `funcUsesI18n`, `stmtUsesI18n`, `exprUsesI18n` at 492–577).
- `codegen/platform/html/i18n.go:23` (`hasI18nCalls`, plus a private
  `walkPkgExprs` IR-visitor at 47–278).
- `codegen/platform/android/i18n.go:18` (`hasI18nCalls`, with its own
  duplicate visitor — the file's `visitExpr/visitStmt` panic messages still
  read `android.i18n.visitExpr`/`visitStmt`).

Each visitor open-codes the same IR walk and asks a per-language
`IsI18nCall` helper (`codegen/lang/golang/golang.go:406`,
`codegen/lang/javascript/javascript.go:113`, `codegen/lang/kotlin/kotlin.go:33`)
whether a qualified call is an i18n intrinsic. The three string-set bodies
are identical except for spelling (`i18n.tr`, `i18n.numberInt`, ...).

**Problem.** Every new i18n entry point has to be added in four places
(stdlib + three IsI18nCall lists). The walkers also diverge in node
coverage — `html/i18n.go`'s walker panics on unhandled nodes, the
android one panics too, but the golang one falls through silently.

**Direction.** Define `i18n.IsCall(*ir.Call) bool` once (drive it off
intrinsic name, which the checker already attaches) and use the existing
`codegen.WalkLowered` / shared `treewalk.go` infrastructure. Move
`SnglI18nImportPath` into the language translator interface or onto a
shared `i18nRuntime` capability so platforms don't reach into
`golang.SnglI18nImportPath` (currently referenced from fyne, bubbletea,
gtk4).

---

### 2. Three near-identical IRContext implementations ✅ RESOLVED (2026-05-23)

Hoisted recursive Expr/Stmt dispatch + body-walk into `codegen/irwalk`.
Each IRContext now implements `irwalk.Renderer`; the walk skeleton
(EvalExpr type-switch, EvalStmt, For/If body+indent recursion,
EvalMutTarget) lives once in irwalk. Three IRContexts kept for the
lang-owned subgraphs (Call/Conversion/Lambda) and leaf rendering.

---

- `codegen/lang/golang/ircontext.go` (1244 lines, `GoIRContext`)
- `codegen/lang/javascript/ircontext.go` (825 lines, `JsIRContext`)
- `codegen/lang/kotlin/ircontext.go` (885 lines, `KtIRContext`)

All three implement the same shape: `EvalExpr(ir.Expr) string`,
`EvalStmt(ir.Stmt) []string`, `evalFor`, `evalIf`, `evalLiteral`,
`evalIdent`, `evalCall`, `evalNativeCall`, `evalNamespaceCall`,
`evalTypeMethodCall`, `evalConversion`, `evalLambda`, `evalCallArgs`,
`evalMutTarget`, `WithLocal`. The walks are mechanical IR traversals;
only the leaf rendering differs.

**Problem.** A bug fix to the IR walker has to land three times and gets
caught by three different test suites with different coverage. The
`LangTranslator` interface still has `WriteExpr` / `WriteStmt` /
`WriteType` stubs (`codegen.go:107-109`) that all three return
`"not yet implemented"` for (`golang.go:50`, `javascript.go:29`,
`kotlin.go:104`) — there is a half-finished v2 API that was never
unified with these IRContexts.

**Direction.** Either finish the `WriteExpr`/`WriteStmt` migration and
delete the IRContexts, or hoist the walking skeleton into `codegen/`
(parallel to `codegen.WalkLowered`) and have each language plug in only
the rendering hooks (string-format leaves, name mangling).

---

### 3. `LangTranslator` interface has a giant abandoned v2 surface ✅ RESOLVED

`WriteExpr`/`WriteStmt`/`WriteType` v2 stubs removed when `FileEmitter`
landed. Each lang owns file-level emission via `NewFileEmitter`; the
remaining `LangTranslator` surface is the live API (no dead methods).

---



`codegen/codegen.go:98-120`:

```
WriteExpr(...) error  // stub
WriteStmt(...) error  // stub
WriteType(...) error  // stub
GenerateIdentifier(*ir.Ident) string
Eval(ir.Expr) string  // "eval not implemented"
TranslateIRExpr(...) string  // actual API
TranslateIRMutation(...) []string  // actual API
TranslateIRLiteral(...) string  // actual API
TypeToNative(string) string
ExportName(string) string
```

`Eval` and the three `Write*` methods are stubs in **every** language
(see #2). Either the migration is dead or it never started.

**Problem.** New language implementations don't know which methods are
real and which are placeholders. The interface is large enough to obscure
its actual contract.

**Direction.** Delete `Eval`, `WriteExpr`, `WriteStmt`, `WriteType` (or
finish them — but they're inert today). Decide whether
`GenerateIdentifier` and `ExportName` are the same concept.

---

### 4. Bubbletea reaches into HTML via `LookupPlatform("html")`

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

### 5. JS has no test lowering; android-go is opaque ✅ RESOLVED-as-documented (2026-05-23)

Confirmed test-execution is platform-driven, not lang-driven, and
keep the existing asymmetry rather than force a single interface:

- **html (any lang)** runs tests via CDP/rod through
  `codegen/platform/html/testing.go` — exercises the real JS bundle
  in a headless browser, no per-lang testlower needed.
- **gtk4, fyne, bubbletea (go)** use `golang.LowerTestFunc` and
  delegate to per-platform `RunTests` Go-test-harness scaffolding.
- **android (kotlin)** uses `kotlin.LowerTestFunc` and
  `android/runtests.go`'s Robolectric harness.
- **android (go)** generates a golib bridge for use inside an
  Android app — it is not itself a runnable app, so
  `android/runtests.go:62` correctly errors when `lang != "kotlin"`.

Unifying these behind a single `LangTranslator.LowerTestFunc` would
obscure the difference between in-process testlowers and
browser/emulator-driven harnesses. The matrix is data-driven via
`codegen.Snapshotter` / `codegen.TestRunner` type assertions in
`docs/targets.go`, so the docs site reflects this automatically.

---



`codegen/lang/golang/testlower.go` exists (191 lines).
`codegen/lang/kotlin/testlower.go` exists (305 lines).
JS has no testlower; the html platform's tests run inside CDP/rod
through `codegen/platform/html/testing.go` instead.

**Problem.** The matrix is uneven: tests for the same SNGL source render
through different translation paths depending on the target. The
android+go combination (`android.go:38` says `SupportedLangs = ["kotlin","go"]`)
also lacks a `go`-specific test lowering — `android/runtests.go:62`
hard-errors when `lang != "kotlin"`.

**Direction.** Either unify test lowering into a single
`LangTranslator.LowerTestFunc` interface (so all langs implement it the
same way) or document that test-runner is a platform-platform-and-lang
specific thing and remove it from `none` and android-go's `SupportedLangs`.

---

### 6. Three independent `goImports` maps tracking which Go stdlib packages were used ✅ RESOLVED (2026-05-23)

Shared `golang.BaseImports(pkg) []BaseImport` collects native imports
+ sngl-i18n runtime in one place. gtk4, fyne, bubbletea seed their
`goImports` from this. Android-go's gogen emits a golib bridge only —
not a BaseImports user.

---



`codegen/platform/bubbletea/compiler_ir.go:93,118,135,141,165,201,242,266,297`
`codegen/platform/fyne/compiler_ir.go:80,86,149,179,184`
`codegen/platform/gtk4/compiler_ir.go:172,175,180,181,245,337`
`codegen/platform/android/gogen_ir.go` similarly

Each Go-emitting platform sprinkles `info.goImports["time"] = ""` /
`"fmt" = ""` / `"strings"` across its analyzer.

**Problem.** Whether a Go file needs `"time"` should be a fact derivable
from the lowered IR (it has timers, or uses `i18n.date`, etc.), not
something every platform recomputes. Today fyne uses
`map[string]bool`, bubbletea uses `map[string]string` (alias!), gtk4
uses `map[string]bool` — even the data type differs.

**Direction.** Put the bookkeeping on the `golang.Translator` itself
(or on `codegen.ExprScope.NativeImports` which already exists for this
purpose) and have all four platforms read out a single
`golang.RequiredImports(pkg) []ImportSpec` derived from the IR.

---

### 7. Cross-platform i18n manifest reading is duplicated ✅ RESOLVED (2026-05-23)

New `codegen/i18n.LoadManifest(projectFS, projectDir)` is the single
disk/FS reader; it compact-marshals the JSON so callers can embed it
inline without bloat. html and android consume it directly. The
Go-desktop platforms (bubbletea, fyne, gtk4) now ship the manifest
embedded into the binary via a generated `i18n_embed.go` sidecar
plus `//go:embed i18n.manifest.json` plus an `init()` calling the
new `pkg/go/i18n.SetManifestBytes`. Installed Go-desktop binaries no
longer silently mis-translate when there's no
working-directory manifest beside the executable. Shared emit helper:
`golang.EmitI18nManifestEmbed(sink, pkgName, projectFS, projectDir)`.

---



`codegen/platform/html/i18n.go:331-371` reads/marshals `i18n.manifest.json`.
`codegen/platform/android/i18n.go:55+` does the same and emits a different
output location (`app/src/main/assets/...`).

The bubbletea, fyne, gtk4 platforms don't ship i18n manifests at all,
even though `golang.PackageUsesI18n` is checked and the runtime import
is added (`bubbletea/compiler_ir.go:140-141`,
`fyne/compiler_ir.go:85-86`, `gtk4/compiler_ir.go:174-175`). So i18n
"works" for android+kotlin and html+js, "compiles but probably
mis-translates" for the three Go desktop platforms because the runtime
loads no manifest.

**Direction.** Move manifest discovery into a shared helper
(`codegen.LoadI18nManifest(projectFS, projectDir) ([]byte, error)`) and
have each platform decide *where* to drop the file. Or, more
aggressively: have the Go i18n runtime accept a manifest blob set at
init time, and have every Go-emitting platform inject it as a
generated `init()` constant.

---

## Severity: Medium

### 8. `SupportedLangs() []string` AND `IsLanguageSupported(Language) bool` both exist

`codegen/codegen.go:124-125` (PlatformGenerator):

```
ir.Platform                          // contributes IsLanguageSupported
SupportedLangs() []string            // separate
```

`ir/ir.go:434-440` shows `IsLanguageSupported(Language) bool` on
`ir.Platform`. Every platform implements both, and they always agree
(bubbletea/fyne/gtk4 hard-code `"go"` in both; android lists
`["kotlin","go"]` and a switch returning `id == "kotlin" || id == "go"`).

**Problem.** Two sources of truth — `none.go:22` returns `false` for
all languages but `SupportedLangs() == nil`. They could disagree
without any compile error.

**Direction.** Drop `IsLanguageSupported`; derive it from
`SupportedLangs()` in a registry helper. Or vice versa, but in either
direction don't keep both.

---

### 9. Capabilities are declared imperatively per platform, easy to under-declare

`codegen/platform/html/html.go:54-55`:

```go
return lower.Caps{NoAsyncReactive: true, NoContext: true, NoImplicitRecv: true,
	NoInlineComponents: true, NoReactivity: true, NoStdlibWrappers: true}
```

`codegen/platform/android/android.go:51`:

```go
return lower.Caps{NoContext: true, NoInlineComponents: true}
```

`codegen/platform/fyne/fyne.go:58`:

```go
return lower.Caps{NoContext: true, NoReactivity: true, NoDeclarative: true,
	NoStdlibWrappers: true, NoInlineComponents: true}
```

bubbletea and gtk4 differ from fyne by one or two flags despite
solving the same problem. Caps is essentially a free-form bag — a
platform that forgets to set `NoTernary` gets ternaries the codegen
doesn't know how to render.

**Problem.** There's no static check that the platform's actual codegen
matches its capability declaration. Coverage is by panic-in-codegen.

**Direction.** Have each platform declare *positive* capabilities (i.e.
"I can render `*ir.Ternary` natively") and `lower.Caps` is derived as
the complement. Then a missing declaration falls back to "lower this"
which is safe. Or generate a compile-time test that walks lowered IR
and asserts no high-level node shapes survive that the platform
hasn't acknowledged.

---

### 10. `ExprScope` is a mutable junk-drawer that grows test-runner hacks

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

### 11. Multiple optional Platform interfaces with overlapping intent

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

### 12. Per-language `IsI18nCall` is a static string-set, redundant with `Func.Intrinsic` ✅ RESOLVED (2026-05-23)

New `codegen/i18n` package exposes `IsCall(*ir.Call)` (matches both
`Func.Receiver == "i18n"` pre-inline and `Func.Intrinsic ∈
ir.I18nIntrinsics` post-inline) and `IsIntrinsic(name)`. The three
per-lang `IsI18nCall` string-set switches are gone; `IsIntlIntrinsic`
shims remain on the language translators as one-line delegations for
existing internal call sites. Single source of truth: `ir.I18nIntrinsics`.

---



`codegen/lang/golang/golang.go:406`
`codegen/lang/javascript/javascript.go:113`
`codegen/lang/kotlin/kotlin.go:33`

All three switch on the same set of strings: `"i18n.tr"`, `"i18n.format"`,
etc. — i.e. the qualified `Receiver.Name` shape, not the intrinsic.
Meanwhile `IsIntlIntrinsic` (`javascript.go:129`, similar in others)
switches on the intrinsic shape *after* `InlinePure` has run.

**Problem.** Two parallel detection schemes (pre-lowering vs
post-lowering); each lang re-encodes the same i18n function set.

**Direction.** Mark i18n stdlib funcs with a `tags: "i18n"` field on
the `*ir.Func` at stdlib load time, and have one shared helper check
it. The set lives in exactly one place: the stdlib declaration in
`lib/i18n.sngl`.

---

### 13. `golang.SnglI18nImportPath` is referenced cross-platform via the lang package ✅ RESOLVED (2026-05-23)

Absorbed by `golang.BaseImports` (item #6): the i18n-runtime gate now
lives inside the helper, so platforms no longer reference
`golang.SnglI18nImportPath` directly. (`grep` across non-golang
packages returns no hits.)

---



`codegen/platform/bubbletea/compiler_ir.go:141`
`codegen/platform/fyne/compiler_ir.go:86`
`codegen/platform/gtk4/compiler_ir.go:175`

All three Go-target platforms import the constant from
`codegen/lang/golang`. JS has the parallel `javascript.SnglI18nImportPath`
(`javascript.go:108`).

**Problem.** Platform code knows the Go-runtime import path for a stdlib
package. If we add another stdlib runtime package (date/time, http
client, fs) every platform will grow another `golang.SnglXxxImportPath`
reference.

**Direction.** Expose stdlib-runtime imports through the language
translator generically: `golang.StdlibImports(pkg *ir.Package) []string`
that returns every required `git.duckfam.us/jonathan/sngl/pkg/go/*`
import. Platform just splices the slice into its import block.

---

### 14. `kotlin.testlower` hard-codes `c` as the component receiver ✅ RESOLVED (2026-05-23)

`LowerTestFunc` now walks `fn.Params` for component-typed entries,
captures the actual receiver name(s) into a `compRecvs map[string]bool`
threaded through all helpers, and uses the first one as the declared
local. Test bodies that name their receiver anything other than `c`
now lower correctly. Falls back to `c` when no component param is
present (assertion-only tests).

---



`codegen/lang/kotlin/testlower.go:60,251,259,263,293`:

```go
if id, ok := sel.Operand.(*ir.Ident); ok && id.Name == "c" { ... }
```

The `c.<field>` / `c.<id>.@event()` pattern is encoded as a literal
string match on `c`. The Go testlower handles this generically through
`scope.RawFieldAccess` (`golang/testlower.go:34-42`) — by marking
component-typed params at declaration time.

**Problem.** Test bodies that name their receiver anything other than
`c` silently produce wrong Kotlin. The convention should be enforced
or detected, not assumed.

**Direction.** Have Kotlin testlower walk params and seed a
`compRecvs map[string]bool` mirroring Go's approach.

---

### 15. Asymmetric snapshot support across platforms ✅ RESOLVED (2026-05-23)

gtk4 now implements `Snapshotter` + `BatchSnapshotter` via
`codegen/platform/gtk4/snapshot.go`. The harness wraps the top-level
window in a `GdkPaintable`, renders it through `GskCairoRenderer`
(no GdkSurface required), and saves the resulting `GdkTexture` to
PNG. Still requires `$DISPLAY` / `$WAYLAND_DISPLAY` for
`gtk_window_present`; a follow-up will spawn weston-headless / Xvfb
when neither is set so snapshots become fully self-contained.

---

- html: yes (cdp/rod, via `cdprunner.go`)
- bubbletea: yes (via html bridge, see #4)
- fyne: yes (`snapshot.go`)
- android: yes (emulator-based, `snapshot.go` + `batchsnapshot.go`)
- gtk4: **no** — `codegen/platform/gtk4/` has no `snapshot.go`.
- none: n/a

`TextSnapshotter`/`BatchTextSnapshotter` exists only on bubbletea.

**Problem.** The matrix is sparse and the inconsistency is hidden behind
type assertions. `cmd/sngl/preview.go` and the docsgen pipeline silently
skip platforms that don't implement Snapshotter.

**Direction.** At minimum, document the expected interface set per
platform (the `docs/targets.go` table is a start). For gtk4 specifically,
either implement Snapshot or have `IsLanguageSupported`/`SupportedLangs`
acknowledge "preview only" status.

---

### 16. WASM extern bridge convention vs ES-import convention is encoded ad-hoc

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

### 17. `walkPkgExprs` in html/i18n.go duplicates `ir.Strip` / `treewalk.go` ✅ RESOLVED (2026-05-23)

New `ir.WalkExprs(pkg, fn)` in `ir/walkexprs.go` covers every
expression-bearing node in a package (consts, vars, funcs,
components, timers, windows, plus statement-containers like
If/For/PlatformFilter/SlotInst/ErrorBoundary/ContextProvider). The
panic-on-unknown convention is preserved. The two ~230-line
`walkPkgExprs` copies in html/i18n.go and android/i18n.go are
deleted; both platforms now call `ir.WalkExprs`. Adding a new IR
node now updates exactly one site.

---



`codegen/platform/html/i18n.go:47-278` is a 230-line IR visitor that
panics on every unhandled node. `codegen/treewalk.go` and `ir/strip.go`
already exist for IR walks. Android's i18n.go has its own copy too.

**Problem.** Every IR shape addition (e.g. `ir.ErrorBoundary`,
`ir.ContextProvider`, `ir.ContextRead`) requires updating these
copy-pasted walkers in lockstep, or unrelated codegen crashes at
runtime.

**Direction.** A single generic `ir.WalkExprs(pkg, fn)` that callers
parameterize. The panic-on-unknown convention is reasonable; centralize
it.

---

### 18. The `c` C-FFI receiver is conflated with the Go test-component receiver

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
magic string. Held pending design pass; depends on #16 reaching the
same conclusion via a different angle (BindingKind on
NativeImport).

---

### 19. `Caps.NoContext` is conflated with i18n-locale threading ✅ PARTIAL (2026-05-23) — full split deferred

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

---



The `NoContext` lowering rewrites context reads into hidden parameters.
Every Go-target platform sets it because Go has no implicit threading.
But `codegen/platform/html/html.go:55` *also* sets `NoContext` even
though JS could fake context via closures — and html therefore has to
specially re-inject `__ctx_locale` for i18n in `html.go`'s emit path.

**Problem.** A platform that wants ordinary context support but not
locale-as-context can't get it. The behavior is all-or-nothing.

**Direction.** Either split `NoContext` into per-context-name flags, or
build context support directly into each language's IR walker so the
lowering pass isn't needed.

---

### 20. PlatformExtensionBody is platform-specific by string

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

### 21. Stub `Description()` strings differ in capitalization

`codegen/lang/golang/golang.go:34`: "Generate Go source..."
`codegen/lang/javascript/javascript.go:22`: "Generate JavaScript source."
`codegen/lang/kotlin/kotlin.go:???`: similar.

`html/html.go:47` is two sentences with no period. Pure presentation
inconsistency.

---

### 22. `intrinsic_translator.go` files duplicate the pattern but only three platforms use it

Present in `html`, `fyne`, `gtk4`. Bubbletea and Android use a
different walker (RenderModel via `BuildRenderModel`). The
`IntrinsicTranslator` interface (`codegen/intrinsic_walker.go:20`) is
the "mutation model" walker; the render model has no equivalent
language-agnostic interface.

**Direction.** Either give `RenderModelEmitter` an analogous walker so
all five platforms share the same IR-walking shape, or document why
the two models can't unify.

---

### 23. `pkg/go/i18n/`, `pkg/js/i18n/`, `pkg/kotlin/i18n/` are the only runtime packages

Only one stdlib package (`i18n`) has runtime support. `lib/` has
`components.sngl`, `functions.sngl`, `types.sngl`, `units.sngl` — none
of these have a `pkg/<lang>/` runtime. So today the three-language
runtime layout is bespoke for i18n; the pattern is asserted by CLAUDE.md
but un-tested by repetition.

**Direction.** Either build another runtime package (date/time
formatting, http client) to validate the pattern, or document that
i18n is a one-off until further notice.

---

### 24. Two platforms hard-code Go via `LangRunner` while two go through `lang.LangRunner`

`fyne/run.go:11`, `bubbletea/run.go:11`, `gtk4/run.go:11`, `html/run.go:17`
all do `codegen.LookupLang(cfg.Lang)` and then call `lang.RunDir(...)`
via a type-assert to `codegen.LangRunner`. But `android/run.go` ignores
this entirely and runs `gradlew` directly via exec — even when
`--lang go`. The android-go path is a separate code path.

**Direction.** Decide whether `Runner` (platform) or `LangRunner`
(language) is canonical. `cmd/sngl/run.go:140` already merges
capabilities; pick one entry point.

---

### 25. `none` platform has no `IsLanguageSupported` semantics

`codegen/platform/none/none.go:22`: returns `false` for every language,
yet `RunTests` accepts any LangTranslator (and the test runner is
interpreter-based — independent of lang). Tests that target
`platform=none` work but the model says they shouldn't.

**Direction.** Either make `none` truly lang-agnostic by removing the
language requirement from its API, or have it accept `lang=none` (the
`none.Translator` exists in `codegen/lang/none/none.go`) as the only
supported lang and use it.

---

### 26. `Translator.Capabilities()` on `none` lang returns `lower.Caps{}` — no lowering

`codegen/lang/none/none.go:28`: `Capabilities() lower.Caps { return lower.Caps{} }`.

The `none` lang isn't used by any platform other than html-static and
the `none` platform's interpreter. Yet it claims to consume *everything*
in raw form. In practice the html platform's `Caps` declaration
dominates, so this is moot — but a future consumer pairing `lang=none`
with `platform=none` would skip every lowering pass.

**Direction.** Match `Caps` to what the interpreter actually consumes
(probably all lowerings should still apply pre-interp for consistency).

---

### 27. `EmitCHeader` is the only `CCompiler` interface user

`codegen/codegen.go:323` defines `CCompiler.EmitCHeader`. Only golang
implements it (`ccompiler.go`). It's called from
`fyne/compiler_ir.go:557` and `gtk4/compiler_ir.go` to gate cgo
preamble emission.

**Problem.** Single-method interface that only ever has one implementor
and two callers. Could be a method on the golang translator directly,
called via type-assert at call sites. Or, better, made part of a
broader "native-import emission" interface alongside the WASM path.

---

### 28. Bubbletea's `runtests_js.go` / `fyne/runtests_js.go` / `gtk4/runtests_js.go` / `html/testing_js.go` are stub files for WASM

Each file is identical in shape — a no-op `RunTests` for the WASM
playground build. Four copies of essentially the same stub.

**Direction.** A single `codegen/wasmstub.go` with `//go:build js` that
provides a `NoopTestRunner` embeddable struct. Each platform embeds it
instead of duplicating.

---

### 29. `html.html.go` is 4000+ lines and mixes everything

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

### 30. `BatchSnapshotter` adoption is uneven

Only `android/batchsnapshot.go` declares `var _ codegen.BatchSnapshotter`.
Bubbletea has `BatchSnapshot` and `BatchSnapshotText` methods but no
interface assertion. Fyne and html don't implement batching at all.

**Problem.** docsgen (`go tool docsgen`) calls per-doc Snapshot for
the platforms without batch support, paying repeated build cost.

---

### 31. `Pkg().Document` returned from each lang/platform parses its own embedded `.sngl`

`codegen/lang/golang/golang.go:22-26`, `codegen/platform/android/android.go:22-29`,
etc.: each `init()` parses its `*.sngl` source and panics on error. The
boilerplate is identical across all 7 platforms and 3 languages.

**Direction.** A `codegen.MustParse(name, fs.FS) []*ast.Document` helper
that all `init()`s use.

---

### 32. CLAUDE.md says `--lang none` is the default for html, but the static-mode check is by string

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

The `StructComponents` lowering (item #19) is currently set by every
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
