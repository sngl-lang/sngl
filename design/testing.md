# Test infrastructure

- `testdata/` at project root contains `.sngl` fixture files (e.g., `test_arithmetic.sngl`, `component_simple.sngl`, `error_*.sngl`)
- `cmd/sngl/testdata/` contains CLI golden test files (`txtar` format)
- Test runners resolve testdata via relative paths from their package directory
- Error directive comments in test files (e.g., `// ERROR(check) "invalid color literal"` — phase is `parse`, `check`, etc.) drive expected-failure assertions via `internal/testutil`

**A fixture-first fixture the backends cannot emit yet carries `// SKIP(codegen) "reason"`.** Every platform harness compiles the *whole* of `testdata/` for its own target, so a fixture naming a construct no platform can lower does not fail one assertion — it takes that harness down. The directive is the fixture's own opt-out from codegen only: `TestFixtures` still parses, formats, checks and folds it, which is the point of writing the fixture before the implementation. The decision lives in one place, `testutil.CodegenSamples` (a `TestdataSamples` that drops the skipped) plus `RunComponentFixtures`; a platform harness walks testdata through those and never tests the flag itself. Remove the directive in the commit that makes the fixture emit.

**Txtar script tests** (`cmd/sngl/script_test.go`): each `.txt` file is a txtar archive with script commands at top and embedded files below `-- filename --` markers. The `sngl` command runs in-process. Use `stdout`, `stderr`, `exists`, `grep`, and `!` for assertions. A script gets `$WORK` (its directory), its own `XDG_CONFIG_HOME` so it never reads or writes the user's trust config, and `GOENV` pinned to the user's so the go command's own config survives the move. A script that builds a Go module against the checkout writes its go.mod with `exec sh -c` from `$SNGL_HOST_GO_MOD` (a `go` line included, or an offline `go mod tidy` needs the unpruned graph, the checkout's `require` blocks, or the pruned graph selects versions the module cache was never given, and its `replace` lines, or tidy looks up the version a replace stands in for) and runs `go mod tidy` under `GOPROXY=off GOFLAGS=-mod=mod`; `trust_eval.txt` is the pattern.

**Golden codegen fixtures** (`testdata/*.txtar`, run by `internal/goldentest`
from the root `TestGolden`): the archive's root-level files are one SNGL
package, `out/<lang>/<platform>/` is the generated code, and which targets run
is the source's own `output` block rather than a harness header. Seed or
refresh with `go test . -run TestGolden -update`. A `written <import path>`
line in the archive's comment adds what that import's plugin wrote, as its
store holds it, under `written/<scheme>/` -- the package an `openapi:`
import resolves to -- with its notes in `written/<scheme>/notes`. The compile is
`internal/build`'s — the same code `sngl generate` runs, which is why that
package exists.

**Assert on generated code with a golden, not a grep.** A substring assertion
cannot see the shape of what it matched or the order two statements came out
in. `passForElse` sets its flag as the loop body's first statement; moving it
to the last broke `break` and `continue` on every compiled target and passed
the whole suite, because the only assertion was `grep -count=1 '__ran0 = true'`. `testdata/for_else_imperative.txtar` is that fixture converted, and it
fails on all three targets under the same change.

**The two harnesses are split by what they assert, and must stay split.**
`cmd/sngl/testdata/*.txt` is the CLI's: flags, exit codes, `dump` stages,
error text, `--out` layout, and anything that compiles or *executes* generated
code (`sngl test --language go`, a browser run, a gradle build). Those keep
their `[!node]`/`[gtk4]`/`[chromium]` skip conditions and their real
subprocesses; a golden runs nothing, so moving them would lose the assertion,
not reformat it. `testdata/*.txtar` is the language's: given this program,
this is the code every target generates. A claim about the CLI goes in the
first; a claim about codegen goes in the second.

