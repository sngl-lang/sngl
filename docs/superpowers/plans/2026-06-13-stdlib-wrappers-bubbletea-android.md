# Session prompt: finish audit finding #3 — NoStdlibWrappers for bubbletea + android

Paste the section below into a fresh Claude Code session in the `sngl` repo.

---

Complete finding **#3** in `docs/superpowers/audit/lowering-migration.md` ("Stdlib-component → native-widget translation done per platform"). Read that finding's Status block first — it has the full mechanism map and file:line references from the prior session.

## Goal

Make **bubbletea** and **android** lower stdlib components the way **html/fyne/gtk4** already do (they set `StdlibWrappers=false`), so each platform's hand-rolled name-keyed switch is replaced by a data-driven renderer. Net result: codegen no longer branches on stdlib component name (`vbox`/`hbox`/`text`/`button`/…); the per-widget mapping lives in the platform `.sngl` file.

## Background mechanism (already verified)

`Caps.StdlibWrappers=false` (⇒ `NoStdlibWrappers=true`) makes `passInlinePure` (`internal/lower/inline_pure.go`) substitute each `component sngl.X { … }` wrapper body in place before codegen. Three existing realizations:

- **html** — wrappers inline to native `html.*` element bodies; codegen sees only native tags.
- **fyne / gtk4** — wrappers inline to a single blueprint-carrying node; `fyne.sngl` declares `Constructor{goFn, goType, args, prelude, switches, imports}` + `Init/Reactive/Event` records, and a **generic** renderer (`fyne/view_ir.go` `renderFromBlueprint`) emits Go from that data with **no name switch**. This is the target model.
- **bubbletea / android** — wrappers are kept; `bubbletea/view_ir.go` `renderStdlibComponent` (~150-line switch) and `android/compose_ir.go` `renderStdlibComposable` do the dispatch at codegen. `bubbletea.sngl` / `android.sngl` already carry per-widget metadata props (`join=`, `content=`, `focusable=`, `pkg=`, `modelType=`, …) on the wrapper bodies.

## Plan (do bubbletea first, fully verify, then android)

1. Study `fyne.sngl` + `fyne/view_ir.go renderFromBlueprint` as the reference for a data-driven renderer.
2. For bubbletea: enable `f.StdlibWrappers = false` in `bubbletea/bubbletea.go` `Capabilities()`. The wrappers in `bubbletea.sngl` will then inline. Decide the renderer shape — either (a) dispatch on the inlined native-primitive node names (`VJoin`/`HJoin`/`Text`/…) that the wrapper bodies expand to, reading their metadata props, or (b) a fuller blueprint à la fyne. Prefer whichever yields **byte-identical** output with least new surface.
3. Delete `renderStdlibComponent` once the data-driven path covers every widget.
4. Repeat for android (`renderStdlibComposable`, `android.sngl`, `android.go`).

## Hard constraints

- **All-or-nothing per platform.** `NoStdlibWrappers` inlines *every* stdlib wrapper at once — you cannot stage widget-by-widget. Expect a large single change per platform.
- **Byte-exact.** Generated lipgloss / Compose code must match pre-change output. The only behavioral gate is the snapshot suites (`go test ./codegen/platform/bubbletea/...` and `.../android/...`, 60–90s each) plus `go tool verify`.
- Output must stay **deterministic** (a prior fix made android style-field order deterministic — don't reintroduce map ranges; `codegen.NodeStyleFields` returns an ordered `[]codegen.StyleField`).

## Verification workflow (set up a fresh baseline FIRST, before any change)

```bash
# from a clean HEAD, capture baseline of example codegen across the matrix
bash tmp/gen-matrix.sh /tmp/sngl-mig/baseline    # script exists in repo
# ...make changes...
bash tmp/gen-matrix.sh /tmp/sngl-mig/current
diff -rq /tmp/sngl-mig/baseline /tmp/sngl-mig/current | grep -vE '/\.(stdout|stderr|results|sngl)'
go tool verify   # full suite; 0 failures required
```
`tmp/gen-matrix.sh` runs `sngl generate` for every `examples/*` across the platform×lang matrix. The example most exercising stdlib widgets is `examples/showcase` (bubbletea, fyne, android targets) and `examples/todo`.

## Project conventions (from CLAUDE.md + repo memory)

- Work directly on `main`; no worktrees/branches. Commit working increments as you go (commit only meaningful, building states). Commit-message footer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- `go tool verify` runs formatters; if it produces changes, commit them.
- Fix root-cause compiler bugs forward; don't work around. Raise scope decisions, don't silently punt.
- Update the #3 Status block in `docs/superpowers/audit/lowering-migration.md` as you land each platform.

Start by capturing the baseline, then dig into `bubbletea.sngl` + `renderStdlibComponent` and propose the data-driven renderer shape before editing.
