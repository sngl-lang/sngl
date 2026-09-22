# After the window collapse

Scratch. Delete this file in the commit that finishes the work.

Three pieces are left, in the order Jonathan set: **scope ordinality first**,
then the window primitive, then the owner predicate. They are independent
enough to land separately, and the first is the one with a language rule in it.

Starts from `feat/window-collapse` (!169), which is green and open for review.

## 1. Ordinality of scopes

**The rule: a handle reached *through* a scope carries that scope's count.**
Through an `if`, `option<T>`. Through a `for`, `list<T>`. Through a window,
`option<T>`, because a window may not be open. Through a `tree.one` slot, `T`.

This is not a modeling nicety. It is the missing rule behind three live codegen
defects, each of which type-checks clean today and emits a reference to a name
nothing declares.

| written                                                            | emitted                                                                                                                                                                                                            |
|--------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `if show { text #maybe(…) }`, read from a sibling handler          | html: `let maybe2` is declared *inside* the slot render and the handler reads a bare `maybe`. bubbletea: `maybe.Value = "x"` is the only occurrence of the name in the file, so the generated Go does not compile. |
| `for var it = items { text #row(…) }`, read from a sibling handler | html declares `row2` per iteration inside the loop; the handler reads a bare `row`. One name, N elements.                                                                                                          |
| `#btn1` in `#win1` read from a handler in `#win2`                  | refused by the tree-family id barrier (`2d8a3838`) — the only one of the three that is not silent, and only because it was closed by hand.                                                                         |

Two things make the change smaller than it looks.

**The vocabulary exists.** A slot's count is already bare, `tree.one<T>` or
`option<T>`, and the count is already enforced in both directions: `scroll {}`
with zero children and with two both report *"component scroll requires exactly
one child"*. What is missing is applying that count to a handle *reached
through* the scope, not to the scope's contents.

**Windows are not the hard case.** `collectForLoopWindowIDs` already binds
`list<window>` for a loop over windows, so the `for` arm of the rule has a
working precedent — it stops being a window special case and becomes the
general one. The earlier claim that a runtime list would have to be invented
was measured against ordinary nodes and is wrong about windows.

### Landed: the ordinary-node half

`if` → `option<T>`, `for` → `list<T>`, and a for-else → `option<T>` rather than
a list, since it runs at most once. `declareNodeIDsIn` carries the chain of
scopes crossed as `[]nodeCount` and `countedHandleType` wraps the handle in
them innermost-first, so an `if` inside a `for` composes to
`list<option<T>>`.

The count is what a scope says about a handle read from *outside* it. Read
from inside, the handle is the one node it has always been — which is a
working program today, and html already renders it correctly. So `checkBlockIR`
hoists each block's own ids again, at their own depth, into the scope it
pushes, and that inner binding shadows the counted one. `declareNodeID` now
measures `LookupLocal` before `Lookup` for exactly that: it still declines to
shadow a prop, var, func or outer symbol, and the one thing it does shadow is a
node handle an enclosing scope bound for the same node.

The read is refused rather than the type shipped. `option` and `list` are both
absent from `hasNoLegitimateFields`, so `maybe.value` off one degrades to `dyn`
and the two broken builds would have become two silent no-ops — which is the
"decision to take with it" below, answered the recommended way. The diagnostic
is positioned at the read, which is the line that has to move.

Fixtures: `testdata/error_scope_ordinality.sngl` (both reads) and
`testdata/scope_ordinality_inside.sngl` (the positive half — it fails if the
`checkBlockIR` re-hoist is removed, which is what makes the re-hoist
load-bearing rather than defensive).

### Landed: the window half

A window is a scope like an `if` and confers the same count, for the reason
Jonathan gave when the target-dependence was raised as an objection: the type
is `option` everywhere, user code handles the absence, and html simply never
has the value. So there is no target-dependent diagnostic in the checker at
all — the read is refused uniformly, exactly as for `if` and `for`, until the
language can unwrap one.

`countWindow` is a count of its own only so the message can name what was
crossed; the type it wraps in is the same option. `hoistWindowInteriorIDs` is
a pass because a window at the root of a file is *registered* in pass1 rather
than left in `pkg.Body`, so `declareNodeIDsStmt` — which counts a window it
meets as a statement — never walks one. It runs before the window bodies are
checked, since a handler in the second window is what reads the first's ids,
and `checkWindow` then hoists the same ids plain into the window's own scope,
shadowing these.

