# SNGL plugins: import schemes and build targets written in SNGL

This is the plan for the compiler's extensibility system: a new language, a
new platform, or a new import scheme is a SNGL package a program imports, not
Go compiled into the `sngl` binary.

```sngl
import gen "sngl:x/gen"

// An import scheme: `import "example:widgets"` in a program runs this, and
// what it writes is the SNGL package the import resolves to.
gen.scheme(name="example", @generate(out, importPath) {
    out.write("widgets.sngl", render(importPath))
})

// A build target. The component is the node an `output` block names and the
// schema of its options; the generator is attached to it.
component lua(version string) build.language

gen.language(target=lua, @generate(pkg, out) {
    for var f = pkg.funcs {
        out.write(f.name + ".lua", emitFunc(f))
    }
}) {
    gen.run(@run(dir, args) { … })
    gen.test(@test(pkg, dir) { … })
}
```

A program opts in by importing the package that declares these, the way it
already opts into a target's rules by importing `sngl:platform/<name>`.

Running a command should be rare. Most languages and platforms are meant to
ship in SNGL's own library, written this way, so a program reaches for a
third-party plugin -- let alone one that runs a process -- only for what the
batteries do not cover.

## Prerequisites

Two defects this plan runs into first. Each is a branch of its own, landing
before Phase 0.

- **An event binds exactly one payload.** A component declares `@mount T`
  and a handler binds it as `@mount(v)`; there is no way to declare or bind
  more than one parameter. That is a limit of the event syntax rather than a
  choice, since a func type already takes a parameter list. The fix is an
  event declared with a parameter list -- `@generate(out gen.Out, importPath string)` -- and a handler binding them by position, the way a func literal
  does, with the one-payload form as the case it already is.

  **Done** in !176 (`fix/event-params`). What it settled, for the plan:
  `ir.EventDecl.Params` is a FuncSig-shaped list, so an interpreted gen
  handler gets its arguments by index. An emit passes every parameter or none,
  and none *forwards* the enclosing handler's parameters. A gen component that
  fires its own event therefore has to pass `out` and `importPath` explicitly.
  A bare `@name` is still one loose `dyn` parameter, so the gen components
  should declare their events with a list (`@generate(out Out, importPath string)`) and never bare. Host-widget dispatch still reads a single payload
  (`EventDecl.Payload()`), which is fine here because gen handlers never reach
  a platform emitter.
- **The root family is declared in `sngl:ui`.** It is the family a package
  body accepts, which is a fact about every package and not about widgets, and
  having it in `sngl:ui` is what forces the exemptions around it: `output` is
  let off the root-family check because `sngl:builtin` cannot import
  `sngl:ui`, and anything else that wants to be written at a file's root
  would have to import the widget library to say so. `root` moves to
  `sngl:builtin`, `window` keeps returning it, and the `output` exemption is
  re-examined once it can simply be a member.

  **Done** in !177 (`fix/root-in-builtin`). `output` and `cache.inputs` now
  name `root`, and `treeOptional` covers only extension bodies. The
  directives' root-of-a-file rule stays, because a directive is read once
  before anything runs. That is a separate question from family membership.
  Found along the way, and relevant to Phase 0: a package-body node that is
  neither a window nor a directive, such as `meta()` for a
  `component meta() root {}`, reaches the lowering as
  `lower.CreateNode("meta")` in the package body. Nothing renders or collects
  it. This is the gap decision 1's "collected, not rendered" has to close, and
  the gen nodes will be the first real users of it.

## Where it starts from

More of the seam is SNGL already than the Go interfaces suggest. What has moved
over the last three sections of PLAN.md, and what is left:

| concern                                  | today                                                                                                               | carrier in this plan                   |
|------------------------------------------|---------------------------------------------------------------------------------------------------------------------|----------------------------------------|
| what a target is, and its options        | `component go(…) build.language` in the target's own `.sngl`                                                        | unchanged                              |
| what a target emits natively             | `#[gen.can]`, `#[gen.cannot]`, `#[gen.wants]` on that node (`CapsFor`)                                              | unchanged                              |
| declarations a target synthesizes        | `PackageFS()`; gtk4's is a stored gencache entry                                                                    | `gen.scheme`-style producer            |
| an import scheme                         | `codegen.SchemeImporter` / `FSSchemeImporter`, registered in `init`                                                 | `gen.scheme`                           |
| what a producer read                     | `cache.inputs` (`sngl:x/gen/cache`) + `internal/gencache`                                                           | recorded by the host API automatically |
| turning IR into files                    | `PlatformGenerator.Generate`, `LangTranslator`, the two model emitters                                              | `gen.platform` / `gen.language`        |
| intrinsic ids a target answers           | `RegisterIntrinsic` / `RegisterPlatformIntrinsic`, `IntrinsicTranslator`                                            | `gen.intrinsic` child                  |
| `sngl run`, `sngl test`, snapshots, etc. | `Runner`, `Builder`, `TestRunner`, `TestLauncher`, `Snapshotter`, `PreviewStyler`, `HTTPCompiler` by type assertion | children of the target node            |
| identity, description, supported langs   | `PlatformIdentifier()`, `Description()`, `SupportedLangs()`                                                         | the build node and its doc comment     |

