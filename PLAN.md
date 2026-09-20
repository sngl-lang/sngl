# The window collapse — branch plan

Delete `ir.Window` and `ir.Timer`. Checking and lowering get one path for all
families; codegen decides how a family is special.

Scratch. Delete this file in the commit that finishes the work.

## Measured on this branch

- `*ir.Window` outside tests: **156** references. **107** of them in
  `internal/lower` (27 files), `internal/optimize` (7), `internal/checker` (5),
  `ir` (10) — against **8** across `codegen/` and all six platforms combined.
- `internal/lower` carries **39 `case *ir.Window:` arms across 24 files**.

The layer that should know a family is special barely mentions it; the layers
that should not are where it lives. That is the whole argument.

## The four questions, settled

### 1. A func declaration becomes an `ir` statement

`ir.FuncDecl`, so a body's funcs are in its statement list and an owner is just
a body. The alternative considered and rejected was growing `NodeInst` the
body-owner fields `Component` already carries (`Vars`, `Funcs`, `Timers`,
`Methods`, `BodyDecls`): mechanically smaller, but it keeps "what a window
owns" as a set of fields that are nil on every other node, and moves the
specialness from a type to a nil check rather than removing it.

The price is honest and is paid on purpose: every statement walk grows a
`FuncDecl` arm. That is the same currency the 39 `*ir.Window` arms are paid in
— the trade is 39 arms for one, in a kind that is uniform across owners
instead of special to a family.

`passCanvas` appends a synthesized draw func with no source body to live in.
Under a statement it appends a statement, which is what it wanted.

### 2. `pkg.Windows` is derived at codegen

`passRootWindow` stops writing a field. `CodegenCtx.Windows` — which already
exists, and is already the layer that knows what a window is — builds the list
by asking which `NodeInst`s instantiate the `#[builtin("window")]` declaration.

This is what makes §8's **B** and **C** go away rather than get a fortieth
special case: there is no lift to reach into a slot population too early,
because there is no lift.

### 3. `ir.Timer` is deleted, not relocated

An earlier fork here was "make `ir.Timer` a statement". Superseded: the record
is `{Interval, Enabled, Handler}` and every field of it is already readable off
the timer-primitive `NodeInst` — props `interval` and `enabled`, handler
`@tick` — plus the chain of enclosing `if` conditions that `passTimerPrimitive`
ANDs onto the gate.

So the primitive **stays in the tree** and `codegen.AnalyzeCommon` builds
`TimerInfo` by walking for it, ANDing the enclosing conditions as it descends.
`passTimerPrimitive` is deleted entirely.

Two things to hold on to while doing it:

- Only bubbletea, android and `none` contribute a timer primitive at all; html,
  fyne and gtk4 override `timer` with an `effect` and have no such node in
  their trees. So "left standing, a declarative target turns it into a widget"
  — the failure that motivated the pass — cannot arise on the three that would
  suffer it. Confirm `passDeclarative` is not reached with one in the tree
  before deleting the pass, rather than assuming it.
- The gate fold is the pass's one non-mechanical act. A timer under a branch is
  armed only while that branch would have rendered; a timer under a *loop* is
  deliberately not gated, because a schedule per iteration is not something any
  backend's timer runtime expresses. Both behaviors have to survive the move to
  `AnalyzeCommon`, and the second is invisible unless a fixture says so.

### 4. `#243` needs no written exception

It said bubbletea cannot follow html/fyne/gtk4 in lowering a timer to an
effect, because Elm lets nothing outside `Update` touch the model and `Init()`
needs a period and a body. All still true, and all still satisfied: bubbletea
reads `CommonAnalysis.Timers`, which it already does today, and which still
carries a period and a body. Nothing about #243 depended on `ir.Timer` being a
field on a window.

## Open — needs Jonathan

**Root components are not dead.** The claim that the lift is mostly unnecessary
because "main components no longer exist" holds for the *harness convention* —
`CodegenCtx.RootDecl()` answers only for a harness that cleared the windows,
and `main` is an ordinary component. It does not hold for
`component X ui.root`, which is still writable, still checked, and still
exercised:

- `testdata/root_component_state.txtar` — `component main ui.root` with
  `var hits` and `func bump`, hoisted into the window, on three targets.
- `testdata/root_component_state_two_windows.txtar` — the same var mounted on
  two windows, and the file where the per-platform copy-or-share table is
  written down.
- `testdata/root_component_mutating_func.txtar`,
  `testdata/route_path_param_param.txtar`,
  `cmd/sngl/testdata/route_param_beside_root_state.txt`,
  `examples/http-session/app.sngl`.

So `hoistRootState` and `stripComponentReceivers` are live code with goldens
behind them, and the second path of `applyRootWindow` cannot simply be dropped.

Either the collapse keeps a hoist for that shape, or `component X ui.root` is
itself on the way out and these fixtures change. That is a language decision,
not a refactor decision, and it is the one thing here still unanswered.

## Staging

Each step green, each with its own fixture, a golden refresh read rather than
rubber-stamped.

1. This plan. (done)
2. `ir.FuncDecl` — introduce the statement, move a *component's* funcs onto it
   first, where there is no window in the picture. Proves the shape.
3. Delete `ir.Timer`: gate fold into `AnalyzeCommon`, `passTimerPrimitive`
   deleted.
4. `pkg.Windows` derived; `passRootWindow` reduced to whatever the answer to
   the open question leaves of it.
5. The 39 statement arms, once a window is no longer a statement kind.
6. Delete `ir.Window`. Delete this file.

## Rules that bite

- 250 goldens and 165 CLI scripts; nearly all have a window. Seed with
  `go test . -run TestGolden -update`, then **read the diff** — a refactor that
  changes emitted output has changed behavior.
- `run/<lang>/<platform>` records are the toolchain gate; `-update` compiles.
  `run/kotlin/android` needs `export ANDROID_HOME=$HOME/Android/Sdk`. If a
  record cannot be produced, say so rather than committing a golden nothing
  compiled.
- Absence is a `deny` line, never `! grep`.
- Confirm a new fixture fails when the behavior is reverted.
- Do not run `sngl fmt` over the tree; `codegen/platform/gtk4/gtk4.sngl` is not
  fmt-clean.
- Build the binary out of tree: `go build -o /tmp/sngl-win ./cmd/sngl`.
- No history rewriting. One fix, one commit.
