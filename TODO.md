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

- `go tool verify` passes at the end of each part; recorded runs are
  `-update`d after any codegen change (goldens
  `go test . -run TestGolden -update`, scripts
  `ANDROID_HOME=$HOME/Android/Sdk go test ./cmd/sngl -run TestScript -update`,
  and the platform packages' own records: `./codegen/platform/gtk4`,
  `./codegen/platform/html`, `./internal/build`). Read each golden diff for
  behaviour, not only hashes.
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

## Part 1: `go tool verify` and `sngl run`

- [ ] `sngl run --platform html --lang go` serves the generated directory --
  `server.go` as a file listing -- rather than running the Go server the
  generated `Handler()` is for. Reportedly `--lang none` html is broken the
  same way. Run each html mode by hand and fix both; a script test runs each
  and fetches a page.
- [ ] `go tool verify` counts the ignored `tmp/` directories' Go packages
  (`tmp/serve`, `tmp/srv`) in its coverage, and builds them. It should walk
  what git tracks.
- [ ] A recorded run outside `TestGolden` and `TestScript` --
  `internal/build`'s `TestTestReachLibrarySitesRun`, gtk4's `go_runs` -- went
  stale after a codegen change, and only a full `verify` said so. Make the
  `-update` path that rerecords everything one command, or have the
  goldens' and scripts' `-update` say which other packages hold records.
- [ ] An edit during `verify` flakes the suites that rebuild from the
  checkout (`internal/optimize`'s evalstore, `cmd/sngl`'s trust_eval,
  `sngl` proxying to `./cmd/sngl`). Either build those once from a snapshot
  of the tree, or say on failure that the tree changed under the run.

## Part 2: gtk4 reads a node's style

gtk4 reads no field of `style` but `flex`: sizes, gap, padding, margin, font
size and weight, colours and border radius are GTK's own. The Pokédex's
artwork is 90 px where it asks for 192, its cards show no artwork, and its
columns and badges are unstyled.

- [ ] Width and height as a size request; gap as a box's spacing; padding
  and margin.
- [ ] Font size, weight and style; foreground and background colours and
  border radius, through a CSS provider per widget or per class.
- [ ] A card's artwork on home: find why a picture in a clickable card shows
  nothing.
- [ ] Each with a golden, and the screenshots taken again.

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
