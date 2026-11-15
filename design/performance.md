# Performance

What the compiler costs is measured in total work — CPU and bytes allocated —
not only wall time: it runs beside everything else on a developer's machine,
so a change that finishes sooner by doing the same work on more cores (running
targets in parallel) is not a saving.

- **Profile.** `SNGL_CPUPROFILE=f.prof` / `SNGL_MEMPROFILE=f.prof` on any
  command; env vars rather than flags so a tool driving a build need not
  thread one through. Set `SNGL_NO_PROXY=1` when timing the installed binary,
  or the first run measures `go tool` rebuilding it. A benchmark over
  `examples/` in a gitignored `tmp/` package (`build.ParsePackageFS` →
  `build.Check` → `build.Emit`, slog discarded) gives the warm per-phase
  numbers a one-shot CLI profile is too short to show.
- **An AST is immutable once parsed.** Nothing after the parser writes to one,
  which is what lets the embedded library tiers (`parseStdlibDocs`), target
  packages included, be parsed once per process and shared by every check.
  `TestGolden`, once it has built every fixture, compares
  each target package's shared document with a fresh parse; a phase that needs a modified tree
  builds IR or a new node, never an edit.
- **A library package is loaded against the targets it belongs to.**
  `CheckLibPackage` selects a target package's own target (`ownTarget`);
  selecting none loads every registered target's overrides, gtk4's GIR
  included.
- **The collector runs at Go's defaults, on purpose.** GC is a large share of
  a build's CPU, and the fix is allocating less: a `GOGC`/memory-limit value
  tuned against today's allocation profile goes stale as that profile shrinks,
  and is tuned to one machine besides.
- **The docs site (`go tool docsgen`) is the stress case**: one html build
  writing ~1200 files, where per-page costs dominate. Its compile step is
  `sngl generate --allow-eval=go:duckfam.us/sngl/docs --allow-eval=go:duckfam.us/sngl/docs/lookup --platform html --lang none website.sngl` once `internal/playground/assets/sngl.wasm.gz` is
  staged.
- **What another process produces is SNGL, and it is stored.** A step
  whose output the compiler reads but did not write -- the compile-time
  evaluator's run, gtk4's GIR -- is a *producer* registered with
  `internal/gencache`, and what it hands back is a SNGL file whose root
  `cache.inputs` directive (`sngl:x/gen/cache`) lists every input it read,
  recorded before reading it. The store is separate from the file: keyed by
  the request and the compiler's identity (the executable's hash, since a
  producer's code is an input nothing in a file can record), it re-checks the
  recorded inputs and hands back the stored file when all still hold. Under
  `os.UserCacheDir()/sngl/gen`, `SNGL_GENCACHE_DIR` to move it,
  `SNGL_GENCACHE=off` to run every producer, 512MB and 30 days LRU. A
  producer written in SNGL adds its own identity to the key
  (`Request.Identity`, the digest of its package closure), since editing it
  changes every answer while every input it recorded still holds. `Default`
  is the store the environment names *when asked*, and `ResetDefault` forgets
  them: a store memoizes each input's verdict for its life, which is one
  build's, and the in-process script harness runs many.
  An `entry` input names another producer's output by digest, which is how a
  consteval value depends on the `go.deps` closure of the packages its
  program imported without restating hundreds of files, and on whatever of the
  compiler's own generated source the evaluator read (`codegen.GeneratedInputs`,
  reported as `//gencache` comment records in the results file). A batch
  producer uses `Lookup`/`Put` rather than `Get`: the evaluator looks each call
  up and runs its misses as one program. The docs site's warm compile answers
  every call from the store and runs no subprocess. js: calls are not stored
  yet, nor are go: import declarations -- both are producers still to move.
  A plugin's handler is the third producer, `sngl.scheme` (`internal/plugin`):
  what it records reading, plus each input a literal `cache.inputs` in what it
  *wrote* names -- what a process it ran read, which no recorder sees -- with
  any digest or value left out filled in by `Store.Complete` as it is stored.
- **A folded call reads the package env through a frame.** The optimizer
  interprets a const func against one env holding every const of the package
  (`interpEnv`), and runs each call in an `interp.Env.Frame` over it rather
  than a `Snapshot`, which copied all of them per call and again per lambda
  call inside it -- a `.filter(func…)` in a function folded per node. Safe
  because a pure body writes none of the package's bindings. `RebindFrom`
  walks what its source wrote, not what the destination binds.
  `BenchmarkFoldStyledPage` (`internal/optimize`) is the shape.
- **A pure function's folded answer is kept** in the compilation's
  `EvalCache` (`interpretCached`), by function, package and arguments --
  html folds one Style through `css` node after node. The arguments are keyed
  by `interp.AppendValueKey`, which spells each value with its type and
  refuses one that is no constant (a function, an owned handle); a key made
  with `%v` printed a struct's strings unquoted, so two different arguments
  shared one answer. A hit hands back a copy.
- **Cloning is the largest remaining cost.** `build.Emit` clones the checked
  package for every target, and `ir.ClonePackage` copies by reflection
  everything reachable, library IR included.
- **A derived function keeps its answer between writes** in the program a
  build writes, on the targets whose reactivity is lowered
  (`internal/lower/memo.go`). Every update reading `rows()` called it, and
  so did each derived function built on it: an answer of the Pokédex's Moves
  tab, 237 of Mew's moves, built them six times. One taking no parameter but
  the instance and scalars, reading only vars and what pure calls answer,
  and called by no var's initializer and no work `async.start` begins (a
  goroutine in Go, which would write the cell beside the drawing thread)
  keeps its answer with the count of writes it was computed at, and each
  write of a var it reads moves the count. A parameter -- the locale
  passContext hands it -- is kept and compared too. It cut the page's CPU by
  about a tenth in Go and C (November 2026).
  `testdata/derived_kept_until_written.txtar`,
  `testdata/derived_kept_not_under_async_start.txtar` and
  `cmd/sngl/testdata/derived_kept_runs.txt` pin it.
- **A prop setter handed the value it holds does nothing**
  (`lower.propSetter`). A render hands every instance it holds each of its
  props again, and the setter wrote the cell and ran every update reading it
  whether the value had changed or not: each of the Pokédex's 237 move rows
  re-rendered for every move's answer, though one row at most had changed. A
  value every target's == compares by value -- a scalar, a string, an enum
  without payloads, a single-base unit, and an option or a program's struct
  of those -- is compared first. A map and a function are written whatever
  they hold, and so is a list: compared, the Pokédex's gtk4 Go tests left its
  forms unrendered and its evolution rows removed a freed widget, and no
  smaller program has reproduced either yet. With the derived function's cell
  above, Mew's Moves tab took 3.5 s of CPU on C rather than 10.6 s; with
  lists compared too it took 1.8 s. A test build's setter still stamps the
  copies of an `#id` its render started afresh before it returns
  (`lower.clearCopies`). `testdata/prop_setter_skips_an_equal_value.txtar`,
  `cmd/sngl/testdata/prop_setter_equal_value_stamps_its_copy_runs.txt` and
  `codegen/lang/c/testdata/run/equals.txtar` pin it.
