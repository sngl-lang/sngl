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

## 1. Lowering — `richText` to runs

The span tree has to become something a backend can emit. A `passMarkup`
analogous to `passCanvas`: walk a `richText`'s children, accumulate the style
set down the nesting, and flatten to a list of runs.

- [ ] Mark the family so a pass can name it: `#[marks.builtin("treeMarkup")]`
      on `struct span`, `ir.BuiltinTreeMarkup`, `ir.IsMarkupTree`. The mark
      names the role, so a program's own `struct span` is not this family.
- [ ] Decide the IR shape of a run. Carries an **expression**, not a string —
      `markup.text("Hello, {name}!")` has to stay a render slot when `name`
      changes, so the pass runs early enough that `passReactivity` sees it the
      way it sees an `if`.
- [ ] Style accumulation is now a defined operation: a `richText`'s five font
      fields seed a `SpanStyle`, and each nested span overwrites the fields it
      set, `inherit`/zero meaning it set none. Write it once in the pass so
      every target gets the same answer.
      It is a pass and not a context: a `context #spanStyle` each span
      overrides would have put the spans' implementation in SNGL, which is the
      `shapes.rect → draw(@draw(e){…})` split — and a style set has to be
      resolvable at lowering time whatever the host. (The nesting a context
      would have needed now works; the reason is still the second one.)
- [ ] `link` and `token` are not style bits — a run carries an optional href
      and an optional token kind alongside its flags. That is also what makes
      `monospace { link { … } }` and `link { monospace { … } }` render the
      same: one record carries both, so neither nesting order can win over the
      other. Verify per target rather than assuming it.
- [ ] `image` is not a run at all. Decide whether it interrupts the run list
      as its own entry or lowers to a node the platform splices into the flow.
- [ ] Nested `richText` is not a thing (it is a node, not a span), so the pass
      has one level to walk. Confirm the tree rule already refuses it.
- [ ] Reactivity is settled and needs no new pattern — confirmed against the
      canvas, which is the same shape. A reactive canvas lowers to a draw
      function plus `__canvasRedraw(_canvasDraw0)` appended to the handler that
      mutates the state, and an `if` inside it becomes a conditional *within*
      that function rather than a render slot. So a `richText` is one reactive
      unit: anything its runs read makes the whole run list rebuild. Const
      inputs fold in the optimizer and the run list is static, which is what
      lets a documentation page emit literal markup and no script.
      Open: whether a paragraph-sized rebuild is granular enough, or whether an
      `if` in a span slot should become a real render slot. Start with the
      canvas behavior; it is the simpler one and matches the precedent.

## 2. Platform implementations

Every target that a document can reach needs one, or
`reportBodylessLibComponents` fails the build — which is the point.

- [ ] **html** first, `--lang none`. Escaping is the whole reason this lives in
      Go: `htmlutil` escapes the text, whitespace is honored with `pre-wrap` on
      the run (entities and `<br>` only where a style cannot be attached), and
      `token` emits a class the page styles.
- [ ] **html, `--lang go`** — route mode, same emitter.
- [ ] **none** (interpreter) — needed for `sngl test` and for the golden
      fixtures to run at all.
- [ ] **gtk4** — Pango markup in a `GtkLabel`. Escape `&`, `<`, `>`; `<b>`,
      `<i>`, `<u>`, `<s>`, `<tt>`, `<a href>`, `<span foreground=>`.
- [ ] **fyne** — `widget.RichText` segment list. Confirm whether
      `RichTextStyle` carries underline and strikethrough; if not, a custom
      `RichTextSegment` in `pkg/go/fynert` (a widget implementation, not a
      language gap).
- [ ] A span behaves the same everywhere, which is a requirement and not an
      aspiration. Decided: the flattening always resolves the cascade, so a
      host with no inheritance (a segment list) gets the same answer as one
      with it, and no target needs a capability of its own.
- [ ] fyne likely needs a custom `RichTextSegment` for the whole `SpanStyle` —
      `RichTextStyle` carries named sizes and theme colors, not arbitrary ones,
      on top of the underline/strike question.
- [ ] **bubbletea** — lipgloss. `image` has to render its description; that is
      the platform answering rather than dropping it.
- [ ] **android** — `AnnotatedString` + `SpanStyle`, `LinkAnnotation` for href.

### Token palette

- [ ] Decide where a target's `Token` → appearance mapping lives, and how an
      application overrides it. A context with a per-platform default is the
      obvious shape; html wants classes, not values, so the mapping may not be
      one kind of thing on every target.

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
      break (`"\n"`), soft break (the importer decides space or newline).
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

- [ ] Golden per target: `testdata/markup_inline.txtar` once one platform
      emits, growing a target per implementation. Assert on the golden, not a
      grep, and `deny` the absences (no `innerHTML`, no unescaped `<`).
- [ ] A `cmd/sngl/testdata/*.txt` that **runs** a document — the escaping and
      the whitespace rule are claims about output a golden cannot execute.
      Chromium for html, the interpreter for `none`.
- [ ] Positive checker fixture for the family, which cannot exist until a
      target implements `richText` (the bodyless rule fires first).
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
- [ ] `lib/lib.go`'s package list gains `ui/markup/`.
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
caller involved. **It cannot be seen yet**: a bodied `lib/` component with no
platform override does not reach codegen at all — `md.list { md.listItem { … } }`
emits its children and neither body — so the bullet does not render, on any
target, and did not before this either. That is §2's work, and it is the first
thing to check when a target implements the family. One thing to confirm there:
`computeSlotEnvs` and the reachability scan both iterate `pkg.Components`, and
a lib component is not in it today.

## Open questions

- Where does the `Token` palette live, and how does an application override it?
  html wants classes and everyone else wants values, so it may not be one kind
  of thing on every target.
- Does a platform ever need to override a block component after all? They are
  bodied so it is optional, but `hostsTree` makes a span-hosting component a
  primitive, so an override would not inline — meaning the answer has to be a
  richer `Role`, not an override. Worth confirming against a real platform
  before the vocabulary is fixed.
