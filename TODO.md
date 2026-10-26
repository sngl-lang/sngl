# TODO

**When a task here is completed**, the session rewrites the *Next part* of its
section with the prompt for the part after it -- the branch, what to read
first, the items, the fixture each wants, the verify and push steps, and this
same instruction -- and ends by giving that prompt to Jonathan.

## Style fields, translated in SNGL

The places to read first: `codegen/iterate.go` (`NodeStyleFieldsOf`, which
decides literal vs held), `codegen/measurement.go`, the platforms'
`lib/platform/<name>/style.sngl`, fyne's `spec.go`, `layout.go`, `theme.go`
and `pkg/go/fynetheme`, `pkg/go/gtk4rt/look.go`, and html's
`css` in `lib/platform/html/html.sngl` and `codegen/platform/html/stylewrite.go`.

### The design

No new language feature. Each platform package translates a Style with a
`const func` written in SNGL over the host declarations it already has --
`#[kt.native]` Modifier and TextStyle calls on android, `#[go.native]`
lipgloss methods on bubbletea, fynetheme on fyne -- and every override calls
it on its `style`. `match` reads an enum field (fontWeight, textAlign) into
the host's constant. One function serves a literal and a held Style alike. html is the exception (step 5).

Fields a parent consumes -- `flex`, fyne's `margin`, Compose's
`Modifier.weight`, which exists only in a Row or Column scope -- are answered
by a context: vbox and hbox provide their axis, and the child's function reads
it. On Compose the call is then emitted inside `Row { }`, where `weight`
compiles. On fyne the layout reads each child's weight and margin off the
child's widget rather than taking them as arguments, a fynelayout change.

gtk4 gets a CSS setter in gtk4rt, exposed as a prop of the GIR widget
declarations, so reactivity re-sets it when the style changes; an effect is
for what the host adds and removes with the node, such as a CssProvider.

**Silence is an error, as a repo test.** Each platform declares the fields it
cannot express, with a reason, in a `const` list. A test in `lib/` holds the
fields its style functions read, together with that list, to every field of
`ui.Style`, so adding a field fails each platform until it answers. A
program's write of a field its target lists does not warn (Jonathan's call):
only a literal shows the write, a multi-target build would warn per target
with no way to say it is meant for one, and a field not answered yet is the
compiler's gap rather than the program's. A lint the build opts into may come
later.

When a platform's translation is SNGL, the Go it replaces goes: the field
switches, the held-Style helpers, and the per-node routing that the overrides
now say themselves (android's `modifier=` / `colors=` / `shape=` / Text
`style=`; bubbletea's flow split).

### Order

Each is a commit series on the branch; one MR, so CI runs once.

1. **android.** Fixture first: the audit's repro, `margin` and `borderRadius`
   written on android, both dropped today. Then `modifier`, `textStyle`,
   `buttonColors` and `shape` functions in `lib/platform/android/style.sngl`,
   the axis context on vbox/hbox, the overrides calling them, and the Go
   above deleted. Done. The axis is a closure rather than an enum: vbox
   and hbox provide `flexShare`, a `func(float32) Modifier` written in the
   Column's or Row's content, so a component built at run time -- a
   composable of its own, outside that scope -- still calls `weight` through
   it. Nothing in style.sngl reads a composable: under `--lang go` a ternary
   splits a function into statements no splice takes, so the theme's text
   style and a button's default colors are read in the overrides.
2. **bubbletea**, the lipgloss chain; a `Widget`'s style answered or listed.
   Done: `lib/platform/bubbletea/style.sngl` over `#[go.native]` lipgloss
   declarations, and a Widget renders inside its look. `flex` is read by the
   Layout off its child: the override hands each primitive `grow`, and a
   Layout with a growing child renders that child in a closure once the fixed
   ones have said what they leave of its extent -- its look's width, else
   its parent's, else the terminal's (`tui.Join`). A context was no use here:
   a share depends on the siblings' rendered sizes, which only the Layout
   sees.
3. **fyne**, with the fynelayout change for flex and margin. Done:
   `lib/platform/fyne/style.sngl`'s `lookOf` builds a `Look`, a
   `#[go.native]` record that is `fynelayout.Look`, in plain SNGL, so a
   literal Style folds to a Go literal and the interpreted host is handed
   the same record. A node whose look asks for anything is shown in a
   `fynelayout.Box`, which draws the margin, border, fill and padding and
   puts the widget under a theme of its own only where the look sets one.
   `flex` is read by the parent off the child: every vbox and hbox is
   fynelayout's `Weighted` layout, which takes each child's share from its
   Box, and a margin is the child's own Box's.
4. **gtk4**, with the gtk4rt CSS setter. Done: `lib/platform/gtk4/style.sngl`'s
   `lookOf` builds a `Look`, a `#[go.native]` record that is `gtk4rt.Look`,
   declared in the `gir:` package beside the widgets and carried by each as
   a `look` prop; the CSS text is plain SNGL, so a literal Style folds to a
   constant. `gtk4rt.SetLook` answers it in both modes: a CssProvider per
   styled widget, owned by the widget, so no effect; the size request, a
   label's alignment and lines, overflow, visibility and cursor; and what an
   earlier Look took, given back when a later one leaves it unset. `flex` is
   expand along the axis of the box the widget sits in, read off the parent
   when the widget lands there, and GTK splits the free space evenly: a
   weighted share is a layout of gtk4rt's own. A box's `alignItems` is put
   on each child it holds, later ones included.
5. **html**. Done: every element's `style` (and a flow's `classStyle`) is
   the `list[Declaration]` html.sngl's `css` answers for a ui.Style; each
   override writes `style=css({…, ...style})` and a program writing a raw
   element writes `style=html.css({…})`. The fold reduces each declaration
   the build can read to text in the markup; one only the page knows is a
   setProperty, and a list held whole goes through `_snglCSSText`.
