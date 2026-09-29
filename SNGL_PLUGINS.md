# SNGL plugins: families, targets and import schemes written in SNGL

This is the plan for the compiler's extensibility system: a new family of
nodes, a new language, a new platform or a new import scheme is a SNGL package
a program imports, not Go compiled into the `sngl` binary. It covers the uses
beyond user interfaces as well: SNGL's reactivity and declarative trees driving
something that is not a window, such as an i3/sway status bar whose click
opens a GTK window in the same process.

```sngl
import build "sngl:build"
import gen "sngl:x/gen"
import go "sngl:language/go"

// A family, and the members that join it.
component block build.family
component text(name string, full string, @click(button int)) block
component bar(blocks ...component block) root

// How the family is generated for Go. A member says what it writes in its own
// override, so the walk is the compiler's and nothing switches on node kinds.
component block[go.language] {
    gen.emit(file="blocks.txt", open="[\n", close="]\n")
}
component text[go.language] {
    gen.node(open="  {name}: {full}\n")
}
```

That much works today, for build-time content and for content that reads
state (see *Done*). The remainder is emitter layers of `@run`, hosts beyond the
file root and reactive windows, then the import schemes, the trust model and the commands.

`example/i3blocks/` is the program the work is steered by. It compiles and runs
on gtk4 and fyne; its README lists what it still works around.

## Done

On `feat/family-components`:

- **An event binds a parameter list** (!176) and **`root` is in
  `sngl:builtin`** (!177) -- the two prerequisites the first version of this
  plan named.
- **A family is a component.** `component shape build.family` declares one;
  `sngl:build`'s `family` is the family of families, its own member by
  `#[marks.builtin("treeFamily")]`. `#[tree.kind]` is gone. `Component.Tree`
  is the family's `*ir.Component` (`IsFamily`, `ir.TypeFamily`). A family
  takes no parameters, has no body, is exempt from the bodyless rule and is
  refused in a tree. `familiesFirst` registers a package's components after
  the same-package ones their return position and slot types name, so a family
  may sit below its members.
- **A target is its build-tree node.** A platform package declares
  `#[gen.name("html")] component platform(…) build.platform`, a language
  package `component language(…) build.language`. `[platform]`,
  `[html.platform]` and `PLATFORM == html.platform` name the node; read as a
  value it has sngl:builtin's `platform` type and folds to its name. The
  generated identity const and `#[macro.identity]` are gone. `#[gen.name]` is
  required on a build node and refused elsewhere, unique within a tier, and a
  target package's node carries its package's name (`reportTargetNames`).
- **A family overridden with a `gen.emit` is generated** (`internal/build/emit.go`).
  The build walks each host at the root of a file -- a bodyless component whose
  rest slot takes the family (`ir.EmittedFamily`) -- before the first
  optimize, into plain data (`emittedNode`); a bodyless member contributes
  through its `gen.node` override, a bodied member composes, `if`/`for` are
  decided, a read of state is refused. A runner writes the file and the host is
  removed from the package. `gen.emit`/`gen.node` are refused anywhere else
  (`reportEmitterPlacement`). Fixtures: `testdata/emit_family.txtar`,
  `cmd/sngl/testdata/emit_family_refused.txt`,
  `testdata/error_emitter_placement.sngl`.
- **Content that reads state is generated as SNGL** (Phase A, decision 4). A
  `gen.emit` giving `render` is code mode: the walk keeps `if`/`for` and hands
  a runner a tree of data plus handles into a table of expressions and handlers
  (`internal/build/emitcode.go`). The SNGL runner writes one function returning
  each member's `gen.node(value=…)` -- props bound, events a call of the
  call-site handler -- and puts the family's `gen.emit`, now an ordinary bodied
  component of two effects, where the host stood. Six host gaps closed on the
  way: package-root effects never mounted on fyne/gtk4, lambdas in struct/map
  literals and returns and called in place missed by `WalkLowered`, a free
  func spelled as a method value, calls through a substituted func-typed prop
  invisible to the call graph (`passDirectCalls`), a directory package's
  function held only as a value never emitted, and an override in another file
  of its package checked against the declaration's file's imports. Fixtures:
  `testdata/emit_family_state.txtar`, `cmd/sngl/testdata/emit_family_state_runs.txt`
  (runs the bar on gtk4 and fyne under the headless compositor), and one per
  gap.

