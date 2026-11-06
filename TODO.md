# Pokédex audit

A review of `examples/pokedex` and of what it compiles to on each target, at
`850332d7a`. The work below goes in the order written: the language's pain
points first, then the bugs and the generated code.

## Where the language makes this painful

What the example worked around is done: requests and their retries are
queries (`asked` wraps the ones under a page's `if`), the grid is `ui.grid`,
moves sort by `before`, the Moves select holds a `LearnMethod`, and the kept
language is a locale. What is left:

1. **Testing requests needs help.** 63 tests need 26 wrapper components, each
   `poke.server(local){ artwork(dot){ … } }`, and `testdata/http` is trimmed
   by hand. Ideas: a SNGL-level recorder of real requests, and a `TestMain`
   kind of setup shared by a file's tests.
2. **A first search of the National dex mounts about 1,025 requests at
   once.** Nothing caps the requests in flight (see `snglhttp` below).
3. **A node's method is out of reach in a block built at run time.** On html,
   gtk4, fyne and android, `ask.refresh()` refuses to build where `#ask` is
   under a reactive `if` or in a tab of the same component (bubbletea answers
   it), so the example puts each such request in a component of its own,
   `asked`, whose instance holds the node.

## Bugs

7. **A two-way prop read after its write is stale in a run-time instance.**
   `value = x; change(value)` in an override hands `change` the old value
   where the component is built at run time (android's `Select__string`):
   `rewritePropMutationsToEmit` turns the write into an emit, and the read
   that follows still reads the parameter. The overrides now pass the
   picked value; the lowering still reads stale.
8. **The other triggers should move to overrides of their events.** Done
   for select and radio, which say how a test raises `@change` in SNGL
   (`func ui.select[T].change[platform](value)`); the generated invokers
   (`codegen.TriggerEventName`, `ir.PressedNode`, bubbletea's payload
   invokers) still answer every other event.
9. **html has no node handles in JS**, so its select's trigger calls the
   override's handler logic (`pickLabel`) rather than setting the element's
   value and dispatching `change`; android and bubbletea do the same.
10. **fyne and bubbletea render no radio**: each override is an empty box.
11. **`this.value = value` in a component method does not compile on fyne**
    (`undefined: this`).
12. **A trigger in a block's state instance falls back to the invoker.** gtk4
    holds a select under a reactive `if` or a tab in an instance of its
    block's state, whose methods a test has no route to, so the test's
    trigger is restored to the event (`restoreUnreachedTriggers`) and the
    invoker is handed the value: right for a string select, wrong for a
    typed one.
13. **bubbletea loses a write made in the waiting part of a call.** A
    query's `fetch=func(k) { var r = get(k); mon = "b"; return r }` runs on
    a goroutine against `m.snapshotRef()`, so `mon = "b"` lands on the copy
    and nothing rekeys; every other target writes the page's `mon`.
14. **Binding a two-way prop to an inner query's cell loses the query's
    method.** `component asked(:failure option[async.Failure] = null)` whose
    query binds `:failure=failure` and whose body calls `ask.refresh()`,
    placed under a reactive `if` with `:failure` bound, is refused on html,
    gtk4 and fyne as a node not spliced where it is written; with `:failure`
    left unbound (the query's cell moved to a wrapper) it builds.
15. **fyne gives a page's node and one of a run-time instance under its `if`
    one field.** A page writing `ui.text #failed` beside `if on { asked() }`,
    where `asked`'s body holds `loadError #failed`, builds `c.failed` for
    both: `c.failed.Add undefined (type *widget.Label …)`, or in the Pokédex
    `*Container has no setter for "text"`. The block's state gathers both
    after uniqueNodeIDs has held each owner's ids apart. `asked` names its
    sign `#problem`, an id nothing else in the example takes.
16. **fyne writes a two-way binding back under the name a child instance
    gave it.** A var a block's child instance reads as a prop (`shape`, as
    `__p4`) and an `asked(:value=shape)` inside that block binds is written
    back in the page as `__prop___p4 = __value`: `undefined: __prop___p4`.
    The Pokédex asks for the form beside the Pokémon, outside the header.
17. **fyne's test agent cannot reach a node a window's menu calls.** A
    `poke.pokedexList #dexesAsk(…)` in the window whose menu item's
    `@select` calls `dexesAsk.refresh()` builds a test agent reading
    `c.dexesAsk`, which the model does not hold. The window keeps asking
    again by forgetting the failure.

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
