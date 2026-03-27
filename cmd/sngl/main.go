package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
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
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(lspCmd)
	rootCmd.AddCommand(fmtCmd)
	rootCmd.AddCommand(previewCmd)
	rootCmd.AddCommand(docCmd)
	rootCmd.AddCommand(snapshotCmd)
}

// proxyToGoTool checks if "go tool sngl" is available and execs into it.
// The SNGL_NO_PROXY env var prevents infinite recursion.
func proxyToGoTool() {
	if os.Getenv("SNGL_NO_PROXY") != "" {
		return
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return
	}

	// Check that "go tool sngl" is configured by running "go tool sngl version".
	check := exec.Command(goPath, "tool", "sngl", "version")
	check.Env = append(os.Environ(), "SNGL_NO_PROXY=1")
	if err := check.Run(); err != nil {
		return
	}

	fmt.Fprintln(os.Stderr, "sngl: proxying to go tool sngl")

	args := append([]string{goPath, "tool", "sngl"}, os.Args[1:]...)
	env := append(os.Environ(), "SNGL_NO_PROXY=1")
	if err := syscall.Exec(goPath, args, env); err != nil {
		fmt.Fprintf(os.Stderr, "sngl: proxy exec failed: %v\n", err)
	}
}

func main() {
	proxyToGoTool()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
