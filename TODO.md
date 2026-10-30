# TODO

## The Pokédex as an Android app

On a phone `examples/pokedex` reads as a web page drawn in Compose. There is a
row of two selects over the content, a "‹ Back" button, tabs that change only
when tapped, and panels cut off at the right edge. The goal is an app that
looks like one built in Android Studio. Each gap below is a library gap that
other programs will hit too, so each is fixed in `lib/` and the example is
the first user.

### Where it stands

- ~~**No app bar.**~~ Done in step 4: the window draws the page's title,
  its way back and its menu, which chooses the language and the dex.
- ~~**No setting outlives the process.**~~ Done in step 2: `sngl:settings`
  keeps `language` and `dexName` from one run to the next.
- ~~**Tabs do not swipe.**~~ Done in step 3: every panel stays mounted, and
  android and html swipe between them.
- ~~**No target scrolls sideways.**~~ Done in step 1: every target scrolls
  along `ui.scroll`'s `direction`, and the Sprites, Evolution and Moves
  panels scroll sideways.
- ~~**Tabs look like buttons on three targets.**~~ Done in step 5: gtk4
  draws a stack switcher, fyne an AppTabs and bubbletea a boxed tab bar.

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
   `settings.kept(name="language", :value=language)`. On mount it loads the
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
     The desktop targets hide the panels not shown.
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
5. **Tabs drawn as each platform draws them.** Each target overrides
   `ui.tabs` with its own widget, the panels kept as step 3 keeps them.
   - gtk4: a `GtkStack` under a `GtkStackSwitcher`. A stack holds every page
     it is given, which is the keeping-loaded step 3 asks for.
   - fyne: `container.AppTabs`. Its items are `[]*container.TabItem`, which
     no `Arg` builds out of a SNGL prop -- why fyne draws sngl:ui's body
     today -- so a `fynelayout` helper takes the labels, the selected index
     and the panels and builds them.
   - bubbletea: a tab bar lipgloss draws, each tab boxed and the selected
     one open into the panel under it; bubbles has no tabs.
   - `variant` and `disabled` mean what they do on android and html, and a
     test still chooses a tab by pressing it (`c.strip.change(k)`), so each
     override keeps a node per tab the press rule finds, or the rule learns
     the widget.

### Order

Do each fixture first, and confirm it fails with the change reverted. One
branch with one commit series per step, then re-record the runs that change.

Each step is its own session, on this branch and MR !279. A session ends by
marking its step done here and in the MR's checklist, recording what it found
under the step, and handing over the prompt that starts the next step's
session.