## Decisions

Settled unless marked *open*.

### 1. A platform is a host plus the families it renders

A `PlatformGenerator` does two jobs, and the plan separates them. The **host**
owns the process: `main`, the Model, the event loop, the drawing thread.
gtk4, fyne, bubbletea and a headless Go loop are hosts. A **family emitter**
turns the lowered IR of the family's subtrees into code inside that host. gtk4
is a host plus the `ui.node`, `draw.shape` and `markup.span` emitters that
live in its Go generator today.

So a new use is usually a family and not a platform. i3bar is a family whose
Go emitter writes a render function, a click reader and a `main` layer, and a
program using it builds for gtk4 or fyne and gets the bar and its windows in
one process. Two code-generating platforms in one process (fyne and gtk4
together) is the case that would need real composition, and is out of scope
until someone asks for it.

### 2. Dispatch is the override mechanism

A family's override is the family's emitter, and a member's override is what
that member contributes: the order is the existing one (platform, then
language, then the declaration's own body, then the per-target bodyless error
at the position the program reached). The compiler walks; an emitter never
switches on a node's kind, which SNGL has no polymorphism to express anyway.
A member with a body composes, so only primitives need an override.

### 3. Walk and runner are separate

The walk produces data that carries no IR -- member, position, build-time prop
values, children -- and a runner turns it into files. Today's runner evaluates
`gen.node` templates in the interpreter. A runner that execs another process
takes the same tree, serialized, and returns files; it goes through the host
API and trust gates of decisions 7 and 11. Keep the tree free of IR pointers
when it grows.

### 4. An emitter that reads state writes code the host compiles

*Landed, as SNGL.* Content that reads state cannot be decided at build time,
so the generator writes code the host runs. The first generator writes
**SNGL**, not host code: the host is rewritten before the first optimize into a
function returning the members' values and an instance of the family's
`gen.emit`, whose body is two effects. That was chosen over the `gen.Program`
API below because every host already compiles effects, lambdas and
`async.post`, so no host grew a hook; the Go-only parts of a family are
`#[go.native]` declarations in the family's own package.

```sngl
component block[go.language] {
    gen.emit(
        render=encode,                       // func(list<T>) string: pure, the key
        @start(members) { … },               // once; members() reads them again later
        @change(line) { print(line) },       // each time render's answer differs
    )
}
component text[go.language] {
    gen.node(value=Entry{full=full, click=click})   // an event read as a value is its handler
}
```

**Several generators, one walk.** The walk hands a runner data and handles
rather than IR, so the SNGL runner is one of several. Still open, and the door
is kept open for both:

- **A direct API**, the `gen.Program` below: a handler on a gen node, run in the
  interpreter, writing host code through the language's own translator. For
  output no SNGL construct expresses. Its interpreter cost has to be measured
  on a large host before it is built on (decision 9); the SNGL runner does not
  run the interpreter at all, so Phase A did not measure it.
- **An external process**, handed the walked tree serialized -- likely as
  protobuf -- and returning files or SNGL (decision 3, Phase F).

| call                                                                   | backed by today                                                                                                          |
|------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------|
| `p.expr(e)`, `p.text(e)`, `p.ifHead(s)`, `p.forHead(s)`, `p.lambda(h)` | the language's `FileEmitter.EvalExpr`/`EvalStmt`, `ForHead`, the handler lowering                                        |
| `p.file(name)`, `f.line`/`open`/`close`/`require`                      | `Sink`, `FileEmitter.RequireImport`                                                                                      |
| `p.field(name, type)`, `p.method(name, body)`                          | Model fields (`CodegenCtx.ModelState`); the host owns the receiver, so the emitter never writes `func (m *Model)` itself |
| `p.post(code)`                                                         | the host's hand-off to its loop thread (`async.post`) followed by its updates                                            |
| `p.onUpdate(code)`                                                     | render-model: run after every state change                                                                               |
| `p.updater(deps, code)`                                                | mutation-model: `CommonAnalysis` deps -- *open* whether it is needed from the start                                      |
| `p.main(before, after)`                                                | a layer of the `@run` chain (decision 6)                                                                                 |
| `p.error(pos, msg)`                                                    | a positioned build error                                                                                                 |

