package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"github.com/spf13/cobra"
)

// atomicWrite replaces filename with data via a sibling temp file +
// rename, so a partial write never leaves the destination corrupted.
func atomicWrite(filename string, data []byte) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".sngl-fmt-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filename)
}

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
	// fmt rewrites files in place, and a package addressed by URI has none:
	// its source is embedded in this binary, or synthesized by the target that
	// serves it. Saying so beats the stat error the URI used to produce, and
	// beats quietly turning fmt into a printer.
	for _, arg := range args {
		uri, err := libraryURI(arg)
		if err != nil {
			return err
		}
		if uri != "" {
			return fmt.Errorf("%s: fmt works on files, and this package has no source on disk", arg)
		}
	}

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

		if check {
			var buf bytes.Buffer
			if _, err := parser.FormatTo(doc, &buf); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
				unformatted = true
				continue
			}
			if !bytes.Equal(buf.Bytes(), original) {
				fmt.Fprintf(os.Stderr, "%s\n", filename)
				unformatted = true
			}
			continue
		}

		// Atomic write: format into a buffer, then replace the original via
		// rename. Truncating the source before writing risks corrupting it
		// if FormatTo errors mid-write.
		var buf bytes.Buffer
		if _, err := parser.FormatTo(doc, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			unformatted = true
			continue
		}
		if err := atomicWrite(filename, buf.Bytes()); err != nil {
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
