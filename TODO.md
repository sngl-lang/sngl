# Target gaps

One MR, `target-gaps`, for the gaps the Pokédex redesign (!270) found and
left. The branch starts from !270's last commit; once !270 is on `main`,
merge `origin/main` (never rebase) so the MR targets `main` with nothing
under it. Delete this file before the MR leaves draft.

## Rules for every part

- A compiler or runtime fix gets its own neutral fixture, which fails with
  the fix taken out. Nothing Pokémon outside `examples/pokedex`.
- Run the goal-1 grep before every push; its only hits are the two
  `design/` pointers (`import-schemes.md`, `nodes.md`):

  ```
  git grep -niE 'pok[eé](mon|api|dex)|base_experience|base_stat|front_default|\b(pikachu|bulbasaur|ivysaur|venusaur|charmander|charmeleon|squirtle|ditto|mew|missingno|pichu|raichu|eevee)\b' -- ':!examples/pokedex' ':!TODO.md'
  ```

- `go tool verify` passes at the end of each part; after any codegen change
  `ANDROID_HOME=$HOME/Android/Sdk go tool verify -update` rerecords every
  package's goldens and recorded runs. Read each golden diff for behavior,
  not only hashes.
- Edit nothing -- source, `lib/`, `examples/` -- while a suite runs, and stop
  any browser, server or harness of your own first: port 47819 must be free,
  and a leftover headless Chromium failed html scripts.
- gtk4 needs a fresh headless compositor
  (`XDG_RUNTIME_DIR=<short dir> WLR_BACKENDS=headless WLR_RENDERER=pixman cage -- sleep infinity`),
  with `XDG_RUNTIME_DIR` and `WAYLAND_DISPLAY=wayland-0` passed to the
  tests.
- `t.snapshot` with `SNGL_UPDATE_SNAPSHOTS=1` captures fyne, gtk4 and
  bubbletea; a test package under `tmp/` (ignored) with real network makes
  screenshots of the example.
- Commit and push as you go.

## Part 1: `go tool verify` and `sngl run` (done)

- [X] `sngl run --platform html --lang go` ran html's static server over
  `server.go`: html's node held its `gen.run`, which wins over the language's.
  It is now html's override of its node for `none`
  (`const component platform[static.language]`), which the checker dropped
  silently before (`addNodeOverride`); Go's route `main()` listens at `listen`
  and prints its URL. `--lang none` worked for a static program by hand -- the
  Pokédex cannot build static, its pattern pages need a server.
  `run_html_go_serves.txt` and `run_html_static_serves.txt` start each with
  the new `serve` script command and `fetch` a page.
- [X] verify checks `./...` less what git ignores (`goPackages`).
- [X] `go tool verify -update` rerecords every package whose tests declare an
  `update` flag, and a stale record's failure names it.
- [X] A failing verify names the files that changed under the run
  (`reportTreeChange`); a snapshot would cost a cold build cache.

## Part 2: gtk4 reads a node's style (done)

- [X] Width and height as a size request; gap as a box's spacing; padding
  and margin.
- [X] Font size, weight and style; foreground and background colors and
  border radius, through CSS: `gtk4rt.SetStyle` takes a `gtk4rt.Style` of
  the fields gtk4 draws, in pixels, and keeps one class per distinct style on
  one provider for the display. A literal is read field by field, so a field
  reading state is written again where the state is; a Style held as a value
  goes through `snglStyle`, generated from the declaration. An image is
  exactly its style's size (a custom layout on its frame), since a size
  request is only a minimum and a picture's natural size is its file's.
- [X] A card's artwork on home: not the card. A GtkScrolledWindow lays its
  content out at its minimum height, a picture's minimum is nothing, and once
  a scroll's text alone was taller than it every picture was drawn at no
  height; `ScrollNew` sets the viewport's `vscroll-policy` to natural.
- [X] Found on the way: the Sprites tab's pictures landed under one another's
  labels, differently each run. A fetch arriving after a re-render painted
  the new picture GTK made at the freed one's address; `setTexture` asks for
  its own source.
- [X] `testdata/gtk4_style.txtar`; `cmd/sngl/testdata/style_gtk4_rendered_runs.txt`
  (`t.snapshot`s); `pkg/go/gtk4rt` `style_test.go`, `scroll_test.go` and
  `image_test.go`, each failing with its fix taken out. The example's gtk4
  screenshots were taken again: artwork at 96 and 192, badges in their
  colors, bold headings, columns at their widths. Left: a label's text is
  centered in its box (gtk4 reads no `alignItems` or `textAlign`), and a
  width is only the least a label is.

## Part 3: fyne's and bubbletea's layout gaps (done)

- [X] A fyne label with a background is drawn on a `fynetheme.Backdrop`,
  painting the color its theme override answers, so a badge is no longer
  plain text and a color read from state repaints with its override.
- [X] A fyne box whose style names a gap, padding or alignment is
  `fynelayout.New`'s from the start, so `Adopt` only ever adopts a box whose
  style said nothing; `alignItems` places each child at its own minimum
  (`NewAligned`).
- [X] `@reachEnd` is one rule on fyne and gtk4: measure on a scroll, a
  resize and the content changing size, through a probe box between the
  viewport and the content, and post one report. gtk4 had a gap too: its
  adjustment's upper never drops below the page, so content shorter than the
  viewport grew unheard. A fyne test build queues `fynelayout.Post`.
