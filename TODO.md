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

`style-unset` is MR !280, pushed through the commit that wrote this, with
`go tool verify` passing. The part before this one: a test's write redraws
what reads it (`__wrote_<v>`), a run-time writer is updated once, a partly
held Style's color folds with its alpha, android's written Columns give a
flex child its share (`LocalFlexShare`), bubbletea grows a child under an
`if` or a `for` and passes over hidden stops in loops and boxes, and the
select's cross-target gaps are #295. The prompt for the next part:

```text
Continue on branch style-unset in the primary checkout (no worktree), MR !280.
If !280 has merged, branch target-agreement from a fresh origin/main instead
(merge, never rebase). Look at `git status` and `git log --oneline -8` first.

Read TODO.md's "Found on the way, not yet fixed" under "Style fields,
translated in SNGL". This part is where a target still shows something other
than the interpreter, or does more work than it needs:

1. On android a child a caller writes into the slot of a vbox inside a
   component built at run time reads the caller's flexShare, not the vbox's:
   the composable is handed the caller's `__ctx_flexShare`, and the slot's
   content is the caller's lambda. Read design/language.md's *Slot content
   stands under the providers its component wraps the slot in* and say
   whether the vbox should provide LocalFlexShare as the written Columns do,
   or collectSlotEnv should hand the content the callee's value, and ask
   before building it.
2. On bubbletea a growing child holding a loop's focus stop or canvas is
   still joined at its own size, since the closure it renders in would count
   its copies after its later siblings'.
3. On fyne a widget added to a container already under a Box's theme keeps
   the theme it had until that Box refreshes; a render slot appending into a
   styled vbox meets it.
4. The shake keeps a library body reachable through a node handle's
   component type, so `#d` on a datepicker keeps gtk4's `LookOf` and its
   helpers though nothing calls them.
5. The optimizer interprets a const function per call, and a lambda call
   snapshots the environment: a `.filter(func…)` in a function folded per
   node was most of a 7 GB increase on the docs site. Measure it first
   (design/performance.md).

