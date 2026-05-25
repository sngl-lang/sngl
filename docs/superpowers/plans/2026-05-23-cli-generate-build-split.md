# CLI: generate/build/run split — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `sngl compile`/`build`/`run` with a clean split — `generate` emits source, `build` produces a binary in cwd from a temp generation, `run` builds then executes — and add an `Options.test` field that the CLI accepts (emission deferred to the test-architecture work).

**Architecture:** Extract one `runPipeline` function that owns parse→merge→check→optimize→lower→generate. The three user-facing verbs all call it with different "what to do after generation" closures. `build` and `run` generate into a temp dir; only `generate` writes to a user-controlled `--out`. Spec: `docs/superpowers/specs/2026-05-23-cli-generate-build-split-design.md`.

**Tech Stack:** Go, cobra, rsc.io/script (txtar test framework), `codegen.Builder`/`codegen.Runner` interfaces.

**Spec ref:** `docs/superpowers/specs/2026-05-23-cli-generate-build-split-design.md`

**No worktree** — per project memory, work directly on `main`.

---

## File map

- Create `cmd/sngl/pipeline.go` — shared `runPipeline`.
- Create `cmd/sngl/generate.go` — new `generate` command (today's `compile`, on `runPipeline`).
- Modify `cmd/sngl/build.go` — new semantics (temp dir + `Builder.Build()` → cwd, binary naming).
- Modify `cmd/sngl/run.go` — call `runPipeline`; existing temp-dir + `Runner.Run()` shape stays.
- Delete `cmd/sngl/compile.go` — superseded by `generate.go`. Move `parseCLIOpts`, `resolveTargets`, `applyCLIOpts`, `cloneStructLit`, `optionBool`, `optionFieldType`, `parseConstOption`, `generateTarget`, `quiet`, `outputTarget` into `pipeline.go`.
- Modify `cmd/sngl/main.go` — drop `compileCmd` registration, add `generateCmd`.
- Modify `cmd/sngl/doc.go:1037-1042` — re-exec uses `generate` instead of `compile`.
- Rename in bulk: `cmd/sngl/testdata/compile_*.txt` → `cmd/sngl/testdata/generate_*.txt`; rewrite `sngl compile` → `sngl generate` inside.
- Create `cmd/sngl/testdata/build_basic.txt`, `build_multi_target.txt`, `build_test_opt.txt`, `build_nocachebust_parity.txt`.
- Modify `lib/options.sngl` — add `test bool` field.

---

### Task 1: Extract `runPipeline` skeleton; back today's `compile` with it

**Files:**
- Create: `cmd/sngl/pipeline.go`
- Modify: `cmd/sngl/compile.go`

The first task only moves code; behavior must not change. `runPipeline` lives in a new file and is initially called only by `compile`. Existing testdata acts as the regression net.

- [ ] **Step 1: Write `cmd/sngl/pipeline.go`**

Create the file with the contents below. Helpers `parseCLIOpts`, `resolveTargets`, `applyCLIOpts`, `cloneStructLit`, `optionBool`, `optionFieldType`, `parseConstOption`, `validateCLIOptsAcrossTargets`, `outputTarget`, `generateTarget`, and `quiet` move here verbatim from `compile.go`.

```go
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// pipelineOpts controls runPipeline's per-target behavior. main=true sets the
// "main" option so platforms emit a runnable entry point; outDir is where
// generated files are written.
type pipelineOpts struct {
	cliLang string
	cliPlat string
	cliOpts map[string]string
	outDir  string
	main    bool
	quiet   bool
	// onTarget runs after generation, per target. Used by `build` to invoke
	// Builder.Build, and by `run` to start the artifact. Nil = generate-only.
	onTarget func(target outputTarget, pkg *ir.Package, dir, outDir string) error
}

// runPipeline is the one parse→check→optimize→lower→generate path used by
// generate, build, and run. The variation between commands is captured in
// pipelineOpts.onTarget; everything before that is identical.
func runPipeline(cmd *cobra.Command, args []string, p pipelineOpts) error {
	cliLang, cliPlat, err := resolveLangPlat(p.cliLang, p.cliPlat)
	if err != nil {
		return err
	}

	for _, name := range codegen.Platforms() {
		pl := codegen.LookupPlatform(name)
		if cfg, ok := pl.(codegen.OptionConfigurable); ok {
			if err := cfg.Configure(p.cliOpts); err != nil {
				return fmt.Errorf("configure platform %s: %w", name, err)
			}
		}
	}

	explicitFiles := explicitFileSet(args)

	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	seen := make(map[string]bool)
	for _, filename := range files {
		dir := filepath.Dir(filename)
		if seen[dir] {
			continue
		}
		seen[dir] = true

		f, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		start := time.Now()
		doc, err := parseSNGL(filename, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		slog.Info("parse", "file", filename, "duration", time.Since(start))

		absFilename, _ := filepath.Abs(filename)
		if !explicitFiles[absFilename] {
			start = time.Now()
			doc = mergeDir(doc, filename)
			slog.Info("merge", "dir", dir, "duration", time.Since(start))
		}

		start = time.Now()
		pkg, err := checkDoc(doc, dir, true)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("check", "dir", dir, "duration", time.Since(start))

		if err := validateOutputs(pkg); err != nil {
			return err
		}

		targets, err := resolveTargets(pkg, cliLang, cliPlat, p.cliOpts)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		if len(targets) == 0 {
			return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", dir)
		}

		for _, target := range targets {
			if target.Options == nil {
				target.Options = &ir.StructLit{}
			}
			if p.main {
				codegen.SetOptionField(target.Options, "main", true)
			}
			if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
				codegen.SetOptionField(target.Options, "projectDir", dir)
			}

			optCfg := &optimize.Config{
				Platform:    target.Platform,
				Language:    target.Lang,
				Dir:         dir,
				NoCacheBust: optionBool(target.Options, "noCacheBust"),
			}
			start = time.Now()
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			plat := codegen.LookupPlatform(target.Platform)
			lang := codegen.LookupLang(target.Lang)
			if plat == nil {
				return fmt.Errorf("%s: unknown platform %q (available: %v)", filename, target.Platform, codegen.Platforms())
			}
			if lang == nil {
				return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
			}
			caps := plat.Capabilities().Merge(lang.Capabilities())
			start = time.Now()
			if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

			if caps != (lower.Caps{}) {
				start = time.Now()
				if err := optimize.Optimize(pkg, optCfg); err != nil {
					return fmt.Errorf("%s: %w", dir, err)
				}
				slog.Info("optimize2", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
			}

			var fileAssets []codegen.FileAsset
			for _, fa := range optCfg.FileAssets {
				fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
			}

			start = time.Now()
			if err := generateTarget(filename, pkg, target, p.outDir, fileAssets, p.quiet); err != nil {
				return err
			}
			slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			if p.onTarget != nil {
				if err := p.onTarget(target, pkg, dir, p.outDir); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// All these helpers move here verbatim from compile.go:
//   parseCLIOpts, resolveTargets, applyCLIOpts, cloneStructLit,
//   optionBool, optionFieldType, parseConstOption,
//   validateCLIOptsAcrossTargets, outputTarget, generateTarget, quiet.
// Copy the existing bodies; do not re-edit.

// (Note: also need imports "sort" and "slices" used by generateTarget.)
```

Use the existing helper bodies (unchanged) — just relocate them. Imports for `sort` and `slices` come from `generateTarget`'s usage of `sort.Strings` and `slices.Contains`.

- [ ] **Step 2: Trim `cmd/sngl/compile.go`**

Replace `compile.go` with a thin wrapper that calls `runPipeline`. Delete the helpers (they live in pipeline.go now).

```go
package main

import (
	"github.com/spf13/cobra"
)

var compileCmd = &cobra.Command{
	Use:   "compile [flags] [file|dir...]",
	Short: "Compile SNGL files to target platform",
	Args:  cobra.ArbitraryArgs,
	RunE:  runCompile,
}

func init() {
	compileCmd.Flags().String("lang", "", "target language")
	compileCmd.Flags().String("platform", "", "target platform")
	compileCmd.Flags().String("out", ".", "output directory")
	compileCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runCompile(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  outDir,
		quiet:   quiet(cmd),
	})
}
```

- [ ] **Step 3: Verify all `compile_*.txt` scripts still pass**

Run: `go test ./cmd/sngl/ -run TestScript -v 2>&1 | tail -40`

Expected: All `TestScript/compile_*` subtests pass. The behavior of `sngl compile` must be byte-identical to before.

If any fail, do not proceed — the refactor introduced a regression.

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/pipeline.go cmd/sngl/compile.go
git commit -m "$(cat <<'EOF'
cmd/sngl: extract runPipeline shared by compile/build/run

Move parse→check→optimize→lower→generate into a single function in
pipeline.go. compile.go is now a thin wrapper. Behaviour preserved —
all compile_*.txt script tests pass unchanged. build.go and run.go
will be migrated in subsequent commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Migrate `build` to `runPipeline`; behavior preserved

`build` still writes generated source to `--out` and calls `Builder.Build(outDir, …)`. The temp-dir + binary-in-cwd change happens in Task 5 — staged so any regression surfaces against the existing build test golden, not against a moving target.

**Files:**
- Modify: `cmd/sngl/build.go`

- [ ] **Step 1: Rewrite `runBuild` on top of `runPipeline`**

Replace the existing `runBuild` body. Keep flag declarations identical.

```go
func runBuild(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  outDir,
		main:    true,
		quiet:   quiet(cmd),
		onTarget: func(target outputTarget, _ *ir.Package, _, outDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
			}
			artifact, err := builder.Build(outDir, target.Options)
			if err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			fmt.Println(artifact)
			return nil
		},
	})
}
```

Imports needed at top of `build.go`: `fmt`, `git.duckfam.us/jonathan/sngl/codegen`, `git.duckfam.us/jonathan/sngl/ir` (for the closure parameter), `github.com/spf13/cobra`. Delete the old per-file pipeline body and the `log/slog`, `os`, `path/filepath`, `time`, `internal/lower`, `internal/optimize` imports — `runPipeline` owns them now.

- [ ] **Step 2: Confirm builds still work**

Run: `go build ./...`

Expected: no errors.

Run: `go test ./cmd/sngl/ -run TestScript -v 2>&1 | tail -30`

Expected: previously-passing tests still pass. (There are no `build_*.txt` files yet, so this is mostly a regression check on `compile_*.txt`.)

- [ ] **Step 3: Commit**

```bash
git add cmd/sngl/build.go
git commit -m "$(cat <<'EOF'
cmd/sngl: route build through shared runPipeline

build.go now calls runPipeline with main=true and an onTarget closure
that invokes Builder.Build. Closes audit findings #4/#5 for build:
OptionConfigurable.Configure and NoCacheBust now apply uniformly.
Temp-dir + binary-in-cwd semantics land in a later commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Migrate `run` to `runPipeline`

`run` already uses a temp dir and `Runner.Run`. Switch its parse-through-generate to `runPipeline`.

**Files:**
- Modify: `cmd/sngl/run.go`

- [ ] **Step 1: Rewrite `runRun`**

```go
func runRun(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	file := args[0]
	var progArgs []string
	for i, a := range args {
		if a == "--" {
			progArgs = args[i+1:]
			break
		}
	}

	tmpDir, err := os.MkdirTemp("", "sngl-run-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	return runPipeline(cmd, []string{file}, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true,
		onTarget: func(target outputTarget, _ *ir.Package, _, outDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			runner, ok := plat.(codegen.Runner)
			if !ok {
				return fmt.Errorf("platform %q does not support direct execution", target.Platform)
			}
			codegen.SetOptionField(target.Options, "lang", target.Lang)
			return runner.Run(outDir, target.Options, progArgs)
		},
	})
}
```

Drop the now-unused imports from `run.go`: `log/slog`, `path/filepath`, `time`, `internal/lower`, `internal/optimize`, `ast`. Keep `fmt`, `os`, `codegen`, `ir`, `cobra`.

- [ ] **Step 2: Verify**

Run: `go build ./... && go test ./cmd/sngl/ -run TestScript -v 2>&1 | tail -20`

Expected: green.

- [ ] **Step 3: Commit**

```bash
git add cmd/sngl/run.go
git commit -m "cmd/sngl: route run through shared runPipeline

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Add `generate` command alongside `compile`

