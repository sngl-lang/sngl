# Pokédex redesign

One MR, `pokedex-7-redesign` (!270), targeting `main` directly with nothing
stacked under it. Every part below is commits on this branch; none gets its
own MR. Merge `origin/main`, never rebase. Delete this file before the MR
leaves draft.

Two goals:

1. **Nothing Pokémon outside `examples/pokedex`.** No fixture, golden,
   harness route, library doc, code comment or docs page names a Pokémon,
   PokéAPI or the Pokédex, except a `design/` sentence pointing at the
   example where a rule came from it.
2. **A fuller Pokédex.** Choose a Pokédex, browse its Pokémon as a grid of
   pictures, tap one for a page of tabs about it, with many pictures -- in
   the reader's language, PokéAPI's names and descriptions as well as the
   app's own words.

## Rules for every part

- Write for part 10 from part 7 on: every word the app itself shows is a
  `$"..."` literal, and every name or text PokéAPI gives per language is
  picked through one function in `names.sngl` (part 6's `speciesName`,
  `genusOf`, `latestFlavor` and `englishName` are the ones to fold into it),
  so part 10 adds languages rather than rewriting screens.

- A compiler fix the example needs gets its own fixture, and that fixture
  uses neutral names. Goal 1 holds for new fixtures too.
- The check for goal 1, run before every push:

  ```
  git grep -niE 'pok[eé](mon|api|dex)|base_experience|base_stat|front_default|\b(pikachu|bulbasaur|ivysaur|venusaur|charmander|charmeleon|squirtle|ditto|mew|missingno|pichu|raichu|eevee)\b' -- ':!examples/pokedex' ':!TODO.md'
  ```

  Its only hits are the pointers listed under part 3. It matches whole
  names, so `bespoke`, `spoken` and a `poke()` stay out of it.

- `go tool verify` passes at the end of each part; recorded runs are
  `-update`d after any codegen change.
- At the end of each part: commit, push, update the MR description, tick the
  part here, and write the prompt for the next session.
- Recording runs: gtk4 needs a display. Run a headless compositor
  (`XDG_RUNTIME_DIR=<short dir> WLR_BACKENDS=headless WLR_RENDERER=pixman cage -- sleep infinity`),
  pass `XDG_RUNTIME_DIR` and `WAYLAND_DISPLAY=wayland-0`, and keep port 47819
  free. Use `go test . -run TestGolden -update`, `go test ./codegen/platform/gtk4 -update`,
  `go test ./codegen/platform/html -update`, and
  `ANDROID_HOME=$HOME/Android/Sdk go test ./cmd/sngl -run TestScript -update`.
  Read each golden diff for a change in behaviour, not only a new hash. Edit
  nothing while a suite runs.
- The `sngl` binary hands off to `./cmd/sngl` in the current directory. To
  compare two compilers, run each one from its own checkout.
