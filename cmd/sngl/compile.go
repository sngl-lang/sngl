package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/checker"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/parser"
	"github.com/spf13/cobra"
)

type outputTarget struct {
	Lang     string
	Platform string
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
		return fmt.Errorf("no .sngl or .kdl files found")
	}

	for _, filename := range files {
		f, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}

		doc, err := parser.Parse(filename, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}

		if err := checker.Check(doc); err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}

		targets := resolveTargets(doc, cliLang, cliPlat, cliOpts)
		if len(targets) == 0 {
			return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", filename)
		}

		for _, target := range targets {
			if err := generateTarget(filename, doc, target, outDir, quiet(cmd)); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveTargets(doc *ast.Document, cliLang, cliPlat string, cliOpts map[string]string) []outputTarget {
	if cliLang != "" && cliPlat != "" {
		return []outputTarget{{Lang: cliLang, Platform: cliPlat, Options: cliOpts}}
	}
	var targets []outputTarget
	for _, o := range doc.Outputs {
		opts := make(map[string]string)
		for k, v := range o.Options {
			opts[k] = v
		}
		targets = append(targets, outputTarget{Lang: o.Lang, Platform: o.Platform, Options: opts})
	}
	return targets
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

	supported := false
	for _, l := range plat.SupportedLangs() {
		if l == target.Lang {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", filename, target.Platform, target.Lang, plat.SupportedLangs())
	}

	resp, err := plat.Generate(&codegen.Request{
		Doc:     doc,
		Lang:    lang,
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
		if err := os.WriteFile(path, file.Content, 0o644); err != nil {
			return err
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
