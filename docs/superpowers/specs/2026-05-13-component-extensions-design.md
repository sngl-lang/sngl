# Component Extensions (Issue 63)

## Background

SNGL stdlib declares abstract components in `lib/components.sngl` (e.g.
`text`, `button`, `vbox`). Each platform ships a `.sngl` file that today
contains either:

- `component sngl.X() { body }` (html, bubbletea) — declarations whose
  intent is "override stdlib `X` for this platform." The `sngl.` prefix
  is parsed verbatim into `*ir.Component.Name = "sngl.X"`. The checker
  doesn't merge this into the stdlib component, so bare user calls like
  `text(value="hi")` still bind to the empty stdlib stub.
- `component X() { body }` (gtk4, fyne) — separately-named components
  that don't even pretend to be overrides. The platform's translator
  maps stdlib tag → native via a Go-side table (`gtk4TagToCType`, fyne
  blueprint lookup).

Both forms duplicate knowledge the `.sngl` file already declares, and
neither wires the platform body into the stdlib component the user
actually invokes. Plan G's `passInlinePure` can only inline a component
whose body is present at the user-call binding site, so without
extensions, Plan G can't reach gtk4/fyne (and even html's "extension"
is only window-dressing).

This spec defines the real mechanism behind `component sngl.X { ... }`.

## Goals

1. New declaration syntax: `component sngl.X { platform <name> { body } }`.
   Props and children-type are omitted (inherited from stdlib).
2. Checker merges platform extensions into the stdlib `*ir.Component`:
   for the active build's platform, the matching `platform <name>` body
   becomes the stdlib component's body. Bare user `X(...)` calls then
   resolve to a body-bearing component, and `passInlinePure` can inline.
3. Validation:
   - `sngl.X` extensions: empty PropList, empty ChildrenType, body must
     be one or more `platform <name> { ... }` blocks (no naked stmts).
   - User-component children-count enforcement at call sites:
     - bare `T` ChildrenType: exactly one child required
     - `option<T>`: zero or one
     - `list<T>`: any
     - no ChildrenType declared: no children allowed
4. Rewrite all four platform `.sngl` files to use the extension form.
5. After (4), Plan G Task 10-12 resumes: delete `gtk4TagToCType`,
   `gtk4Constructor`, `htmlTagToDOM` switches.

## Non-goals

- New "platform" keyword behavior outside component extensions —
  `PlatformStmt` already exists in the AST and ignores non-matching
  platforms at lower-time via `passPlatform`. Component-body usage gets
  the same treatment.
- Allowing extension components to add new props/events/children types.
  Reject at the checker; tracked separately if a use case emerges.
- Multi-active-platform builds at checker level. The checker is
  platform-agnostic: it walks **all** registered platforms'
  `Package()` docs and collects `sngl.X` extension bodies onto each
  stdlib component as `map[platformName]→body`. Duplicate platform
  entries for the same stdlib X across two different platform packages
  is an error. The lowering pass (which already operates per-platform)
  picks the active platform's body from the map and swaps it into the
  component before the rest of lowering runs.
- Cross-package extensions: only `sngl.X` (stdlib namespace) extensions
  are supported. `othernamespace.X` is rejected.

## Design

### 1. Parser

No grammar change required. `component sngl.X { platform html { ... } }`
is already parsed:

- `buildComponentDecl` in `internal/parser/build.go` joins
  `name + "." + name` into the qualified component name when a DOT
  follows the bare ident.
- `PlatformStmt` is an existing stmt type that can appear inside any
  `StmtBlock`, including a component body.

What's missing is *validation* (props/children must be omitted; body
must consist of platform blocks). That lives in the checker (Section 3).

### 2. Extension collection + lowering swap

Split into two phases — checker collects, lower swaps.

#### Checker-side collection

Add to `*ir.Component`:

```go
// PlatformBodies maps platformName → checked body stmts for sngl.X
// extensions. Populated by mergePlatformExtensions; consumed by the
// lowering pass `passPlatformExtensionBody` which swaps the active
// platform's body into Component.Body at lower start.
PlatformBodies map[string][]ir.Stmt
```

In `mergePlatformExtensions`, walk **every** registered platform:

1. Parse `p.Package()` documents.
2. For every top-level `*ast.ComponentDecl` whose Name has the form
   `sngl.X` and `HasParens == false` (new-form only):
   - Look up `c.symtab.Comps["X"]`. Must exist; otherwise checker error
     `extension "sngl.X" references unknown stdlib component`.
   - Walk `decl.Body.Stmts` for `*ast.PlatformStmt` entries:
     - For each `platStmt`:
       - If `stdComp.PlatformBodies[platStmt.Platform]` already set,
         error: `component sngl.X has duplicate platform block for %q`.
       - Otherwise temporarily set `stdComp.AST.Body = platStmt.Body`,
         run `checkComponentBody(stdComp)`, capture the resulting IR
         body into `stdComp.PlatformBodies[platStmt.Platform]`.
   - Validation of "body must contain only platform blocks" already
     happens in `registerComponent` per Section 3; merge can assume
     well-formed.

