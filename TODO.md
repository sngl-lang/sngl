# TODO

## Style fields, translated in SNGL

P-1's third part, on !257's branch (`refactor/android-select-state`), which
already lets a target package's override hold state, writes android's select
as an override, takes every gtk4 setter from GIR, and has `match` merged in.

### Where it stands

Each platform's Go switches on `ui.Style` field names, and drops every field
it does not name with no diagnostic. On android, bubbletea and fyne the work
is done twice: a Style written as a literal is mapped field by field in Go,
and a Style held in a value goes through a runtime helper the Go writes out
(Kotlin `Modifier.snglStyle`, Go `__styleLipgloss`, `__styleTheme`). The two
copies disagree:

- android's helper drops flex, fontFamily, borderRadius and button colours,
  and maps textAlign center/right only.
- bubbletea's helper renders `opacity=1` faint.
- All three helpers read `.px` and ignore em and rem.

What each platform answers today:

| Platform  | Fields it maps (of 52)                               |
|-----------|------------------------------------------------------|
| html      | all, in Go (htmlutil); stays there, see step 5       |
| android   | done in SNGL (style.sngl); `unstyled` lists the rest |
| bubbletea | done in SNGL (style.sngl); `unstyled` lists the rest |
| fyne      | done in SNGL (style.sngl); `unstyled` lists the rest |
| gtk4      | done in SNGL (style.sngl); `unstyled` lists the rest |

The places to read first: `codegen/iterate.go` (`NodeStyleFieldsOf`, which
decides literal vs held), `codegen/measurement.go`, android's finished
`lib/platform/android/style.sngl` and bubbletea's as the pattern, fyne's
`spec.go`, `layout.go`, `theme.go` and `pkg/go/fynetheme`, gtk4's
`lib/platform/gtk4/style.sngl` and `pkg/go/gtk4rt/look.go`,
and `internal/htmlutil/style.go`.

### The design

No new language feature. Each platform package translates a Style with a
`const func` written in SNGL over the host declarations it already has --
`#[kt.native]` Modifier and TextStyle calls on android, `#[go.native]`
lipgloss methods on bubbletea, fynetheme on fyne -- and every override calls
it on its `style`. `match` reads an enum field (fontWeight, textAlign) into
the host's constant. One function serves a literal and a held Style alike, so
the two copies above become one. html is the exception (step 5).

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
5. **html** stays in Go until a Style can say "unset". Tried and dropped: a
   `css` function in SNGL answered every field, but a function of a Style
   cannot tell a written `0px` from an omitted one, and on html, where the
   browser has defaults of its own, that is a loss rather than a no-op --
   `padding=0px` on a card, `alignItems=start`, `display=flex` on a text,
   `listStyleType=none`, `fontWeight=normal` on a header cell and black
   would each write nothing. A style reading state would also ship with none
   of its static CSS in the markup, since the whole call runs where the page
   does, and a held literal carries all 52 fields. Folding the function per
   node per page cost the docs site 35% more allocation (12.6 GB against
   9.3), most of it the interpreter rebinding an environment per call. The
   way back is step 7, then a fold cache keyed on a const call's arguments.
6. The coverage test across all six, and CLAUDE.md's platform sections.
7. **A Style that can say "unset".** Every style function reads a field at
   its type's zero as unset, so a written zero and an enum's first member are
   lost: `alignItems=start` stretches on fyne and gtk4, `textAlign=left` leaves
   a fyne button centered, an explicit `#000000` is unset everywhere, a side
   written `0px` beside a non-zero whole takes the whole, and html cannot move
   at all (step 5). An `unset` member leading each Style enum, as
   `markup.SpanStyle`'s `inherit` does, answers the enums; a measurement and
   a color need option fields or an unset value of their own. With it, a
   JavaScript struct literal can write every field
   (`js_struct_omitted_fields`), since nothing would read a field's presence
   any more.

### Found on the way, not yet fixed

- A literal Style's Modifier is built at run time on android,
  `styleModifier(Style(…))`: the optimizer splices single-expression
  functions only, and the Modifier builder is a block.
