# Pokédex

A choice of Pokédex from [PokéAPI](https://pokeapi.co) and a grid of its
Pokémon, and a page per species: a header of its artwork, name, number, genus
and types over tabs about it. It builds for html, fyne, gtk4, bubbletea and android, and
the interpreter runs it. Nothing in it makes a request by hand, and nothing in
it says how a target waits for one.

```
sngl generate --platform html      --lang go     -o /tmp/dex-web examples/pokedex
sngl generate --platform bubbletea --lang go     -o /tmp/dex-tui examples/pokedex
sngl generate --platform android   --lang kotlin -o /tmp/dex-apk examples/pokedex
```

## How it is put together

| file                | what is in it                                                                   |
|---------------------|---------------------------------------------------------------------------------|
| `app.sngl`          | the window: a `nav.stack` of home and a page per Pokémon                        |
| `home.sngl`         | a `ui.select` of Pokédex and a grid of its Pokémon, 30 at a time                |
| `detail.sngl`       | a species' page: a header over About, Stats, Evolution and two tabs to come     |
| `names.sngl`        | ids, names, rows, the artwork, an evolution's rows, and what a page's lines say |
| `pokeapi.yml`       | PokéAPI's OpenAPI document, vendored                                            |
| `pokedex_test.sngl` | the tests, against the answers in `testdata/http`                               |

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

**Home is two requests.** One lists the Pokédexes, with a limit that holds
every one PokéAPI has (it answers 20 a page otherwise), and fills the
`ui.select`. The other is the dex chosen, keyed on its name, so choosing
another moves the key; `@change` maps the label chosen back to the dex's name:

```sngl
poke.pokedexList(limit=dexLimit, offset=0, @load(page) { dexes = page.results })
poke.pokedexRetrieve(id=dexName, :value=dex, :pending=loading, :failure=failure)
```

Beside the select are the dex's English name, its region ("every region"
for the National dex, which has none) and how many Pokémon it holds.

**Every entry arrives with the dex**, so the search covers the whole dex and
"Show more" asks for nothing: it shows the next 30 of what matches. The grid
is rows of three that `rows` in `names.sngl` cuts, since a `Style` neither
wraps a row nor lays out columns.

**A card asks for nothing.** It shows the entry's number in this dex, its name
and its official artwork, whose URL is the species id out of the entry's URL
put into `artwork`, a context holding where the artwork is:

```sngl
context #artwork(`https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/other/official-artwork/{id}.png`)
```

A test provides one with no `{id}` in it, the harness's `/dot.png`, so no card
a test renders reaches the network.

**A page is three requests, each after the first keyed on it.** A card opens its
species by name, and a species' name is not always a Pokémon's: the species
deoxys is the Pokémon deoxys-normal. So the page asks for the species, and
then for its default variety:

```sngl
poke.pokemonSpeciesRetrieve(id=name, :value=about, :pending=loading, :failure=failure)
if about != null {
    poke.pokemonRetrieve(id=defaultVariety(about.varieties, about.name), :value=mon)
}
```

The header needs only the species -- its artwork through `artwork`, its
English name and genus, its national number as `#001` -- but for its types,
which wait for the Pokémon. Under it is a `ui.tabs`: About, Stats, Evolution,
Sprites and Moves, the last two still to come.

**About** is the species' newest English description on one line (the games
break a description with newlines and form feeds where their text box did),
the Pokémon's height and weight in metres and kilograms -- `unit length { m }`
and `unit mass { kg }`, since PokéAPI gives tenths of each -- its abilities with
a hidden one marked, the species' habitat (which may be `null`), egg groups,
gender ratio (from `gender_rate`, eighths female or -1 for none) and capture
rate. **Stats** is a bar per base stat against 255, their total, and the effort
values defeating the Pokémon gives. Each line's text is a function in
`names.sngl`, which `testNames` checks beside the page's own tests.

Every name and text PokéAPI gives per language -- a species' name and genus,
its newest description, a dex's name -- is picked by one function,
`inLanguage`, out of the entries PokéAPI lists for it, the reader's language
being English until the app offers others.

**Evolution** is the species' evolution chain, the page's third request,
keyed on the chain's id out of the species' `evolution_chain` URL:

```sngl
poke.evolutionChainRetrieve(id=idOf(about.evolutionChain.url), :value=evolution)
```