**The barrier splits rather than retires.** A window counts; every other
family change still stops outright, which today means a canvas. That is not
the "no runtime identity" argument — Jonathan is right that a shape having no
API to interact with would make the distinction moot. It is that a shape's
*props* resolve: a typed `dot.r` checks clean and renders nothing, which is
the silence `error_tree_family_id_barrier.sngl` was written to end, and
`option<circle>` would restate it rather than fix it. `ir.CrossesTreeFamily`
keeps its job for that half and says so.

Fixtures: `testdata/error_scope_ordinality_window.sngl` is the cross-window
read; `testdata/error_tree_family_id_barrier.sngl` keeps its subject
unchanged — its program was always canvas-only, so the plan's expectation that
it would become a positive fixture was wrong about what it covered.

Two windows writing one id is the flat namespace it has always been: the first
claims the name, the second is skipped, and the type names the first window's
component. Only the message is affected, since every read of a counted handle
is refused either way. **There is still no way to say which window you meant**
— that is the one thing this does not answer.

### Left of section 1

`collectForLoopWindowIDsStmt` is not folded into the general `for` arm.
Folding it would make the `list<window>` it binds a `NodeHandle`, which the
read diagnostic then refuses — and that list is legitimately read, being what
`expandForWindows` fills in. It is a real cleanup and it needs the unroll's
contract looked at, not a one-line move. `declareNodeID` also still types a
window handle itself as `c.windowType`, uncounted.

### The decision to take with it, not after

Typing the handle is half the job. All three rows above are loud failures
today; under the rule they compile, so an unwrapped handle that no backend can
act on turns three broken builds into three silent no-ops. On html a
cross-window handle is always none — two windows are two documents with no
shared address space. On fyne, gtk4 and bubbletea it is one Model and a
genuinely live optional.

So either the backend half lands with the rule, or **an `option` handle no
target can reach is an error at the read**. Recommended: the second, as the
first cut. Every error stays an error, and the type is honest rather than
aspirational.

### What it retires

- The tree-family id barrier in `declareNodeIDsStmt` (`ir.CrossesTreeFamily`),
  since the cross-window read becomes `option<ui.button>` rather than
  `undefined`. Keep `RestSlotTree`; `TreeHosted` is built on it.
- `collectForLoopWindowIDsStmt`, folded into the general `for` arm.
- `testdata/error_tree_family_id_barrier.sngl`, whose subject is the refusal.
  Its program becomes a positive fixture for the `option` type.

## 2. The window primitive

Two halves of one change, both settled with Jonathan.

**`ui.window` is declared with no body, not with `{}`.** `{}` says the
component renders nothing, which is not what is meant; bodyless says the render
comes from somewhere the declaration names. It is currently

```sngl
component window<T = struct {}>(
    title string,
    href string,
    favicon string,
    params T,
    @error error,
    content ...component(v T) node,
) root {}
```

**Every target then declares one**, which `reportBodylessLibComponents` already
enforces per target: `component ui.window[platform] { html.Document(…) }` and
its equivalent in fyne, gtk4, android, bubbletea and none. What a window *is*
differs per target in a way the compiler has no stake in — a `fyne.Window`, a
`GtkApplicationWindow`, an Activity, the whole TUI, an output file — which is
the argument for moving it.

The platform side is thin: six `ir.IsWindowNode` sites across all of `codegen/`
(`iterate.go`, android, bubbletea ×2, html ×2), mostly "skip the window node
and render its children". fyne and none have none at all. The bulk of the work
is six `.sngl` declarations and an intrinsic id each.

Note this touches the same declaration as (1): the window's `content` slot is
where its cardinality is written. Doing (2) first means editing that
declaration twice, which is accepted.

### What it does not do

It does **not** let the compiler identify a window by a platform intrinsic id,
which is what the deleted plan's step 8 proposed. The checker runs with no
target picked — `reportBodylessLibComponents` returns early on
`len(c.targets) == 0` for the LSP and `sngl fmt` — and `ir.Owners` is asked
long before any override is merged. Identification stays with (3).

## 3. The owner predicate

Measured at `2a2c2f1f`: 33 `IsWindowNode`/`isWindowNode` sites plus 7 direct
`BuiltinWindow` sites. Six are in `codegen/` and the platforms, which is where
the knowledge belongs. Of the other 27:

- **9 of the 12 in `internal/lower` are one question asked nine ways.**
  `declarative`, `inline_components`, `inline_pure`, `boundary_failed`,
  `node_escape` and all four in `reactivity` say the same thing in their own
  comments: *driven as its own owner*, *its own scope*, *`ir.Owners` hands it to
  us separately*. None cares that the node is a window. Give `ir` one predicate
  beside `Owners`, which already computes exactly that set, and nine sites stop
  naming windows.
