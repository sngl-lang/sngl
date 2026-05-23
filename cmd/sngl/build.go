package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
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

	// Dry-run target resolve to know whether to append "-<platform>" to
	// binary names. Duplicates a parse+check, but keeps onTarget local;
	// the alternative is a callback from runPipeline post-resolve, which
	// would complicate its API for one consumer.
	multiTarget := isMultiTarget(args, cliLang, cliPlat, optSlice)

	return runPipeline(cmd, args, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: parseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true, // suppress per-file print; only print artifact path
		onTarget: func(target outputTarget, _ *ir.Package, _, srcDir string) error {
			plat := codegen.LookupPlatform(target.Platform)
			builder, ok := plat.(codegen.Builder)
			if !ok {
				return fmt.Errorf("platform %q does not support building", target.Platform)
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

// binaryName derives the on-disk filename for a built artifact. Default base
// is Options.name, lowercased and with non-alphanumeric runes replaced by '-'.
// When the build emits more than one target, "-<platform>" is appended so
// concurrent targets don't overwrite each other's binary. The ".exe"
// extension is added for windows.
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

// sanitizeBinaryBase lowercases s and replaces any non-alphanumeric rune
// with '-'. Consecutive '-' are collapsed and leading/trailing '-' trimmed.
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

// optionString reads a string-valued option from opts, returning "" if absent
// or non-string. Mirrors optionBool in pipeline.go.
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
	raw := lit.Raw
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return raw
}

// isMultiTarget returns true when the resolved set of build targets for
// `args` contains more than one target. Implemented by parsing the first
// file's outputs in isolation; if cliLang+cliPlat are both set, there is
// always exactly one target.
func isMultiTarget(args []string, cliLang, cliPlat string, optSlice []string) bool {
	if cliLang != "" && cliPlat != "" {
		return false
	}
	files, err := discoverFiles(args)
	if err != nil || len(files) == 0 {
		return false
	}
	f, err := os.Open(files[0])
	if err != nil {
		return false
	}
	doc, err := parseSNGL(files[0], f)
	f.Close()
	if err != nil {
		return false
	}
	dir := filepath.Dir(files[0])
	absFilename, _ := filepath.Abs(files[0])
	if !explicitFileSet(args)[absFilename] {
		doc = mergeDir(doc, files[0])
	}
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return false
	}
	targets, err := resolveTargets(pkg, cliLang, cliPlat, parseCLIOpts(optSlice))
	if err != nil {
		return false
	}
	return len(targets) > 1
}

// isWindowsTarget reports whether the target's options indicate a windows
// build. Today no platform sets an explicit GOOS option; this is a
// forward-compat hook that returns false unless someone wires up goos.
func isWindowsTarget(target outputTarget) bool {
	return optionString(target.Options, "goos") == "windows"
}

// moveFile relocates src to dst. Falls back to copy+remove if Rename fails
// (cross-device).
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