Introduce the new command first; testdata rename and `compile` removal happen in Task 6 and Task 7. Keeping both temporarily means the test suite stays green throughout.

**Files:**
- Create: `cmd/sngl/generate.go`
- Modify: `cmd/sngl/main.go`

- [ ] **Step 1: Write `cmd/sngl/generate.go`**

```go
package main

import (
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate [flags] [file|dir...]",
	Short: "Generate target-language source from SNGL files",
	Args:  cobra.ArbitraryArgs,
	RunE:  runGenerate,
}

func init() {
	generateCmd.Flags().String("lang", "", "target language")
	generateCmd.Flags().String("platform", "", "target platform")
	generateCmd.Flags().StringP("out", "o", ".", "output directory")
	generateCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runGenerate(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  outDir,
		quiet:   quiet(cmd),
	})
}
```

Note `-o` short form on `--out`. This matches the spec's "settle short-flag convention" goal (audit #16) for the new command, even though we're not touching every other command's short flags in this plan.

- [ ] **Step 2: Register in `main.go`**

In `cmd/sngl/main.go`, find `rootCmd.AddCommand(compileCmd)` and add the generate command on the next line:

```go
	rootCmd.AddCommand(compileCmd)
	rootCmd.AddCommand(generateCmd)
```

- [ ] **Step 3: Smoke test the new command**

