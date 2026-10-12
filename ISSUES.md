# Issues from the newcomer tutorial walk

Found by walking `docs/learn/getting-started.md`, the Tour (`docs/learn/_tour.md`)
and the pages they link to as a newcomer: fresh `claude -p` sessions reading
only `docs/`, in an empty directory outside the checkout, with a clean module
environment. Two passes completed; a third was killed for low memory. Items
marked *reproduced* were confirmed by hand; the rest are from the passes'
reports.

## Decisions needed

- **No documented way to save a list when it changes.** The persistence
  section of getting-started, effective-sngl's "Reacting to a change" and the
  spec's "Variable handlers" all teach `var … @change { … }`, which has been
  replaced by `effect(on=)` but still checks. `effect(on=todos)` over a
  `list<Todo>` is refused: "an effect's `on` is the identity of its lifetime,
  so it must compare alike on every target, and list<string> does not".
  Wrapping it in an anonymous struct literal, `on={todos=todos}`, is refused
  the same way, naming the field: `list<string> (field todos of struct __anon_todos_fc0e38) does not`. A key like `{n=todos.length}` would
  check but misses a toggle that changes no length.

  *Decided:* an effect reports writes. `effect(on=todos, @update(t) { … })`
  runs after a write to what `on` reads, whatever its type; a list held in a
  cell also reports `@insert`, `@set`, `@remove` and `@clear`, and a key that
  does not compare may not handle `@mount`/`@unmount`
  (`lib/builtin/effect.sngl`, `testdata/effect_update.sngl`,
  `testdata/error_effect_changes.sngl`). `@update` is lowered by passEffect on
  every target but android (`testdata/effect_update.txtar`,
  `cmd/sngl/testdata/effect_update_runs.txt`); refused, with a position, are
  the change events everywhere, `@update` under a `for`, and `@update` on
  android (`cmd/sngl/testdata/effect_update_refused.txt`). Left: those three,
  then rewrite the three pages and remove `var … @change`.
- **Clone URL.** `installation.md` says
  `git clone https://github.com/sngl-lang/sngl.git`, but the module is
  `duckfam.us/sngl` and origin is `git.duckfam.us`. Which is canonical?

## Compiler and runtime bugs

### Break a tutorial program

1. **html: a checkbox in a `for` can't be checked** (*reproduced*; a
   concurrent session is fixing it). The handler is handed the DOM `Event` and
   reads `__event.checked`, so `done` becomes `undefined` and the re-render
   unchecks the box. Breaks the getting-started todo app, Tour lesson 15
   "Lists" and effective-sngl's todo pattern (`checked=… @change { …!! }` hits
   the same listener).

   ```sngl
   struct Item { done bool = false }
   ui.window {
       var items list<Item> = [Item{}]
       for var &item = items { ui.checkbox(:checked=item.done) }
   }
   ```

2. **`var … @change` never fires on assignment** (*reproduced*). Superseded by
   `effect(on=)` but not removed: it checks, and on Go it is wired only into
   the exported `SetX()` setter, never into the program's own writes; gtk4
   emits it nowhere; the interpreter does not run it
   (`testdata/test_data_events.sngl` says so). It should be refused, or
   removed with the docs above.

3. **`sngl run` can't build a program with a local `go:` import.** It builds in
   `~/.cache/sngl/build/run/<hash>` with `module tmp` and no `replace`, so the
   import `sngl check` resolved fails: `package myapp/store is not in std`.
   With a dotted module path it goes to the proxy and 404s. May interact with
   `SNGL_HOST_GO_MOD=` (empty) in the test environment; check under a normal
   one. Repro: the persistence section of getting-started.

4. **The persistence program won't build for `--lang go --platform html`**:
   `route / has a server-side action, so window "" is rendered per request — but a conditional or loop in it re-renders on the client (__renderSlot0), and route mode cannot emit both`.
   The message also leaks `__renderSlot0` and names the window `""` though it
   has a title.

5. **Kotlin miscompiles a `go:` import instead of refusing it.**
   `--lang kotlin --platform android` on the persistence program exits 0 and
   emits `remember { Load().toMutableStateList() }` with no import and no
   `Save`. `--lang go --platform android` emits the same plus an empty
   `golib`. The html/none target refuses the same program with a clear error.

6. **Bare `sngl generate` loses a build** (*reproduced*). With
   `output { go { bubbletea fyne } }`, both write `model.go` to the same
   directory and fyne's silently replaces bubbletea's; the output lists
   `model.go` twice. There is no per-target output directory option. The
   tutorial's own `output` example and Tour lesson 05 trigger it.

7. **Wrong default packages** (*reproduced*).
   - gtk4 generates `package main` with no `main()` into `-o ./ui`: neither
     importable nor buildable (`function main is undeclared in the main package`).
     `--opt package=ui` works. bubbletea, fyne and html default to `ui`.
   - Kotlin defaults to `package test.sngl.app`; so does android's
     `--opt main=true` project (namespace and applicationId too).