- An `#id` is a declaration in its component's scope (`design/nodes.md`). It
  shadows a package func the body calls, and that applies inside an `if` too
  (part 6's `#number` became `#nationalNo` for this). Beside a var, prop or
  method of the component it is an error. Name ids so that they never match a
  function the page calls.
- A program's write to a control fires none of its events. Only the user or
  a test's trigger fires `@change` or `@input`, so no screen may rely on a
  bound write to run a handler.
- The gtk4 and android scaffolds are Go (`codegen/platform/*/scaffold_files.go`),
  not templates. A platform helper goes there.

## Part 1: neutral `openapi:` fixtures

The `openapi:` scheme's fixtures run on a trimmed copy of PokéAPI's document.
Replace it with a small hand-written one in a neutral domain (a petstore,
OpenAPI's own sample) that keeps every feature the trimmed copy exercised.

- [X] List what `testdata/openapi_pokeapi.txtar` exercises before deleting
  it: 3.1 null unions in both spellings, `allOf` wrapping a `$ref`, a paged
  list, path and query parameters, `readOnly`, fields the import leaves out
  with a warning, operation-name casing (`codegen/scheme/openapi/names.go`).
- [X] `testdata/openapi_petstore.txtar` (or one golden per feature) covering
  all of it.
- [X] `cmd/sngl/testdata/openapi_runs.txt` against the neutral document;
  the harness's `/api/v2/pokemon/` routes become neutral ones.
- [X] `cmd/sngl/testdata/error_openapi_oneof.txt`'s comment,
  `lib/x/scheme/openapi/openapi.sngl`'s doc, `codegen/scheme/openapi/{names,spec}.go`
  comments, `design/import-schemes.md`'s examples.

## Part 2: neutral `http`, `json` and `async` fixtures and docs

- [X] `cmd/sngl/httpserver_test.go`: `/pokemon/{name}` becomes
  `/books/{title}` ("dune", and "war and peace" with null fields);
  `cmd/sngl/testdata/http_runs.txt` follows.
- [X] `testdata/async_query.sngl`, `async_query_loop.sngl`,
  `async_query_go.txtar`, `async_query_bubbletea.txtar`,
  `cmd/sngl/testdata/async_query_bubbletea_runs.txt`: fruit names of the
  same lengths; goldens and records regenerated. `effect_changes_runs.txt`
  only had a `poke()`.
- [X] `testdata/json.sngl`, `cmd/sngl/testdata/json_runs.txt`,
  `testdata/http_json.txtar`: a book in place of a Pokémon.
- [X] `cmd/sngl/testdata/context_func_value_param_runs.txt` and the comment in
  `internal/lower/context.go` name `petRetrieveCall`.
- [X] `lib/async/doc.sngl`, `lib/http/doc.sngl`, `lib/json/doc.sngl`,
  `lib/json/json.sngl`, `design/stdlib.md`: neutral examples;
  `front_default` leaves `openapi_renamed.txt` and `codegen/scheme/openapi/names.go`.

## Part 3: the example tests itself

`cmd/sngl/testdata/pokeapi/**` and `pokedex_runs.txt` move into the example.
The harness gains one generic feature in place of PokéAPI routes.

- [X] Recorded answers move to `examples/pokedex/testdata/http/`, and the
  tests to `examples/pokedex/pokedex_test.sngl`.
- [X] The harness serves each `examples/<name>/testdata/http/` as static
  files under `/examples/<name>/`.
- [X] One `cmd/sngl/testdata/examples_runs.txt` runs `sngl test` on every
  example that has tests, on all six targets. No example is named in it.
  html runs each component a test takes as a static page: `sngl test` has
  no route-mode launcher (`--platform=html --language=go` never connects).
- [X] `docs/learn/pokedex.md` folds into `examples/pokedex/README.md`;
  `docs/learn/index.md` and `internal/cmd/docsgen/examples.go` stop naming it.
- [X] `cmd/sngl/script_test.go`'s comment on copying the repository stops
  naming `examples/pokedex` once every example is copied alike.
- [X] `design/import-schemes.md` and `design/nodes.md`: one sentence each
  pointing at `examples/pokedex` where a rule came from it; the rest moves to
  the README. These are the goal-1 grep's only hits.

## Part 4: the document and the recordings

- [X] Correct `pokeapi.yml` where the redesign reads what it does not
  declare, each listed in the README: `PokemonSprites` (back, shiny, female,
  `other.official-artwork`, `other.home`), species fields (`genera`,
  `flavor_text_entries`, `egg_groups`, `gender_rate`, `capture_rate`,
  `varieties`, `habitat`), and whatever `PokedexDetail` needs.
- [X] Record answers: `pokedexList`; Kanto and one other regional dex; and
  Pokémon, species and chain for a three-stage line (Bulbasaur), a branching
  one (Eevee), one with a female sprite (Pikachu), one with forms (Vulpix's
  Alolan form or Charizard's Megas). Each decodes.

## Part 5: home, a Pokédex as a grid

- [X] A `ui.select` over `pokedexList`, defaulting to National; the dex's
  name, region and count beside it.
- [X] One `pokedexRetrieve` per dex. Each card shows its official artwork,
  its number in this dex and its name. Artwork comes from the species id in
  the entry's URL, so a card makes no request of its own.
- [X] Rows of three, built by a helper in `names.sngl` (`Style` has no wrap
  or grid columns), 30 cards at a time with "Show more".
- [X] The search covers the whole dex.

## Part 6: the detail page, header and tabs

- [X] `/pokemon/{name}`: Back, large artwork, name, national number, genus,
  type badges.
- [X] `ui.tabs`: About, Stats, Evolution, Sprites, Moves.
- [X] About: latest English description, height and weight in units,
  abilities with hidden ones marked, habitat, egg groups, gender ratio,
  capture rate.
- [X] Stats: base-stat bars, total, the EVs it gives.

## Part 7: Evolution

- [X] The chain as artwork cards with the trigger between stages ("Lv. 16",
  "Thunder Stone", "High Friendship"); tapping one navigates to it.
- [X] A branching chain (Eevee) lays out as rows.

Notes:

- An evolution card is a component under a `for`. If an override in it holds
  an effect, it is built once per copy, so each card gets its own effects and
  handles.
- The detail page has only `@back` today. A stage's card needs an event of
  its own up to `app.sngl`, which calls `pages.go(detail, Mon{name=...})`, as
  home's `@open` does. The page must follow its `name` prop changing, since
  the species query is keyed on it.
- A link's `evolution_details` is a list of `PokemonEvolution`, often
  several per stage. Eevee's chain has one Leafeon and one Glaceon entry per
  game, and a stage's `is_default` entry is the one to show. A regional form
  is a second entry carrying `evolved_pokemon_form`: Raichu's Alolan form by
  Thunder Stone, and Ninetales' by Ice Stone from Alolan Vulpix. Decide
  whether such a form gets a card of its own or waits for part 8's forms, and
  say so in the README. A stage with no detail marked `is_default` shows its
  first.
- Write a trigger's text through one function in `names.sngl` that takes
  `$"..."` words, so that part 10 can translate it. Cover `min_level`
  ("Lv. 16"), `item` ("Thunder Stone"), `min_happiness` ("High
  Friendship"), with `time_of_day` ("High Friendship, day"), and
  `known_move_type` (Sylveon). Item and trigger names stay PokéAPI's slugs
  made readable until part 10 asks for their per-language names.
- Recorded chains (`testdata/http/api/v2/evolution-chain/`): 1 Bulbasaur and 2
  Charmander (level), 10 Pichu (a baby, friendship, a stone, an Alolan
  form), 15 Vulpix (stones, an Alolan form) and 67 Eevee (eight branches).
  The page asks for the chain with `poke.evolutionChainRetrieve(id=idOf(about.evolutionChain.url))`, a request keyed on the chain id, as `testEveryChainDecodes` does. Trim any new
  recording the way the README says.
- Tests: each chain's stages and trigger text, a branching chain's rows, and
  tapping a stage. Each must fail when its behaviour is reverted, and every
  test must pass on all six targets through `examples_runs.txt`.

## Part 8: Sprites

- [X] A labeled gallery: front and back, normal and shiny, female where
  there is one, official artwork normal and shiny, Home.
- [X] Alternate forms from `species.varieties`, each with its picture,
  tapping to its page.
- [X] The regional forms part 7 left to this part: Alolan Raichu and Alolan
  Ninetales as cards of the Evolution tab, beside their species' stage, from
  the default detail carrying `evolved_pokemon_form` (the README's "A
  regional form waits for the Sprites tab's forms").

Notes:

- A form is a Pokémon, not a species: `/pokemon/{name}` opens a species, and
  `artwork` is keyed on the species id. A form's page and picture need the
  Pokémon (`pokemonRetrieve`, its `sprites.other.officialArtwork`), so a
  form's card is a request of its own, built once per copy under the `for`.
- `inLanguage` in `names.sngl` is the one picker for PokéAPI's per-language
  names and texts; a form's name (`PokemonFormDetail.names`) goes through it.

## Part 9: Moves

- [X] A `ui.table` of the moves learned by level in the latest version group.
- [X] A `ui.select` of learn method: level, machine, egg, tutor.

Notes:

- The dex chosen on home is package state (`dexName`, `chosenDex`), and the
  version group is the latest, in `versionGroups`' release order, that the dex
  covers (all of them for National) and the Pokémon learns moves in.
- Gaps found and left, for part 11 to look at: the detail page built at run
  time (under an `if`) does not build on fyne or gtk4 -- a lift nested in a
  lifted panel hands its props names the outer scope lacks (`__pN`), as at
  HEAD; a test's own write to a component var re-renders no slot inside a
  context provider on html, fyne or gtk4 (a program's write does);
  bubbletea keeps a hidden instance's cells, where every other target starts
  a fresh instance; on gtk4 a select changed in an `if` with no tabs around
  it once left the page's value unchanged in a bisect, unexplained; and the
  app itself does not build on android (`sngl build`), as at HEAD: a page's
  `@back { pages.back() }` names `pages__nav`, the stack's controller, where
  the composable it is written in holds none.

## Part 10: every language

SNGL has the features -- `$"..."` strings backed by `i18n.manifest.json`
(`examples/hello-i18n`), ICU plurals, `i18n.numberFloat`, and the
`i18n.locale` context, which a subtree or a test overrides -- and PokéAPI has
the data. Languages: English, French, German, Spanish and Japanese.

- [X] A language choice in the app's shell, a `ui.select` providing
  `i18n.locale` around the `nav.stack`, starting at the process locale and at
  English where that is none of the five.
- [X] One function from a SNGL locale to PokéAPI's language names (`fr`,
  `de`, `es`, `ja-Hrkt` and `ja`, `en`), and one picker over any list of
  per-language entries, falling back to English, then to the resource's own
  name made readable: species names, genera, the newest description in the
  language, dex names.
- [X] The names a page shows that live on resources of their own -- types,
  abilities, stats, egg groups, habitat, the items and triggers in an
  evolution, moves and learn methods -- asked for by name where shown, each a
  query keyed on it. Say in the README what that costs per page, and whether
  a resource asked for twice on one screen is asked for twice.
- [X] The app's own words through `$"..."` and a manifest for the five
  languages: tab labels, About's labels, "Show more", the search placeholder,
  the errors, "Genderless", the dex count as a plural ("1 Pokémon" / "40
  Pokémon"). Numbers through `i18n.numberFloat`, so French reads "0,7 m".
- [X] Re-record what parts 4-9 trimmed to English, keeping the five
  languages, and record the per-name resources the screens ask for. The
  README's trim rules say which languages are kept.
- [X] Tests set `i18n.locale` with `t.setContext`: a species' name, genus and
  description in Japanese and French; a type and an ability name; a word
  from the manifest; a plural; a decimal comma; and a language PokéAPI lacks
  for an entry falling back to English. Each fails when its behaviour is
  reverted.

Notes:

- Decided with the user: names on resources of their own go in a page store
  (`names`, `kind/slug` to the names in every language) filled by a query
  per distinct name a tab shows, so the line functions still return strings;
  Japanese prefers `ja` (kanji), then `ja-hrkt`; the Moves tab's note is a
  plural whose forms differ, so the plural test can fail; learn methods are
  never asked for, since the select keeps the app's `$"..."` labels.
- PokéAPI spells kana Japanese `ja-hrkt`, not `ja-Hrkt`. A habitat is named
  in English, French and Spanish only, in lower case; a stat and an egg group
  in kana only; a dex and a form have no Japanese name.
- Left for part 11: a card's name on home and in Evolution is the species'
  slug made readable (a card asks for nothing), and so is a dex's region; a
  language chosen while the Moves tab shows a way other than level shows the
  level moves, the old label matching no choice; a Japanese description's
  line breaks become ASCII spaces (`oneLine`).
- On html and android a test's `t.setContext` redraws nothing: a test's read
  of a node's prop is evaluated in the test's locale, but a select's choices
  stay in the language they were drawn in, so a test cannot pick a choice by
  its translated label (`testHowManyMovesIsAPlural` does not).
- gtk4 failed five of the tests, each a compiler bug with a neutral script
  test on all six targets: an instance under a node of a slot's body was
  built afresh by every render, so each answer re-rendered the tab and asked
  for every name again; a rebuilt drop-down took the old one's address and
  was offered no choices (the select part 9 saw unchanged); a render re-entered
  from an answer that arrived during it; a reused instance's `@load` ran for
  the last key. Fixing those found a crashed test program reported as `ok`,
  a query's `fetch` prop never coloured as waiting (html, fyne, gtk4 and
  android), bubbletea refusing an `@load` that reads its loop variable, and
  android writing a function's local as the page's var. The MR description
  lists each with its fixture.

## Part 11: every target, and the MR

- [ ] Every line the example builds with `join` (`dexLine`, `oneLine`,
  `display`) is built with `builder` once !265 is on main, as the user asked.

- [ ] The example's tests cover each tab, the dex switch and the language
  switch, and pass on all six targets through `examples_runs.txt`.
- [ ] Look at it running on html, bubbletea, fyne, gtk4 and android;
  screenshots in the MR description.
- [ ] README describes the redesign: files, the request per screen, where
  each target stands, the document corrections.
- [ ] A review agent passes over the whole branch; fix what it finds.
- [ ] Delete this file; take the MR out of draft.