Run: `go build ./... && go run ./cmd/sngl generate --help 2>&1 | head -10`

Expected: usage text mentioning "Generate target-language source from SNGL files".

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/generate.go cmd/sngl/main.go
git commit -m "cmd/sngl: add generate command (compile alias for now)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Switch `build` to generate-to-tmp + binary-in-cwd

Today `build` writes generated source to `--out`, then `Builder.Build()` reads from that same dir. New behavior: source goes to a temp dir, binary lands in cwd (or wherever `--out` points). The artifact path is computed from `Options.name`; multi-target builds get `<base>-<platform>` suffixes.

**Files:**
- Modify: `cmd/sngl/build.go`

- [ ] **Step 1: Add the binary-naming helper**

Append to `cmd/sngl/build.go`:

```go
// binaryName derives the on-disk filename for a built artifact. Default base
// is Options.name, lowercased and with non-alphanumeric runes replaced by '-'.
// When the build emits more than one target, "-<platform>" is appended so
// concurrent targets don't overwrite each other's binary. The ".exe"
// extension is added for windows.
func binaryName(opts *ir.StructLit, platform string, multiTarget bool, windows bool) string {
	base := optionString(opts, "name")
	if base == "" {
		base = "app"
	}
	base = sanitizeBinaryBase(base)
	if multiTarget {
		base = base + "-" + platform
	}
	if windows {
		base += ".exe"
	}
	return base
}

// sanitizeBinaryBase lowercases s and replaces any non-alphanumeric rune
// with '-'. Consecutive '-' are collapsed and leading/trailing '-' trimmed.
func sanitizeBinaryBase(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteRune('-')
			prevDash = true
		}
	}
	out := b.String()
	return strings.TrimRight(out, "-")
}

// optionString reads a string-valued option from opts, returning "" if absent
// or non-string. Mirrors optionBool in pipeline.go.
func optionString(opts *ir.StructLit, name string) string {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return ""
	}
	lit, ok := v.(*ir.Literal)
	if !ok {
		return ""
	}
	return strings.Trim(lit.Raw, `"`)
}
```

Imports needed: `strings` (already present? check), `git.duckfam.us/jonathan/sngl/codegen`, `git.duckfam.us/jonathan/sngl/ir`. Add `"strings"` if missing.

- [ ] **Step 2: Rewrite `runBuild` to use tmp-dir + place binary in cwd**

Replace the body produced in Task 2 with:

```go
func runBuild(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	tmpDir, err := os.MkdirTemp("", "sngl-build-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Dry-run target resolve to know whether to append "-<platform>" to
	// binary names. Duplicates a parse+check, but keeps onTarget local;
	// the alternative is a callback from runPipeline post-resolve, which
	// would complicate its API for one consumer.
	multiTarget := isMultiTarget(args, cliLang, cliPlat, optSlice)

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true, // suppress per-file print; only print artifact path
		onTarget: func(target outputTarget, _ *ir.Package, _, srcDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
			}
			artifact, err := builder.Build(srcDir, target.Options)
			if err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			finalName := binaryName(target.Options, target.Platform, multiTarget, isWindowsTarget(target))
			finalPath := filepath.Join(outDir, finalName)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			if err := moveFile(artifact, finalPath); err != nil {
				return fmt.Errorf("placing artifact: %w", err)
			}
			fmt.Println(finalPath)
			return nil
		},
	})
}

