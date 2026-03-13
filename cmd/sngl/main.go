package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
)

var rootCmd = &cobra.Command{
	Use:   "sngl",
	Short: "SNGL compiler and toolchain",
}

func init() {
	rootCmd.PersistentFlags().String("project", ".", "project root directory")
	rootCmd.PersistentFlags().String("format", "text", "output format (text, json, sarif)")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress non-error output")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")

	rootCmd.AddCommand(compileCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(stubCmd("lint", "Lint SNGL files"))
	rootCmd.AddCommand(stubCmd("test", "Run SNGL tests"))
	rootCmd.AddCommand(stubCmd("lsp", "Start the SNGL language server"))
	rootCmd.AddCommand(stubCmd("fmt", "Format SNGL files"))
	rootCmd.AddCommand(stubCmd("preview", "Live-preview an SNGL app"))
	rootCmd.AddCommand(stubCmd("init", "Initialize a new SNGL project"))
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
