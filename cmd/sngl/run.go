package main

import (
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] <file.sngl> [-- args...]",
	Short: "Compile and run an SNGL app",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().String("lang", "", "target language")
	runCmd.Flags().String("platform", "", "target platform")
	runCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

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