The collection step is idempotent and platform-agnostic. Checker
output (the `*ir.Package`) carries `PlatformBodies` for every stdlib
component that has at least one extension.

#### Lower-side swap

Add a new pass `passPlatformExtensionBody`, registered first in the
lowering order (before any pass that walks component bodies). The pass
reads `Options.Platform` (a new field — the active platform name) and
for each `*ir.Component` with a non-nil `PlatformBodies`, sets
`comp.Body = comp.PlatformBodies[opts.Platform]`. If no entry exists
for the active platform, leaves `comp.Body` empty — the original
behavior for "platform doesn't implement this stdlib component."

`Options.Platform` is set by the build driver from
`PlatformGenerator.PlatformIdentifier()`. CLI tools (LSP, format,
multi-platform discovery) that don't have a specific active platform
either:
- pass `Options.Platform == ""` (pass becomes a no-op, components keep
  empty bodies — same as pre-Plan-H behavior), or
- run Lower per-platform with the relevant identifier each time.

This keeps the checker call platform-agnostic and avoids any
multi-platform-registration error.

#### Resolution scope for platform bodies

The platform body references the platform's native tags (e.g.,
`span`, `GtkLabel`, `Container`). These must resolve. The platform
already exposes them via `Namespace.Resolve` (e.g., `html.span`). For
unqualified access inside the extension body, the merge wraps the
body's resolution in a scope whose parent's `Resolve` is the
platform's. Cleanest implementation: synthesize an implicit
`import <platform-path>` at the head of the platform package's parse
output (the existing platform package layer already does this — verify
during implementation).

#### Effect on `sngl.X` reference

