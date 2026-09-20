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
- [x] **gtk4** — Pango markup in a `GtkLabel`, and the one target where the
      *host* resolves the cascade: Pango nests, so `<b><i>x</i></b>` is what
      `bold { italic { … } }` means and the tree an author wrote is the tree
      GTK is handed. Nothing is flattened; what the platform builds is the
      string.

      A flow is one `GtkLabel` and a span is no widget at all, so neither
      primitive names a GIR class and the span nodes emit nothing. The markup
      is a Go *expression* -- a literal run escaped where it is written, a run
      reading state as `gtk4rt.Escape(expr)` -- so reactivity costs one
      `LabelSetMarkup` and no span in the Model. The tree is collected
      package-wide ahead of the walk, on `emitShared` beside the canvas maps
      and for the same reason: a reactive splice assigns a span's prop in a
      scope that never saw the flow.

      The flow's own typography is the outer `<span>`, which is also what
      Pango inherits from -- and it is the only part of a `ui.Style` this
      target has ever answered, gtk4 having no per-widget styling. So a
      heading is large and bold here and a code sample still has no
      background.
- [x] **fyne** — `widget.RichText`, via `pkg/go/fynetext`. Two primitives and
      a table of overrides, and the ordinary fyne path downstream of them: a
      constructor, a Model field, `Add`, a setter per reactive prop, because a
      span is a value with an `Add` and a `SetText`, which is all this platform
      has ever asked a node to be. The one thing its Spec cannot be is *data* --
      a run is constructed with the style it was written with, and which fields
      a `SpanStyle` literal set is a question about the literal -- so
      `codegen/platform/fyne/markup.go` builds it from the node's props, and
      the style reaches the constructor as a builder chain rather than a record
      so only the first name needs the alias this file gave the runtime package.

      Three things Fyne does not do for itself are in the runtime package, and
      each is a widget implementation rather than a language gap:

      - **The cascade.** Flattened at render time and not at build time,
        because the tree is what a reactive program mutates -- a `SetText`
        against a nested span is all that reaches the emitted code when state
        moves, and the flow rebuilds its segments from the tree it still holds.
      - **Color and size.** A segment names them through a *theme* and never
        carries a value, so the flow names the ones its runs asked for -- one
        name per distinct value -- and carries a theme answering those names
        over whatever theme the block style put it under.
      - **Underline and strikethrough.** Drawn by a segment of the package's
        own, which is the exception: `widget.RichText` breaks a line inside
        `*widget.TextSegment` and nothing else, so anything else inline is one
        unbreakable box, as Fyne's own `HyperlinkSegment` already is. Short for
        a run of emphasis, wrong for a paragraph, which is why every other run
        stays Fyne's.

      A `token` is a theme color name, which is the form this host has for the
      family's "whoever is drawing decides" -- so an application that themes
      its app themes its code samples with it. A `link` is Fyne's own
      `HyperlinkSegment`, which carries its words and no style: what was said
      inside the link is lost, and the link is followed when it is tapped.
      `image` renders its description, Fyne's image segment being a block.
- [x] **bubbletea** — lipgloss. `Flow` and `Span` in `bubbletea.sngl`, a
      table of overrides, and the cascade resolved in `markup.go`: lipgloss
      renders a string and hands back a string, so the nesting is flattened at
      compile time and each run of words is rendered exactly once with the
      style it ends up with. A second render over the joined flow is not the
      tidier alternative -- the runs inside it have left their reset sequences
      in the string, and an outer color stops at the first of them -- so a
      flow's typography seeds the words and only its box style reaches the
      block.

      Three answers a terminal gives differently. One face and one size, so
      `monospace` needs no override at all and a heading is bold rather than
      large. No way to follow a link, so the URL is dropped and the words are
      underlined, which is the trade `ui.link` already makes. And `image`
      renders its description in brackets, the form `ui.image` renders its own
      placeholder in.
