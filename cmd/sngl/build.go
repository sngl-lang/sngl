package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
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

	cliLang, cliPlat, err := resolveLangPlat(cliLang, cliPlat)
	if err != nil {
		return err
	}

	cliOpts := parseCLIOpts(optSlice)

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

		targets, err := resolveTargets(pkg, cliLang, cliPlat, cliOpts)
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
			codegen.SetOptionField(target.Options, "main", true)
			if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
				codegen.SetOptionField(target.Options, "projectDir", dir)
			}

			plat := codegen.LookupPlatform(target.Platform)
			if plat == nil {
				return fmt.Errorf("%s: unknown platform %q", filename, target.Platform)
			}
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
			}
			lang := codegen.LookupLang(target.Lang)
			if lang == nil {
				return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
			}

			start = time.Now()
			optCfg := &optimize.Config{
				Platform: target.Platform,
				Language: target.Lang,
				Dir:      dir,
			}
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

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
			if err := generateTarget(filename, pkg, target, outDir, fileAssets, quiet(cmd)); err != nil {
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
