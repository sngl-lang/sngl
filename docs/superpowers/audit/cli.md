# CLI audit: `cmd/sngl/`

Findings from reading every subcommand file plus `main.go`, `discover.go`,
`dumpformat.go`, `dumpinput.go`, `dumpomit.go`, `script_test.go`, and a scan
of `cmd/sngl/testdata/`. Ranked by user-visible impact, highest first.

## High impact

### 1. `test --language` vs everything else's `--lang`
- `cmd/sngl/test.go:30` declares `--language` (long form, no short).
- `build.go:25`, `compile.go:34`, `run.go:26`, `dump.go:71,73,76` all use `--lang`.
- A user who learns `--lang` for compile/build/run then gets an unknown-flag
  error from `test`. There's no compatibility alias.
- Unify on `--lang`. If we want both, add `--language` as an alias in
  `test.go` (Cobra: `Flags().SetNormalizeFunc` or a hidden duplicate) — but
  pick one.

### 2. Root `--format` collides with subcommand `--format`
- `main.go:28` registers a persistent `--format text` on the root with
  values "text, json, sarif".
- `dump.go:64` adds `--format spew` on the dump group with values
  "spew, json, sngl".
- `test.go:28` adds `--format text` on the test command with values "text or json".
- Cobra silently lets the local flag shadow the persistent one. Outcome: the
  persistent flag is functionally dead (nothing reads it on root), and the
  three meanings conflict. `check.go` and `fmt.go` mention nothing about
  output formats yet inherit the persistent flag and ignore it — so
  `sngl check --format json file.sngl` runs without error and prints plain
  text, which is a lie by omission.
- Remove the persistent `--format` from root, or wire it into `check`/`fmt`
  and document the allowed values per command.

### 3. Root `--project` flag is declared but never read
- `main.go:27` adds `--project . "project root directory"`. Grep shows zero
  `GetString("project")` calls anywhere in `cmd/sngl/`. Pure dead UI.
- Either implement it (resolve relative paths under it) or delete the
  flag.

### 6. `dump optimized` always lowers but is named "optimized"
- `dump.go:178-186` runs `lower.Lower` after Optimize. So the result is
  post-lowering IR, not post-optimization IR. The comment says
  "matches what codegen actually consumes" — fine — but the subcommand
  name lies and there is no way to ask for *just* optimized.
- Same for `dump analysis` (`dump.go:226-233`).
- Either add `--no-lower` to those commands, or rename `dump optimized`
  to something honest, or expose `dump lowered --after none` as the
  canonical way (it already exists, `dump.go:78`).

### 7. `pkg cache clear` reads stdin even with `--directory` change
- `pkg.go:217-238`: the confirmation prompt uses `os.Stdin` directly. Fine,
  but no `--format` / `--quiet` respect — a `-q` user still gets the
  interactive prompt printed to stderr. Add `if quiet(cmd) || yes { skip }`,
  or have `-q` imply `-y`.

### 8. `doc build` shells out to `os.Args[0] compile` (re-exec)
- `doc.go:1037-1042` constructs `exec.Command(os.Args[0], "compile", ...)`.
  That breaks when invoked via `go tool sngl doc build` because `os.Args[0]`
  is the cached-build binary path; the proxy in `main.go:proxyToGoTool`
  isn't honored on the re-exec, and the child inherits no `SNGL_NO_PROXY`
  guard, so it could re-proxy. Subtle.
- Also breaks if the user runs `sngl` from a renamed symlink or via
  embedding — `os.Args[0]` is unreliable.
- Call the compile pipeline in-process, the same way every other dump/run
  command does. The file `runSNGLCompile` exists solely to avoid pulling
  in the right packages; just import them.

### 9. `compile`'s `validateCLIOptsAcrossTargets` silently no-ops when no schema
- `compile.go:241-250`: if no resolved target carries a `Def`, *every*
  `--opt` key is accepted with no validation. So
  `sngl compile --lang js --platform html --opt nonsense=1` succeeds
  silently because the CLI-target path produces an empty `StructLit{}` with
  no `Def`. Surprising.
