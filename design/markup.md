# Markup and the md: scheme

**`sngl:ui/markup` is inline and nothing else.** A heading is a `text` with a
size, a quote a `vbox` with a rule down its side, a list a `vbox` of rows —
ordinary layout every platform already renders. A bold word inside a sentence
is not: it has to live in one flow of text with the words around it, and a box
cannot hold it without breaking the line. So the family is the part of a
document there was no other way to say, and a document is a `vbox` of
`richText` flows interleaved with anything else a `ui.node` can be — which is
also what makes a document extensible without the vocabulary growing.

`span` is an ordinary family, and that is the design rather
than a shortcut. A member holds its own family and never a `node`, so
emphasis around a link and a link inside emphasis are both one flow, and a box
in the middle of a sentence is a membership error rather than a content model
written in prose. There is no `passMarkup`: each platform overrides the members
in its own package and the nesting an author wrote is the tree the host is
handed. A run list flattened in `ir` was built and reverted, being a second
representation of a tree. Where the cascade is resolved is each host's answer —
CSS on html, Pango and Compose's `SpanStyle` merge natively on gtk4 and
android, fyne flattens at render time because the tree is what a reactive
program mutates, and bubbletea flattens at compile time because lipgloss
returns a string with reset sequences in it.

**A reactive `if` or `for` among spans** is a render slot only where a span
can hold one, which is html's `display:contents` wrapper and what
`#[gen.can(inlineSlots)]` says. Withheld, `slotFlows` makes the flow holding it
the slot -- wrapped in a one-pass loop over a const, which re-renders its body
on any state it reads -- and the `if` inside is an ordinary one: fyne builds
the flow around it, gtk4 guards the runs in its markup with `gtk4rt.When` and
refuses a `for` there, a label's markup being one expression
(`testdata/markup_reactive_span.txtar`). Only the flow's own content is asked:
an `if` holding a whole paragraph is its container's ordinary slot
(`testdata/markup_flow_under_reactive_if.txtar`).

`markup.SpanStyle` is its own struct and not `ui.Style` for two reasons. A run
has no box, so most of `ui.Style` would type-check on a span and do nothing
except on html, where a `<span>` takes padding. And flattening needs a third
state per field: in `ui.Style` an enum's zero is its first member, so an inner
run would say `normal` and un-bold its parent. `Weight` and `Slant` lead with
`inherit` for that reason, and the cost is that a span cannot turn *off* a
decoration an enclosing span turned on.

