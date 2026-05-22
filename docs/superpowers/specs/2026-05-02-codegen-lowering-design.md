# Codegen Lowering Layer — Design

**Date:** 2026-05-02
**Status:** Shipped (internal/lower/)

## Summary

Introduce an `internal/lower/` package that runs between `optimize` and `codegen`. Lowering rewrites high-level SNGL IR constructs (toggle, ternary, computed vars, reactivity, declarative visual trees, etc.) into simpler primitive forms when the target platform or language declares it can't natively express them. Capabilities are declared per platform and per language via a `Capabilities() lower.Caps` interface method; the merged caps drive which lowering passes run. The result is much simpler downstream codegen: each platform/language only handles the IR shapes it can natively emit.

## Motivation

Today every platform reimplements similar transforms — HTML and Fyne both build their own dataflow analysis to wire reactivity into mutations; bubbletea and Android Compose both consume the rich IR directly. As more platforms (winforms, fyne v2, HaxeUI, etc.) are added, the duplication compounds. Capability-driven lowering moves these transforms into one place, lets new platforms opt out of features they don't support by setting flags rather than writing transform code, and keeps codegen narrow.

The other forcing function is the imminent imperative-model platforms (winforms, .NET, fyne v2). They want a stream of `create_node()` / `update_node()` IR calls, not a visual tree with reactive bindings. Without lowering, each such platform re-derives that stream from scratch.

## Three Platform Models

Lowering supports three distinct platform consumption models:

1. **Reactive** (Vue, Svelte, React, Compose, BubbleTea) — consumes SNGL IR mostly unchanged. Framework handles diffing. Caps mostly zero.
2. **Declarative-initial + imperative-updates** (HTML, Fyne (today), Android XML, HaxeUI) — consumes IR with constant visual nodes plus injected updater statements bound to mutations. Caps: `NoReactivity`.
3. **Purely imperative** (winforms, .NET, fyne v2) — consumes IR as a flat stream of create / update / delete calls; no visual tree at all. Caps: `NoReactivity` + `NoDeclarative` + `NoTimer`.

Languages declare orthogonal caps (e.g., `NoTernary`, `NoLambda`) regardless of platform. Caps merge field-wise via OR.

## Architecture

### Pipeline

```
parse → check → optimize → lower → optimize → codegen
```

Lowering is sandwiched between two optimize passes. The first removes dead code and folds constants on rich IR; the second cleans up artifacts of lowering (e.g., a `NoToggle`-rewritten `x = !x` may become a constant assignment after second-pass folding when `x` is provably const).

### Package layout

```
internal/lower/
  lower.go         // Lower(pkg, caps, opts) error; pass registry + ordered execution
  caps.go          // Caps struct, Merge, String
  toggle.go        // pass NoToggle
  ternary.go       // pass NoTernary
  lambda.go        // pass NoLambda
  unit.go          // pass NoUnit
  enum.go          // pass NoEnum
  computed.go      // pass NoComputed
  reactivity.go    // pass NoReactivity (largest — owns dataflow analysis)
  timer.go         // pass NoTimer
  declarative.go   // pass NoDeclarative
  *_test.go        // each pass exercises its own goldens
  testdata/        // flat .txtar fixtures (see Testing)
```

### Capability sources

```go
// codegen/codegen.go (additions)
type LangTranslator interface {
    ...existing...
    Capabilities() lower.Caps
}

type PlatformGenerator interface {
    ...existing...
    Capabilities() lower.Caps
}
```

Both are required (not optional). Implementations that need no lowering return `lower.Caps{}`. The compile pipeline merges:

```go
caps := platform.Capabilities().Merge(lang.Capabilities())
```

### Caps struct

