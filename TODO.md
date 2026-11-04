# Pokédex audit

A review of `examples/pokedex` and of what it compiles to on each target, at
`850332d7a`. The work below goes in the order written: the language's pain
points first, then the bugs and the generated code.

## Where the language makes this painful

1. **Failure and retry is hand-rolled per request.** Each of the detail page's
   four requests carries `xFailure`, `xFailedFor`, an `if x != xFailedFor`
   guard and its own sign, and home's `dexFailures` is state only a test
   reads. `formCard` says `#id.refresh()` needs every two-way prop bound, so
   the others could not use it: that is a compiler bug the example worked
   around. Fix it, and let `async.query` own "failed, not asked again until
   refreshed".
2. **A `for` matches its copies by position**, so home keeps `asking`
   append-only (never cleared when the dex changes) for its requests to keep
   their place. Wanted: identity tied to the loop, not a magic `key` prop on
   whatever the body holds, and not a wrapper that adds a level of nesting.
   Design open.
3. **No structural typing over generated types.** `lookup` is nine `if kind ==`
   branches, `names.sngl` has nine `xNamed` helpers identical but for their
   types, and `inLanguage` takes two accessor lambdas where a constraint on
   `{language.name, name}` would do. Try constraint aliases.
4. **Effect keys built as strings**
   (`on="{chosenDex != null ? chosenDex.name : ""}/{namingAll}/{shown}"`): a
   struct literal works. Example fix.
5. **Missing library pieces:**
   - no `sort`, so `movesOf` writes an insertion sort;
   - no grid or wrapping row, so `rows()` cuts the grid by hand;
   - `ui.select` binds its label rather than a value, which costs
     `methodNamed`, `methodLabeled` and an effect relabeling on a locale
     change.
6. **A hand-made cache in package vars** (`names`, `failures`, `round`) with a
   global counter driving retries. Bad design: compose the logic in a generic
   component, the way hooks compose in React.
7. **Compiler bugs worked around in comments:**
   - `home.sngl:141`: a request under an `if` in a `for` kept bubbletea from
     asking for another dex;
   - `detail.sngl:308`: the checker takes a call in a branch to reach `about`
     and its null test.
8. **Testing requests needs help.** 63 tests need 26 wrapper components, each
   `poke.server(local){ artwork(dot){ … } }`, and `testdata/http` is trimmed
   by hand. Ideas: a SNGL-level recorder of real requests, and a `TestMain`
   kind of setup shared by a file's tests.
9. **Example design and bugs:**
   - `idOf("")` indexes `-1` and panics;
   - `slugsOf` dedupes in O(n²) where `.contains` exists;
   - `names.sngl:673`'s `$"{count} {stat}"` warns on every build;
   - the kept language is its label ("Français"), not its locale;
   - the README contradicts itself on whether `names` is shared (lines 410
     and 431–436 against `names.sngl:330`);
   - a first search of the National dex mounts about 1,025 requests at once.

## Bugs

1. **html does not generate.** `sngl generate --platform html --lang go examples/pokedex`, the README's first command, fails with `prop "placeholder" cannot be rendered server-side`: `$"…"` reads the locale
   context, and `exprIsReactive` (`codegen/platform/html/rendermodel.go:308`)
   counts only state. No test generates the app for html in route mode.
2. **html never changes language.** `var __ctx_locale__inst0 = i18n.defaultLocale()` is never written again, so the menu's choice only
   rerenders the menu. Likely the same root as 1: a context bound to state is
   not reactive.
3. **html crashes on a species with no habitat.** Inlining `lookup` hoisted
   `wanted("habitat", state.about__inst0.habitat.name)` out of its
   `if about.habitat != null`. Every recorded species has a habitat.
4. **html keeps a reused card's old artwork**: an update writes
   `setAttribute("url")` on the `<img>`, and only creation sets `.src`
   (unconfirmed).
5. **Test code ships.** `tackleAndGrowl` and `stageOf` from
   `pokedex_test.sngl` are in every target's output, and bubbletea's ten
   "for tests" click methods (about 200 lines each) have no callers. Likely a
   function reached only from a lambda in a test survives DCE.
6. **Race on gtk4 and fyne**: fetch goroutines read `__prop___ctx_server`
   while renders rewrite it.

## Generated code

| target    | grade | why                                                                                                                                                                                               |
|-----------|-------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| html      | D     | 2.2 MB `server.go`, the same ~726 KB bundle once per route with the detail page four times in it; an empty server-rendered shell; every navigation a reload, dropping `names`; no gzip or caching |
| android   | C-    | a grid that is not lazy and has no keys; full-size bitmaps in a cache never evicted; a `names` write copies the map and recomposes home; JSON decoded on the main thread; no timeouts             |
| fyne      | C+    | gtk4's bloat, with refreshes that push derived props instead of rebuilding                                                                                                                        |
| gtk4      | C     | ~36% of `model.go` is slot fields no instance uses; one name arriving rebuilds the 1,219-line detail subtree; setters never compare                                                               |
| bubbletea | D+    | every fetch deep-copies the whole model and drops it (`m := m.snapshotRef(); _ = m`, 39 sites); `View()` recomputes everything per message; `detailPage` duplicated per nav page                  |

Across targets:

- No memoization or CSE: `moveRows()` runs four times a render, and
  `matching()` refilters the dex and calls `inLanguage` per entry whenever any
  name arrives.
- About 20 near-identical query-state components, one per call site.
- Value semantics deep-copy lists just built: `stepDetail`, 7 lines, is 358
  lines of Go.
- `sngljson.Decode` is reflective and two-pass, decodes every field the
  schema has, and rebuilds its codec tree per call.
- `snglhttp` uses `http.DefaultClient`: no timeout, no cap on requests in
  flight.
