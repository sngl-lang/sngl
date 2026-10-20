# Plugins

A new family, language, platform or import scheme is a SNGL package rather than
Go compiled into the binary, and the rules below are what that rests on. The
mechanisms themselves are described where they live: families under
*Stdlib*, targets under *A target is its library package*, `gen.emit` and
`gen.scheme` under `sngl:x/gen` and *Import schemes*, commands beside them, and
*Trust*.

- **A platform is a host plus the families it renders.** The host owns the
  process -- `main`, the Model, the event loop, the drawing thread; a family
  emitter turns a family's subtrees into code inside it. A new use is usually
  a family, not a platform: an i3 bar is a family a program builds for gtk4 or
  fyne, getting the bar and its windows in one process
  (`example/i3blocks/`). Two code-generating platforms in one process is out
  of scope until someone asks.
- **Dispatch is the override mechanism.** A family's override is its emitter
  and a member's override is what that member contributes, found in the
  existing order -- platform, language, the declaration's own body, then the
  per-target bodyless error. The compiler walks; an emitter never switches on
  a node's kind. A member with a body composes, so only primitives need an
  override.
- **Walk and runner are separate.** The walk produces data carrying no IR --
  member, position, build-time prop values, children -- and a runner turns it
  into files: a template, SNGL the host compiles (code mode), or a handler
  (process mode). Keep that tree free of IR pointers as it grows; it is what a
  process would be handed.
- **An emitter that reads state writes SNGL**, not host code: the host is
  rewritten into a members function and the family's `gen.emit`, two effects
  every host already compiles. A direct API writing host code through the
  language's translator, for output no SNGL construct expresses, is the door
  left open; measure the interpreter on a large host before building on it.
- **Every read a handler makes is recorded**, through `sngl:x/gen`, so a
  plugin's output is a store entry with no bookkeeping by its author, and a
  result depends only on what was recorded -- there is no unrecorded way to
  read. `gen.exec` is the exception in what it can see (the process's own
  reads are invisible), which is why it is never allowed unasked.
- **Handlers run in the interpreter**, in process: an import resolves during
  the check, which is every LSP keystroke, and the playground cannot exec a
  toolchain. The cost is that the interpreter's correctness now matters for
  output, not only for tests.
- **A plugin's code is part of its key**: a SNGL producer adds the digest of
  its package closure to the store key (`Request.Identity`), or editing a
  plugin would replay what the old one produced.
- **A command is the user's terminal, not a build input**: `gen.run` and
  `gen.build` handlers run processes through `gen.shell`, which records
  nothing; test, snapshot and preview are still Go interfaces
  (`codegen.TestRunner`, `Snapshotter`, `PreviewStyler`) and are the next to
  become commands.

Known gaps, each refused or documented where it bites:

- A generated family's host under a `for` is refused, and so are a composed
  member's handlers and a member holding children in code mode.
- Emitter layers of `@run` (a family adding its own process start) do not
  exist; `none` and bubbletea do not declare `@run`.
- html route mode under `--lang go`: `sngl run` serves the static files
  rather than running the server.
- bubbletea splices a stateful component in a page, or a window under an `if`,
  into the Model, so its state survives the page being left.
- A handle read above its node has what the node's props bind, not what its
  children bind: a `go` above a stack whose pages carry a meta does not check
  (`preSpecializeHandles` reads props alone).
- `sngl dump` prints a qualified node by its local name, so a dump of a
  program writing `nav.page` does not check again.
- A component's props, events and slots keep their names whatever a target
  reserves (`passReservedNames` renames declarations, and a call site binds
  these by name), so a prop named `range` is still a Go parameter no target
  can declare.
- A local that shares a package var's name reads the var on the Go mutation
  targets wherever the scope declaring it does not bind it first: codegen
  resolves an identifier by name, and `ExprCtx.Resolve` finds the Model field
  first. A function and a lambda bind every local of their body
  (`codegen.BodyLocals`, `cmd/sngl/testdata/func_local_named_like_state_runs.txt`),
  and a read of the state itself is told apart by its symbol
  (`ExprCtx.ShadowedStateVar`).
- `gir:` reads only `Gtk-4.0`.
