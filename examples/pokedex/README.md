# Pokédex

Every Pokémon from [PokéAPI](https://pokeapi.co), a page of names at a time,
and a page per Pokémon with its picture, its types, its base stats and its
evolution chain. It builds for html, fyne, gtk4, bubbletea and android, and
the interpreter runs it. Nothing in it makes a request by hand, and nothing in
it says how a target waits for one.

```
sngl generate --platform html      --lang go     -o /tmp/dex-web examples/pokedex
sngl generate --platform bubbletea --lang go     -o /tmp/dex-tui examples/pokedex
sngl generate --platform android   --lang kotlin -o /tmp/dex-apk examples/pokedex
```

## How it is put together

| file                | what is in it                                                       |
|---------------------|---------------------------------------------------------------------|
| `app.sngl`          | the window: a `nav.stack` of the list and a page per Pokémon        |
| `list.sngl`         | the list, keyed on `offset`; a card per name, each its own request  |
| `detail.sngl`       | one Pokémon, and the evolution chain two requests behind it         |
| `names.sngl`        | ids out of PokéAPI's URLs, names as the games print them, the chain |
| `pokeapi.yml`       | PokéAPI's OpenAPI document, vendored                                |
| `pokedex_test.sngl` | the tests, against the answers in `testdata/http`                   |

**The API is an import.** `import poke "openapi:./pokeapi.yml"` reads
PokéAPI's own OpenAPI document and writes a SNGL package from it: a struct for
each schema (`poke.PokemonDetail`, `poke.PaginatedPokemonSummaryList`), an
async function for each operation (`poke.pokemonRetrieveCall(id="pikachu")`,
answering an `async.Result`), and, for each `GET`, a component that asks when
it mounts. A JSON key that is no SNGL name is camelCased and keeps its wire
name in a mark, so `front_default` is `sprites.frontDefault`.

`poke.server` is a context holding the document's first server,
`https://pokeapi.co`. Provide another around a subtree, or in a test, and
every request under it goes there instead.

**A request is a component.** A screen writes the component where it wants
the answer:

```sngl
poke.pokemonRetrieve(id=name, :value=mon, :pending=loading, :failure=failure)
```

`poke.pokemonRetrieve` is `async.query` keyed on `id`. It asks when the page
mounts and again whenever `name` changes; it keeps the last answer while the
next is on its way, so the page does not blank between Pokémon; the latest
request wins when two overlap; and an answer that arrives after the page is
gone lands nowhere. A failure is a value beside the answer, not a raise, so a
page draws a spinner or what went wrong next to whatever it still has.
`loading` and `failure` are the page's own state, bound with `:` the way a
text field's value is, and a page that does not care leaves them out.

**More is a change of key.** The list keys its request on `offset`, so "More"
moves it, and `@load` appends each page that arrives to the names already
shown:

```sngl
poke.pokemonList(limit=pageSize, offset=offset, :pending=loading, :failure=failure, @load(page) {
    for var r = page.results {
        names.push(r)
    }
    count = page.count
})
```

The search filters what has loaded: PokéAPI has no endpoint that searches.

**A card per Pokémon asks for its own.** Each card is an instance with its own
`pokemonRetrieve`, keyed on its name, for its picture and types: the instance
is the key, so nothing caches per argument, and a card a search hides and
shows again asks again.

**The evolution chain is two requests behind the first.** The species names
its chain by URL, and the chain names its stages, so `evolution` writes a
query under an `if` on each answer before it:

```sngl
poke.pokemonSpeciesRetrieve(id=species, :value=about)
if about != null {
    poke.evolutionChainRetrieve(id=idOf(about.evolutionChain.url), :value=chain)
}
```

A stage other than the one showing is a button going to its page.

**Pages.** The app is a `nav.stack` of two pages. The detail page's params are
a struct, and its href names their field:

```sngl
nav.page #detail(href=`/pokemon/{name}`, title="Pokémon", params=Mon{name="bulbasaur"}) {
    component content(p) {
        ui.button(text="Back", @click { pages.back() })
        detailPage(name=p.name, @open(name) { pages.go(detail, Mon{name=name}) })
    }
}
```

On html each page is a route, so the app is served by a Go program
(`--lang go`). Elsewhere the stack is the target's own: a NavHost on android,
a field of the model on the desktop and in the terminal.

## Where each target stands

| target      | what runs                                                                   |
|-------------|-----------------------------------------------------------------------------|
| `none`      | the interpreter, all of it                                                  |
| `html`      | a Go server (`--lang go`), `/pokemon/{name}` a route; tests run as pages    |
| `bubbletea` | all of it, a sprite as half-block cells; answers come back through `Update` |
| `fyne`      | all of it                                                                   |
| `gtk4`      | all of it                                                                   |
| `android`   | all of it; the manifest asks for `INTERNET` because the program fetches     |

`--lang none` on html is refused where the detail page is written: a static
site writes one document per page, and `/pokemon/{name}` is a pattern no file
can be. A test takes a component rather than the window, so html runs each as
a static page of its own all the same.

## Testing it

`pokedex_test.sngl` points each screen at `http://127.0.0.1:47819/examples/pokedex`
through `poke.server`, where `internal/testhttp` answers from `testdata/http`.
`cmd/sngl/testdata/examples_runs.txt` runs it on all six targets, and
`go tool verify` on all but android. Without that server nothing answers
there, so a bare `sngl test` of this directory fails every test that waits
for a request.

Each test takes a component wrapping one of the example's under the provider,
so what it reads is one instance deep: a compiled test reaches into the
instance its component renders, and no further.

```sngl
component ivysaur ui.node {
    poke.server(local) {
        detailPage #detail(name="ivysaur", @open(n) { opened = n })
    }
}

func testTheDetailPageShowsThePokemon(t test.Test, c ivysaur) {
    t.wait(func() => c.detail.mon != null, 5000)
    t.assert(c.detail.mon != null && c.detail.mon.id == 2)
}
```

`testdata/http` is PokéAPI's answers for five Pokémon, recorded from
https://pokeapi.co and trimmed: each long list cut to its first entries,
every link pointed at the harness's server, and every sprite at its
`/dot.png`, so nothing a test runs reaches the network. A request's path
names its file with the trailing slash dropped, then `@` and the query in key
order where there is one, then `.json`: `/api/v2/pokemon/ivysaur/` is
`api/v2/pokemon/ivysaur.json`, and the list's second page,
`/api/v2/pokemon/?limit=2&offset=2`, is `api/v2/pokemon@limit=2&offset=2.json`.

These tests are why a fyne test build queues `async.do`'s closure for
`t.wait` to run (`design/nodes.md`): fyne's test driver runs `fyne.Do` on the
goroutine that calls it, so a request's answer rebuilt the list's widgets
while a test clicked others, and fyne crashed laying them out.

## The document

An importer believes the document, and PokéAPI's says things its answers do
not: that a species always evolves from another, that every field of an
evolution's conditions is present, that a chain's next stages are a map of
anything. As published, a Pokémon, a species and an evolution chain all fail
to decode -- `$.evolves_from_species: want an object` is the failure a page
shows. So the correction belongs in the document the program is written
against.

`pokeapi.yml` is PokéAPI's `openapi.yml` at commit
[`a003ae375b69`](https://github.com/PokeAPI/pokeapi/blob/a003ae375b69a99907ec273fe100d97e7f36321c/openapi.yml),
with these corrections:

- `EvolutionChainLink.evolves_to`'s items are `$ref: EvolutionChainLink`. The
  document says `{type: object, additionalProperties: {}}`, a map of any
  value, which the import leaves out, and with it every stage after the
  first.
- `EvolutionChainDetail.baby_trigger_item`, `PokemonAbilityPast.ability`,
  `PokemonSpeciesDetail.evolves_from_species` and
  `PokemonSpeciesDetail.habitat` may be `null`.
- `PokemonEvolution` requires only `trigger`: every other field of it is
  `null` in most answers.
- `PokemonHeldItem.pokemon` and `TypePokemon.pokemon` are not required: the
  answers carry neither.

The import still says, at every build, that it leaves out `sprites` on
`PokemonFormSprites` and `TypeSprite`, which the document gives no type.
