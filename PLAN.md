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

## Root components inline down; they are not lifted

Settled. A component may name the root family, and it reaches codegen the way
every other component does -- inlined at the site that instantiates it, with
its state renamed per instantiation by the pass that already does that. There
is no hoist and no scan of `pkg.Components` for a family.

Where the family is declared, since it comes up: `struct root {}` in
`lib/ui/window.sngl`, carrying `#[marks.builtin("treeRoot")]`. A program
spells it `ui.root`; `ir.IsAppRootTree` matches the *mark* and never the name,
as it does for the other two tree kinds.

**The consequence is that a root component nobody instantiates renders
nothing**, and that is the last remnant of the harness convention rather than
a new rule. `applyRootWindow` today scans every declaration for the root tree
and lifts its windows whether or not anything renders it, which is why

```sngl
component main ui.root {
    var hits = 0
    ui.window #page(title="t") { ... }
}
```

is a whole program in six fixtures with nothing calling `main`. Under ordinary
inlining it is a declaration nobody reached. Each of those moves its windows
and state to package scope, where `pkg.Vars` already owns them:
`root_component_state.txtar`, `root_component_state_two_windows.txtar`,
`root_component_mutating_func.txtar`, `route_path_param_param.txtar`,
`unroll_static_view.txtar`, `unroll_static_view_empty.txtar`,
`cmd/sngl/testdata/route_param_beside_root_state.txt`, and
`examples/http-session/app.sngl`.

`root_component_state_two_windows.txtar` is the one to rewrite rather than
delete: it is where the per-platform copy-or-share table for one var mounted on
two windows is written down, and that claim survives the move.

Codegen is permitted to *error* where it cannot honor the result -- html on
`--lang none` cannot build a static page out of state whose value is not known
at build time. That is the right layer for it, and it replaces a lowering pass
that quietly rearranged the program instead.
## passCanvas goes away

Settled with Jonathan: a canvas's shapes are already segmented into their own
family, so a pass that surgically lifts them out of the tree is special logic
the type system has since made unnecessary.

What the pass does today: finds a canvas, turns its shape children into a
synthesized `_canvasDrawN` `*ir.Func`, hangs it off `NodeInst.CanvasDraw`, and
strips the children to the tree-less ones. That synthesized func is one of the
two reasons TODO §9 called `Funcs` the blocker, so **this comes before the func
work, not after**.

The tell that it is the same mistake the timer was:

```go
func isCanvasDrawFunc(fn *ir.Func) bool {
	return fn != nil && fn.Synthesized && strings.HasPrefix(fn.Name, "_canvasDraw")
}
```

A name match, against the rule stated everywhere else in this repository -- and
a name match *because the pass invented a function with no other identity*.

**Measured, not predicted.** Disabling the pass outright and generating
`canvas_shape_in_conditional` on html: the canvas still came out a `<canvas>`
element and no shape was emitted as DOM, but the `if` around the shapes became
a **render slot** (`<span data-sngl-slot="0">`) and the shape nodes consumed two
`__nN` refs before the canvas did. Moving the pass to the end of the pipeline
instead broke exactly the seven canvas goldens and nothing else.

So the gap is three widget passes, not the 51 `case *ir.NodeInst` arms across 26
files in `internal/lower`. The other 48 do things to a shape node that are
harmless or correct.

- `passReactivity` -- must not make a shape's `if` a render slot.
- `passNodeEscape` -- must not allocate a widget ref for a shape.
- `passDeclarative` -- must not flatten a shape into `CreateNode`.

One predicate, `ir.IsSegmentedTree(comp.Tree)`, which already exists and which
`isPrimitiveComponent` already uses to exempt shapes from inlining. It is not a
new fact about the program; it is an established fact the rendering passes were
never told.

**There is no draw function.** Two findings turned the first plan around, and
both came from the `run/` toolchain records rather than from any text diff.

Building the body at codegen puts it *after* the optimize pass that runs again
following lowering, and two things depend on that pass seeing it:

- `undefined: applyStyle` (fyne), `undefined: paint` (gtk4). Those helpers are
  referenced only from shape override bodies, which `ir.SpecializeForTarget`
  swaps in during lowering -- after the first shake. They survived because the
  draw func sat in `w.Funcs`, which is a shake root. Unowned and built later,
  the func is invisible and the helpers are shaken away.
- android grew a `drawRect` with a zero-alpha stroke: `Color(0,0,0,0).a > 0`
  used to fold to false and drop the branch.

