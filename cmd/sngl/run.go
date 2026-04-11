package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
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

	if (cliLang == "") != (cliPlat == "") {
		return fmt.Errorf("--lang and --platform must both be specified or both omitted")
	}

	cliOpts := make(map[string]string)
	for _, kv := range optSlice {
		k, v, _ := strings.Cut(kv, "=")
		cliOpts[k] = v
	}

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
		doc = mergeDir(doc, file)
	}
	slog.Info("parse", "file", file, "duration", time.Since(start))

	start = time.Now()
	if err := checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), defaultSchemeResolver(), defaultFSSchemeResolver(), sngl.BuildAPIConfig(doc), true); err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	if err := validateOutputs(doc); err != nil {
		return err
	}

	// Resolve target
	targets := resolveTargets(doc, cliLang, cliPlat, cliOpts)
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

	// Force main-package options
	if target.Options == nil {
		target.Options = make(map[string]string)
	}
	target.Options["main"] = "true"
	target.Options["projectDir"] = dir

	// Optimize
	start = time.Now()
	if err := optimize.Optimize(doc, optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "sngl-run-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate
	if err := generateTarget(file, doc, target, tmpDir, true); err != nil {
		return err
	}

	// Run
	return runner.Run(tmpDir, target.Options, progArgs)
}
