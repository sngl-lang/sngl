package main

import (
	"duckfam.us/sngl/internal/build"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate [flags] [file|dir...]",
	Short: "Generate target-language source from SNGL files",
	Args:  cobra.ArbitraryArgs,
	RunE:  runGenerate,
}

func init() {
	generateCmd.Flags().String("lang", "", "target language")
	generateCmd.Flags().String("platform", "", "target platform")
	generateCmd.Flags().StringP("out", "o", ".", "output directory")
	generateCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runGenerate(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: build.ParseCLIOpts(optSlice),
		outDir:  outDir,
		quiet:   quiet(cmd),
	})
}