1. **Horizontal scroll.** Done. `direction` is a `ui.ScrollDirection`
   (`vertical`, the default, `horizontal`, `both`); the boxes' `scroll`
   props and `ui.Style.overflow` are gone, a clip being each platform's own
   (html's image frame by `.sngl-image`). Pinned by
   `testdata/scroll_direction*.txtar` (android's run scrolls sideways),
   `testdata/gtk4_scroll_direction.txtar`,
   `cmd/sngl/testdata/scroll_reach_end_sideways_runs.txt` (Chromium) and
   fynelayout's and gtk4rt's sideways tests. gtk4's came through
   `gtk4-declared-protocol`, which merged main and was merged here. Found:
   - A node's `#id` shadows a bare enum variant or a const of the same name
     in its own arguments: `ui.scroll #both(direction=both)` is "cannot use
     scroll as ScrollDirection", which names neither. Fixed: resolution
     passes over the handle while the node's props are checked, so the name
     reads what it shadows, and one nothing else declares is a recursive
     declaration; a handler still reads its node (Jonathan's call). Pinned by
     `testdata/node_id_not_in_own_args.sngl` and
     `error_node_id_in_own_args.sngl`.
   - `ui.scroll`'s `scrollX`, `scrollY` and `showScrollbar` are read by no
     override, as the boxes' `scroll` was. Deleted (Jonathan's call);
     `component_scroll.txtar`, which only wrote the vars it bound them to,
     presses a button inside a both-way scroll instead.
   - Compose gives a node carrying two scroll modifiers the outer one's
     `ScrollBy`, so `SnglScroll` puts the vertical one outside: scrolling
     either way, the end it reports is the bottom. That end is the spec's
     (`ui.scroll`'s doc: the bottom, or the right edge of a horizontal
     scroll) and html's; what Compose limits is a test's scroll, which moves
     only the vertical one.
   - A vertical scroll is as wide as its content's minimum on fyne and gtk4,
     where html and android cut the content off at the scroll's edge. Fixed
     (Jonathan's call: clip): gtk4's horizontal policy is
     `GTK_POLICY_EXTERNAL`, its natural width the content's, and fynelayout's
     `ReachScroll` reports Fyne's least scroll width, which has no natural
     size to keep. Pinned by `TestAVerticalScrollIsNotAsWideAsItsContent` in
     gtk4rt and fynelayout.
   - html writes `class=` twice on some image frames, and `.sngl-image`'s
     clip is a rule the page writes only beside a source, so a frame
     showing only its placeholder is not clipped. Fixed: a test build wrote
     every literal class again with the test's own attributes, and the rules
     are the page's wherever a frame's class names one (`namesImageFrame`).
     Pinned by `TestATestBuildWritesAFramesClassOnce` and
     `TestAFrameWithNoSourceIsClipped`.
   - `iter[int]` has no `map`.
2. **Settings.** Done. `sngl:settings` has `kept(name=, :value=)`, `load`,
   `save` and a `store` context (`scope`, and a `Place`: `platform` or
   `memory`); `design/stdlib.md`'s *Settings* says how each target keeps a
   value. Only the program's own entry point opens the platform's place, so
   every test, on every target and in the interpreter, keeps values in memory
   and starts with none. The Pokédex keeps `language` and `dexName`. Pinned by
   `testdata/settings_kept.{sngl,txtar}`,
   `cmd/sngl/testdata/settings_kept_runs.txt` (gtk4 and fyne run twice),
   `TestSettings_AKeptValueOutlivesAReload` (Chromium) and
   `pkg/go/snglsettings`' tests. It needed a generic body to encode its own
   T (`testdata/json_generic.*`, `error_json.sngl`). Found:
   - `key=` is a node's identity in a loop, so the prop is `name`, which is
     what the design above now says.
   - A generic component's two-way prop, bound where the component is built
     at run time, wrote back through a `func(any)` on fyne and gtk4, which
     did not build. Fixed here (`generic_component_runtime_instance.txtar`).
   - A package var written from outside a component built at run time marks
     that component's effect by a bare name the writer does not have, so Go
     does not build. Neither a mount in the Model nor a test reaches the
     instance. The Pokédex binds `dexName` in `homePage`, beside the query
     keyed on it, for this reason; `t.setContext(settings.store, …)` in a test
     gets there too, by making the component reading the context an instance,
     and `settings_kept.txtar` leaves that test out. Fixed before step 4 (see
     there).
   - An effect in a component copied per row of a const loop cannot read a
     prop bound to the loop variable ("an effect's @mount reads the loop
     variable"), so `settings.kept(name=row.id, …)` under a `for` is
     refused. Fixed on bubbletea, the one target refusing it: the splice
     substitutes the prop as the loop variable, and the effect is keyed by
     the copy now, its handlers reading the loop variables at the copy's
     index and an `@unmount` reading what a recorder kept for a copy the list
     no longer holds (`cmd/sngl/testdata/component_effect_reads_loop_prop_runs.txt`).
     `kept` itself then failed everywhere else, built at run time with its T
     unbound ("PlanCodec reached T"): no copy per instantiation was made while
     its family parameter F was unbound, which a call site with no children
     always leaves it. Fixed, so `kept` goes under a `for` on all six
     (`cmd/sngl/testdata/settings_kept_per_row_runs.txt`).
     Found while pinning it:
     - On android a test's `@unmount` wrote into the next test's state. Fixed
       (`cmd/sngl/testdata/test_unmount_stays_in_its_test_runs.txt`).
     - On html a component built at run time with a prop JavaScript reserves
       -- `kept`'s `default` -- was bound as `let default = …`, which esbuild
       refused. Fixed
       (`cmd/sngl/testdata/runtime_instance_reserved_prop_runs.txt`).
     - A write reaching an `@update` through a two-way prop is reported only
       when it changes the value on the interpreter, html and fyne; bubbletea,
       which splices the component, reports every write as the effect does
       outside one (#302).
     - android's `@unmount` reads the prop its copy mounted with, not the
       one it holds (#301).
     - A copy that leaves and one that rekeys in one settle run in different
       orders on html and fyne (#303), and `key=` on a node in a loop is
       honoured only by the interpreter (#304).
   - The interpreter read another package's var from its initializer on
     every access, so a library's state never changed under `sngl test`.
     Fixed here (`importedVarOwner`).
   - A Go test cannot compare two lists with `==`: `[]int` is not
     comparable. Fixed on Go and html by goEqualer and `jsEqual`
     (`struct_list_equality_runs.txt`). Kotlin was wrong too: a list var is
     a `SnapshotStateList`, whose `equals` is its identity, so `xs == [1]`
     was false; a list operand is read through `.toList()` now, pinned by
     the same script's `AListVarComparesItsElements`.
   - On bubbletea, `languagePick.change(…)` in a test moved `language` but
     never ran the `@update` keyed on it, while a handler's write (`dexPick`)
     and a test's direct write did. Fixed: bubbletea wrote a bound widget's
     value back with the assignment alone, dropping the marks and settle the
     effect lowering put in the bind's handler, so a key in Update ran no
     `@update` either; the write-back is that handler now
     (`cmd/sngl/testdata/bound_widget_effect_runs.txt`,
     `TestTypingIntoABoundInputRunsItsEffects`). On html, a test writing `c.app.language` through a
     nested instance needs an `UpdateComponent` intrinsic JavaScript lacks.
     So the Pokédex's tests check the kept dex, not the kept language. Fixed
     by main's writer for a var (`__wrote_<v>`), merged in 2e2797843, which a
     bisect over the Pokédex finds; the Pokédex's
     `testWritingTheLanguageTranslatesThePages` pins it
     (`cmd/sngl/testdata/examples_runs.txt`).
   - The per-platform fixture harnesses compile every component as an entry
     with nothing instantiating it, so a fixture whose components bind a
     two-way prop needs `SKIP(codegen)`.
   - The value loads in a mount effect, so a kept setting shows its
     initializer for the first frame on every target. Fixed (Jonathan's
     call): `kept` takes a `default`, and its value is an `option[T]`, null
     until the store answers, so a program shows what it chooses meanwhile
     and a store answering late can keep it null; loading writes nothing
     back. Pinned by `testdata/settings_kept.sngl`'s `NullUntilItMounts` and
     `StartsAtTheDefaultAndWritesNothing`, and `settings_kept.txtar`.
   - Nothing relaunches an android activity, so SharedPreferences is pinned
     by the golden's code, not by a run.
   - html on `--lang go` keeps settings in localStorage too; a cookie, so
     the server renders the chosen language on the first paint, is left for
     later.
3. **Tabs.** Done. Every panel of a `ui.tabs` is mounted with the tabs and
   stays mounted; the doc allows a platform that cannot keep one loaded to
   mount it on selection, and none needs to. android pages through them in
   `SnglPager`, a HorizontalPager under the TabRow keeping every page
   composed; html keeps every tabpanel with all but one `hidden` and swipes
   through `sngl-swipe`; fyne, gtk4 and bubbletea hide the box sngl:ui's body
   puts each panel in. A test swipes with `sngl:test/mobile`'s `swipe`, on
   android and html. design/nodes.md's *Every tab's panel is mounted* says
   how. Pinned by `testdata/tabs_panels_stay_loaded.{sngl,txtar}` and
   `cmd/sngl/testdata/tabs_panels_stay_loaded_runs.txt` (all six; android
   and html swipe). Found:
   - No desktop target had a notebook: fyne, gtk4 and bubbletea rendered
     sngl:ui's own body (fyne's AppTabs takes typed items no `Arg` builds),
     so the body is what changed for them.
   - A null test asks whether a node is mounted, so a node of a panel not
     shown is present. Nine tests said otherwise: `tabs_runs`,
     `tabs_panel_presence_runs`, `table_rows_runs`,
     `slot_nested_instance_runs` (which now drops a tab, since a switch drops
     no panel) and four of the Pokédex's.
   - fyne and gtk4 lift each panel's box into an instance, and two gaps came
     with it, both fixed: each instance's render cleared the Model's presence
     field of an instance only another builds, and a widget a record's slot
     builds -- a button written straight in a panel -- had no test invoker.
   - An emit under a context provider in an override's body was never
     answered: android's pager handler sits under `flexShare`, and its
     `change(k)` came out as an `onChange` nothing declared. Fixed in
     `substituteEvents`.
   - android's intrinsics took no slot with parameters; `SnglPager`'s
     `page component(index int)` is the first.
   - bubbletea answered no `display`; a Layout's `hidden` is `none` now. Its
     Tab still stopped on a hidden panel's controls, drawing nothing. Fixed
     by main's 44f04701e, which passes over a stop a box displayed none
     holds (`__focusCounted`, `__focusShown`), merged here in 2e2797843;
     `TestTabPassesOverAHiddenTabPanel` pins it for the tabs, and fails on
     the branch before that merge.
   - On bubbletea, a `ui.tab` under an `if` beside a panel with a focus stop
     does not build: the focus functions read the list of tabs the view
     hoisted into a local of its own (`undefined: __lt11`), with the old body
     too. Fixed in step 5 (`loopTemps`); `slot_nested_instance_runs.txt` runs
     bubbletea again.
   - A test reading a prop whose value reads a `var` a panel's block declares
     (`c.tap.text` over `"{taps}"`) emitted the var bare on the Go targets,
     `undefined: taps`, and html threw on it. Fixed: passHoistState places
     the read on the instance holding the var
     (`test_reach_block_state_runs.txt`).
   - A swipe on android turns one page, so one settling on a disabled tab
     goes on to the next that is not, which is what html's chooses.
   - Opening Bulbasaur's page against the recorded answers asked 11 requests
     on html and asked 45 once every panel loaded: 15 moves and 3 of their
     types, 6 stats, 8 sprite pictures. On the interpreter it settled in 2.4s
     where About alone took 0.1s. The recorded answers are trimmed, 3.8 KB a
     move, where PokéAPI's carry every flavor text and `learned_by_pokemon`,
     and a Pokémon's machine moves run to dozens. Loading a row's facts as it
     comes into view needs a visibility event no target has -- the nearest is
     a scroll's `@reachEnd`, and the table scrolls sideways -- so Moves asks
     the first time its tab is shown and keeps what it got (`movesShown`):
     the page opens without those 18 requests, and settles in 0.8s on the
     interpreter. A row-visibility event is the follow-up.
4. **The app bar.** Before it, done: a write from outside a component built
   at run time reaches the effects keyed on it, the bug step 2 found, since
   the menu moves who writes `language` and `dexName`. Each package var an
   effect's key reads becomes a prop of the instance (`passEffectOutsideKeys`),
   so the site's setter reaches every live copy; an instance in a test build
   rooted at a component reaches that component's flags through the Model.
   design/instances.md says how. Pinned by
   `testdata/effect_keyed_on_package_var_runtime_instance.txtar`,
   `cmd/sngl/testdata/effect_keyed_on_package_var_runs.txt` (all six, a query
   included) and `settings_kept.txtar`, which has its left-out test back. It
   was wider than step 2 saw: html built and silently ran no `@update`,
   remount or query, and android did not build either. A key that reads the
   var through a call (`effect(on=current())`, or through a func calling it)
   is fixed too: it calls a copy of each func that takes the var as a
   parameter, handed the prop, and the interpreter, which ran no such
   `@update` even outside an instance, follows the calls. Pinned by
   `cmd/sngl/testdata/effect_keyed_through_call_runs.txt` (all six) and
   `testdata/effect_update_through_call.sngl`. The golden harness's fyne test file has no `t.wait` (it lacks
   the post queue `sngl test` declares, `undefined: __snglRunPosts`), so the
   query case is a script.

   Done. `ui.window` has a `menu` slot of `ui.menuItem`s, which gained
   `checked`, and `canGoBack` and `@back`; `sngl:ui/nav`'s stack has
   `canGoBack()` and a page `titleOf`, a function of its params that
   `pages.current.title` returns while it is current. A window draws a bar
   only where its call site binds `canGoBack` or populates the menu, so no
   program written before moved. android draws a Material3 Scaffold and
   TopAppBar (appbar.kt: the favicon or the launcher's icon, the title, an
   up arrow, an action per top-level entry opening a DropdownMenu), and the
   system back runs `@back` while `canGoBack` holds, ahead of a NavHost's own
   pop; gtk4 a GMenu behind a header bar's menu button, with a back button;
   fyne its main menu, with a back button over the content; html a `<header>`
   over a `ui.menubar` of `<details>`, the browser's back popping an entry
   `sngl-back` keeps; bubbletea a title line, esc its back, and no menu.
   `ui.menu` and `ui.menubar` are real on android and fyne. `sngl:test/mobile`
   has `back()`, on android and html. The Pokédex's `Pokedex` is its window:
   the menu chooses the language and the dex, detail's title is the
   species' name from its params, and `#backButton` is gone. design/
   windows.md's *A window draws a bar*, navigation.md's *`canGoBack` and
   `titleOf`* and nodes.md's *A menu entry is picked* say how. Pinned by
   `testdata/window_menu.{sngl,txtar}`, `nav_can_go_back.{sngl,txtar}`,
   `nav_page_title_from_params.{sngl,txtar}` and its `_html` route golden,
   `cmd/sngl/testdata/window_app_bar_runs.txt` (android opens a submenu,
   chooses a checked entry and presses back, over a NavHost too; html the
   same) and fynelayout's `TestAWindowsMenuIsItsEntries`. Found:
   - A prop holding a record whose handler writes what the prop reads loops
     the lowering: passReactivity puts the prop's updater inside the
     handler, and the updater holds the expression the handler is in
     (`MatchLate` overflowed the stack). Fixed: the update of a prop whose
     expression holds a handler writing state is a function of the prop's
     owner (`propSetter`), which the handler calls, so the update no longer
     holds the handler holding it. Pinned by
     `testdata/record_handler_prop.txtar` and
     `cmd/sngl/testdata/record_handler_prop_runs.txt`. The window's menu is a
     slot population by design, not because of this.
   - An `if` whose condition reads a data slot's records, `menu.length() > 0`,
     inlines the records, handlers and all, into the condition, which fyne
     then translated outside the slot's scope (`shown.Text`, `m.main`).
     `dropEmptyLoops` now reads a list literal's length as its count and
     settles an `if` its literals decide, so the window's bar is decided
     where it is written.
   - A test picking a record through a node's data slot ran, on a compiled
     target, the record's handler off the call site's list re-evaluated in
     the test: its write landed and none of the window's updates ran, and
     Go spelled the write exported (`c.Size`, fixed in `MutTargetField`).
     It now presses what the window drew (`lower.NameRecordPresses`). A test
     reading a record's `checked` re-evaluated the whole list, every handler
     with it, and a handler writing state an instance built at run time reads
     dragged `UpdateComponent` into the test, which no test emitter had: html
     and fyne did not build at the step's commit. Merging main stopped that
     failing (the handler's write calls the var's writer, `__wrote_size`, now),
     but the test still built the menu to read one field. Fixed: the read is the one entry
     as its call site wrote it, its loop's variable bound to the list at the
     index (`lower.answerTestRecordRead`), so no handler reaches the test, and
     the Pokédex's menu tests read `checked` again. Pinned by `deny` lines in
     `testdata/window_menu.txtar` and
     `cmd/sngl/testdata/window_menu_checked_runs.txt` (all six). Found:
     - On html, a window whose menu loops over a package const beside a
       `nav.stack` raised `sizes is not defined` as the page loaded. Fixed:
       the const is an asset a page loads where its script names it, and the
       spread `[...sizes.map(…)]` read as a member access. Under it, a pick
       on the home page wrote the about page's text from the menu's render
       func, which every document shares; each document's copy leaves those
       writes out now (design/navigation.md). Pinned by
       `testdata/nav_stack_html_menu_const.txtar` and
       `cmd/sngl/testdata/nav_stack_html_menu_runs.txt`.
   - A data slot handed on in a platform override's body,
     `ui.menubar { menu }`, reached nothing: DataFamilies reads the program's
     populations, not a library body's, so the menubar iterated `[]`. The
     override writes `ui.menubar(items=menu)`.
   - The fyne host's constructor table took only each override's first
     primitive; it takes every one now (`WidgetSpecsFrom`).
   - A document's `<title>` on html was written only where the window's
     title folds to a literal; one that reads anything else is now set as
     the script starts, once, which covers a title from a route's params.
     A title reading state did not follow the state: the page dropped every
     write to the document's props, the title's updater with them; it is
     `document.title` now (`TestWindow_ATitleReadingStateFollowsIt`). A
     route-mode server rendering no `<title>` for one is #300.
   - On html a test page for a component that is not a window cannot read
     `pages.current` (`current` is undefined), with or without `titleOf`; a
     window's documents answer it. Fixed: a document published the page's
     record only where its own script read `current`, and a test's script is
     its own, so where the fold had settled every read the document makes --
     no `nav.link` -- the test read `undefined`. A test build's documents
     always publish it (`cmd/sngl/testdata/nav_current_test_page_runs.txt`).
   - `navigation.canGoBack` on html is the browser's, so a test page, which
     is never at the start of its history, can go back: the Pokédex's tests
     assert the window's title, not its `canGoBack`.
   - A press of a copy drawn under a reactive `if` on html (the header,
     before its condition was settled) ran nothing, with no error. Fixed: a
     menu whose entries an `if` adds still leaves the bar's condition open,
     and lifting the bar into an instance made each value its records read a
     prop, so Save's handler wrote the instance's copy of the var; a literal
     writing what the site binds now travels whole (`writesFree`). Under it,
     the walk translating html's intrinsics never entered a condition, where
     the same records' `UpdateComponent` reached JavaScript raw, and
     NoTernary hoisted nothing out of a callee, which bubbletea's pick reads
     the handler off. Pinned by
     `cmd/sngl/testdata/window_menu_under_if_runs.txt` (all six).
   - The interpreter mounted a window's props before its body, so one
     reading a handle the body declares (`title=pages.current.title`) was
     missing from the mounted node; it is evaluated again after the body.
   - android's showcase test lowered without DataFamilies, so a radio's
     loop walked the empty default; it runs it now, as a build does.
   - gtk4's recorded Go builds (`TestABlockingCallCompilesIntoAPostedUpdate`
     and two more), gtk4rt's `test_run` and every fyne and gtk4 run digest
     moved with the runtimes, and were recorded again.
   - The detail page's title is the species' name as the slug spells it
     (`display(p.name)`), in every language: what the page fetches comes
     after the title is asked for.

5. **Tabs that look like tabs.** Done. gtk4 draws a `Tabs`, gtk4rt's
   GtkStackSwitcher over a GtkStack, with a `TabPage` per tab the stack
   holds; fyne fynelayout's `Tabs`, a widget drawing an AppTabs with a
   `TabPage` per item; bubbletea a bar of `tui.TabLook` boxes, the selected
   one open onto the panels' box (`pills` reverses the selected label
   instead). Every panel still mounts with the tabs, a disabled tab cannot be
   chosen, and a test presses a tab as before. `variant` is GTK's and Fyne's
   own look, as it is Material's on android. design/nodes.md's *Every tab's
   panel is mounted* says how. Pinned by `testdata/tabs_look.txtar`,
   `tabs_look_cgo.txtar`, `cmd/sngl/testdata/tabs_look_runs.txt` (gtk4's and
   fyne's snapshots, bubbletea's bar as text) and gtk4rt's, fynelayout's and
   tui's tests; `tabs_runs.txt` and `tabs_panels_stay_loaded_runs.txt` pass
   unchanged. Found:
   - The pages reach the tabs through the render slot of the strip's loop
     even when every tab is written out, so neither widget can take them
     through its own add: gtk4rt's `BoxAppend`, `InsertBefore` and
     `BoxRemove` route a child of a `Tabs` to its stack, and fynelayout's
     `Tabs` keeps the pages in a box drawn nowhere and makes the AppTabs'
     items theirs as the box is laid out.
   - A fynelayout `Tabs` that embedded `*fyne.Container` drew nothing: Fyne
     walks into a container and a widget and nothing else. And a Spec cannot
     name the box a render slot renders into, so fyne's `Tabs` and `TabPage`
     are spec'd in codegen as `MenuEntry` is, not through the Spec
     vocabulary the plan named.
   - GTK and Fyne report the program's own change of the page shown as the
     user's, so each runtime fires a page's `@change` only for a page the
     program did not select.
   - The page's event has to be `change`, the tabs' event it raises: gtk4
     and fyne name an invoker by the event the handler raises
     (`TriggerEventName`), NamePresses by the node's own, and a button agreed
     only because its override raises `click` from `@click`. A page with
     `@shown` was pressed as `shown` and invoked as `change`.
   - bubbletea's Layout has no cross-axis stretch, so the bar and the
     panels' box each grow across a row of their own to share a width. A
     focused tab carries `> `, as a focused button does.
   - Step 3's bubbletea finding is fixed: a focusable loop over tabs an `if`
     extends read the list View hoists into a local (`undefined: __lt11`).
     Every walk that starts at a loop now makes the temps the loop reads
     (`loopTemps`), and `slot_nested_instance_runs.txt` runs bubbletea again.
   - A bubbletea presence walk declared a loop key it never read once a
     widget's walks had marked the loop `KeyRead`; `tabs_runs.txt` stopped
     building. Its copy of a loop now drops the mark.
   - GtkStackSwitcher's buttons fill the bar's width, and the panels sit at
     their natural height: nothing expands the stack.
   - Merging main brought two fixtures this branch broke:
     `style_bubbletea_fit_runs.txt` set `overflow`, which step 1 removed,
     and `android_flex_in_written_column` exposed Kotlin writing an optional
     function type as `(T) -> R? = null`, now `((T) -> R)?`.
   - The merge's first rerecord was stopped for low memory;
     `SNGL_BUILD_SLOTS=2` ran it. Under that load, niced, the clean verify's
     `TestUnchangedCallIsNotEvaluatedAgain` failed once: the const
     evaluator's build was killed at `evalTimeout`'s 120s. The package passes
     alone in 32s.
6. **Re-record and the README.** Done. `go tool verify -update`, niced with
   `SNGL_BUILD_SLOTS=2`, changed nothing: step 5's rerecord already held
   every run of the Pokédex. `examples/pokedex/README.md` says what the app
   does now: the window's bar per target, its menu choosing the language and
   the dex, both kept, the page's title from its params, tabs drawn as each
   target draws them and swiping on android and html, every panel kept
   loaded with Moves asking the first time it is shown, and the sideways
   Evolution, Sprites and Moves. The language select, home's dex select, the
   page's "‹ Back" and the titles left in English are gone from it. Found:
   - The README had no screenshots, and nothing made any. Jonathan takes and
     adds them later.
   - A test cannot capture what the app draws (#299): with the app's window
     as a test's root, gtk4's header bar has no title, menu or back and the
     page is clipped to a strip about 40px high, and fyne's PNGs are the
     theme's background alone. Fixed: a test build's BuildUI shows its
     root's window as the application's root does -- gtk4rt's
     `ToplevelWindow`, which now forgets each harness window's bar when the
     window goes, and fyne's content box -- pinned by
     `cmd/sngl/testdata/test_root_window_snapshot_runs.txt` on all four
     targets.
   - On bubbletea, a test root whose body is `Pokedex #app` alone declares
     `Model.__snglTeardown` once per copy and does not build (#298), and so
     did the app without its tests. Fixed: a component that renders one with
     state is spliced as a stateful one is (`keepsState`), pinned by
     `cmd/sngl/testdata/bubbletea_stateless_wrapper_built_instance_runs.txt`.
   - Step 5 never regenerated `pkg/go/fynehost/registry_gen.go`, so the fyne
     host had no constructor for `Tabs` or `TabPage`. The clean verify's
     `go generate` wrote them, and they are committed.

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