- Either require a schema (use the platform's declared option type) or
  warn on unknown keys when no schema is available.

### 10. Exit code: stub commands return code 1 instead of an unimplemented-specific code
- `stubs.go:13`: `lint` stub returns `fmt.Errorf("%s: not yet implemented", name)`.
  Cobra surfaces this as exit 1, identical to any real failure. CI cannot
  distinguish "feature missing" from "lint found violations".
- Use exit 2 (`os.Exit(2)`) or have stubs print to stderr + exit 64 (EX_USAGE-ish).

## Medium impact

### 11. `--out` default is `.` for compile/build but `""` for snapshot and `_site` for doc
- `compile.go:36`, `build.go:27`: `--out .`
- `snapshot.go:29`: `--out ""` (means `<dir>/snapshots`)
- `doc.go:55,57`: `--out _site` (with short `-o`)
- Only `doc` exposes `-o` short form. Make `-o` consistent across all four
  output-emitting commands.

### 12. `--platform` is `String` everywhere except `snapshot` where it's `StringSlice`
- `snapshot.go:28`: `StringSlice("platform", ...)`.
- Every other command uses a single string. A user passing
  `--platform html,bubbletea` to compile gets a parse error; passing it to
  snapshot works. Document this, or make all of them accept a list (and
  expand it).

### 13. `test --platform all` is undocumented magic
- `test.go:84`: the string `"all"` triggers the matrix path
  (`runTestAll`). Not mentioned in the flag help (`test.go:29`).
- Document it: `target platform (e.g. html, none, or 'all' to fan out)`.

### 14. `discoverFiles` defaults to "." silently, but `preview` / `run` demand 1 arg
- `discover.go:21-23`: any subcommand calling `discoverFiles` defaults to
  cwd when given zero args.
- `preview.go:31` (`ExactArgs(1)`), `run.go:21` (`MinimumNArgs(1)`)
  reject zero args.
- `compile`/`build`/`check`/`fmt`/`test`/`snapshot` all accept zero args
  and walk cwd. The split is unprincipled — `run` could just as well
  default to cwd's main.

### 15. Inconsistent stdin support
- Only `dump` supports stdin (`dumpinput.go:111-121`, via `--input stdin`
  or `-` arg).
- `check`, `fmt`, `compile`, `build` cannot read stdin. `fmt` from stdin
  is a common editor integration that's missing.

### 16. `--directory` is `-C`, but no other shortcuts are conventional
- `main.go:26`: `-C` (uppercase, matches `make`/`git`). Good.
- `-q` / `-v` exist, but no `-d` for `--debug`, no `-o` for `--out` (only
  on `doc`), no `-p` for `--platform` (only on `doc serve --port`). The
  use of `-p` for `--port` precludes giving it to `--platform`.
- Settle a short-flag convention: `-l/--lang`, `-p/--platform`, `-o/--out`,
  `-f/--format`.

### 17. `dump` subcommands don't all carry the same flag set
- `dump parsed` / `dump checked` do not register `--lang` / `--platform`
  (not needed for those phases). That's intentional — but a user passing
  them gets a `unknown flag` error, not the more useful "ignored at this
  phase". The persistent flags on `dump` (`--format`, `--color`, `--input`,
  `--pointers`, `--depth`, `--omit`) all apply. Inconsistency: `--after`
  and `--list` are only on `dump lowered`; using them on other dump verbs
  is silently rejected.

### 18. Dump format names "format/input/omit" aren't subcommands
- The task description mentions `dump format`, `dump input`, `dump omit`
  as subcommands. They are not: they're flags / files (`dumpformat.go`,
  `dumpinput.go`, `dumpomit.go`). The actual `dump` verbs are
  parsed/checked/optimized/analysis/lowered. Either the docs need updating,
  or `dump format/input/omit` should be added as self-documenting verbs
  (e.g. `sngl dump format` lists supported formats).

### 19. `compile` uses `cliOpts` map ordering for application
- `compile.go:302`: `for k, v := range kv` iterates Go map keys in random
  order. If two `--opt` keys both write to the same StructLit field via
  different normalized paths, the last write wins nondeterministically.
  Not currently a problem in practice but a latent reproducibility bug.

