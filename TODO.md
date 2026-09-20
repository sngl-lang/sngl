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

- [ ] Walk goldmark's AST. Not a decision to make: goldmark v1.7.8 is already
      a direct dependency with GFM on, in `internal/docsite/markdown.go`,
      which renders the doc site. Its tree is its own `ast.Node` and not
      mdast — and its nodes carry source *segments*, so the text is read back
      out of the original buffer rather than held on the node.
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
      `monospace`. Chroma already answers every language, and
      `internal/highlight` already registers the SNGL lexer with it -- which
      is what `internal/docsite.Highlight` uses. What is new is the mapping:
      the doc site turns a chroma token type into a CSS class, and the
      importer has to turn one into a `markup.Token` member instead. That is
      the narrower vocabulary, so it is a fold rather than a translation, and
      `Token.plain` is what an unclassified token becomes.
- [ ] **Live fences.** A fence whose content should become components rather
      than text — the thing that lets a tutorial show a running example beside
      its source. The spelling is a **`mode=` trailer on the info line**
      (Jonathan's call): ` ```sngl mode=island `. goldmark's `Language()`
      splits the info string at the first space and `Info.Segment` carries the
      rest, so the fence still reports `sngl` and still highlights through the
      chroma path every other fence uses — a trailer costs the default path
      nothing.

      Four modes, and what separates them is **which scope the source lands
      in** rather than how it is parsed:

      - **`view`** — the default, and what every fence in every other language
        already is. Handled like any other code fence: shown, highlighted, not
        compiled. Never written, being the default.
      - **`island`** — wrapped in a component of its own, with its imports
        hoisted to the generated package and its `output` blocks dropped. The
        isolation is the point and is what the name says: an example is not
        the host application, so two islands each writing `var n = 0` are two
        examples and neither reaches the other. This is the mode the "running
        example beside its source" case wants, and it is the word this file
        already used for a document's snippets before there were modes.
      - **`package`** — package-level declarations, and what they are for is
        the component the page gets wrapped in: a document can then be handed
        props and can set context values for everything under it.
      - **`body`** — placed directly into the generated document's own
        component body, as raw source, at the position it was written. The
        positional counterpart of `package`: `package` declares, `body`
        emits.

      The three written modes name the scope the source lands in, which is
      also what keeps them clear of three terms this repository already spends
      precisely. `inline` is the inliner and is markup's own inline-vs-block
      distinction; `root` is `sngl:ui`'s root family, the thing that makes a
      window top-level; and a `view` body is what renders, which is the
      opposite of the fence that does not. `view` keeps the name anyway, being
      the default and so never written.

      Four things the modes do not settle and the implementation will reach:

      - Whether an `island` or `body` fence also *shows* its source. The entry
        above has always said a live fence leaves the `richText` and splices
        the components, so both is the standing answer for `island`;
        `package` has no position to show at, and `body` is a judgment call.
      - How a `package` fence's wrapper component is named or selected.
      - What an island's hoisted import does when two of them claim one alias,
        or when one claims an alias the document already uses.
      - What `mode=` other than `view` means on a fence whose language is not
        `sngl`. Refusing it at the fence is the cheap answer and says so where
        it was written.

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
