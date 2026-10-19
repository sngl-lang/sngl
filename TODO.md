# Pokédex redesign

One MR, `pokedex-7-redesign`, stacked on !268 (`pokedex-6-example`). Every
part below is commits on this branch; none gets its own MR. Merge
`origin/main` as !267, !268 and !269 land. Delete this file before the MR
leaves draft.

Two goals:

1. **Nothing Pokémon outside `examples/pokedex`.** No fixture, golden,
   harness route, library doc, code comment or docs page names a Pokémon,
   PokéAPI or the Pokédex, except a `design/` sentence pointing at the
   example where a rule came from it.
2. **A fuller Pokédex.** Choose a Pokédex, browse its Pokémon as a grid of
   pictures, tap one for a page of tabs about it, with many pictures.

## Rules for every part

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

- [ ] Recorded answers move to `examples/pokedex/testdata/http/`, and the
  tests to `examples/pokedex/pokedex_test.sngl`.
- [ ] The harness serves each `examples/<name>/testdata/http/` as static
  files under `/examples/<name>/`.
- [ ] One `cmd/sngl/testdata/examples_runs.txt` runs `sngl test` on every
  example that has tests, on all six targets (html in route mode where the
  example needs it). No example is named in it.
- [ ] `docs/learn/pokedex.md` folds into `examples/pokedex/README.md`;
  `docs/learn/index.md` and `internal/cmd/docsgen/examples.go` stop naming it.
- [ ] `cmd/sngl/script_test.go`'s comment on copying the repository stops
  naming `examples/pokedex` once every example is copied alike.
- [ ] `design/import-schemes.md` and `design/nodes.md`: one sentence each
  pointing at `examples/pokedex` where a rule came from it; the rest moves to
  the README. These are the goal-1 grep's only hits.

## Part 4: the document and the recordings

- [ ] Correct `pokeapi.yml` where the redesign reads what it does not
  declare, each listed in the README: `PokemonSprites` (back, shiny, female,
  `other.official-artwork`, `other.home`), species fields (`genera`,
  `flavor_text_entries`, `egg_groups`, `gender_rate`, `capture_rate`,
  `varieties`, `habitat`), and whatever `PokedexDetail` needs.
- [ ] Record answers: `pokedexList`; Kanto and one other regional dex; and
  Pokémon, species and chain for a three-stage line (Bulbasaur), a branching
  one (Eevee), one with a female sprite (Pikachu), one with forms (Vulpix's
  Alolan form or Charizard's Megas). Each decodes.

## Part 5: home, a Pokédex as a grid

- [ ] A `ui.select` over `pokedexList`, defaulting to National; the dex's
  name, region and count beside it.
- [ ] One `pokedexRetrieve` per dex. Each card shows its official artwork,
  its number in this dex and its name. Artwork comes from the species id in
  the entry's URL, so a card makes no request of its own.
- [ ] Rows of three, built by a helper in `names.sngl` (`Style` has no wrap
  or grid columns), 30 cards at a time with "Show more".
- [ ] The search covers the whole dex.

## Part 6: the detail page, header and tabs

- [ ] `/pokemon/{name}`: Back, large artwork, name, national number, genus,
  type badges.
- [ ] `ui.tabs`: About, Stats, Evolution, Sprites, Moves.
- [ ] About: latest English description, height and weight in units,
  abilities with hidden ones marked, habitat, egg groups, gender ratio,
  capture rate.
- [ ] Stats: base-stat bars, total, the EVs it gives.

## Part 7: Evolution

- [ ] The chain as artwork cards with the trigger between stages ("Lv. 16",
  "Thunder Stone", "High Friendship"); tapping one navigates to it.
- [ ] A branching chain (Eevee) lays out as rows.

## Part 8: Sprites

- [ ] A labeled gallery: front and back, normal and shiny, female where
  there is one, official artwork normal and shiny, Home.
- [ ] Alternate forms from `species.varieties`, each with its picture,
  tapping to its page.

## Part 9: Moves

- [ ] A `ui.table` of the moves learned by level in the latest version group.
- [ ] A `ui.select` of learn method: level, machine, egg, tutor.

## Part 10: every target, and the MR

- [ ] The example's tests cover each tab and the dex switch, and pass on all
  six targets through `examples_runs.txt`.
- [ ] Look at it running on html, bubbletea, fyne, gtk4 and android;
  screenshots in the MR description.
- [ ] README describes the redesign: files, the request per screen, where
  each target stands, the document corrections.
- [ ] A review agent passes over the whole branch; fix what it finds.
- [ ] Delete this file; take the MR out of draft.
