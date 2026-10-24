# Stdlib, library loading and targets

## Stdlib

Stdlib source lives in `lib/<package>/*.sngl`, embedded via `//go:embed` in `lib/lib.go` (exported as `lib.FS`). **Each subdirectory is one importable package: `lib/<path>` is `sngl:<path>`.** Nothing in Go enumerates them — `lib.Packages()` reads the embedded directory, so adding a package is adding a directory.

A package is named for **what it does**, never for who ships it — `std` was a
name of the second kind, which is why everything drifted into it. Within a
package, files are organised by topic and not by declaration kind: a type and
its methods sit together (`lib/builtin/numbers.sngl`, `lib/ui/form.sngl`),
because grouping by kind splits every subject in two.

The tiers, and the split between them is the whole point of the system:

- **`lib/builtin/` → `sngl:builtin`** — the `#[builtin]` types and their methods, plus `output`, the error channel and `root`, the family a package body accepts. The build directive is here rather than in a tier of its own because a package names its own targets without importing anything; `error`, `error.raise` and the `boundary` that catches one are here because a boundary is generic over the tree it was placed in and so belongs to no family — it is the compiler's construct, not a widget. Ambient: dot-imported into every file implicitly, and importing it explicitly is an error. This is the *only* implicit import in the language.

  **A builder** (`lib/builtin/strings.sngl`, `ir/builder.go`) grows text in
  place: `b.add(x)`, `b.line(x)` and `b.string()`, which is its string form,
  so `"{b}"` and `string(b)` read it too (`inferBuiltinConversion` converts
  to a string through a declared `string()`). Adding to a string with `+`
  copies it, so a loop building one that way is quadratic; a builder is not.
  It is a value, copied by a store as a list is (`ir.HoldsValue`), and no
  state: a package or component var, a view block's `var`, a prop and a
  context may not hold one (`refuseBuilderState`), since a view shows the
  string. The interpreter holds a `[]byte` (`internal/interp/builder.go`), Go
  a `[]byte` and Kotlin a `StringBuilder`, each declaring
  `#[gen.can(stringBuilders)]` and answering the three intrinsics;
  everywhere else `passStringBuilders` writes the builder as a string, which
  is what html's script gets under any `--lang`, a JavaScript engine joining
  `+=` as a rope (`testdata/builder.sngl`, `builder.txtar`,
  `error_builder_state.sngl`). The optimizer does not rewrite a `s = s + x`
  loop into one.
- **`lib/ui/` → `sngl:ui`** — the portable components most applications are built from, the `node` family they belong to, the `window` that is a member of `sngl:builtin`'s `root`, and the vocabulary every one of them refers to: `Style`, the style enums and `measurement`. Those sit here rather than in packages of their own precisely because every component in every package under `sngl:ui/` names them. Reaches user code only through `import <alias> "sngl:ui"` (qualifies) or `import . "sngl:ui"` (flattens).
- **`lib/ui/draw/` → `sngl:ui/draw`** — `canvas`, the `shape` tree it hosts, and the 2D shapes that are members of it. It is the first *specialised surface* under `sngl:ui/`: a program pays for a drawing canvas only by importing it.
- **`lib/ui/markup/` → `sngl:ui/markup`** — inline rich text: the `span` family, the `richText` node that shows one flow of it, and the bodied block components a document is written in. The second specialised surface; see **Markup and the `md:` scheme** below.
- **`lib/ui/nav/` → `sngl:ui/nav`** — navigation: `stack` (pages one on top of another, `current`/`go`/`back` as `#[intrinsic]` methods each target answers), `page` and the unexported family `_page` it is the one member of, and `link`. See **Navigation** below.
- **`lib/family/` → `sngl:family`** — the family *vocabulary* and no families: the `crosses` mark a placement writes to stand outside the family its position accepts, the `eventless` mark a family writes, and `one[T]` for a slot that takes exactly one. A family lives where its members do, which is why the widget family is `sngl:ui`'s `node` and not a `family.default` here. It was `sngl:tree`, whose `none` mark is gone: a component that takes the family it is placed in returns a type parameter (see *A wrapper whose family is whatever it was handed*).
- **`lib/build/` → `sngl:build`** — families, and the build-target tree: `family`, the family of families every family is a member of, and `language` and `platform`, the two families an `output` directive's contents are members of. It is a package of its own rather than part of `sngl:ui` because `sngl:builtin` declares `output` and so has to import whatever holds its slot's type; `sngl:ui` is what `sngl:builtin` would then be importing, and it loads before `sngl:builtin` is adopted into the ambient scope, so the load fails on `unknown type "color"`. The two target families double as the target identities: a target's node read as a value is a value of its family, and `PLATFORM` and `LANGUAGE` are declared `build.platform` and `build.language`. An application names it to declare a family (`component block build.family`); otherwise a program writes `output`, and a target package names `build.language` or `build.platform` in its own node's return position.
- **`lib/time/` → `sngl:time`** — dates and the clock: `date`, `time`, `datetime`, the `duration` between two of them, a date's `year()`, `month()` and `day()` and `parseDate(s)` for text a program did not write (written in SNGL over `string(d)`, which every target spells as the literal), and the `timer` that fires every duration -- an ordinary component, not a builtin node, which is why it carries no `#[builtin]` mark. What it lowers to is each target's answer: html, fyne and gtk4 override it with an `effect` over a start/stop pair of host natives, since such a pair already is a lifetime with a thing to release; bubbletea, android and none still override it with an `#[intrinsic]` node, which stays where it was written -- `codegen.CollectTimers` reads the schedule off it and the platform's view emitter draws nothing for it, so the branch and the component boundary around it are answered by the tree rather than by a gate a pass folded. **An effect hands over a closure, and a closure is only a schedule where the host may run it against live state** — which is what the first two cannot offer, bubbletea because Elm lets nothing outside `Update` touch the model, android because `LaunchedEffect` already is the bracket. `none` is not that case: the interpreter honours `effect` itself, so an override would bracket correctly and schedule nothing, since it owns the clock and finds the node in the rendered tree. None of it is ambient — a program that never asks what time it is never names any of it — which is why all four types moved out of `sngl:builtin`. Loaded at startup even when nothing imports it, because its declarations carry kinds the compiler dispatches on.

  **The clock is `now`, and the `const` it does *not* carry is the
  load-bearing part.** A const call is a constant, so a clock reading declared
  `const func` would be accepted wherever a constant is demanded and the value
  a build ships would be the one the build machine read; a bodyless library
  function is not const unless it says so, and `now` keeps
  `#[marks.intrinsic("time.now", readonly)]` besides. `since` and `until` have
  SNGL bodies and inherit the impurity through the purity fixed point rather
  than restating it, while `add` and `between` are `const func`, being pure
  functions of their arguments.

  The instant is **`datetime` itself** rather than a type of its own: it
  already maps to the host's date type everywhere, and a second one would be
  two SNGL types over one host representation. What that costs is that Go
  carries it as a `time.Time` beside a `time.Duration` and converts nothing,
  while JavaScript and Kotlin carry an RFC3339 string and pay a parse per
  operation — and both have to normalise first, because a datetime *literal*
  may be written with no offset, which ES reads as local time and
  `Instant.parse` refuses outright.

  `between` is a **package function and not a `datetime` method**, which is
  forced rather than chosen: `resolve.go` reads a method's first parameter *as*
  the receiver when it carries the receiver's own type (what lets
  `int.ident(1)` mean `1.ident()`), so no `datetime` method can take a second
  instant — `d.since(x)` reports `too many arguments: expected 0`.

  It is **wall-clock** time and says so in `lib/time/doc.sngl`: what a date
  shown to someone is, and what an interval against a calendar deadline is. Not
  a scheduling primitive — a wall clock jumps on an NTP correction and on a
  daylight saving change, and an RFC3339 string cannot carry a monotonic
  reading at all.

  Duration arithmetic needed no declarations: `unit / unit` is the
  dimensionless `float` the checker has always typed it, and `unit / scalar` is
  still that unit. It had simply never compiled — Go divided two
  `time.Duration`s into a `float64` return and Kotlin two `Long`s into a
  `Double`, so a program could write the division and no Go or Kotlin target
  could build it. `ir.IsUnitRatio` is the shared answer, single-base only for
  the reason the checker already refuses *ordering* and *casting* a multi-base
  value: a magnitude per base has no one ratio to report.