A user writing `sngl.text(...)` resolves through namespace `sngl` to
the same `*ir.Component`. After extension merge, the body is the
platform's; `sngl.text` and bare `text` now point at the same body. The
existing test at `internal/checker/checker_test.go:995` ("sngl.text
resolves to stdlib text even when user shadows it") still holds: both
forms still resolve to the same Component object. The body of that
object is platform-dependent.

### 3. Checker validation

In `registerComponent`, when `comp.Name` contains a dot:

- Split into `namespace.local`. Today only `sngl.X` is valid.
- Reject any non-`sngl` namespace: `extension namespace %q not
  supported` at `comp.Pos`.
- Require `len(comp.Props.Props) == 0`: error
  `component extension "sngl.%s" may not declare props (inherited from
  stdlib)` at the first prop's position.
- Require `comp.ChildrenType == nil`: error
  `component extension "sngl.%s" may not declare children type
  (inherited from stdlib)` at the children-type position.
- Require `comp.Body.Stmts` is non-empty and consists entirely of
  `*ast.PlatformStmt` (plus comments): error
  `component extension "sngl.%s" body must contain only platform
  blocks` at the first offending stmt.

These validations apply uniformly to platform-supplied and
user-supplied extension declarations. User-supplied extensions are
allowed but they only override at the user-package level (no merge
into stdlib — extension merge is platform-only).

#### Children-count enforcement at call sites

In `checkNodeInst` (the call-site checker for `*ast.VisualNode`):

- Let `n` = number of children stmts in `node.Body` after lowering
  out comments and control-flow (count VisualNode + ControlFlow as 1
  each; PlatformStmt contributes its body length).
- Let `ct` = resolved component's `ChildrenType`:
  - `nil`: require `n == 0`. Error: `component %q does not accept
    children`.
  - kind `TypeList`: any `n` allowed.
  - kind `TypeOption`: require `n <= 1`. Error: `component %q accepts
    at most one child (got %d)`.
  - other: require `n == 1`. Error: `component %q requires exactly one
    child (got %d)`.

Children-count enforcement is the second half of issue 63; it lands
in the same change.

### 4. Platform package rewrites

#### html

`codegen/platform/html/html.sngl` already uses `component sngl.X()`
declarations. Convert each to the new form:

```diff
-component sngl.text() {
-    span(textContent=value) {}
-}
+component sngl.text {
+    platform html {
+        span(textContent=value) {}
+    }
+}
```

Drop the parens (no props), drop ChildrenType, wrap body in `platform
html { ... }`.

#### bubbletea

Same shape as html. Identical conversion.

#### gtk4

`codegen/platform/gtk4/gtk4.sngl` currently declares bare
`component button() { GtkButton(...) }`. Rename + wrap:

```diff
-component button() {
-    GtkButton(label=text, @clicked { @click() }) {}
-}
+component sngl.button {
+    platform gtk4 {
+        GtkButton(label=text, @clicked { @click() }) {}
+    }
+}
```

#### fyne

Same as gtk4. Rename `component label`, `component button`, etc. →
`component sngl.X` with `platform fyne { ... }` wrapper.

### 5. Plan G Task 10-12 resumption

After (4), with `Caps.NoStdlibWrappers` enabled on each platform:

- `passInlinePure` sees bare `button(...)` resolves to stdlib `button`
  whose body is now `GtkButton(...)` (or whichever platform). Pure (no
  Vars/Funcs/Timers). Inlines.
- Platform translators (`gtk4Translator.OnCreateNode`,
  `htmlTranslator.OnCreateNode`, fyne) only see native tags
  (`GtkButton`, `span`, `Label`). Delete the SNGL-stdlib-tag switches:
  - gtk4: delete `gtk4TagToCType` + `gtk4Constructor`.
  - html: delete `htmlTagToDOM`.
  - fyne: blueprint table stays (it's keyed by native tag, not stdlib
    tag, after Section 4). Audit during implementation.

## Failure modes & errors

- `extension "sngl.X" references unknown stdlib component` — typo or
  stdlib gap.
- `extension namespace "X" not supported` — only `sngl.` is valid.
- `component extension "sngl.X" may not declare props/children type` —
  enforced symmetry with stdlib.
- `component extension "sngl.X" body must contain only platform
  blocks` — naked stmts rejected.
- `component "X" has no platform implementation for "<p>"` —
  lower-time error if user invokes a stdlib component with no
  platform body merged in. Currently this case falls back silently to
  the empty stdlib body; the new error is more useful.
- `component "X" requires exactly one child (got %d)` /
  `does not accept children` / `accepts at most one child` —
  children-count enforcement.

## Test fixtures

In `internal/checker/testdata/`:

- `extension_basic.sngl` — `component sngl.text { platform html {
  span(textContent=value){} } }` then user `text(value="hi")`.
- `extension_rejects_props.sngl` — extension with `()` props →
  ERROR(check).
- `extension_rejects_children_type.sngl` — extension with
  ChildrenType → ERROR(check).
- `extension_rejects_naked_body.sngl` — extension body without
  `platform` wrapper → ERROR(check).
- `extension_rejects_non_sngl.sngl` — `component foo.bar { ... }` →
  ERROR(check).
- `children_count_required.sngl`, `children_count_optional.sngl`,
  `children_count_list.sngl`, `children_count_none.sngl` — each fixture
  exercises one ChildrenType variant.

In `internal/lower/testdata/`:

- `inline_strict_extension.txtar` — uses a teststub platform that
  declares `component sngl.text { platform teststub { span(...) } }`,
  caps: `NoStdlibWrappers`, asserts inlining occurs.

## Sequencing

1. Implement checker validation (Section 3 minus children-count).
   Land with fixtures that error correctly. Existing platform .sngl
   files will start failing because they use the old form — gate
   validation on extension being in body-only form, or rewrite the
   .sngl files in the same commit.
2. Implement extension merge (Section 2). Existing platform .sngl
   files don't have `platform` blocks so they continue to be ignored.
3. Rewrite html.sngl + bubbletea.sngl to new form. Verify html tests.
4. Rewrite gtk4.sngl. Enable Plan G Task 10 (delete gtk4 tables).
5. Rewrite fyne.sngl. Audit blueprint table (should be keyed by
   native tag).
6. Implement children-count enforcement + fixtures.
7. Resume Plan G Task 11-12 (html htmlTagToDOM delete) + Task 13-14
   (verify + handoff).

## Risks

- **PlatformStmt vs component-body interaction**: the existing
  `passPlatform` lowering pass strips non-matching `platform` blocks
  from component bodies. Our merge happens at *check* time and writes
  the matching body in directly, so the stdlib component's body is
  already specialized. The `passPlatform` pass becomes a no-op for
  merged extensions but should still handle user-level platform blocks
  (e.g. inside windows) — verify in implementation.

- **User-extension semantics**: a user writing `component sngl.text
  { ... }` in their own code shadows the platform's. Spec leaves this
  permissive: it's a legitimate way to override even the platform's
  behavior. The merge-into-stdlib-component approach makes this
  natural: user extensions just overwrite the same Component's body.

- **Backward compatibility**: `component sngl.X()` (with parens) in
  html.sngl/bubbletea.sngl currently parses fine and is checker-ignored.
  Once validation lands, those decls error until rewritten. Land the
  rewrites in the same commit as the validation.

## Next steps

After this spec lands:

1. `writing-plans` produces an implementation plan.
2. Implementation plan lands.
3. Plan G resumes (Tasks 10-14).
