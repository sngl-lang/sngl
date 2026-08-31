package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check [file|dir...]",
	Short: "Parse and type-check SNGL files",
	Args:  cobra.ArbitraryArgs,
	RunE:  runCheck,
}

func runCheck(cmd *cobra.Command, args []string) error {
	libs, paths, err := resolveInputs(args)
	if err != nil {
		return err
	}

	var failed bool
	for _, in := range libs {
		if err := in.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", in.Path, err)
			failed = true
			continue
		}
		if !quiet(cmd) {
			fmt.Printf("%s: ok\n", in.Path)
		}
	}

	// A package argument is the whole input. Only an invocation with no
	// argument at all falls back to the current directory, which is what
	// discoverFiles does with an empty list.
	if len(paths) > 0 || len(args) == 0 {
		units, err := resolveUnits(paths)
		if err != nil {
			return err
		}
		if len(units) == 0 {
			return fmt.Errorf("no .sngl files found")
		}

		for _, u := range units {
			start := time.Now()
			doc, err := u.doc()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", u.name, err)
				failed = true
				continue
			}
			slog.Info("parse", "unit", u.name, "duration", time.Since(start))

			start = time.Now()
			if _, err := checkDoc(doc, u.dir, true); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", u.name, err)
				failed = true
				continue
			}
			slog.Info("check", "unit", u.name, "duration", time.Since(start))

			if !quiet(cmd) {
				for _, filename := range u.files {
					fmt.Printf("%s: ok\n", filename)
				}
			}
		}
	}

	if failed {
		return fmt.Errorf("check failed")
	}
	return nil
}