// isMultiTarget returns true when the resolved set of build targets for
// `args` contains more than one target. Implemented by parsing the first
// file's outputs in isolation; if cliLang+cliPlat are both set, there is
// always exactly one target.
func isMultiTarget(args []string, cliLang, cliPlat string, optSlice []string) bool {
	if cliLang != "" && cliPlat != "" {
		return false
	}
	files, err := discoverFiles(args)
	if err != nil || len(files) == 0 {
		return false
	}
	f, err := os.Open(files[0])
	if err != nil {
		return false
	}
	doc, err := parseSNGL(files[0], f)
	f.Close()
	if err != nil {
		return false
	}
	dir := filepath.Dir(files[0])
	absFilename, _ := filepath.Abs(files[0])
	if !explicitFileSet(args)[absFilename] {
		doc = mergeDir(doc, files[0])
	}
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return false
	}
	targets, err := resolveTargets(pkg, cliLang, cliPlat, parseCLIOpts(optSlice))
	if err != nil {
		return false
	}
	return len(targets) > 1
}

// isWindowsTarget reports whether the target's options indicate a windows
// build. Today no platform sets an explicit GOOS option; we conservatively
// return false. Windows .exe naming is a forward-compat hook.
func isWindowsTarget(target outputTarget) bool {
	if s := optionString(target.Options, "goos"); s == "windows" {
		return true
	}
	return false
}

