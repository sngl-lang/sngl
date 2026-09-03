package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build [flags] [file|dir...]",
	Short: "Build SNGL project into a runnable artifact",
	Args:  cobra.ArbitraryArgs,
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().String("lang", "", "target language")
	buildCmd.Flags().String("platform", "", "target platform")
	buildCmd.Flags().StringP("out", "o", ".", "directory to place built binaries")
	buildCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runBuild(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	tmpDir, err := os.MkdirTemp("", "sngl-build-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Duplicates a parse+check, but keeps onTarget local; the alternative is a
	// post-resolve callback out of runPipeline, for one consumer.
	multiTarget := isMultiTarget(args, cliLang, cliPlat, optSlice)

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: build.ParseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true, // suppress per-file print; only print artifact path
		onTarget: func(target build.Target, _ *ir.Package, _, srcDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			lang := codegen.LookupLang(target.Lang)
			var builder codegen.Builder
			if b, ok := plat.(codegen.Builder); ok {
				builder = b
			} else if b, ok := lang.(codegen.Builder); ok {
				builder = b
			} else {
				return fmt.Errorf("platform %q with language %q does not support building", target.Platform, target.Lang)
			}
			artifact, err := builder.Build(srcDir, target.Options)
			if err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			finalName := binaryName(target.Options, target.Platform, multiTarget, isWindowsTarget(target))
			finalPath := filepath.Join(outDir, finalName)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			if err := moveFile(artifact, finalPath); err != nil {
				return fmt.Errorf("placing artifact: %w", err)
			}
			fmt.Println(finalPath)
			return nil
		},
	})
}

// "-<platform>" is appended on a multi-target build so the targets don't
// overwrite each other's binary.
func binaryName(opts *ir.StructLit, platform string, multiTarget bool, windows bool) string {
	base := optionString(opts, "name")
	if base == "" {
		base = "app"
	}
	base = sanitizeBinaryBase(base)
	if multiTarget {
		base = base + "-" + platform
	}
	if windows {
		base += ".exe"
	}
	return base
}

func sanitizeBinaryBase(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteRune('-')
			prevDash = true
		}
	}
	out := b.String()
	return strings.TrimRight(out, "-")
}

func optionString(opts *ir.StructLit, name string) string {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return ""
	}
	lit, ok := v.(*ir.Literal)
	if !ok {
		return ""
	}
	// Source-parsed string literals store Raw with surrounding quotes; CLI
	// --opt values store Raw verbatim. Trim a single pair of leading/
	// trailing quotes to normalise both.
	raw := lit.Value
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return raw
}

func isMultiTarget(args []string, cliLang, cliPlat string, optSlice []string) bool {
	if cliLang != "" && cliPlat != "" {
		return false
	}
	units, err := resolveUnits(args)
	if err != nil || len(units) == 0 {
		return false
	}
	doc, err := units[0].doc()
	if err != nil {
		return false
	}
	pkg, err := checkDoc(doc, units[0].dir, true)
	if err != nil {
		return false
	}
	targets, err := build.ResolveTargets(pkg, cliLang, cliPlat, build.ParseCLIOpts(optSlice))
	if err != nil {
		return false
	}
	return len(targets) > 1
}

// No platform sets a goos option yet, so this is false until one does.
func isWindowsTarget(target build.Target) bool {
	return optionString(target.Options, "goos") == "windows"
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