- android's `unstyled` still lists fields Compose can express: `gap`,
  `alignItems` and `justifyContent` (Column and Row arguments), `maxLines` and
  `whiteSpace` (Text arguments). Each would put an argument on every node.
- On android a Column the Go writes rather than an override -- a window's
  content arriving through a slot, a nav page of several nodes -- provides no
  `flexShare`, so a `flex` child there fills the screen; a page under an hbox
  inherits the Row's closure. Both took a Column weight before.
- A side written `0px` beside a non-zero whole (`{padding=16px, paddingTop=0px}`) reads as unset, so it is 16 on android and 0 on html: zero
  is unset in every Style.
- A Go struct literal leaves an omitted enum field `""`, not the first
  member: `S{n=1}` then fails `s.t == Tone.calm` on every Go target, where
  the interpreter passes. bubbletea's style.sngl never compares against a
  first member for that reason. Writing the member in every literal would
  put twelve fields in each Go Style; spelling the first member `""` would
  change what `"{e}"` prints.
- The interpreter, and the fold, print a color as `{r = 51, g = 102, b = 153, a = 255}`; every compiled target prints `#336699`.
- A struct spread in a library function is not lowered on a target withholding
  `structSpread`: LibFuncs brings the function into the package after
  NoStructSpread has run.
- On bubbletea a `flex` child is joined at its own size under an `if` or a
  `for`, being part of what that statement renders, and where it holds a
  loop's focus stop or canvas, whose count would come after its later
  siblings'. `justifyContent`, `overflow`, `whiteSpace` and `display` are
  listed as not answered yet.
- On clean main, `ui.select` does not compile on bubbletea under a `for`
  (`too many arguments in call to m.widget0Copy`), nor on fyne or gtk4 under
  a reactive `if` (`undefined: o`; a missing `c.__n1`).
- passImplicitState refuses a call site that binds one two-way prop and
  leaves another unbound.
- android's select honors none of `disabled`, `multiple`, `@focus`,
  `@blur`.
- On fyne a widget added to a container already under a Box's theme keeps
  the theme it had until that Box refreshes, Fyne's ThemeOverride rule; a
  render slot appending into a styled vbox meets it.
- On fyne an explicit `#000000` is unset, as the color zero is, and so are
  `alignItems=start` (stretches) and `textAlign=left` (a button stays
  centered): each is its type's zero.
- On gtk4 `flex` shares the free space evenly whatever the weights, and
  `justifyContent` is listed as not answered yet. A `fontSize` is written in
  points, as Pango reads a span's, so 1em is GTK's default text, while every
  other length is pixels. A Look's CSS is at application priority on the
  widget's own node, so a button given a `background` loses the theme's hover
  and press feedback, and a card's border is drawn beside the one Adwaita draws
  on the frame's border node; a popover's `background` paints its body and
  not its arrow.
- The shake keeps a library body reachable through a node handle's component
  type, so a program holding `#d` on a datepicker keeps `LookOf` and its
  helpers on gtk4 though nothing calls them.
- A JavaScript struct literal writes only the fields it names, so an omitted
  one reads undefined where Go and Kotlin read its zero
  (`testdata/js_struct_omitted_fields.txtar`). Writing every field is a
  small change in the JS emitter, but html's held-Style helper reads a
  field's presence as having been set, so it waits on step 5.
- html writes a Style field the build cannot fold with `setProperty` after the
  static attribute, in the literal's field order: `{padding=p, paddingTop=4px}`
  with `p` state resets the side on every write.
- The optimizer interprets a const function per call, and a lambda call
  snapshots the environment: a `.filter(func…)` in a function folded per node
  was most of a 7 GB increase on the docs site.

## The Pokédex as an Android app

On a phone `examples/pokedex` reads as a web page drawn in Compose. There is a
row of two selects over the content, a "‹ Back" button, tabs that change only
when tapped, and panels cut off at the right edge. The goal is an app that
looks like one built in Android Studio. Each gap below is a library gap that
other programs will hit too, so each is fixed in `lib/` and the example is
the first user.

