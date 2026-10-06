# After the window collapse — leftovers

Scratch. Delete this file once the list below is empty.

Sections 1–4 (scope ordinality, the window primitive, the owner predicate,
capabilities in source and `sngl:x/gen`) have landed: `ui.window` is a bodyless
component every target overrides, `IsWindowNode`, `BuiltinWindow`,
`CrossesTreeFamily`, `collectForLoopWindowIDsStmt`, `c.windowType`,
`Capabilities()`, `Caps`, `ToLowerCaps` and `AllFeatures` are gone. A plugin as
configuration rather than compiled-in code is #253.

## Open questions

- **What the second per-primitive capability is.** `#[gen.renders(identity)]`
  has one user; find a second before fixing the shape of the record.
- **Whether a counted handle (`option<T>`, `list<T>`) ever becomes readable.**
  Every read of one is refused today. The capability that would answer it is
  readable identity asked of a window rather than a shape.

## Carried forward, still unfixed

- `inlinable()` (`internal/lower/inline_components.go`) and
  `internal/lower/inline_pure.go` test
  `len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0` where
  they mean `comp.Bodyless`.
- An undefined name in a shape prop leaks an unrelated library diagnostic:
  `draw.circle(cx=1, cy=2, r=nope)` reports
  `x/scheme/c/c.sngl:57:31: operator > not defined for int and float`.
- The package body has no `LocalRefs` (only `Component.LocalRefs`).
- Unverified: a route-mode func that mutates state carries DOM patches;
  `internal/optimize/shake.go` wants `ir.Walk`.

## Rules that bite

- **This file is a build gate.** `go tool verify` runs `mdox fmt --check` over
  every markdown file, so run `go tool mdox fmt --soft-wraps PLAN.md` before
  committing.
- **The binary proxy resolves `./cmd/sngl` against cwd.** Build out of tree and
  *run* out of tree, or a "does this fail when reverted?" check compares HEAD
  with itself.
- **`go test ./internal/checker/` does not evaluate a new `// ERROR(check)`
  fixture.** The root package's `TestFixtures` does.
