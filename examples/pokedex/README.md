# Pokédex

A choice of Pokédex from [PokéAPI](https://pokeapi.co) and a grid of its
Pokémon, and a page per species and per form of one: a header of its artwork,
name, number, genus and types over tabs about it, in English, French, German,
Spanish or Japanese. The window's bar names the page shown, goes back through
the pages, and holds a menu choosing the language and the Pokédex, both kept
from one run to the next. It builds for html, fyne, gtk4, bubbletea and
android, and the interpreter runs it. Nothing in it makes a request by hand,
and nothing in it says how a target waits for one.

```
sngl generate --platform html      --lang go     -o /tmp/dex-web examples/pokedex
sngl generate --platform bubbletea --lang go     -o /tmp/dex-tui examples/pokedex
sngl generate --platform android   --lang kotlin -o /tmp/dex-apk examples/pokedex
```

The app is the Pokédex wherever it is installed: `output` names it and gives
it `icon.svg`, and android's package is `us.duckfam.sngl.pokedex`, its
launcher red.

## How it is put together

| file                 | what is in it                                                                                   |
|----------------------|-------------------------------------------------------------------------------------------------|
| `app.sngl`           | the window: its bar and menu over a `nav.stack` of home, a page per Pokémon and a page per form |
| `home.sngl`          | the Pokédex chosen, a search, and a grid of its Pokémon, 30 at a time                           |
| `detail.sngl`        | a species' page: a header over About, Stats, Evolution, Sprites and Moves                       |
| `names.sngl`         | ids, names, rows, the artwork, an evolution's rows, a gallery, the moves, and lines' text       |
| `icon.svg`           | the app's icon, which `output` names                                                            |
| `pokeapi.yml`        | PokéAPI's OpenAPI document, vendored                                                            |
| `i18n.manifest.json` | the app's own words in French, German, Spanish and Japanese                                     |
| `pokedex_test.sngl`  | the tests, against the answers in `testdata/http`                                               |

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

**The window chooses the dex, and home shows it.** The window lists the
Pokédexes, with a limit that holds every one PokéAPI has (it answers 20 a
page otherwise), and its menu's Pokédex entry offers each, the one chosen
checked. Home asks for the dex chosen, keyed on its name, so choosing another
moves the key and starts the grid again:

```sngl
poke.pokedexList(limit=dexLimit, offset=0, @load(page) { dexes = page.results })
poke.pokedexRetrieve(id=dexName, :value=chosenDex, :pending=loading, :failure=failure)
```

Over the grid are the dex's name, its region ("every region" for the National
dex, which has none) and how many Pokémon it holds, and a search of it.