Issue #253 filed the rest of the seam as "plugins as configuration files naming
a command" and asked four questions that had to be answered before it was a
plan. Writing the plugin in SNGL answers them differently than an
out-of-process command would, and the answers are in *Decisions* below.

Two pieces of groundwork are already here and are what make this tractable
rather than a rewrite:

- **Every producer's output is SNGL and is stored** (!175). An import scheme
  written in SNGL is one more producer: its output is SNGL files, its inputs
  are recorded, and the store keeps the answer between builds and LSP
  keystrokes.
- **A capability not written is not held.** A target that claims nothing gets
  every lowering pass, so a new plugin sees the *smallest* IR the lowering can
  reduce a program to. That subset is what a plugin author has to handle on
  day one, and it only grows as the plugin opts into more.

## Decisions

Each has a recommendation. The ones marked *open* need an answer before the
phase that depends on them starts.

### 1. The nodes are root-family members

`gen.scheme`, `gen.language` and `gen.platform` are written at the root of a
file, like a window, and they belong to the root family (`root`, the
`#[marks.builtin("treeRoot")]` family a package body accepts, in
`sngl:builtin` after the prerequisite). So "where
may I write this" is the tree-membership rule that already exists, and a
component whose family is `root` can render them, which lets a plugin package
build one target out of shared pieces.

What that implies, and has to be built:

- **`sngl:x/gen` needs nothing but the ambient scope** for the family, once
  `root` is in `sngl:builtin`.
- **An imported package's gen nodes are collected, not rendered.** A package
  body today holds windows and is only meaningful for the program. For a
  library package, the body's gen nodes are its extension declarations, read
  when the package is imported; its windows stay the program's business (and
  an imported package with windows is already a question the checker answers).
  The collection is keyed by the declaration, like everything else, so two
  packages both declaring `gen.scheme(name="example")` are two declarations and the
  conflict is reported where the second is imported.
- **The declarations are checked like any other node**: props are declared
  props of the gen components, a misspelled one is an unresolved name, and
  a scheme's `name` and a target's `target` must be constant (the `requireConstGenInputs` rule).

The scheme node is `gen.scheme` rather than `gen.import`: `import` is a
keyword, so no package could declare a component by that name, and `scheme`
says what the node declares.

The alternative is a directive kind like `output` and `cache.inputs`
(`IsDirective`). It avoids the family question, but a directive is read once
and cannot be composed by a component, and the point of writing a target in
SNGL is that it can be.

### 2. Handlers are events with parameter lists

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

### 3. Handlers run in the interpreter

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
tests. Both are measured in Phase 2 against the docs-site compile.

### 4. The host API records its own inputs

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
all, which is decision 10. `gen.exec` is the one call that can reach anything:
what it records is the binary and its arguments, and what the process reads is
not visible, which is why it is never allowed unasked.

### 5. A plugin's code is part of its key

The store keys every entry by the compiler's identity because a producer's
code is an input no file can record. For a SNGL producer the code is the
plugin package, not the compiler. The key becomes (request, compiler
identity, **producer identity**), where a SNGL producer's identity is the
digest of the plugin package closure -- its source and the source of what it
imports -- and a Go producer's is empty.

Without it, editing a plugin would replay what the old plugin produced.

### 6. Import resolution runs in two phases

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

### 7. A target is a build node plus a generator attached to it

The build node stays a component: `component lua(version string) build.language`. It is what an `output` block names, the schema of its
options, and the carrier of `#[gen.can]` marks, and all three already work.
`gen.language(target=lua, …)` attaches the implementation by reference, the
way `output(entry=home)` names a window: a misspelled target is a name nobody
declared.

So one package may declare the node and another implement it, and the existing
Go targets can keep their node while their implementation moves.

`gen.platform(target=…, langs=[…])` replaces `SupportedLangs()`.

### 8. What a target handler receives: IR as data

