package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build [flags] [file|dir...]",
	Short: "Build SNGL project into a runnable artifact",
	Args:  cobra.ArbitraryArgs,
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().String("lang", "", "target language")
	buildCmd.Flags().String("platform", "", "target platform")
	buildCmd.Flags().String("out", ".", "output directory")
	buildCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runBuild(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	if (cliLang == "") != (cliPlat == "") {
		return fmt.Errorf("--lang and --platform must both be specified or both omitted")
	}

	cliOpts := make(map[string]string)
	for _, kv := range optSlice {
		k, v, _ := strings.Cut(kv, "=")
		cliOpts[k] = v
	}

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

		start = time.Now()
		doc = mergeDir(doc, filename)
		slog.Info("merge", "dir", dir, "duration", time.Since(start))

		start = time.Now()
		if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), defaultFSSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("check", "dir", dir, "duration", time.Since(start))

		if err := validateOutputs(doc); err != nil {
			return err
		}

		targets := resolveTargets(doc, cliLang, cliPlat, cliOpts)
		if len(targets) == 0 {
			return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", dir)
		}

		for _, target := range targets {
			if target.Options == nil {
				target.Options = make(map[string]string)
			}
			target.Options["main"] = "true"
			if target.Options["projectDir"] == "" {
				target.Options["projectDir"] = dir
			}

			plat := codegen.LookupPlatform(target.Platform)
			if plat == nil {
				return fmt.Errorf("%s: unknown platform %q", filename, target.Platform)
			}
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
			}

			start = time.Now()
			if err := optimize.Optimize(doc, optimize.Config{
				Platform: target.Platform,
				Language: target.Lang,
				Dir:      dir,
			}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			start = time.Now()
			if err := generateTarget(filename, doc, target, outDir); err != nil {
				return err
			}
			slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			start = time.Now()
			artifact, err := builder.Build(outDir, target.Options)
			if err != nil {
				return fmt.Errorf("%s: build failed: %w", filename, err)
			}
			slog.Info("build", "artifact", artifact, "duration", time.Since(start))
			fmt.Println(artifact)
		}
	}
	return nil
}
