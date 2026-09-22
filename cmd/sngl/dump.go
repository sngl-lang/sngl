package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/highlight"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var dumpCmd = &cobra.Command{
	Use:   "dump [file|dir]",
	Short: "Dump compiler phase output",
	Long: `Dump compiler phase output in various formats.

Use --stage to control how far the compiler runs before dumping.
Output format is unstable and may change between versions.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDump,
}

func init() {
	dumpCmd.Flags().String("stage", "codegen", "pipeline stage (parsed, checked, optimized, analysis, lowered, codegen)")
	dumpCmd.Flags().String("format", "sngl", "output format for IR stages (spew, json, sngl)")
	dumpCmd.Flags().String("color", "auto", "colorize output (auto, on, off)")
	dumpCmd.Flags().String("input", "auto", "input source (auto, sngl, stdin, txtar, markdown)")
	dumpCmd.Flags().Bool("pointers", false, "show pointer addresses (useful for identifying shared objects)")
	dumpCmd.Flags().Int("depth", 0, "maximum depth for spew output (default unlimited)")
	dumpCmd.Flags().StringSlice("omit", nil, "omit struct fields by name (comma-separated, e.g. --omit AST,Pos)")
	dumpCmd.Flags().String("lang", "", "target language")
	dumpCmd.Flags().String("platform", "", "target platform")
	dumpCmd.Flags().StringSlice("opt", nil, "generator options (key=value, for codegen stage)")
	dumpCmd.Flags().String("after", "", "dump IR after named pass (lowered stage only; 'none' = pre-lower state)")
	dumpCmd.Flags().Bool("list", false, "print resolved caps and pass list, then exit (lowered stage only)")
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

// A library package has no directory — its source is embedded, or synthesized
// by the target serving it — so its imports resolve through the checker.
func dumpParsed(args []string, inp dumpInput) (*ast.Document, string, error) {
	if inp == dumpInputSNGL && len(args) > 0 {
		in, err := resolveInput(args[0])
		if err != nil {
			return nil, "", err
		}
		if in != nil {
			return in.Document(), ".", nil
		}
	}
	return dumpParseInput(inp, args)
}

// A library package is handed back as the checker already built it: it loads
// under the lib-source rules that permit its own sngl:internal/ imports,
// which a fresh check of the same source would reject.
func dumpChecked(cmd *cobra.Command, args []string, inp dumpInput) (*ir.Package, string, error) {
	if inp == dumpInputSNGL && len(args) > 0 {
		in, err := resolveInput(args[0])
		if err != nil {
			return nil, "", err
		}
		if in != nil {
			if err := in.Err(); err != nil {
				return nil, "", fmt.Errorf("%s: %w", in.Path, err)
			}
			return in.Pkg, ".", nil
		}
	}
	doc, dir, err := dumpParseInput(inp, args)
	if err != nil {
		return nil, "", err
	}
	targets, err := dumpCLITargets(cmd)
	if err != nil {
		return nil, "", err
	}
	start := time.Now()
	pkg, err := checkDoc(doc, dir, true, targets...)
	if err != nil {
		return nil, "", err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))
	return pkg, dir, nil
}

func runDump(cmd *cobra.Command, args []string) error {
	stage, _ := cmd.Flags().GetString("stage")

	// codegen stage has its own output format; skip IR format resolution.
	if stage == "codegen" {
		if err := resolveColor(cmd); err != nil {
			return err
		}
		inp, err := resolveDumpInput(cmd, args)
		if err != nil {
			return err
		}
		return runDumpCodegen(cmd, args, inp)
	}

	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}
	f = dumpDefaultFormat(cmd, stage, f)

	switch stage {
	case "parsed":
		doc, _, err := dumpParsed(args, inp)
		if err != nil {
			return err
		}
		return dumpDocument(f, doc)

	case "checked":
		pkg, _, err := dumpChecked(cmd, args, inp)
		if err != nil {
			return err
		}
		return dumpDocument(f, pkg)

	case "optimized":
		pkg, dir, err := dumpChecked(cmd, args, inp)
		if err != nil {
			return err
		}
		target, err := dumpResolveTarget(cmd, pkg)
		if err != nil {
			return err
		}
		start := time.Now()
		if err := optimize.Optimize(pkg, &optimize.Config{
			Platform: target.Platform,
			Language: target.Lang,
			Dir:      dir,
		}); err != nil {
			return err
		}
		slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		if plat := codegen.LookupPlatform(target.Platform); plat != nil {
			if lang := codegen.LookupLang(target.Lang); lang != nil {
				caps := codegen.CapsOrNone(lang.LanguageIdentifier(), plat.PlatformIdentifier()).ToLowerCaps()
				if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform, Language: target.Lang, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
					return err
				}
			}
		}
		return dumpDocument(f, ir.Convert(pkg))

	case "analysis":
		pkg, dir, err := dumpChecked(cmd, args, inp)
		if err != nil {
			return err
		}
		target, err := dumpResolveTarget(cmd, pkg)
		if err != nil {
			return err
		}
		start := time.Now()
		if err := optimize.Optimize(pkg, &optimize.Config{
			Platform: target.Platform,
			Language: target.Lang,
			Dir:      dir,
		}); err != nil {
			return err
		}
		slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		if plat := codegen.LookupPlatform(target.Platform); plat != nil {
			if lang := codegen.LookupLang(target.Lang); lang != nil {
				caps := codegen.CapsOrNone(lang.LanguageIdentifier(), plat.PlatformIdentifier()).ToLowerCaps()
				if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform, Language: target.Lang, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
					return err
				}
			}
		}
		return dumpDocument(f, codegen.AnalyzeCommon(pkg))

	case "lowered":
		return runDumpLowered(cmd, args, f, inp)

	default:
		return fmt.Errorf("unknown stage %q (valid: parsed, checked, optimized, analysis, lowered, codegen)", stage)
	}
}

func runDumpLowered(cmd *cobra.Command, args []string, f dumpFormat, inp dumpInput) error {
	pkg, dir, err := dumpChecked(cmd, args, inp)
	if err != nil {
		return err
	}

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
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), plat.PlatformIdentifier()).ToLowerCaps()

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

	start := time.Now()
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
	if err := lower.Lower(pkg, caps, lower.Options{StopAfter: stopAfter, Platform: target.Platform, Language: target.Lang, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
		return err
	}
	slog.Info("lower", "dir", dir, "caps", caps.String(), "stopAfter", stopAfter, "duration", time.Since(start))

	return dumpDocument(f, ir.Convert(pkg))
}

func runDumpCodegen(cmd *cobra.Command, args []string, inp dumpInput) error {
	pkg, dir, err := dumpChecked(cmd, args, inp)
	if err != nil {
		return err
	}

	target, err := dumpResolveTarget(cmd, pkg)
	if err != nil {
		return err
	}

	plat := codegen.LookupPlatform(target.Platform)
	lang := codegen.LookupLang(target.Lang)
	if plat == nil {
		return fmt.Errorf("unknown platform %q (available: %v)", target.Platform, codegen.Platforms())
	}
	if lang == nil {
		return fmt.Errorf("unknown language %q (available: %v)", target.Lang, codegen.Langs())
	}

	optSlice, _ := cmd.Flags().GetStringSlice("opt")
	cliOpts := build.ParseCLIOpts(optSlice)
	if err := build.ApplyCLIOpts(target.Options, cliOpts); err != nil {
		return err
	}
	if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
		codegen.SetOptionField(target.Options, "projectDir", dir)
	}

	optCfg := &optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}
	start := time.Now()
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), plat.PlatformIdentifier()).ToLowerCaps()
	start = time.Now()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: target.Platform, Language: target.Lang, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
		return err
	}
	slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

	// Unconditional. It used to run only when the target had capabilities to lower
	// for, on the reading that a build lowering nothing had nothing new to fold --
	// which stopped being true when passQuery became always-on: it synthesizes a
	// thunk after the optimizer has walked every declaration, so the calls inside
	// one are calls nothing has looked at.
	start = time.Now()
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return err
	}
	slog.Info("optimize2", "dir", dir, "duration", time.Since(start))

	var fileAssets []codegen.FileAsset
	for _, fa := range optCfg.FileAssets {
		fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}

	req := &codegen.Request{
		Pkg:        pkg,
		Lang:       lang,
		Options:    target.Options,
		Source:     filepath.Base(dir),
		FileAssets: fileAssets,
		ProjectFS:  os.DirFS(dir),
	}
	mem := codegen.NewMemSink()
	start = time.Now()
	if err := plat.Generate(req, mem); err != nil {
		return err
	}
	slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	files := mem.Files()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	return dumpCodegenOutput(names, files)
}

// A stage dump must check against the same target it then dumps for, or it
// reports a tree the build would never make.
func dumpCLITargets(cmd *cobra.Command) ([]ir.StaticTarget, error) {
	lang, _ := cmd.Flags().GetString("lang")
	plat, _ := cmd.Flags().GetString("platform")
	lang, plat, err := build.ResolveLangPlat(lang, plat)
	if err != nil {
		// Returning no targets instead checks against every registered one and
		// dumps a tree for a target the caller never named.
		return nil, err
	}
	return build.SelectedTargets(lang, plat), nil
}

func dumpResolveTarget(cmd *cobra.Command, pkg *ir.Package) (build.Target, error) {
	lang, _ := cmd.Flags().GetString("lang")
	plat, _ := cmd.Flags().GetString("platform")
	lang, plat, err := build.ResolveLangPlat(lang, plat)
	if err != nil {
		return build.Target{}, err
	}
	targets, err := build.ResolveTargets(pkg, lang, plat, nil)
	if err != nil {
		return build.Target{}, err
	}
	if len(targets) == 0 {
		return build.Target{}, fmt.Errorf("no output target specified (use --lang/--platform flags or add an output node)")
	}
	return targets[0], nil
}

func dumpCodegenOutput(names []string, files map[string][]byte) error {
	if len(names) == 1 {
		name := names[0]
		content := string(files[name])
		if dumpColor {
			if lex := highlight.LexerForFile(name); lex != "" {
				content = highlight.Terminal(content, lex, "dracula")
			}
		}
		fmt.Print(content)
		return nil
	}

	const headerReset = "\033[0m"
	const headerColor = "\033[1;36m" // bold cyan

	var sb strings.Builder
	for i, name := range names {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if dumpColor {
			sb.WriteString(headerColor)
		}
		sb.WriteString("-- ")
		sb.WriteString(name)
		sb.WriteString(" --")
		if dumpColor {
			sb.WriteString(headerReset)
		}
		sb.WriteByte('\n')

		content := string(files[name])
		if dumpColor {
			if lex := highlight.LexerForFile(name); lex != "" {
				content = highlight.Terminal(content, lex, "dracula")
			}
		}
		sb.WriteString(content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	fmt.Print(sb.String())
	return nil
}
