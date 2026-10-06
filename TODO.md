# markup + markdown importer — leftovers

Scratch. Delete this file once the list below is empty.

Done: the `span` family and its blocks on all six targets, the `md:` scheme
(file and directory forms, live fences, `#[md.order]`), the token palette
(`markup.palette`, light/dark default), the docs site's pages imported through
`md:./docs/`, the tutorial rewritten as `docs/learn/getting-started.md`, and §8's
four slot defects. CLAUDE.md's *Markup and the `md:` scheme* describes the
result.

## Open

- [ ] **Anchors** — #254. `markup.heading*` carries no id, so the in-page `#`
  links in `specification.md` break now that docs render through markup.
- [ ] **`markup.md("""...""")`** — compile-time parse in node position, a marked
  component whose `source` prop is const, expanded through the same parser.
  The hand-written door to the tree the importer builds.
- [ ] **Delete `internal/docui/markdown.sngl` and its `innerHTML` path.** Three
  call sites remain in `website.sngl` (`comp.previewHTML`,
  `comp.highlightedCode`, `d.body`). `docui.Markdown` is an HTML sink fed
  already-rendered HTML, inside `for` loops over data, so `md:` cannot reach
  it. The replacement is the Go side handing back markdown source and
  `markup.md` folding it per iteration.
- [ ] **Reconsider the rest of `internal/docui`** — `Heading1`, `Paragraph`,
  `CodeBlock`, `Table` overlap the block vocabulary the importer emits.
- [ ] **`list` / `listItem` overrides per platform** (optional), e.g. a real
  `<input type=checkbox>` inside an `<li>` for a task item. The default bodies
  render everywhere today.
- [ ] **html miscompiles a population inside a recursion.** Still reproduces:
  `frame(n=2)` whose body writes `frame(n=n - 1) { ui.text(value="level {n}") }`
  renders `level 1, level 0` on html `--lang none`; it should be
  `level 2, level 1`. The content's `n` binds to the callee's prop rather than
  the body it was written in. Silent.

## Open question

- Does a platform ever need to override a block component? They are bodied, so
  it is optional, and nothing now prevents it. Confirm against a real platform
  before the `Role` vocabulary is fixed.
