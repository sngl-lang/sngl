# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Reviewing changes

**Start with `testdata/`.** Every language feature in SNGL has at least one fixture in `testdata/*.sngl` that exercises it. Reading those fixtures is the fastest way to understand what a change is meant to do and to spot gaps. When proposing a feature, write the fixture first and let the test framework drive the implementation. Fixtures use directives like `// ERROR(check) "msg"` and `// FOLD(...)` to assert behavior at specific compiler phases — see `internal/testutil/sample.go` for the framework. A fixture written ahead of the lowering that would let a backend emit it carries `// SKIP(codegen) "reason"` (see `design/testing.md`).

Every fixture is written the way `sngl fmt` writes it, and
`TestFixtures` says so, so add one with `sngl fmt` rather than by
hand. A fixture whose exact layout is the thing under test opts out with
`// NOFMT "reason"`; the directive exempts it from that test only, so do not
run `sngl fmt` over the tree while one is in it. An `// ERROR(...)` directive
names the line it sits on, which is why the formatter keeps a comment on the
line it was written on.

## Where the rest is written

**What the language means is `docs/reference/specification.md`**, whose
grammar blocks are generated from `internal/parser/sngl.ebnf`, and how to write
it well is `docs/reference/effective-sngl.md`. Read the spec's section before
changing what a construct means, and change the spec with it. A function is
`func f(…) [T] { … }` or `func f(…) => expr`; `func f(…) -> T` is not SNGL.

**How the compiler holds it is `design/*.md`**: each rule, the bug that
motivated it, and the fixture that pins it. Neither is loaded automatically.
**Read the `design/` file for the area you are changing before you change
it**, and grep `design/` for a function or pass name before concluding nothing
explains it. A note there that only says what a construct means belongs in the
spec instead.

- `design/language.md` — `const`; bodyless components and overrides; events; loops and for-else; view walks (`ir.ViewBlocks`, `blocks.go`); lambda bodies; `match`; payload enums; `async.query`; contexts
- `design/architecture.md` — codegen registry and models, per-platform details, `ir`/`checker`/`optimize`/`lower` packages and the always-on passes, runtime `pkg/` packages, AST
- `design/stdlib.md` — `lib/` tiers and what each package holds, package docs, library loading/caching/freezing, clones, target nodes and reserved names, `#[builtin]`, marks, scalars, macros, i18n; `sngl:http` and `sngl:json`
- `design/families.md` — families, data components and records, generated families (`gen.emit`), family inference and membership, `#[family.crosses]`, boundaries and raises
- `design/windows.md` — `root`, windows, prop reads through an `#id`, self-reference and declarative cycles, view-block state, root components, `output` and `@run`, how each platform shows windows, unavailable targets
- `design/slots.md` — slot types and counts, spreads and variadics, rest slots, entries, fallbacks, `.empty`, slots in runtime instances, populations
- `design/nodes.md` — `#[intrinsic]` and native marks, `go.chan`/`go.select`, element refs and presence, event parameters, handles and prop writes, `#[foreign]`, pure funcs
- `design/instances.md` — body-scoped declarations, `__instN`, override bodies and helpers, runtime instances, render slots, unbound two-way props, bubbletea state/focus, nested funcs
- `design/markup.md` — `sngl:ui/markup`, the `md:` scheme, `md.document`
- `design/images.md` — `ui.imageSource` and each target's answer
- `design/plugins.md` — plugin principles and known gaps
- `design/import-schemes.md` — plugin schemes, `c:` and `openapi:`
- `design/navigation.md` — `sngl:ui/nav` on each target
- `design/types.md` — anonymous and generic structs, monomorphization, units and their display, member lookup
- `design/testing.md` — fixture directives, goldens and `deny`, recorded runs, the two harnesses, test lowering and test reach
- `design/performance.md` — profiling, the store (`internal/gencache`), what costs
- `design/trust.md` — grants, `--allow-eval`, fetch gating, the LSP
- `design/ownership.md` — `#[owned]`, `#[retains]`/`#[moves]`, the four lifetimes, `ir.Keyable`, the interpreter's handles and leak check, application teardown

**Where a new note goes.** A rule a session must follow whatever it is
working on goes here, in a sentence or two. The reasoning behind a design --
what it replaced, which bug it fixed, which fixture pins it -- goes in the
`design/` file for its area, or in the commit message. Do not append a
paragraph here per change: this file is loaded into every session.

## Rules that hold everywhere

- **`ir.IsConst` asks the declaration, never the inferred purity.** Every
  component and override a target package declares must be const.
- **Built-ins are declared, not hardcoded.** The compiler finds a built-in by
  its `#[builtin("kind")]` mark, never by name, so every one is shadowable.
- **Every phase after the check works on a copy** (`ir.ClonePackage`); the
  checked package and the library are shared and frozen, and writing one
  panics.
- **An AST is immutable once parsed.**
- **A capability not written is not held**: a target that says nothing in
  `#[gen.can]` gets every lowering pass.
- **A walk over a view asks `ir.ViewBlocks`; a walk over everything is
  `ir.Walk`.** Never hand-write a switch over statement kinds.