The dex chosen is the package's, not the page's: `dexName` and the answer,
`chosenDex`, are package `var`s the menu and home's request write. So home
shown again starts at the dex chosen, and a Pokémon's page lists the moves of
the latest game that dex covers. `dexName` is kept from one run to the next
(see [Kept settings](#kept-settings)).

**Every entry arrives with the dex**, so a page more asks for nothing: the
grid's scroll fires `@reachEnd` as its end comes into view, and home shows the
next 30 of what matches. A first page too
short to scroll has its end in view already, so the next one follows it at
once. The grid is rows of three that `rows` in `names.sngl` cuts, since a
`Style` neither wraps a row nor lays out columns.

**A card shows its species' name in the reader's language.** An entry names
its species only by PokéAPI's slug, `nidoran-f`, and the names in each
language are the species' own, so home asks for the species of each card
shown, and a card shows the slug made readable until it answers. The names
are the package's (`names` in `names.sngl`), which a Pokémon's page fills
too, so nothing named once is asked for again, and a species that fails
keeps its slug rather than being asked again.

What home has asked for is `asking`, a list it only ever adds to: an effect
adds each species coming into view that no page has named, and the requests
are a `for` over it. A `for` matches its copies by position, so a request
dropping out of the middle of the list, as one would once answered, moved
every one after it, and each started again:

```sngl
effect(on="{chosenDex != null ? chosenDex.name : ""}/{namingAll}/{shown}", @mount { ask() })
for var slug = asking {
    poke.pokemonSpeciesRetrieve(id=slug, @load(got) { names["species/{got.name}"] = speciesNamed(got.names) }, @fail { names["species/{slug}"] = [] })
}
```

**The search matches what the cards say.** It matches the slug or the name
in the reader's language, so the first search of a dex asks for the species
of every entry, under a spinner until all have answered; a search of a dex
already named asks for nothing.

A clickable `ui.card`, opened by a click anywhere on it, it shows the
entry's number in this dex, its name and its official artwork, whose URL is
the species id out of the entry's URL put into `artwork`, a context holding
where the artwork is:

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

The header needs only the species -- its artwork through `artwork`, its name
and genus, its national number as `#001` -- but for its types, which wait for
the Pokémon and are asked for by name (see [Languages](#languages)). A type
is a `typeBadge`, a `ui.badge` in the type's own color (`typeColors` in
`names.sngl`), its name dark over the light types and white over the rest.
Under it is a `ui.tabs`: About, Stats, Evolution, Sprites and Moves, drawn as
each target draws tabs -- a Material TabRow on android, a GtkStackSwitcher on
gtk4, an AppTabs on fyne, a bar of boxed labels on bubbletea. Every panel is
mounted as the page opens and kept while another shows, so a panel left and
come back to is as it was, and on android and html the panels swipe. Moves is
the one that waits: its rows ask for a move each and for their types, so they
ask the first time its tab is shown (`movesShown`).

Evolution, Sprites and Moves are wider than a phone, so each scrolls
sideways, a `ui.scroll(direction=horizontal)` around its rows, while the page
scrolls down.

**About** is the species' newest description in the reader's language on one
line (the games
break a description with newlines and form feeds where their text box did),
the Pokémon's height and weight in meters and kilograms, with feet and inches
and pounds after them -- `unit length { m, in = 0.0254m }` and
`unit mass { kg, lb = 0.45359237kg }`, since PokéAPI gives tenths of each, and
`l / 1in` is a length in inches -- its abilities with
a hidden one marked, the species' habitat (which may be `null`), egg groups,
gender ratio (from `gender_rate`, eighths female or -1 for none) and capture
rate. **Stats** is a bar per base stat against 255, colored by how good the
stat is (`statColor`: red under 30, through orange, yellow and two greens, to
teal from 150), their total, and the effort
values defeating the Pokémon gives. Each line's text is a function in
`names.sngl`, which `testNames` checks beside the page's own tests; one that
puts its pieces together -- `display`, `oneLine`, home's `dexLine` -- builds
it with a `builder`.

Every name and text PokéAPI gives per language -- a species' name and genus,
its newest description, a dex's name -- is picked by one function,
`inLanguage`, out of the entries PokéAPI lists for it, and every name that
lives on a resource of its own -- an ability, a habitat, an egg group, a stat
-- is a request for it ([Languages](#languages)).

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
item, a type, a move or a trigger is PokéAPI's name for it in the reader's
language, asked for by name.

Tapping a card opens that species' page. `detailPage` hands the name up
through `@open`, and the app's stack goes to it as home's `@open` does; where
the stack keeps the page and changes its `name`, every request follows the
new name, since each is keyed on what came before it.

**A regional form is a card beside its species' stage.** The chain names
species, and a form is not one: Alolan Raichu is a second default detail of
Raichu's stage, carrying `evolved_pokemon_form`, and so is Alolan Ninetales,
by an Ice Stone from Alolan Vulpix. So a stage shows its first default detail
that reaches no form -- the Thunder Stone, the Fire Stone -- and under its card
a card per form, after the way the form is reached (`formSteps`).

**A form's card asks for the form.** Nothing in a chain or a species says a
form's name or which artwork is its own, so the card is a request of its own,
built once per copy under the `for`:

```sngl
poke.pokemonFormRetrieve(id=form, :value=got)
```

The answer names the form in each language, picked by `inLanguage` like every
other name ("Alolan Raichu"), and the Pokémon it is a form of, whose number in
its URL is the artwork's -- 10100, not Raichu's 26. A card shows the form's
own name made readable until the answer arrives, and keeps the last answer
while the next is on its way. Tapping it opens that Pokémon's page.

**A form's page is its species' page, showing the form.** `/pokemon/{name}`
opens a species and its default variety; `/pokemon/{name}/{form}` opens the
same page showing one of its varieties, `detailPage(name="vulpix", form="vulpix-alola")`. The species and its chain are the species', so About and
Evolution are too; the Pokémon asked for is the form, so the header's artwork,
types, Stats and Sprites are the form's, and the title is the form's name, a
fourth request keyed on the Pokémon's first form:

```sngl
poke.pokemonRetrieve(id=shown(), :value=mon, :failure=monFailure)
if form != "" && mon != null && mon.forms.length() > 0 {
    poke.pokemonFormRetrieve(id=mon.forms[0].name, :value=shape)
}
```

**Sprites** is a labeled gallery of the Pokémon's pictures, four to a row:
front and back, normal and shiny, female where it looks different (Pikachu
does, Bulbasaur not, whose female pictures PokéAPI gives as `null`), its
official artwork normal and shiny, and its Home picture, normal, shiny and
female (`gallery`). Below it are the species' other varieties as form cards:
Pikachu's caps, Alolan Vulpix, and on a form's page the species' own, which
opens the species' page. A card asks for the form by its Pokémon's name, which
is how PokéAPI names a variety's form.

**Moves** is a list of the moves the Pokémon shown learns, a level, a name,
a type, a power and an accuracy per line, under a `ui.select` of the way it learns them: by level, by
machine, from an egg or from a tutor (`learnMethods`). By level the rows are
ordered by level, and moves learned at one level by PokéAPI's `order` --
Tackle before Growl; the other ways have no level, and are ordered by name
(`movesOf`). The moves are those of one version group (`latestGroup`): the
latest that both the dex chosen on home covers -- every group, for the
National dex, which names none -- and the Pokémon learns any move in.
PokéAPI's ids are not release order (red-green-japan is 28, after
scarlet-violet's 25), so `versionGroups` in `names.sngl` lists the groups in
the order their games came out, PokéAPI's `order`, and a group added after
it counts as the newest. Bulbasaur's moves are Scarlet and Violet's under
the National dex and FireRed and LeafGreen's under Kanto. A form's page lists
the form's moves, since the Pokémon asked for is the form. Each line is a
`moveLine`, columns of fixed widths, rather than a `ui.table`, whose cells
are strings: a move's type is its `typeBadge`. Its type, power and accuracy
come with the move's own answer, a power or accuracy it has none of is "—",
and its type's name is asked for by name. Under the list the tab says how many moves the way teaches, none included, and the headings
and the select's labels are the app's own words; a move's name is PokéAPI's
in the reader's language, asked for by name, and the ways with no level order
by it.

**Pages.** The app is a `nav.stack` of three pages: home, a species and a
form of one. The detail page's params are a struct, and its href names their
field:

```sngl
nav.page #detail(href=`/pokemon/{name}`, title="Pokémon", params=Mon{name="bulbasaur"}, titleOf=func(p Mon) => display(p.name)) {
    component content(p) {
        detailPage(name=p.name, @open(name, form) { show(name, form) })
    }
}
```

`titleOf` is the page's title as a function of its params, so the window's
bar reads "Bulbasaur" while that page shows, and "Pokédex" on home. It is the
species' name as its slug spells it, in every language: the title is asked
for before the page has fetched anything.

A form's page is a second `nav.page`, `/pokemon/{name}/{form}` with a `Form`
of both, since html serves a page's params from its path alone. `show`, a func
of the app, goes to one or the other; each page names the other, and a page's
handle read above it in source has the params type its props bind.

On html each page is a route, so the app is served by a Go program
(`--lang go`). Elsewhere the stack is the target's own: a NavHost on android,
a field of the model on the desktop and in the terminal.

## The window

`Pokedex` is its window, so the stack, the language and the dexes are in
scope where the window's bar is written:

```sngl
ui.window #pokedex(title=pages.current.title, canGoBack=pages.canGoBack(), @back { pages.back() }) {
    component menu {
        ui.menuItem(label=$"Language") {
            for var l = languages {
                ui.menuItem(label=l.label, checked=l.label == language, @select { language = l.label })
            }
        }
        ui.menuItem(label=$"Pokédex") {
            for var d = dexes {
                ui.menuItem(label=display(d.name), checked=d.name == dexName, @select { chooseDex(d.name) })
            }
        }
    }
    …
}
```

The bar is the page's title, a way back while the stack has somewhere to go
back to, and the menu, a submenu for the language and one for the dex, each
marking the one chosen. No page draws a back button of its own. Each target
draws the bar as its own:

| target      | the bar                                                                                                                |
|-------------|------------------------------------------------------------------------------------------------------------------------|
| `android`   | a Material3 TopAppBar: the launcher icon, the title, an up arrow, and an action per submenu; the system back goes back |
| `html`      | a `<header>`: the title, an up arrow, and the menu as a menubar; the browser's back goes back                          |
| `gtk4`      | the header bar: the title, a back button, and a menu button opening the menu                                           |
| `fyne`      | the window's main menu, and a back button over the content                                                             |
| `bubbletea` | a title line, `‹` before it while esc goes back; a terminal has no menu                                                |

### Kept settings

The language and the dex outlive the process. `sngl:settings` binds each to
a name, loads what was kept when the window mounts, and keeps every change:

```sngl
settings.kept(name="language", default=languageLabel(i18n.defaultLocale()), :value=language)
settings.kept(name="dexName", default="national", :value=dexName)
```

Each target keeps them where it keeps an app's settings: SharedPreferences on
android, `localStorage` on html, fyne's preferences, and on gtk4 and
bubbletea a JSON file per setting under the user's config directory, in
`Pokédex/`. Each is null until the kept value loads: the window speaks the
process's language meanwhile, and home asks for no dex until it knows which.
A test keeps values in memory and starts with none.

## Languages

The app reads in English, French, German, Spanish or Japanese. The window's
Language menu offers the five, each named in itself, and the window is under
an `i18n.locale` provider holding the one chosen, so its title and its menu
are in it too:

```sngl
i18n.locale(localeLabeled(language)) {
    ui.window #pokedex(…) { … }
}
```

It starts at the language kept from the last run, and on a first run at the
process's locale -- `fr_CA.UTF-8` is French -- or English where that is none
of the five (`languageLabel`).

**What PokéAPI says is picked by one function.** `pokeLanguages` turns a SNGL
locale into PokéAPI's names for its language, in the order to look for them,
English last: `fr`, `de` and `es` alone, and Japanese as `ja`, then `ja-hrkt`.
PokéAPI writes Japanese twice, `ja` in kanji as the newer games print it and
`ja-hrkt` in kana, and some resources only in kana, a stat or an egg group
among them. `inLanguage` takes any list of per-language entries and gives the
last entry in the first of those languages that has one -- the newest
description, since PokéAPI lists them game by game -- and the resource's own
name made readable where none has. A species' name and genus, its newest
description, a dex's name and a form's name all go through it.

PokéAPI does not name everything in every language, so the fallback is
reached: a habitat has English, French and Spanish names only, in lower case
("grassland", "champs"), and German reads it in English; a dex and a form have
no Japanese name.

**A name that lives on a resource of its own is a request for it.** A type,
an ability, a stat, an egg group, a habitat, the items, types, moves and
triggers an evolution names, and a move are asked for by name where the page
shows them, each a query keyed on it under a `for` over the names shown:

```sngl
for var a = mon.abilities {
    poke.abilityRetrieve(id=a.ability.name, @load(got) { names["ability/{got.name}"] = abilityNamed(got.names) })
}
```

`names` is the page's: a map from `kind/slug` to the resource's names in every
language. The lines' functions -- `abilitiesOf`, `effortOf`, `triggerOf`,
`movesOf` -- take it and still answer strings, through `called`, which reads
the name in the reader's language and the slug made readable until the answer
arrives. So the lines and the select keep their strings, and choosing
another language asks for nothing again.

What that costs per page, beside the page's own three or four requests:

| where      | asked for                                      | Bulbasaur |
|------------|------------------------------------------------|-----------|
| the header | each type                                      | 2         |
| About      | each ability, the habitat, each egg group      | 5         |
| Stats      | each stat                                      | 6         |
| Evolution  | each item, type, move or trigger a step names  | 0         |
| Moves      | each move the way chosen teaches, and its type | 18        |

Eevee's Evolution asks for six: five stones and the Fairy type. Every panel
mounts as the page opens, so every tab's requests but Moves' go out then;
Moves' go out the first time its tab is shown.

**A resource is asked for once per place it is shown.** A tab asks for each
name it shows once, however many times it shows it -- a move learned at two
levels, a stone two stages reach. Nothing is kept between places: the
header's Fairy type and a Fairy move in Evolution are two requests. A tab
shown again asks for nothing, since its panel stayed mounted, and a page
opened again starts with no names.

**The app's own words are `$"..."`**, and `i18n.manifest.json` translates
each into the four other languages: the tab labels, About's and Stats'
labels, the sprite labels, the Moves tab's headings, choices and note, the
words of an evolution's step, the search's placeholder, the
errors, "Genderless", and the menu's "Language" and "Pokédex". A word is a
key, and a key has one meaning: "Back" is a sprite's, the Pokémon's back
(French "Dos"). A line joining several names joins them with
`$"{text}, {p}"`, so Japanese joins with 「、」.

Two counts are plurals: a dex's, "40 Pokémon" ("40匹" in Japanese), and the
Moves tab's note, "It learns no moves this way.", "It learns 1 move this
way.", "It learns 15 moves this way." Numbers are written as the reader
writes them, by `i18n.numberFloat` and `i18n.numberInt`: Bulbasaur is "0.7
m" in English and "0,7 m" in French, and "87.5% male, 12.5% female" is
"87,5 % mâles, 12,5 % femelles".

The Moves select's labels are the app's own words, and the way chosen is
PokéAPI's name for it: choosing a label maps it back by comparing it with the
same `$"..."` labels (`methodNamed`), and an `effect` keyed on `i18n.locale`
writes the label of the way shown again as the language changes
(`methodLabeled`), so a language chosen while the tab shows the machine's
moves still shows them, under "CT". Learn methods are therefore never asked
for.

Left in English, or as PokéAPI's slug made readable: a card's name on home
and in Evolution -- a species' name lives on the species, and a card asks for
nothing -- a dex's region, the Pokédex menu's entries, and a page's title,
the slug of the species its params name.

## Where each target stands

| target      | what runs                                                                                                                                                                                                                                         |
|-------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `none`      | the interpreter, all of it                                                                                                                                                                                                                        |
| `html`      | a Go server (`--lang go`), `/pokemon/{name}` a route; tests run as pages; a card opens on Enter or Space as on a click, and not on a click its button took                                                                                        |
| `bubbletea` | all of it, a sprite as half-block cells; a row's columns line up, its badges are spaced, and a box's `alignItems` centers its parts, so an evolution card's name sits under its picture; a test that waits sees a timer tick and a picture arrive |
| `fyne`      | all of it; a badge is drawn on its color, a card's lines are centered, and a table's columns follow the list they are read from                                                                                                                   |
| `gtk4`      | all of it, styled: sizes, gaps, padding, fonts, colors and rounded corners; a line starts at the left as on html, `alignItems` and `textAlign` place it, and a label given a width wraps inside it                                                |
| `android`   | all of it; the manifest asks for `INTERNET` because the program fetches; a test that waits moves Compose's clock, so a timer ticks                                                                                                                |

`--lang none` on html is refused where the detail page is written: a static
site writes one document per page, and `/pokemon/{name}` is a pattern no file
can be. A test takes a component rather than the window, so html runs each as
a static page of its own all the same; a test of the whole app, `shell`, runs
in home's document, and a test build writes none for a page at a pattern,
which no test reaches, since a `go` would end the run.

## Testing it

`pokedex_test.sngl` points each screen at `http://127.0.0.1:47819/examples/pokedex`
through `poke.server`, where `internal/testhttp` answers from `testdata/http`.
`cmd/sngl/testdata/examples_runs.txt` runs it on all six targets, and
`go tool verify` on all six, android from its recorded run. Without that server nothing answers
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
    t.assert(c.detail.effort != null && c.detail.effort.value == "1 Special Attack, 1 Special Defense")
}
```

`c.detail.tabs.change(1)` chooses a tab as a user does, by pressing the one
at that index. A test is a root, so what a page's function reads of a context
is what the test provides: `testTheHeaderNamesTheSpecies` sets `artwork` with
`t.setContext` before it asks the page which picture it shows, and a test of
another language sets `i18n.locale` the same way:

```sngl
func testADecimalCommaInFrench(t test.Test, c seed) {
    t.setContext(i18n.locale, "fr")
    t.wait(func() => c.detail.mon != null, 5000)
    t.assert(c.detail.height != null && c.detail.height.value == "0,7 m")
}
```

Every other test reads English. `examples_runs.txt` sets `LC_ALL` to
`en_US.UTF-8`, since a test that sets no locale reads the process's, and a
name PokéAPI gives arrives after the page does, so a test of one waits for it.

`testdata/http` is PokéAPI's answers, recorded from https://pokeapi.co: the
list of Pokédexes, as home asks for it (`limit=50`); the National, Kanto and
Johto dexes; the Pokémon, its
species and its evolution chain for Bulbasaur's and Charmander's lines,
Eevee, Pikachu, Raichu, Vulpix and Ninetales; the Pokémon of Alolan Vulpix,
Raichu and Ninetales; the forms the tests' cards ask for, under
`api/v2/pokemon-form/`; and the resources a test's page asks a name of: every
recorded Pokémon's types and abilities, every recorded species' habitat and
egg groups, the six stats, the stones the recorded chains name, and the moves
Bulbasaur learns in Scarlet and Violet, the one Moves tab a test shows. Each
is trimmed so nothing a test
runs reaches the network and no file is longer than the app reads: every
link points at the harness's server and every picture at its `/dot.png`, a
sprite's with its path under PokéAPI's sprites in the query
(`dot.png?sprite=back/female/25.png`), which the server ignores and a test
reads to tell the pictures apart; the
dex list keeps the three dexes recorded, and a dex its first 40
entries; a Pokémon's moves are those of Scarlet and Violet, FireRed and
LeafGreen, and Crystal -- the latest group of each recorded dex -- every one
learned by level and, per group, the first four of each other method, a
move's method in a group being its first detail's there; every name or text
given per language is kept in the five languages the app offers, Japanese as
both `ja` and `ja-hrkt`, and nothing else: a species' genus and names, its
first and last description in each, and its first three varieties; a form's
and a dex's names, and a form's first description in each; a resource asked
for by name its names, its other per-language lists in English alone, and
each other list cut to its first two entries; and every other long list is
cut to its first entries. A request's path
names its file with the trailing slash dropped, then `@` and the query in key
order where there is one, then `.json`: `/api/v2/pokemon/ivysaur/` is
`api/v2/pokemon/ivysaur.json`, and the list of Pokédexes,
`/api/v2/pokedex/?limit=50&offset=0`, is `api/v2/pokedex@limit=50&offset=0.json`.

These tests are why a fyne test build queues `async.do`'s closure for
`t.wait` to run (`design/nodes.md`): fyne's test driver runs `fyne.Do` on the
goroutine that calls it, so a request's answer rebuilt a page's widgets
while a test clicked others, and fyne crashed laying them out. And why it
drops what the last test's page posted as the next test starts: the Moves tab
redraws its list for each move's answer, and the next test's waits ran the
last test's redraws and timed out.

**The window's menu** is tested through `shell`, the app's own `Pokedex`
under the test's server. A test opens a submenu and picks from it as a person
does, `c.app.pokedex.menu[0].select()` and then the entry under it: choosing
"Español" turns home's dex line into "Nacional · todas las regiones · 40
Pokémon", and choosing Kanto is the dex home shows and the one kept. A test's
read of a node's prop is read under the providers around the node, so it
reads home in the language the shell chose, not the test's. No test checks
the kept language, only the kept dex.

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
- What the names the app asks for decode as: `TypeDetail.move_damage_class`
  and `StatDetail.move_damage_class` may be `null` (Fairy, HP, Speed),
  `ItemDetail.fling_effect` may be (a Thunder Stone), and so may
  `MoveDetail.contest_effect` (Seed Bomb), `MoveDetail.effect_chance`, and
  `MoveChange.effect_chance` and `MoveChange.type`, a move's past values
  (Tackle).

The species' genus, descriptions, egg groups, gender and capture rates and
varieties were declared already, and decode as published; its habitat is the
correction above.

The import still says, at every build, that it leaves out `sprites` on
`PokemonFormSprites` and `TypeSprite`, which the document gives no type.
