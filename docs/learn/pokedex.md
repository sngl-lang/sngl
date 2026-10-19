---
title: A Pokédex
order: 40
description: An app over a real API, from its OpenAPI document to six targets
---

`examples/pokedex` browses [PokéAPI](https://pokeapi.co): every Pokémon, a page
of names at a time, and a page per Pokémon with its picture, types, base stats
and evolution chain. It builds for html, fyne, gtk4, bubbletea and android,
and the interpreter runs it. Nothing in it makes a request by hand, and
nothing in it says how a target waits for one.

## The API is an import

<!-- SNGL-nocheck -->

```sngl
import poke "openapi:./pokeapi.yml"
```

The `openapi:` scheme reads PokéAPI's own OpenAPI document and writes a SNGL
package from it: a struct for each schema (`poke.PokemonDetail`,
`poke.PaginatedPokemonSummaryList`), an async function for each operation
(`poke.pokemonRetrieveCall(id="pikachu")`, answering an `async.Result`), and,
for each `GET`, a component that asks when it mounts. A JSON key that is no
SNGL name is camelCased and keeps its wire name in a mark, so
`front_default` is `sprites.frontDefault`.

`poke.server` is a context holding the document's first server,
`https://pokeapi.co`. Provide another around a subtree, or in a test, and
every request under it goes there instead.

## A request is a component

<!-- SNGL-nocheck -->

```sngl
component detailPage(name string, @open(name string)) ui.node {
    var mon option[poke.PokemonDetail] = null
    var loading = false
    var failure option[async.Failure] = null

    poke.pokemonRetrieve(id=name, :value=mon, :pending=loading, :failure=failure)

    if mon == null && loading {
        ui.spinner(label="Loading {name}")
    }
    if failure != null {
        ui.text(value="Could not load {name}: {failure.message}")
    }
    if mon != null {
        ui.text(value="No. {mon.id}")
        …
    }
}
```

`poke.pokemonRetrieve` is `async.query` keyed on `id`. It asks when the page
mounts and again whenever `name` changes; it keeps the last answer while the
next is on its way, so the page does not blank between Pokémon; the latest
request wins when two overlap; and an answer that arrives after the page is
gone lands nowhere. A failure is a value beside the answer, not a raise, so a
page shows what went wrong next to whatever it still has.

`loading` and `failure` are the page's own state, bound with `:` the way a
text field's value is. A page that does not care leaves them out.

## Asking again

The list keys its request on `offset`. "More" moves it, and the page of names
that arrives is appended in `@load`:

<!-- SNGL-nocheck -->

```sngl
poke.pokemonList(limit=pageSize, offset=offset, :pending=loading, :failure=failure, @load(page) {
    for var r = page.results {
        names.push(r)
    }
    count = page.count
})
```

## A request per row

Each card in the list asks for its own Pokémon, for its picture and types:

<!-- SNGL-nocheck -->

```sngl
component card(name string, @open()) ui.node {
    var mon option[poke.PokemonDetail] = null

    poke.pokemonRetrieve(id=name, :value=mon)
    …
}
```

A card is an instance of its own, so the instance is the key: nothing caches
per argument, and a card that a search hides and shows again asks again.

## A request behind a request

The evolution chain is two requests behind the Pokémon: the species names its
chain by URL, and the chain names its stages. Each is a query under an `if` on
the answer before it:

<!-- SNGL-nocheck -->

```sngl
poke.pokemonSpeciesRetrieve(id=species, :value=about)
if about != null {
    poke.evolutionChainRetrieve(id=idOf(about.evolutionChain.url), :value=chain)
}
```

## Pages

The app is a `nav.stack` of two pages. The detail page's params are a struct,
and its href names their field:

<!-- SNGL-nocheck -->

```sngl
nav.page #detail(href=`/pokemon/{name}`, title="Pokémon", params=Mon{name="bulbasaur"}) {
    component content(p) {
        detailPage(name=p.name, @open(name) { pages.go(detail, Mon{name=name}) })
    }
}
```

On html each page is a route, so the app is served by a Go program
(`--lang go`): a static site writes one document per page, and
`/pokemon/{name}` is a pattern no file can be. Elsewhere the stack is the
target's own: a NavHost on android, a field of the model on the desktop and
in the terminal.

## When the document is wrong

An importer believes the document, and PokéAPI's says things its answers do
not: that a species always evolves from another, that every field of an
evolution's conditions is present, that a chain's next stages are a map of
anything. As published, a Pokémon, a species and an evolution chain all fail to
decode -- `$.evolves_from_species: want an object` is the failure a page
shows. The example vendors the document with those corrections, each listed
in its README beside the commit it was taken from, which is the honest place
for them: in the document the program is written against.

## Testing it

`cmd/sngl/testdata/pokedex_runs.txt` runs the example's components on all six
targets against PokéAPI's answers recorded on a local server. Each test wraps a
component in `poke.server(…)` pointing at that server and waits for the
answer:

<!-- SNGL-nocheck -->

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