- **Write the qualified form of an import** (`ui.text`, `#[marks.builtin]`);
  dot imports are legal but the repository does not use them.
- **A nil `*trust.Policy` refuses everything.**

## Build & Test Commands

```bash
go install ./cmd/sngl          # build CLI (preferred over go build)
go build ./...                 # verify all packages compile
go tool verify                 # full test suite with coverage
go test ./path/to/pkg/...      # test a single package tree
go test -run TestName ./pkg/   # run a single test
go fmt .                       # format Go (run from package dir)
go tool docsgen                # build docs site to _site/
```

`githooks/pre-commit` refuses a commit over the two `verify` steps that fail for
purely mechanical reasons — `go fix` rewriting a loop, `mdox fmt` reformatting a
paragraph — because neither is visible until CI says so. It checks staged files
only and takes about a second. Install it with `git config core.hooksPath githooks`; `git commit --no-verify` skips it.

`mdox fmt --check` covers every markdown file `verify` finds, a scratch note at
the repository root included, so run `go tool mdox fmt --soft-wraps <file>` on
one before committing it.

WASM build (used by docsgen for playground):

```bash
GOOS=js GOARCH=wasm go build ./internal/playground
```

## Architecture

SNGL is a UI language that compiles to multiple platforms. The pipeline:

```
.sngl source → Parser → AST → Checker → Optimizer → Lower → Platform+Lang Codegen → Output
```

**Public API** is in `sngl.go`: `Parse`, `Format`, `FormatTo`, `FormatExpr`,
`Check`, `Convert`. Keep this file intact as the stable surface.

- `ast/`, `internal/parser/` — syntax, lexer, parser, formatter
  (`internal/parser/sngl.ebnf` is the grammar)
- `internal/checker/` — two-pass type checker producing `ir/`
- `internal/optimize/` — folding, DCE, the per-document `Fold`
- `internal/lower/` — capability-gated IR→IR passes; entry `lower.Lower`
- `codegen/lang/{golang,javascript,kotlin}`, `codegen/platform/{html,fyne,gtk4,bubbletea,android}` — generators
- `internal/interp/` — the interpreter, the reference every target is held to
- `internal/build/` — what `sngl generate` runs, shared with the golden harness
- `lib/<path>` is `sngl:<path>`; `lib/platform/<name>` and
  `lib/language/<name>` are the targets; `pkg/<lang>/<name>` are runtimes
  generated code imports

## Tests

- `testdata/*.sngl` — language fixtures, run by the root `TestFixtures`
  (not by `go test ./internal/checker/`); their test functions run on the
  interpreter in `TestRunFixtures`, `SKIP(codegen)` fixtures included.
- `testdata/*.txtar` — codegen goldens (`go test . -run TestGolden -update`).
  Assert generated code with a golden, and absence with a `deny` line, never a
  grep.
- `cmd/sngl/testdata/*.txt` — CLI script tests, including anything that runs
  generated code.
- Toolchain, browser and gtk4 runs are recorded; `-update` after a codegen
  change, `-verify` to rerun them all.
- **Confirm a new fixture fails when the behaviour is reverted.** The `sngl`
  on `PATH` resolves `./cmd/sngl` against the current directory, so run an
  out-of-tree binary out of tree.

## Debugging

Structured logging via `slog` at three levels controlled by CLI flags:

- `-v, --verbose` — Info: phase timing, file discovery, external commands run
- `--debug` — Debug: const folding, dead code elimination, import resolution
- `-q, --quiet` — Error only

`sngl dump` inspects each compiler phase. It ships in release builds but is an
internal debugging tool and unsupported: its output in every format may change
in any release, and it is not snapshot tested --
`cmd/sngl/testdata/dump_every_stage_spew_json.txt` only checks that each stage
and format runs without error. Assert a compiler property with a golden or a
`sngl test`, not with a dump.

```bash
sngl dump --stage parsed  [file|dir]                              # after parse + merge
sngl dump --stage checked [file|dir]                              # after type check
sngl dump --stage optimized --lang js --platform html [file|dir]  # after optimization
sngl dump --stage analysis --lang js --platform html [file|dir]   # CommonAnalysis as JSON
sngl dump --stage lowered --after none [file|dir]                 # pre-lower IR
```

The stage is a **flag**, not a positional argument — `sngl dump checked f.sngl`
fails with "accepts at most 1 arg(s)". `--format` selects `sngl` (default),
`spew`, or `json`; `--omit AST,Pos` trims noise. The analysis stage defaults to
`json` instead: it dumps facts *about* a program rather than a program, and
those have no source form — `--format sngl` on it is an error naming the two
that work.

In `json` and `spew` a declaration is written in full once, carrying an
`"(id)"`, and everywhere else it is a reference naming it by that id, its
kind, name and package (`cmd/sngl/dumpomit.go`). A declaration another
package makes -- the library, a directory import (`dir:widgets`) -- is only
referenced.

A positional argument may also be a package rather than a path:
`sngl dump --stage checked sngl:platform/gtk4`. It arrives already checked,
because a library package loads under the rules that permit its own
`sngl:internal/` imports and because a target synthesizes part of it with no
file on disk. `check` and `generate` take one too; `fmt` does not, since it
rewrites files and a package has none.
