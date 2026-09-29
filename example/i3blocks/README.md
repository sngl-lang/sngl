# i3blocks: a user-defined family, emitted by a language override

**Design sketch. This does not compile.** `sngl check` stops at the first
proposed construct (`unknown type "family" in namespace "build"`). Every file
parses and is `sngl fmt`-clean, and each construct that does not exist yet is
marked `PROPOSED` where it is used.

It lives under `example/` rather than `examples/` because `docsgen` loads every
`examples/*/*.sngl` into the playground.

## What it demonstrates

A status bar for i3 whose blocks are a SNGL tree, running in the same process as
a GTK window that a click on a block opens:

- `bar.sngl` is the program: state, a timer, an `i3.bar`, and a `ui.window`
  under `if details`.
- `i3/i3.sngl` declares the family (`block`), its members (`text`, `gap`) and
  the root member that hosts them (`bar`).
- `i3/go.sngl` is `block[go.language]`, the family's emitter for Go. It runs at generate
  time over the lowered IR and writes `i3bar.go` into whichever Go host the build
  picked.

No platform is involved. gtk4 and fyne each render the window their own way, and
both reach the bar through the same language override.

## The proposal it is written against

1. **A family is a component in `build.family`.** `component block build.family`
   replaces `#[tree.kind] struct block {}`. *Done.* `sngl:ui`'s `node` becomes one too.
   A platform supports a family by overriding it, as in `ui.node[gtk4]`.
2. **An override is keyed by a build node's declaration.** *Done.*
   `[go.language]` names `sngl:language/go`'s
   `#[gen.name("go")] component language(…) build.language`, and the
   synthesized `platform` const is gone.
3. **A family's override is its emitter**, chosen platform → language →
   declaration, or else the per-target bodyless error at the position the
   program reached. i3 needs a language override because nothing it writes
   depends on the host.
4. **The emitter's body is build-tree content.** `gen.emit(@generate(tree, p))`
   runs in the interpreter at generate time:
   - `tree.hosts[i].members` is the lowered IR of each place the program renders
     the family.
   - `p` is the host program the emitter writes into.
5. **A bodyless root member whose rest slot takes a family is answered by that
   family's emitter** (`bar`).
6. **`@main` is a chain.** The program's package-level `main(@main(run))` is
   outermost, emitter layers (`p.main`) come next, and the host's loop is
   innermost.
7. **A window is part of the tree while its condition holds**, and `@close`
   writes that condition back.

### `gen.Program`: what a host hands an emitter

| call                                                                   | backed by today                                                                                              |
|------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------|
| `p.file(name)`; `f.line`/`open`/`close`/`reopen`/`require`             | `Sink`, `FileEmitter.RequireImport`                                                                          |
| `p.expr(e)`, `p.text(e)`, `p.ifHead(s)`, `p.forHead(s)`, `p.lambda(h)` | the language's `EvalExpr`/`EvalStmt`, `ForHead`, the handler lowering                                        |
| `p.field(name, type)`                                                  | Model fields (`CodegenCtx.ModelState`)                                                                       |
| `p.post(code)`                                                         | the host's hand-off to its loop thread (`async.post`: `fyne.Do`, a GLib idle source) followed by its updates |
| `p.onUpdate(code)`                                                     | render-model: run after each state change                                                                    |
| `p.main(before=…, after=…)`                                            | a layer of the `@main` chain                                                                                 |
| `p.error(pos, msg)`                                                    | a positioned build error                                                                                     |

### What the build would write on gtk4

The host writes the Model, the window, the timer and `main`. The emitter adds
`i3bar.go` and three hook-ins:

```go
func main() {
    m := newModel()
    os.Stdout.WriteString("{\"version\":1,\"click_events\":true}\n[\n") // p.main(before=…)
    m.i3Render()
    go m.i3ReadClicks()
    gtk4rt.Run(m.buildWindows) // host loop, innermost
}

func (m *Model) i3Render() {
    var blocks []i3Block
    m.i3Handlers = map[string]func(int, int, int){}
    for i, w := range m.workspaces { // p.forHead
        {
            b := i3Block{FullText: w}
            b.Name = "ws"
            b.Color = snglColorString(__tern0) // ternary lowered: the emitter claims nothing
            ...
            b.Instance = fmt.Sprint(len(blocks))
            m.i3Handlers[b.Name+"/"+b.Instance] = func(int, int, int) { m.SetFocused(i) } // p.lambda
            blocks = append(blocks, b)
        }
    }
    ...
}
```

A click is decoded on the reader goroutine and posted to GTK's loop, where it
runs the handler. When the handler writes `details = true`, the render slot
around the window fires and the window is created.

## Open questions

- **Per-family capabilities.** Can an emitter only withhold capabilities the
  host holds (its subtrees get extra lowering), or does it get real
  per-subtree lowering?
- **What the emitter sees.** Only its hosts' subtrees, or the whole lowered
  package?
- **`p.expr` reading state.** The emitter writes `m.`-qualified Go inside a
  `func (m *Model)` it wrote itself, and `p.expr` must agree on the receiver.
  Should the host own the method signature (`p.method("i3Render", body)`)?
- **Mutation hosts.** Is `p.onUpdate` after every change enough, or should
  `p.updater(deps, code)` be exposed from the start?
- **Reactive windows on gtk4.** Unverified. `if details { ui.window … }` may
  need gtk4 to create and destroy a window from a render slot.
- **Headless builds.** `output { go { headless } }` would build the bar alone,
  and the window's missing override is then a build error at the `ui.window`.
  Is that the right answer, or should the program gate it with
  `if PLATFORM == …`?
