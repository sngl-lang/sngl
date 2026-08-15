# SNGL Bug Audit — General Correctness

Findings from a strategic-sample audit (Parser, Checker, Optimizer, Lower, Interp, html platform, scheme/http, LSP). Ranked by severity: **crash** (panics on plausible input), **wrong-output** (incorrect values silently), **latent** (latent crash/leak under specific conditions), **cosmetic**.

---

## CRASH

### 1. `sngl.Check` / `sngl.Lower` not protected by `recover`
- **RESOLVED**: `Check`, `Lower`, and `Convert` now wrap their bodies in `recover` at the `sngl.go` boundary (Check → internal-error diagnostic, Lower → error, Convert → nil), mirroring `Parse`. Regression test in `sngl_test.go`. Remaining: the string-returning `Format`/`FormatExpr`/`FormatType` (no error channel) — see #17.
- **file**: `sngl.go:63`, `sngl.go:78`
- The package-level `Parse` wraps `recover()`, but `Check`, `Lower`, and `Convert` do not. The pipeline contains 100+ `panic(fmt.Sprintf("unhandled %T", ...))` guards across `internal/checker`, `internal/lower`, `internal/optimize`, `internal/interp` (155 total `panic(` sites). Any malformed IR or unexpected node — including from a checker error-recovery path that emits an unusual node — crashes the embedding host (LSP, playground WASM, docsgen).
- **repro**: feed a syntactically malformed-but-parseable program whose checker error-recovery path produces a stmt/expr type that any `lower/*.go` switch doesn't cover.
- **severity**: crash (host process)

