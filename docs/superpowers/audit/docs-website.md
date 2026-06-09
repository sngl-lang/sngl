# Docs & Website Audit

Findings ranked roughly by impact within each section. Severity legend:
**wrong** (broken/incorrect), **outdated** (no longer true), **inconsistent**
(disagrees with itself or other docs), **orphan** (file/entry without
counterpart).

## 1. Syntax drift

### 1.1 `effective-sngl.md:182-183` — unit-literal example contains `msms`/`ss`/`mm`/`emem` typos (**wrong**)

```sngl
unit duration { ms, s = 1000msms, m = 60ss, h = 60mm }
unit measurement { px, em, rem = 16emem, vw, vh, pct }
```

Same typo also at line 750 (`interval=100msms`) and line 991 (`interval=50msms`).
The fixtures wouldn't lex (`msms` isn't a unit literal). Real declarations in
`lib/units.sngl` are `s = 1000ms`, `m = 60s`, `h = 60m`, `rem = 16em`. Most
egregious doc bug — a reader copy/pasting any of these gets a parser error.

### 1.2 `effective-sngl.md:444-447` — generic method declaration syntax is the dropped pre-`<T>.method` form (**outdated**)

```sngl
func list.push<T>(l list<T>, item T) list<T> { ... }
func list.reverse<T>(l list<T>) list<T> { ... }
func list.indexOf<T>(l list<T>, item T) { ... }
```

`lib/functions.sngl` uses the current shape: `func list<T>.push(item T) list<T> {}`.
The receiver's type params now go on the receiver (`list<T>`), not after the
method name, and the receiver is **not** a leading parameter — it's implicit
(invoked via method syntax `xs.push(x)`).

The third line also drops the return type entirely, which the parser would
reject after the recent `func name() Type` cleanup.

### 1.3 `effective-sngl.md:457-471, 1015-1016` — calling list methods as `list.filter(todos, ...)` (**outdated**)

```sngl
func active() => list.filter(todos, (t) => !t.done)
func labels() => list.map(todos, (t) => t.text)
...
func activeCount() => list.length(active)
```

After the type-method dispatch rewrite, the call form is `todos.filter(...)`,
`todos.map(...)`, `active.length()`. The static-call form would require the
receiver as the first explicit param — and `lib/functions.sngl` no longer
declares them that way. These examples likely fail to type-check.

### 1.4 `effective-sngl.md:201` — `var timeout duration = "5s"` assigns a string to a unit (**wrong**)

```sngl
var timeout duration = "5s"
```

Unit values can't be initialised from string literals. The correct form is
`var timeout duration = 5s` (unit literal). Reader would copy a value the
checker will reject.

### 1.5 `effective-sngl.md:215-217` — `var handler func() = null` mixed with unrelated coercion claim (**inconsistent**)

Line 260's coercion table says "`null` → `func(...)` compiles to a zero-value-returning callable stub" — but `func` types aren't `option<T>`-style nullable in the type model, and the `lib/functions.sngl` decls don't carry a null story. Either the coercion table or the type-system docs should be reconciled; treating funcs as nullable is at odds with "structs cannot be null" in the same file.

### 1.6 `effective-sngl.md:182, 446, 506` — `enum { … }` inline syntax (**inconsistent style**)

`size enum { small, medium, large } = "medium"` is documented as valid (and
matches the parser), but elsewhere docs and stdlib only use named enums. Worth
confirming this still parses; recent parser/spec audit (`docs/superpowers/audit/parser-checker.md`) flagged inline enum/struct in type position as fragile.

### 1.7 `effective-sngl.md:615` — claim that `if` does not support `else` (**wrong**)

```
Use `if` blocks. `if` does not support `else` -- use two `if` blocks with
opposite conditions (note: `else` is only available on `for` loops):
```

The grammar in `specification.md:45` and the lib's own examples both have
`if … { … } else { … }`. `tour.md:187` shows `if … { … } else { … }` working.
This guidance is just false.

### 1.8 `effective-sngl.md:794-808` — top-level `style primary { … }` block + `class="…"` prop (**outdated/wrong**)

```sngl
style primary {
    color = #0000ff
    fontWeight = "bold"
    ...
}
... text(value="hello", class="primary")
```

`internal/parser/sngl.ebnf:266` explicitly notes "style is no longer a keyword
— style blocks parse as ident StmtBlock via VisualOrStmt". No stdlib component
in `lib/components.sngl` declares a `class` prop. This whole "Named styles"
section is dead.