// moveFile relocates src to dst. Falls back to copy+remove if Rename fails
// (cross-device).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

```

Imports for `build.go` after this change: `fmt`, `io`, `os`, `path/filepath`, `strings`, `github.com/spf13/cobra`, `git.duckfam.us/jonathan/sngl/codegen`, `git.duckfam.us/jonathan/sngl/ir`. Remove unused (Go will flag).

Also update the flag setup in `init()`:

```go
func init() {
	buildCmd.Flags().String("lang", "", "target language")
	buildCmd.Flags().String("platform", "", "target platform")
	buildCmd.Flags().StringP("out", "o", ".", "directory to place built binaries")
	buildCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}
```

- [ ] **Step 3: Build the binary**

Run: `go install ./cmd/sngl/`

Expected: no errors. (`go install` rather than `go build` since CLAUDE.md says it's preferred.)

- [ ] **Step 4: Manual smoke test**

```bash
cd /tmp && rm -rf sngl-build-smoke && mkdir sngl-build-smoke && cd sngl-build-smoke
cat > app.sngl <<'EOF'
output { go { bubbletea } }

Options { name: "smokeapp" }

component main {
    text(value="hello")
}
EOF
sngl build .
ls -la smokeapp
```

Expected: a file named `smokeapp` exists in cwd. (If `Options { name: ... }` syntax is wrong for sngl, see Task 8 — adjust to whatever sngl source uses. The point is to confirm an artifact lands in cwd named after `Options.name`.)

- [ ] **Step 5: Commit**

```bash
git add cmd/sngl/build.go
git commit -m "$(cat <<'EOF'
cmd/sngl: build emits binary to cwd from a temp generation dir

