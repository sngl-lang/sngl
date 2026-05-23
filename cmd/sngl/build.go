package main

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
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

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  outDir,
		main:    true,
		quiet:   quiet(cmd),
		onTarget: func(target outputTarget, _ *ir.Package, _, outDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
			}
			artifact, err := builder.Build(outDir, target.Options)
			if err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			fmt.Println(artifact)
			return nil
		},
	})
}