### 1.9 `tour.md:28` — `sngl run example.sngl --target html` (**wrong flag**)

`cmd/sngl/run.go:26-27` declares `--lang` and `--platform`, not `--target`.
First CLI invocation a reader sees in the tour — should be
`sngl run example.sngl --platform html`.

### 1.10 `effective-sngl.md:582` — `input(value=name, @input { name = event.value })` reads `event.*` from bare scope (**outdated**)

`tour.md:431` and the rest of the docs (and the stdlib examples) use the
typed-arg form `@input(e) { name = e.value }`. The bare `event` identifier in
event-handler scope may still work, but every other doc passes the payload
explicitly — this is a one-off inconsistency the tour and effective-sngl
should reconcile.

### 1.11 `tour.md:298, 312, 433, etc.` — `xs.length` without parens (**inconsistent**)

`lib/functions.sngl:136` declares `func list<T>.length() int {}` — a method.
It auto-fires as zero-arg func in expression position, so `tasks.length` works
today, but `README.md` and `docs/index.md` use `todos.length()` with parens.
Pick one convention (and prefer `()` since the value-method is a function).

### 1.12 `effective-sngl.md:582, 624, 935, 959` — `@input { name = event.value }` bare-event usage repeated (**inconsistent with tour**)

Tour uses the typed param form everywhere; effective-sngl mixes them. This is
the single biggest readability inconsistency in `effective-sngl.md`.

## 2. Stdlib surface drift

### 2.1 `effective-sngl.md:130-132, 215-217, 258-259, 367`; `specification.md:272, 286` — `option<T>` documented but undeclared (**inconsistent**)

`option<T>` is widely documented (nullable struct/var idiom, coercion table
rows, children annotation, generics list). It is *not* declared in any
`lib/*.sngl` file. The checker handles it (`internal/checker/resolve.go:113`)
as a built-in named type, so the docs aren't wrong functionally — but the lib
surface fails to advertise it. Either declare `struct option<T> {}` in
`lib/types.sngl` alongside `list<T>` / `map<K, V>` / `iter<T>`, or downgrade
the doc examples.

### 2.2 `effective-sngl.md:87, 95-108` — `regex`, `url`, `email`, `uuid`, `date`, `time`, `dateTime` documented but undeclared (**inconsistent**)

Same shape as #2.1: builtins recognised by `internal/checker/resolve.go:75-89`
and `expr.go:641-665` but with no `struct` declaration in `lib/types.sngl`. The
playground / `sngl doc` lookup of these types finds nothing because they have
no doc source. Adding stub declarations with doc comments would close the gap
without touching the checker.

### 2.3 `README.md:26, docs/index.md:39` — `style={flexGrow=1}` references undeclared field (**wrong**)

`lib/types.sngl:172-176` declares only `flex float` on `Style`. There is no
`flexGrow`. The featured README example fails type-check. Either add
`flexGrow`/`flexShrink` to `Style`, or change the snippet to `flex=1`.

### 2.4 `effective-sngl.md:537, 904` — "Modal, drawer, popover have varying support" mentions support that isn't surfaced anywhere else (**inconsistent**)

The capability matrix lives in `docs/targets.go` / website-rendered Platforms
page. `effective-sngl.md` should either drop the per-component-support claim
or link to the matrix. Currently it's a hand-wave without a referent.

### 2.5 `README.md:32-37` — `for item, index = todos { checkbox(...) }` style example uses `key=index` (**ok, but undocumented in lib**)

`lib/components.sngl:122-128` (`checkbox`) does not declare a `key` prop. Keys
are a meta-prop on every visual node — but that meta-prop isn't declared
anywhere user-visible. Add a single paragraph in `effective-sngl.md` (it
mentions it at line 643, but not under "Visual Nodes"). Low impact.

### 2.6 `docs/learn/tour.md:298` says "Methods — `.length`, `.push`, `.remove`, `.map`, `.filter`, `.contains`" omits stdlib methods (**incomplete**)

`lib/functions.sngl` declares additionally: `indexOf`, `join`, `reverse`,
`slice`. None are mentioned in the tour. Either say "and others" (the doc
does, weakly) or list them. Low impact.

### 2.7 `errorBoundary` / `error.raise` not mentioned in tour or effective-sngl (**undocumented**)

`lib/components.sngl:141-143` and `lib/functions.sngl:282` declare a
full error-boundary mechanism. Neither narrative doc covers it. The component
gallery is the only place users will find it.