- [x] **android** — `AnnotatedString` + `SpanStyle`, `LinkAnnotation.Url` for
      href, and the best-matched host of the six. Compose's `SpanStyle`
      carries color, size, family, weight and slant, leaves every one unset by
      default, and merges overlapping styles with an unset field leaving the
      enclosing one standing -- which is exactly what `markup.SpanStyle`'s
      `inherit` members mean. So nothing is flattened and the *host* resolves
      the cascade, as it does on gtk4.

      A span is a piece of an `AnnotatedString` *value* rather than a
      composable, so this looks like gtk4: a flow is one `Text` and neither
      primitive is a call. It needs no pre-pass, though -- android is a
      RenderModel target and its emitter walks the `ir.NodeInst` tree, where
      gtk4 reads lowered CreateNode statements and has to collect the flows
      ahead of the walk. Reactivity is free for bubbletea's reason: Compose
      re-renders from state.

      `textDecoration` is the one field the host does not merge and the one
      thing the walk carries down. Compose applies a decoration as a single
      span setting both of the paint's flags, so a `strike` written inside an
      `underline` turns the underline off unless the inner run writes the
      whole accumulated set.

      Two bugs fell out on the way, both pre-existing and both in the Style a
      block component sets: a measurement written with its unit came out as
      `Measurement(px = 8.0).dp`, which compiles nowhere, and a Style's
      `fontWeight`, `fontStyle` and `fontFamily` never reached the
      `TextStyle` at all -- the weight was compared against the string
      `"bold"`, which an enum member never is. A heading was unemphasized and
      a quotation upright on this target. Each has its own commit and fixture.

      `image` renders its description in brackets. Compose has
      `InlineTextContent` and is the one host here that could draw an inline
      picture, but it needs a painter per source, which is the same thing
      `ui.image` has no answer for on android.

      With this, `testdata/markup_flow.sngl` no longer carries its
      `// SKIP(codegen)`: every registered platform implements the family.

### Token palette

- [ ] html emits `class="sngl-tok-<kind>"` and styles nothing. Decide where a
      page's palette comes from, and how an application overrides it; the other
      targets want values rather than classes, so it may not be one kind of
      thing on every target.

      bubbletea is the first target to answer it and answers it the same way
      html does, in the vocabulary it has: one of the terminal's own sixteen
      colors, which is a palette the reader already picked. Three kinds --
      `variable`, `operator`, `punctuation` -- map to the foreground, which is
      what most themes do with them and what sixteen colors is worth spending.
      fyne answers it in its own vocabulary too, and more directly: a theme
      color name, so an application that themes its app themes its code
      samples with it.

      android answers it in the same spirit and with the most to give: a role
      in the Material color scheme, so a themed app themes its code samples
      and light and dark come out right without this compiler naming a value.
      What a scheme does not have is seven hues -- three accents, an error
      color and two muted roles -- so the kinds are grouped by what they are:
      a keyword is the primary accent, the names of things share the
      secondary, the literals share the tertiary, and a comment is
      onSurfaceVariant, which is the one mapping that is exactly right.

      gtk4 is the one that cannot. Pango markup has no classes and no theme
      names, so a foreground colour is the only thing a span can be told --
      which is what the family's own declaration nominates for this host, and
      is also the case the `token` doc warns about: the names chosen there
      read on the light theme GTK ships with and are approximate against a
      dark one. Five targets, four of which hand the palette to the reader
      and one of which cannot, is the shape the answer is taking -- and the
      open half is a host with real colours reading them from somewhere an
      application writes.

## 3. Markdown parser and the `md:` scheme

`codegen/scheme/markdown` is the importer. `import doc "md:./guide.md"` parses
the file and hands back a filesystem holding one generated `.sngl`, which the
checker then checks like any other package -- the `FSSchemeImporter` seam
`git:` and `http:` use, chosen over the native one so the generated package can
be dumped and read. `testdata/markdown_import.txtar` is the golden and
`cmd/sngl/testdata/markdown_import_errors.txt` the diagnostics.

- [x] Walk goldmark's AST. The same `goldmark.New(WithExtensions(extension.GFM))`
      the doc site parses with, so two readings of one document cannot differ.
      Its nodes carry source *segments*, so every run of words is read back out
      of the original buffer.
- [x] `codegen/scheme/markdown`, registered like `file` and `http`.

      One thing the plan did not have: the document is a file of the *project*,
      not something the scheme fetches, so it has to be read through the
      filesystem the program is checked against -- an in-memory package (a
      golden archive, the playground) has no other. `codegen.ProjectFSScheme`
      is that, the FS-side counterpart of `FSAwareScheme`, and `ResolveFS`
      stays the fallback for a caller with no FS.
- [x] Emit SNGL: a `vbox` of block components. The component is named
      `document` whatever the file is called -- deriving it from the filename
      would make the call site depend on a path the alias already stands for.
      No props. Frontmatter scalars become consts, in the order the document
      wrote them, keeping their type.
