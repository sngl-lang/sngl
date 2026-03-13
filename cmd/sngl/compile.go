package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/checker"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/parser"
	"github.com/spf13/cobra"
)

var compileCmd = &cobra.Command{
	Use:   "compile [flags] <file...>",
	Short: "Compile SNGL files to target platform",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runCompile,
}

func init() {
	compileCmd.Flags().String("lang", "", "target language (required)")
	compileCmd.Flags().String("platform", "", "target platform (required)")
	compileCmd.Flags().String("out", ".", "output directory")
	compileCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
	compileCmd.MarkFlagRequired("lang")
	compileCmd.MarkFlagRequired("platform")
}

func runCompile(cmd *cobra.Command, args []string) error {
	langName, _ := cmd.Flags().GetString("lang")
	platName, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	lang := codegen.LookupLang(langName)
	if lang == nil {
		return fmt.Errorf("unknown language %q (available: %v)", langName, codegen.Langs())
	}

	plat := codegen.LookupPlatform(platName)
	if plat == nil {
		return fmt.Errorf("unknown platform %q (available: %v)", platName, codegen.Platforms())
	}

	supported := false
	for _, l := range plat.SupportedLangs() {
		if l == langName {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("platform %q does not support language %q (supported: %v)", platName, langName, plat.SupportedLangs())
	}

	opts := make(map[string]string)
	for _, kv := range optSlice {
		k, v, _ := strings.Cut(kv, "=")
		opts[k] = v
	}

	for _, filename := range args {
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

		resp, err := plat.Generate(&codegen.Request{
			Doc:     doc,
			Lang:    lang,
			Options: opts,
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
			if !quiet(cmd) {
				fmt.Println(path)
			}
		}
	}
	return nil
}

func quiet(cmd *cobra.Command) bool {
	q, _ := cmd.Flags().GetBool("quiet")
	return q
}