```go
// internal/lower/caps.go
package lower

type Caps struct {
	NoToggle      bool // x!! → x = !x
	NoTernary     bool // a ? b : c → introduce temp var, assign in if/else, replace expr with var ref
	NoLambda      bool // closures → top-level funcs + captured-state struct
	NoUnit        bool // unit values → underlying numeric (always int — see Open Items)
	NoEnum        bool // enum members → int constants
	NoComputed    bool // computed vars → inlined exprs or memoized funcs
	NoTimer       bool // timer decls → explicit scheduler.At() / cancel() calls
	NoReactivity  bool // reactive deps → explicit updater stmts injected after each mutation
	NoDeclarative bool // visual node tree → flat stream of create/update/delete IR calls
}

func (c Caps) Merge(other Caps) Caps { /* field-wise OR */ }
func (c Caps) String() string        { /* sorted comma list of enabled flags */ }
```

Default zero value = no lowering, IR passes through unchanged.

### Pass ordering

Fixed in `lower.Lower`. Order is deliberate; each later pass may rely on transformations performed by earlier ones:

1. **NoUnit** — collapses unit values to ints. No deps.
2. **NoEnum** — collapses enum refs to int constants. No deps.
3. **NoTernary** — rewrites ternary expressions to if/else statements with a temporary var. No deps on visual model.
4. **NoComputed** — resolves computed vars to inlined expressions or memoized funcs. Must run before NoReactivity (which needs to see plain reads, not computed indirections).
5. **NoLambda** — lifts closures to top-level funcs + captured-state structs. Must run before NoReactivity (which may inject helpers; lambdas inside those helpers would re-introduce closures).
6. **NoToggle** — rewrites `x!!` to `x = !x`. Cheap stmt rewrite. Run before NoReactivity so the assignment is visible to the reactivity pass.
7. **NoReactivity** — analyzes dataflow, injects explicit updater statements after each mutation that touches a reactive dep.
8. **NoTimer** — rewrites timer declarations to explicit scheduler `At()` / `cancel()` calls. Depends on reactivity decisions (timer handlers may have been wrapped).
9. **NoDeclarative** — flattens the visual node tree into a stream of explicit create / append / update IR calls. Last because it destroys the tree shape that earlier passes rely on.

Passes whose Caps flag is false are skipped.

### Pass shape

Every pass exposes the same struct so `lower.go` can iterate uniformly:

```go
type pass struct {
	name    string // matches Caps field name, e.g. "NoToggle"
	enabled func(Caps) bool
	apply   func(*ir.Package) error
}

var passToggle = pass{
	name:    "NoToggle",
	enabled: func(c Caps) bool { return c.NoToggle },
	apply:   lowerToggle,
}
```

