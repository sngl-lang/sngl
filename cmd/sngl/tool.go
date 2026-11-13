package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"duckfam.us/sngl/internal/cgogen"
)

// toolCmd holds the generators a package's author runs by hand, or through
// `go generate`, rather than every build.
var toolCmd = &cobra.Command{
	Use:   "tool",
	Short: "Run a generator over a SNGL package",
}

var cgogenCmd = &cobra.Command{
	Use:   "cgogen sngl:<package>",
	Short: "Write the Go package wrapping a package's C declarations",
	Long: `cgogen writes a Go package with one function for each #[c.native]
function of the package and of the packages under it that it imports, each
calling it through cgo, and marks each declaration #[go.native] with its
wrapper, so a Go program calls C with no cgo of its own.

The package goes beside the SNGL one, in cgo/, unless --out says where, and
its import path is read from the nearest go.mod. Run it from a checkout of
this repository: it writes the library's own files.`,
	Args: cobra.ExactArgs(1),
	RunE: runCgogen,
}

func init() {
	cgogenCmd.Flags().String("out", "", "the wrapper package's directory (default: cgo/ beside the package)")
	cgogenCmd.Flags().String("goos", "", "the one GOOS the package builds for, where its headers are one system's")
	toolCmd.AddCommand(cgogenCmd)
	rootCmd.AddCommand(toolCmd)
}

func runCgogen(cmd *cobra.Command, args []string) error {
	name, ok := strings.CutPrefix(args[0], "sngl:")
	if !ok {
		return fmt.Errorf("cgogen wraps a library package, sngl:<name>; %q is none", args[0])
	}
	root, err := checkoutRoot()
	if err != nil {
		return err
	}
	out, _ := cmd.Flags().GetString("out")
	goos, _ := cmd.Flags().GetString("goos")
	plan, err := cgogen.Lib(root, name, out, goos)
	if err != nil {
		return err
	}
	return plan.Write()
}

// checkoutRoot is the checkout of this repository the working directory is
// in: the nearest directory above it holding lib/lib.go.
func checkoutRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "lib", "lib.go")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("%s is in no checkout of sngl, whose lib/ cgogen writes", wd)
		}
	}
}
