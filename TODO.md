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
  package's goldens and recorded runs. Read each golden diff for behaviour,
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
- [X] Font size, weight and style; foreground and background colours and
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
  colours, bold headings, columns at their widths. Left: a label's text is
  centred in its box (gtk4 reads no `alignItems` or `textAlign`), and a
  width is only the least a label is.

## Part 3: fyne's and bubbletea's layout gaps

- [ ] fyne paints no label's background, so a `ui.badge` is plain text: a
  label with a background needs a rectangle behind it, kept in step when the
  colour is a run-time value.
- [ ] fyne's box ignores `alignItems`, so a card's lines are not centred.
- [ ] An adopted fyne box (`fynelayout.Adopt`) takes Fyne's gap where a box
  codegen builds takes the style's.
- [ ] fyne's `@reachEnd` fires from the renderer synchronously and looks
  again only 100 ms after firing, so a short first page fetched slower than
  that never loads the next; gtk4 posts to idle. Make them one behaviour, with
  a runtime test of each.
- [ ] bubbletea: a row's columns and badges are written with no space between
  them (`moveLine`'s `flex=1` and widths), blank lines stand where requests
  are, and the artwork leaves blank rows under it.
- [ ] fyne lays a whole table out again for each answer that lands in it:
  the detail page's move table takes 2-5 s for its fifteen answers, text
  shaping first in the profile, and `pokedex_test.sngl` waits 20 s for it.
  Lay out what changed, and bring the wait back down.
- [ ] Runtime tests for `fynelayout.ShareSized`/`Weighted`'s size math and
  `Nothing`, which a golden alone does not hold to.

## Part 4: what the review found and left

- [ ] The interpreter's tab press (`View.Choosing`, `internal/interp/view.go`)
  does not follow `lower.NamePresses`: it presses where the body's first node
  handles the event, accepts any loop variable compared by value, and reads
  no callback prop. One rule, both sides, a script test on all six.
- [ ] Kotlin's state reads are hidden by a same-named local anywhere in the
  body (`codegen.BodyLocals` binds every local up front; writes check by
  symbol, reads by name). A script test with a page var and a nested local
  of its name.
- [ ] A render that raises leaves `__renderingN` set, so the slot never draws
  again: the re-entry guard wants a `finally`, which the IR does not have.
- [ ] A timer in a tab's loop never ticks on bubbletea. `collectLoopTimers`
  (`codegen/timers.go`), `widgetLoops` (`bubbletea/loopwidget.go`) and
  `callSiteWraps` (`internal/lower/test_reach_flatten.go`) count a
  container's loop over its records, which `lower.BlockLoops` drops; doing
  the same there left the record branch's condition reading the loop's
  variables. Reproducer: a `time.timer` in a `for` in a `ui.tab`'s panel.
- [ ] html's clickable card takes no Enter or Space: an element's handlers
  read no key.
- [ ] gtk4's drop-down C helpers are written twice, in `pkg/go/gtk4rt` and
  `codegen/platform/gtk4/scaffold_files.go`.
- [ ] Lower confidence: a reactive write to a fyne constructor-only prop
  (`intrinsic_translator.go`'s `reflect.DeepEqual`) may go silently stale;
  a package const named like an import (`theme`, `layout`, `time`) collides
  on fyne and gtk4 (`golang.PackageConstDecls`).

## Part 5: the example on every target again

- [ ] Screenshots of html, fyne, gtk4 and bubbletea after parts 2-3, in the
  MR description, and the README's "Where each target stands" brought up to
  date.
- [ ] The user checks android on the device.
- [ ] A review agent over the branch; fix what it finds.
- [ ] Delete this file; take the MR out of draft -- ask the user first.

Not here: `ui.table`'s redesign is #290, and a constant `$"..."` message
(android's page titles) is #292.
