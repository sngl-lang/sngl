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

- **Hosts beyond the file root, and windows that come and go** (Phase C,
  decision 5). A window is an OS window of its own on gtk4 and fyne, each
  openable by `#id`, around content built once (`codegen.HostWindows`); a
  window under an `if` that reads state is created and destroyed with it
  (`passWindowLifetimes`, lowered to an effect, `#[gen.can(windowLifetimes)]`,
  refused elsewhere); `@close` is the window manager's close, with the tree
  deciding what exists. A generated host is read under a root `if` -- its
  gen.emit's effects mount and unmount with the branch, which passEffect
  already gave -- and in a root component. `example/i3blocks/` toggles its
  window with `if details` and is built and run by
  `cmd/sngl/testdata/example_i3blocks.txt`. Fixtures: `testdata/window_several.txtar`,
  `testdata/window_under_if.txtar`, `testdata/emit_family_under_if.txtar`,
  and a `_runs.txt` script for each.

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

*Landed, but for `for`.* The walk reads a host at the root of a file, under an
`if` there, and in a component whose family is `root`. A code-mode host under
an `if` is rewritten in place, so its effects are the branch's; a template-mode
one needs the `if` decidable at build time. A host under a `for` is refused:
each copy would be an emitter of its own, and the members function is a
function the loop's variables do not reach -- the effect under the loop would
read them from a handler, which passEffect refuses.

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
- ~~Whether `output(entry=…)` keeps only its routing meaning~~: it goes,
  with decision 15. A desktop `run()` presents every window the tree holds.

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

### 15. A window is a surface; a navigator holds the pages

*Settled, not built.* `window` has meant two things. On html a window is a
**destination** -- a page with an `href`, `params` and a route, one showing at
a time -- and on a desktop it is a **surface**, a toplevel the user can close.
No target treated it as the second: html wrote a document per window, fyne
switched between them inside one OS window, and gtk4, android and bubbletea
took `wins[0]` and dropped the rest without a word.

So the two are split. `ui.window` is the surface -- `title`, `favicon`,
`@closed`, `open`/`close`, created and destroyed by a reactive `if` -- and
**`sngl:ui/nav`** holds the destinations:

```sngl
import nav "sngl:ui/nav"

ui.window #main(title="Docs - {pages.current.title}") {
    ui.vbox {
        nav.link(to=about, text="About")
        nav.stack #pages {
            nav.page #home(href="/") { … }
            nav.page #about(href="/about", title="About") { … }
            nav.page #pkg(href=`/p/{name}`, params=P{}) {
                component content(p) { … }
            }
        }
    }
}
// in a handler: pages.go(about), pages.go(pkg, P{name="x"}), pages.back()
```

The package, as settled:

```sngl
component _page<M = struct {}>(href string, title string, meta M) build.family

component stack<M = struct {}>(pages ...component _page<M>) ui.node
func stack<M>.current() _page<M>                          // read as pages.current
func stack<M>.go<T>(to page<T, M>, params option<T> = null)  // none: the page's own
func stack<M>.back()                                      // pop; nothing at the bottom

component page<T = struct {}, M = struct {}>(
    href string,
    title string,
    meta M = M{},
    params T = T{},                     // where it starts
    content ...component(v T) ui.node,  // handed the params it is showing
) _page<M>

component link<T = struct {}, M = struct {}>(to page<T, M>,
    params option<T> = null, text string, style ui.Style) ui.node
```

Which page shows is state, and where it lives is the platform's: the URL and
the browser's history on html, the back stack the system button pops on
android, a Model field on a desktop, nothing at all on a static site, whose
pages are separate documents. So the navigator's state is the host's and is
reached through its handle, as a window's visibility is.

| target      | `nav.stack` is                                 | `link` / `go`                              |
|-------------|------------------------------------------------|--------------------------------------------|
| html static | one document per page; the window is the shell | `<a href>`                                 |
| html route  | a route per page (`HTTPRoute`s keyed by page)  | `<a href>`                                 |
| gtk4        | `GtkStack`                                     | `set_visible_child`                        |
| fyne        | fyne's window switcher, moved into the content | show/hide                                  |
| android     | Compose `NavHost`                              | `navController.navigate`; system back pops |
| bubbletea   | a current-page field                           | set it                                     |