This is the largest piece, and it is `sngl:x/gen/ir`: the lowered IR exposed as
SNGL structs and enums the interpreter marshals `*ir.Package` into.

- **The lowered subset only.** A plugin never sees a construct a lowering pass
  would have removed for a target that claims nothing. The schema therefore
  covers what survives full lowering, and a construct a plugin claims with
  `#[gen.can]` brings its IR node into the schema it must handle.
- **Positions and names, not pointers.** `ir` is a Go graph with shared
  declarations and `ast` nodes in it; the SNGL form refers to a declaration by
  a stable id and carries source positions as data, so a handler can emit
  source maps and diagnostics.
- **The analysis too.** `CommonAnalysis` (model fields, computed deps, timers)
  is already a JSON dump (`sngl dump --stage analysis`); exposing it saves a
  plugin rebuilding what every platform needs.
- **Versioned by the schema's own shape**, with the same polarity: a field
  added to the schema is one a plugin that has not heard of it ignores.

**A language and a platform stay separate**, as they are for the Go targets:
a language translates expressions and statements (today's
`FileEmitter.EvalExpr`, `EvalStmt`) and a platform assembles files around
them, which is what lets one language serve fyne, bubbletea and gtk4. For a
SNGL target that means `gen.language` children `@expr(e)` and `@stmt(s)`
returning source text, which a platform's handlers call per node. A platform
may still write files with no language behind it, as `html` on `none` does.

### 9. Extensions are children of the target node

Each optional interface found by type assertion today becomes a child the
node's rest slot accepts:

| child                              | replaces                                                                | used by         |
|------------------------------------|-------------------------------------------------------------------------|-----------------|
| `gen.build(@build(dir))`           | `Builder`                                                               | `sngl build`    |
| `gen.run(@run(dir, args))`         | `Runner`, `LangRunner`                                                  | `sngl run`      |
| `gen.test(@test(pkg, dir))`        | `TestRunner`, `TestLauncher`, `TestProber`                              | `sngl test`     |
| `gen.snapshot(…)`                  | `Snapshotter`, `TextSnapshotter`, the batch forms                       | `sngl snapshot` |
| `gen.preview(css=…)`               | `PreviewStyler`                                                         | `sngl preview`  |
| `gen.intrinsic(id=…, @emit(args))` | `RegisterIntrinsic`, `RegisterPlatformIntrinsic`, `IntrinsicTranslator` | codegen         |
| `gen.http(@compile(routes, out))`  | `HTTPCompiler`                                                          | html route mode |

A child the target does not write is a feature it does not have: `sngl run`
on it says so, which is the capability polarity applied to commands. The
children are the extension list's tree, so `gen.language { gen.platform … }`
is refused by membership rather than by a check someone has to remember.

`gen.run` and `gen.test` do exec processes, and their output is not SNGL:
they are the one place a handler's result is not a stored producer output.

### 10. Trust

**The threat model is a malicious repository.** Cloning a project and running
any `sngl` command in it -- `check`, `generate`, `doc`, or opening it in an
editor, which starts the LSP -- must not run a process, read the environment
or read outside the project on the repository's say-so. What the repository
may do unasked is what a SNGL function can do in the interpreter: compute, read
files inside the project, and write its own output.

So the host API of decision 4 is gated, per call and per plugin:

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
  Either it is deleted in Phase 0, or it becomes the one way to name an import
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
audited against the table above in Phase 0.

### 11. The compile-time evaluator is held to the same model

`#[foreign(pure)]` evaluation already does what decision 10 forbids: `sngl generate` in a cloned repository builds the repository's own Go packages and
runs them, and a js: call runs node over its modules. That is `go generate`,
not `go build` -- running a project's code -- and it happens today with no one
asked. Applying the threat model to it is part of this plan and lands in
Phase 0, before any plugin can exec, so there is never a release in which the
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
has one (`c.warn`) -- so Phase 0 adds one, returned through `build.Emit` and
printed by the CLI with the checker's. The LSP never evaluates -- it optimizes with no project directory,
which already skips the evaluator -- and keeps not doing so.

### 12. #253's questions, answered

- **What crosses the process boundary, and in what form.** Nothing, for the
  generator: it runs in process on IR marshalled into `sngl:x/gen/ir`. A
  handler that shells out does so through `gen.exec`, which crosses with
  strings.
- **When the command runs.** A `gen.scheme` handler runs during the check, as
  `PackageFS` does now, and its output is a stored entry, so a keystroke that
  changes nothing it recorded reruns nothing. A target handler runs at
  generate time only.