### 2.8 `Test.setLocale` deprecation noted in lib but not in narrative docs (**outdated**)

`lib/types.sngl:61` marks `Test.setLocale` deprecated in favour of
`t.setContext(locale, …)`. Test docs in `effective-sngl.md:822-895` don't
mention either, so the deprecation is invisible until someone hits it.

## 3. Nav, orphans, broken links

### 3.1 `docs/index.md:72`, `docs/learn/index.md:12`, `docs/docs.go:152` — `learn/getting-started.html` has no markdown source (**orphan-ish**)

`docs/docs.go:60` explicitly skips `learn/getting-started.md` because the page
is rendered by `internal/learn/getting_started.sngl` instead. That is intended
(per memory `feedback_docsite_html_hacks.md` the docs site should target
non-HTML platforms eventually), but the project layout invites confusion:

- `find docs/ -name "getting-started*"` finds zero files.
- The link in `docs/index.md` looks like a normal cross-doc reference.
- `docs.go:60` reads a file that doesn't exist (the `return nil` branch is
  effectively dead code today).

Either remove the dead `if path == "learn/getting-started.md"` branch in
`docs.go`, or add a `learn/getting-started.md` that stubs the page so the
sidebar source is in one place. Currently the route only exists because the
website.sngl `window` block at line 538 hard-codes it.

### 3.2 `docs/reference/index.md:11-14` — links to `/components/`, `/docs/sngl/`, `/platforms/`, `/languages/` (**ok but undocumented**)

These pages exist only via `website.sngl` `window` declarations (lines 217,
240, 398, 464). The `navExtras` in `docs.go:154-157` knows about them. No
markdown sources, no test that the links resolve. Worth a single-line note in
`docs.go:147-159` documenting the dependency.

### 3.3 `effective-sngl.md` and `tour.md` have no frontmatter `nav_order:` field (**inconsistent with memory**)

Memory `feedback_data_driven_nav.md` says nav is frontmatter-driven. Both files
use `order:` only (not `nav_order:` / `nav:`). It's not actually broken — see
`docs.go:108`'s `fm.Order` — but the memory key implies the nav key name is
`nav_order`/`nav`. Either rename the frontmatter key or update the memory.

### 3.4 `docs/superpowers/audit/parser-checker.md:154` — stale comment in code (**reference**, not in this audit's scope)

Mentioned for completeness — `ast/expr.go:119` still has `// FuncType is a function type: func(int, string) -> bool.` per a previous audit. Worth
sweeping at the same time as the `->` cleanup.

## 4. Terminology

### 4.1 `effective-sngl.md:9, 904`; `index.md:9-11`; `README.md:7` — "target" vs "platform" vs "language" (**inconsistent**)

`effective-sngl.md:9` says "native code across multiple platforms — HTML/JS,
Go TUI (Bubbletea), Android (Compose), and desktop (Fyne)" — conflating
platform and language.
`README.md` and `docs/index.md` use a clean "Target / Language / Platform"
table.
The CLI flags are `--lang` and `--platform`. The output declaration in `.sngl`
nests language-as-key over platform-as-call (`js { html }`).

Pick one model and stop using "target" loosely. Suggestion: stick with the
table from `index.md:63-68` and avoid the word "target" for individual
platforms.

### 4.2 `effective-sngl.md:904` — uses "widget" for stdlib components (**inconsistent**)

> Each platform maps stdlib components to native widgets.

Every other doc says "component" or "primitive". Pick one; "widget" is only
correct on the platform side (Fyne / Compose).

### 4.3 `effective-sngl.md` mixes "function", "func", and "method" (**inconsistent**)

"Type methods", "Generic functions", "Lambdas", "Void/action functions" — fine
on their own, but the doc never defines the distinction between "function" and
"method" for `func T.foo() …` declarations. The lib calls them "methods on
the … receiver" (e.g. types.sngl:13). Pick one term.

## 5. website.sngl & portability

### 5.1 `website.sngl:62-99, 209-211, etc.` — heavy raw-HTML in `PlatformCarousel`, playground, tutorial sections (**violates portability invariant**)

Per memory `feedback_docsite_html_hacks.md`, the docs site is meant to target
Android too — no raw-HTML hacks. Today:

- Lines 110-122 (`PageLayout`) have an entire alternative implementation in
  a `platform html` block built out of `html.div(innerHTML="…")` etc. The
  hamburger toggle is literally `html.div(innerHTML="<input type='checkbox' …>")`.