The nav chrome is written once, in the window, rather than once per page, and
a host that can swap a page in place (a partial page load, a native stack)
swaps only the page.

Settled:

- **A stack first.** html and android are stacks natively; a switch is a stack
  nobody pushes onto. Navigators are a family (`tabs`, `split` later) and a
  page is its member.
- **The current page is readable**: `pages.current == about` is a reactive
  read through the handle, the way a bound prop's value is.
- **`link` and `go` name a page by its `#id`**, so a misspelled destination is
  an unresolved name, not a dead link.
- **`window` loses `href` and `params`**, to `page` -- `checkWindowPathParams`
  and the route cell in `NodeInst.Params` with them -- and **`output(entry=…)`
  goes**: a stack starts at the page at `/`, or its first. Migrated in one
  commit; a shorthand would keep the conflation alive.
- **A second window on html is an in-page modal** -- an html5 `<dialog>` with
  default CSS -- opened and closed as a desktop window is.
- **The `md:` site's `layout` becomes the window and its chrome**, and `site`
  one `nav.page` per file in a stack, where today the layout has to *be* a
  window per page.

Settled in C2's planning:

- **`nav.navigator` is the family** (withdrawn for `_page<M>`, last
  bullet), `nav.stack` the first member that holds
  pages and `nav.page` the member of it. `ui.link(href=)` stays as the
  hyperlink; `nav.link(to=)` names a page.
- **A handle read as a value is its node**: a constant record of the node's
  constant props plus its identity, which `==` compares. That is what
  `pages.current == about` and `pages.current.title` read, and it is general
  -- `windowStructValue` is the case of it that exists. A handle is typed by
  its call site's specialization, so `pages.go(pkg, 3)` is refused against
  `pkg`'s `T`.
- **A page's params are state of the page** (withdrawn for a one-way
  prop and the content's population, last bullet): `:params` is a two-way prop, so
  unbound it is a cell starting at what the call site wrote, and `go` writes
  it. `content` is handed its current value. `go` takes the struct, the zero
  value when it is not passed, and `nav.link` takes `params=` for the same.
- **A page that is not current is not mounted**, as under an `if`.
- **A page's title contributes nothing on its own.** The stack's `current` is
  reactive, so a window writes `title="App - {pages.current.title}"`, which
  on html static folds per document.
- **The first window on html is the document**: its `visible` and `@closed`
  are accepted and do nothing. A second window is a `<dialog>` opened with
  `show()`, non-modal as a desktop window is; `visible` is `show()`/`close()`
  and `@closed` its `close` event.
- **The md site is a `nav.stack`**: the package's generated component
  renders one `nav.page` per file, and the program's window is the chrome.
- **`current` is typed by the family, which declares the props its members
  share** (the family is now `_page<M>` and `current` a computed, last
  bullet). A family may declare props; every member declares each of them,
  by name and type. A handle to a member is assignable to its family's type,
  one way only, and a select through a family-typed value reaches only the
  family's props. So no type parameter escapes and no value is polymorphic:
  a family value is one record on every target, identity plus those props.

  ```sngl
  component navigator(href string, title string) build.family
  component page<T = struct {}>(href string, title string, :params T = T{},
      content ...component(v T) ui.node) navigator
  component stack(:current navigator, pages ...component navigator) ui.node
  ```

  `pages.current.title` and `pages.current == about` read it;
  `pages.current.params` is an error, `pkg.params` is `Pkg`, and
  `pages.go(pages.current)` is refused, since `go` takes a `page<T>`.
  Settled while building it: every family has values, a propless one's being
  identity alone, and naming the family itself as an expression stays an
  error; a family prop may carry a constant default, which a member omitting
  the prop holds and no call site sets; a two-way or const prop, an event, a
  slot or a type parameter on a family is refused; and a bare generic
  component type is its defaults, as a struct type is.