### 4. `evalIndex` map key collisions
- **file**: `internal/interp/eval.go:822-836`
- For `map[string]any`, the code does `key := fmt.Sprintf("%v", idx)` — int `1`, float `1.0`, and string `"1"` all collide on the same key. For miss with `Type == Dyn`, it samples the map by range — non-deterministic.
- **repro**: `m[1] vs m["1"]` with a `map<dyn, T>`.
- **severity**: wrong-output
- **DEFERRED**: the non-deterministic sampling half is fixed (#24). The collision itself is inherent to the `map[string]any` runtime representation — keys are stringified on both write and read, so a fix needs a type-discriminated key scheme applied consistently across every map write and read path (a representation change, not a local patch). Tracked separately.

### 5. `i18n.tr`-style native call paths assume arg count
- **file**: `internal/interp/builtins.go:24-203` (all entries)
- None of the `nativeMethods` validate `len(args)`. If the checker ever lowers a partial call (e.g. error-recovery path with default args missing), these panic with index-out-of-range.
- **severity**: latent crash

---

## WRONG-OUTPUT

### 6. `evalBinary` BinMod ignores divisor=0
- **RESOLVED**: BinMod now guards `r == 0` → "modulo by zero" error, matching BinDiv.
- **file**: `internal/interp/eval.go:1043-1044`
- BinDiv guards `r == 0` (returns error). BinMod calls `math.Mod(.., 0)` which returns NaN — no error. Same hole in `execAssignOp`/AssignMod at `internal/interp/exec.go:328`.

### 7. `execAssignOp` AssignDiv ignores divisor=0
- **RESOLVED**: `ApplyOp` now returns `(any, error)`; AssignDiv and AssignMod guard divisor=0. All 5 call sites (interp exec + none testrunner SetField) propagate the error. Regression test in `internal/interp/arith_test.go`.
- **file**: `internal/interp/exec.go:325-326`
- `numericResult(toFloat(cur) / toFloat(val))` produces ±Inf / NaN with no error. The plain `BinDiv` path checks divisor; `AssignDiv` (`x /= 0`) does not.

### 8. Integer arithmetic silently promotes to float
- **file**: `internal/interp/eval.go:1025-1042`
- `BinAdd/Sub/Mul/Div` always go through `toFloat()`/`numericResult()`. For two `int` operands, this introduces float64 rounding error for values > 2^53. The const folder (`internal/optimize/consteval.go:512`) keeps int+int in int — so interp and folded values disagree at large magnitudes.
- **severity**: wrong-output (rare)

### 9. `evalSelect` returns nil for missing map field
- **file**: `internal/interp/eval.go:780-781`
- `m[e.Field]` on `map[string]any` returns zero (nil) silently rather than an error. The same path errors for non-map types (line 783). Test runner thus accepts misspellings as "value is nil".
- **DEFERRED**: erroring on an absent key is a runtime-semantics change. Whether it's safe depends on SNGL struct-field-presence guarantees (are all struct fields always materialised in the backing map, so absent ⇒ only a checker-caught misspelling?). Needs that confirmation plus a full test-suite pass before flipping; a `v, ok := m[field]` two-value guard is the intended shape.

### 10. Constant folder integer overflow is ignored
- **file**: `internal/optimize/consteval.go:517-522`
- `li + ri`, `li * ri` in `numericOp` use Go int arithmetic with no overflow check. Folded value at compile time differs from runtime (where the same int arithmetic also overflows, so consistent in Go) — but the interp eval uses `toFloat` (#8), so folded vs interpreted results diverge near int64 limits.
- **severity**: wrong-output (rare)

### 11. `resolveQualifiedType` emits errors at `ast.Pos{}`
- **file**: `internal/checker/resolve.go:195, 200, 213`
- Errors for unknown namespaces / unknown types are emitted with zero position. User sees the diagnostic at 1:1 instead of the actual reference site. Same pattern at `resolve.go:31`.
- **severity**: cosmetic (poor UX)

### 12. Static stdlib slice exposed by value
- **file**: `internal/checker/stdlib.go:22-51`
- `StdlibDocs()` returns the package-global `stdlibDocs` slice directly. Callers in `docs/lookup`, `docs/targets`, `internal/lspcore` only iterate, but an `append` by a future caller would write into shared state. Return a defensive copy.
- **severity**: latent

---

## LATENT

### 13. `pureCache` keyed by `Sprintf("%v", args)` — type collisions
- **file**: `internal/optimize/goexec.go:27`
- Cache key is `fmt.Sprintf("%s:%v", nativeType, args)`. `fmt.Sprintf("%v", []any{1})` and `fmt.Sprintf("%v", []any{"1"})` print as `[1]` — collision. Two different calls return the same cached value.
- **severity**: latent wrong-output

### 15. LSP `if err == io.EOF` won't match wrapped EOF
- **file**: `internal/lsp/server.go:98`
- `bufio` can return a `*net.OpError` wrapping EOF on TCP. Use `errors.Is(err, io.EOF)`. With the current code, the EOF path is skipped and the connection logs "read: <wrapped EOF>" before closing.
- **severity**: cosmetic→latent (logs noise; rarely missed-cleanup)

### 16. `string.substring` and `string.length` byte-not-rune
- **file**: `internal/interp/builtins.go:178-197`
- Operates on byte indices. `"é".length` returns 2, `"é".substring(0,1)` returns the leading byte of a multi-byte rune (invalid UTF-8). Test runner output disagrees with browser runtime that uses JS string ops (UTF-16 code units).
- **severity**: wrong-output (i18n/unicode)

### 17. Public API `Convert` / `Format*` panic-prone on partial IR
- **file**: `sngl.go:36-58`
- No recover. If a host (LSP, doc tooling) passes a partially-constructed AST/IR, a panic in `ir.Convert` propagates. Parser is protected; nothing else is.
- **PARTIAL**: `Convert` now recovers (returns nil). Still open: `Format`, `FormatExpr`, `FormatType` return a bare string with no error channel, so a recover would have to substitute "" — deferred pending a degradation-contract decision. `FormatTo` (has an error return) is a clean candidate to wrap next.

### 18. `LSP.RunTCP` leaks preview servers on accept-loop errors
- **file**: `internal/lsp/server.go:65-91`
- Each accepted conn creates a new `previewServer` with its own listener. If `srv.serve` returns via panic (no recover in goroutine), `conn.Close()` runs but `srv.preview.Stop()` only runs on EOF/read error. Any panic above that point leaks the preview listener.
- **severity**: latent leak

### 20. `parseStdlibDocs` swallows parse errors
- **file**: `internal/checker/stdlib.go:44-47`
- Stdlib file with a parse error is silently skipped — checker proceeds with a partial stdlib, and downstream errors look unrelated. Worth at least a `slog.Error`.

### 21. http scheme extractor doesn't reject tar symlinks
- **file**: `codegen/scheme/http/http.go:206-217, 241-260`
- `extractTarGz` writes any non-dir entry (including `tar.TypeSymlink` and `tar.TypeLink`) through `extractOne`, which calls `os.Create` and copies the reader. Symlink content is the *target path*, written as bytes — wrong-output rather than security, since the symlink isn't created.
- **severity**: wrong-output (would matter once a real symlink is hit)

### 22. http archive extract: cleanup races on `os.Rename`
- **file**: `codegen/scheme/http/http.go:96-102`
- If concurrent processes race to populate `cacheDir`, the loser does `os.Rename(tmpDir, cacheDir)` which fails because cacheDir exists. The fallback only checks `Stat(cacheDir) != nil` — but the winner's contents may still be writing/incomplete. No mutex / lockfile.
- **severity**: latent

### 23. Parser recover registered after `tree == nil` early return
- **file**: `internal/parser/parse.go:38-48`
- The `defer recover` (line 42) is registered *after* the `if tree == nil { return ... }` short-circuit. Means panics from `Tokenize`, `encode`, or remapErrors are not recovered. They reach the top-level `recover` in `sngl.go:27`, but library callers using `parser.Parse` directly (lspcore, snapshot) lose the protection.

### 24. `evalIndex` zero-value sampling is non-deterministic
- **RESOLVED**: sample the value at the smallest (sorted) key instead of ranging, so a dyn-map miss yields a deterministic zero-value template.
- **file**: `internal/interp/eval.go:833-835`
- For map miss with `Dyn` value type, samples an existing value via `range m`. Map iteration is randomized in Go — the zero-value template differs run-to-run. Test runs become flaky.

### 25. Reactivity panics on nested slot constructs
- **file**: `internal/lower/reactivity.go:982-993`
- `panic("nested *ir.If with its own slot — not yet supported")`. Any user program that triggers this nesting (e.g. condition inside a list) crashes the compiler. Should be a checker error, not a lowering panic.

### 26. WASM playground builds depend on `goexec` having a `_js` stub
- **file**: `internal/optimize/goexec_js.go`
- Stub exists and returns an error. But `consteval.go` calls `execPureGoFunc` from non-build-tagged code — every WASM compile that touches a pure-go-import path will surface "compile-time Go execution not available in WASM" as a diagnostic. Confirm the playground gracefully handles that error path, or skip the call when GOARCH==js.
- **severity**: cosmetic (UX)

### 27. `goLiteral` codepath via `Sprintf("%v")` for args is fragile
- **file**: `internal/optimize/goexec.go:122-...`
- Args are formatted into Go source via `goLiteral`. The cache key uses `Sprintf("%v", args)` (#13), but the *call site* uses `goLiteral` which presumably escapes strings. Different escaping in two places → desync between cache key uniqueness and code that runs.

### 28. Many `panic(fmt.Sprintf("unhandled %T"))` in lower/ are not under recover
- **file**: `internal/lower/*.go` (60+ sites)
- `sngl.Lower` does not install a recover. If checker outputs a new node kind that any lowering pass doesn't case on, the CLI / LSP crashes instead of reporting an internal-error diagnostic.

### 30. Tar extraction doesn't check `hdr.Size` vs disk
- **file**: `codegen/scheme/http/http.go:206-217`
- A zip-bomb-style tar.gz from a malicious import URL is extracted verbatim (no size cap, no entry-count cap). DoS via large `http://` import.
- **severity**: latent (assumes trust on import URLs)

### 31. Recursive go-lib (gomobile) func renders its self-call with a Model receiver
- **file**: android `go { android() }` path — `codegen/platform/android/gogen_ir.go` `emitGoLibIRFunc` (signature) + the body via `gc.EvalStmt`
- A top-level func emitted into the android Go library that calls itself renders the recursive call as `m.fib(...)` (Model-receiver method) inside a free function `func Fib(n int) int { ... }` — there is no `m` in scope, so the generated Go does not compile. Repro: `func fib(n int) int { return n < 2 ? n : fib(n-1) + fib(n-2) }` targeting `go { android() }` → `golib/golib.go` emits `return ternary((n < 2), n, (m.fib((n - 1)) + m.fib((n - 2))))`. Pre-existing (the body is rendered by the shared call translator, which assumes the `m.` receiver convention); surfaced while exercising `emitGoLibIRFunc`. Edge case (recursive gomobile-backed func); the android gradle test would catch it if it weren't already timing out.
- **severity**: wrong-output (uncompilable Go) — narrow trigger

---

## NOTES

- **Optimizer**: BinPow/BinShiftLeft/BinShiftRight are absent from `numericOp` — confirm the parser doesn't accept those tokens, or folding silently fails through.
- **goexec cache** is `sync.Map` so concurrent reads/writes are safe, but the cache **persists across compile invocations** (package-global) — so a bug in one test could leak a wrong value into another. Reset between `Check()` runs.
- **No `errors.Is`/`errors.As`** anywhere in `internal/` (0 occurrences). All error-type checks are `*exec.ExitError` type assertions or string compares — any wrapped error is missed.
- The 26 TODO/FIXME comments in non-test code are mostly intentional scope deferrals (LSP completions, kotlin function types, AndroidCompose's RadioGroup/Select/Image). Worth tracking but not bugs.

## Top fixes (suggested priority)

1. **Wrap `Check`/`Lower`/`Convert` in recover at the `sngl.go` boundary** — single change protects all hosts.
2. **Fix `evalIndex` and `evalSelect` map semantics** — these affect interpreter-driven tests, which is the testrunner's primary path.
3. **`BinMod 0` and `AssignDiv 0`** — one-line guards in interp.
4. **`pureCache` key collision** — switch to JSON-marshaled args.
5. **`stdlib.go` parse-error swallowing** — at minimum log.