**Per-family capabilities** are moot for the SNGL runner -- what it writes is
lowered for the host like the rest of the program -- and stay open for a direct
runner, with the recommendation standing: a family may only withhold
capabilities the host holds.

**What code mode refuses today**, each with a position: a member holding
children, a handler on a composed member, a composed member rendering itself,
and a member whose `gen.node` gives no value.

### 5. Hosts anywhere a root member goes

*Open.* The walk reads hosts at the root of a file and refuses one a
component renders. `bar` is a root member, so the next places are a root-level
`if`/`for` and a component whose family is `root`. A host under a reactive
`if` needs decision 4 first.

### 6. `@run` wraps the process start, on the platform's node

*Landed for the program's layer.* The handler is written on the platform's own
node in the output block, and the event is what says a platform supports it:

```sngl
output {
    go {
        gtk4(@run(args, run) {
            if args.contains("--window") {
                run()               // present the windows and run the loop
            }
        })                          // not called: the loop runs, no window
    }
}
```

It was `@main` at package level in the first version of this plan; it is
`@run` because every platform node already has a `main` prop, and it is on the
node because not every platform can support it -- a page has no process start
to wrap, and android's state lives inside a composable -- so html and android
simply do not declare it. The build makes the handler a function
(`ir.Package.Run`), which answers the owner problem this decision named: as a
function it is lowered like any other, with no new owner kind in `ir.Owners`.

The host builds the tree first, its first settle included, then calls the
handler. Code before `run` runs before the loop and code after it once the loop
has exited; a handler that never calls `run` gets the loop with no window
presented.

*Open:*

- **Emitter layers** (`p.main`), nested inside the program's. A family whose
  generator writes SNGL has no layer to add yet; the i3 header is written by
  `@start`, with the first settle.
- **The interpreter and bubbletea.** `none` does not declare `@run`, so
  `sngl run --lang none` starts as before; bubbletea owns stdio and was left
  out.
- Whether `output(entry=…)` keeps only its routing meaning.

### 7. The host API records its own inputs

A handler reaches the world only through `sngl:x/gen`, and every call that
reads something records it:

| call                     | records                                                       |
|--------------------------|---------------------------------------------------------------|
| `gen.readFile(path)`     | `cache.file`                                                  |
| `gen.exists(path)`       | `cache.file` or `cache.absent`                                |
| `gen.list(dir, pattern)` | `cache.dir`                                                   |
| `gen.env(name)`          | `cache.env` / `cache.unsetenv`                                |
| `gen.exec(cmd, …)`       | the tool (`cache.file` of the resolved binary) and its output |
| another import's output  | `cache.entry`                                                 |
| `out.write(name, src)`   | nothing: it is the output                                     |

So a plugin's output is a gencache entry with no bookkeeping by its author,
and the rule `pure` now states -- a result may depend only on what was
recorded -- holds by construction: there is no unrecorded way to read. This is
the payoff of forbidding run-time reads in !175 rather than tracking them.

Recording is half of it; the other half is whether the call is allowed at
all, which is decision 11. `gen.exec` is the one call that can reach anything:
what it records is the binary and its arguments, and what the process reads is
not visible, which is why it is never allowed unasked.

### 8. Handlers are events with parameter lists

`@generate(out, importPath)` is an event handler binding two parameters, which
needs the first prerequisite. The gen components declare their events with
the parameters their handlers receive:

```sngl
component scheme(name string, @generate(out Out, importPath string)) root {}
```