**A `sngl test` a script launches is recorded, as a golden's toolchain run
is.** Each launch of a generated test program -- every target but `none` --
is answered from a `testrun/<platform>/<lang>/<file>/<component>/<n>` file in
the script's own archive: a digest of the generated program and the snapshots
it was compared against, then the results, the error, and any snapshots the
run wrote, which a replay writes back. An ordinary run only compares the
digest, so CI verifies a gtk4, Chromium or Robolectric run with none of them
installed, and a script needs no `[!display]`, `[!chromium]` or `[short]`
guard around one. A digest that moved, or a launch with no record, fails
naming `-update`; `go test ./cmd/sngl -run TestScript/<name> -update` reruns
those launches for real, refuses to record one the host cannot run, and
rewrites the records, while `-verify` reruns every launch whatever its record
says, as a golden's does -- a toolchain or browser upgrade moves no digest.
The digest does not cover `pkg/<lang>/` runtimes, which the generated program
imports from the checkout -- a change there wants a `-verify` the digest will
not ask for, the same gap a golden's record has.

**One command rerecords every package's.** Records live in the goldens, the
scripts and four packages beside them -- gtk4's `go_runs`, html's component
and browser runs, `internal/build`'s library-sites run, `internal/lower`'s
goldens -- and a codegen change moves them all, while the first two were the
only ones anyone remembered to `-update`: the rest went stale until a full
verify. `go tool verify -update` runs, under the headless compositor, the
tests of every package whose tests declare an `update` flag with `-update`
(`recordPackages`), and a stale record's failure names it.

**A failure while the tree changed says so.** Some suites build from the live
checkout -- `sngl` proxies to `./cmd/sngl`, `internal/optimize`'s evalstore
and the scripts' `trust_eval` build modules that replace `duckfam.us/sngl`
with it, the `serve` script command builds an out-of-tree `sngl` -- so an
edit made mid-run is what they build, half-written or not. verify reads the
size and time of every file git keeps before its test steps (`treeState`) and,
when one fails, names what changed since (`reportTreeChange`). Building from a
snapshot instead would cost every run a cold build cache, which keys on the
absolute source directory, and write records into the copy.

**verify launches nothing a record answers.** It runs no `sngl test` of its
own: the examples are all `./...` reaches, and `examples_runs.txt` runs them
on every target from its records. Run unrecorded in verify, every launch
needed every toolchain and a display, so gtk4 failed in CI, where cage does
not start, and a slow fyne run timed out there.

**So is a component fixture's run in Chromium.** `TestComponentFixtures` in
`codegen/platform/html` runs every `testdata/component_*.txtar` declaring a
`func test…` through the same launcher (`internal/testlaunch`, in process),
answering each launch from a `testrun/html/none/<fixture>.sngl/<component>/<n>`
file at the end of the fixture's own archive, after the golden and its `run/`
record. A moved digest or a missing record fails naming `-update`, and
`go test ./codegen/platform/html -run TestComponentFixtures -update` reruns
those in a real browser, `-verify` all of them. On html the digest has no runtime gap: the
testagent and the i18n runtime are files of the generated page.
What a SNGL test cannot ask -- a DOM mutation count, a node's identity across
a render, a raise it has no way to expect -- is still a Go test driving
Chromium, through `inBrowser`, and recorded the same way in
`codegen/platform/html/testdata/browser_runs/<Test>`: a digest of the page it
serves and the test's own source file, so an edit to the assertions asks for a
run too.

**And so is a gtk4 test's `go build` or `go test`** of the program it
generated (`runGo` in `codegen/platform/gtk4`), recorded in
`codegen/platform/gtk4/testdata/go_runs/<Test>`: a digest of the files the
command runs over, its arguments, and the source of every package of this
module those files import, transitively -- so an edit to `gtk4rt` asks for a
run as an edit to the codegen does, and only a run that succeeded is recorded.
The program never depends on the host's GIR, because no test reads one (see
gtk4 in design/architecture.md).

**A test program that goes away before it answers its run fails it**
(`exitedEarly`, `cmd/sngl/testdata/test_program_exits_runs.txt`): a Go
target's stack overflow ends the program where no recover reaches, and the
driver read the end of its output as the end of the run, `ok 0 tests`, which
a script's `stdout ok` accepted. The test that was running fails, naming the
exit, and is recorded as any failure is: the crash is the program's answer,
not the host's, so it is no launch error, which is never recorded. A program
that exits with no test running is still one.

