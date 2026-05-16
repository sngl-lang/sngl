# Showcase as Language Tour — Design

**Date:** 2026-05-15
**Status:** Shipped (commit c4f2a9b)
**Target file:** `examples/showcase/app.sngl` (replace in place)

## Goal

Convert `examples/showcase/app.sngl` from a component-only gallery into a
"Learn SNGL in Y minutes" annotated source file. The file teaches every
language feature top-to-bottom via comments interleaved with valid, compiling
declarations, then ends with a `component main` that ties everything together
and doubles as the visual showcase.

Secondary goal: the file is a portable test case for new platforms. It must
build and run under every target the existing showcase supports:

```
output {
    none { html }
    go { bubbletea fyne }
    kotlin { android }
}
```

## Style

Modeled after `learnxinyminutes.com/docs/<lang>`:

- Banner comment at top introducing SNGL.
- File reads top-to-bottom. Each feature gets a `// === Section ===` header,
  a short prose comment, and a minimal compiling demonstration.
- Code is real, not pseudo. The whole file passes `sngl check` and produces
  working output on every platform listed in the `output` block.
- No graphical "sections" — narrative lives in comments, not in headings
  rendered as `text(value="...")`.

## Section outline

The file is laid out in this order. Each section is a few comment lines plus
the minimum declarations to demonstrate the feature.

1. **Banner** — what SNGL is, how to read the file, link to docs.
2. **`output` block** — platforms/languages the file targets.
3. **Constants & types** — `const`, grouped `const ( ... )`, primitive types
   (`int`, `float`, `string`, `bool`, `date`), type coercion notes.
4. **Operators & expressions** — arithmetic, comparison, logical, ternary,
   string interpolation `"{x}"`, compound assignment, `++`/`--`/`!!` sugar.
5. **Units** — `unit duration { ms, s = 1000ms, ... }`, unit arithmetic.
6. **Structs** — named struct, anonymous struct in field position, defaults.
7. **Enums** — named enum, inline enum in type position.
8. **Options / null** — `option<T>`, `null`, narrowing.
9. **Lists** — literal, methods (`length`, `filter`, `map`, `contains`).
10. **Maps** — `{k = v}` literal disambiguated by expected type, methods.
11. **Iter & for-loops** — `for x = xs`, `for k, v = m`, `for _ = xs`,
    list→iter implicit conversion, for-index, for-else.
12. **If / else** — boolean condition, no truthiness; nested if as expression
    in interpolation.
13. **Functions** — block-body `func f(x int) int { ... }`, expr-body
    `func g(x int) => x * 2`, no return type when no value.
14. **Lambdas & func values** — `xs.map(x => x * 2)`, func-type parameters.
15. **Generic functions** — `list<T>.map<U>(...)`, type-param inference from
    operand and lambda return.
16. **Recursion** — direct + mutual.
17. **i18n** — `$"..."` strings, `{n, plural, ...}`, `{x, select, ...}`,
    `{p, number, currency}`, `{d, date, long}`, manifest with `en` + `es`.
18. **Components: declaration** — `component name { var ... visual ... }`.
19. **Component state & computed** — `var` reactive state, computed funcs.
20. **Component events** — `@click { ... }`, `@input(e) { e.value }`,
    handler bodies as statement blocks.
21. **Timers** — `timer(interval=50ms, enabled=..., @tick { ... })`.
22. **Component composition** — child components from a `widgets` import or
    local declaration, props + events flowing in/out.
23. **Component recursion** — a tree-renderer component that calls itself.
24. **Visual nodes & styling** — vbox/hbox/spacer/divider/text/button with
    `style={...}`, brief reference (one or two examples per category).
25. **`component main`** — the demo. Mirrors the current showcase's visual
    grid (every stdlib component used at least once) and reads from the
    structures/funcs/i18n declared above. Comments label which earlier
    section each subsection exercises.
26. **Tests (kept minimal)** — 1–2 trivial `func testX(t Test, c main)`
    bodies with a comment explaining that the file doubles as a platform
    conformance fixture, and that any TestRunner-bearing platform can run
    them via `go tool verify`.

## i18n manifest

New file `examples/showcase/i18n.manifest.json` alongside `app.sngl`.

- Locales: `en` (manifest fallback / source) and `es`.
- Covers each ICU feature used in section 17: plain, plural, select,
  number-as-currency, date-long.
- Mirrors the structure of `examples/hello-i18n/i18n.manifest.json`.

## Component composition

To showcase composition without inventing a widgets directory, the tour
declares one small child component inline (e.g. `component Counter` with a
prop, an event, and internal state) and uses it inside `component main`.
This keeps the example single-file other than the manifest.

## What we deliberately skip

- HTTP routes / multi-window / `http-session` patterns — html-only, breaks
  portability across bubbletea/fyne/android.
- `go://` imported functions — language-specific, not portable.
- Error fixtures (`// ERROR(check) "..."`) — those belong in `testdata/`.
- Per-feature test funcs — user chose demo-only with two token tests.

## File ordering rationale

Declarations before use. Components last (they reference everything above).
This is the only order that keeps the file readable as a linear tutorial
**and** valid under SNGL's top-level decl model.

## Acceptance

- `go install ./cmd/sngl && sngl check examples/showcase/app.sngl` clean.
- `sngl dump optimized --lang js --platform html examples/showcase/app.sngl`
  succeeds.
- `sngl dump optimized --lang go --platform bubbletea examples/showcase/app.sngl`
  succeeds.
- `sngl dump optimized --lang go --platform fyne examples/showcase/app.sngl`
  succeeds.
- `sngl dump optimized --lang kotlin --platform android examples/showcase/app.sngl`
  succeeds.
- `go tool verify` passes (project-wide).
- The two retained test functions execute against the `none` interpreter.