- **`lib/math/` → `sngl:math`** — mathematical constants and functions: `pi` and `tau`, `sqrt`, `pow` and the trigonometry. A package rather than methods on `float`, because a constant has no receiver and nothing to fold — a zero-parameter static method survived only where the optimizer ran. The functions moved here from `float`: each takes a float without being about one, so a call reads `math.sin(x)`, and their intrinsic ids are `math.<name>`.
- **`lib/seq/` → `sngl:seq`** — integer sequences: `count`, `range` and `step`, the `iter[int]` a counting loop iterates. Nothing else can produce one, since building a range in SNGL would need a loop and a loop needs a range; a sequence in a loop head lowers to the host's counting loop (`ir.IterCounted`), and anywhere else it is the pull sequence `iter[T]` is spelled as -- `func(func(T) bool)` in Go, a generator in JS, `Iterable<T>` in Kotlin -- so no list is built to iterate one. A list reaching an iter[T] position is wrapped by the conversion the checker already inserts there (`wrapIfNeeded`); a two-variable loop over one gets its ordinal from a counter (`passIndexedIter`), since a pull sequence hands out no index.
- **`lib/async/` → `sngl:async`** — `start` and `do`: handing a closure somewhere else to run. They are each other's halves — `start` begins work that must not block the caller, `do` brings the answer back to the thread the target draws on — and **each is answered by a different half of the build**, which is why they are two declarations. Starting work is the host *language*'s (`go func(){}()`); reaching the drawing thread is the *platform*'s, because there is no such thread in general. `passAsyncOffload` has always written both for a blocking call on a language that cannot suspend; they are *declared* because a platform package needs to name one — a platform whose own `.sngl` describes a schedule (a timer handing its tick to a host scheduler) has to be back on the drawing thread before it touches a widget, and with no declaration has nothing to write but that platform's own spelling of `fyne.Do`, in its own package. A generator reaching its own thread from hand-written Go needs no name; a platform *package* does. That is the line against `ir.NodeOps`, which stay undeclared precisely because nothing can name them. `query`, `Result` and `Failure` sit beside them: a request bracketed by the tree is the two one level up (see *Queries* in `design/language.md`). A target that answers neither is refused at the call by `passAsyncCapable` rather than emitting code that does not compile: `async.do` on bubbletea used to come out as `m.post(...)`, a method on the model that does not exist.
- **`lib/http/` → `sngl:http`** and **`lib/json/` → `sngl:json`** — a
  request while the program runs, and a declared type read from and written
  as JSON. Neither knows the other; see **HTTP and JSON** below.