Parameters are positional, as a func literal's are, and a handler may leave
trailing ones unbound. Adding a parameter to an event is therefore a change
existing handlers survive only at the end of the list -- the cost of
positional binding, taken knowingly because it is what every func in the
language already does.

### 9. Handlers run in the interpreter

The compiler already carries an interpreter (`internal/interp`) that runs
checked IR: `sngl test` on `none` uses it. A plugin's handlers run there, in
process.

Why not compile the plugin to Go and run it like the compile-time evaluator:

- An import is resolved during the check, which is every LSP keystroke.
  Building a Go program there is seconds; the interpreter is not.
- The playground is WASM and cannot exec a toolchain.
- Nothing crosses a process boundary for the generator itself, which is
  #253's first question answered by not asking it.

What it costs: interpreter speed over a large IR, and the interpreter becoming
a compile-time dependency whose correctness matters for output, not just for
tests. Both are measured in Phase A against the docs-site compile.

### 10. A plugin's code is part of its key

The store keys every entry by the compiler's identity because a producer's
code is an input no file can record. For a SNGL producer the code is the
plugin package, not the compiler. The key becomes (request, compiler
identity, **producer identity**), where a SNGL producer's identity is the
digest of the plugin package closure -- its source and the source of what it
imports -- and a Go producer's is empty.

Without it, editing a plugin would replay what the old plugin produced.

### 11. Trust

**The threat model is a malicious repository.** Cloning a project and running
any `sngl` command in it -- `check`, `generate`, `doc`, or opening it in an
editor, which starts the LSP -- must not run a process, read the environment
or read outside the project on the repository's say-so. What the repository
may do unasked is what a SNGL function can do in the interpreter: compute, read
files inside the project, and write its own output.

So the host API of decision 7 is gated, per call and per plugin:

| call                                     | unasked                                             | needs an allow                               |
|------------------------------------------|-----------------------------------------------------|----------------------------------------------|
| `gen.exec`                               | never                                               | `--allow-command`                            |
| `gen.env`                                | never                                               | `--allow-env`                                |
| `gen.readFile`, `gen.list`, `gen.exists` | inside the import root, or the plugin's own package | anywhere else: `--allow-file`, `--allow-dir` |
| `out.write`                              | always; it is the plugin's own output               | --                                           |

Reads are gated as well as commands because a read outside the project is how
a repository would put `~/.ssh/id_ed25519` into a generated file that is then
published: the same threat as a command, with no process involved.

**The import root is the boundary, and it is already defined.** It is the
directory the program's package is read from -- `Config.Dir`, what every
relative import (`import "internal/docui"`) and every `file:` import resolves
against, and what `file:` already refuses to escape. Everything a program can
import by a relative path lies beneath it, so "inside the project" needs no
new notion of a project. Two consequences:

- **Not the working directory.** `sngl generate app/site.sngl` run from a
  parent directory must not widen what a plugin may read to the parent.
- **`--project` is declared and read by nothing.** The root command carries a
  persistent `--project` ("project root directory") that no command consults.
  Either it is deleted in Phase D, or it becomes the one way to name an import
  root other than the package's directory -- and then a grant has to say which
  root it was given under. Recommendation: delete it; nothing needs it yet.
- **No manifest for this.** A `sngl.mod` would be the go.mod reinvented, and
  badly if it existed only to draw this line. If SNGL ever wants a manifest --
  versions, a dependency list -- that is a design of its own, and the trust
  boundary should not have been the reason for it. A plugin fetched through
  `git:` or `http:` lives in the cache rather than under the root, which is
  why its own package directory is readable to it by origin rather than by
  location.

**Allows name the plugin and the narrowest thing it asked for.**

```
--allow-command="<import path>:go list"   # this plugin may run `go list …`
--allow-env="<import path>:GOOS"          # this plugin may read $GOOS
--allow-file="<import path>:/usr/share/gir-1.0/Gtk-4.0.gir"
--allow-dir="<import path>:/usr/share/gir-1.0"   # the directory and below
--allow-all                               # trusted code: everything
```

