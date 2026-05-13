package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
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

var dumpLoweredCmd = &cobra.Command{
	Use:   "lowered [file|dir]",
	Short: "Dump IR after lowering passes",
	Long: `Dump IR after lowering. Use --after PASS to dump intermediate state
after a specific pass (case-sensitive, matching Caps field name).
--after none dumps post-optimize / pre-lower state.
--list prints resolved caps and pass list, then exits.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDumpLowered,
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

	dumpLoweredCmd.Flags().String("lang", "", "target language")
	dumpLoweredCmd.Flags().String("platform", "", "target platform")
	dumpLoweredCmd.Flags().String("after", "", "dump IR after named pass (e.g. NoToggle); 'none' = pre-lower state")
	dumpLoweredCmd.Flags().Bool("list", false, "print resolved caps and pass list, then exit")

	dumpCmd.AddCommand(dumpParsedCmd, dumpCheckedCmd, dumpOptimizedCmd, dumpAnalysisCmd, dumpLoweredCmd)
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

	// Also run lowering so the dump matches what codegen actually consumes.
	if plat := codegen.LookupPlatform(target.Platform); plat != nil {
		if lang := codegen.LookupLang(target.Lang); lang != nil {
			caps := plat.Capabilities().Merge(lang.Capabilities())
			if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform}); err != nil {
				return err
			}
		}
	}

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

	// Lower as well so analysis reflects post-lowering shape.
	if plat := codegen.LookupPlatform(target.Platform); plat != nil {
		if lang := codegen.LookupLang(target.Lang); lang != nil {
			caps := plat.Capabilities().Merge(lang.Capabilities())
			if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform}); err != nil {
				return err
			}
		}
	}

	return dumpDocument(f, codegen.AnalyzeCommon(pkg))
}

func runDumpLowered(cmd *cobra.Command, args []string) error {
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

	plat := codegen.LookupPlatform(target.Platform)
	lang := codegen.LookupLang(target.Lang)
	if plat == nil {
		return fmt.Errorf("unknown platform %q", target.Platform)
	}
	if lang == nil {
		return fmt.Errorf("unknown language %q", target.Lang)
	}
	caps := plat.Capabilities().Merge(lang.Capabilities())

	if listOnly, _ := cmd.Flags().GetBool("list"); listOnly {
		enabled := lower.EnabledPasses(caps)
		fmt.Printf("caps: %s\n", caps.String())
		if len(enabled) == 0 {
			fmt.Println("passes: (none)")
		} else {
			fmt.Printf("passes: %s\n", strings.Join(enabled, " → "))
		}
		return nil
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

	stopAfter, _ := cmd.Flags().GetString("after")
	start = time.Now()
	if err := lower.Lower(pkg, caps, lower.Options{StopAfter: stopAfter, Platform: target.Platform}); err != nil {
		return err
	}
	slog.Info("lower", "dir", dir, "caps", caps.String(), "stopAfter", stopAfter, "duration", time.Since(start))

	doc = ir.Convert(pkg)
	return dumpDocument(f, doc)
}

func dumpResolveTarget(cmd *cobra.Command, pkg *ir.Package) (outputTarget, error) {
	lang, _ := cmd.Flags().GetString("lang")
	plat, _ := cmd.Flags().GetString("platform")
	lang, plat, err := resolveLangPlat(lang, plat)
	if err != nil {
		return outputTarget{}, err
	}
	targets, err := resolveTargets(pkg, lang, plat, nil)
	if err != nil {
		return outputTarget{}, err
	}
	if len(targets) == 0 {
		return outputTarget{}, fmt.Errorf("no output target specified (use --lang/--platform flags or add an output node)")
	}
	return targets[0], nil
}
