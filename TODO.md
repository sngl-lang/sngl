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
      `shapes.rect → draw(@draw(e){…})` split, but it is exactly the shape the
      context bug below breaks — and a style set has to be resolvable at
      lowering time whatever the host.
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

## Blocked on a compiler bug

- [ ] **A context provider whose value reads the context it overrides is
      broken.** Two symptoms, and the second is the serious one: the value does
      not accumulate across nesting, and the context name reaches codegen as a
      bare identifier nothing declares — so a language target emits code that
      does not compile.

      Repro, verbatim:

      ```sngl
      import ui "sngl:ui"

      context #depth(0)

      component main() ui.root {
          ui.window(title="t") {
              depth(depth + 1) {
                  ui.text(value="one={depth}")
                  depth(depth + 1) {
                      ui.text(value="two={depth}")
                  }
              }
          }
      }
      ```

      `sngl dump --stage lowered --platform html --lang none <file>` gives:

      ```
      span(textContent="one=" + string(depth + 1), …)
      span(textContent="two=" + string(depth + 1), …)
      ```

      Expected `1` and `2`. Note `depth` itself in the output, declared
      nowhere.

      A **plain** provider is fine and is the contrast to work from: with
      `depth(7) { reader() }` where `reader` reads `{depth}`, the lowering
      declares `var __ctx_depth__inst1 int = 7` and the read resolves to it.
      So the defect is specific to a provider reading the context it is
      overriding, not to contexts or to inlining.

      The passes live in `internal/checker/context.go` and
      `internal/lower/context.go`; neither has been read, so that is a starting
      point and not a diagnosis. This is the same self-reference family as the
      window-prop and const guards CLAUDE.md describes, except those guard and
      this one is silent — worth deciding whether the answer here is a
      positioned error or a working accumulation, since a language rule
      refusing self-reference would also settle those two.

      Blocks nothing in markup: `listItem.marker` is written by the importer,
      which knows the depth the way it knows the ordinal. Fixing it would let a
      nested list pick its own bullet with no caller involved.

## Open questions

- Where does the `Token` palette live, and how does an application override it?
  html wants classes and everyone else wants values, so it may not be one kind
  of thing on every target.
- Does a platform ever need to override a block component after all? They are
  bodied so it is optional, but `hostsTree` makes a span-hosting component a
  primitive, so an override would not inline — meaning the answer has to be a
  richer `Role`, not an override. Worth confirming against a real platform
  before the vocabulary is fixed.