- Lines 127-171 (`PlaygroundContent`) is HTML-only by design; that's fine.
- Lines 174-212 (`TutorialContent`) similarly HTML-only — fine.
- Line 162: `html.script(innerHTML="window.__sngl_wasm_url=\"" + … + "\";")` — string concatenation building JS at compile time.

The carousel itself (lines 52-99) is portable — that's good. The drift is in
`PageLayout`: there are *two* implementations, the default `vbox` body and a
`platform html` override. The override is what gets used today, so the
default is dead. Either delete the default, or accept the default is the
real implementation and trim the override.

### 5.2 `website.sngl:30-50` — `NavSidebar` body uses `if group.title == ""` / `if group.title != ""` rather than `if … else` (**outdated style**)

The "no else" workaround is from a time the parser didn't accept `else` on
`if`. It does now (see #1.7 / `specification.md:45`). Simplify to:

```sngl
for group = navTree {
    if group.title == "" {
        for item = group.items { docui.NavItem(...) }
    } else {
        docui.NavGroup(...) { ... }
    }
}
```

### 5.3 `TODO.md:1` — "html codegen scope quirks → search index lives at website.sngl top-level" (**design debt, not a docs bug**)

Recorded for context. The `searchEntries` const at `website.sngl:23` is the
top-level workaround. Once cross-package const folding works, move it into
`docui`.

## 6. README / docs drift

### 6.1 `README.md:42-47` vs `docs/index.md:14-20` — different feature lists (**inconsistent**)

README says "Declarative / Reactive / Cross-platform / Type-safe / Minimal
runtime"; index.md says "Declarative UI / Reactive by default / Cross-platform
/ First-class expressions / Compile-time analysis / Minimal runtime". Pick a
canonical bullet list and reuse it.

### 6.2 `README.md:51-57` and `docs/index.md:63-68` — identical platform table, but neither lists `gtk4` (**outdated**)

`codegen/platform/gtk4/` exists. Either the table needs a row (Status:
experimental?) or the gtk4 directory needs to be marked WIP somewhere
user-visible. Today gtk4 is a stealth platform.

### 6.3 `README.md:13-39` example uses `:value=newTodo` (two-way bind shorthand) while `docs/index.md:39` uses `@input { newTodo = event.value }` (**inconsistent**)

Same example, different idiom. Tour favours `:value=`. Make README and
index.md match (preferably both using the `:value=` shorthand since that's
the language's headline feature).

### 6.4 `README.md:60` — "Requires Go 1.26+" matches `learn/installation.md:9` but no `go.mod` cross-check done here (**cross-check requested**)

If `go.mod` declares `go 1.26.0` it's fine. Otherwise drift.

## 7. Other

### 7.1 `specification.md:284, 286` — built-in / generic type lists are out of sync with lib (**incomplete**)

> Built-in types: `int`, `float`, `bool`, `string`, `color`, `date`, `measurement`, `dyn`
> Generic types: `list<T>`, `option<T>`

Missing: `time`, `dateTime`, `duration`, `url`, `email`, `uuid`, `regex` (all
recognised by the checker; see #2.2). Generics missing: `map<K, V>`, `iter<T>`
(both declared in `lib/types.sngl:316-330`, both have method syntax in
`lib/functions.sngl:258-272`).

### 7.2 `specification.md:54-56` — `ImportDecl` grammar shows `[ "=>" STRING ]` (**verify**)

```ebnf
ImportDecl = "import" [ IDENT ] STRING [ "=>" STRING ]
```

The `=>` suffix is unique to imports and isn't explained anywhere in the
`Imports` section. If this is the rename-target syntax (`import "go://…" => "foo"`),
spell it out next to the grammar. Otherwise it looks like a copy-paste
mistake.

### 7.3 `specification.md:284` lists `dyn` but no narrative coverage (**incomplete**)

`dyn` is the host-interop escape hatch (per `effective-sngl.md:85`). The
spec mentions it once and doesn't define it. Add one paragraph.

### 7.4 `effective-sngl.md:55-56` — "top-level declarations available are: `import`, `output`, `struct`, `enum`, `unit`, `style`, `const`, `var`, `func`, components (PascalCase names), `timer`, and `test`." (**outdated**)

- `style` is no longer a keyword (parser ebnf:266).
- `timer` is not a top-level declaration — it's a visual node inside
  components.
- `test` was retired in favour of test functions (`func testFoo(t Test, c X)`,
  see `lib/types.sngl:42-74`).
- Missing: `context` (declared at top-level per `lib/i18n.sngl:23` and
  `tour.md:658`), `window`.

### 7.5 `effective-sngl.md:619` — "if active = true" reads as assignment but is comparison (**typo**, cosmetic)

The surrounding sentence is fine; just flagging the visual confusion. Low
impact.

### 7.6 `effective-sngl.md:825-845, 879-890` — `test` blocks (`/-test app …`) syntax (**outdated**)

```
Prefix with `/-` to comment out a test block: `/-test app "skipped" { assert(false) }`.
A test block can omit the description string: `test app { assert(count == 0) }`.
```

Tests are now functions (`func testFoo(t Test, c X)`); there's no `test app "…" { … }` form anymore — confirmed by `lib/types.sngl:43` and every test
fixture. Whole sub-section needs rewrite for the new function-based API.

### 7.7 `effective-sngl.md:887` — `t.test("inner")` API (**unverified**)

```sngl
func testNesting(t Test, c app) {
    c.x = 1
    t.test("inner")
    t.assert(c.x == 1)
}
```

No `Test.test` method is declared in `lib/types.sngl`. Either the doc invents
the API or the method exists undocumented. Check stdlib testrunner support.

### 7.8 `effective-sngl.md:873` — `tick()` (no receiver) (**wrong**)

Should be `t.tick()` based on the `Test.*` receiver convention; bare `tick()`
isn't a stdlib function (`lib/functions.sngl` has no `tick`). Confirm with
testrunner before fixing — possibly the method exists but isn't declared in
lib (compare #2.7).

### 7.9 `tour.md:11` and `effective-sngl.md` — no code-block language tag inconsistency (**ok**)

All `.md` files in `docs/` consistently use `sngl`, `bash`, or ````ebnf`. No untagged or `snippet` blocks observed.

### 7.10 `TODO.md:3` — "revisit highlighted code in the tutorial once cross-package const folding works" (**still valid**)

Recorded. Not stale — matches the open design in `website.sngl:23`.

### 7.11 `docs/reference/effective-sngl.md:9` — "Bubbletea" capitalisation drift (**cosmetic**)

`bubbletea` (lower) in CLI flags and most docs; `Bubbletea` here; `BubbleTea`
in `README.md:54` and `tour.md:96`. Pick one.

### 7.12 `tour.md:493-499, 538-590` — color literals as strings (`color = "#2196f3"`) inside `platform html` blocks (**ok, but document**)

Inside a `platform html { html.* … }` block, props are typed by the raw HTML
element which takes `string` for `style.color`. Outside, `color = #2196f3`
(hex literal) is the SNGL form. This is correct but the change in syntax
mid-tour is jarring; one sentence explaining why would help.

### 7.13 `effective-sngl.md:444` — generic `func list.push<T>(...)` example contains `{ ... }` as the body (**cosmetic**)

The empty-ish body marker `{ ... }` is meant to be elided implementation, but
SNGL doesn't have an "elision" token. New readers may copy/paste. Use
`/* ... */` comments or call it out explicitly.

### 7.14 `lib/types.sngl:60-67` — `Test.setLocale` and `Test.setContext` coexist (**design debt**)

Not strictly a docs issue, but `setLocale` is deprecated in favour of
`setContext(locale, …)`. If the locale context is now declared
(`lib/i18n.sngl:23`), `setLocale` should be removable — the deprecation has
been outstanding long enough that anything still using it is stale.

---

## Quick-fix priorities

1. **`effective-sngl.md` unit literals** (#1.1) — copy-paste poison, trivially fixable.
2. **`effective-sngl.md` list method form + `style { … }` + `class` + "no else"** (#1.2, 1.3, 1.7, 1.8) — large swathes of the doc are wrong; users following them will hit errors.
3. **`tour.md` `--target` flag** (#1.9) — first CLI line a reader sees.
4. **`README.md` / `index.md` `flexGrow`** (#2.3) — featured example doesn't compile against current `Style`.
5. **Spec built-in / generic type lists** (#7.1) and **option/regex/etc. lib decls** (#2.1, 2.2) — get the stdlib surface honest.
6. **Test-section rewrite** (#7.6, 7.7, 7.8) — the entire testing API in `effective-sngl.md` predates the function-based runner.
7. **`PageLayout` dual-implementation in `website.sngl`** (#5.1) — pick one path, delete the other.
8. **GTK4 visibility** (#6.2) — either land it on the platform table or quarantine it.
