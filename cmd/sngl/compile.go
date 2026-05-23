package main

import (
	"github.com/spf13/cobra"
)

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

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  outDir,
		quiet:   quiet(cmd),
	})
}
