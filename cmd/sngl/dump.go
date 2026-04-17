package main

import (
	"fmt"
	"log/slog"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Dump compiler phase output",
	Long: `Dump compiler phase output in various formats.

Output format is unstable and may change between versions.`,
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
	Short: "Dump codegen analysis",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDumpAnalysis,
}

func init() {
	dumpCmd.PersistentFlags().String("format", "spew", "output format (spew, json, sngl)")
	dumpCmd.PersistentFlags().String("color", "auto", "colorize output (auto, on, off)")
	dumpCmd.PersistentFlags().String("input", "auto", "input source (auto, sngl, stdin, txtar, markdown)")
	dumpCmd.PersistentFlags().Bool("pointers", false, "show pointer addresses (useful for identifying shared objects)")
	dumpCmd.PersistentFlags().Int("depth", 0, "maximum depth for spew output (default unlimited)")
	dumpCmd.PersistentFlags().StringSlice("omit", nil, "omit struct fields by name (comma-separated, e.g. --omit AST,Pos)")

	dumpOptimizedCmd.Flags().String("lang", "", "target language")
	dumpOptimizedCmd.Flags().String("platform", "", "target platform")
	dumpAnalysisCmd.Flags().String("lang", "", "target language")
	dumpAnalysisCmd.Flags().String("platform", "", "target platform")

	dumpCmd.AddCommand(dumpParsedCmd, dumpCheckedCmd, dumpOptimizedCmd, dumpAnalysisCmd)
}

var dumpOmitSet map[string]bool

func dumpResolveFlags(cmd *cobra.Command, args []string) (dumpFormat, dumpInput, error) {
	f, err := resolveDumpFormat(cmd)
	if err != nil {
		return "", "", err
	}
	if err := resolveColor(cmd); err != nil {
		return "", "", err
	}
	i, err := resolveDumpInput(cmd, args)
	if err != nil {
		return "", "", err
	}
	if ptrs, _ := cmd.Flags().GetBool("pointers"); ptrs {
		dumpSpew.DisablePointerAddresses = false
	}
	if depth, _ := cmd.Flags().GetInt("depth"); depth > 0 {
		dumpSpew.MaxDepth = depth
	}
	if names, _ := cmd.Flags().GetStringSlice("omit"); len(names) > 0 {
		dumpOmitSet = make(map[string]bool, len(names))
		for _, name := range names {
			dumpOmitSet[name] = true
		}
	}
	return f, i, nil
}

func runDumpParsed(cmd *cobra.Command, args []string) error {
	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}
	doc, _, err := dumpParseInput(inp, args)
	if err != nil {
		return err
	}
	return dumpDocument(f, doc)
}

func runDumpChecked(cmd *cobra.Command, args []string) error {
	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}
	doc, dir, err := dumpParseInput(inp, args)
	if err != nil {
		return err
	}

	start := time.Now()
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	return dumpDocument(f, pkg)
}

func runDumpOptimized(cmd *cobra.Command, args []string) error {
	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}

	doc, dir, err := dumpParseInput(inp, args)
	if err != nil {
		return err
	}

	start := time.Now()
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	target, err := dumpResolveTarget(cmd, pkg)
	if err != nil {
		return err
	}

	start = time.Now()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	doc = ir.Convert(pkg)
	return dumpDocument(f, doc)
}

func runDumpAnalysis(cmd *cobra.Command, args []string) error {
	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}

	doc, dir, err := dumpParseInput(inp, args)
	if err != nil {
		return err
	}

	start := time.Now()
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	target, err := dumpResolveTarget(cmd, pkg)
	if err != nil {
		return err
	}

	start = time.Now()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	doc = ir.Convert(pkg)
	return dumpDocument(f, codegen.AnalyzeCommon(doc))
}

func dumpResolveTarget(cmd *cobra.Command, pkg *ir.Package) (outputTarget, error) {
	lang, _ := cmd.Flags().GetString("lang")
	plat, _ := cmd.Flags().GetString("platform")
	if (lang == "") != (plat == "") {
		return outputTarget{}, fmt.Errorf("--lang and --platform must both be specified or both omitted")
	}
	targets := resolveTargets(pkg, lang, plat, nil)
	if len(targets) == 0 {
		return outputTarget{}, fmt.Errorf("no output target specified (use --lang/--platform flags or add an output node)")
	}
	return targets[0], nil
}