- `ir/owner.go` defines ownership; `IsProgram` asks whether the package renders
  one; the interpreter's key and view walks ask a renderer's question (*a window
  contributes a scope, not a widget*); `hoist_state` and `window_nesting` are
  window-specific by construction; `optimize/expand` collects build-time window
  values for id folding.
- The checker has seven: the predicate, `bindBuiltinRole`, the pass1
  `BuiltinWindow → windowShell` dispatch, the duplicate-id scan, the
  `list<window>` loop hoist, and picking `c.windowType` for a handle — and that
  last one's own comment says a scope lookup answers identically except for a
  program that shadows the name.

**Then re-key the predicate from the window mark to the root tree.**
`ir.IsAppRootTree` already exists, reading the `#[builtin("treeRoot")]` mark on
`sngl:ui`'s `root`. `IsProgram` becomes "the package body renders a root-family
member", which is what `internal/build.Emit`'s error message already claims it
checks.

The residual the tree cannot answer: a user `component main ui.root` carries the
root tree too and *does* inline down, so root-family over-matches by itself. It
is separated by the empty body — which is also why `inlinable()` already returns
false for `window` independently of the mark, its test being
`len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0`. After (2)
makes the declaration bodyless, `comp.Bodyless` is the honest spelling of that
test.

## 4. Capabilities in source, and `sngl:x/codegen`

`lower.Features` is 23 booleans answered by a `Capabilities()` method compiled
into each plugin, merged as `f := lang.Capabilities()` and then restricted by
the platform. Three things are wrong with that shape, and only the last is
what this section started from:

- It is a **Go interface**, so a target that is a command rather than a
  compiled-in package cannot answer it.
  `codegen.PlatformRendersViewStatically` already makes the argument in its own
  doc comment -- it reads `Features` rather than type-asserting an optional
  interface precisely because *"a type assertion is capability detection and
  cannot cross a process boundary"*. That reasoning applies to the whole
  record, not just to the one question it was written for.
- It is **two vocabularies for one fact** -- `Features` positive, `Caps`
  negative -- with a hand-written `ToLowerCaps` and `Merge` between them, each
  naming every field, and a `String` naming them a third time.
- It is **per target and nothing finer**, so a tree family cannot differ from
  the target that renders it. Which is where this came from: a shape's props
  resolve, so `ui.text(value="{dot.r}")` beside `circle #dot` type-checks and
  renders nothing on every target.

### The tier

`sngl:x/codegen`, at `lib/x/codegen/`, macros and their flag enum and nothing
else. The boundary against the two mark packages that exist: `sngl:macro` is
what a package says about **the declarations it exports**,
`sngl:internal/marks` is what the compiler says about **itself**, and this is
what a target package says about **its own code generation**. Its audience is
exactly the plugin set -- a language or platform package, in this repository or
outside it -- which is why it is not under `internal/`.

The flag enum is declared in the package itself rather than in
`sngl:internal/ir` beside the others. The reason that one exists is that a
mark's package gets dot-imported and a dot import lifts what it declares, so
an enum next to the mark would land in the scope of every program that imports
the package that imports it. This one is written qualified --
`#[codegen.can(...)]`, as the repository's own source now writes every mark --
so nothing is lifted and the vocabulary belongs with the marks it is the
vocabulary of.

Three marks, and the split between them is one `caps.go` already admits to in
prose:

- `#[codegen.can(...)]` and `#[codegen.cannot(...)]` say what the target emits
  natively.
- `#[codegen.wants(...)]` asks for a pass. `StructComponents`,
  `StdlibContextParam`, `FocusOrder`, `Canvas`, `ReactiveCanvas`,
  `InsertBefore` and `Effects` are all of this kind -- `Features`' own comment
  calls them *"platform-opt-in passes rather than language limitations"* --
  and spelling them with the same word as a capability is what kept that
  distinction a comment rather than a spelling. A platform asking for `canvas`
  is not confessing that it cannot do something.

`ViewStatements` is a fourth kind again and keeps `can`: it requests no pass at
all, which is why `ToLowerCaps` deliberately drops it, and its one reader is
the optimizer.

### Polarity: nothing until it is said

`AllFeatures()` says *everything, minus what you withdraw*. `Features`' doc
comment says the opposite two paragraphs earlier -- *"the zero value is safe:
every flag defaults to false (needs lowering), so new platforms automatically
get all lowering passes until they opt in"* -- and both are true of today's
code, the first for a language and the second for the platform-only flags. A
declaration is where that gets decided once, and the decision is the second:
**a capability not written is not held.**