- **`lib/dialog/` → `sngl:dialog`** — `Alert` and `File`: host-native modal surfaces. Not components — a component is placed in a tree and rendered, whereas `Alert.confirm` hands control to the host and returns what the user chose.
- **`lib/test/` → `sngl:test`** — `Test`, the receiver a test function's first parameter carries.
- **`lib/test/browser/` → `sngl:test/browser`** — what a page shows, read in the browser a test runs in: an element's text, attribute, markup and computed style, how many elements a node renders, and a pixel it paints. Each takes the node a test reaches by its `#id` and is answered by html's JavaScript alone, so a fixture importing it builds for html only -- `sngl:test` stays portable, and a question about the document is asked where only a document answers it.
- **`lib/i18n/` → `sngl:i18n`** — the translation surface `$"..."` lowers to.
- **`lib/macro/` → `sngl:macro`** — the public mark vocabulary a package writes to describe its own declarations (`foreign`, `wildcard`, `construct`). Only the vocabulary: `sngl:platform/<name>` and `sngl:language/<name>` are not this vocabulary but targets, described below — under `lib/platform/` and `lib/language/` once moved.
- **`lib/x/gen/` → `sngl:x/gen`** — what a target package says about what it generates: `#[gen.can]` and `#[gen.cannot]` name the SNGL constructs it emits natively, `#[gen.wants]` the lowering passes it asks for. Written on the build-tree node the package already declares (`component go(…) build.language`), and read by `codegen.CapsFor` into `lower.Features` — there is no `Capabilities()` method, because a target that is a command rather than a linked-in package cannot answer one. **A capability not written is not held**: there is no base set to subtract from, so a target that says nothing gets every lowering pass. That is what lets the language grow — a new construct arrives with a lowering pass that converts it away, and every target that has not heard of it keeps working unedited, opting in only when its code generator can do better than the pass -- `navigation` is that shape: every target gets `passNavigation` until it says it renders `sngl:ui/nav` itself. Under the other polarity, silence would mean "I emit this" on every declaration written before the construct existed, so adding one would break every target at once. A plugin answering nothing, or answering a vocabulary older than the compiler asking, is the same argument with a version skew in place of a new construct. A platform overrules a language per capability, which is what lets html take back the `ternary` Go withdrew; naming one both ways on one declaration is an error. `#[gen.protocol(op)]` is what a platform writes on functions rather than the node: how it makes, fills, writes and connects an `#[intrinsic]` component's widget, as SNGL calling the host, so every call is one a pass sees (`design/nodes.md`). Not `sngl:build`, whose audience is the same: that package is the build-target *tree*, and what generating for a target involves is a different subject — `sngl:x/gen/cache`, the inputs a generated file records (see *Performance*), is the second member, and `sngl:x/gen/trust`, the user's grants (see *Trust*), the third. It also holds the host API a build-time function reads the machine through (`host.sngl`), and what a handler writes through (`scheme.sngl`): `out.file(name)` opens one file and hands back a `gen.File` handle whose `add` and `line` write into a buffer the build host owns (`buildhost.Output`), so a file built a line at a time costs its length; each file is written when the handler returns, and a name opened twice is an error at the open (`cmd/sngl/testdata/scheme_errors.txt`). Where a mark may be written is checked in `finishFamilyMarks` rather than in the handler, which measures it against the return position `bindFamily` read, beside the other rules about what a component's marks say of its tree.
  **`sngl run` and `sngl build` are commands a target writes**, not Go
  interfaces: a `gen.run(@run(dir, args))` or `gen.build(@build(dir, out))` in
  the body of its build-tree node (`lib/x/gen/command.sngl`). The platform
  node's override for the language wins, then the platform node's body, then
  the language node's (`internal/build/command.go`), so `go run` is written
  once on `sngl:language/go` and fyne, gtk4 and bubbletea say nothing. The
  handler runs in the interpreter with the node's props bound to the target's
  options; `gen.shell` runs a process on the user's terminal, and what only Go
  can do is a build intrinsic the target registers with
  `codegen.RegisterCommand` -- `go.buildModule` (the go.mod written against
  the host checkout, tidy, build), `html.serve`, `android.buildApk` and
  `android.install`. A target that writes none has no such command. A command
  written in a program is refused (`error_command_placement.sngl`): nothing
  would read it, and a repository may not choose what `sngl build` runs. A
  build node is never rendered, which is why it may have a body and is exempt
  from the const-component rule.
- **`lib/x/scheme/<name>/` → `sngl:x/scheme/<name>`** — the library's own import-scheme plugins, each serving `<name>:` with no import (see *Import schemes*). `c` runs pkg-config and writes a `c.link`; `gir` writes gtk4's widget declarations from GIR through a build intrinsic gtk4's Go answers (`buildhost.RegisterIntrinsic`); `openapi` writes a package from an OpenAPI 3 document (see *The `openapi:` scheme* in `design/import-schemes.md`). Checked as a package of its own when its scheme is first asked for -- against no target, since it is a build tool -- and left out of the merged stdlib documents. It may import `sngl:internal/`, and what it writes is library source in all but origin (`Config.byLibrary`): it may too. A check with no build resolver -- `sngl doc`, the LSP's library path, `CapsFor` -- still resolves one, through the runner `internal/plugin` registers with `checker.RegisterLibraryRunner`, which is what lets library source import a library scheme.
- **`lib/x/c/` → `sngl:x/c`** — `link`, the directive a package writes to say which C header its `#[cnative]` declarations come from and the flags compiling and linking against it need; read onto `ir.Package.CLinks`, from which a Go target writes its cgo preamble (`ir.ReachedCLinks`). Also the conversions a C call's arguments cross through, `#[builtin]` functions `lower.CMarshal` finds by kind (`cString`, `cInt`, `cDouble`, `cBool`), and the owned `str` with its `free`.
- **`lib/internal/` → `sngl:internal/<name>`** — the compiler's own tier, importable only from lib source.

A package documents itself with a **package comment**: a run of line comments
at the top of its `doc.sngl`, separated from what follows by a blank line
(without the blank line it documents the declaration below it instead). The
text is markdown, and `sngl doc` renders it as the package description — so
adding a `lib/` directory with a package comment needs no code change.