A claim of *absence* is the one thing a golden cannot state — it makes
absence visible, but no reader notices that something is not there. Those are
written as `deny` lines in the archive comment, naming a golden file (or `*`
for all of them), a backquoted regex, and a reason:

```
deny out/go/bubbletea/model.go `held := \[\]int` -- the held sequence is a range func, not a slice
deny * `\.toList\(\)` -- no target copies a progression into a list
```

Deliberately not `! grep`: rsc.io/script's `grep` wants a pattern and a file,
one argument is a usage error, and a usage error under `!` is a *pass* — six
such lines had accumulated, three asserting the absence of the very thing
their change was about (`TestScriptGrepsNameAFile` is the lint against the
class). A `deny` that names no reason, no file, a file no target generates, or
a pattern that does not compile is a failure.

**What a program does when it runs is an `exec` line**, for a Go target:

```
exec out/go/gtk4 --window x <clicks.txt -- shown.txt
exec out/go/gtk4 --show toplevels=2
exec out/go/gtk4 toplevels[Load]=2 -- want.txt
```

Its arguments, a standard input and the standard output it must print, each
file one of the archive's; `toplevels=N` counts gtk4's `set_title` requests in
its Wayland protocol log, of every toplevel or of the one titled, which is how
a run tells a window reached the screen. A verification builds the program
once its tests pass and runs each line inside the headless compositor the root
test binary re-execs under, and the target's `run/` digest covers the lines,
the files they name and the source of every package of this module the
program imports (`toolchain.HashModuleImports`), so the ordinary run replays
them as it does the build and an edit to `gtk4rt` or `fynelayout` asks for a
run. A program meant to be run carries the scenario that drives it -- a
timer, a `fmt.Println`, an `os.Exit` -- so it is a fixture of its own,
`<name>_runs.txtar` beside the `<name>.txtar` showing the plain code.

Neither harness fixes a fixture whose input never reaches the branch it means
to exercise: generated output is identical either way. Confirm a new fixture
*fails* when the behavior is reverted.

**Know which harness sees platforms.** `TestFixtures` (root package, `internal/fixtures`) is the one walk over `testdata/*.sngl`: each fixture is read, parsed, formatted, checked, folded and LSP-marked once, as its own directives ask. It checks against every registered language and platform via `internal/testtargets`, so a fixture *can* exercise platform element resolution and `component sngl.X` extension bodies. No fixture gets the real import resolver — directory imports resolve through the stub in `internal/fixtures/resolver.go`. For that, and for anything driven by CLI flags, use a txtar test in `cmd/sngl/testdata/`: it runs the real CLI. For generated output use a golden in `testdata/*.txtar`, described above.

**A test body is lowered by each language's own test emitter**
(`codegen/lang/{golang,javascript,kotlin}/testlower.go`), and the interpreter
is the reference every target is held to — `cmd/sngl/testdata/test_component_surface*.txt`
runs one program through every form of access a test body has on all six.
These rules keep them agreeing:

- **Test bodies are checked last**, after every component and window body:
  an unannotated component var has no type until its body is read.
- **A call of an instance's method carries the instance in front of its
  arguments** on Kotlin, where the composable's own call leaves it implicit,
  so the emitter splits it off (`carriedReceiver`) wherever the call is
  spelled -- by the scope's name or as a component's method -- or it was
  handed to the method as an argument once the call sat inside another,
  `c.g.rows().length()` (`cmd/sngl/testdata/test_method_answer_receiver_runs.txt`).
  The imports what a test body spells requires -- the i18n runtime a
  context's default names -- are written into the Kotlin test file beside
  the fixed list it starts from (`i18n_test_state_class_runs.txt`).
- **A test holds its instance, not `m` or `state`**: package state and the
  funcs reading it are spelled through the instance (`ExprCtx.StateReceiver`
  on Go and JS; on android the state members MainScreen spells as `state.<x>`,
  rebound to the instance). Each test starts from a fresh one — a new Model on
  the Go targets, a remounted page on html.
