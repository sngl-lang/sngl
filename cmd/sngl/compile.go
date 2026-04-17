package main

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

type outputTarget struct {
	Lang     string
	Platform string
	Opts     codegen.Opts
	Options  map[string]string
}

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

	// Group files by directory so sibling .sngl files are merged into one
	// compilation unit (package-level semantics).
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
		pkg, err := checkDoc(doc, dir, true)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("check", "dir", dir, "duration", time.Since(start))

		if err := validateOutputs(pkg); err != nil {
			return err
		}

		targets := resolveTargets(pkg, cliLang, cliPlat, cliOpts)
		if len(targets) == 0 {
			return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", dir)
		}

		for _, target := range targets {
			if target.Options == nil {
				target.Options = make(map[string]string)
			}
			// Set projectDir for resolving relative paths in output options
			// (icon paths from output declarations are relative to the .sngl dir)
			if target.Options["projectDir"] == "" {
				target.Options["projectDir"] = dir
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

			targetDoc := ir.Convert(pkg)

			// Convert optimizer file assets to codegen file assets.
			var fileAssets []codegen.FileAsset
			for _, fa := range optCfg.FileAssets {
				fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath})
			}

			start = time.Now()
			if err := generateTarget(filename, targetDoc, pkg, target, outDir, fileAssets, quiet(cmd)); err != nil {
				return err
			}
			slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		}
	}
	return nil
}

func resolveTargets(pkg *ir.Package, cliLang, cliPlat string, cliOpts map[string]string) []outputTarget {
	if cliLang != "" && cliPlat != "" {
		return []outputTarget{{Lang: cliLang, Platform: cliPlat, Options: cliOpts}}
	}
	var targets []outputTarget
	for _, o := range pkg.Outputs {
		perTarget := make(map[string]string)
		maps.Copy(perTarget, o.Options)
		targets = append(targets, outputTarget{Lang: o.Lang, Platform: o.Platform, Options: perTarget})
	}
	return targets
}

func generateTarget(filename string, doc *ast.Document, pkg *ir.Package, target outputTarget, outDir string, fileAssets []codegen.FileAsset, q bool) error {
	lang := codegen.LookupLang(target.Lang)
	if lang == nil {
		return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
	}

	plat := codegen.LookupPlatform(target.Platform)
	if plat == nil {
		return fmt.Errorf("%s: unknown platform %q (available: %v)", filename, target.Platform, codegen.Platforms())
	}

	supported := slices.Contains(plat.SupportedLangs(), target.Lang)
	if !supported {
		return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", filename, target.Platform, target.Lang, plat.SupportedLangs())
	}

	resp, err := plat.Generate(&codegen.Request{
		Doc:        doc,
		Pkg:        pkg,
		Lang:       lang,
		Opts:       target.Opts,
		Options:    target.Options,
		Source:     filepath.Base(filename),
		FileAssets: fileAssets,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("%s: %s", filename, resp.Error)
	}

	for _, file := range resp.Files {
		path := filepath.Join(outDir, file.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteTo(f)
		f.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return writeErr
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