- [x] Block mapping -- `paragraph`, the six headings, `quote`, `codeBlock`,
      `list`/`listItem` from `sngl:ui/markup`; `thematicBreak` → `ui.divider`;
      table → `ui.table`. A blockquote's *paragraphs* each become a
      `markup.quote`, the family having no block that contains prose. An html
      block and a raw inline are dropped: one target's vocabulary, and this
      renders on six.
- [x] Task list rendering -- `listItem(task=)`, lifted off the item's first
      inline before the block is walked. The per-platform half is still open
      below.
- [x] Inline mapping -- emphasis, strong, delete, link, autolink, image,
      inlineCode, hard break (`"\n"`), soft break (a space: the family's text
      is literal, so the wrapping the author's editor chose must not reach the
      reader as one). Adjacent literal text is coalesced, so a sentence is one
      `markup.text` rather than one per word.
- [x] Syntax highlighting inside a fence -- chroma, with
      `internal/highlight`'s SNGL lexer registered, folded onto the family's
      eleven kinds. `plain` is written rather than left out, so whitespace and
      an unclassified identifier are spans a reader of the tree can see. A
      language chroma does not know, and a fence that named none, is one plain
      run.
- [x] **Live fences.** All four modes, and the four questions the spelling
      left open are answered below.
- [ ] `list` / `listItem` overrides per platform, and the task item's real
      `<input type=checkbox>` inside an `<li>`. Still open, still optional --
      the default bodies render on every target, which is what the golden
      shows.

### Live fences -- the settled spelling