**A parameter documents itself the same way**: a prop, event, slot or function
parameter written in a list across lines carries the line comments directly
above it, or with none the comment after it on its line (`ast.CommentDoc`, read
as `ast.Param.Doc()` and onto `ir.Prop.Doc`, `ir.EventDecl.Doc`,
`ir.SlotDecl.Doc` and `ir.Param.Doc`). A comment opening with `TODO` documents
nothing. `sngl doc`, the docs site, the doc browser and hover all print it
beside the name, so a declaration's own comment says what the component is and
each parameter says what it takes.

**Only `doc.sngl` answers** (`checker.PackageDocFile`, enforced in
`checker.PackageDoc`), which is where Go's semantics are dropped: every file's
comment counting, in a load order nothing guarantees, published whichever file
header a directory listed first. It is also what keeps a stored file's
`// Code generated … DO NOT EDIT.` header -- what the `gir:` scheme writes for
gtk4 opens with one -- out of that package's description. A leading comment in any other
file is an ordinary comment about that file. Target packages
follow the same rule, each with a `doc.sngl` beside its source.

Packages import each other — `lib/ui/draw` is written against `lib/ui`, and `lib/ui` in turn against `sngl:time` and `sngl:family` — so they load lazily and memoized (`libPkg`), not in directory order. A lib package qualifies its dependencies rather than dot-importing them: lib source is registered into the checker's own symbol table, so a name it lifted would be indistinguishable from one it declared and would be re-lifted by a dot import of it. User packages do not re-export a dot import; lib packages must not either.

**A library load owns what it records.** Each `libCache` carries the
`ir.Builtins` its loads registered -- sngl:builtin's `list` and `map`, the
datetime and duration types, every intrinsic -- and the `declTables` its loads
wrote (type parameter symbols, body owners, nested funcs, interned anonymous
structs, inherited family props), and a check reads its own library's.
`ir.Published` is the process-wide copy every load also writes, for the phases
that hold no checker. A target package's own overrides are checked as part of
loading it (`mergeTargetExtensions`), so what one declares is the target
package's, and they are merged as the package loads, whoever loaded it, so
nothing writes a package another check can reach; what the merge reports is
a check's only where it builds for the target or imports it. What a load made
is frozen as the load lets go of the cache:
`ir.Freeze` sets `Frozen` on its packages, their root scopes and their
declarations, and the known writers -- a package root's bindings,
`AttachMethod`, `specializeComp`, an overlay install, a `Builtins`
registration -- panic on one (`ir.MustWriteDecl`). They ask the declaration
rather than its package, since a build's copy of a library declaration names
the package it came from; the cloner leaves the flag behind.

**The library is checked once per process** (`libhold.go`). A check finds
the cache for its targets in `processLibs`, keyed by each registered target,
whether it is available, and its `LibraryState` -- gtk4's GIR setting, which
decides the widgets it declares. A check substituting library source
(`LibSources`) or asking for a library of its own (`PrivateLibrary`, which a
test that strips what it checked takes) gets a fresh one, and
`EjectLibraries` drops the lot. A check holds its cache for reading from the
moment its checker is built, a nested check sharing the hold (`libHold`), so
the cache's tables need no lock of their own; a load trades that for the
write lock, looks again for what it was about to load, and before it lets go
resolves every macro's signature and mints every prop's symbol, which a
later check would otherwise write. A load that panics drops the cache from
the process, since it is half-written, and a check that panics lets go of its
hold (`TestCheckAfterAPanickingLoadFinishes`). What a load reported is replayed to each later check that reaches the
package (`replayLoad`), and a context a library declares is on that package
and adopted by the program (`adoptLibContexts`), as each check that loaded
its own library used to report and append them.
`TestConcurrentChecksAndBuildsShareTheLibrary` runs checks and builds at once
from a cold cache under `-race`; the writes it found were a program's check
storing a library macro's signature and an override's check re-storing a
library prop's type. A library loaded part-way through a program's pass2 --
an output block naming a target the build is not for -- is loaded with no
file of the program's current, or a file switch inside the load set the
package's import scope to the program's file
(`TestTargetLoadedByAnOutputBlockKeepsItsImports`).

**A directory import and a scheme's package are kept beside the library**
(`dircache.go`), keyed by the import root, the path or URI, the replace map
and the targets, and holding the digest of the source they were checked from
(`ast.Document.Digest`, which `parser.Parse` records). A later check reusing
one asks that its source is unchanged and that each directory package it
imported is still the one kept, read again and recursively; storing a new
version drops the old one and every kept package checked against it, which is
how an edit reaches the LSP's next check (`TestImportedPackagesAreKept`). Only
a package whose check is a function of those things is kept: one importing a
`go:` package, a plugin's output, a fetch or a directory under a scheme's FS
is checked every time, and so is one whose check reported anything of its
own. A hit does for the build what checking each package it reaches would
have: it memoizes them, adopts their contexts and replays the library loads
they reached (`useEntry`), since the build that kept one may have done all
three through another import. And a directory this build has already checked
into a package of its own -- under another replace map, so another key --
fails every kept package that imported a different one, or two versions of a
type meet in the program (`TestKeptPackageAgreesWithTheBuildsOwn`). A kept
package is settled and frozen as a library load is, which is why two imports
under one alias no longer chain the first package's scope onto the second's:
that wrote into whatever package the first import found. The resolvers parse with `parser.ParseShared`, which hands two reads of
the same bytes one tree while either is held: a hit still resolves the
package's source, and the `md:` site's was most of what a warm check of the
docs site parsed.

