# What the target gaps left

What !275 (`target-gaps`) found and did not fix. It was worked on
`target-gaps-left` (!277), which started from !275's last commit; since !275
was not yet on `main`, the two are combined into !275: `target-gaps` is
fast-forwarded to `target-gaps-left`, !277 is closed pointing at !275, and its
description folds into !275's. Work from here goes on `target-gaps`, and
!275's description carries this plan's progress. Should `main` move, merge
`origin/main` into `target-gaps` (never rebase). Delete this file before !275
leaves draft. `ui.table`'s redesign (#290) and
a constant `$"..."` message (#292) are issues of their own and not here.

## Rules for every part

- A compiler or runtime fix gets its own neutral fixture, which fails with
  the fix taken out; confirm that by running the old behavior, out of tree
  where the `sngl` on PATH would resolve the current checkout. Nothing
  Pokémon outside `examples/pokedex`.
- Run the goal-1 grep before every push; its only hits are the two
  `design/` pointers (`import-schemes.md`, `nodes.md`):

  ```
  git grep -niE 'pok[eé](mon|api|dex)|base_experience|base_stat|front_default|\b(pikachu|bulbasaur|ivysaur|venusaur|charmander|charmeleon|squirtle|ditto|mew|missingno|pichu|raichu|eevee)\b' -- ':!examples/pokedex' ':!TODO.md'
  ```

- `go tool verify` passes at the end of each part; after any codegen or
  runtime change `ANDROID_HOME=$HOME/Android/Sdk go tool verify -update`
  rerecords every package's goldens and recorded runs. It can hit verify's
  60-minute limit when most records are stale; run it again and it carries on.
  A runtime change (`pkg/go/gtk4rt`, `pkg/go/fynelayout`, ...) makes gtk4's
  and fyne's records stale too. Then run a plain `go tool verify`.
- Read each golden diff for behavior, not only hashes: compare the unpaired
  `+`/`-` lines, since re-indentation hides moved code.
- Edit nothing -- source, `lib/`, `examples/` -- while a suite runs, and run
  nothing beside one: no second verify, no browser, nothing on port 47819.
  Check `ps` for a verify an earlier session left running first.
- gtk4 needs a fresh headless compositor
  (`XDG_RUNTIME_DIR=<short dir> WLR_BACKENDS=headless WLR_RENDERER=pixman cage -- sleep infinity`),
  with `XDG_RUNTIME_DIR` and `WAYLAND_DISPLAY=wayland-0` passed to the
  tests.
- An out-of-tree `sngl`: `go build -o <scratch>/sngl ./cmd/sngl`, then
  `SNGL_HOST_GO_MOD=<checkout>/go.mod <scratch>/sngl test ...` from a scratch
  directory. `go test ./cmd/sngl -run 'TestScript/...'` wants `-timeout 60m`
  for `examples_runs`.
- Commit and push as you go, and keep !275's description up to date with
  this plan's progress.

## Part 1: what the review did not reach

!275's review agent ran out of budget before these. Read each for
correctness, with its `design/` file, and fix what is wrong.

- [X] Part 1 of !275: the checker's `addNodeOverride` (a platform's override
  of its node for a language, `const component platform[static.language]`)
  and `sngl run --platform html --lang go` (Go's route `main()`, `listen`).
- [X] fyne: `fynetheme.Backdrop`, `fynelayout.New`/`NewAligned`/`Adopt`,
  `InsertBefore` laying out without refreshing, and `@reachEnd`'s probe and
  `fynelayout.Post`.
- [X] gtk4: `pkg/go/gtk4rt`'s scroll (the probe between viewport and
  content), image (a picture exactly its style's size, `setTexture` asking
  for its own source) and reach-end code.

  Found and fixed: fyne's spacer in a `Weighted` box, a margin-only child,
  a backdrop rounded with no radius; gtk4's cgo scroll set again on each
  render, a gone fetch's texture kept; reach-end firing on a shrink.

## Part 2: a raise in a render

html, fyne and gtk4 render inside the handler whose write asked for the
render, so a raise there is caught by the handler's catch block and reaches
the boundary's `@error`. The interpreter and bubbletea render after the
handler, and the boundary is never told; android composes later still, and a
raise while Compose composes reaches no catch block at all -- the test ends
there. `cmd/sngl/testdata/slot_render_raise_draws_again_runs.txt` asserts
only the redraw, and leaves android out, for these reasons.

- [X] The spec says a boundary catches what the expressions of its content
  raise (*Error boundaries*). Decided with the user: a render's raise reaches
  the boundary around what rendered, on every target, not the handler that
  asked for the render.
- [X] Make the interpreter and bubbletea agree with it, and android catch
  one at all. A script test on all six asserting where the raise went, which
  `slot_render_raise_draws_again_runs.txt` can grow into.

## Part 3: alignment on gtk4 and bubbletea

- [X] gtk4 centers a label's text in its box: it reads no `alignItems` or
  `textAlign`, where html starts a line at the left. A width is also only
  the least a label is (a size request), not its width.
- [X] bubbletea centers nothing (`alignItems`), so the example's evolution
  cards' names sit left.
- [X] Each with a golden and a recorded-run or snapshot fixture, as Part 2
  and 3 of !275 pinned gtk4's style and fyne's layout
  (`cmd/sngl/testdata/style_gtk4_rendered_runs.txt`,
  `layout_bubbletea_rendered_runs.txt`).

## Part 4: runtime leftovers

- [X] fyne: a reactive write to a constructor-only prop other than the
  grid's column count is still answered as the constructor's
  (`intrinsic_translator.go`'s `reflect.DeepEqual`): markup's `kind` and
  `spanStyle`. Give each a setter (a Spec `Setter` may name a function, as
  `fynelayout.SetGridColumns` does), or report the write as reaching nothing,
  as a prop with no setter is.
- [X] gtk4: `gtk4rt.SetStyle` keeps one CSS class per distinct style and never
  removes one, reloading the whole stylesheet each time, so a style computed
  from a value that keeps changing -- an animated width or color -- grows the
  provider by a rule per value. Drop a class no widget holds, or set the
  changing fields some other way. A `pkg/go/gtk4rt` test counting rules.
- [X] bubbletea: a test draws the Model's view only when it snapshots, so a
  picture (`tui.Picture`) starts fetching there and the first snapshot shows
  blank cells where the program, which draws every frame, shows the picture.
  !275's screenshots took a snapshot first to start the fetches. Draw the view
  as a waiting test settles (codegen/platform/bubbletea/post.go), and pin it
  with a script test snapshotting a remote picture after a wait.

## Part 5: the card's click, and the example again

- [X] html: a click on a button inside a clickable card also clicks the card,
  by the DOM's bubbling, as a mouse click does; Enter on the button does too.
  Ask the user whether a card's `@click` should hear a click its content
  handled, and what the other targets do, before changing anything.
- [ ] The example on every target again: screenshots of html, fyne, gtk4 and
  bubbletea in the MR description, the example README's "Where each target
  stands" brought up to date, and the user checks android on the device.
- [X] A review agent over the branch (`git diff origin/main...target-gaps-left`);
  fix what it finds, each with a fixture.
- [X] Combine !277 into !275, as above.
- [ ] Delete this file; take !275 out of draft -- ask the user first.