A fence whose content becomes components rather than text -- the thing that
lets a tutorial show a running example beside its source. The spelling is a
**`mode=` trailer on the info line** (Jonathan's call): ` ```sngl mode=island `.
goldmark's `Language()` splits the info string at the first space and
`Info.Segment` carries the rest, so the fence still reports `sngl` and still
highlights through the chroma path every other fence uses -- a trailer costs
the default path nothing.

Four modes, and what separates them is **which scope the source lands in**
rather than how it is parsed:

- **`view`** -- the default, and what every fence in every other language
  already is. Shown, highlighted, not compiled. Never written, being the
  default.
- **`island`** -- a component of its own, inserted where the fence was, with
  its imports hoisted and its `output` blocks dropped.

  The isolation is the language's own rather than a mechanism this importer
  built: the whole fence goes in the component's **body**, where a `var` is
  that component's state and a `struct`, `enum`, `unit` or nested `component`
  is scoped to that body. So two islands each writing `var n = 0` are two
  examples and neither reaches the other, and neither reaches the page.

  What follows from that is the contract: an island *is* a component body.
  An example that wants a `window` has nowhere to put one, a `window` being a
  member of `sngl:ui`'s root family and a body accepting `ui.node`.
- **`package`** -- package-level declarations, and nothing at the position it
  was written. **Declarations only** (Jonathan's call): it contributes funcs,
  consts, types, contexts and components that the document's islands and body
  fences can name. It selects no wrapper and nothing is reserved -- the
  question of how one would have been named is retracted with the feature.
- **`body`** -- the statements it holds, placed into the generated document's
  own component body at the position it was written. The positional
  counterpart of `package`: `package` declares, `body` emits.

The three written modes name the scope the source lands in, which is also what
keeps them clear of three terms this repository already spends precisely.
`inline` is the inliner and is markup's own inline-vs-block distinction; `root`
is `sngl:ui`'s root family, the thing that makes a window top-level; and a
`view` body is what renders, which is the opposite of the fence that does not.
`view` keeps the name anyway, being the default and so never written.

The four things the modes did not settle, now settled:

- **A live fence does not also show its source** (Jonathan's call), for any of
  the three. An example is what it does, and a document that wants both writes
  both -- which costs nothing, the two fences being the two things they are.
- **A `package` fence names no wrapper**, above.
- **Imports are hoisted as written, and collapsed when redundant** (Jonathan's
  call). Rewriting an alias means resolving every name under it, which is
  checking, and this importer emits source *for* the checker rather than doing
  its work. The same alias for the same path, twice, is one import; the two
  the generated package always writes are seeded into that set, so a fence
  importing `sngl:ui` as `ui` adds nothing. A genuine conflict reaches the
  checker as the program it is, and fails there.

  The cost is written down in
  `cmd/sngl/testdata/markdown_live_fence_errors.txt`: the diagnostic names
  `document.sngl`, the generated file, which the author cannot open. A second
  check in the importer would fix that and is the thing this decision declines
  to build.
- **`mode=` on a fence whose language is not `sngl` is an error**, named at the
  fence. So is a mode value nobody defined.

  An *unrecognised trailer* is not. The info line is shared vocabulary -- a doc
  site reads `title=` and `linenos` off it -- and refusing one would turn a
  document that renders everywhere else into a build failure. The cost is that
  `mod=island` is silently `view`; `mode=islnd` is not, being a value this does
  name.

A mistake inside a fence reports the **markdown file and line**, because the
position a checker would report is the `import` that read the document and says
nothing about where in it the mistake was written. That is what the frontmatter
splitter's line count is for: goldmark never sees those lines.

`testdata/markdown_live_fences.txtar` is the golden -- two islands with a `var n`
each, coming out as two cells with different values -- and
`cmd/sngl/testdata/markdown_live_fence_errors.txt` the diagnostics.

### Three bugs the importer found, each fixed with its own commit and fixture

None is about markdown; each is a gap a document was simply the first thing to
reach.

- A component an **import left standing** never had its body inlined. The
  inliner walked `main` and the windows -- every root a *program* declares and
  none of the ones the pass elects -- so `markup.list` written in an imported
  component reached codegen as a node and html wrote `<list>`.
- An imported package's **contexts** landed on a list nothing downstream
  reads, which was a nil-map panic in `computeReachability`; and html then
  spelled a synthesized reference bare while binding it under a promoted name,
  which was a ReferenceError under markup that had rendered.
- The optimizer's inliner **bound only the props the call site wrote**, so
  `markup.listItem(marker="1.")` kept a bare `task` and every numbered list
  item drew a stray disabled checkbox beside its ordinal.

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
- [x] `testdata/markup_reactive.txtar` is the run-that-reads-state claim, per
      target: html patches a text node, bubbletea re-renders, fyne calls
      `SetText` on a span that refreshes the flow owning it, and gtk4 writes
      the whole label again from an expression over the same state.
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
- [x] Round-trip: a document imported twice is the same SNGL.
      `codegen/scheme/markdown/convert_test.go` converts one sample five times
      and compares, and parses the result -- the generated package is source,
      so the first thing it owes anyone is to parse.

## 6. Acceptance

**Both halves want a decision before they are written.** What each actually
needs is below, found by reading the two files rather than by attempting them.

- [ ] Rewrite `internal/learn/getting_started.sngl` as markdown with live
      `sngl` fences.

      **What is in the way: the tutorial's prose is inside its conditionals.**
      It holds `var runPlatform`, four buttons that set it, and a run of
      `if runPlatform == "bubbletea" { CodeBlock(...) Paragraph(...) }` --
      prose and samples *inside* a branch, and that is most of the file.

      A `body` fence holds complete statements, so an `if` written in one takes
      its branches with it, and the prose inside them has to be written as
      `markup.paragraph { markup.text(...) }` by hand. Markdown would survive
      in the eighth of the file that is unconditional and nowhere else, which
      is not the rewrite this item means.

      Three ways out, none of them free:

      - **A paired fence** -- one that opens a block and a later one that
        closes it -- so prose between two fences is inside the branch. It is
        the obvious answer and it is also the one that makes a document's
        blocks no longer nest the way the document does: a mismatched pair is
        a new class of error with nothing local to report it against.
      - **A slotted component per branch**, written in a `package` fence and
        given the prose as children by an `island`. Expressible today, and it
        moves the conditional out of the prose rather than letting prose sit
        inside one.
      - **Drop the interactivity** and show every platform's instructions,
        which is what most tutorials do and what markdown is shaped for.

- [ ] Delete `internal/docui/markdown.sngl` and its `innerHTML` path.

      **`md:` does not replace it, and neither would §4 on its own.** The name
      is misleading: `docui.Markdown` renders no markdown. Its `src` is
      *already-rendered HTML*, produced Go-side -- `lookup.DeclPage.Body` says
      so in as many words ("rendered HTML body (already markdown-converted)"),
      and the other three call sites are a component preview, a
      chroma-rendered code sample and a doc page's body. It is an HTML sink.

      And its four call sites are inside `for` loops over *data*
      (`docs.Pages()`, `lookup.AllDeclPages()`), not over a fixed list of
      files. An import names one document at build time, so nothing about the
      `md:` scheme reaches a page whose existence is a row in a slice.

      What would close it is a different change: the Go side handing back
      markdown *source* instead of HTML, and §4's compile-time parse over the
      const string that results, folded per iteration of the loop. Worth doing
      and worth costing separately -- it is the `innerHTML` path's real
      replacement and it is not this branch's.

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

## 8. Directory form: `md:./docs/` as a package of pages

The docsgen site needs the importer to take a **directory** as well as a file.
Settled in conversation with Jonathan; nothing below is written yet.

The shape:

- A directory is one package. Plain `.sngl` files in it are part of that
  package, so a document's own declarations live beside its prose.
- Each page is a component. `index.md` is the parent page's content.
- Frontmatter is checked against a `struct Frontmatter` the directory
  **declares in one of those `.sngl` files** -- the importer emits
  `Frontmatter{...}` literals and the checker validates them, so the importer
  does no checking of its own and a site defines its own front matter
  vocabulary. With no such struct declared, the importer synthesizes one from
  the union of the keys it saw.

### Components are not values, and are not going to be

`struct Page { content component() ui.node }` is refused today, with the
message the rule deserves: *a component type declares a slot, so only a
parameter of a component may have one*. So is a component type inside a slot's
invocation list. That is not an accident to work around.

The compiler reads `NodeInst.Component` as a **declaration pointer** in the
inliner, the recursion-cycle finder, tree inference, `reachedLibComponents`,
`computeSlotEnvs` and the purity fixpoint. A component *value* has two honest
shapes and neither is worth it here: a const-only type -- no `var`, no func
return, no element of a runtime list, which is an orthogonality wart in the
type system -- or a real renderable handle on all six targets plus a
"don't know" arm in every one of those analyses.

### The mechanism is a scoped slot, and most of it already works

Measured on this branch, all of it:

- **One slot inserted many times**, each with its own arguments, populated
  once at the call site. Composes correctly through codegen.
- **A struct-typed invocation argument** -- the scoped slot. So frontmatter
  rides to the layout as an argument and needs no second channel:
  `layout(Frontmatter{title="One", weight=1})`, read as `fm.title` in the
  population.
- **Generic over the family**, so the caller decides what wrapping a page
  means. One `each<T>` declaration, bound to `ui.node` under a `vbox` and to
  `draw.shape` under a `canvas` in one program, emitted two `<span>`s and two
  `arc(10, 10, 4, ...)`. `T` binds off the population the way `boundary<T>`
  binds off its content.

Two limits found while measuring, both by design: a slot's invocation
arguments are **positional** (the names belong to whoever populates it), and
the invocation list may not carry `...`.

So the generated package is:

```sngl
// generated by md:./docs/
component site<T>(layout component(fm Frontmatter, content ...component ui.node) T) T {
    layout(Frontmatter{title="Home", href="/index.html"}) { <index.md blocks> }
    layout(Frontmatter{title="Guide", href="/guide.html"}) { <guide.md blocks> }
}
```

and the program supplies the layout once:

```sngl
docs.site {
    component layout(fm Frontmatter, content ...component) {
        ui.window(title=fm.title, href=fm.href) {
            PageLayout(currentHref=fm.href) { content }
        }
    }
}
```

All of it compile-time: a slot population is substituted structurally by
`substituteSlots`, the repetition is source the importer wrote, and nothing
holds a component.

`struct Page` drops its `content` field and is plain data -- `href`,
`frontmatter`, `children` -- for whatever navigation the site draws. A
recursive `children list<Page>` is fine.

### What stands in the way

Four defects, all found by measuring the design rather than by attempting it.
Three are silent.

- **A -- children at a slot insertion are dropped, with no diagnostic.**
  `layout("One") { ui.text(value="body") }` type-checks and the text vanishes.
  This is the enabling half of the design: it is how a page's blocks reach the
  layout. The spelling already parses, so implementing it is not a new syntax
  decision -- only the semantics are missing. The population has to be able to
  name them too, which today is an arity error (`slot "layout" binds 2
  parameter(s), but declares 1`), so the slot's *declaration* grows the
  children contract and the insertion supplies them.

- **B -- a slot population on a root component drops everything.**
  `component site(layout ...) ui.root` whose windows hold the insertions: both
  pages came out with an empty `<body>`, no error.

- **C -- a window produced from a slot population is lifted once, unbound.**
  The `T = ui.root` variant above, which is the version that makes the
  importer stop caring what a page becomes. `passRootWindow` runs 2nd of 35
  passes, before any component is expanded, so it reaches into the population
  and hoists the template as though it were an instance:

  ```
  // after RootWindow
  component site(layout component(title string, href string) T) T {
      layout("One", "/one.html")
      layout("Two", "/two.html")
  }
  window(title=title, href=href) { ui.text(value="body of " + title) }
  ```

  One window instead of two, `title` and `href` bare identifiers naming
  nothing, `site` never expanded. html reports "dynamic href", which is a true
  statement about a wrong tree. Slots are substituted in `expandCall`, fourteen
  passes later.

  **B and C are the same root cause and are not to be fixed here.** Teaching
  `liftWindows` what a slot population is means adding a fortieth place that
  knows what a window is, and it is work §9 would delete. Left broken and
  named, deliberately.

- **D -- a struct literal that omits a field with a declared default emits an
  empty value.** `Frontmatter{title="Home"}` against
  `struct Frontmatter { title string = ""  weight int = 0 }` comes out as
  `{title: "Home", weight: }` in JavaScript and the same shape in Go. Caught by
  esbuild and by `format.Source`, not by us. Unrelated to any of the above, and
  it will hit any `Frontmatter` struct on the first page that omits a key.

Order: **D**, then **A**. Both are independent of the window question, and with
the two of them the design works in the form where the importer writes the
windows. **B/C** waits on §9.

## 9. The window collapse

Jonathan's framing, and the right one: checking and lowering should have **one
path for all families**, and codegen should decide how a family is special.
`window` is the family that does not follow it.

The codebase already agrees in writing. `internal/lower/walk.go`, on
`walkWindow`:

> The cost is real and pre-dates this: `window #a(title = c ? x : y)` at the
> root of a file panics the Go emitter with "ir.Ternary reached Go codegen",
> while the same ternary a level in lowers. Where that temp belongs is the open
> question, **and a window that is a NodeInst in pkg.Body answers it for free.**

### What it costs today, measured

`*ir.Window` appears ~164 times outside tests. **~95 of those are in
`internal/checker`, `internal/optimize` and `internal/lower`; ~15 are in
`codegen/` and all six platforms combined.** The layer that should know a
family is special barely mentions it, and the layers that should not are where
it lives.

`internal/lower` alone carries **39 `case *ir.Window:` arms across 26 files**.
That is a statement kind every walk has to remember, which is the exact failure
CLAUDE.md already records one instance of: a window owns timers, `walkWindow`
and `offloadableFuncs` both forgot, and a ternary in a `@tick` panicked the Go
emitter while a `#[go.async]` call ran on fyne's drawing thread.

### What is left to collapse

`ir.Window` is already half-way there, and each field says so in its own
comment: `Comp`, `Props` and `Handle` are `NodeInst`'s shape, adopted in
earlier passes of this work.

- **`Vars`** -- a lowering artifact of `passHoistState`. The checker leaves a
  window's `var` as an `*ir.LocalVar` statement in the body, which is already
  the `NodeInst` shape. Follows for free.
- **`Funcs`** -- the real blocker. `ir` has no statement for a func
  declaration; `passCanvas` *appends* a synthesized draw func with no source
  body to live in; and around forty files read the per-window grouping to
  decide which funcs become that window's methods.
- **`Timers`** -- kept by decision (#243). bubbletea cannot follow the others:
  Elm lets nothing outside `Update` touch the model, and `Init()` needs a
  period and a body, which `ir.Timer` carries and the closure an `@mount` hands
  over cannot.
- **`Body`, `ErrorHandler`, `LocalRefs`** -- `NodeInst` has analogues or can
  grow them.

### What has to be named before it starts

Two things, and neither is mechanical:

1. **An `ir` statement for a func declaration**, or a body-owner concept on
   `NodeInst`. Without one, `Funcs` has nowhere to go.
2. **An answer for bubbletea's timer**, or an accepted exception for it.

Everything after that is mechanical across ~40 files. It is its own branch and
its own sitting, not a fix folded into this one.

What it buys, beyond the tidiness: the ternary panic above, §8's B and C, and
the standing gap `applyRootWindow` notes about itself -- a statement that is
not a window dropped in silence with the body it was written in, so a
`time.timer(...)` at the root of a file reaches no backend.

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
- Is a component ever a *value*? Answered no for §8 and worth holding to:
  the compiler reads `NodeInst.Component` as a declaration pointer in six
  analyses, so a value of component type is either a const-only type -- an
  orthogonality wart -- or a renderable handle on all six targets. The scoped
  slot in §8 is the compile-time version and covers the case that asked.
- Does a platform ever need to override a block component after all? They are
  bodied so it is optional. The reason it used to be impossible is gone —
  hosting a family no longer makes a component a primitive, so an override of
  one inlines like any other — which means a richer `Role` and an override are
  both now on the table. Worth confirming against a real platform before the
  vocabulary is fixed.