8. **bubbletea layout and widgets.**
   - An `input` in an `hbox` is padded to the terminal width, so the sibling
     button is off-screen (todo app "Add", Tour 10, 15). Tab+Enter still
     presses it.
   - Assigning `""` to a `:value`-bound var doesn't clear the text input, and
     the stale text writes back on refocus.
   - A checkbox draws no checked/unchecked glyph; Space toggles state that is
     never shown.
   - `hbox` gap is ignored: Tour 23 renders `> -1 + Reset`.
   - Long text is clipped, not wrapped (Tour 27).
   - Tour 36's 240×160 canvas fills the terminal with background and hides the
     buttons.

9. **`sngl test` off the interpreter.** effective-sngl's Testing examples pass
   on the default `none` runner and fail elsewhere:
   - element refs: html `Cannot read properties of undefined (reading 'value')`;
     Go `c.displayValue undefined (type *Model has no field or method displayValue)`;
   - `t.tick()`, `t.test(…)`, `t.must`: html `t is not a function`; Go
     `cannot call t (variable of type *testagent.T)`;
   - text output hides the failure reason on html (only `--format json` or
     `-v` shows it), and names are mangled: `/Increments`, `html//Ticks`;
   - `--platform all` summary counts don't add up (`2 tests, 4 failures`).

10. **Refs in a `for`.** On the default runner a ref in a loop over a list that
    started empty is nil after a push (`cannot select field "value" on <nil>`);
    starting non-empty works. `c.row.length` is refused
    (`no member "length" on component checkbox`) although effective-sngl says
    such a ref is a list.

    ```sngl
    component L ui.node {
        var names list<string> = []
        for var n = names { ui.text #item(value=n) }
    }
    func testRef(t test.Test, c L) {
        c.names.push("c")
        t.assert(c.item[0].value == "c")
    }
    ```

11. **Android Compose state**: the todo app's checkbox writes
    `todos[i].done = on` on a `data class` with `var` fields inside a
    `mutableStateListOf`, which Compose does not observe. Judged from the
    generated code; not run.

12. **html `ui.modal` is always visible**: the overlay's inline
    `display:flex` overrides the `hidden` attribute it is toggled with
    (effective-sngl "Modal open/close").

### Smaller

- Unknown `--opt` keys are accepted silently (`--opt bogus=1`), while the same
  prop in an `output` block is an error.
- `sngl lint` is listed in `--help` and fails `lint: not yet implemented`.
- Bare `sngl doc` opens a TUI and fails without a TTY, though its help says
  "Show stdlib component index"; `sngl doc fyne` falls back to text.
- `sngl test` takes `--language`; `run`, `generate` and `build` take `--lang`.
- `sngl preview` stops reloading after a rename-style save (`sed -i`, most
  editors) and stays stale after later in-place writes. Its sidebar says "No
  app state defined" for a window with a `var`, and lists a `fyne (none)`
  target.
- android `--opt main=true` writes `gradlew` mode 0644, as a curl-download
  script with no wrapper jar, and adds `coil-compose` the app doesn't use.
- `cannot initialize list<Todo> with list<Todo>` when a SNGL `Todo` meets
  `store.Todo`: the two names should be qualified.
- A missing closing brace reports `unexpected "EOF"` at the wrong line with
  about fifty expected-token names.
- `sngl run` reports `Error: go: exit status -1` when the built app is killed,
  blaming `go`.
- Errors print twice (`Error: X` then `X`).
- Generated Go exports internal names (`Visible__inst0`, `SetVisible__inst0`,
  `Measurement`).
- html emits `<input type="">`.
- html: a component method named `replace`, called from a test, comes out as
  `c.replaceAll(void 0, void 0)` -- the string method's spelling.

## Docs gaps still open

- **Platform options offline.** getting-started now points `--opt` at
  `/platforms/index.html`, which only the site build generates;
  `sngl generate --help` lists no keys, and `sngl doc <platform> platform`
  (which lists some) is mentioned nowhere.
- **Android embedding** has no Gradle snippet or versions (material3 ≥ 1.3
  for `PullToRefreshBox`, activity-compose, the Compose compiler plugin), and
  `--opt main=true` producing a whole project is undocumented for android.
- **gtk4 embedding** has no host example; its `BuildUI` takes a
  `gtk4rt.Handle`, unlike fyne's.
- **Tour lesson 34** never says where translations come from, so the `es-MX`
  block shows English.
- **"Use a persistence package for that platform"** (getting-started) has no
  pointer to one.
- `installation.md` gives no expected `sngl version` output; a source build
  prints `sngl dev (commit none, built unknown)`.
- The "module proxy cannot serve … builds against the latest sngl runtime"
  warning a source install from a dirty tree prints is explained nowhere.
- `sngl test --platform android` runs Gradle; nothing says what each test
  platform needs.
- effective-sngl uses `email.length()` while the tutorial and Tour use
  `todos.length`; nothing says both work.
- The getting-started and Tour Go snippets in islands are indented with
  spaces, not gofmt tabs.

## Environment only

- No Android device; an emulator run in the killed pass did build, install
  and launch the hello and todo apps.
- Wayland: `grim` and `x11grab` give no screenshot and `xdotool` sees no
  windows, so fyne and gtk4 were judged by starting and staying up.
- The `Gdk-WARNING … org.freedesktop.portal.Inhibit` line comes from the
  session.
- The proxy warning above came from a binary built from a dirty tree.
