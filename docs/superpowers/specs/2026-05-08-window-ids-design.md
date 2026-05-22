# Window identifiers + path-derived output

**Status:** Shipped

## Problem

`website.sngl` declares many windows inside `for` loops:

```sngl
for comp = components {
    window(title=comp.name, href="/docs/sngl/components/" + comp.name + ".html") { ... }
}
```

After the optimizer unrolls the for-loop, every cloned `ir.Window` keeps the
same `Name` value — `"window_L336"` — that the checker assigned from the AST
line at `internal/checker/checker.go:1383`. The html platform writes one
output file per `ir.Window`, using `Name + ".html"` as the path, so 36
component windows all collide into `_site/window_L336.html`. The component
pages currently visible under `_site/docs/sngl/components/*.html` are stale
artifacts from a prior build, not output from this one.

Cross-window links are also brittle: `html.a(href="/docs/sngl/components/" + comp.name + ".html")` is hand-typed string concat. Nothing ties the link to a
real window declaration; renaming a window's path silently breaks every link
to it.

## Goals

1. **Output path is the href.** A window's emitted file path is derived from
   its folded literal `href`. No synthetic `window_L<line>` filenames.
2. **Windows have stable identifiers.** A window can carry a `#id` that
   resolves as a value of type `Window` with a `.href` field, so links can be
   written as `home.href` instead of repeating the path string.
3. **Renaming-safe links.** Changing a window's `href` updates every
   reference in one place.

## Non-goals

- Server-mode (`--lang go`) routing — already uses `Href` directly via
  `routes.go`. Untouched here.
- Implicit links / typed `link(to=foo)` API. We only add `pageName.href` as a
  constant string expression. The existing `link` / `html.a` components keep
  taking string `href`s.
- Detecting links broken by typo (e.g. literal hrefs that don't match any
  window). Deferred — the `#id`/`.href` path solves it for code that adopts
  it, and we don't need a global checker pass yet.

## Design

### Syntax

The existing visual-node `#id` syntax (`internal/parser/build.go:920-926`)
already lifts `#id` from `Target` into `VisualNode.ID`. No grammar change.

```sngl
window #home(title="Dashboard", href="/") { ... }

for comp = components {
    window #componentPage(title=comp.name,
                          href="/docs/sngl/components/" + comp.name + ".html") {
        ...
    }
}

link(href=home.href, text="Home")

for cp = componentPage {
    link(href=cp.href, text=cp.title)
}
```

### `Window` type

A new builtin type `Window` with fields:

- `href: string`
- `title: string`

Anonymous windows (no `#id`) bind no symbol.

### Scoping

Given `window #pageName(...) { ... }`:

- **Outside any enclosing for-loop:** `pageName` is a scalar `Window`.
- **Inside a for-loop containing the decl:** `pageName` is a scalar `Window`
  (the current iteration's window).
- **Outside the for-loop:** `pageName` is `list<Window>`, one element per
  iteration.

If a window with `#id` lives inside multiple nested for-loops, the outermost
non-enclosing scope sees `list<list<Window>>` etc. (Mirrors how list
comprehensions would degrade — keeps the rule mechanical.)

### IR

Repurpose `ir.Window.Name` to mean the user `#id`. Empty when anonymous. The
checker no longer synthesizes `window_L<line>`. The optimizer's fold-time
"derive Name from folded href" branch (`internal/optimize/fold.go:165-177`) is
removed.

### Output filename (html platform)

In `codegen/platform/html/html.go:290-326` the per-window loop computes a
filename from `win.Name`. Replace with a derivation from the folded literal
`Href`:

| folded `href`   | output path      |
|-----------------|------------------|
| `/` or empty    | `index.html`     |
| `/index.html`   | `index.html`     |
| `/foo/bar.html` | `foo/bar.html`   |
| `/foo/`         | `foo/index.html` |
| `/foo`          | `foo/index.html` |

Static mode already errors at `html.go:106` when a window's href doesn't fold
to a literal, so by the time we're choosing filenames every href is a literal
string. Single-window packages with no `href` continue to emit `index.html`
(this branch is at `html.go:287`, separate from the loop, and stays).

### Collision detection

After expansion, two windows that resolve to the same output path are a
checker error:

```
window collision: "/foo/bar.html" emitted by both window at L42:5 and L88:3
```

Two top-level windows with the same `#id` are a checker error. Two windows
with the same `#id` inside the same for-loop body don't conflict (each
iteration is a distinct binding); but two for-loops at the same scope using
the same `#id` do conflict.

### Existing usage to update

- `examples/http-session/app.sngl:12,31` — `window home(...)` /
  `window about(...)` parses today as two adjacent visual nodes (`window`
  with no args, then `home(...)` separately). It "works" because the bare
  `window` node ends up nameless and the `home`/`about` nodes get the
  args. Change to `window #home(...)` / `window #about(...)`.
- `website.sngl` — opportunistic follow-up. Replace string-concat hrefs with
  `name.href` references where it tightens the dependency. Out of scope for
  the core feature, in scope for this branch since we're touching every
  window decl in the file anyway.

## Implementation phases

1. **Parser/AST.** Confirm `#id` on `window` populates `VisualNode.ID`. Add
   tests in `testdata/` if missing.
2. **IR + checker.** Carry `VisualNode.ID` to `ir.Window.Name`. Drop the
   `window_L<line>` fallback. Register a `Window`-typed symbol bound to the
   declaration when `#id` is set.
3. **Builtin type.** Add `Window` to the type system with `href` / `title`
   string fields. Field access on a `Window` ident lowers to the underlying
   expression (so `home.href` constant-folds to the literal href string).
4. **Optimizer.** During `expandForStmt` (`internal/optimize/expand.go:43`),
   when a `#id` window is inside the loop, accumulate per-iteration values
   under that symbol so the outside-the-loop view sees a `list<Window>`.
   Inside-the-loop refs already resolve to the current iteration via
   `evalCtx.values`.
5. **html codegen.** Replace `name := win.Name; ... name + ".html"` in
   `codegen/platform/html/html.go:290-326` with `pathFromHref(folded)`. Add
   collision check.
6. **Cleanup.** Remove dead `if n.Name == ""` href-to-name fold in
   `internal/optimize/fold.go:165-177`.
7. **Migrate examples.** `examples/http-session/app.sngl` → `#home` /
   `#about`. `website.sngl` → adopt `.href` refs where they reduce string
   duplication.
8. **Verify.** `go tool docsgen`; confirm no `_site/window_L*.html`; spot
   check `_site/docs/sngl/components/avatar.html` is rebuilt fresh; check
   internal links still resolve in the browser.

## Test plan

- New `testdata/` fixtures:
  - `window_id_basic.sngl` — top-level `window #home`, ref to `home.href`
    folds to literal.
  - `window_id_for_loop.sngl` — for-loop window with `#id`, outside-loop ref
    yields `list<Window>` and iterates correctly.
  - `error_window_id_collision.sngl` — two top-level `#home` windows →
    `// ERROR(check) "duplicate window id"`.
  - `error_window_path_collision.sngl` — two windows folding to the same
    path → `// ERROR(check) "window output collision"`.
- Existing html integration tests should still pass; per-window output paths
  in `_site` will change from `window_L<line>.html` to real paths.