6. The coverage test, and the platform notes. Done:
   `lib/style_coverage_test.go`'s `TestEveryPlatformAnswersEveryStyleField`
   holds the ui.Style fields each platform's package reads, with its
   `unstyled` list (html's empty), to every field, and refuses a listed field
   the platform reads. `none` draws nothing and is not held to it (Jonathan's
   call). The notes are design/architecture.md's *Platform Details*, with a
   rule in CLAUDE.md.
7. **A Style that can say "unset".** Committed on `style-unset`
   (`da4a62b84`, `8232b1be9`), with two things since decided against: see
   *Next session*. `ui.Style`'s lengths, colors and `flex`, `opacity`,
   `zIndex` and `maxLines` are options, null when unset; each of its enums
   leads with `unset`; a string is unset empty. Each platform's style.sngl
   takes a side as the first of its own field, its axis's and the whole box's
   that is set, so `{padding=16px, paddingTop=0px}` is 0 on top everywhere
   (`cmd/sngl/testdata/style_side_zero_runs.txt`), and `alignItems=start`,
   `textAlign=left`, `fontWeight=normal` and `#000000` each mean what they
   say. An omitted struct field reads its default on every language
   (`struct_omitted_fields_runs.txt`).

### Next part

`style-unset` is MR !280, pushed through `04b35ae5f` with `go tool verify`
passing: the order above, the fields below, bubbletea's `display=none`,
gtk4's flex weights and column axis, a head's ternary, and `ui.spacer` as
`flex: 1`. The literal Style fold was decided against (below). The prompt
for the next part:

```text
Continue on branch style-unset in the primary checkout (no worktree), MR !280,
pushed through 04b35ae5f with verify passing. If !280 has merged, branch
generated-code-compiles from a fresh origin/main instead (merge, never
rebase). Look at `git status` and `git log --oneline -8` first.

Read TODO.md's "Found on the way, not yet fixed" under "Style fields,
translated in SNGL". This part is the items where a target's generated code
does not compile or disagrees with the interpreter:

1. ui.select under a `for` on bubbletea (`too many arguments in call to
   m.widget0Copy`), and under a reactive `if` on fyne and gtk4 (`undefined:
   o`; a missing `c.__n1`). TODO found these on clean main; reproduce each on
   the branch first. Read design/instances.md and design/slots.md.
2. On Go, `==` between two structs holding a list does not compile (`struct
   containing []string cannot be compared`), where the interpreter compares
   field by field. Read design/types.md, and how html answers the same in
   design/architecture.md (jsEqualExpr).
3. A struct spread in a library function is not lowered on a target
   withholding structSpread: LibFuncs brings the function in after
   NoStructSpread has run. Read design/types.md on spreads and
   design/architecture.md on the always-on passes.
4. The interpreter and the fold print a color as `{r = 51, g = 102, b = 153,
   a = 255}` where every compiled target prints `#336699`. This is what a
   color's string is: say which you would make it and ask before changing
   the language's side.

For each, write the fixture first -- a testdata/*.sngl, a golden, or a
cmd/sngl/testdata/*_runs.txt that runs it on every target it concerns -- and
confirm it fails before the fix. Read the design/ file for the area before
changing it, and update it and TODO.md as each lands. One commit per item
plus a rerecord commit. Fix bugs you meet on the way where the fix is clear,
and say so; ask where it touches what the language means.

Then run `go tool verify -update` with ANDROID_HOME=/home/claude/Android/Sdk
in the background and don't edit files while it runs. If it fails on stale
generated artifacts (examples/todo, internal/docbrowser), run `go generate`
on them and run -update again. Finish with a plain `go tool verify`, check its
own exit line and its FAIL rows, and push to origin on the branch, never to
main.

When it is done, rewrite TODO.md's "Next part" with the prompt for the part
after it, and give me that prompt.
```

### Fields listed only because they were not done yet

Each answered where its host can express it, with the field off the
platform's `unstyled` list in the same commit.

1. android: `maxLines` and `whiteSpace`, Text's `maxLines`, `overflow` and
   `softWrap` (`testdata/android_text_lines.txtar`). Done. `gap`,
   `alignItems` and `justifyContent` were answered already, as the Column's
   and Row's Arrangement and Alignment.
2. bubbletea: `whiteSpace`, `display=none`, `overflow`, `justifyContent`.
   Done: a `tui.Fit` beside the look, and `tui.Join`'s justify
   (`cmd/sngl/testdata/style_bubbletea_fit_runs.txt`). Not `nowrap` as
   lipgloss's Inline, which also drops the box's padding, border and margin.
3. gtk4: `justifyContent` and `flex` weights, a layout of gtk4rt's own.
   Done: a custom layout manager on a box that asks for either, and a box's
   spacing moved into its look (`pkg/go/gtk4rt/flex_test.go`).
4. fyne: `cursor`, through a desktop.Cursorable box. Done: a pointer area in
   the Box, over the widget and under a container's children
   (`pkg/go/fynelayout/cursor_test.go`).

### Found on the way, not yet fixed

- A literal Style is translated at run time on bubbletea and android, and
  stays so (Jonathan's call): `BoxLook(Style{…}, true)`,
  `styleModifier(Style(…), true)`. fyne's and gtk4's `lookOf` build a host
  record or a CSS string in plain SNGL, which the optimizer interprets and
  spells back as a literal; bubbletea's and android's builders call
  `#[go.native]` and `#[kt.native]` const funcs, and `interpretFunc` refuses
  a body that reaches a native, while the inliner splices a single `return`
  only and never copies a native call read twice (`cheapToCopy`) -- which
  also leaves android's `flexed(m, null, …)` and `columnSpacing(UNSET, Dp(8f))` as calls. Folding them would take a value standing for a host
  call in the const interpreter, or an inliner that folds a block body to
  one expression.
- On android a Column the Go writes rather than an override -- a window's
  content arriving through a slot, a nav page of several nodes -- provides no
  `flexShare`, so a `flex` child there fills the screen; a page under an hbox
  inherits the Row's closure. Both took a Column weight before.
- The interpreter, and the fold, print a color as `{r = 51, g = 102, b = 153, a = 255}`; every compiled target prints `#336699`.
- A struct spread in a library function was not lowered on a target
  withholding `structSpread`: LibFuncs brought the function into the package
  after NoStructSpread had run. Fixed by running NoStructSpread after it
  (`internal/lower/lib_funcs_test.go`). Every other pass before LibFuncs still
  never sees a library helper's body; nothing in `lib/` needs one of them yet.
- On bubbletea a `flex` child is joined at its own size under an `if` or a
  `for`, being part of what that statement renders, and where it holds a
  loop's focus stop or canvas, whose count would come after its later
  siblings'. A focusable node displayed none in a loop slot is still
  counted as a stop (Tab passes over a static one), and so is one inside a
  container displayed none, whose own `focus` record says nothing of it.
- A zero `borderWidth` drew a border on bubbletea, and
  `TestReactiveStyleRunsInTheEmittedProgram` said so on the branch; fixed,
  but how verify passed over it is not known.
- On clean main, `ui.select` did not compile on bubbletea under a `for`
  (`too many arguments in call to m.widget0Copy`), nor on fyne or gtk4 under
  a reactive `if` (`undefined: o`; a missing `c.__n1`). The select shapes
  build and run on the branch; the bubbletea error was a `ui.table` under a
  `for` (#263), fixed (`table_under_for_runs.txt`). On the way: a window's
  handler writing page state a looped instance reads named the instance's
  nodes as the Model's on fyne and gtk4, fixed by giving it no update of them
  (`page_write_kept_instance.txtar`) -- so the instance shows what it read
  when it was built, design/instances.md's missing direction, which the
  interpreter does not share.
- A test reading a looped `#id` whole, `c.title.length()`, does not build on
  any compiled target (`c.title undefined` on Go, `undefined.length` on
  html); `c.title[1]` does.
- passImplicitState refuses a call site that binds one two-way prop and
  leaves another unbound.
- android's select honors none of `disabled`, `multiple`, `@focus`,
  `@blur`.
- On fyne a widget added to a container already under a Box's theme keeps
  the theme it had until that Box refreshes, Fyne's ThemeOverride rule; a
  render slot appending into a styled vbox meets it.
- On gtk4 a box gtk4rt lays out measures height for width only, and
  `orientation` set on one after gtk4rt has taken it over reaches the layout
  GtkBox no longer has. A box with no weighted child is GTK's, so a child that
  grows only for what it holds -- a box holding a widget given `hexpand` --
  still takes room CSS gives it none of. A `fontSize` is written in
  points, as Pango reads a span's, so 1em is GTK's default text, while every
  other length is pixels. A Look's CSS is at application priority on the
  widget's own node, so a button given a `background` loses the theme's hover
  and press feedback, and a card's border is drawn beside the one Adwaita draws
  on the frame's border node; a popover's `background` paints its body and
  not its arrow.
- The shake keeps a library body reachable through a node handle's component
  type, so a program holding `#d` on a datepicker keeps `LookOf` and its
  helpers on gtk4 though nothing calls them.
- html writes a Style field the build cannot fold with `setProperty` after the
  static attribute, in the literal's field order: `{padding=p, paddingTop=4px}`
  with `p` state resets the side on every write.
- On Go, `==` between two structs holding a list did not compile
  (`struct containing []string cannot be compared`), where the interpreter
  compares them field by field. Fixed, with two lists, and two options --
  pointers on Go, so two holding 1 were unequal -- and lists on html, where
  `===` was the array's identity (`struct_list_equality_runs.txt`). A map's
  `==` is still the host's: the interpreter does not answer it either.
- The optimizer interprets a const function per call, and a lambda call
  snapshots the environment: a `.filter(func…)` in a function folded per node
  was most of a 7 GB increase on the docs site.

## `match`, later

`match` is implemented: the rules are in CLAUDE.md's *Match* section and the
specification's *The match statement*. What is left is the longer road.

### Generated code a person would write

The checker turns a match into an if/else-if chain or nested ternaries, which
every target emits unchanged, so the first implementation needed no backend
work. The cost is the shape of the output: Go gets `if … else { if … }`,
Kotlin nested `if` expressions, JavaScript chained `?:`, where each has a
construct that says it -- Go's `switch`, Kotlin's `when`, JavaScript's
`switch` -- and an exhaustive match over an enum is exactly what a compiler
checks a `when` for.

1. Keep the match in the IR: an `ir.Match` statement and expression the
   checker produces instead of the chain, with the subject, the arms'
   patterns and whether the last arm is taken unasked.
2. Lower it to the chain in a pass every target gets unless its language
   declares `#[gen.can(match)]` -- the capability polarity `sngl:x/gen`
   already uses, so a target that has not heard of it keeps working.
3. Teach the walks that ask an `if` what it holds to ask a match the same:
   `ir.TransparentBlocks`, the family, slot, element-ref and `.empty` rules,
   the interpreter, the optimizer's folds, the reactivity pass's render slots.
   Each is a case beside the `if` it already has, and the reason the first
   implementation did not keep one.
4. Emit it natively in Go, Kotlin and JavaScript, view bodies included where
   the platform renders statements (Compose's `when` in a composable).

### Language

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
