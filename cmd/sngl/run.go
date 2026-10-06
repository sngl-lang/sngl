package main

import (
	"fmt"
	"os"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/interprun"
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
	// Cobra takes the `--` out of args, and says where it stood.
	var progArgs []string
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		progArgs = args[at:]
	}

	// Stable, key-derived build directory so repeated invocations reuse
	// the Go build cache instead of relinking from scratch every time.
	// See codegen.BuildDir.
	key := []string{cliLang, cliPlat, strings.Join(optSlice, ","), file}
	tmpDir, release, err := codegen.BuildDir("run", key...)
	if err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}
	defer release()

	return runPipeline(cmd, []string{file}, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: build.ParseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true,
		onTarget: func(target build.Target, pkg *ir.Package, _, outDir string) error {
			if build.IsInterpreted(target) {
				dir, err := os.Getwd()
				if err != nil {
					return err
				}
				return interprun.Run(pkg, interprun.Options{
					Dir: dir, Args: progArgs, Platform: target.Platform,
					Headless: codegen.OptionString(target.Options, "host") == "mem",
				})
			}
			if !build.HasCommand(target, ir.BuiltinGenRun) {
				return fmt.Errorf("platform %q with language %q does not support direct execution", target.Platform, target.Lang)
			}
			args := make([]any, len(progArgs))
			for i, a := range progArgs {
				args[i] = a
			}
			return build.RunCommand(target, ir.BuiltinGenRun, outDir, args)
		},
	})
}