A command is allowed by **prefix**: `go list` permits `go list -deps …` and
not `go run`. A command that is safe can still be handed an argument that is
not -- `find -exec`, `go list -toolexec`, `go build -overlay` -- so a
`gen.exec` call declares what it needs rather than leaving the prompt to
guess:

```sngl
gen.exec(
    cmd=["go", "list", "-deps", "-json", "--", pkg],
    prefix=["go", "list"],      // default: the command alone
    banFlags=["-exec", "-toolexec", "-overlay", "-modfile"],
)
```

The compiler enforces both at every call: the arguments must begin with the
allowed prefix, and none may be a banned flag in any of its spellings (`-x`,
`--x`, `-x=v`). A well-behaved plugin asks for the least it needs, and the
prompt shows exactly that.

**Where allows live, and who may grant them.**

- **Flags** grant for one invocation.
- **A config file** under the user's config directory
  (`os.UserConfigDir()/sngl/`) records permanent allows. It is written in SNGL
  -- a tree of `trust.allow(…)` nodes -- and is never read from the project: a
  repository shipping its own allow list is the attack. The LSP reads only
  this file and never prompts; a call it refuses is a diagnostic on the import
  that reached the plugin, saying which flag or config line would allow it.
- **The CLI prompts** when a refused call happens at an interactive terminal:
  it names the plugin, shows the prefix and banned flags it declared, and
  offers to allow once or to add the line to the config file. With no
  terminal the call is refused with the same message the LSP gives.
- **`sngl trust`** is the prompt without a prompt: `sngl trust --allow-eval="go:…"` resolves the origin, records its digest and writes the
  config line, and `sngl trust --list` / `--remove` manage what is there. It
  is what a warning or diagnostic points at, since neither can prompt.

**Flags are global, and an environment variable carries the same grants.**
The allow flags are persistent flags on the root command, so every
subcommand takes them -- `sngl lsp` included, for an editor whose server
settings take arguments. In practice an editor launches the LSP with no
arguments anyone thinks to set, which is why the config file is the LSP's
real source and flags are for CI and one-off runs.

An environment variable is easier to reach from both: `SNGL_ALLOW`, holding
the flags' grants in the flags' own spelling, one per line or space-separated:

```
SNGL_ALLOW='eval=go:example.com/docs command=<origin>:go list'
```

Recommended, with two restrictions that follow from what an environment
variable is:

- **No blanket grant through it.** `--allow-all` is a flag only. An exported
  variable is ambient: set once in a shell profile, it applies to every
  repository the user ever enters, which is precisely the repository this
  model is about. A narrow grant names an origin and a content digest, so an
  ambient one still reaches only the code it was given for; a blanket one
  would reach everything.
- **Stripped from every process sngl starts.** `go`, `node`, the evaluator
  and anything a plugin execs run without `SNGL_ALLOW` in their environment,
  so an allowed child cannot hand its grants to a nested `sngl` in some other
  project. (The evaluator links the compiler's registries, so this is not
  hypothetical.)

Where an editor reads its server's arguments or environment from a
workspace file committed to the repository -- `.vscode/settings.json` -- both
come from the repository, and the editor's own workspace trust is the only
guard. The config file does not have that problem, since it is the user's and
keyed by origin. The LSP logs every grant it received from flags or the
environment at startup, so a grant nobody meant to give is visible.

Grants from the config file, the environment and the flags are a union.
Nothing on the command line revokes one.

**An allow binds to where the code came from, not to what it calls itself.**
An import path is a name the repository chooses: a malicious project can put
a local package at `git.example.com/trusted/plugin` through a replace. So an
allow is keyed by the plugin's resolved origin -- embedded in the compiler,
fetched from a URL at a revision, or a directory on disk -- and the config
records the content digest it was granted for, so a changed plugin is asked
about again. The prompt shows the origin, not just the path.

**The library's own plugins are trusted.** A target shipped in the `sngl:`
tree is compiled into the binary the user chose to run, so its calls are
allowed without asking, as a Go target's are today. The gate is for plugins
the project brought.

