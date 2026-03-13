package main

import (
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/checker"
	"git.duckfam.us/jonathan/sngl/parser"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check [file|dir...]",
	Short: "Parse and type-check SNGL files",
	Args:  cobra.ArbitraryArgs,
	RunE:  runCheck,
}

func runCheck(cmd *cobra.Command, args []string) error {
	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl or .kdl files found")
	}

	var failed bool
	for _, filename := range files {
		f, err := os.Open(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			failed = true
			continue
		}

		doc, err := parser.Parse(filename, f)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			failed = true
			continue
		}

		if err := checker.Check(doc); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			failed = true
			continue
		}

		if !quiet(cmd) {
			fmt.Printf("%s: ok\n", filename)
		}
	}
	if failed {
		return fmt.Errorf("check failed")
	}
	return nil
}