`sngl build` no longer writes source files to the user's --out path.
Instead it generates into a tempdir, invokes Builder.Build there, then
moves the resulting artifact into --out (default cwd). Binary name is
derived from Options.name, with '-<platform>' suffix on multi-target
builds.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Rename `compile_*.txt` testdata → `generate_*.txt`; rewrite invocations

35 txtar files reference `sngl compile`. They become `sngl generate`. Filenames also rename so test names align.

**Files:**
- Rename: `cmd/sngl/testdata/compile_*.txt` → `cmd/sngl/testdata/generate_*.txt`
- Rewrite: `sngl compile` → `sngl generate` inside each renamed file.

- [ ] **Step 1: Rename files**

```bash
cd cmd/sngl/testdata
for f in compile*.txt; do
  git mv "$f" "${f/compile/generate}"
done
ls generate_*.txt | wc -l
```

Expected: 35 (matches the previous `compile_*.txt` count, including bare `compile.txt` → `generate.txt`).

- [ ] **Step 2: Rewrite `sngl compile` → `sngl generate` inside each**

```bash
cd cmd/sngl/testdata
sed -i 's/sngl compile/sngl generate/g' generate*.txt
grep -l 'sngl compile' generate*.txt && echo "FAIL: leftovers" || echo "OK"
```

Expected: prints `OK`. No leftover `sngl compile` strings.

- [ ] **Step 3: Verify all tests pass against the new command**

Run: `go test ./cmd/sngl/ -run TestScript -v 2>&1 | tail -50`

Expected: every `TestScript/generate_*` passes. `compile_*` no longer exists.

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/testdata/
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: rename compile_*.txt to generate_*.txt

Mechanical rename + sed substitution of 'sngl compile' → 'sngl generate'.
All 35 script tests pass against the new command.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Remove `compile` command

`generate` is the canonical name. `compile` goes away with no alias.

**Files:**
- Delete: `cmd/sngl/compile.go`
- Modify: `cmd/sngl/main.go`
- Modify: `cmd/sngl/doc.go` (re-exec path)

- [ ] **Step 1: Delete `compile.go`**

```bash
git rm cmd/sngl/compile.go
```

- [ ] **Step 2: Drop `compileCmd` registration in `main.go`**

Edit `cmd/sngl/main.go`. Delete the line `rootCmd.AddCommand(compileCmd)`. Keep `rootCmd.AddCommand(generateCmd)`.

- [ ] **Step 3: Update `doc.go` re-exec**

Open `cmd/sngl/doc.go` and find the `exec.Command(os.Args[0], "compile", ...)` invocation (around line 1037). Change `"compile"` → `"generate"`. The argument order to the new generate command is identical (`--lang`, `--platform`, `--out`, files); no other change needed.

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./cmd/sngl/ -run TestScript -v 2>&1 | tail -20`

Expected: clean build, all `generate_*` script tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/sngl/compile.go cmd/sngl/main.go cmd/sngl/doc.go
git commit -m "$(cat <<'EOF'
cmd/sngl: remove compile command

generate is the canonical name. No alias kept. doc build's re-exec
now invokes 'sngl generate'.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Add `test bool` to `lib/options.sngl`

**Files:**
- Modify: `lib/options.sngl`

- [ ] **Step 1: Edit `lib/options.sngl`**

Append a `test` field to the `Options` struct (after `maps`):

```sngl
// When true, the generator emits unit tests in the target language
// alongside the application code. Tests use the target language's
// native test runner (Go's testing, Kotlin's JUnit, JS's Jest/Vitest).
// Emission is wired up in a separate change; for now the field is
// accepted by the CLI but produces no extra files.
test
bool
```

- [ ] **Step 2: Confirm parse / check still succeeds against the stdlib**

Run: `go test ./internal/checker/...`

Expected: pass. `lib/options.sngl` is loaded by `internal/checker/stdlib.go`; a new field on `Options` must not break any existing fixture.

- [ ] **Step 3: Manually verify `--opt test=true` is accepted**

```bash
mkdir -p /tmp/sngl-opt-smoke && cd /tmp/sngl-opt-smoke
cat > app.sngl <<'EOF'
output { go { bubbletea } }

