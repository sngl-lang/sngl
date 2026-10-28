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
  `TestProvidedDocsSurviveBuilds` builds every golden fixture and then compares
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
- **Cloning is the largest remaining cost.** `build.Emit` clones the checked
  package for every target, and `ir.ClonePackage` copies by reflection
  everything reachable, library IR included.