- **Navigation is a lowering, not SNGL bodies.** `passNavigation` turns a
  stack into plain UI, gated by `#[gen.can(navigation)]`: html and android
  declare it and answer the nodes in their own codegen (a document or route
  per page; a Compose `NavHost`), and every other target gets the lowering,
  since a capability not written is not held. The lowering, per stack:
  `current` is a var of a synthesized record of the navigator's props plus
  an `id` (the `/` page, or the first); each page with params gets a var
  holding them; the history is a list of a synthesized entry struct holding
  the page replaced and one field per page's params; the pages become an
  `if`/`else if` chain on `current.id`, so a page not showing is not
  mounted; `go` pushes, writes the params and `current`; `back` pops and
  restores both; `nav.link` becomes the target's clickable around its
  content firing the same `go`. A read of `current` is a read of the var,
  and `== about` compares ids. The interpreter stays the reference
  (`internal/interp/nav.go`, `none`'s primitives), running the checked IR.

  Writing the package in SNGL was planned first and dropped. It needed
  `this` as a value, a slot read as its members, family methods, a family's
  zero and `effect`'s `@create`/`@destroy` -- and then ran into membership:
  `stack` is a `ui.node` whose body inserts navigator members, and `page` a
  navigator whose body renders widgets. What that wants is a contract on
  members (an interface) rather than a family, which is a language design
  of its own and is not needed for navigation. `this` and the family zero
  were built on the interpreter (19823435) and reverted.
  The family-typed `current` was chosen over an `interface` type (more
  language than the problem needed then), inheritance from a non-generic base (a second
  statement of what a component is, beside its return position), and a
  non-generic page with its params in a node of their own (which undoes the
  params being the page's).
- **Settled while building the lowering.** `nav.link` shows `text`, as
  `ui.link` does, and takes no content: no Go target's button renders any,
  so "the target's clickable around its content" had nothing to be.
  `stack`, `page` and `link` are builtin node kinds (`navStack`, `navPage`,
  `navLink`), as `window` is, which exempts them from the bodyless-library
  rule and is how the pass finds them. The pass runs before ImplicitState.
  A bound `:params=x` makes `x` the cell; a bound `:current` is routed the
  same way but cannot be written yet, since there is no family zero to start
  a `navigator` var at.
- **Settled before android: navigation's state is navigation's.** Which page
  a stack shows is internal to it, and `current` is a computed of the stack
  (`stack<M>.current()`, read with the parentheses elided), so nothing sets
  or binds it. A page's `params` is a one-way prop: where the page starts,
  and what a `go` or a `link` naming none shows it with -- the argument is
  `params option<T> = null`, since a default cannot name `to`'s. The params
  a page is showing reach its content's population and nothing else, and
  `pkg.params` reads what the call site wrote. That withdraws `:current` and
  `:params` and their bindings.

  The family is `_page<M>`, unexported, and `page` is its only member: no
  custom page types, and no `tabs` or `split` in it, which withdraws
  "navigators are a family" above. It exists so `pages.current` has a type,
  so a program cannot name it and a func cannot take one. `meta M` is what a
  program says about every page of a stack -- a menu's label -- with one
  type per stack, bound from the pages written in it; it is what iterating
  the pages (`stack.all()`, later, for the markup conversion) will read.
  That needed families to take type parameters, a member binding them in
  its return position (`page<T, M>` is a `_page<M>`), and a generic slot's
  family arguments bound from its children. `go` and `link` take a
  `page<T, M>` and convert it internally, so `pages.go(pages.current)` stays
  refused.

### 16. A window is a component; the package body is the application's view

*Settled, not built.* The compiler knows a window by `#[builtin("window")]`
and answers it in about 130 places (`IsWindowNode`, `AllWindows`,
`pkg.Windows`), and Phase C added 46 more. None of it is needed. What a window
needs, each has a general answer:

| a window needs                                 | general answer                                           |
|------------------------------------------------|----------------------------------------------------------|
| to stand where the root family goes            | the `root` family, already                               |
| a host object that lives a while               | the node ops: `CreateNode`, `AppendChild`, `RemoveChild` |
| its children in a container it owns            | the platform primitive's own codegen (below)             |
| to survive a change inside it under an `if`    | a slot body that patches in place (below)                |
| `@closed`, `visible`                           | an event and a prop of the platform's primitive          |
| `@error` as the outermost boundary             | a `boundary` in the platform override's body             |
| `href`, `params`, `entry`, a document per page | `nav.page` (decision 15)                                 |

**The package body is a view whose parent is the application.** Every node at
the root of a file is `AppendChild(app, node)`, and what attaching to the
application means is the platform's translator's answer: a toplevel presented
on gtk4 and fyne, a document or a `<dialog>` on html, a bar's output for a
generated family. This is the answer the language already gives for every
other node, and it is why a code-mode host may stand at the root at all. So
`ui.window` becomes an ordinary component of `sngl:ui`, and each platform
implements it with a primitive it declares:

```sngl
// codegen/platform/gtk4/gtk4.sngl
#[intrinsic("gtk4:Toplevel")]
component Toplevel(title string, :visible bool, @closed(), content ...component ui.node) root

component ui.window[platform] {
    Toplevel(title=title, :visible=visible, @closed { closed() }) { content(params) }
}
```

A window under `if details` is then a node in a render slot like any other:
created with it, destroyed with it -- widgets, effects and all. Nothing about
it survives the condition turning false, which is where Phase C's runtime was
wrong: it kept the content tree alive across an unmount. Hiding without
destroying is `visible`.

*Later, per platform:* a mark on a primitive saying its children begin a
render tree of their own (`#[gen.renders(surface)]` beside the existing
`identity`). A platform that wants the compiler to know uses it; none has to.

**A slot body patches in place.** A render slot rebuilds its whole body when
anything its body reads changes, so a window under `if details` whose label
reads `uptime` would be destroyed and recreated every tick -- and so is any
large subtree under an `if` today, which is the general form of the problem
Phase C worked around with a window-only lowering. The slot re-renders when
what its *structure* reads changes (the `if`'s condition, the `for`'s
iterable); a prop inside the body is an updater like one outside it, applied
to the nodes the live render holds.

**`visible` is a two-way prop, and `open`/`close` assign it.** A window
manager's close is a change of visibility the host reports, the way a
checkbox reports a click: `ui.window(:visible=shown)` writes `shown = false`
back, and `@closed` fires as well (built as `@closed`: `@close` clashed
with the `close` method). `details.open()` and `details.close()` stay
as the imperative spelling and write the same state (decision 17 says where
that state is when nothing is bound). The program ends when a close leaves no
window visible, unless it started with none.

### 17. An unbound two-way prop is state of its own

*Built* (Phase C1 step 2). A `:prop` a caller left unbound had no cell: a
`ui.checkbox` nobody binds reported clicks into nothing, and an unbound
`:visible` could not be closed without the tree and the host disagreeing.
Instead **an unbound two-way prop is implicit state of the instance**,
initialised from the prop's default and written by the host's reports, as
if the component declared a `var` for it. **A one-way value on a two-way prop
leaves it unbound**: `checkbox(checked=done)` is a cell that starts at `done`,
and a later write to `done` does not reach the box -- `:checked=done` is the
spelling for one that should. Three consequences:

- **It makes the component impure**: an instance with implicit state keeps
  state, so the inliner treats it as it treats a component with a `var`
  (per-instance, a record, `remember`).
- **`const` requires the binding.** A `const` component or a `const` prop
  cannot keep state of its own, so leaving a `:prop` of one unbound is an
  error at the call site rather than implicit state.
- **The state is reachable through a `#ref`**: `details.visible` reads it and
  `details.open()` writes it, which is what `open` and `close` are.

This fixes the unbound inputs as a class rather than per widget: every
`:prop` in `sngl:ui` gets a cell whether or not the caller supplies one.

As built, `passImplicitState` wraps each such node in a component written for
its call site whose `var` is the cell, so the inliner's existing handling of a
component with state is the whole implementation; the interpreter keeps the
cell on the prop's symbol in the instance's env. A `#ref` read of the cell
from outside a component built at run time -- a checkbox under a reactive
`if`, read beside it -- is refused with a position, since the read cannot say
which instance it means; so is a call site binding one two-way prop and not
another, and one populating a named slot, which no `sngl:ui` component reaches.

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

### Phase B: `@run` -- the program's layer done

Decision 6 on gtk4 and fyne, with `example/i3blocks/` opening its window only
under `--window`. Left: emitter layers, the interpreter, and bubbletea.

### Phase C: hosts beyond the file root, and reactive windows -- done, in part superseded

Decision 5, several OS windows and a window under a reactive root `if` on gtk4
and fyne, and `@close`. The window half is interim: Phase C1 replaces its
window-only machinery -- `passWindowLifetimes`, `NodeInst.Presence`,
`window.mount`/`window.unmount`, `codegen.HostWindows`, `ir.WindowRootName`,
gtk4rt's `Window` record and fyne's `snglWindow`, and the kept-alive content
tree -- with decision 16. Its fixtures describe behaviour that survives and are
rewritten rather than deleted, except where they assume content outlives an
unmount. The host half (decision 5) stays. Left, each found on the way:

- **A window under a `for`** keeps one record per window *statement*, which a
  copy per element is more than; it is not a lifetime and not refused.
- **A host under a `for`**, above.
- **A top-level render slot renders before its window's static children** are
  appended to the window's box, so its content lands first. It did in the
  single-window output before this phase too.
- **A window's title is read once**, when its record is made.

Settled for it: a window the window manager closes runs its `@close`, and
the tree decides whether the window still exists. With no `@close` the close
hides it -- it stays in the tree and `open` brings it back -- and the program
ends when a close leaves no window on screen, unless it started with none. Both
hosts can create and destroy a toplevel from their loop at any time (probed:
gtk4 needs `g_application_hold` to live with none, fyne a window it never
shows, since its driver quits when the last one goes).

### Phase C1: the window as a component -- done

Decisions 16 and 17, in order, each landing with its fixtures:

1. **A slot body patches in place** -- *done.* The slot re-renders on what its
   structure reads; each top-level node of its body that reads state is
   lifted into a component built at run time, and a write hands the live
   instances their new value (`liftSlotBodies`, `liveUpdaters`).
   `testdata/slot_body_updates_in_place.txtar` and its `_runs.txt`. No test
   surface yet reads a widget's identity, so the script shows the patch
   landing and the golden shows the slot not re-firing; an unbound input
   inside the panel (step 2) is the first thing a test could see survive.
2. **An unbound two-way prop is state** (decision 17) -- *done.* Implicit
   per-instance state, impurity, the `const` refusal and the `#ref` read
   (`passImplicitState`, `refuseUnboundConstProps`).
   `testdata/unbound_prop_state.txtar`, its `_runs.txt`, and
   `testdata/error_unbound_prop_const.sngl`. The call sites whose one-way value
   the program writes elsewhere were moved to `:prop=`. A test now reaches a
   `#id` inside a component built at run time on html, fyne and gtk4, so the
   slot script types into an unbound input in the panel and snapshots it
   after a tick: the text is there only if the panel was patched.
3. **The package body's parent is the application** -- *done.* A root node
   is `AppendChild(__app, node)` (`ir.AppParent`), and a slot there is handed
   the application; gtk4rt's and fynelayout's box calls answer for it. gtk4
   and fyne declare `Toplevel` and override `ui.window` with it, which
   `composeOverriddenBuiltins` makes an ordinary component on those two
   targets. `visible` is a two-way prop the host reports a close through,
   `@closed` (renamed from `@close`, which clashed with the method) fires
   after it, and `open`/`close` are bodied methods assigning `visible`,
   reached through the `#id` by `repointHandleCalls`. A window under
   `if details` is a node in a render slot, destroyed with its content and
   effects (`destroyBuiltInstances`). Phase C's machinery is gone:
   `passWindowLifetimes` (its refusal survives as `passWindowUnderIf` for the
   targets that still build a builtin window, which after step 4 is html), `NodeInst.Presence`,
   `window.mount`/`unmount`, `codegen.HostWindows`, `ir.WindowRootName`,
   gtk4rt's `Window`, fyne's `snglWindow` and the kept-alive content.
   `testdata/window_visible.txtar` and `cmd/sngl/testdata/window_visible_runs.txt`
   are new; the three window goldens and `window_under_if_runs.txt` are
   rewritten, the last asserting a counter in the window starts again at zero.
4. **html, bubbletea and android answer the window** -- *done.* bubbletea
   and android override `ui.window` with a `Screen` primitive,
   `Screen(visible=visible, …) { content(params) }`, so the window is a
   component on both: the view is the package body, a hidden window draws
   nothing while its content stays mounted, a window under `if details` is an
   ordinary conditional, and
   `open`/`close`/`:visible` work. The close is ctrl+c and the system back,
   reported as `visible = false` before `@closed`, and the program ends when
   the window is off screen after it. They show one window, and a second is
   refused at the second (`codegen.SoleScreen`) where it used to be dropped.
   html keeps the builtin through C2 -- a document or a route, which
   `optimize.Documents` and route mode read as windows, byte-identical -- and
   refuses what a page cannot answer: `:visible`, a `visible` other than the
   default and `@closed` (`passWindowSurface`), beside the `if` and the method
   call it refused already. `testdata/window_screen.txtar`,
   `testdata/window_under_if_screen.txtar`,
   `codegen/platform/bubbletea/window_close_run_test.go`,
   `cmd/sngl/testdata/window_second_refused.txt` and
   `window_visible_refused.txt`. Left: on bubbletea, which keeps no state of an
   instance's own, the `visible` cell of a window under `if details` survives
   the condition turning false. The html `<dialog>` for a second window is
   Phase C2's.

### Phase C2: navigators

Decision 15: `sngl:ui/nav` with `stack`, `page` and `link`, every target's
answer in the table there, the html `<dialog>` for a second window, and the
migration of every `window(href=…)` in one commit. In commits, each with its
fixtures written first:

1. **bubbletea asks a timer's gate as an expression** -- *done.* Every
   timer is a schedule synced after each Update, keyed `""` under no loop, so
   a timer under an `if` stops with it and starts again
   (`bubbletea/timer_gate_run_test.go`). Predates C1, and a page is the next
   thing a timer would be under.
2. **`sngl:ui/nav` and the handle as a value** -- *done on the interpreter.*
   The declarations, a handle typed by its specialization, `T{}` defaults, a
   two-way prop that is never required, a window's props reading its body's
   handles, and `none`'s `Stack`/`Page`/`Link` answered by the interpreter,
   which now drops an unmounted instance's state (`testdata/nav_stack.sngl`
   and six fixtures for the checker rules). `current` is typed by the family,
   which declares the props its members share (decision 15); the bare-type
   leniency it replaced is gone from `ir.Type.Equal`
   (`testdata/family_props.sngl`, `error_family_member_props.sngl`). The
   surface was revised before step 4 (decision 15's last bullet:
   `pages.current` a computed, `params` one-way, the family `_page<M>`):
   `testdata/nav_stack.sngl`, `family_generic.sngl`,
   `error_nav_surface.sngl`.
3. **`passNavigation`** and the `navigation` capability in `sngl:x/gen` --
   *done.* `testdata/nav_stack_lowered.txtar` on gtk4, fyne and bubbletea,
   `cmd/sngl/testdata/nav_stack_runs.txt` running nav_stack's program there
   and on the interpreter, and `nav_lowering_refused.txt` for a stack under a
   `for` and a `go` through a value. Still open: bubbletea splices a
   stateful component in a page into the Model, so its state survives the
   page being left, as the `visible` cell survives `if details`; a page
   handle read above its `nav.page` does not check, `specializeHandle`
   running only when the node is; and no Go test emitter reaches a node's
   prop or a nested component, so the script tests a narrower surface than
   `nav_stack.sngl`.
4. **android** declares `navigation`: a `NavHost`, system back pops; the
   `navigation-compose` dependency joins the scaffold. Fixes the lost
   `safeDrawingPadding` of a window whose content arrives through a slot.
5. **html declares `navigation` and answers a page** by a mark on its primitive rather than the
   builtin: one document per marked node, the whole tree with every other
   one pruned, so the window is the shell; one route per marked node in route
   mode, the params cell its population's. `window` still has `href`.
6. **The migration**: `window` loses `href` and `params`, `output(entry=…)`
   goes, every call site, the md site, `website.sngl` and `docbrowser` move.
   Every html golden that was a window per page is byte-identical as a stack of
   pages.
7. **The `<dialog>`** for a second window on html, and `passWindowSurface`'s
   refusals become meanings.

### Phase C3: no window in the compiler

With routing on `nav.page` (C2) and a window a component (C1), delete what is
left: `ir.Window`, `pkg.Windows`, `ir.AllWindows`, `IsWindowNode`,
`WindowsFlat`, `#[builtin("window")]`, the checker's `windowShell` and
`checkWindow`, window arms in the interpreter and every platform. What html
needs of a page is `nav.page`'s. Success is `grep -rn IsWindowNode` empty.

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
