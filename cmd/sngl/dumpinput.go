package main

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"github.com/spf13/cobra"
	"golang.org/x/tools/txtar"
)

type dumpInput string

const (
	dumpInputAuto     dumpInput = "auto"
	dumpInputSNGL     dumpInput = "sngl"
	dumpInputStdin    dumpInput = "stdin"
	dumpInputTxtar    dumpInput = "txtar"
	dumpInputMarkdown dumpInput = "markdown"
)

func resolveDumpInput(cmd *cobra.Command, args []string) (dumpInput, error) {
	raw, _ := cmd.Flags().GetString("input")
	f := dumpInput(raw)
	switch f {
	case dumpInputAuto:
		if len(args) > 0 && args[0] == "-" {
			return dumpInputStdin, nil
		}
		if len(args) > 0 {
			switch strings.ToLower(filepath.Ext(args[0])) {
			case ".txt":
				return dumpInputTxtar, nil
			case ".md":
				return dumpInputMarkdown, nil
			}
		}
		return dumpInputSNGL, nil
	case dumpInputSNGL, dumpInputStdin, dumpInputTxtar, dumpInputMarkdown:
		return f, nil
	default:
		return "", fmt.Errorf("unknown input source %q (valid: auto, sngl, stdin, txtar, markdown)", raw)
	}
}

// dumpParseInput parses input according to the given source mode.
// Returns the parsed document and the directory context for imports.
func dumpParseInput(input dumpInput, args []string) (*ast.Document, string, error) {
	switch input {
	case dumpInputSNGL:
		return dumpParseAndMerge(args)
	case dumpInputStdin:
		return dumpParseStdin()
	case dumpInputTxtar:
		if len(args) == 0 {
			return nil, "", fmt.Errorf("txtar input requires a file argument")
		}
		return dumpParseTxtar(args[0])
	case dumpInputMarkdown:
		if len(args) == 0 {
			return nil, "", fmt.Errorf("markdown input requires a file argument")
		}
		return dumpParseMarkdown(args[0])
	}
	return nil, "", fmt.Errorf("unhandled input source %q", input)
}

// dumpParseAndMerge parses the input file/directory and merges siblings.
func dumpParseAndMerge(args []string) (*ast.Document, string, error) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}

	info, err := os.Stat(target)
	if err != nil {
		return nil, "", err
	}

	start := time.Now()
	if info.IsDir() {
		doc, err := parseDir(target)
		if err != nil {
			return nil, "", err
		}
		slog.Info("parse", "dir", target, "duration", time.Since(start))
		return doc, target, nil
	}

	f, err := os.Open(target)
	if err != nil {
		return nil, "", err
	}
	doc, err := parseSNGL(target, f)
	f.Close()
	if err != nil {
		return nil, "", err
	}
	slog.Info("parse", "file", target, "duration", time.Since(start))

	start = time.Now()
	doc = mergeDir(doc, target)
	slog.Info("merge", "file", target, "duration", time.Since(start))

	return doc, filepath.Dir(target), nil
}

func dumpParseStdin() (*ast.Document, string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, "", err
	}
	doc, err := parser.Parse("-", data)
	if err != nil {
		return nil, "", err
	}
	return doc, ".", nil
}

func dumpParseTxtar(path string) (*ast.Document, string, error) {
	ar, err := txtar.ParseFile(path)
	if err != nil {
		return nil, "", err
	}
	var doc *ast.Document
	for _, f := range ar.Files {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".sngl") {
			continue
		}
		d, err := parser.Parse(f.Name, f.Data)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", f.Name, err)
		}
		if doc == nil {
			doc = d
		} else {
			mergeInto(doc, d)
		}
	}
	if doc == nil {
		return nil, "", fmt.Errorf("%s: no .sngl files in txtar archive", path)
	}
	return doc, filepath.Dir(path), nil
}

func dumpParseMarkdown(path string) (*ast.Document, string, error) {
	blocks, err := extractMarkdownSNGL(path)
	if err != nil {
		return nil, "", err
	}
	if len(blocks) == 0 {
		return nil, "", fmt.Errorf("%s: no ```sngl code blocks found", path)
	}

	var src strings.Builder
	for _, b := range blocks {
		if b.annotation == "nocheck" {
			continue
		}
		src.WriteString(b.prelude)
		switch b.annotation {
		case "component":
			src.WriteString("component _block")
			src.WriteString(fmt.Sprint(b.line))
			src.WriteString(" {\n")
			src.WriteString(b.source)
			src.WriteString("\n}\n")
		case "expression":
			src.WriteString("component _block")
			src.WriteString(fmt.Sprint(b.line))
			src.WriteString(" {\n  computed _x = ")
			src.WriteString(strings.TrimSpace(b.source))
			src.WriteString("\n}\n")
		default:
			src.WriteString(b.source)
		}
	}

	doc, err := parser.Parse(path, []byte(src.String()))
	if err != nil {
		return nil, "", err
	}
	return doc, filepath.Dir(path), nil
}

type mdBlock struct {
	source     string
	line       int
	annotation string
	prelude    string
}

func extractMarkdownSNGL(path string) ([]mdBlock, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var blocks []mdBlock
	inBlock := false
	var current strings.Builder
	blockStart := 0
	var annotation, prelude string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "```sngl" {
			inBlock = true
			blockStart = i + 2 // 1-based, next line
			current.Reset()
			annotation, prelude = mdFindAnnotation(lines, i)
			continue
		}
		if inBlock && strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, mdBlock{
				source:     current.String(),
				line:       blockStart,
				annotation: annotation,
				prelude:    prelude,
			})
			continue
		}
		if inBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	return blocks, nil
}

// mdFindAnnotation looks backward from fenceLine to find a <!-- SNGL-... --> comment.
func mdFindAnnotation(lines []string, fenceLine int) (annotation, prelude string) {
	i := fenceLine - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 {
		return "", ""
	}

	prev := strings.TrimSpace(lines[i])
	if strings.HasPrefix(prev, "<!-- SNGL-") && strings.HasSuffix(prev, "-->") {
		inner := strings.TrimPrefix(prev, "<!-- SNGL-")
		inner = strings.TrimSuffix(inner, "-->")
		inner = strings.TrimSpace(inner)
		return inner, ""
	}

	if prev != "-->" {
		return "", ""
	}

	var preludeLines []string
	for i = i - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if after, ok := strings.CutPrefix(trimmed, "<!-- SNGL-"); ok {
			inner := strings.TrimSpace(after)
			if before, after, ok := strings.Cut(inner, " "); ok {
				annotation = before
				preludeLines = append([]string{after}, preludeLines...)
			} else if before, ok := strings.CutSuffix(inner, "-->"); ok {
				annotation = strings.TrimSpace(before)
			} else {
				annotation = inner
			}
			var b strings.Builder
			for _, pl := range preludeLines {
				b.WriteString(pl)
				b.WriteByte('\n')
			}
			return annotation, b.String()
		}
		preludeLines = append([]string{lines[i]}, preludeLines...)
	}
	return "", ""
}