- **The component a test takes is rendered with no call site**, so each of its
  props holds its default, or its type's zero where it has none: a var of the
  component on a compiled target (`bindRootProps`), bound ahead of its own vars
  in the interpreter (`cmd/sngl/testdata/test_reads_root_prop_runs.txt`).
- **Tests taking no component build the program's window**, or nothing where
  the package body renders none (`ir.Package.IsProgram`): `testlaunch.Run`
  drops its components, each of which was otherwise an entry point emitted
  against a Model holding none of its state
  (`cmd/sngl/testdata/test_group_no_component_runs.txt`).
- **An event invoker takes the event's arguments**: `c.f.input("v")` hands
  the invoker `"v"`, which it puts in the widget before firing the one event,
  as android's `SetText` semantics action does.
- **Effects settle after every statement**: a compiled test settles after
  each write (`settleAfterWrites`), and the interpreter's runner after each
  statement as well as on each read through the instance, since a read of
  package state goes through none -- a click that unmounted and remounted a
  branch left `asked.length()` unsettled (`testdata/async_query_loop.sngl`).
- **A test waiting lets time pass as the program's loop would**: `t.wait`
  asks its predicate between turns of whatever runs the target's schedules.
  bubbletea's test build runs no program, so each turn settles the Model the
  test holds -- Update after what the test did, the commands it returns run
  off the test's goroutine and their messages posted back to it -- or its
  timers, tea.Tick commands, never ticked; android's moves Compose's test
  clock, where it lowered no wait at all
  (`cmd/sngl/testdata/timer_tab_loop_wait_runs.txt`). `t.tick` is the
  interpreter's alone.
- **A member named `length` is a list's, a map's or a string's measure only
  on one of those** (`ir.HasLength`): read off an instance it is the
  program's var, which Go wrote as `len(c)`
  (`cmd/sngl/testdata/test_reads_var_named_length_runs.txt`).

**A test reaches into what its component renders** -- `c.b.hits`,
`c.b.btn.click()`, `c.b.row[0].click()`, `c.b.reset()`, `c.r[1].pressed(7)`
-- and reads a node's one-way prop, `c.t.value`, whether the instance is
spliced or built at run time. Each emitter reaches only the instance's own
members by name, so the lowering says the rest in those terms, first in
`passTestReach` (`internal/lower/test_reach_flatten.go`), before anything
moves a node out of the body that wrote it:

- **A one-way prop is the expression its call site gave**, read through the
  instance (`rewriteTestPropRead`), under the providers around the node on the
  way from the test's instance, each value read through the instance it is
  written in (`underProviders`): a test is a root, so it read a page's words in
  its own language where a select above the page chose another
  (`cmd/sngl/testdata/test_reach_prop_under_provider_runs.txt`). `"{hits}"`
  in `bar`'s body is `"{c.b.hits}"` in the test, and a loop's variable is the copy the test
  indexed was handed -- the loop's list at that index, the number a
  `sngl:seq` sequence counts there, or the index itself (`loopBindings`). An
  inner loop's list is read with the outer loops' bindings in it, so
  `for var cell = row` under `for var row = cells` hands `cells[i][j]`, never
  a `row` nothing in the test binds; a fire's handler read that and the Go
  targets did not build (`cmd/sngl/testdata/test_reach_nested_loop_fire_runs.txt`).
  A method of the component its own body calls with the instance left
  implicit, `for var row = rows()`, is called through the instance, as a
  test calls one (`test_reach_prop_through_method_runs.txt`). A
  prop reading what a test cannot name -- a slot's parameter, a local, a pull
  sequence's element -- is left as written for a node of the test's own
  instance and refused with a position for one reached through another
  instance (`unnamedTestPropRead`). What a function literal in the
  expression binds is no such name: `names.map(func(n) => …)` reads `n`
  wherever it is read, a test included -- refused before, and so was any
  prop whose pure helper the optimizer inlined into one
  (`cmd/sngl/testdata/test_reach_prop_lambda_runs.txt`). The call has to
  still be there: the optimizer splices no node whose prop a test reads
  (`ir.PropReadIDs`), which left html's badge the `span` it draws one with
  and the read `""` (`test_reach_spliced_prop_runs.txt`).
