package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
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
			// Set projectDir for resolving relative paths in output options
			// (icon paths from output declarations are relative to the .sngl dir)
			if target.Options["projectDir"] == "" {
				target.Options["projectDir"] = dir
			}

			targetDoc := doc
			if len(targets) > 1 {
				targetDoc = doc.Clone()
			}
			if err := optimize.Optimize(targetDoc, optimize.Config{
				Platform: target.Platform,
				Language: target.Lang,
			}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			if err := generateTarget(filename, targetDoc, target, outDir, quiet(cmd)); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveTargets(doc *ast.Document, cliLang, cliPlat string, cliOpts map[string]string) []outputTarget {
	opts := optsFromDefaults(doc.OutputDefaults)
	if cliLang != "" && cliPlat != "" {
		return []outputTarget{{Lang: cliLang, Platform: cliPlat, Opts: opts, Options: cliOpts}}
	}
	var targets []outputTarget
	for _, o := range doc.Outputs {
		perTarget := make(map[string]string)
		maps.Copy(perTarget, o.Options)
		targets = append(targets, outputTarget{Lang: o.Lang, Platform: o.Platform, Opts: opts, Options: perTarget})
	}
	return targets
}

func optsFromDefaults(defaults map[string]string) codegen.Opts {
	return codegen.Opts{
		Name: defaults["name"],
		Icon: defaults["icon"],
	}
}

func generateTarget(filename string, doc *ast.Document, target outputTarget, outDir string, q bool) error {
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
		Doc:     doc,
		Lang:    lang,
		Opts:    target.Opts,
		Options: target.Options,
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