A new target's file is longer for it, and that is the trade taken knowingly. A
target that forgets a line gets a lowering pass it did not need; under the
other polarity it gets emitted code the host compiler rejects. And it is the
only polarity that survives the plugin this is all for: a command that answers
nothing at all, or that answers a vocabulary older than the compiler asking,
is then a target every pass runs for rather than a target claiming everything.

Both directions stay spellable, because the platform having the last word is
load-bearing today: Go withdraws `ternary`, `listLambdas` and `asyncCalls`, and
html takes all three back, since the language emits the server half of route
mode and not the markup. `#[codegen.can]` on the platform overrules
`#[codegen.cannot]` on the language, per capability. One capability named both
ways on one declaration is an error rather than a precedence rule nobody can
remember.

`AllFeatures()` retires with the polarity, and so does the case it documents
in its own comment: `AsyncSpawn` is *"deliberately absent"* from it, because a
language that has not registered the emitter must not claim the capability.
Under the new polarity that is not an exception -- it is what every capability
does.

### Two grains, two carriers

| grain         | carrier                                            | readable from          |
|---------------|----------------------------------------------------|------------------------|
| whole target  | the build node: `component html(…) build.platform` | before any pass        |
| per primitive | the `#[intrinsic]` component                       | after `passInlinePure` |

The second row is the constraint the whole design turns on, and it belongs in
`sngl:x/codegen`'s package comment because it is the one thing a future
capability can get wrong in silence. "Which primitive does this node become"
is not a question until the overrides have been substituted, which is
`passInlinePure` -- slot 22 of 33, and ungated, so the answer exists on every
target from there on. A capability-gated pass earlier than that can ask the
first row only.

### The whole-target half

The build node is the carrier, and it costs no new lookup path: `ir.Output`
already holds `LangComp` and `PlatComp` as `*ir.Component`, so
`internal/build.Emit` reads the capabilities off the output it is already
holding. It also puts a target's capabilities beside its build options, which
is the other half of what that declaration already says about itself.

```sngl
#[codegen.cannot(ternary, listLambdas, asyncCalls, asyncReactive)]
#[codegen.can(asyncSpawn)]
component go(
    goVersion string,
    …
) build.language {}
```

The merge is the rule the Go code already follows, stated rather than
implemented: the language answers, the platform overrides per capability.

One caller changes shape rather than moving.
`codegen.PlatformRendersViewStatically` takes a platform and a language *by
name*, which is no longer enough to answer -- the answer is on a declaration,
and finding it needs the package. Its single caller is
`internal/optimize/optimize.go`, which holds the `*ir.Package` and so holds
`Outputs`; the function becomes one that takes the `*ir.Output`.

### The per-primitive half

The placement the last write-up proposed -- a mark on each
`component shapes.circle[platform]` -- is wrong, and the source says why:
there are seven shape overrides per platform on three platforms, which is
twenty-one copies of one fact and an agreement rule to invent for when two
disagree. What the source also says is where the fact actually belongs. Every
canvas platform funnels its **entire** shape family through exactly one
intrinsic component, itself a member of the family:

```sngl
#[intrinsic("html:draw")] component draw(@draw DrawEvent) shapes.shape  // html
                          component draw(@draw DrawEvent) shapes.shape  // gtk4
                          component draw(@draw DrawEvent) shapes.shape  // android
```

Every `shapes.X[platform]` override lowers to that node. So the mark on the
primitive is one statement of one fact, beside the implementation, with nothing
to keep in agreement.

**That retires "per family" as the grain.** The capability is per primitive,
and a family-level question is answered by the primitives its members lower
to. One-per-family is a property of `shape` and not a law -- android's widget
family has `Column`, `Row`, `Text` and `Spacer`, fyne's has `Widget`,
`Container` and `Wrapper` -- and html is the case that settles it, declaring
`element`, which retains what it renders, beside `draw`, which does not. One
platform, one family, two answers. A grain that could not hold both would be
answering the wrong question.

The two other placements are worse for one reason each. The tree struct --
`#[tree.kind] struct shape` -- is one declaration for every target, and whether
a rendered thing can be read back is the target's answer, which was the whole
finding that moved this off `#[tree.kind]` in the first place. The wrapper --
`canvas`, `window` -- is the host and not the thing hosted: it would say that
everything inside a canvas is unreadable, which is true today only because
every shape primitive happens to say so.

**The first capability is readable identity**, which is what the canvas half of
the id barrier asked for: can a handle to a node this primitive rendered be
read back while the program runs. `html:draw` says no; `html:element` says yes.

