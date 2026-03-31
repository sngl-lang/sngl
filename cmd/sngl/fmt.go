package main

import (
	"bytes"
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"github.com/spf13/cobra"
)

var fmtCmd = &cobra.Command{
	Use:   "fmt [file|dir...]",
	Short: "Format SNGL files",
	Args:  cobra.ArbitraryArgs,
	RunE:  runFmt,
}

func init() {
	fmtCmd.Flags().Bool("check", false, "check formatting without modifying files")
}

func runFmt(cmd *cobra.Command, args []string) error {
	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	check, _ := cmd.Flags().GetBool("check")
	q := quiet(cmd)
	var unformatted bool

	for _, filename := range files {
		original, err := os.ReadFile(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			unformatted = true
			continue
		}

		doc, err := parseSNGL(filename, bytes.NewReader(original))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			unformatted = true
			continue
		}

		formatted := parser.Format(doc)
		if formatted == string(original) {
			continue
		}

		if check {
			fmt.Fprintf(os.Stderr, "%s\n", filename)
			unformatted = true
			continue
		}

		if err := os.WriteFile(filename, []byte(formatted), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			unformatted = true
			continue
		}

		if !q {
			fmt.Println(filename)
		}
	}

	if unformatted {
		if check {
			return fmt.Errorf("files are not formatted")
		}
		return fmt.Errorf("fmt failed")
	}
	return nil
}
