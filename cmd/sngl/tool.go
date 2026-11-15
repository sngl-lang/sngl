package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"duckfam.us/sngl/internal/cgogen"
	"duckfam.us/sngl/ir"
)

// toolCmd holds the generators a package's author runs by hand, or through
// `go generate`, rather than every build.
var toolCmd = &cobra.Command{
	Use:   "tool",
	Short: "Run a generator over a SNGL package",
}

var cgogenCmd = &cobra.Command{
	Use:   "cgogen sngl:<package> | <dir>",
	Short: "Write the Go package wrapping a package's C declarations",
	Long: `cgogen writes a Go package with one function for each #[c.native]
function of the package and of the packages under it that it imports, each
calling it through cgo, and marks each declaration #[go.native] with its
wrapper, so a Go program calls C with no cgo of its own.

The package is a library package, sngl:<name>, run from a checkout of this
repository since it writes the library's own files, or a directory of the
program's own, which a //go:generate line beside it keeps in step:

    //go:generate sngl tool cgogen .

The wrapper package goes beside the SNGL one, in cgo/, unless --out says
where, and its import path is read from the nearest go.mod.`,
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
	out, _ := cmd.Flags().GetString("out")
	goos, _ := cmd.Flags().GetString("goos")
	var plan *cgogen.Plan
	if name, ok := strings.CutPrefix(args[0], "sngl:"); ok {
		root, err := checkoutRoot()
		if err != nil {
			return err
		}
		if plan, err = cgogen.Lib(root, name, out, goos); err != nil {
			return err
		}
	} else {
		pkg, err := checkDir(args[0])
		if err != nil {
			return err
		}
		if plan, err = cgogen.Dir(pkg, args[0], out, goos); err != nil {
			return err
		}
	}
	return plan.Write()
}

// checkDir is the package the .sngl files in dir make, checked as a
// package a program imports.
func checkDir(dir string) (*ir.Package, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("cgogen wraps a library package, sngl:<name>, or a directory; %q is neither", dir)
	}
	units, err := resolveUnits([]string{dir})
	if err != nil {
		return nil, err
	}
	if len(units) != 1 {
		return nil, fmt.Errorf("%s: no .sngl files", dir)
	}
	doc, err := units[0].doc()
	if err != nil {
		return nil, err
	}
	return checkDoc(doc, units[0].dir, false, nil)
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