### 20. Error message format is inconsistent
- `compile.go:104`, `build.go:84`: `fmt.Errorf("%s: %w", dir, err)`
- `check.go:33,42`: `fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)` then
  returns the sentinel `fmt.Errorf("check failed")` at end.
- `test.go:195`: `fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)` then
  bumps a `totalFail` counter, prints `not ok ...` at end.
- `fmt.go:60-70`: same pattern as check.
- Some commands print per-file errors to stderr and a sentinel to stdout;
  some return the first error; some return a count-summary sentinel. Pick
  one: probably "print each, exit non-zero with the first error message".

### 21. `test` short-circuits in the middle of file iteration on parse failure
- `test.go:200-205`: parse failure on file N continues to file N+1. But
  the safeRunTests recover at `test.go:273-279` is per-file too. Mixed
  semantics: compile-time error vs runtime panic both increment
  `totalFail` but with different visible output.

### 22. `--debug` and `--verbose` together: silent precedence
- `main.go:45-51`: `case debug:` wins over `case verbose:` wins over
  `case quiet:`. No error when two are passed together. Consider warning,
  or making them an exclusive group via Cobra `MarkFlagsMutuallyExclusive`.

### 23. `doc build` and `doc serve` duplicate `--out` registration
- `doc.go:55` (`docBuildCmd`) and `doc.go:57` (`docServeCmd`) both
  declare `-o, --out _site`. Fine — but `serve` does not inherit `build`'s
  flag because they're siblings. If `--out` ever gains another value
  (e.g. `--base-url`), it has to be added in two places. Move shared
  flags to a `docCmd.PersistentFlags()` block.

## Low impact

### 24. `version` uses `Run` not `RunE`
- `version.go:18`: `Run` signature swallows errors. Trivial here (no errors
  possible) but inconsistent with every other command.

### 25. `lsp` flag doc says "e.g. :7998" but no validation
- `lsp.go:25`: empty TCP is the trigger to use stdio. A typo like
  `--tcp 7998` (no leading colon) will fail at listen-time with a
  cryptic net error. Validate up front.

### 26. `proxyToGoTool` prints `sngl: proxying ...` to stderr unconditionally
- `main.go:145`: noisy for scripted use. Gate behind `--verbose` or
  `SNGL_NO_PROXY` debug.

### 27. `runDocServe` uses `http.FileServer` with no MIME hint override and no cache headers
- `doc.go:701`: a vanilla file server. Fine for local dev but should at
  least set a no-cache header so refreshes after `doc build` see new
  content.

### 28. `preview` silently defaults to js/html if no output declaration
- `preview.go:142-144`: `s.activeLang = "js"; s.activePlat = "html"`.
  A user previewing a file targeting bubbletea-only gets a confusing
  empty preview because the default doesn't honor declared outputs when
  none match. Already handled when outputs exist; the fallback is fine
  but should warn.

### 29. `preview.go:201` debounce timer leaks on shutdown
- The `time.AfterFunc` is not stopped when the watcher ends. Minor — the
  process is exiting anyway. Worth tracking if preview ever becomes
  embeddable.

### 30. `snapshot --platform` ignores `--lang`
- `snapshot.go` never lets the user pick a language; it derives one via
  `snapshot.LangForPlatform(plat)` at line 146. Not a bug, but inconsistent
  with every other command. Add `--lang` for symmetry, default empty
  → derive.

### 31. Testdata coverage gaps
- `cmd/sngl/testdata/` has txtar fixtures for: `check`, `compile`, `fmt`,
  `run`, `test_cmd`, `version`, `discover`, `doc_resolve`,
  `dump_lowered_list`, `lint`, plus many compile_* variants.
- Missing: no `preview*.txt`, no `snapshot*.txt`, no `pkg*.txt`, no
  `build*.txt`, no `lsp*.txt`. `build` in particular ships
  zero golden tests — every regression in `build` is invisible to CI
  except through full e2e snapshot tests.

### 32. `parseSNGLValue` (preview) infers float from integers
- `preview.go:660-668`: integer matches first, but `fmt.Sscanf("%g", &f)`
  accepts "5" as a float too. The order works (int branch returns first)
  but is fragile. The bigger issue is no error handling: `Sscanf` failures
  fall through to "treat as quoted string", which silently mis-parses
  any malformed input the editor sends.