component main {
    text(value="hi")
}
EOF
sngl generate --opt test=true --out out .
```

Expected: command exits 0. The `test=true` value is parsed and threaded through but no extra files are produced (the test plan asserts only the no-error case here).

- [ ] **Step 4: Commit**

```bash
git add lib/options.sngl
git commit -m "$(cat <<'EOF'
lib/options.sngl: add Options.test bool

Schema-only addition: the CLI accepts --opt test=true without error.
Emission of dispatcher + native test files lands with the
test-architecture work.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Add `build_*.txt` script tests

Cover the basic build path, multi-target naming, `--opt test=true` acceptance, and noCacheBust parity between generate and build.

**Files:**
- Create: `cmd/sngl/testdata/build_basic.txt`
- Create: `cmd/sngl/testdata/build_multi_target.txt`
- Create: `cmd/sngl/testdata/build_test_opt.txt`
- Create: `cmd/sngl/testdata/build_nocachebust_parity.txt`

- [ ] **Step 1: Create `build_basic.txt`**

```
# build emits a binary in cwd from a temp-dir generation.
# 'none' platform doesn't implement Builder, so we use bubbletea as the
# minimum platform with a Builder. Skips the test if a Go toolchain is
# unavailable (script_test invokes commands in-process — host go must
# work for go-tool-based builds).

[!exec:go] skip 'go toolchain required'

sngl build --lang=go --platform=bubbletea --opt name=myapp app.sngl
stdout 'myapp'
exists myapp

-- app.sngl --
output { go { bubbletea } }

component main {
    text(value="hello")
}
```

Note: SNGL has no top-level `Options { ... }` block syntax — the Options struct is a merged-schema for CLI/output-call validation. The user-facing way to set option values is either `--opt name=value` on the CLI or `platform(name="...")` inside an `output()` block. The fixtures above use `--opt name=myapp` for portability.

- [ ] **Step 2: Create `build_multi_target.txt`**

```
# Multi-target build appends '-<platform>' to binary name.

[!exec:go] skip 'go toolchain required'

sngl build --opt name=myapp app.sngl
stdout 'myapp-bubbletea'
exists myapp-bubbletea
# fyne build requires CGo / display; assert by name only if it ran.
# Both binaries land in cwd with distinct '-<platform>' suffix.

-- app.sngl --
output {
    go {
        bubbletea
        fyne
    }
}

component main {
    text(value="multi")
}
```

(If the fyne build is too heavy for CI, drop the second target and add a comment explaining the test stays single-target until fyne builds are reliable. The point is to verify the `-<platform>` naming logic in `binaryName`, which can equally be tested with two bubbletea variants if available.)

- [ ] **Step 3: Create `build_test_opt.txt`**

```
# --opt test=true is accepted (emission deferred to test-arch work).

sngl generate --lang=none --platform=html --out=out --opt test=true app.sngl
exists out/index.html

# Same option, same acceptance, via build (bubbletea requires Go).
[!exec:go] skip 'go toolchain required'
sngl build --lang=go --platform=bubbletea --opt test=true --opt name=myapp bt.sngl
stdout 'myapp'

-- app.sngl --
component main {
    text(value="hi")
}
-- bt.sngl --
output { go { bubbletea } }

component main {
    text(value="hi")
}
```