**An override belongs to the package that declares it.** A target package's
override of `sngl:ui`'s `text` goes in that package's `Overlay`, as a
program's does (`overrideMap`), and the cache publishes every library
overlay as one merged overlay it replaces rather than writes
(`libCache.targets`, `ir.Overlay.Merged`). A check reads it through
`overridesOf`, and records the one it saw on its package as `Targets`, which
`ir.InstallOverlay` installs on a build's copy ahead of the program's own and
`ir.Package.OverridesOf` reads off a checked package. So a later check may
load a target an earlier one never did without writing the declaration both
read, and the earlier check does not see it
(`TestFrozenLibraryTakesANewTarget`).

**Every phase after the check works on a copy** (`ir.ClonePackage`,
`ClonePackageFor`), since the checked package and the library it reaches are
what every later build reads: `build.Emit` copies for every target, the last
included, `interp.Prepare` returns the copy a session or a test run uses, and
the CLI's `dump` stages, the playground's compile and generate, the docs
preview, every snapshot path, the LSP's color fold and the fixture fold each
copy before the first phase that writes. A phase handed the checked package itself panics at the first library
write. TestGolden fingerprints the checked package, and the library it
reaches, around `Emit`. A target's copy keeps other targets' override entries
emptied, since a build still asks whether a declaration is overridden for
*some* target (`cmd/sngl/testdata/emit_family_refused.txt`).

A `#[builtin("kind")]` mark says which IR construct a declaration dispatches to, **not** which tier it lives in nor what the declaration is called — the builtin visual nodes are spread across tiers, `sngl:ui/nav`'s three in `ui/nav`, and `effect`, `context`, `output` and `boundary` in `builtin`. `boundary` is the case that makes the second half plain: it carries the `errorBoundary` kind, because the kind names the role and `ir.ErrorBoundary` is the construct it dispatches to, while the name a program writes is the declaration's own.

What compiler code reads *off* a marked declaration -- a nav page's `href`,
`gen.emit`'s `render`, a stack's `nav.current` method -- is the declaration's
own vocabulary, written once in `ir/builtinprops.go` and listed per kind in
`ir.BuiltinVocabulary`. `lib/builtin_vocabulary_test.go` holds each marked
declaration to declaring it, so a rename in `lib/` fails there rather than
leaving every reader finding nothing. A method is found by its intrinsic id
(`Component.MethodByIntrinsic`) and a rest slot's population by the slot's
declaration (`NodeInst.RestPopulation`), never by the name either is written
under.

`internal/checker/stdlib.go` parses the library at startup. User declarations shadow stdlib ones. Platform-specific component implementations live in that platform's own package; its source imports the stdlib under an alias and overrides through it (`import ui "sngl:ui"` + `component ui.vbox`), and the prefix is that alias, not a fixed name. The override names the target it implements by the package's own build-target node, unqualified — `component ui.vbox[platform]`, not `[android.platform]`: inside the package that declares it, saying the package name would say it twice. A program outside the package writes the qualified form, `[html.platform]`, because that is how the node reaches it.

**A target is its build-target node.** A platform package declares `#[gen.name("html")] component platform(…) build.platform`, a language package `component language(…) build.language`: the node is named for its tier by convention only -- it is found by its family and its mark (`ir.TargetNodeOf`) -- and `#[gen.name]` is the string the CLI, the Go registry and an `output` block's bare `html` match on (`ir.TargetNode`, `GenCaps.TargetName`). The same declaration is the option schema, carries the `#[gen.can]` marks and is the identity: `[platform]` and `[html.platform]` resolve to it (`resolveTargetIndex`), and read as a value it is a value of its family, `build.platform` -- the type `PLATFORM` is declared with (`targetValueType`) -- folding to its name in the optimizer and the interpreter, so `PLATFORM == html.platform` is unchanged. There used to be a generated `const platform = "html"` mounted into every target package (`identityDoc`, `#[macro.identity]`), a second declaration that could disagree with the first. A build-target node without the mark is an error, as is the mark anywhere else, and so is a name another node of the same tier carries (`reportTargetNames`, run last over every loaded package, library nodes ahead of the program's so the program's is the one reported) or a target package's node naming anything but its own package: `none` may be both a language and a platform, and nothing else may be two things. Diagnostics name a node by `Component.DisplayName`, its target name, since every platform's node is called `platform`.