**What it buys is a pass, not a rule retired.** For a primitive with no
readable identity, `dot.r` is rewritten to the expression the prop was assigned
-- reconstructing the value rather than reading it off a node that will not
exist -- and for one with identity the read is left alone. The window for that
pass already exists: after `passInlinePure` (22) the primitive is known, before
`passShapeDraw` (27) the shape node is still there. Nothing has to move, which
is the check the placement needed before it could be chosen.

So `ir.CrossesTreeFamily` **stays**, and this is a reversal of what section 4
called its load-bearing question. The checker cannot ask a primitive-level
capability at all -- not because of the no-target case that kept window
identification out of section 2, but because at check time no override has been
substituted and the primitive does not exist yet. The barrier is a structural
rule and keeps its subject and its fixture. It retires later or never, once the
reconstruction covers the cases it guards.

### Migration

The safety argument is step 3 and nothing else.

1. `lib/x/codegen/`, the three marks and their enum, handlers in
   `internal/checker/marks_impl.go`, landing on a record on `ir.Component`.
2. `lower.FeaturesFor(*ir.Output)`, falling back to the Go method wherever a
   declaration says nothing -- so the migration is per target rather than a cut.
3. Declare all six platforms and four languages in source. **A test asserts
   source-derived equals method-derived for every registered pair.** Every
   capability is a pass that runs or does not, so a disagreement is a golden
   that moves; the test is what says which ones were meant to.
4. Delete `Capabilities()` from both interfaces and fix the callers that ask
   out of band: `internal/snapshot` (four sites), `internal/playground` (two),
   `internal/build`, and `PlatformRendersViewStatically` as described above.
5. Collapse `Features` and `Caps` into one record, which is the point at which
   `ToLowerCaps`, `Merge`, `String` and `AllFeatures` all go. Doing it while
   the vocabulary is being moved anyway is cheaper than doing it twice; doing
   it *before* step 3 would move the equivalence test's goalposts, so it is
   last.

### What it does not do

It does not make a plugin a configuration file. After this, a target is still
compiled in for its intrinsic emitters, its model emitter, `PackageFS`,
`SupportedLangs` and the four optional interfaces -- one method of about ten,
and the only one that is pure data. Filed as #253, which lists what is left and
the four questions that need answers before that is a plan rather than a
direction.

Two questions this leaves open on purpose:

- **What the second per-primitive capability is.** One real second user should
  be found before the shape of the per-primitive record is fixed; readable
  identity alone would be better as a boolean than as a vocabulary.
- **Whether `option<T>` from section 1 ever becomes readable.** Section 1
  refuses every read of a counted handle because no target can act on one. A
  capability saying a target can is the thing that would let one answer, and it
  is the same capability as readable identity asked of a window rather than of
  a shape -- so the two meet here rather than drift, but nothing in this plan
  makes them meet yet.

## Carried forward, unfixed

Each was reproduced on `main` and is listed on !169.

- **A window loop does not compile on a host-language target.**
  `for var it = items { window #page(title=it) { … } }` on bubbletea is not
  unrolled, `ir.Owners` lifts the window out of the loop, and the body keeps a
  read of `it` that nothing declares.
- **`expandForWindows` is vestigial.** Replacing its unroll with a panic leaves
  every golden and all 165 CLI scripts green; the only test that fires it is
  `TestOptimize_PropPropagatesAsConst`, whose `component main node` holds no
  window.
- **Two windows each declaring `var n`** check clean and emit `n int` twice in
  one bubbletea Model.
- `inlinable()` tests `len(comp.Body) == 0` where it means `comp.Bodyless`; a
  route-mode func that mutates state carries DOM patches;
  `internal/optimize/shake.go` wants `ir.Walk`; the package body has no
  `LocalRefs`; an undefined name in a shape prop leaks
  `remote/http/http.sngl:17:30: operator >= not defined for int and float`.

## Rules that bite

- **This file is a build gate.** `go tool verify` runs `mdox fmt --check` over
  every markdown file `findMarkdownFiles(".")` discovers, so a scratch doc at
  the repo root fails CI on a missing blank line. Run
  `go tool mdox fmt --soft-wraps PLAN.md` before committing.
- **The binary proxy resolves `./cmd/sngl` against cwd.** Build out of tree and
  *run* out of tree, or a "does this fail when reverted?" check compares HEAD
  with itself and says ok.
- **A fixture whose subject was kept alive by something being deleted has lost
  its subject.** Edit the program to use the thing rather than recording the
  loss.
- **`go test ./internal/checker/` does not evaluate a new `// ERROR(check)`
  fixture.** The root package's `TestFixtures` does.