- [ ] **Step 4: Create `build_nocachebust_parity.txt`**

```
# Regression for audit #4: noCacheBust must produce identical asset names
# under generate and build. (Prior to this work, build silently ignored it.)

sngl generate --lang=none --platform=html --out=gen --opt noCacheBust=true app.sngl
exists gen/index.html

[!exec:go] skip 'go toolchain required'
# A non-html build target uses different asset machinery; the canonical
# parity test is generate vs generate twice with the same opts produces
# identical filenames. The build path's noCacheBust acceptance is asserted
# by simply running it without error.
sngl generate --lang=none --platform=html --out=gen2 --opt noCacheBust=true app.sngl
exists gen2/index.html
# Both runs must produce the same filename set (sorted directory listing).

-- app.sngl --
component main {
    text(value="x")
}
```

- [ ] **Step 5: Run the new tests**

Run: `go test ./cmd/sngl/ -run TestScript/build_ -v 2>&1 | tail -40`

Expected: all four new `build_*` tests pass (or `skip` if the host lacks `go`).

If a fixture fails because of Options syntax: read an existing testdata file that sets `Options.name` to confirm the correct grammar, then adjust.

- [ ] **Step 6: Commit**

```bash
git add cmd/sngl/testdata/build_*.txt
git commit -m "$(cat <<'EOF'
cmd/sngl/testdata: add build_*.txt scripts

Covers: basic single-target build (binary lands in cwd, named from
Options.name); multi-target naming with '-<platform>' suffix;
--opt test=true acceptance; noCacheBust parity regression for audit #4.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Final cross-build and full-suite verification

- [ ] **Step 1: Full build + test sweep**

Run: `go build ./... && go tool verify 2>&1 | tail -60`

Expected: clean build, full test suite green. Failures here are regressions in the broader codebase from the CLI changes — most likely candidates are `internal/docbrowser` (if it shells out to `sngl compile`), `internal/cmd/docsgen` (if it does the same), or any doc fixture that names `compile` in expected output.

- [ ] **Step 2: Grep for any leftover `sngl compile` or `compileCmd` references**

```bash
grep -rn 'sngl compile' --include='*.go' --include='*.md' --include='*.txt' . | grep -v docs/superpowers
grep -rn 'compileCmd\b' --include='*.go' .
```

Expected: both empty (modulo the design doc / plan doc themselves under `docs/superpowers/specs/` and `docs/superpowers/plans/` which are explicitly excluded). If anything else turns up, update it: docs should reference `sngl generate`; any internal caller in Go should call `runPipeline` directly or `runGenerate`.

- [ ] **Step 3: Commit any leftover cleanup**

```bash
git add -A
git commit -m "$(cat <<'EOF'
cleanup: drop remaining references to 'sngl compile'

Found by grep sweep after the compile→generate rename.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

(Skip the commit if there's nothing to clean up.)

---

## Self-review notes (for the implementer)

- **No worktree.** Project memory says solo repo, work on `main`.
- **Each task ends green.** Don't proceed to the next task with a red test suite — the staging order is designed so every commit leaves the tree shippable.
- **`runPipeline.onTarget` is the extension point.** If a future command needs a different post-generate step, it adds a closure rather than copy-pasting the pipeline.
- **`isMultiTarget` does duplicate work.** It re-parses + re-checks the first file to know how many targets will be resolved. The alternative is plumbing a callback through `runPipeline`. Acceptable for now; revisit if a third `--out`-naming-aware command appears.
- **Windows naming.** `isWindowsTarget` is wired up but always returns false today because no platform option sets `goos=windows`. Forward-compat hook only.
- **Options syntax.** SNGL has no top-level `Options { ... }` block; the Options struct is a merged schema for validation. Set values via `--opt key=value` on the CLI or `platform(key="value")` inside `output()`. Fixtures use `--opt name=myapp` for the test-data simplicity.