So *unowned* and *built at codegen* are separable, and conflating them was the
error. The answer (Jonathan's) is that neither is needed:

- **Lowering rewrites a canvas's shape children in place** into the imperative
  statements the platform package's `@draw` handlers supply. They stay in the
  tree as ordinary statements, so folding, shaking and usage reach them the way
  they reach anything else. The pass splices; it lifts nothing, invents no
  declaration and has no owner to find.
- **Codegen understands its canvas component** and emits those statements where
  its own backend needs them -- the cairo closure on gtk4, the `Raster`
  generator on fyne, the `DrawScope` lambda on android (which already inlines
  one), the rasteriser closure on bubbletea, the JS draw callback on html.

What that deletes: the synthesized `*ir.Func`, its name and the numbering
behind it, `NodeInst.CanvasDraw`, `CanvasRedrawStmt.DrawFunc`, three of
`LocalVar`'s four canvas fields, `isCanvasDrawFunc`'s name match, and
`isPrimitiveComponent`'s `hostsTree` exception -- whose own comment on
`feat/markup-tree` says it exists only because passCanvas needs the node the
shapes hang off.

**One commit, not staged:** `codegen` imports `lower` for `lower.Features`, so
`lower` cannot import `codegen` and there is no intermediate where both hold
the emission code.

Two fixes on that branch are general rather than markup's, and a composed shape
may need them: a *kept* component's body is never inlined into, and a stdlib
component with a body of its own is not inlinable (`|| comp.Stdlib`). Take them
only if canvas reaches them.

## A window is not a storage level

Settled with Jonathan, and it dissolves the question step 4 was going to be
about. The IR does not need lexical levels, it needs **storage levels** -- the
places a declaration can exist more than once at run time: the package, a
component instance, a loop iteration. Scoping and shadowing are the checker's,
resolved into pointers by IR time, so a `var` under a `vbox` and one at the
root of the component body are the same thing to every consumer.

A window is a *rendering root*, not a lifetime. On bubbletea, fyne and gtk4 the
windows are one process with one Model -- the shared root-component var is
already one cell there. On html each window is a separate document, but that is
the platform instantiating one declaration per page, which is the divergence
this repository already defends.

So a window's declarations go to its container, and since `window` is
root-only that is almost always the package; it is a component exactly when a
root-family component renders the windows, which html on `--lang none` should
reject as something a static page cannot produce.

**`ir.Owner` was the right shape with the wrong membership.** One enumeration
of who owns state, asked rather than restated, is what stopped six consumers
each naming the package and `main` and stopping. It listed the window, and it
shrinks to package and component.

**What is left of `ir.Window` after that is nothing that needs a home.**
`Comp`, `Props` and `Handle` are already `NodeInst`'s; `Body` is `Children`;
`Timers` is gone; `Name` is `NodeInst.ID`, which is what `buildWindow` already
sets it from; `ErrorHandler` is an ordinary entry in `NodeInst.Handlers`;
`LocalRefs` is a set of id strings and `uniqueNodeIDs` has already made those
unique package-wide, so the container's set cannot confuse two. `Checked` is a
checker flag and can be a set in the checker. None of `ir.FuncDecl`, a durable
body scope, or an ownership field is needed -- all three answered a question
that this dissolves.

**Two behaviour changes, not a pure refactor.** html's per-page `State` is
built by list membership today (`routeStateVars` adds `pkg.Vars` unconditionally,
then the main component's, then the window's) and has to become reachability --
what this page's tree actually reads. That is strictly more accurate than what
is there. And colliding names need renaming, which is *not* a new cost: two
windows each declaring `var count` already emit

```go
type Model struct {
	count int
	count int
}
```

on bubbletea today, which does not compile. The field being preserved is not
preserving anything.

## The mark goes last, behind a platform primitive

`BuiltinWindow` has exactly three non-test uses and all three are in the
checker: the dispatch that builds `ir.Window` (`expr.go`), `isWindowNode`
(`checker.go`, three callers), and `bindBuiltinRole` storing the reference.
`isWindowNode`'s own doc says why it exists -- *"Window is the only node kind
that owns a lexical scope and hoists its own element ids"* -- and both of those
are what step 4 removes.

What does *not* dissolve is `isPrimitiveComponent`'s `comp.Builtin != ""`,
which is what keeps a declaration standing against `passInlinePure`. It is
redundant for `window` today only because the declaration is empty-bodied and
`inlinable()` tests `len(comp.Body) == 0`. That test conflates bodyless with
empty-bodied and should read `comp.Bodyless` -- a one-line fix, and the
distinction matters the moment a platform gives `window` an override body.

The replacement is the pattern `timer`, the drawing shapes and `button`
already use: **the override bottoms out in a platform-declared `#[intrinsic]`
the codegen understands.**

```sngl
component ui.window[platform] {
    html.Document(title=title, href=href) { content }
}
```

`isPrimitiveComponent` protects the primitive rather than the wrapper, and
codegen dispatches on the id. Identification moves from a compiler mark to a
platform id, which is the right layer: what a window *is* differs per target in
a way the compiler has no stake in -- a `fyne.Window`, a
`GtkApplicationWindow`, an Activity, the whole TUI, or an output file.

Every target must then declare one, and `reportBodylessLibComponents` already
enforces exactly that. html's is the odd one, being an output file rather than
a widget: an intrinsic id carries it, but it is the first whose effect is on
the build output rather than the render.

`internal/build.Emit`'s "a program declares at least one window" becomes "the
package body renders at least one root-family member" -- the tree answering a
tree question instead of a check that knows a construct's name.

### What is actually left, measured after step 7

The opening paragraph above is stale. `BuiltinWindow` is no longer three uses
in the checker: `ir.IsWindowNode` reads it and is the one predicate that
replaced 72 `case *ir.Window:` arms. Of the checker's six sites, four are
bookkeeping (`bindBuiltinRole` stores the declaration, two duplicate-id scans,
the build dispatch). **Two are decisions, and both are the same one twice:**

- `declareNodeIDsStmt` does not descend into a window, because a window hoists
  its own ids.
- `declareNodeIDsStmt` skips a window id under a `for`, and
  `collectForLoopWindowIDsStmt` declares `list<window>` outside the loop
  instead.

So what the mark still decides is **that a window owns an id namespace**, and
that is the reduction §8 wants: a window owns one because it is a rendering
root, which is the *tree*'s answer (the root family) and not the mark's. The
one bit the tree does not give is that a root-family *component* has that tree
too, so telling the primitive from a component that renders one needs
bodyless-ness or the mark.

**The namespace is already half-gone, inconsistently.** A window's
declarations go to the package -- a body `func` is hoisted there at check
time, a body `var` by passHoistState -- while its ids stay in a scope
checkWindow pushes. Scoped at check time, shared at storage time, renamed by
nobody: two windows each declaring `var n` check clean and bubbletea emits
`n int` twice in one Model, which does not compile. A component instantiation
does not have this problem, because the inliner renames its state per instance
(`__instN`). Removing the last of the specialness is what gets a window that
rename, and the runtime-instance machinery with it.

**The window-loop machinery it would replace is already vestigial.**
`expandForWindows` is reached by no program that contains a window: replacing
its unroll with a panic leaves every golden and all 165 CLI scripts green, and
the only test that fires it is `TestOptimize_PropPropagatesAsConst`, whose
`component main node` holds no window and unrolls a `text`. What it does is
"unroll a const loop in a component called `main`" -- a leftover of the
harness convention step 5 ended. The real window-loop unroll rides on the
ordinary const-loop unroll over `pkg.Body`, which runs only for a target with
no host language.

Which leaves a hole worth knowing about before the design is picked:
`for var it = items { window #page(title=it) { … } }` on **bubbletea** is not
unrolled, `ir.Owners` lifts the window out of the loop, and the body keeps a
read of `it` that nothing declares -- `fmt.Sprint(it)`, one occurrence in the
file. It does not compile. Pre-existing (same output at `d2526c58`), and it is
exactly the case a window would stop having if it reached the instance
machinery every other node reaches. The one thing that would not fall out for
free is the `list<window>` binding, which needs a compile-time list; for html
static, N pages must be N files anyway, so that unroll survives as a platform
fact rather than a window fact.

## Staging

Each step green, each with its own fixture, a golden refresh read rather than
rubber-stamped.

1. This plan. (done)
2. Delete `ir.Timer`. (done -- 53 files, -764/+340, every golden byte-identical)
3. Delete `passCanvas`. (done -- `passShapeDraw` splices the drawing where it
   was written; no function is synthesized into the IR, so 4's blocker is
   gone.)
4. **A window's declarations go to its container**, the way any other visual
   node's would. `ir.Window.Vars` and `.Funcs` are deleted; a `var` or `func`
   at the root of a window body lands on the package, or on the component that
   renders the window where a root-family component does. (done in `31e63e21`
   and the six commits that repaired its 48 goldens; see below.)
5. `pkg.Windows` derived; `passRootWindow` deleted, root components left to
   ordinary inlining, the fixtures above rewritten. (done -- `ir.AllWindows`
   off `ir.Owners` is the one enumeration, `pkg.Body` is a live body for the
   whole pipeline, and `ir.Package.IsProgram` asks reachability rather than
   membership.)
6. The 39 statement arms, once a window is no longer a statement kind.
   (done -- 72 of them by the time it happened, and they went with 7 rather
   than before it: a window stops being a statement kind by becoming a
   NodeInst, so the two steps are one change.)
7. Delete `ir.Window`: a window is a `NodeInst` like any other node. (done --
   `type Window = NodeInst`, and `ir.IsWindowNode` is what a walk asks. The
   name is kept as an alias because nineteen consumers spell the answer that
   way; it buys no type safety and is documented not to.)
8. Delete the `#[builtin("window")]` mark, behind per-platform window
   primitives. See below. Delete this file.

   **Not started, and the section below has gone stale.** It opens
   "`BuiltinWindow` has exactly three non-test uses and all three are in the
   checker", which step 7 made untrue: `ir.IsWindowNode` reads that mark and
   is the one predicate that replaced 72 `case *ir.Window:` arms. The checker
   asks it with no target picked, and half the lowering asks either side of
   the platform override, so "identify a window by the platform's intrinsic
   id" has nowhere to stand. The goal is not in question; the route to it is.
   See `handoff/window-collapse-6.md`.

Emit-when-called is a separable follow-up: `shakeUnused` filters `pkg.Funcs`
but treats `comp.Funcs` and `w.Funcs` as roots, so those are unconditionally
live today whatever records ownership.

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
