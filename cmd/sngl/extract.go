package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/spf13/cobra"
)

var extractCmd = &cobra.Command{
	Use:   "extract [flags] [file|dir...]",
	Short: "Extract translatable strings from SNGL source files",
	Long: `Walk SNGL source files and collect all translatable string literals
(written with the $ prefix, e.g. $"Login" or $"Hello {name}!").

Outputs a translation template in the requested format:
  json  — JSON array of message entries (default)
  pot   — GNU gettext .pot file
  arb   — Application Resource Bundle (Flutter/Dart)`,
	Args: cobra.ArbitraryArgs,
	RunE: runExtract,
}

func init() {
	extractCmd.Flags().String("out", "", "output file (default: stdout)")
	extractCmd.Flags().String("out-format", "json", "template format: json, pot, arb")
}

// TranslatableEntry is one extracted translatable string.
type TranslatableEntry struct {
	MsgID string `json:"msgid"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	// Notes contains context comments found adjacent to the string (currently empty; reserved).
	Notes string `json:"notes,omitempty"`
}

func runExtract(cmd *cobra.Command, args []string) error {
	outPath, _ := cmd.Flags().GetString("out")
	outFmt, _ := cmd.Flags().GetString("out-format")

	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	var entries []TranslatableEntry
	seen := make(map[string]bool) // deduplicate by (msgid, file)

	for _, filename := range files {
		f, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		doc, err := parseSNGL(filename, f)
		f.Close()
		if err != nil {
			// Skip files that don't parse rather than aborting.
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", filename, err)
			continue
		}

		rel := filename
		if abs, err2 := filepath.Abs(filename); err2 == nil {
			if wd, err3 := os.Getwd(); err3 == nil {
				if r, err4 := filepath.Rel(wd, abs); err4 == nil {
					rel = r
				}
			}
		}

		collectFromDoc(doc, rel, &entries, seen)
	}

	var out io.Writer = os.Stdout
	if outPath != "" {
		fout, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer fout.Close()
		out = fout
	}

	switch strings.ToLower(outFmt) {
	case "pot":
		return writePOT(out, entries)
	case "arb":
		return writeARB(out, entries)
	default:
		return writeJSON(out, entries)
	}
}

// collectFromDoc walks an AST document and appends TranslatableEntry values for
// every $"..." literal found.
func collectFromDoc(doc *ast.Document, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	for _, stmt := range doc.Stmts {
		collectFromStmt(stmt, file, entries, seen)
	}
}

func addEntry(x *ast.InterpolationExpr, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	msgid := buildMsgID(x)
	key := file + "\x00" + msgid
	if seen[key] {
		return
	}
	seen[key] = true
	*entries = append(*entries, TranslatableEntry{
		MsgID: msgid,
		File:  file,
		Line:  x.Pos.Line,
	})
}

// buildMsgID builds the message template from an interpolation expression.
// Expression holes are replaced with numbered placeholders: {0}, {1}, …
func buildMsgID(x *ast.InterpolationExpr) string {
	var sb strings.Builder
	holeIdx := 0
	for _, part := range x.Parts {
		if lit, ok := part.(*ast.LiteralExpr); ok {
			sb.WriteString(lit.Raw)
		} else {
			fmt.Fprintf(&sb, "{%d}", holeIdx)
			holeIdx++
		}
	}
	return sb.String()
}

func collectFromArgList(args ast.ArgList, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok {
			collectFromExpr(arg.Value, file, entries, seen)
		}
		if eh, ok := a.(ast.EventHandler); ok {
			collectFromBlock(eh.Body, file, entries, seen)
		}
	}
}

func collectFromExpr(e ast.Expr, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.InterpolationExpr:
		if x.Translatable {
			addEntry(x, file, entries, seen)
		}
		for _, p := range x.Parts {
			collectFromExpr(p, file, entries, seen)
		}
	case *ast.BinaryExpr:
		collectFromExpr(x.Left, file, entries, seen)
		collectFromExpr(x.Right, file, entries, seen)
	case *ast.UnaryExpr:
		collectFromExpr(x.Operand, file, entries, seen)
	case *ast.TernaryExpr:
		collectFromExpr(x.Cond, file, entries, seen)
		collectFromExpr(x.Then, file, entries, seen)
		collectFromExpr(x.Else, file, entries, seen)
	case *ast.CallExpr:
		collectFromExpr(x.Func, file, entries, seen)
		collectFromArgList(x.Args, file, entries, seen)
	case *ast.SelectExpr:
		collectFromExpr(x.Operand, file, entries, seen)
	case *ast.IndexExpr:
		collectFromExpr(x.Operand, file, entries, seen)
		collectFromExpr(x.Index, file, entries, seen)
	case *ast.StructExpr:
		for _, f := range x.Fields {
			collectFromExpr(f.Value, file, entries, seen)
		}
	case *ast.ListExpr:
		for _, elem := range x.Elements {
			collectFromExpr(elem, file, entries, seen)
		}
	case *ast.LambdaExpr:
		for _, p := range x.Params.Params {
			collectFromExpr(p.Default, file, entries, seen)
		}
		collectFromBlock(x.Block, file, entries, seen)
		collectFromExpr(x.Body, file, entries, seen)
	case *ast.ParenExpr:
		collectFromExpr(x.Inner, file, entries, seen)
	case *ast.SpreadExpr:
		collectFromExpr(x.Operand, file, entries, seen)
	case *ast.ConstExpr:
		collectFromExpr(x.Operand, file, entries, seen)
	}
}

func collectFromBlock(block ast.StmtBlock, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	for _, s := range block.Stmts {
		collectFromStmt(s, file, entries, seen)
	}
}

func collectFromStmt(stmt ast.Stmt, file string, entries *[]TranslatableEntry, seen map[string]bool) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.ComponentDecl:
		for _, p := range s.Props.Props {
			if param, ok := p.(ast.Param); ok {
				collectFromExpr(param.Default, file, entries, seen)
			}
		}
		collectFromBlock(s.Body, file, entries, seen)
	case *ast.VarDecl:
		for _, spec := range s.Specs {
			collectFromExpr(spec.Default, file, entries, seen)
		}
	case *ast.ConstDecl:
		for _, spec := range s.Specs {
			collectFromExpr(spec.Default, file, entries, seen)
		}
	case *ast.FuncDef:
		for _, p := range s.Params.Params {
			collectFromExpr(p.Default, file, entries, seen)
		}
		collectFromBlock(s.Block, file, entries, seen)
		collectFromExpr(s.Body, file, entries, seen)
	case *ast.VisualNode:
		collectFromArgList(s.Args, file, entries, seen)
		collectFromBlock(s.Block, file, entries, seen)
	case *ast.AssignStmt:
		collectFromExpr(s.Value, file, entries, seen)
	case *ast.EmitStmt:
		collectFromArgList(s.Args, file, entries, seen)
	case *ast.IfStmt:
		collectFromExpr(s.Cond, file, entries, seen)
		collectFromBlock(s.Body, file, entries, seen)
		collectFromBlock(s.Else, file, entries, seen)
	case *ast.ForStmt:
		collectFromExpr(s.Iter, file, entries, seen)
		collectFromBlock(s.Body, file, entries, seen)
		collectFromBlock(s.Else, file, entries, seen)
	case *ast.ReturnStmt:
		collectFromExpr(s.Value, file, entries, seen)
	case *ast.PlatformStmt:
		collectFromBlock(s.Body, file, entries, seen)
	case *ast.VarStmt:
		collectFromExpr(s.Init, file, entries, seen)
	case *ast.CallStmt:
		collectFromArgList(s.Call.Args, file, entries, seen)
	case *ast.DisabledDecl:
		// intentionally skip slashdash'd nodes
	case *ast.StructDef:
		for _, f := range s.Fields {
			collectFromExpr(f.Default, file, entries, seen)
		}
	case ast.Expr:
		collectFromExpr(s, file, entries, seen)
	}
}

// writeJSON writes entries as a JSON object with a "strings" array.
func writeJSON(w io.Writer, entries []TranslatableEntry) error {
	type payload struct {
		Strings []TranslatableEntry `json:"strings"`
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload{Strings: entries})
}

// writePOT writes entries in GNU gettext .pot format.
func writePOT(w io.Writer, entries []TranslatableEntry) error {
	fmt.Fprintln(w, `# SNGL i18n extraction`)
	fmt.Fprintln(w, `# Generated by sngl extract`)
	fmt.Fprintln(w, `msgid ""`)
	fmt.Fprintln(w, `msgstr ""`)
	fmt.Fprintln(w, `"Content-Type: text/plain; charset=UTF-8\n"`)
	fmt.Fprintln(w, `"Content-Transfer-Encoding: 8bit\n"`)
	fmt.Fprintln(w)
	for _, e := range entries {
		if e.File != "" {
			fmt.Fprintf(w, "#: %s:%d\n", e.File, e.Line)
		}
		if e.Notes != "" {
			fmt.Fprintf(w, "#. %s\n", e.Notes)
		}
		fmt.Fprintf(w, "msgid %q\n", e.MsgID)
		fmt.Fprintln(w, `msgstr ""`)
		fmt.Fprintln(w)
	}
	return nil
}

// writeARB writes entries in Flutter Application Resource Bundle (.arb) format.
func writeARB(w io.Writer, entries []TranslatableEntry) error {
	// ARB is a JSON object: {"@@locale": "en", "key": "value", "@key": {"description": ""}}
	out := make(map[string]any)
	out["@@locale"] = "en"
	for i, e := range entries {
		key := fmt.Sprintf("msg%d", i)
		out[key] = e.MsgID
		out["@"+key] = map[string]any{
			"description": "",
			"source":      fmt.Sprintf("%s:%d", e.File, e.Line),
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