The go: and js: importers are library-shipped and so trusted, but they run
`go list` and the TypeScript resolver on the repository's files, and `go list` can select a toolchain the project's `go.mod` names. What each runs is
audited against the table above in Phase D.

### 12. The compile-time evaluator is held to the same model

`const func` evaluation already does what decision 11 forbids: `sngl generate` in a cloned repository builds the repository's own Go packages and
runs them, and a js: call runs node over its modules. That is `go generate`,
not `go build` -- running a project's code -- and it happens today with no one
asked. Applying the threat model to it is part of this plan and lands in
Phase D, before any plugin can exec, so there is never a release in which the
new gate exists and the old hole does not close.

**The grant is per evaluated package, and it is total.** A plugin's calls go
through a host API the compiler controls, so they can be allowed one command
or one variable at a time. An evaluated Go function is native code with the
user's privileges: it can open any file, read any variable and start any
process, and nothing between it and the OS asks. So there is nothing finer to
grant than "run this package's code", and the flag says that rather than
pretending otherwise:

```
--allow-eval="go:git.duckfam.us/jonathan/sngl/docs"   # build and run its pure funcs
--allow-eval="js:./lib/format"
```

keyed by origin like every other grant: the main module's package by its
directory and content, a dependency by `module@version`, which `go.sum`
already pins -- so a grant for a dependency survives an edit to the project
and not an upgrade of the dependency.

**Replaying a stored value needs no grant.** A hit in the store runs nothing:
the value was computed by a run that was allowed, and the store lives in the
user's cache directory, which a repository cannot write. So the prompt comes
once, on the first build that evaluates, and a warm build of an allowed
project asks nothing -- which is what the store is for.

**Refused, a call does not fold, and the build says so.** Whether an unfolded
call is fatal is still the target's answer: a target that can call the scheme
at runtime emits the call, and one that cannot fails the build with the flag
that would allow it. The first case is the one that needs care, because the
build *succeeds* with different output than an allowed build writes -- a call
where a literal would have been, and on a static target a page that renders
nothing there -- so it is always a warning:

```
warning: website.sngl:18:20: 12 calls into go:git.duckfam.us/jonathan/sngl/docs were not evaluated: running them needs --allow-eval="go:git.duckfam.us/jonathan/sngl/docs" (or `sngl trust` to record it)
```

One warning per evaluated package rather than per call site, positioned at
the first call, naming the count, the flag and the config route. It is not
suppressed by a store hit elsewhere in the package: a build that folded some
calls from the store and skipped the rest is exactly the build whose output is
half of each. The optimizer has no warning channel today -- only the checker
has one (`c.warn`) -- so Phase D adds one, returned through `build.Emit` and
printed by the CLI with the checker's. The LSP never evaluates -- it optimizes with no project directory,
which already skips the evaluator -- and keeps not doing so.

### 13. Import schemes: `gen.scheme` and two-phase resolution

A scheme is declared at the root of a file,
`gen.scheme(name="example", @generate(out, importPath) { … })`, and its
handler's output is the SNGL package the import resolves to, stored as any
producer's is. The gen nodes of an imported package are *collected, not
rendered*, which is the same gap the emitter pass closes for a host: read them
where they are written and take them out of the package.

Imports resolve eagerly in pass1, in statement order (`registerImport`). A
scheme a plugin defines is therefore visible only to imports written after the
plugin's import, and across the files of one package the order is not defined
at all.

Recommendation: **two phases per package**. First, resolve every import whose
scheme is built in or already known, and collect the gen nodes of each package
that resolves. Then resolve the rest against the schemes collected. Repeat
until nothing new resolves; an import whose scheme is still unknown is an
error naming the scheme. That makes the result independent of the order the
imports were written in, which import aliases already are, since an alias is
bound before any use of it is checked.

Rules that follow:

- **Scope is the package**, not the file. A plugin import says "this package
  can resolve `example:`", and which file carries it should not matter. This
  differs from an alias, which binds a name in one file; a scheme is not a name
  in scope.
