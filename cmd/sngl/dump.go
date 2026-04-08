package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"github.com/spf13/cobra"
)

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Dump compiler phase output",
}

var dumpParsedCmd = &cobra.Command{
	Use:   "parsed [file|dir]",
	Short: "Dump AST after parsing and merging",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDumpParsed,
}

var dumpCheckedCmd = &cobra.Command{
	Use:   "checked [file|dir]",
	Short: "Dump AST after type checking",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDumpChecked,
}

var dumpOptimizedCmd = &cobra.Command{
	Use:   "optimized [file|dir]",
	Short: "Dump AST after optimization",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDumpOptimized,
}

var dumpAnalysisCmd = &cobra.Command{
	Use:   "analysis [file|dir]",
	Short: "Dump codegen analysis as JSON",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDumpAnalysis,
}

func init() {
	dumpOptimizedCmd.Flags().String("lang", "", "target language")
	dumpOptimizedCmd.Flags().String("platform", "", "target platform")
	dumpAnalysisCmd.Flags().String("lang", "", "target language")
	dumpAnalysisCmd.Flags().String("platform", "", "target platform")

	dumpCmd.AddCommand(dumpParsedCmd, dumpCheckedCmd, dumpOptimizedCmd, dumpAnalysisCmd)
}

func runDumpParsed(cmd *cobra.Command, args []string) error {
	doc, _, err := dumpParseAndMerge(args)
	if err != nil {
		return err
	}
	fmt.Print(sngl.Format(doc))
	return nil
}

func runDumpChecked(cmd *cobra.Command, args []string) error {
	doc, dir, err := dumpParseAndMerge(args)
	if err != nil {
		return err
	}

	start := time.Now()
	if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), defaultFSSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	fmt.Print(sngl.Format(doc))
	return nil
}

func runDumpOptimized(cmd *cobra.Command, args []string) error {
	lang, platform, err := dumpTargetFlags(cmd)
	if err != nil {
		return err
	}

	doc, dir, err := dumpParseAndMerge(args)
	if err != nil {
		return err
	}

	start := time.Now()
	if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), defaultFSSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	start = time.Now()
	if err := optimize.Optimize(doc, optimize.Config{
		Platform: platform,
		Language: lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", lang, "platform", platform, "duration", time.Since(start))

	fmt.Print(sngl.Format(doc))
	return nil
}

func runDumpAnalysis(cmd *cobra.Command, args []string) error {
	lang, platform, err := dumpTargetFlags(cmd)
	if err != nil {
		return err
	}

	doc, dir, err := dumpParseAndMerge(args)
	if err != nil {
		return err
	}

	start := time.Now()
	if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), defaultFSSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	start = time.Now()
	if err := optimize.Optimize(doc, optimize.Config{
		Platform: platform,
		Language: lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", lang, "platform", platform, "duration", time.Since(start))

	analysis := codegen.AnalyzeCommon(doc)

	// Build a JSON-friendly representation
	out := analysisJSON{
		ModelFields:    sortedKeys(analysis.ModelFields),
		ComputedFields: sortedKeys(analysis.ComputedFields),
		ComputedDeps:   mapBoolToSlice(analysis.ComputedDeps),
		FuncNames:      sortedKeys(analysis.FuncNames),
		ExternFuncs:    sortedKeys(analysis.ExternFuncs),
		ExternVars:     sortedKeys(analysis.ExternVars),
		StructFields:   analysis.StructFields,
		UsedComponents: sortedKeys(analysis.UsedComponents),
		NeedsToast:     analysis.NeedsToast,
	}
	for _, c := range analysis.Components {
		out.Components = append(out.Components, c.Name)
	}
	for _, s := range analysis.Structs {
		out.Structs = append(out.Structs, s.Name)
	}
	for _, e := range analysis.Enums {
		out.Enums = append(out.Enums, e.Name)
	}
	for _, t := range analysis.Timers {
		out.Timers = append(out.Timers, timerJSON{
			Index:      t.Index,
			IntervalMs: t.IntervalMs,
			ActiveVar:  t.ActiveVar,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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

func dumpTargetFlags(cmd *cobra.Command) (lang, platform string, err error) {
	lang, _ = cmd.Flags().GetString("lang")
	platform, _ = cmd.Flags().GetString("platform")
	if lang == "" || platform == "" {
		return "", "", fmt.Errorf("--lang and --platform are required")
	}
	return lang, platform, nil
}

type analysisJSON struct {
	ModelFields    []string            `json:"modelFields"`
	ComputedFields []string            `json:"computedFields"`
	ComputedDeps   map[string][]string `json:"computedDeps"`
	FuncNames      []string            `json:"funcNames"`
	ExternFuncs    []string            `json:"externFuncs"`
	ExternVars     []string            `json:"externVars"`
	StructFields   map[string][]string `json:"structFields"`
	Components     []string            `json:"components"`
	Structs        []string            `json:"structs"`
	Enums          []string            `json:"enums"`
	Timers         []timerJSON         `json:"timers"`
	UsedComponents []string            `json:"usedComponents"`
	NeedsToast     bool                `json:"needsToast"`
}

type timerJSON struct {
	Index      int    `json:"index"`
	IntervalMs int    `json:"intervalMs"`
	ActiveVar  string `json:"activeVar"`
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices_sort(keys)
	return keys
}

func slices_sort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func mapBoolToSlice(m map[string]map[string]bool) map[string][]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = sortedKeys(v)
	}
	return out
}
