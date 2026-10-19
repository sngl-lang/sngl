# Pokédex

Every Pokémon from [PokéAPI](https://pokeapi.co), a page of names at a time,
and a page per Pokémon with its picture, its types, its base stats and its
evolution chain. Written against the types and calls an `openapi:` import
writes from PokéAPI's own document, so nothing in it makes a request by hand.

```
sngl generate --platform html      --lang go     -o /tmp/dex-web examples/pokedex
sngl generate --platform bubbletea --lang go     -o /tmp/dex-tui examples/pokedex
sngl generate --platform android   --lang kotlin -o /tmp/dex-apk examples/pokedex
```

`cmd/sngl/testdata/pokedex_runs.txt` runs it on all six targets against
PokéAPI's answers recorded on the test harness's server.

## How it is put together

| file          | what is in it                                                       |
|---------------|---------------------------------------------------------------------|
| `app.sngl`    | the window: a `nav.stack` of the list and a page per Pokémon        |
| `list.sngl`   | the list, keyed on `offset`; a card per name, each its own request  |
| `detail.sngl` | one Pokémon, and the evolution chain two requests behind it         |
| `names.sngl`  | ids out of PokéAPI's URLs, names as the games print them, the chain |
| `pokeapi.yml` | PokéAPI's OpenAPI document, vendored                                |

**A request is a component.** `import poke "openapi:./pokeapi.yml"` writes a
component per `GET` over `async.query`, and a screen writes it where it wants
the answer:

```sngl
poke.pokemonRetrieve(id=name, :value=mon, :pending=loading, :failure=failure)
```

It asks when the page mounts and again whenever `name` changes, keeps the last
answer while it asks, and drops an answer that arrives after the page is gone.
`loading` and `failure` are the page's own state, and the page draws a spinner
or the failure beside whatever it still has.

**More is a change of key.** The list keys its request on `offset`, so "More"
moves it, and `@load` appends each page that arrives to the names already
shown. The search filters what has loaded: PokéAPI has no endpoint that
searches.

**A card per Pokémon asks for its own.** Each card is an instance with its own
`pokemonRetrieve`, keyed on its name: the instance is the key, so nothing
caches per argument, and a card a search hides and shows again asks again.

**The evolution chain is two requests behind the first.** The species names
its chain by URL, and the chain names its stages, so `evolution` writes a
query under an `if` on each answer before it. A stage other than the one
showing is a button going to its page.

## Where each target stands

| target      | what runs                                                                             |
|-------------|---------------------------------------------------------------------------------------|
| `none`      | the interpreter; `sngl test` reads each answer as there                               |
| `html`      | a Go server (`--lang go`), `/pokemon/{name}` a route; tests run each screen as a page |
| `bubbletea` | all of it, a sprite as half-block cells; answers come back through `Update`           |
| `fyne`      | all of it                                                                             |
| `gtk4`      | all of it                                                                             |
| `android`   | all of it; the manifest asks for `INTERNET` because the program fetches               |

`--lang none` on html is refused where the detail page is written: a static
site writes one document per page, and `/pokemon/{name}` is a pattern no file
can be.

## The document

`pokeapi.yml` is PokéAPI's `openapi.yml` at commit
[`a003ae375b69`](https://github.com/PokeAPI/pokeapi/blob/a003ae375b69a99907ec273fe100d97e7f36321c/openapi.yml),
with the corrections below. Each is a place the document says something
PokéAPI's answers do not: as published, a Pokémon, a species and an
evolution chain all fail to decode.

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