**A token's color is a palette's answer first and the host's second.**
`markup.palette` is a context holding a `Palette`, one color per `Token` kind,
where alpha zero means the palette says nothing about that kind. Unset, html and
gtk4 draw `lightPalette`/`darkPalette` (GitHub's syntax colors, which the docs
site's chroma style already used) and bubbletea, fyne and android ask their
theme. html writes the pair as the `sngl-tok-<kind>` class's rules, the dark
half under `prefers-color-scheme`, so a page's own CSS still wins; gtk4 takes
the light half. A kind the program sets wins on all five. An unset or constant
palette is a literal by the time it is emitted -- `UnprovidedContext` folds the
first, and an inlined body reads a provider's value in place of the context --
so only a palette read from state reaches an emitter as a run-time value:
`gtk4rt.Foreground`, `style.color`, fyne's `SetColor`, and a fallback chain on
bubbletea and android.

**Text in the family is literal.** Every character of a `markup.text` renders
as written — two spaces are two, a `"\n"` ends the line — on every target,
because what an author wrote between the quotes is the one thing a document
cannot have a platform reinterpret. Wrapping still happens around it. That is
why there is no line-break component, a second spelling of `"\n"` being a second
answer to give, and why html's collapsing is html's to undo (`white-space: pre-wrap`, `markup_rendered.txt` in a real Chromium) rather than the
author's. The importer follows from it: a soft break in markdown is a space,
since the wrapping an editor chose must not reach the reader as one, and a hard
break is `"\n"`.

html has two more ways to lose it, both closed. Its pretty-printer strips
the newline runs between tags inside a pre-wrap element, which is also what a
code sample's line-break-and-indent token looks like as a `<span>`, so a text node
that is all whitespace spells its line breaks `&#10;` (`escapeTextContent`,
denied in `markdown_import.txtar`). And a flow is a `<p>`/`<h*>`/`<pre>`,
which the browser's stylesheet gives a margin no other target has, so the
flow zeros it: the space between blocks is the document's `vbox` gap, and a
list item's text sits on its bullet's line rather than a margin below it.

The block components (`paragraph`, the six headings, `quote`, `codeBlock`,
`caption`, `list`, `listItem`) are **bodied** — each a `richText` with its
`role` and style set, or a `vbox` — so a platform implements the two
primitives and inherits every block. `role` is what a flow is *for*, and a
prop rather than a component per role, so it reaches every emitter through the
node they already render. html is the one target overriding a block: `list`
and `listItem` are `<ul>`/`<ol start>` and `<li>`, so the browser numbers the
list and picks a nested bullet, and a task item is a disabled
`<input type=checkbox>` in a flex `<li>`, which drops the marker. Every other
target renders the bodies. An override's prop defaults are the declaration's,
checked in its file: re-read in the override's, `task Task = Task.none` named a
type html.sngl never imported (`installedOverride`).

**`md:` imports markdown as SNGL source.** `codegen/scheme/markdown` is an
`FSSchemeImporter`, the seam `git:` and `http:` use, rather than a native
importer: what comes back is `.sngl` source the checker checks like any other
package, so a bad import can be dumped and read and a round-trip is assertable.
The document is read through the filesystem the program is checked against
(`codegen.ProjectFSScheme`), because it is a file of the project and an
in-memory package has no other. It parses with the same goldmark configuration
the doc site uses, so two readings of one document cannot differ -- heading
ids included: a heading is written with the `anchor` goldmark's
`WithAutoHeadingID` gave it, repeats numbered per document, so an in-page `#`
link resolves (`anchor_test.go` holds it to the doc site's rendering). The
anchor is `richText`'s; html writes it as the element's `id`, an empty one
writing none, and the other targets have nothing a link could land on.

- **A file**, `import doc "md:./guide.md"`, is a package holding one component,
  **`document`**, whatever the file is called: a name derived from the path
  would make the call site depend on something the alias already stands for.
  Frontmatter scalars become consts, in the order written and keeping their
  type. An html block and a raw inline are dropped — one target's vocabulary,
  in a document that renders on six.
- **A directory**, `import docs "md:./docs/"`, is a **site**: one package in
  which every markdown file under it is a page, and every `.sngl` file
  *directly* in it is a file of the package, so a site's own declarations sit
  beside its prose. A page is a component named for its path, `_` standing for
  what an identifier cannot hold (`index`, `guide`, `guide_intro`). The root is
  `index.md` and is required; `guide/intro.md` is a child of `guide`, whose
  content is `guide.md` or `guide/index.md` — both is an error, and neither
  leaves the child with no parent. A page's href follows its file:
  `/guide/intro.html`, and `/guide/index.html` for `guide/index.md` where
  `guide.md` is `/guide.html`. A file or directory whose name starts with `_`
  is the directory's own material and no page -- a template, a tutorial's
  source -- as Go skips one; the docs site's tutorial is `learn/_tour.md`.

  The package declares **`Page`** (`href`, `frontmatter`, `children list[Page]`),
  **`root`**, a const holding the whole tree, and **`site(layout list[component(page Page, content component ui.node) ui.node])`**, which renders
  one `nav.page` per file -- at the file's href, titled by the frontmatter's
  `title` where `Frontmatter` has one -- with `layout` inserted around it,
  reading each page out of `root` so the tree is written once, and populates
  its `content` entry with that page's component. `site` renders pages, so it
  composes them (see *Navigation*): the program
  writes it among a `nav.stack`'s pages, beside pages of its own, and the
  window around the stack is the chrome every page shares. Nothing holds a
  component, so all of it is compile-time. Those four names are reserved, and a page taking one, or two
  paths becoming one name, is refused naming the files.

  **`Frontmatter` is the site's own vocabulary.** A `struct Frontmatter`
  declared in one of the directory's `.sngl` files is what each page's literal
  is checked against, so the importer does no checking of its own. With none
  declared the importer synthesizes one: the union of the keys the pages wrote,
  in first-seen order, each typed by its first scalar and defaulting to that
  type's zero; a key written with two types is an error naming both files.

  **`#[md.order]` on a declared field sorts each page's children by it**,
  ascending and stable over path order. The mark is `sngl:ui/markup/md`'s,
  the home of the scheme's vocabulary, and the importer reads it off the AST
  before anything is checked — which is why a page omitting the key sorts as
  the field's default and that default has to be a literal. The checker's
  handler only holds the field to an int, float or string.

**A `sngl` fence may be live**, spelled `mode=` on the info line, which leaves
the fence reporting `sngl` and highlighting like any other. `view`, the
default, shows it. `island` is a component of its own at the fence's position,
with the whole fence as its **body** — so two islands each writing `var n = 0`
are two cells, by the language's own scoping rather than a mechanism the
importer built, and an island cannot hold a `window`. `package` contributes
declarations only; `body` places statements into the page at its position. A
live fence does not also show its source. Imports are hoisted as written and
collapsed only when identical, since rewriting an alias would be checking. A
mistake inside one reports the markdown file and line, because the position the
checker has is the `import` that read the document.

**`md.document(source=…)` parses markdown written as a string** -- the
hand-written door to the same tree, for a source that is a value rather than a
file (`lib/ui/markup/md/document.sngl`). The parse is the build intrinsic
`md.parse`, answered by `codegen/scheme/markdown/parse.go` with the scheme's
goldmark and highlighter, and wrapped in a `const func` the optimizer folds as
a producer: a literal folds in the first optimize, and a page's loop variable
in html's per-document fold, which has the build host for exactly that. What
comes back is data -- `_Block`, `_Item`, `_Run` -- that the declaration's own
components lay out with loops and `if`s, which on html unroll to markup. What
follows from the tree being rendered rather than written out:

- **It is the scheme's tree**, so on html the markup is the importer's byte
  for byte (`testdata/markdown_document_matches_scheme.txtar`, compared by
  `TestMarkdownDocumentMatchesScheme`): headings carry the scheme's `anchor`,
  a list is `markup.list`/`markup.listItem` -- html's `<ul>`/`<ol>`/`<li>`
  and a task's disabled checkbox -- and a run is bare `markup.text` or the
  one span member the scheme writes around it. Three things differ. A run
  under two spans is one `markup.run` carrying both, the parse handing it its
  styles rather than their nesting. A table is rows of text: `ui.table`
  renders nothing on fyne or android. And a
  list nests three deep, one component per level, since a component calling
  itself is built at run time; deeper items are laid out in their parent's
  place.
- **A run's style may be computed.** `listItem` reads the `listDepth`
  context, which passContext lowers to a `var` of the component, so under a
  `for` the item is an instance built at run time; there the two-span
  `markup.run`'s style, read from the data, reaches fyne as a write after
  construction. fyne's span answers it with `SetSpanStyle`, the
  `markup.SpanStyle` handed over a field at a time (`spanStyleArgs`, the
  spec's `SetterArgs`) since the runtime has no type for it, and the flow
  rebuilds its segments. A token's kind read from data is `SetToken`. A
  write the constructor was built from is the constructor's only when it
  reads no state: one reading state is the same expression after the state
  moved, and was dropped, so a run kept the kind and style it was built with
  (`testdata/fyne_markup_reactive_kind.txtar`). gtk4 refuses every `md.document`, a label's markup
  being one expression that a `for` among spans cannot be.
- **A loop's library structs are declared.** A target that runs the loop
  holds `[]_Block{…}` as a value, and Go's literal names its type, so
  `promoteForeignStructs` declares the element types of every view loop's
  iterable -- not only the types a function signature names.
- **A live fence is refused.** `mode=island`, `package` or `body` is source
  the program compiles, and a string parsed here is read after the check; the
  error names the markdown line and is reported at the program's node rather
  than the library's call (`evalCtx.site`, set where the optimizer splices a
  library body). `mode=view` is the default and passes.
- **The source must be known while the program builds.** An argument reading
  state is refused at the argument right after the first optimize
  (`refuseStateIntoBuildCalls`); a call no fold answered is refused at the node
  the program wrote (`refuseBuildOnlyCalls`, which on html leaves a view's
  calls to the fold and `codegen.RefuseUnfoldedBuildCalls` asks after each
  document). `testdata/markdown_document.txtar`, `markdown_document_pages.txtar`
  and `cmd/sngl/testdata/markdown_document_refused.txt`.

Four general changes it needed: the optimizer does not inline a function that
reaches the build host (it is folded whole, as a producer, or the bare
intrinsic is left for nothing to fold); `promoteForeignStructs` declares a
promoted struct's field types too; a library body may render its own
unexported components; and a registered build intrinsic is handed its call, so
it can build values of its declared return type.
