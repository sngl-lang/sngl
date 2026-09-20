# markup + markdown importer — remaining work

Scratch plan for `feat/markup-tree`. **Delete this file before merging.**

The end state: `internal/learn/getting_started.sngl` is a markdown document,
imported as SNGL, rendering on every target — and `docui.Markdown`'s
`html.div(innerHTML=src)` escape hatch is gone with it.

Done so far: `lib/ui/markup` declares the `span` family, `richText`, and nine
inline components. Every one is a signature, so nothing can be built until a
target implements it. `testdata/error_markup_span_membership.sngl` pins the
three membership refusals.

---

## 1. Lowering — not needed, and that was the finding

There is no `passMarkup` and there should not be one. The span family is an
ordinary component tree: a platform implements each member the way `html.sngl`
implements every other family it serves, and the nesting an author wrote is
the nesting the host renders. Flattening to a run list in `ir` was tried and
reverted -- a second representation of a tree is a class of bug this repository
has spent enough time chasing, and `ir.NodeInst.CanvasDraw` is the same
mistake one family over rather than a pattern to copy.

What the experiment did establish, and what is now in main:

- A `lib/` component's **body was never checked and never inlined**, so every
  bodied library component rendered *nothing*, on every target. `md.paragraph`,
  `md.list` and the six headings were dead source. Two commits: the loader
  checks component bodies on every tier, and the inliner splices a stdlib
  component that carries a body. `testdata/lib_component_body_renders.txtar`
  pins it, bullet and all.
- Hosting a family marked a component as "rendered by a codegen", so a
  platform could not implement one in its own package: the override was
  written, never substituted, and the emitter met a node it had never heard
  of. Now only the drawing tree keeps its host standing, which is passCanvas's
  own requirement and is noted as such.
- The cascade is resolved by the host. On html that is CSS, which inherits
  exactly the five typography fields a `SpanStyle` carries. A host that does
  not inherit (a segment list, a Pango string) resolves it in its own emitter;
  a shared helper under `codegen/` is the place for that if two of them want
  the same code, not `ir`.

Three bugs fell out along the way and are fixed: a `continue` or `break` was
deleted outright by the context lowering whenever anything in the build
declared a context; bubbletea emitted `Measurement{Px: 20}/8` for any padding,
margin, width or height; and markup's `Role` was missing the six headings its
own blocks name.

## 2. Platform implementations

- [x] **html**, `--lang none`. `codegen/platform/html/html.sngl` declares
      `flow` and `inline` -- the same element twice, differing only in the
      family each joins -- and one override per member. `spanStyleCSS` in Go
      maps a `run`'s style, because which fields a `SpanStyle` literal set is a
      question about the literal and a prop is opaque to the `if` that would
      ask it. `testdata/markup_inline.txtar` is the golden.
