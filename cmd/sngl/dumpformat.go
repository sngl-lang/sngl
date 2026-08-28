package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/davecgh/go-spew/spew"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/highlight"
	"git.duckfam.us/jonathan/sngl/ir"
)

var dumpSpew = &spew.ConfigState{
	Indent:                  " ",
	DisablePointerAddresses: true,
	DisableCapacities:       true,
	SortKeys:                true,
}

type dumpFormat string

const (
	dumpFormatSpew dumpFormat = "spew"
	dumpFormatJSON dumpFormat = "json"
	dumpFormatSNGL dumpFormat = "sngl"
)

var dumpColor bool

func resolveDumpFormat(cmd *cobra.Command) (dumpFormat, error) {
	raw, _ := cmd.Flags().GetString("format")
	f := dumpFormat(raw)
	switch f {
	case dumpFormatSpew, dumpFormatJSON, dumpFormatSNGL:
		return f, nil
	default:
		return "", fmt.Errorf("unknown dump format %q (valid: spew, json, sngl)", raw)
	}
}

// dumpDefaultFormat is the format a stage falls back to when --format was not
// given. Every stage but one dumps a program, which the sngl default renders
// as source; the analysis dumps a set of derived facts *about* a program, and
// those have no source form to render. Defaulting it to json prints the stage
// rather than failing on a flag nobody set.
func dumpDefaultFormat(cmd *cobra.Command, stage string, f dumpFormat) dumpFormat {
	if stage == "analysis" && !cmd.Flags().Changed("format") {
		return dumpFormatJSON
	}
	return f
}

func resolveColor(cmd *cobra.Command) error {
	raw, _ := cmd.Flags().GetString("color")
	switch raw {
	case "auto":
		dumpColor = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	case "on":
		dumpColor = true
	case "off":
		dumpColor = false
	default:
		return fmt.Errorf("unknown color mode %q (valid: auto, on, off)", raw)
	}
	return nil
}

func dumpDocument(f dumpFormat, doc any) error {
	if len(dumpOmitSet) > 0 && f != dumpFormatSNGL {
		omitFields(doc, dumpOmitSet)
	}
	var text string
	var lexer string
	switch f {
	case dumpFormatSNGL:
		switch doc := doc.(type) {
		case *ast.Document:
			text = sngl.Format(doc)
		case *ir.Package:
			text = sngl.Format(ir.Convert(doc))
		default:
			return fmt.Errorf("cannot render %T as sngl source: only a parsed document or a checked package has one — try --format json or --format spew", doc)
		}
		lexer = "SNGL"
	case dumpFormatSpew:
		text = dumpSpew.Sdump(doc)
		lexer = "spew"
	case dumpFormatJSON:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return err
		}
		text = buf.String()
		lexer = "JSON"
	}
	if dumpColor {
		text = highlight.Terminal(text, lexer, "dracula")
	}
	fmt.Print(text)
	return nil
}