### Where it stands

- **No app bar.** `Pokedex` puts `ui.select #languagePick` in an hbox above
  the stack, and `homePage` puts `#dexPick` above the search box. android's
  `ui.window` is a bare `Screen`: `title` and `favicon` are not read there,
  and `output(icon=…)` goes only to the launcher. There is no Scaffold and
  no TopAppBar. `detailPage` draws its own `#backButton`, though the system
  back already pops the NavHost.
- **No setting outlives the process.** `language` starts at
  `i18n.defaultLocale()` and `dexName` at `"national"` on every launch.
  Nothing in `lib/` persists a value. The persistence example in
  getting-started uses a `go:` import, which android miscompiles
  (ISSUES.md 5).
- **Tabs do not swipe.** android's `ui.tabs` is a TabRow over the selected
  panel, with no pager.
- **No target scrolls sideways.** `ui.scroll`'s `direction` and the `scroll`
  prop on `ui.hbox`/`ui.vbox` are declared in `lib/ui/layout.sngl`, but no
  override reads them. `SnglScroll` is `verticalScroll` only, and html's is
  `overflow=auto` in both directions by accident. The panels that overflow a
  360dp screen are the Sprites rows (4 × 112px plus gaps), the Evolution
  rows (a branching chain like Eevee's), and the Moves table.

### The design

1. **An app bar the window draws.** The window is already "the chrome every
   page shares" (`lib/ui/window.sngl`), so the bar belongs to it, not to a
   new component the program places.
   - `ui.window` gains a `menu ...component ui.menuItem` slot: the window's
     menu, whose top-level entries carry the submenus.
   - android draws `title`, `favicon` (the output's icon where none is
     given) and the menu as a Material3 `Scaffold` with a `TopAppBar`. Each
     top-level entry is an action whose submenu is a `DropdownMenu`.
   - gtk4 draws the menu as its own (a `GMenu` behind the header bar's menu
     button, or the application menu bar), and win32 will use its menu bar
     the same way. fyne uses its main menu, html a `<header>` over
     `ui.menu`, and bubbletea a title line.
   - `ui.menuItem` gains `checked bool` so a submenu can mark the language
     or Pokédex chosen. `ui.menu` and `ui.menubar` are empty overrides on
     android and fyne today, so both get real ones.
   - The up arrow is the window's, from two props:
     `ui.window(canGoBack=pages.canGoBack(), @back { pages.back() })`, with
     nav adding `stack.canGoBack()`. On android the system back runs `@back`
     while `canGoBack` holds and closes the window otherwise, as the NavHost
     does now. `sngl:ui` cannot name `nav.stack`, since nav imports ui, and
     `@back` may mean more than a pop (leaving a search). The Pokédex folds
     `Pokedex` into its window so `pages` and `language` are in scope there,
     which the `menu` needs anyway. This is a stopgap; see *Window and nav,
     later*.
2. **Settings as state: `sngl:settings`.** A component that binds a var,
   following the pattern `poke.pokemonRetrieve(:value=…)` already uses:
   `settings.kept(key="language", :value=language)`. On mount it loads the
   stored value into the var if one exists, and it writes the var back on
   every change. The var keeps its own initializer as the default, so
   `var x = expr` still means what it means. `T` is anything `sngl:json`
   can encode.
   - Where a value is kept is a `settings.store` context. Its default is the
     platform's own place for the app, named by the `output`:
     - android: SharedPreferences, or DataStore if a coroutine load can be
       gated before the first frame.
     - html: `localStorage`; on a `--lang go` server, a cookie.
     - fyne: `App.Preferences`.
     - gtk4 and bubbletea: a JSON file under `$XDG_CONFIG_HOME/<output name>`.
       GSettings needs an installed schema, which a `go build` binary does
       not have.
     - The interpreter: memory, per run.
   - A program, or a test with `t.setContext`, provides another store: a
     scope of its own, or a seeded one in memory. The trust rules in
     `design/trust.md` decide whether writing a file needs a grant.
3. **Tabs that swipe and stay loaded.** A tab's panel is mounted with the
   tabs and kept mounted while another is selected; selecting one shows it.
   That changes `ui.tabs` in `lib/ui/nav.sngl`, whose body and doc say a
   panel is "shown while this tab is selected" and render only that one,
   and every override that does the same. The doc may say that a platform
   which cannot keep a panel loaded mounts it on selection, but keeping it
   loaded is what is preferred.
   - android's `ui.tabs` becomes a `HorizontalPager` synced with
     `selected` under the same TabRow, with `beyondViewportPageCount` set to
     every page so none is disposed.
   - html keeps hidden panels in the DOM and swipes with pointer events.
     The desktop targets keep their notebooks' pages, which they already
     hold.
   - In the Pokédex, every tab's requests then go out when a detail page
     opens. Moves is the costly one: a `moveRetrieve` per move, plus their
     types. Measure that against the recorded runs, and decide whether the
     moves table should load its facts as rows come into view.
4. **Horizontal scroll, implemented everywhere.**
   - `ui.scroll(direction=)` uses `horizontalScroll`/both on android
     (`SnglScroll` takes the axis, and a sideways `@reachEnd` measures
     width), `container.NewHScroll` on fyne, the scrolled window's policy
     on gtk4, and `overflow-x` or `overflow-y` on html. bubbletea cuts
     content off as it does today.
   - The `scroll` props of `ui.hbox` and `ui.vbox` are deleted. A
     scrolling box is a `ui.scroll` around it. vbox's `refreshing` and
     `@refresh` stay.

### Order

Do each fixture first, and confirm it fails with the change reverted. One
branch with one commit series per step, then re-record the runs that change.

Each step is its own session, on this branch and MR !279. A session ends by
marking its step done here and in the MR's checklist, recording what it found
under the step, and handing over the prompt that starts the next step's
session.

1. **Horizontal scroll.** Write `testdata/scroll_direction*.txtar` for each
   target, plus an android recorded run. Then wrap the Sprites, Evolution
   and Moves panels in `ui.scroll(direction="horizontal")`.
2. **Settings.** Write the spec section and `design/` note first. Then
   `lib/settings`, the interpreter, and the targets in the order above, with
   a `sngl test` that seeds a setting and reads it back across a remount,
   plus a CLI script test that runs a generated program twice. Then
   `language` and `dexName` in the Pokédex become kept settings.
3. **Tabs.** The `ui.tabs` doc and body first, with a fixture that holds a
   panel's state across a switch away and back. Then android's pager, html,
   and a recorded android run that swipes.
4. **The app bar.** Write the `ui.window` prop and slot in the spec, then
   implement android, gtk4, html, fyne and bubbletea, and the up arrow. In
   the Pokédex, move the language and the dex choice into the window's `menu`, delete
   `#backButton`, and set the title to `pages.current.title`; detail's
   title should be the species' name rather than "Pokémon", which needs a
   page title that can be computed from its params. The six
   `#languagePick`/`#dexPick`/`#backButton` uses in `pokedex_test.sngl`
   move with them.
5. Re-record the Pokédex's android, html, gtk4 and fyne runs and its
   screenshots, and update `examples/pokedex/README.md`.

## Window and nav, later

How a window and `sngl:ui/nav` meet needs a redesign of its own. The current
shape was written with static html foremost -- a page is an address, a link
an `<a href>`, the window's title read off `pages.current` -- and the other
targets fit around it: android's NavHost, desktop windows that want the stack
in a sidebar or a header bar, a terminal with one screen. Each platform wants
the navigation in a different place and handles going back differently. The
`canGoBack`/`@back` props above are what the Pokédex needs until then, not
the answer; a redesign should decide whether a window finds its stack itself,
whether a stack can own the chrome on targets where that is the idiom, and
what html's addresses become when they are one target's concern rather than
the model.

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