For each, write the fixture first -- a testdata/*.sngl, a golden, a Go run
test, or a cmd/sngl/testdata/*_runs.txt that runs it on every target it
concerns -- and confirm it fails before the fix. Read the design/ file for
the area before changing it, and update it and TODO.md as each lands. One
commit per item plus a rerecord commit. Fix bugs you meet on the way where
the fix is clear, and say so; ask where it touches what the language means.

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
  content arriving through a slot, a nav page of several nodes -- provided no
  `flexShare`, so a `flex` child there filled the screen. It provides one
  through Compose's `LocalFlexShare` now, which the default reads
  (`android_flex_written_column_runs.txt`). Still open: a child a caller
  writes into the slot of a vbox inside a component built at run time reads
  the caller's share, not the vbox's -- the composable is handed the
  caller's `__ctx_flexShare`, and the slot's content is the caller's lambda.
- The interpreter, and the fold, printed a color as
  `{r = 51, g = 102, b = 153, a = 255}`, html as `[object Object]`, and Go as
  `rgba(…)` where translucent. A color's string is its `hex()` everywhere now
  (Jonathan's call, `color_string_runs.txt`). On the way: an interpolated
  literal, `"{1}"` or `"{#fff}"`, checked to "" and `sngl fmt` wrote it back as
  text (`testdata/interpolate_literal.sngl`); android declared no `Color` for
  a component's color state; and a Go test file calling `.Hex()` imported
  snglcolor unused.
- A struct spread in a library function was not lowered on a target
  withholding `structSpread`: LibFuncs brought the function into the package
  after NoStructSpread had run. Fixed by running NoStructSpread after it
  (`internal/lower/lib_funcs_test.go`). Every other pass before LibFuncs still
  never sees a library helper's body; nothing in `lib/` needs one of them yet.
- On bubbletea a `flex` child was joined at its own size under an `if` or a
  `for`, being part of what that statement renders; each node the statement
  renders is a part of the Layout now (`style_bubbletea_flex_under_if_runs.txt`).
  Still so where it holds a loop's focus stop or canvas, whose count would
  come after its later siblings'. A focusable node displayed none in a loop
  slot, and one inside a container displayed none, was still counted as a
  stop; neither is now (`TestFocusPassesOverHiddenStopsInLoopsAndBoxes`).
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
  (`page_write_kept_instance.txtar`) -- so the instance showed what it read
  when it was built, which the interpreter did not share. A write reaches
  every live instance reading it now, through a refresh each carries and the
  page's `__refreshPage_<v>` (`page_write_reaches_instance_runs.txt`,
  Jonathan's call over a hidden prop per page var). A writer that is itself
  such an instance updated its own readers twice, once by its own write's
  updates and once through the page; the refresh alone reaches it now
  (`page_write_by_reader_once.txtar`).
- A test reading a looped `#id` whole, `c.title.length()`, did not build on
  any compiled target (`c.title undefined` on Go, `undefined.length` on
  html); `c.title[1]` did. A count is the loop's own now, and any other whole
  read is refused at the read (`test_reach_looped_id_whole_runs.txt`); a count
  of a loop under an `if` is refused too, where it could be the condition's.
  On the way: a test's write to a spliced instance's state re-rendered a page
  slot html had not published the anchor of (`__slotAnchor_2 is not defined`).
- passImplicitState was noted refusing a call site that binds one two-way prop
  and leaves another unbound. It does not, on any target
  (`two_way_one_bound_runs.txt`, a program component's `:a=x` beside an
  unbound `b`); no `sngl:ui` component has two, and `async.query(:value=v)`
  was answered already. What failed was a test reading the bound one through
  the instance, `c.p.a`, which no compiled target named; it reads what the call
  site bound now.
- Done: android's select, noted honoring none of `disabled`, `multiple`,
  `@focus`, `@blur`. `disabled` it honors (the field and the click, since
  `8577daae0`), and a test's `change` chooses on a disabled select on every
  target, the interpreter included. The rest is no gap of android's and is
  filed as #295: `multiple` is honored by no target, and a select's `@focus`
  and `@blur` reach a handler only on html.
- On fyne a widget added to a container already under a Box's theme keeps
  the theme it had until that Box refreshes, Fyne's ThemeOverride rule; a
  render slot appending into a styled vbox meets it.
- Done on gtk4 (branch `fix/gtk4-look`): a box gtk4rt lays out asks its
  children as GtkBox does and keeps an `orientation` written after it took
  the box over (`pkg/go/gtk4rt/flex_test.go`); a widget `gtk4rt.New` makes
  grows by its own expand and never by what it holds; a `fontSize` is pixels
  and an em 16 of them (`testdata/gtk4_font_size.txtar`); a styled button
  keeps its hover and press feedback; a popover's `background` paints its
  body and its arrow, where it painted neither, its body being a widget of
  its own; and a clickable card's border replaces the theme's frame border
  (`testdata/gtk4_card_border.txtar`). A plain card's border already did:
  GTK 4 draws a frame's border on the frame's own node, and has no `border`
  node (`pkg/go/gtk4rt/paint_test.go`). Still so under the `cgo` option,
  which sets `orientation` and makes widgets through GIR's own calls: the
  turn is lost and expands carry up as GTK carries them.
- The shake keeps a library body reachable through a node handle's component
  type, so a program holding `#d` on a datepicker keeps `LookOf` and its
  helpers on gtk4 though nothing calls them.
- html wrote a Style field the build cannot fold with `setProperty` after the
  static attribute, in the literal's field order: `{padding=p, paddingTop=4px}`
  with `p` state reset the side on every write. Each side is a longhand of its
  own since the side translation, which already fixed it; `style_side_zero_runs.txt`
  pins it now (it fails on main). On the way: a node carrying the program's own
  `#id` was updated twice per handler write on html
  (`html_handler_updates_named_node_once.txtar`).
- A test's own write of its instance's state, `c.p = 8px`, redrew nothing on
  fyne, gtk4 or html: a field write on the Model or the page's `state`, where
  a handler's is followed by its readers' updates. It calls the page's
  `__wrote_<v>` after it now, the updates a handler's write runs, written
  where a handler's are (Jonathan's call over the Model's setter;
  `test_write_redraws_runs.txt`). On the way: a test writing a page var a
  node reads did not build on fyne or gtk4, the updates spliced into the test
  file naming a node it has none of; and a color literal in a Style the build
  can read only part of folded without its `a`, so html's static style said
  `#33669900` for `#336699` (`testdata/style_partly_held_color.txtar`).
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