### 33. `optionBool` accepts only `"true"`, not `1` / `yes`
- `compile.go:328-338`: bool option read by raw string compare to
  `"true"`. Anything else (including `True`, `1`) is false. Consistent
  with SNGL source booleans but a CLI surprise — `--opt noCacheBust=1`
  is silently false.

## Concrete suggested unifications

| Concept         | Today                                                                                             | Suggest                                                                           |
|-----------------|---------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|
| Lang flag       | `--lang` (build, compile, run, dump) / `--language` (test)                                        | `--lang` everywhere, alias `--language`                                           |
| Output dir flag | `--out .` (compile/build) / `--out ""` (snapshot) / `-o, --out _site` (doc)                       | `-o, --out` everywhere, each with a sensible default                              |
| Format flag     | persistent root `--format text` + per-cmd `--format` (dump, test) with different valid value sets | drop root, document per-cmd values, validate                                      |
| Platform flag   | `String` everywhere, `StringSlice` in snapshot                                                    | `StringSlice` everywhere; single-element common case                              |
| Quiet sentinel  | `quiet(cmd)` helper in `compile.go:436` used inconsistently                                       | always honor `-q` to suppress per-file "ok" lines                                 |
| Pipeline        | now unified: compile/build/run all call `runPipeline` in `pipeline.go` (dump still separate)      | fold dump into the shared pipeline too                                            |
| Pkg flags       | `--yes` lives only on `pkg cache clear`                                                           | Promote to `pkgCmd.PersistentFlags()` since `download`/`update` may want it later |

## Bugs (definite)

- `main.go:27` — `--project` flag dead.
- `doc.go:1038` — re-exec via `os.Args[0]` breaks under `go tool sngl`.
- `compile.go:411` — `os.Create` happens before `MkdirAll`'s error is
  observed for sibling files; if any file in `resp.Files` has an
  unwritable parent, a partial file tree is left behind with no cleanup.
- `dump.go:104-109` — `dumpOmitSet` is a package-level global, set on
  every `runDump*` call. In `TestScript`, which runs cobra in-process
  back-to-back, the omit set persists across tests until overwritten. The
  next test that doesn't pass `--omit` keeps the previous test's omit
  set. Same hazard for `dumpSpew.MaxDepth` and
  `dumpSpew.DisablePointerAddresses`. Reset at start of
  `dumpResolveFlags`.
- `discover.go:380-394` — `collectTargets` iterates registries inside
  every `checkDoc` invocation. Cheap, but reorders nondeterministically
  if `codegen.Langs()`/`Platforms()` ever stop sorting.

## Undocumented but parsed flags / vice versa

- `--project` (declared, unused).
- `dump --pointers`, `--depth`, `--omit` — documented in help, but their
  state leaks across invocations (see bug above).
- `test --platform all` — parsed magic value, not in help.
- `preview` has no `--lang` even though `compile` does, so previewing a
  multi-output project always uses the first declared output (see
  `preview.go:138-145`).
- `--format json` on root is accepted on `check` and `fmt` but ignored;
  no diagnostic.

## File-by-file impact summary

| File           | Findings touching it      |
|----------------|---------------------------|
| `main.go`      | 2, 3, 22, 26              |
| `compile.go`   | 2, 9, 19, 20, 33          |
| `build.go`     | 31                        |
| `run.go`       | 14                        |
| `test.go`      | 1, 13, 20, 21             |
| `check.go`     | 2, 20                     |
| `fmt.go`       | 2, 15, 20                 |
| `dump.go`      | 6, 17, 18, omit-leak bug  |
| `dumpinput.go` | 15                        |
| `preview.go`   | 14, 28, 29, 30, 32        |
| `snapshot.go`  | 11, 12, 30, 31            |
| `doc.go`       | 11, 23, 27, doc-build bug |
| `pkg.go`       | 7, 31                     |
| `lsp.go`       | 25, 31                    |
| `stubs.go`     | 10                        |
| `version.go`   | 24                        |
| `discover.go`  | 14, collectTargets note   |
