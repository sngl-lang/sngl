package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		doc, err := parseSNGL(filename, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}

		doc = mergeDir(doc, filename)

		if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}

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

			if err := optimize.Optimize(doc, optimize.Config{
				Platform: target.Platform,
				Language: target.Lang,
				Dir:      dir,
			}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}

			if err := generateTarget(filename, doc, target, outDir, true); err != nil {
				return err
			}

			artifact, err := builder.Build(outDir, target.Options)
			if err != nil {
				return fmt.Errorf("%s: build failed: %w", filename, err)
			}
			fmt.Println(artifact)
		}
	}
	return nil
}