**A target reserves names in a const prop of its build-target node**: a
language its words in `reservedWords` -- Go's keywords and every predeclared
identifier, Kotlin's hard keywords, JavaScript's reserved words, written once
as `js.words` -- and a platform in `reservedNames` the identifiers its
generated code introduces beside a program's: a receiver (`m`, `c`), an import
alias (`fmt`, `tea`, `widget`), a field or method of a type it writes
(bubbletea's `width`), a local its `Update` declares (`cmd`, `msg`), and the
SNGL spelling of a name it capitalizes (`model` for Go's `Model`, `modifier`
for Compose's `Modifier`, which an explicit import outranks). Two names
because a build's options are one record, where a language's prop and a
platform's of one name would be one field. html's page script is JavaScript
under any `--lang`, so its default is `[...js.words, "state", …]`. A program
may set either in its `output` block, which replaces that half for the build:
`passReservedNames` reads the target's merged options off `pkg.Outputs`
(`testdata/reserved_names_output_option.txtar`), and `FeaturesFrom` reads the
defaults into `Features.Reserved` for a build with no output block.

`passReservedNames` renames each declaration that took one, through the
per-build renamer (`range__r0`), and runs last; a node's `#id` cannot wait
that long, since passDeclarative turns a node into a local named by its ID
with no symbol behind it, so `passReservedNodeIDs` renames IDs right after
the inliners settle them. The rename follows the declaration: every Ident, a
LocalVar's and a loop's own name, a node's ID, a method's Receiver, and a
member selected off a component value -- which is how a test's `c.range` and
`c.go.click()` reach what they named. A struct field and an enum member keep
their names, being read by spelling (a route's path, a serialized record): Go
exports a field and JavaScript takes a keyword as a key, so only Kotlin escapes
one, in backticks, from its language's own default (`codegen.LanguageReserves`).
So does a host's declaration -- foreign, native, intrinsic, builtin -- and a
native's parameters, which Kotlin's `named` flag passes by name. Two more are
a harness's: a test function's parameters, which the test emitter spells in a
function of its own, and the component a test build is rooted at, which
`Options.RootComponent` names. `testdata/reserved_names.txtar` is the code and
`cmd/sngl/testdata/reserved_names_runs.txt` runs it on all six.

**A target is its library package.** `sngl:platform/<name>` and `sngl:language/<name>` are `lib/platform/<name>` and `lib/language/<name>`, and the layout is the registration (`codegen/declared.go`), as `lib/x/scheme/<name>` is a scheme's: each directory is a `DeclaredPlatform` or `DeclaredLang`. Its Go, where it has any, is a generator registered by key (`codegen.RegisterNative`) and named by the node's `#[gen.native("html")]`; `LookupPlatform` and `LookupLang` resolve a declared target through that mark, once, and a node naming none generates nothing -- `none`, whose programs the interpreter runs. A package may import a library scheme -- gtk4's imports `gir:`, whose package is the half of the platform derived from the host.

The point is that the checker does not know where a package comes from: a lib/ directory or a scheme's handler. The last is how gtk4's widget set reaches it -- one component declaration per GTK widget class, generated from the host's introspection data -- and the first how the overrides written against it do. `nodeNative` reads `#[gen.native]` off the source rather than the checked package, since `Unavailable` is asked during a check and checking the target to answer would re-enter it.

A target that is unavailable contributes no package, which is a whole-package decision on purpose: `mergePlatformExtensions` walks *every* registered platform's source, so a platform loading overrides whose widgets it cannot also declare would report them as undefined in a build targeting something else. gtk4 is unavailable when its `gir` option names a file it cannot read; `DeclaredPlatform.Unavailable` asks the generator its node names.

**A platform is configured before the check, from the `output` block as well as the command line.** gtk4's `gir` option decides what the `gir:` import declares, so it is a check-time option, and `build.Check` hands each `codegen.OptionConfigurable` the literal options the first `output` block naming it declares -- `true` and `false` read by name, since both are declared, so a block's `gtk4(cgo=true)` selects the per-class protocol -- overlaid by `CheckConfig.Opts` (`--opt`). Only the command line configured it before, so a program whose block named a GIR was checked against the probed one and built against its own: a `#[moves]` the named GIR declared was not there to check, and its argument was freed (`testdata/gir_transfer_full.txtar`, `TestGIRSchemeFollowsTheOption`). A path is relative to the working directory, as `--opt`'s is.

The `#[builtin]` mark only stamps the kind on whichever IR the declaration
became. What a kind then *requires* — that a node kind names a component, that
a const kind names a const — is checked by `bindBuiltinRole`
(`internal/checker/builtins.go`), where the compiler stores the reference,
because that is where the requirement comes from. A duplicate mark is an error
there rather than a silent overwrite. Which declaration forms may carry a mark
at all is the AST's answer: a form implements `ast.Attributed`, and the parser
refuses a mark on one that does not.

**Built-ins are declared, not hardcoded.** The compiler identifies a built-in by
a `#[builtin("kind")]` mark on its `lib/` declaration, never by matching its
name — so every built-in is shadowable by a user declaration of the same name.
Type kinds (`int`, `color`, `datetime`, `list`, `option`, …) mark a struct;
node kinds (`errorBoundary`, `effect`, `context`, `output`, and
`sngl:ui/nav`'s `navStack`, `navPage`, `navLink`) mark a component, and the checker dispatches a visual node to the matching IR construct
off the mark. Six family kinds mark a *family* — itself a component, but one
that is never rendered: `familyOfFamilies` (the family of families), `familyRoot` (the
family a package body accepts), `familySpan` (the rich-text family), `familyShape`
(the drawing family), and `familyLanguage` and `familyPlatform` (`sngl:build`'s two
build-target tiers). Each is a family the compiler itself has to name — to
know what a family is, to check the body against, to know which node is a
rich-text flow, to know which node is a canvas, to know which node is a
target and of which tier (`ir.TargetTier`) — and every other family it
compares by declaration alone. Marking them is what keeps `ir.go` from holding
a package URI and a name for each: `ir.IsShapeFamily` and its siblings read
`Component.Builtin`, so a family may be renamed or moved and a program's own
`shape` family does not become the painted one. The widget family carries no
mark: no pass lowers it specially, and a family needs one only where a pass
does -- `ui.node` used to carry `treeNode` so that a span could be told by
elimination (a family, not the widgets, not the shapes), which every new family
would have been misread as.
The mark is declared in `lib/internal/marks` and implemented in
`internal/checker/marks_impl.go`; kinds are `ir.BuiltinKind`.

**sngl:builtin marks itself with private macros**, and imports nothing but
`sngl:build`. `lib/builtin/marks.sngl` declares `_builtin` and `_intrinsic`
-- the same implementations as `marks.builtin` and `marks.intrinsic`, returning the package's own `_Macro` -- so an ambient package
does not reach the compiler's tier, and its underscore names are no program's
to write. `_builtin` takes an enum member written bare, `#[_builtin(int)]`,
so its signature names none of the scalars its marks make; `_intrinsic(id string, …)` does name one, which resolves because a struct's kind is read off
its source as it registers (below).

**Marks run once the package is registered.** pass1 queues every mark it
meets (`afterMarks`, `internal/checker/marks.go`) and runs the queue after the
last declaration registers and before the initializers -- const, package var,
context default -- that may read what a mark said. So a mark may name a macro
its own package declares anywhere, with no registration order to arrange, and
a check that reads a mark is queued behind it: the intrinsic-with-a-body rule,
a const with no value, and what `finishFamilyMarks` asks of
`family.eventless` and the gen marks. Outside pass1's registration a mark runs
where it is met. Two things registration cannot wait for, and both are read
off the source rather than by running a mark. A struct's or family's
`#[builtin]` **kind** (`writtenBuiltinKind`, stamped by `stampBuiltinKind`):
`list[T]` in sngl:builtin's own signatures resolves to the kind's
constructor, and a family's members, registered after it (`familiesFirst`),
are measured against the family of families (`bindFamily`). The mark then binds
the role and refuses a kind the stamp did not read. And **publishing**
sngl:builtin's `list` and `map` (`publishBuiltinStruct`): `ListOf` attaches
the published one to every list type, so published after the marks, the
package's own signatures attached the previous build's and chained every
build's library to the last. One more reader that is not a function: a scope
notes a wildcard symbol when it binds it, so a component's wildcard is noted
again once its mark has set the pattern (`Scope.NoteWildcard`).

**A scalar's name is read off that source, not a table.** `bool`, `int`,
`string`, the sized numerics and the rest are each a `#[_builtin(kind)]`
struct, and `scalarUniverse` (`internal/checker/universe.go`) reads them off
sngl:builtin's parsed documents into the base scope `checker.NewSymbolTable`
builds, as the singleton `ir.ScalarType(kind)` stands for. That is what the
packages checked before sngl:builtin is in scope -- `sngl:build` and what it
imports -- resolve `string` against. Afterwards the name finds its
declaration, which `scalarOf` reads the same type off, and a program's own
declaration shadows both; a conversion `int16(x)` is any symbol `scalarOf`
answers. There is no list of scalar names in Go.

**Macros are not ambient.** A macro package is imported like any other:
`#[family.crosses]` needs `import "sngl:family"`, and `#[marks.builtin("...")]` and
`#[marks.intrinsic("...")]` need `import marks "sngl:internal/marks"` — which
is why every `lib/` file carrying a mark declares it. The alias is an
ordinary file-scope binding, so the mark follows it: `import t "sngl:family"`
means `#[t.crosses]`.

**Write the qualified form.** A dot import stays legal and supported — with
`import . "sngl:internal/marks"` the mark is the bare `#[builtin("...")]` —
but the repository's own source no longer uses one, so that what a reader
learns from is the qualified form. The alias is the package's last path
segment (`marks`, `ui`, `seq`, `draw` for `sngl:ui/draw`). `sngl:builtin` is
unaffected: it is ambient rather than dot-imported, and it is how `int`,
`string` and `color` are named.

A lib package may carry macros alongside its declarations — `sngl:family`
ships the `none` mark next to the `one` count — so the
`sngl` scheme is checked against the `lib/` layout alone: a directory is what
makes a package exist, macro-only ones included. `sngl:internal/<name>` is
the compiler's own tier: a package there may contribute macros, declarations,
or both. `internal/marks` declares only macros; `internal/ir` declares `Macro`,
the type a macro returns, and the flag enums the marks take.

Notable stdlib packages:

- **`i18n`** — translatable strings via `$"..."` syntax, lowered to `i18n.trInline(key, template, args)`. Supports ICU MessageFormat: plurals (`{n, plural, =0{...} one{...} other{...}}`), selects (`{x, select, key{...} other{...}}`). Manifest-backed translation; runtime locale from `LC_ALL`/`LC_MESSAGES`/`LANG`. Runtimes live in `pkg/{go,js,kotlin}/i18n/`. Go imports the runtime where an emitted spelling names it, as it does every package: `ir.Package.UsesI18n` counts a method of a component a test build rooted elsewhere never emits, and the import it raised was unused (`cmd/sngl/testdata/i18n_test_build_runs.txt`). Direct formatters: `i18n.numberInt`, `i18n.numberFloat`, `i18n.date`, `i18n.time`, `i18n.datetime`, `i18n.select`.

  **A translation's arguments are a struct**, a field per placeholder:
  `i18n.format(t, {name=n})`. `tr`, `trInline` and `format`
  take them as a type parameter constrained to a struct, `[A struct {}]`,
  which an anonymous struct literal binds and which refuses anything else;
  `$"..."` builds
  the same literal. Every runtime takes a string-keyed map, which only the
  emitted code holds: the IR keeps the record, and each language spells its
  runtime's map from the literal's fields (`codegen.RegisterRecordSpeller`,
  reached through `codegen.EmitIntrinsicCall`). `lower.RecordArgs` -- at the
  start of `optimize.Optimize`, and as the always-on `RecordArgs` pass --
  makes the argument of each intrinsic `ir.RecordArg` names a literal: any
  operand that is not one is read a field at a time with a spread site, so
  passSpreadOnce evaluates a computed one once. Such a literal names no
  declaration for the shake (`walkNames`), since nothing declares a struct
  for it. The interpreter reads a struct as the map.
  **A template is written in backticks**, or with its braces escaped: a
  double-quoted string is interpolated where it is written, so `{name}` was
  gone before i18n read it and the arguments were read by nothing. An
  interpolated `tr` key or `format` template is a positioned error for that
  reason (`error_i18n_args.sngl`, `i18n_struct_args.txtar`,
  `cmd/sngl/testdata/i18n_struct_args_runs.txt`). `select` keeps a
  `map[string, string]` -- its keys are data matched at run time -- and
  `PluralKey` stays a declared type, so the predeclared keys and
  `i18n.exactly` are the only ways to write one. html publishes its i18n
  runtime on `window` in test mode, as it does the page's functions: a test
  module is bundled beside the page, and an i18n call the optimizer inlined
  into a test body named nothing there.

  **A test that sets the locale reads the program's words in it, on every
  target** (`cmd/sngl/testdata/i18n_test_locale_runs.txt`):
  - An entry point is an intrinsic with a body, and `passCallDefaults` fills
    its defaults as any function's, since its emitter stands in for the body
    and reads every parameter by position.
  - The interpreter runs an entry point's body, which reads the `locale`
    context and hands it to the primitive, and answers only the primitives
    by id. It translates against the `i18n.manifest.json` beside the
    package's source (`pkg.SourcePath`).
  - android's Robolectric test loads the manifest before it mounts the
    screen, as `MainActivity.onCreate` does, since the test never runs
    MainActivity.
  - JavaScript emits a test body's entry-point call with the locale NoContext
    threaded, as Go does.

## HTTP and JSON

**`sngl:json` reads text as a declared type and writes a value back**:
`json.decode[T](text) async.Result[T]` and `json.encode[T](v) string`. The
type is its own schema -- a struct an object of its fields, an `option` a key
that may be missing or `null`, a `list` an array, a `map[string, V]` an
object of any keys, an enum carrying nothing its member's name, `datetime`
and `time.date` RFC 3339 text. A key the type does not name is ignored, a
field that is no option and has no key fails, writing leaves an absent
option's key out and visits a map's keys in order. A failure is a `decode`
`async.Failure` whose message every target words alike: `not JSON`,
`$.genres[0].rank: want an integer`, `$.name: missing`
(`testdata/json.sngl`, `cmd/sngl/testdata/json_runs.txt` on all six).

**A name on the wire is a fact about the format**, so it is the format's
mark: `#[json.identifier("page_count")]` on a struct field or an enum
member, `#[http.identifier("page-size")]` on a query struct's field, landing
on `ir.StructField.JSON`/`.HTTP` and `ir.EnumMember.JSON` and read by an
encoder alone (`WireName`). An enum member takes marks as a struct field does
(`EnumBodyItem = { MacroAttr } EnumBodyDecl`), and a comment on a body item's
mark line stays on it, as a declaration's does.

**`sngl:http` makes a request**: `send(method, url, query, headers, body)`
and `get(url, query)`, both `#[intrinsic(…, async)]` and answering
`async.Result[Response]`, a failure only where no response arrived
(`transport`, `timeout`); a status is read off the `Response`, whose header
names are lowercased. `Response.checked()` is SNGL -- 2xx the response,
401/403 `auth`, other 4xx `request`, 5xx `server` -- so every target
classifies alike, and `pathEscape` is a `const func` the optimizer folds.
**A query is a struct**, a field per parameter: an absent option left off, a
list repeating its key, everything else written once, every byte but the
unreserved characters percent-encoded. `get` is an intrinsic rather than a
SNGL wrapper over `send` because a generic wrapper hands the intrinsic a `Q`
that names a type parameter on a target that emits generics, and the encoder
needs the struct.

**What has no wire form is refused at the call** (`checkCodecCall`,
`ir.CodecProblem`): a function, a unit, a payload enum (not yet), a map keyed
by anything but a string, two fields written under one name, a query field
holding a struct -- and a type parameter, since the type has to be known where
the call is (`testdata/error_json.sngl`).

**Every language renders one plan** (`ir.PlanCodec`): the type's shape and
each field's and member's wire name, every struct and enum once so a type
holding itself points back at its own. Go hands it to `pkg/go/sngljson`,
which reads and writes the program's own types by reflection, and makes the
request with `pkg/go/snglhttp`; JavaScript to a page helper
(`javascript.CodecHelperJS`, published on `window` where a test calls it);
Kotlin to `SnglCodec`, written beside MainScreen.kt
(`codegen/platform/android/codec.kt`, org.json and `HttpURLConnection` on
`Dispatchers.IO`), whose descriptor also builds and reads each struct; the
interpreter walks the plan over its own values (`internal/interp/codec.go`).
The `Result` the call answers is built by translating IR around the names
the runtime handed back (`codegen.ResultOK`, `ResultFailed`, `HostValue`), so
each language's own spelling names the Result, Failure and Response.

**A call that waits waits as an async function does.** Go runs it off the
drawing thread through passAsyncOffload (a test waits where it calls, being
entered by the harness), JavaScript awaits it, and **Kotlin suspends**: an
async function is a `suspend fun`, an async test body runs in `runBlocking`,
and a handler lambda or an effect's mount that waits is started on the main
dispatcher by `SnglCodec.start`, whose `return` leaves the coroutine; a
function literal that waits, called where it stands -- an adapter's
`fetch=func(k) => call(k.a, k.b)` spliced into the mount -- is written in
place as a `run` block binding its parameters (`waitingLiteralCall`), since a
Kotlin lambda is no suspend function; and a test build's wait for idle also
waits for what `start` began. A test
waits for what a handler's request writes with `t.wait`, which Go now lowers
too (gtk4 pumping its loop meanwhile and bubbletea running what was posted,
`golang.LowerTestFile` being handed the platform).
`cmd/sngl/testdata/http_runs.txt` runs requests, a waiting handler and an
`async.query` over a request on six targets against the
server the script harness runs at `127.0.0.1:47819`
(`internal/testhttp`), and `testdata/http_json.txtar` is the code.

Three general things it needed. A method a library declares on a struct of
its own is promoted into the program like a library helper (`passLibFuncs`),
and `ExprCtx.Resolve` never answers a bare name with a struct's or an enum's
method, which made a local `checked` a Model field. **A generic library body
spliced at bound types names enum instantiations Monomorphize never saw**,
so `MonomorphizeEnums` copies them after the inliners, finding a copy the
first run made through `EnumDef.InstanceOf`. And a generic call's arguments
are inferred from what they state rather than under the call's own expected
type, an argument whose parameter names no type parameter binding nothing
(`testdata/generic_call_argument_unexpected.sngl`).