- [X] bubbletea joins a box's parts with `tui.Row`/`tui.Column` (the gap
  between them, nothing for what draws nothing -- the blank lines were the
  tab panels not shown), a flexing row is `tui.FlexRow` over `m.avail`, a
  height is in rows and a picture fits its image's box.
- [X] fyne's slow move table was `InsertBefore` refreshing its whole box:
  a refreshed theme override is a new scope for Fyne's text cache, so every
  themed label was shaped again per row. That test went from 3-5 s to 0.06 s
  and its wait is 5 s.
- [X] Runtime tests for `ShareSized`/`Weighted`'s size math, alignment and
  `Nothing`. Found on the way: fyne's caption-less `ui.divider` was a blank
  label; it is a separator.
- [X] `testdata/fyne_box_align_backdrop.txtar`,
  `cmd/sngl/testdata/style_fyne_rendered_runs.txt`,
  `cmd/sngl/testdata/layout_bubbletea_rendered_runs.txt`, and runtime tests in
  `pkg/go/fynetheme`, `pkg/go/fynelayout`, `pkg/go/gtk4rt` and `pkg/go/tui`,
  each failing with its fix taken out. The example's fyne and bubbletea
  screenshots were taken again. Left: bubbletea centers nothing
  (`alignItems`), so the evolution cards' names sit left.

## Part 4: what the review found and left

- [X] The interpreter's tab press (`View.Choosing`) did not follow
  `lower.NamePresses`. Both ask `ir.PressedNode` now, which presses by the
  index of a two-variable loop only. No program can tell the rules apart --
  only a library body is pressed, and `ui.tabs` met both -- so
  `internal/interp`'s `TestChoosingPressesByTheRuleTheTargetsDo` pins the
  view; `tabs_runs.txt` still runs on all six.
- [X] Kotlin's state reads asked the name, writes the symbol; both ask the
  symbol. Found on the way: html's page handler and bubbletea's key handlers
  bound no body locals, so the nested local's own read was the page var;
  `irwalk.EvalBlock` binds a block's local from its declaration on.
  `cmd/sngl/testdata/state_read_before_nested_local_runs.txt`, all six.
- [X] A render that raises left `__renderingN` set, so the slot never drew
  again. `ir.If.Finally` is a `finally` (Go's `defer`, `try`/`finally`
  elsewhere), and renderOnce clears the guard in one;
  `cmd/sngl/testdata/slot_render_raise_draws_again_runs.txt` fails on fyne and
  gtk4 without it. Left: on android a raise while Compose composes reaches no
  catch block, so the test ends there; and the interpreter and bubbletea
  render after the handler, so a render's raise never reaches the boundary's
  `@error` that html, fyne and gtk4 report it to.
- [X] A timer in a tab's loop never ticked on bubbletea -- under a test.
  The emitted program ticks, in a tab's loop or out; bubbletea's test build
  ran no command the Model returned, so no timer ticked under any test. A
  waiting test settles the Model each turn. Found on the way: android lowered
  no `t.wait`, and hoisted every timer to the top of the screen, dropping one
  under a loop and putting an instance's in the screen; each is rendered where
  it was written. `cmd/sngl/testdata/timer_tab_loop_wait_runs.txt`, all six.
  `collectLoopTimers`, `widgetLoops` and `callSiteWraps` count the record's
  loop consistently with each other, which is all bubbletea's schedules need.
- [X] html's clickable card took no Enter or Space. `html.element` declares
  `@keydown(e KeyEvent)` (`key`, `preventDefault`), and the card clicks on
  either; `codegen/platform/html`'s `TestACardTakesEnterAndSpaceAsAClick`
  presses them in Chromium.
- [X] gtk4's drop-down C helpers were written twice. A cgo program calls
  `gtk4rt`'s `DropDownLabel`, `DropDownOffer` and `DropDownSelect` through
  `gtk4rt.Handle(unsafe.Pointer(…))`, as it does its style and images, and the
  scaffold's copy is gone; `testdata/select_choices.txtar` denies it.
- [X] Lower confidence, confirmed or not before fixing: a reactive write to
  a fyne constructor-only prop did go silently stale -- a table's column
  count, `container.NewGridWithColumns`' argument, so a column added fell to
  the next row. A Spec `Setter` may name a function (`fn`), and the grid's is
  `fynelayout.SetGridColumns` (`testdata/fyne_table_columns_reactive.txtar`,
  `pkg/go/fynelayout`'s `TestSetGridColumnsLaysOutThatManyToARow`). Left: any
  other constructor-only prop still answers a reactive write as its
  constructor's; markup's `kind` and `spanStyle` are the ones. A package const
  named like an import does not collide: `passReservedNames` renames one
  meeting fyne's or Go's imports (`theme__r0`, `layout__r0`, `time__r0`), and
  gtk4 imports neither `theme` nor `layout`.

## Part 5: the example on every target again

- [ ] Screenshots of html, fyne, gtk4 and bubbletea after parts 2-3, in the
  MR description, and the README's "Where each target stands" brought up to
  date.
- [ ] The user checks android on the device.
- [ ] A review agent over the branch; fix what it finds.
- [ ] Delete this file; take the MR out of draft -- ask the user first.

Not here: `ui.table`'s redesign is #290, and a constant `$"..."` message
(android's page titles) is #292.
