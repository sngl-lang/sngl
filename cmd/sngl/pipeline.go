package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/ir"
)

// main=true sets the "main" option, so platforms emit a runnable entry point.
type pipelineOpts struct {
	cliLang string
	cliPlat string
	cliOpts map[string]string
	outDir  string
	main    bool
	quiet   bool
	// library marks the input a library package the command line named, which
	// is exempt from the window rule -- see build.Options.Library.
	library bool
	// onTarget runs after generation, per target. Nil = generate-only.
	// For an interpreted target it runs instead of generation, against the
	// checked package.
	onTarget func(target build.Target, pkg *ir.Package, dir, outDir string) error
}

func runPipeline(cmd *cobra.Command, args []string, p pipelineOpts) error {
	cliLang, cliPlat, err := build.ResolveLangPlat(p.cliLang, p.cliPlat)
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

	libs, paths, err := resolveInputs(args)
	if err != nil {
		return err
	}
	for _, in := range libs {
		if err := in.Err(); err != nil {
			return fmt.Errorf("%s: %w", in.Path, err)
		}
		// The package has no project directory of its own — its source is
		// embedded, or the target synthesized it — so "." means generating into
		// the working directory.
		libOpts := p
		libOpts.library = true
		if err := emitPackage(in.Pkg, in.Path, ".", cliLang, cliPlat, libOpts); err != nil {
			return err
		}
	}

	// A package named and no paths left is the whole input: only fall through
	// to the current directory when no argument was given at all.
	if len(paths) == 0 && len(args) > 0 {
		return nil
	}

	units, err := resolveUnits(paths)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	// A named library package is output the caller asked for, so it settles the
	// question below before the units are walked at all.
	emitted := len(libs) > 0
	var skipped []string

	for _, u := range units {
		start := time.Now()
		doc, err := u.doc()
		if err != nil {
			return fmt.Errorf("%s: %w", u.name, err)
		}
		slog.Info("parse", "unit", u.name, "duration", time.Since(start))

		start = time.Now()
		pkg, err := checkDoc(doc, u.dir, true, build.SelectedTargets(cliLang, cliPlat)...)
		if err != nil {
			return fmt.Errorf("%s: %w", u.name, err)
		}
		slog.Info("check", "unit", u.name, "duration", time.Since(start))

		// A directory walk reaches a program's library packages too, and a
		// library has nothing to emit -- generating one wrote a second
		// index.html over the program's. A file named on the command line is
		// generated whether or not its body renders anything, since naming it
		// is the request.
		if !u.solo && !pkg.IsProgram() {
			slog.Info("skip: renders nothing", "unit", u.name)
			skipped = append(skipped, u.name)
			continue
		}

		if err := emitPackage(pkg, u.headline(), u.dir, cliLang, cliPlat, p); err != nil {
			return err
		}
		emitted = true
	}

	// Only a command line holds the whole set of units, which is why
	// internal/build, seeing one package at a time, cannot ask this.
	if !emitted && len(skipped) > 0 {
		return fmt.Errorf("nothing to generate: nothing rendered at the root of a file in %s: a program's package body is its view", strings.Join(skipped, ", "))
	}
	return nil
}

// Split out because a package addressed by `sngl:<uri>` arrives already
// checked, the checker having built it under the lib-source rules its own
// imports need: the two entry points share everything after the check and
// nothing before it.
//
// The compile itself is internal/build's, which is also what the golden test
// harness runs — see that package's doc comment. What stays here is what only
// a command line wants: files on disk, paths on stdout, and onTarget.
//
// name identifies the input in diagnostics — a file path, or the URI as
// written. dir is what imports and generated paths resolve against.
func emitPackage(pkg *ir.Package, name, dir, cliLang, cliPlat string, p pipelineOpts) error {
	results, err := build.Emit(pkg, build.Options{
		Name:     name,
		Dir:      dir,
		Lang:     cliLang,
		Platform: cliPlat,
		Opts:     p.cliOpts,
		Main:     p.main,
		OutDir:   p.outDir,
		Library:  p.library,
		Trust:    cliTrust,
	})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, res := range results {
		printWarnings(res.Warnings, seen)
	}
	for _, res := range results {
		out := p.outDir
		// A command that builds what it generates -- run, build -- owns its
		// output directory, and each target is a program of its own: written
		// into one directory, the second target's build found the first's
		// files beside its own and declared main twice.
		if p.onTarget != nil && len(results) > 1 {
			out = filepath.Join(p.outDir, res.Target.Lang+"-"+res.Target.Platform)
		}
		if err := writeFiles(res.Files, out, p.quiet); err != nil {
			return err
		}
		if p.onTarget != nil {
			if err := p.onTarget(res.Target, res.Pkg, dir, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeFiles puts one target's generated files on disk.
func writeFiles(files map[string][]byte, outDir string, q bool) error {
	// Sorted so the write order, and the printed paths, are deterministic.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(outDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
		slog.Info("wrote", "path", path)
		if !q {
			fmt.Println(path)
		}
	}
	return nil
}

func quiet(cmd *cobra.Command) bool {
	q, _ := cmd.Flags().GetBool("quiet")
	return q
}