- **Absent or failing a version check.** A plugin is an import: absent is an
  unresolved import with a position. A version skew is a schema field the
  plugin never saw (ignored) or one it names that no longer exists (a checker
  error in the plugin package, reported at the import).
- **`Config.TargetsComplete`.** A target is complete once the two-phase import
  resolution of decision 6 has settled, which is before any `output` block is
  checked. The flag's meaning is unchanged; it is just computed after
  plugins load rather than from the Go registry alone.

## Phases

Each phase lands with fixtures written first, confirmed to fail on the tree
before it.

### Phase 0: groundwork

After both prerequisites have landed.

- `sngl:x/gen` declares `import`, `language`, `platform` and the children as
  components with empty bodies -- the three in the root family, the children in
  a family of their own -- the way `cache.inputs` members are declared. A
  bodyless declaration would need an override to render, and these render
  nothing on purpose. Fixtures:
  a correct plugin package, a gen node in a component body that is not
  root-family, a non-constant `scheme`, an unknown child.
- The checker collects a package's gen nodes on import (`ir.Package.Gen`).
- Interpreter host API skeleton in `internal/interp`, recording through
  `internal/gencache`.
- The producer identity in the gencache key (decision 5).
- The permission gate (decisions 10 and 11): the allow flags, the config file
  and its reader, the origin a grant binds to, the CLI prompt and the LSP's
  diagnostic -- and `--allow-eval` in front of the existing go: and js:
  evaluation, which is the one part of this that changes what a build does
  today. The flags are persistent on the root command; `SNGL_ALLOW` is read
  beside them and stripped from every child's environment. A fixture for each:
  an unallowed evaluation that does not fold and warns once per package, a
  store hit that folds without a grant, a grant that stops applying when the
  package's content changes, `--allow-all` refused from the environment, and a
  child process that does not see `SNGL_ALLOW`.
- An optimizer warning channel, through `build.Emit` to the CLI.

### Phase 1: `gen.scheme`

- Two-phase import resolution (decision 6), with fixtures for the order cases:
  plugin imported after its use in the same file, in another file, a cycle, an
  unknown scheme, a shadowed built-in.
- Running `@generate(out, importPath)` in the interpreter, its output stored and served as
  the import's SNGL package through the existing `ResolveSchemeFS` path.
- A golden fixture whose archive holds a plugin package, a program importing it
  and the scheme, and the generated output.
- The first real user: `c:`'s `pkg-config` reimplemented in SNGL with
  `gen.exec`, deleting the Go importer.
- In parallel, and independent of SNGL plugins: the Go-implemented importers
  converge on returning SNGL (the go: shims from !175's follow-ups), so that
  "an import resolves to SNGL files" is true of every scheme and not only the
  new ones.

### Phase 2: `gen.language` / `gen.platform`

- `sngl:x/gen/ir` for the fully lowered subset, with the marshaller and a test
  that every IR node reachable after full lowering has a schema type.
- A toy target end to end, as a golden: a plain-text platform on the `none`
  language that writes each window's tree, then a small language (Lua) with
  `@expr`/`@stmt`.
- Measure the interpreter on the docs site's IR size before going further.

### Phase 3: extension children

- `gen.build`, `gen.run`, `gen.test` for the toy target, with CLI script tests
  in `cmd/sngl/testdata/` (they execute, so they are not goldens).
- `gen.intrinsic`, so a SNGL target answers intrinsic ids and
  `lib/internal_intrinsics_test.go`'s "some target emits every id" check reads
  SNGL targets too.

### Phase 4: a built-in target moves

Pick the smallest real one, likely `none` (it generates nothing and tests
through the interpreter), then html's static mode. The measure of success is
that its Go package is deleted and its goldens do not move. Which of the rest
stay Go is decided then, per target, on what they would cost to move; there is
no requirement that all of them do.

### Last: delete this file

The branch's final commit deletes `SNGL_PLUGINS.md`. What the work leaves
true belongs in CLAUDE.md, next to the code it describes, and a plan kept past
its work is read as a description of the tree it no longer matches.

## Rules that bite

- **This file is a build gate**, like PLAN.md: run
  `go tool mdox fmt --soft-wraps SNGL_PLUGINS.md` before committing.
- **A plugin fixture is a golden when it asserts generated output**, and a CLI
  script when it executes something (`sngl run`, `sngl test`).
- **Confirm each new fixture fails when the behaviour is reverted**, and in
  particular that an order-sensitivity fixture fails when the two-phase
  resolution is replaced by statement-order resolution.
