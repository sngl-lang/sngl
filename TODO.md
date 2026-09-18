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
- [ ] Style accumulation down the nesting. Two candidates, and the choice
      decides how much of this is SNGL:
      - a **pass** folds `bold { italic { text } }` into one run with both
        bits set, the way `emitShapes` walks the shape tree;
      - a **context** (`context #spanStyle`) has each span override it for its
        subtree and the leaf read it, which is existing language machinery and
        would put the spans' implementation in SNGL rather than Go.
      The second is the `shapes.rect → draw(@draw(e){…})` split; try it first
      and fall back if the context's value cannot be read at lowering time.
- [ ] `link` and `token` are not style bits — a run carries an optional href
      and an optional token kind alongside its flags.
- [ ] `image` is not a run at all. Decide whether it interrupts the run list
      as its own entry or lowers to a node the platform splices into the flow.
- [ ] Nested `richText` is not a thing (it is a node, not a span), so the pass
      has one level to walk. Confirm the tree rule already refuses it.

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
- [ ] Emit SNGL: a `vbox` of `richText` runs interleaved with `ui.node` blocks.
      One component per document; frontmatter, if any, as consts.
- [ ] Block mapping — all of it ordinary layout, none of it in the family:
      paragraph → `richText`; heading → `richText` with a size; blockquote →
      `vbox` with a rule; list → `vbox` of `hbox` rows (bullet in a fixed
      column so wrapped lines hang); `thematicBreak` → `ui.divider`; table →
      `ui.table`; code fence → a code-block node.
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

## Open questions

- Does a span inherit the `richText`'s `Style` on every target, or only where
  the host's inline model has inheritance? A Fyne segment list does not.
- Should `Token` grow a `plain` member, or is "no token span" the plain case?
- An imported document is a component — can it take props, so app state reaches
  an interpolation in the markdown? If so, how does the markdown name them?
- `monospace` inside a heading, `link` inside `monospace`: legal by the tree,
  but confirm each target renders the combination rather than the last-set wins.
- Accessibility beyond `alt`: does a heading emitted as a styled `richText`
  still announce as a heading? On html that wants a real `<h2>`, which argues
  the importer should emit a semantic node, not a styled run.
