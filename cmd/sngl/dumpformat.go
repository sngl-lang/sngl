package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/chroma/quick"
	"github.com/davecgh/go-spew/spew"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
)

var dumpSpew = &spew.ConfigState{
	Indent:                  " ",
	DisablePointerAddresses: true,
	DisableCapacities:       true,
	SortKeys:                true,
}

type dumpFormat string

const (
	dumpFormatAuto  dumpFormat = "auto"
	dumpFormatSpew  dumpFormat = "spew"
	dumpFormatColor dumpFormat = "color"
	dumpFormatJSON  dumpFormat = "json"
	dumpFormatSNGL  dumpFormat = "sngl"
)

func resolveDumpFormat(cmd *cobra.Command) (dumpFormat, error) {
	raw, _ := cmd.Flags().GetString("format")
	f := dumpFormat(raw)
	switch f {
	case dumpFormatAuto:
		if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
			return dumpFormatColor, nil
		}
		return dumpFormatSpew, nil
	case dumpFormatSpew, dumpFormatColor, dumpFormatJSON, dumpFormatSNGL:
		return f, nil
	default:
		return "", fmt.Errorf("unknown dump format %q (valid: auto, spew, color, json, sngl)", raw)
	}
}

func dumpDocument(f dumpFormat, doc any) error {
	if len(dumpOmitSet) > 0 && f != dumpFormatSNGL {
		omitFields(doc, dumpOmitSet)
	}
	switch f {
	case dumpFormatSNGL:
		switch doc := doc.(type) {
		case *ast.Document:
			fmt.Print(sngl.Format(doc))
		default:
			return fmt.Errorf("unable to format output of type %T as sngl source", doc)
		}
	case dumpFormatSpew:
		dumpSpew.Fdump(os.Stdout, doc)
	case dumpFormatColor:
		fmt.Print(colorSpew(doc))
	case dumpFormatJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	return nil
}

// colorSpew runs spew on v and applies chroma syntax highlighting
// line-by-line to preserve leading indentation.
func colorSpew(v ...any) string {
	raw := dumpSpew.Sdump(v...)
	lines := strings.Split(raw, "\n")
	var out strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(trimmed)]
		if trimmed == "" {
			out.WriteByte('\n')
			continue
		}
		var buf bytes.Buffer
		if err := quick.Highlight(&buf, trimmed, "go", "terminal16m", "dracula"); err != nil {
			out.WriteString(line)
		} else {
			out.WriteString(indent)
			// Highlight adds a trailing newline; strip it since we add our own.
			highlighted := strings.TrimRight(buf.String(), "\n")
			out.WriteString(highlighted)
		}
		out.WriteByte('\n')
	}
	return out.String()
}
