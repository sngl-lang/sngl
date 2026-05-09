package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] <file.sngl> [-- args...]",
	Short: "Compile and run an SNGL app",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().String("lang", "", "target language")
	runCmd.Flags().String("platform", "", "target platform")
	runCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runRun(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	cliLang, cliPlat, err := resolveLangPlat(cliLang, cliPlat)
	if err != nil {
		return err
	}

	cliOpts := parseCLIOpts(optSlice)

	// Split args at "--" to separate sngl args from program args
	file := args[0]
	var progArgs []string
	for i, a := range args {
		if a == "--" {
			progArgs = args[i+1:]
			break
		}
	}

	// Parse and merge directory
	info, err := os.Stat(file)
	if err != nil {
		return err
	}

	var doc *ast.Document
	var dir string
	start := time.Now()
	if info.IsDir() {
		dir = file
		doc, err = parseDir(file)
		if err != nil {
			return err
		}
	} else {
		dir = filepath.Dir(file)
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		doc, err = parseSNGL(file, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	slog.Info("parse", "file", file, "duration", time.Since(start))

	start = time.Now()
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	if err := validateOutputs(pkg); err != nil {
		return err
	}

	// Resolve target
	targets, err := resolveTargets(pkg, cliLang, cliPlat, cliOpts)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("no output target specified (use --lang/--platform flags or add an output node)")
	}
	target := targets[0]

	// Look up platform and check it implements Runner
	plat := codegen.LookupPlatform(target.Platform)
	if plat == nil {
		return fmt.Errorf("unknown platform %q (available: %v)", target.Platform, codegen.Platforms())
	}

	runner, ok := plat.(codegen.Runner)
	if !ok {
		return fmt.Errorf("platform %q does not support direct execution", target.Platform)
	}

	lang := codegen.LookupLang(target.Lang)
	if lang == nil {
		return fmt.Errorf("unknown language %q (available: %v)", target.Lang, codegen.Langs())
	}

	// Force main-package options
	if target.Options == nil {
		target.Options = &ir.StructLit{}
	}
	codegen.SetOptionField(target.Options, "main", true)
	codegen.SetOptionField(target.Options, "projectDir", dir)
	codegen.SetOptionField(target.Options, "lang", target.Lang)

	// Optimize
	start = time.Now()
	optCfg := &optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	// Lower
	caps := plat.Capabilities().Merge(lang.Capabilities())
	start = time.Now()
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		return err
	}
	slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

	if caps != (lower.Caps{}) {
		start = time.Now()
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return err
		}
		slog.Info("optimize2", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "sngl-run-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate
	var fileAssets []codegen.FileAsset
	for _, fa := range optCfg.FileAssets {
		fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}
	if err := generateTarget(file, pkg, target, tmpDir, fileAssets, true); err != nil {
		return err
	}

	// Run
	return runner.Run(tmpDir, target.Options, progArgs)
}