- [x] **`image`.** `alt` is a string (Jonathan's call): no formatting is
      available in a description, every host that has one has a plain
      attribute, and a description is read aloud rather than looked at. So the
      importer flattens CommonMark's inline alt content to its words, and
      html's override is an `<img>` with the attribute it was already a string
      for.
- [x] **A run that reads state** is patched, and nothing had to be written for
      it. A run is an ordinary node at codegen time, so `passReactivity` sees
      the prop and emits the updater -- `md.text(value=string(n))` becomes a
      `textContent` write in the handler that moved `n`. That is the payoff
      for keeping the tree: the canvas needs `passCanvasReactivity` only
      because `passCanvas` hoists its shapes out of the tree first (#252).
- [x] **html, `--lang go`** — route mode, same overrides, and the same markup
      byte for byte. Not free: route mode renders through a second emitter
      that refuses a prop which is neither a literal nor state-dependent, and
      a run's style is neither -- it is a set of CSS declarations. Both
      renderers now ask `nodeInlineCSS` for an element's inline CSS, which is
      also what stops a node carrying a box style and a run style from
      emitting `style` twice. Both targets are in `markup_inline.txtar`.
- [x] **none** (interpreter). `codegen/platform/none/none.sngl` declares
      `flow` and `inline` and overrides the six signatures. The interpreter
      draws nothing, so what it owes the family is the tree `sngl test`
      asserts on -- but a member whose body inserts its content is a component
      with a body, and a snapshot prints a component's expansion rather than
      the component. So `text` and `image` keep the empty bodies the shapes
      have and stay under their own names, and the four that host content go
      through a primitive that survives the expansion carrying the one prop
      the member's name does not already say. `cmd/sngl/testdata/markup_interpreted.txt`
      is the fixture; the golden in `testdata/markup_inline.txtar` cannot grow
      a `none` target, since `none` emits no code.
- [ ] **gtk4** — Pango markup in a `GtkLabel`. Escape `&`, `<`, `>`; `<b>`,
      `<i>`, `<u>`, `<s>`, `<tt>`, `<a href>`, `<span foreground=>`. This is
      the first host that has to resolve the cascade itself.
- [ ] **fyne** — `widget.RichText` segment list. Confirm whether
      `RichTextStyle` carries underline and strikethrough; if not, a custom
      `RichTextSegment` in `pkg/go/fynert` (a widget implementation, not a
      language gap).
- [ ] **bubbletea** — lipgloss. `image` has to render its description; that is
      the platform answering rather than dropping it.
- [ ] **android** — `AnnotatedString` + `SpanStyle`, `LinkAnnotation` for href.

### Token palette

- [ ] html emits `class="sngl-tok-<kind>"` and styles nothing. Decide where a
      page's palette comes from, and how an application overrides it; the other
      targets want values rather than classes, so it may not be one kind of
      thing on every target.

## 3. Markdown parser and the `md:` scheme

- [ ] Go parser producing an mdast-shaped tree. Decide vendored vs. own —
      CommonMark is a large spec and the subset a document needs is not.
- [ ] `codegen/scheme/markdown`, registered like `file` and `http`, resolving
      `import doc "md:./getting_started.md"`.
- [ ] Emit SNGL: a `vbox` of block components interleaved with other
      `ui.node`s. A document is a **package** holding one component and taking
      **no props** — the use case is documentation, so any SNGL snippet inside
      it is a logical island and nothing of the host application reaches in.
      Frontmatter, if any, as consts.
- [ ] Block mapping — none of it in the family. `paragraph`, `heading`,
      `quote`, `codeBlock` and `caption` now exist as bodied `ui.node`
      components carrying a `Role`, so the importer emits those; the rest is
      ordinary layout: list → `vbox` of `hbox` rows (bullet in a fixed column
      so wrapped lines hang); `thematicBreak` → `ui.divider`; table →
      `ui.table`.
- [ ] Task list rendering per platform — `listItem(task=)` emits a disabled
      `ui.checkbox` by default; html wants a real disabled `<input type=checkbox>`
      inside the `<li>`.
- [ ] `list` / `listItem` overrides per platform. They are bodied, so a target
      inherits a box with markers beside it; html wants `<ul>`/`<ol>`/`<li>`,
      and since neither hosts the span family an override of either inlines
      like any `sngl:ui` override. The importer writes `marker`, so an ordered
      list is numbered even where the host cannot count.
- [ ] Inline mapping — emphasis, strong, delete, link, image, inlineCode, hard
      break (`"\n"`), soft break (the importer decides space or newline). An
      image's alt text flattens to its words; nothing else about it survives.
- [ ] Syntax highlighting inside a fence: tokenize, emit `token` spans inside a
      `monospace`. SNGL's own lexer highlights `sngl` fences; decide what
      answers for `go`, `bash`, `json`.
- [ ] **Live fences.** A fence whose content should become components rather
      than text — the thing that lets a tutorial show a running example beside
      its source. Needs a directive spelling (` ```sngl run `? an attribute?),
      and the parser leaves the `richText` and splices the parsed components
      into the `vbox`.

## 4. Compile-time parse (later)

- [ ] `markup.md("""...""")` in node position — a marked component whose
      `source` prop is const, expanded by a pass through the same parser. Not
      needed for the importer path; it is the hand-written door to the same
      tree.

## 5. Tests

- [x] Golden per target: `testdata/markup_inline.txtar`, html only so far and
      growing a target per implementation. It asserts on the golden rather than
      a grep and `deny`s the two absences that matter -- no `innerHTML`, and
      nothing emitted under the family's own names.
- [x] `cmd/sngl/testdata/markup_rendered.txt` runs a document in Chromium and
      snapshots the DOM it parsed: `<em>not markup</em>` comes back as four
      entities in one span, the two spaces and the newline are in the text
      node, and `white-space: pre-wrap` is on the element. Confirmed by
      reverting -- without the escaping, `sngl test` still says PASS and the
      grep is what fails, because an injection is invisible to a state
      assertion. Layout itself is not asserted. The interpreter half waits on
      `none`.
- [x] Positive checker fixture for the family: `testdata/markup_flow.sngl`,
      the other half of `error_markup_span_membership.sngl`. It carries no
      `output` block, so nothing asks which targets implement the family and
      the claim is about the checker alone -- both nesting orders, every
      member, the bodied blocks as ordinary nodes, and a component joining the
      family by naming it and by inference.
- [ ] Round-trip: a document imported twice is the same SNGL.

## 6. Acceptance

- [ ] Rewrite `internal/learn/getting_started.sngl` as markdown with live
      `sngl` fences.
- [ ] Delete `internal/docui/markdown.sngl` and its `innerHTML` path.
- [ ] Reconsider the rest of `internal/docui` — `Heading1`, `Paragraph`,
      `CodeBlock`, `Table` are the block vocabulary the importer now emits, so
      they may belong in `lib/` rather than in an internal package.

## 7. Before merge

- [ ] CLAUDE.md: a section on the markup family, why it is inline-only, and the
      literal-whitespace rule.
- [x] `lib/lib.go` needs nothing: its embed pattern is `*/*.sngl */*/*.sngl`
      and `Packages()` reads the embedded directory, so a nested package is
      already covered.
- [ ] Delete this file.

---

## Contexts — done

A provider's value is evaluated in the scope *enclosing* it, so
`depth(depth + 1)` accumulates across nesting. Not the self-reference family
the const guard is in: the read resolves to a strictly outer scope, so it
terminates. `context #depth(depth)` — the case that *is* self-reference — is
now a positioned checker error, along with every other node argument that
reads the node's own `#id`.

**A context follows a slot.** A provider written in a component's body covers
the content a caller writes into that component's slots. Three gaps were in
the way and all three are closed:

- `rewriteReads` ran *before* `lowerProviders`, so inside a component body
  every read had already collapsed to the one hidden var and a provider there
  set nothing at all. The two are now in the other order, which is the whole
  fix: a read under a provider is substituted by the provider, and what is left
  over is what the hidden var answers.
- Slot populations were threaded by lexical position. `computeSlotEnvs` records,
  per component and per slot, the provider values enclosing that slot's
  insertion, composed down the chain in the callee's own terms; a call site
  substitutes what it threaded to that callee. `ir.CloneExprSharingDecls` is
  what makes one recorded value safe to splice into several call sites.
- The interpreter answered a provider only within the body it was written in.
  A component instance is now mounted with a *copy* of the context values in
  force at that point, and slot content is mounted with the contexts of the
  insertion and the names of the caller.

`testdata/context_through_slot.txtar` and
`cmd/sngl/testdata/context_through_slot.txt` are the pair, the second because
the interpreter's answer has to run.

`listItem` now asks `listDepth` for its bullet and `list` provides
`listDepth + 1` around its items, so a nested list changes bullet with no
caller involved. It renders: a bodied `lib/` component reaching codegen at all
was the blocker, and `testdata/lib_component_body_renders.txtar` is that list,
nested, with a different bullet at each depth.

## Open questions

- Where does the `Token` palette live, and how does an application override it?
  html wants classes and everyone else wants values, so it may not be one kind
  of thing on every target.
- Does a platform ever need to override a block component after all? They are
  bodied so it is optional. The reason it used to be impossible is gone —
  hosting a family no longer makes a component a primitive, so an override of
  one inlines like any other — which means a richer `Role` and an override are
  both now on the table. Worth confirming against a real platform before the
  vocabulary is fixed.