- **A scheme may not shadow a built-in one** (`go`, `js`, `file`, `sngl`, …).
  Shadowing a built-in declaration is allowed because a declaration is looked
  up by name in scope; a scheme is a global dispatch key, and silently
  replacing `go:` would change every go: import in the build.
- **A plugin may not use a scheme it defines**, directly or through a cycle of
  plugins. Reported at the import that closes the cycle.

- **A scheme is visible through the whole import graph.** A package that
  imports a library which imports a plugin can use the plugin's scheme; the
  library needing it is reason enough for everything built on the library to
  resolve it too. So the fixed point runs over the graph rather than per
  package, and two plugins anywhere in it declaring one scheme is an error at
  the import that brings in the second, naming both.

### 14. Commands are children of the target node

`sngl run`, `sngl test`, `sngl build`, snapshots and previews are the optional
Go interfaces found by type assertion today (`Runner`, `TestRunner`,
`Builder`, `Snapshotter`, `PreviewStyler`, `HTTPCompiler`). Each becomes a gen
node in the target's build node or family override (`gen.run(@run(dir, args))`, `gen.test(…)`), and a target that writes none does not have the
feature: the capability polarity applied to commands. These exec processes and
their output is not SNGL, so they are the one place a handler's result is not
a stored producer output.

## Phases

Each lands with fixtures written first, confirmed to fail on the tree before
it.

### Phase A: an emitter that writes code -- done

Decision 4, as a SNGL runner. The bar builds for gtk4 and fyne (bubbletea draws
its TUI on the stdio i3bar owns, so it is in no fixture that runs). Left over,
each found on the way:

- A **qualified** function value -- `render=p.shout` from another package --
  emits the namespace into Go (`undefined: p`).
- An effect settles after every write, so a handler writing two cells prints
  two status lines.
- `example/i3blocks/` is compiled by nothing in the suite; a script or test
  building it would keep it from rotting.

### Phase B: `@run` -- the program's layer done

Decision 6 on gtk4 and fyne, with `example/i3blocks/` opening its window only
under `--window`. Left: emitter layers, the interpreter, and bubbletea.

### Phase C: hosts beyond the file root, and reactive windows

Decision 5, and a window under a reactive root `if` on gtk4 and fyne -- the
bar's click opening a window is `if details { ui.window … }`, and a window
`@close` writing the condition back. Unverified today whether those hosts can
create and destroy a window from a render slot.

### Phase D: groundwork for foreign code

Decisions 7, 10, 11 and 12: the host API skeleton recording through
`internal/gencache`, the producer identity in the key, the permission gate
with `--allow-eval` in front of the existing go: and js: evaluation, and an
optimizer warning channel through `build.Emit`. Lands before any plugin can
exec, so there is no release where the new gate exists and the old hole does
not close.

### Phase E: `gen.scheme`

Decision 13, with the order fixtures (plugin imported after its use, in
another file, a cycle, an unknown scheme, a shadowed built-in). First real
user: `c:`'s `pkg-config` in SNGL with `gen.exec`, deleting the Go importer.

### Phase F: process emitters and commands

A runner that execs a process over the walked tree (decision 3), and decision
14's command children, with CLI scripts since they execute.

### Phase G: a built-in target moves

The smallest real one, likely `none`, then html's static mode. Success is its
Go package deleted with its goldens unmoved. html stays Go-backed for good,
through a mark on its node naming the Go generator; which others move is
decided per target.

### Last: delete this file

The branch's final commit deletes `SNGL_PLUGINS.md`. What the work leaves
true belongs in CLAUDE.md, next to the code it describes.

## Rules that bite

- **This file is a build gate**, like PLAN.md: run
  `go tool mdox fmt --soft-wraps SNGL_PLUGINS.md` before committing.
- **A plugin fixture is a golden when it asserts generated output**, and a CLI
  script when it executes something (`sngl run`, `sngl test`).
- **Confirm each new fixture fails when the behaviour is reverted**, and in
  particular that an order-sensitivity fixture fails when the two-phase
  resolution is replaced by statement-order resolution.
