# SNGL Bug Audit — General Correctness

Findings from a strategic-sample audit (Parser, Checker, Optimizer, Lower, Interp, html platform, scheme/http, LSP). Ranked by severity: **crash** (panics on plausible input), **wrong-output** (incorrect values silently), **latent** (latent crash/leak under specific conditions), **cosmetic**.

---

## CRASH

### 4. `evalIndex` map key collisions
- **file**: `internal/interp/eval.go:822-836`
- For `map[string]any`, the code does `key := fmt.Sprintf("%v", idx)` — int `1`, float `1.0`, and string `"1"` all collide on the same key.
- **repro**: `m[1] vs m["1"]` with a `map<dyn, T>`.
- **severity**: wrong-output
- **DEFERRED**: the collision is inherent to the `map[string]any` runtime representation — keys are stringified on both write and read, so a fix needs a type-discriminated key scheme applied consistently across every map write and read path (a representation change, not a local patch). Tracked separately.

### 5. `i18n.tr`-style native call paths assume arg count
- **file**: `internal/interp/builtins.go:24-203` (all entries)
- None of the `nativeMethods` validate `len(args)`. If the checker ever lowers a partial call (e.g. error-recovery path with default args missing), these panic with index-out-of-range.
- **severity**: latent crash

---

## WRONG-OUTPUT

### 9. `evalSelect` returns nil for missing map field
- **file**: `internal/interp/eval.go:780-781`
- `m[e.Field]` on `map[string]any` returns zero (nil) silently rather than an error. The same path errors for non-map types (line 783). Test runner thus accepts misspellings as "value is nil".
- **DEFERRED**: erroring on an absent key is a runtime-semantics change. Whether it's safe depends on SNGL struct-field-presence guarantees (are all struct fields always materialised in the backing map, so absent ⇒ only a checker-caught misspelling?). Needs that confirmation plus a full test-suite pass before flipping; a `v, ok := m[field]` two-value guard is the intended shape.

### 11. `resolveQualifiedType` emits errors at `ast.Pos{}`
- **file**: `internal/checker/resolve.go:195, 200, 213`
- Errors for unknown namespaces / unknown types are emitted with zero position. User sees the diagnostic at 1:1 instead of the actual reference site. Same pattern at `resolve.go:31`.
- **severity**: cosmetic (poor UX)

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

### 16. Unicode string-op parity — remaining follow-ups
- Rune semantics for `length`/`substring` are done across interpreter, const folder, and Go codegen (see fixture `testdata/test_stdlib.sngl`). **Still open:** (1) astral-plane parity in the JS/Kotlin *runtimes* — their `.length`/`.substring` use UTF-16 code units, which diverge from rune semantics for characters beyond U+FFFF (surrogate pairs); needs code-point-based string ops in generated code. (2) `indexOf` is implementation-defined (byte in Go/interp/folder, UTF-16 in JS/Kotlin); a rune-indexed `indexOf` is a related follow-up.
- **severity**: wrong-output (i18n/unicode)

### 17. Public API `Format*` panic-prone on partial IR
- **file**: `sngl.go:36-58`
- **PARTIAL** (`Convert` now recovers). Still open: `Format`, `FormatExpr`, `FormatType` return a bare string with no error channel, so a recover would have to substitute "" — deferred pending a degradation-contract decision. `FormatTo` (has an error return) is a clean candidate to wrap next.

### 18. `LSP.RunTCP` leaks preview servers on accept-loop errors
- **file**: `internal/lsp/server.go:65-91`
- Each accepted conn creates a new `previewServer` with its own listener. If `srv.serve` returns via panic (no recover in goroutine), `conn.Close()` runs but `srv.preview.Stop()` only runs on EOF/read error. Any panic above that point leaks the preview listener.
- **severity**: latent leak

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

### 26. WASM playground builds depend on `goexec` having a `_js` stub
- **file**: `internal/optimize/goexec_js.go`
- Stub exists and returns an error. But `consteval.go` calls `execPureGoFunc` from non-build-tagged code — every WASM compile that touches a pure-go-import path will surface "compile-time Go execution not available in WASM" as a diagnostic. Confirm the playground gracefully handles that error path, or skip the call when GOARCH==js.
- **severity**: cosmetic (UX)

### 27. `goLiteral` codepath via `Sprintf("%v")` for args is fragile
- **file**: `internal/optimize/goexec.go:122-...`
- Args are formatted into Go source via `goLiteral`. The cache key uses `Sprintf("%v", args)` (#13), but the *call site* uses `goLiteral` which presumably escapes strings. Different escaping in two places → desync between cache key uniqueness and code that runs.

### 30. Tar extraction doesn't check `hdr.Size` vs disk
- **file**: `codegen/scheme/http/http.go:206-217`
- A zip-bomb-style tar.gz from a malicious import URL is extracted verbatim (no size cap, no entry-count cap). DoS via large `http://` import.
- **severity**: latent (assumes trust on import URLs)

---

## NOTES

- **Optimizer**: BinPow/BinShiftLeft/BinShiftRight are absent from `numericOp` — confirm the parser doesn't accept those tokens, or folding silently fails through.
- **goexec cache** is `sync.Map` so concurrent reads/writes are safe, but the cache **persists across compile invocations** (package-global) — so a bug in one test could leak a wrong value into another. Reset between `Check()` runs.
- **No `errors.Is`/`errors.As`** anywhere in `internal/` (0 occurrences). All error-type checks are `*exec.ExitError` type assertions or string compares — any wrapped error is missed.
- The 26 TODO/FIXME comments in non-test code are mostly intentional scope deferrals (LSP completions, kotlin function types, AndroidCompose's RadioGroup/Select/Image). Worth tracking but not bugs.

## Top fixes (suggested priority)

1. **Fix `evalIndex` and `evalSelect` map semantics** — these affect interpreter-driven tests, which is the testrunner's primary path.
2. **`pureCache` key collision** — switch to JSON-marshaled args.