- **A node is one of the test's instance's own**: `c.b.hit` becomes `c.hit`,
  and `c.r[1].tap` the copy of `c.tap` under key "1", the indices of each step
  kept outermost first -- the copy key a node inside an instance built at run
  time already carries (see *A test fires an event as the program would*). So
  a trigger, a null test and a node handed to sngl:test/browser are answered
  by every target's lookup by id and key. The id is renamed where another node
  carries it (`hit__reach0`), and a component rendered at more than one site
  is copied for the site a test reaches into (`badge__site0`), so its nodes
  are that site's alone (`testdata/test_reach_runtime_instance_sites.txtar`).
  A library component is copied the same way, on the build's copy of the
  package, so the declaration every other check reads is left alone
  (`internal/build/testdata/test_reach_library_sites.txtar`, run on all six
  by `TestTestReachLibrarySitesRun`, since only a Go test can hand a check
  library source).
- **A program component's event a test fires runs the handler its call site
  wrote**, with the test's arguments: the component emits its events from the
  program's own code, and the test stands in for that emit. The handler
  becomes a function of the component the call site is written in
  (`__fire_r_tapped__t0`, taking an index per loop around the site), reached
  through the instance chain as a method is; the interpreter runs the call
  site's handler itself (`componentValue.callSite`), and an event no call site
  handles is an error there. A library component's event still enters at the
  widget its override renders. A handler on any component but a primitive --
  a library body's and an override's included -- that forwards
  (`@chose { picked() }`) hands on explicitly what it was handed, given the
  parameters it left unbound (`forwardedParams`), since spliced into the
  component it handles, or copied into a test, it has no event of its own to
  forward from. Each parameter is held to the one it is handed to, a mismatch
  refused at the emit (`error_emit_forward_type_mismatch.sngl`).
- **A spliced instance's members are what the splice made of them**
  (`internal/lower/test_reach.go`): the inliner records each splice of a node
  carrying an id by its handle, and a test's select through it becomes the
  var's clone, the node selected off the instance by the id it now carries,
  or a call of the method's clone. By handle rather than by node, because a
  spliced body may be cloned again -- a population is, where the body inserts
  it -- and every clone shares the handle, which a rename of the id renames
  too. A var of one copy of an instance spliced a cell per copy, bubbletea's
  `c.r[1].taps`, is that copy's cell (`cellRead`). One spliced more than once
  under one id names no one instance and is refused.