A chain is a tree, and the tab draws it as rows, one for each line from its
first stage to a last (`evolutionRows`): Bulbasaur's is one row of three, and
Eevee's eight rows of two, each starting at Eevee. A stage is a card of its
artwork and its name, after the way it is reached -- "Lv. 16", "Thunder
Stone", "High Friendship, day", "High Friendship, knowing a Fairy move".
PokéAPI lists every way a stage is reached, game by game, and marks the ones
to show `is_default`: Leafeon by a Leaf Stone, not by levelling up in Eterna
Forest. A stage shows its first default detail, or its first where none is
marked. The words are the app's own, each a `$"..."` in `triggerOf`, and an
item, a type or a trigger is PokéAPI's name made readable, until the names
PokéAPI gives each language are asked for.

Tapping a card opens that species' page. `detailPage` hands the name up
through `@open`, and the app's stack goes to it as home's `@open` does; where
the stack keeps the page and changes its `name`, every request follows the
new name, since each is keyed on what came before it.

**A regional form waits for the Sprites tab's forms.** The chain names
species, and a form is not one: Alolan Raichu is a second default detail of
Raichu's stage, carrying `evolved_pokemon_form`, and so is Alolan Ninetales,
by an Ice Stone from Alolan Vulpix. A card opens `/pokemon/{name}`, a
species' page, and finds its artwork by species id, so a form's card would
open its species and show its species' picture. Raichu's stage therefore
shows its first default detail, the Thunder Stone, and Ninetales' the Fire
Stone; the forms get cards of their own with the forms the Sprites tab lists.

**Pages.** The app is a `nav.stack` of two pages. The detail page's params are
a struct, and its href names their field:

```sngl
nav.page #detail(href=`/pokemon/{name}`, title="Pokémon", params=Mon{name="bulbasaur"}) {
    component content(p) {
        detailPage(name=p.name, @back { pages.back() }, @open(name) { pages.go(detail, Mon{name=name}) })
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
component bud ui.node {
    poke.server(local) {
        artwork(dot) {
            detailPage #detail(name="ivysaur")
        }
    }
}

func testStatsGiveEveryEffort(t test.Test, c bud) {
    t.wait(func() => c.detail.mon != null, 5000)
    t.assert(c.detail.tabs != null)
    if c.detail.tabs != null {
        c.detail.tabs.change(1)
    }
    t.assert(c.detail.total != null && c.detail.total.value == "405")
    t.assert(c.detail.effort != null && c.detail.effort.value == "1 Sp. Atk, 1 Sp. Def")
}
```

`c.detail.tabs.change(1)` chooses a tab as a user does, by pressing the one
at that index. A test is a root, so what a page's function reads of a context
is what the test provides: `testTheHeaderNamesTheSpecies` sets `artwork` with
`t.setContext` before it asks the page which picture it shows.

`testdata/http` is PokéAPI's answers, recorded from https://pokeapi.co: the
list of Pokédexes, as home asks for it (`limit=50`); the National, Kanto and
Johto dexes; and the Pokémon, its
species and its evolution chain for Bulbasaur's and Charmander's lines,
Eevee, Pikachu, Vulpix and Alolan Vulpix. Each is trimmed so nothing a test
runs reaches the network and no file is longer than the app reads: every
link points at the harness's server and every picture at its `/dot.png`; the
dex list keeps the three dexes recorded, and a dex its first 40
entries; a Pokémon's moves are Scarlet and Violet's, every one learned by
level and four of each other method; a species keeps its English genus,
names and first and last English descriptions; and every other long list is
cut to its first entries. A request's path
names its file with the trailing slash dropped, then `@` and the query in key
order where there is one, then `.json`: `/api/v2/pokemon/ivysaur/` is
`api/v2/pokemon/ivysaur.json`, and the list of Pokédexes,
`/api/v2/pokedex/?limit=50&offset=0`, is `api/v2/pokedex@limit=50&offset=0.json`.

These tests are why a fyne test build queues `async.do`'s closure for
`t.wait` to run (`design/nodes.md`): fyne's test driver runs `fyne.Do` on the
goroutine that calls it, so a request's answer rebuilt a page's widgets
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
- `PokemonSprites` declares every picture an answer carries, not only
  `front_default`: front and back, each normal, shiny, female and shiny
  female, and `other` (`PokemonOtherSprites`), holding `official-artwork` (`PokemonArtworkSprites`,
  normal and shiny) and `home` (`PokemonHomeSprites`, normal, shiny and both
  female). Each picture may be `null`, as a female one is for a Pokémon that
  looks the same either way.
- `PokedexDetail.region` may be `null`: the National dex has none.

The species' genus, descriptions, egg groups, gender and capture rates and
varieties were declared already, and decode as published; its habitat is the
correction above.

The import still says, at every build, that it leaves out `sprites` on
`PokemonFormSprites` and `TypeSprite`, which the document gives no type.