Each pass mutates `*ir.Package` in place (matching `optimize.Optimize`'s convention).

## Dump UX

New CLI subcommand `sngl dump lowered`:

```
sngl dump lowered [file|dir] --lang X --platform Y [flags]

Flags inherited from `dump`:
  --format spew|json|sngl   (default spew, matching other dumps)
  --color, --omit, --depth, --pointers, --input

Flags specific to `dump lowered`:
  --after PASS    dump IR after the named pass (matches Caps field name, case-sensitive)
  --list          print resolved caps + ordered pass list for the target, then exit
```

Behavior:

- No `--after`: runs all enabled passes, dumps fully lowered IR.
- `--after NoToggle`: runs passes up through and including NoToggle, dumps that intermediate state.
- `--after none`: special value — dumps post-optimize / pre-lower state (equivalent to today's `dump optimized`).
- `--list`: prints something like `caps: NoReactivity NoDeclarative; passes: NoComputed → NoToggle → NoReactivity → NoDeclarative` and exits without running passes.
- Invalid pass name → error listing valid passes.

Implementation: `lower.Lower` accepts `Options{StopAfter string}`. Re-runs from scratch on each dump invocation; intermediates are not cached (passes are deterministic, packages are already in memory, cost is negligible).

`--format sngl` roundtrips the lowered IR through `ir.Convert` to an `ast.Document` and through the existing SNGL formatter, so users can read lowered output as ordinary SNGL source.

## Testing

### Layers 1 + 2 — txtar goldens, flat layout

```
internal/lower/testdata/
  toggle_basic.txtar
  toggle_nested.txtar
  ternary_in_assign.txtar
  reactivity_counter.txtar
  reactivity_computed_chain.txtar
  declarative_simple_tree.txtar
  compose_html_caps_counter.txtar
  compose_imperative_full.txtar
  ...
```

Each `.txtar` is self-contained:

```
caps: NoToggle, NoReactivity
-- input.sngl --
component counter {
    var n: int = 0
    button { @click: n!! }
}
-- expected.sngl --
component counter {
    var n: int = 0
    button { @click: { n = !n; __update_0() } }
}
```

The `caps:` header line lives in the txtar archive's `Comment` field (everything before the first `-- name --` marker). Filenames are fixed: `input.sngl` and `expected.sngl`.

Single-pass tests set caps to one flag; composition tests set multiple. The flat layout with descriptive `<topic>_<scenario>.txtar` names makes it easy to skim.

Golden update mode (`go test -run TestLower -update`) rewrites only the `expected.sngl` section, preserving the caps header and input.

### Layer 3 — end-to-end (Phases 4+)

Existing `cmd/sngl/testdata/script/` txtar tests exercise full `sngl compile` runs. Once HTML and Fyne are ported (Phases 4–5), their snapshot tests serve as e2e regression coverage: rendered output must match pre-lowering snapshots.

### Regression guard

Add a script test that runs `sngl dump lowered --format sngl --list` over every `examples/*.sngl` file with each registered platform's resolved caps. Verifies no crashes, no caps-resolution errors, no `ir.Convert` gaps. Cheap canary that catches the long tail.

## Migration Plan

Path C from brainstorming: build lowering alongside the existing world, port consumers incrementally, delete legacy at the end.

### Phase 1 — Scaffolding

Land `internal/lower/` with `Caps`, `Lower`, all 9 pass stubs (no-op `apply` functions registered in order). Add `Capabilities()` to both interfaces; every existing implementation returns `lower.Caps{}`. Wire `lower.Lower` into the compile pipeline (`sngl.go` `Convert` and `cmd/sngl/compile.go`). Add `dump lowered` subcommand. Tests verify the dump command works and that no platform's behavior changes.

**Net behavior: zero.** Validates plumbing.

### Phase 2 — Cheap passes

Implement: `NoToggle`, `NoTernary`, `NoUnit`, `NoEnum`. Add per-pass txtar goldens.

`NoLambda` lands here **only if** the checker's capture analysis is complete and correct. Today the checker is suspected broken/incomplete in this area. Audit it as a Phase 2 prerequisite; if substantial work is needed, NoLambda slips to Phase 3 or later.

No platform turns these caps on yet. They run only when test caps explicitly enable them.

**Net behavior: zero for shipping platforms.** Validates pass mechanics.

### Phase 3 — Heavy passes

Implement: `NoComputed`, `NoReactivity`, `NoTimer`, `NoDeclarative`. Per-pass txtar goldens validate output as lowered SNGL — no platform consumes lowered IR yet.

Dataflow analysis currently lives in `codegen/analysis.go` and `codegen/deps.go`. Phase 3 **copies** the relevant logic into `internal/lower/reactivity.go` rather than maintaining a delegating shim — short duplication window is simpler than a shim that has to be removed later.

Phase 3 explicitly does **not** introduce a new platform. Validation comes from goldens of `dump lowered --format sngl` against expected files. The `none` platform is **not** turned into a lowering target — its interpreter wants the richest IR, not lowered IR, and it is the test backbone for the entire project; rewriting it carries unacceptable risk for no benefit.

### Phase 4 — Port HTML

HTML platform opts into `NoReactivity` cap (model 2). Generator drops `MutationModelEmitter`, consumes lowered IR directly: static visual tree from initial render plus injected updater statements bound to each mutation. Existing snapshot tests serve as e2e regression coverage. The `codegen/analysis.go` + `codegen/deps.go` originals stay in place (HTML's old code path is gone, but Fyne still uses them).

### Phase 5 — Port Fyne

Same as Phase 4 for Fyne — opts into `NoReactivity` cap. After this, `MutationModelEmitter` interface has no implementations; `codegen/analysis.go` and `codegen/deps.go` have no consumers.

### Phase 6 — Delete legacy

Remove `MutationModelEmitter`, `MutationCompilerFactory`, `MutationModel` struct, `Updater`, `Handler` (in `codegen/model.go`), and the now-orphaned `codegen/analysis.go` + `codegen/deps.go`. `RenderModel` / `RenderModelEmitter` stay (still used by bubbletea and Android).

### Phase 7 — Imperative platform (later, separate spec)

Build the new purely imperative platform (working name `imperative`, confirmed). Caps: `NoReactivity` + `NoDeclarative` + `NoTimer`. Targets winforms / .NET / similar. This is where the create/update/delete IR call stream is consumed by a real codegen target.

This phase is out of scope for this spec — it gets its own design once Phases 1–6 are done and the lowering APIs have been pressure-tested.

## Risks

1. **Copy + delete strategy for `codegen/analysis.go` + `deps.go`.** During Phases 3–5, dataflow logic lives in two places. Risk: drift if either copy is patched in isolation. Mitigation: review discipline — Phase 6 deletes the originals as soon as Fyne is ported, narrow window.
2. **WASM build constraints.** Lowering passes must be pure IR transforms (no fs, network, CDP). The playground builds `codegen` under `GOOS=js`; `lower` will too. Should hold naturally — flag any pass that needs disk access.
3. **Pass ordering bugs.** Wrong order silently wrong output. Mitigated by Layer 2 composition tests covering every realistic per-platform cap combination.
4. **NoLambda blocked on checker capture analysis.** Lifting closures requires accurate capture info. The checker should already do this — but the implementation is suspected broken/incomplete. Audit + fix as a Phase 2 prerequisite. May push NoLambda to Phase 3 if checker work grows large.
5. **`ir.Convert` fidelity.** Lowered IR may produce constructs `ir.Convert` doesn't yet handle (synthetic blocks, generated identifiers). Mitigation: extend `ir.Convert` as needed — gap fills should be small since it already handles every existing IR shape.

## Open items

Resolved during brainstorm:

- **Imperative platform name** — `imperative`. Phase 7 (out of scope here).
- **NoEnum target** — int constants, always.
- **NoTernary target** — if/else statement with a temporary var; expression reference replaced with the var.

Deferred (not blocking spec):

- Whether bubbletea / Android benefit from any caps (likely `NoUnit` / `NoEnum` / `NoLambda` only). Decided when porting.

## File-level changes (preview)

- **New**: `internal/lower/{lower,caps,toggle,ternary,lambda,unit,enum,computed,reactivity,timer,declarative}.go` + tests + testdata.
- **New**: `cmd/sngl/dump.go` adds `dumpLoweredCmd`; `runDumpLowered` parallels `runDumpOptimized`.
- **Modified**: `codegen/codegen.go` — add `Capabilities() lower.Caps` to `LangTranslator` and `PlatformGenerator` interfaces.
- **Modified**: every `codegen/lang/*` and `codegen/platform/*` registers a `Capabilities()` method (most return `lower.Caps{}`).
- **Modified**: `sngl.go` `Convert` and `cmd/sngl/compile.go` — invoke `lower.Lower` between the two optimize passes.
- **Modified later** (Phases 4–6): `codegen/platform/html`, `codegen/platform/fyne`, `codegen/model.go`, `codegen/analysis.go`, `codegen/deps.go`.