- **A member of an instance built at run time is read off the instance the
  target holds** (`codegen.InstanceReads`, lowered by
  `codegen.LowerInstanceReads` to `<id>Read<Member>`, `<id>Call<Member>` and
  their `At` variants on the test's surface): fyne's and gtk4's Model holds
  each such record where its root is bound (`golang.HoldInstance`,
  `__inst_<id>` or `__insts_<id>` by key) and lets go in its Destroy; html's
  factory hangs the instance off its root element; android's composable
  registers a closure per member in `SnglInstances` under the entry its call
  site hands it (`testdata/test_reach_runtime_instance.txtar`). bubbletea
  builds none of them but a recursion, which keeps no state and has only
  props, nodes and functions reading neither for a test to reach -- each
  answered as a spliced instance's is -- and an event a test fires on one is
  the handler chain its call sites wrote, inlined into the test up to its own
  instance (`inlineRecursiveFires`): a handler's parameter it never reads is
  bound to nothing, its argument still evaluated where it calls anything --
  as the program functions it calls, in order, or bound and read into `_`
  where a `&&`, a ternary or a host call decides that; the
  handler's own locals and loop variables are written in with it, each local
  under a name of its own per fire; and a `return` in it sets a flag the rest
  of the handler is guarded by (`endReturns`), since a return there would end
  the test
  (`cmd/sngl/testdata/test_reach_recursion_runs.txt`,
  `test_reach_review2_runs.txt`).
- **A build of the whole program reaches a test's instance where the program
  renders it**, which is how a golden whose tests take two components is
  compiled: each is spliced, and each test reaches what its splice made of it
  (`testdata/test_reach_two_roots.txtar`). Nothing is refused there, since a
  build that roots at none emits no test. A node the splice put in the
  package body is no member of its component, so passReservedNodeIDs renames
  a test's select of a reserved id off its own instance itself
  (`test_reach_reserved_id_whole_program.txtar`).
- **A test reaches its instance through its own parameter**, which is all an
  emitter spells a member off: a local holding the instance, `var held = c`,
  or a node reached through it, `var t = c.p.t`, is replaced by what it holds, and a function the test hands an instance to
  is inlined into the test, as a statement or, where its body is one
  `return`, as a value (`passTestInstanceReach`,
  `testdata/test_instance_reach.txtar`). One that cannot be inlined -- a
  return from the middle, a call of itself, a local assigned again -- is
  refused where it is written (`cmd/sngl/testdata/test_instance_refused.txt`).
  A local holding a ref a conditional or a loop wraps -- an option or a list
  of the node -- reads the ref live where it is read, so one read after a
  trigger, an event fired, a method called or a write through the instance is
  refused at the read (`refuseMovedRefLocals`), where the interpreter's local
  keeps what it was handed. After is evaluation order: an expression's
  operands in the order they run, a call's arguments before the call, a
  write's value before the write. Each branch of an `if` or a ternary, the
  right operand of a `&&` or `||`, and a for-else's else start from the state
  before them, and what follows is moved where any path moved it. A loop
  whose body moves anything has moved it for the whole body and for a
  condition asked again, and a function literal that moves anything has moved
  it from where it is written, since it may be called anywhere after. One
  read before anything moves is allowed.
- **A trigger is an event of the receiver's type**, so the checker tags one
  through a nested instance (`nestedElementEvent`), and `c.row[1]` types as
  the node's component. html's `At` invoker indexes a constant loop's unrolled
  copies as it does the copies built at run time, and the interpreter takes
  `[0]` of a loop that rendered one copy as that copy.

`cmd/sngl/testdata/test_reach_nested_runs.txt` and
`test_reach_runtime_instance_runs.txt` run it on all six.

`internal/testtargets` is a separate package from `internal/testutil` on purpose — the platform tests are *internal* test packages (`package html`) that import testutil, so putting the codegen/platform dependency in testutil would close an import cycle.

**A Go type crossing into SNGL is tested with `sngltest/`.** A `go:` value
crosses in two halves — `pkg/go/consteval` encodes it, `codegen/scheme/golang`
types it — and nothing in the compiler forces them to agree.
`sngltest.CheckMarshal(t, v)` runs both over one value and reports which half
is wrong. It is a test helper, not a runtime package, which is why it sits
beside `sngl.go` rather than under `pkg/go/`: `pkg/<lang>/` is what generated
code imports, and the compiler must not be in that graph.

**Nothing typed invalid reaches a package that checked clean.**
`ir.TypInvalid` is the type of what the checker reported an error about, and
of a host declaration it marked unusable; anywhere else it is a type the
checker gave up on without saying so, which `IsAssignableTo` then lets any
misuse through. `TestFixtures` holds every clean fixture to it, override
bodies included, and `TestLibraryHoldsNoInvalidType` every library package
(`internal/fixtures/invalid.go`). A library function with no return
annotation infers its type from its body on demand, as a program's does
(`ensureReturnType`).

`TestFixtures` refuses a diagnostic reported twice: a declaration read twice
-- a component's var registered before its body and checked with it, a body's
declarations bound again for every body in their scope -- reports once, the
other read resolving quietly (`resolveTypeQuietly`, `declareBodyDecls`).

When adding a fixture or directive, confirm it *fails* when the behavior is reverted. Several directives in this repo assert conditions that no test actually evaluates.

Two ways that check passes for the wrong reason. `go test ./internal/checker/`
does not evaluate a new `// ERROR(check)` fixture; the root package's
`TestFixtures` does. And the `sngl` on `PATH` is a proxy that resolves
`./cmd/sngl` against the current directory, so a binary built out of tree has
to be *run* out of tree too, or "does this fail when reverted?" compares HEAD
with itself and says ok.
