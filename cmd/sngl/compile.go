package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

type outputTarget struct {
	Lang     string
	Platform string
	Options  *ir.StructLit
}

var compileCmd = &cobra.Command{
	Use:   "compile [flags] [file|dir...]",
	Short: "Compile SNGL files to target platform",
	Args:  cobra.ArbitraryArgs,
	RunE:  runCompile,
}

func init() {
	compileCmd.Flags().String("lang", "", "target language")
	compileCmd.Flags().String("platform", "", "target platform")
	compileCmd.Flags().String("out", ".", "output directory")
	compileCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

func runCompile(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	outDir, _ := cmd.Flags().GetString("out")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")

	cliLang, cliPlat, err := resolveLangPlat(cliLang, cliPlat)
	if err != nil {
		return err
	}

	cliOpts := parseCLIOpts(optSlice)

	explicitFiles := explicitFileSet(args)

	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	// Group files by directory so sibling .sngl files are merged into one
	// compilation unit (package-level semantics).
	seen := make(map[string]bool)
	for _, filename := range files {
		dir := filepath.Dir(filename)
		if seen[dir] {
			continue
		}
		seen[dir] = true

		f, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		start := time.Now()
		doc, err := parseSNGL(filename, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		slog.Info("parse", "file", filename, "duration", time.Since(start))

		absFilename, _ := filepath.Abs(filename)
		if !explicitFiles[absFilename] {
			start = time.Now()
			doc = mergeDir(doc, filename)
			slog.Info("merge", "dir", dir, "duration", time.Since(start))
		}

		start = time.Now()
		pkg, err := checkDoc(doc, dir, true)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("check", "dir", dir, "duration", time.Since(start))

		if err := validateOutputs(pkg); err != nil {
			return err
		}

		targets := resolveTargets(pkg, cliLang, cliPlat, cliOpts)
		if len(targets) == 0 {
			return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", dir)
		}

		for _, target := range targets {
			if target.Options == nil {
				target.Options = &ir.StructLit{}
			}
			// Set projectDir for resolving relative paths in output options
			// (icon paths from output declarations are relative to the .sngl dir)
			if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
				codegen.SetOptionField(target.Options, "projectDir", dir)
			}

			start = time.Now()
			optCfg := &optimize.Config{
				Platform:    target.Platform,
				Language:    target.Lang,
				Dir:         dir,
				NoCacheBust: optionBool(target.Options, "noCacheBust"),
			}
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			// Lowering: rewrite high-level constructs into primitives the
			// platform/language can consume. Capabilities come from both
			// sides via field-wise OR.
			plat := codegen.LookupPlatform(target.Platform)
			lang := codegen.LookupLang(target.Lang)
			if plat == nil {
				return fmt.Errorf("%s: unknown platform %q (available: %v)", filename, target.Platform, codegen.Platforms())
			}
			if lang == nil {
				return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
			}
			caps := plat.Capabilities().Merge(lang.Capabilities())
			start = time.Now()
			if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

			// Second optimize pass cleans up artifacts of lowering
			// (folded toggles, dead branches, etc.). Skipped when no
			// lowering ran — re-running the optimizer on already-folded IR
			// re-derives file assets and overwrites optCfg.FileAssets.
			if caps != (lower.Caps{}) {
				start = time.Now()
				if err := optimize.Optimize(pkg, optCfg); err != nil {
					return fmt.Errorf("%s: %w", dir, err)
				}
				slog.Info("optimize2", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
			}

			// Convert optimizer file assets to codegen file assets.
			var fileAssets []codegen.FileAsset
			for _, fa := range optCfg.FileAssets {
				fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
			}

			start = time.Now()
			if err := generateTarget(filename, pkg, target, outDir, fileAssets, quiet(cmd)); err != nil {
				return err
			}
			slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		}
	}
	return nil
}

// parseCLIOpts converts a --opt key=value slice into a map. Empty entries and
// the cobra-default sentinel "[]" (left over from flag reset between
// in-process test invocations) are filtered.
func parseCLIOpts(optSlice []string) map[string]string {
	out := make(map[string]string)
	for _, kv := range optSlice {
		if kv == "" || kv == "[]" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func resolveTargets(pkg *ir.Package, cliLang, cliPlat string, cliOpts map[string]string) []outputTarget {
	if cliLang != "" && cliPlat != "" {
		t := outputTarget{Lang: cliLang, Platform: cliPlat, Options: &ir.StructLit{}}
		applyCLIOpts(t.Options, cliOpts)
		return []outputTarget{t}
	}
	var targets []outputTarget
	for _, o := range pkg.Outputs {
		opts := cloneStructLit(o.Options)
		applyCLIOpts(opts, cliOpts)
		targets = append(targets, outputTarget{Lang: o.Lang, Platform: o.Platform, Options: opts})
	}
	return targets
}

// cloneStructLit returns a shallow copy of the StructLit fields slice so that
// per-target option mutations (CLI overlay, projectDir injection) don't
// mutate the IR shared across targets.
func cloneStructLit(src *ir.StructLit) *ir.StructLit {
	if src == nil {
		return &ir.StructLit{}
	}
	out := &ir.StructLit{Type: src.Type, Def: src.Def}
	out.Fields = append(out.Fields, src.Fields...)
	return out
}

// applyCLIOpts overlays --opt key=value pairs onto opts. For string-typed
// fields the value is taken verbatim. For other fields the value is parsed as
// a SNGL const expression and type-checked against the field's declared type.
//
// When the field's type is unknown (no Def, or field absent from Def),
// the value is wrapped as a raw string literal — keeps simple cases working
// without a checker pass.
func applyCLIOpts(opts *ir.StructLit, kv map[string]string) {
	if len(kv) == 0 {
		return
	}
	for k, v := range kv {
		ft := optionFieldType(opts, k)
		if ft != nil && ft.Kind != ir.TypeString {
			parsed, err := parseConstOption(v, ft)
			if err == nil {
				codegen.SetOptionField(opts, k, parsed)
				continue
			}
			// Fall back to string-as-raw on parse failure; the platform
			// will surface a clearer error from ApplyOptions.
		}
		codegen.SetOptionField(opts, k, v)
	}
}

// optionBool reads a bool-valued option from opts, returning false when absent
// or non-bool. Mirrors the semantics codegen.ApplyOptions would apply, but the
// optimizer needs the flag *before* any platform Config struct is populated.
func optionBool(opts *ir.StructLit, name string) bool {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return false
	}
	lit, ok := v.(*ir.Literal)
	if !ok {
		return false
	}
	return lit.Raw == "true"
}

// optionFieldType returns the declared type of a field on opts, or nil if the
// StructLit has no Def or the field isn't declared.
func optionFieldType(opts *ir.StructLit, name string) *ir.Type {
	if opts == nil || opts.Def == nil {
		return nil
	}
	for _, f := range opts.Def.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

// parseConstOption parses a CLI --opt value as a SNGL constant expression of
// the given type. Returns an *ir.Literal with Raw set so codegen.ApplyOptions
// reads the same shape as a source-declared option.
func parseConstOption(raw string, t *ir.Type) (*ir.Literal, error) {
	switch t.Kind {
	case ir.TypeBool:
		switch raw {
		case "true", "false":
			return &ir.Literal{Type: ir.TypBool, Raw: raw}, nil
		}
		return nil, fmt.Errorf("expected bool, got %q", raw)
	case ir.TypeInt:
		return &ir.Literal{Type: ir.TypInt, Raw: raw}, nil
	case ir.TypeFloat:
		return &ir.Literal{Type: ir.TypFloat, Raw: raw}, nil
	case ir.TypeColor:
		return &ir.Literal{Type: ir.TypColor, Raw: raw}, nil
	}
	return &ir.Literal{Type: t, Raw: raw}, nil
}

func generateTarget(filename string, pkg *ir.Package, target outputTarget, outDir string, fileAssets []codegen.FileAsset, q bool) error {
	lang := codegen.LookupLang(target.Lang)
	if lang == nil {
		return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
	}

	plat := codegen.LookupPlatform(target.Platform)
	if plat == nil {
		return fmt.Errorf("%s: unknown platform %q (available: %v)", filename, target.Platform, codegen.Platforms())
	}

	supported := slices.Contains(plat.SupportedLangs(), target.Lang)
	if !supported {
		return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", filename, target.Platform, target.Lang, plat.SupportedLangs())
	}

	resp, err := plat.Generate(&codegen.Request{
		Pkg:        pkg,
		Lang:       lang,
		Options:    target.Options,
		Source:     filepath.Base(filename),
		FileAssets: fileAssets,
		ProjectFS:  os.DirFS(filepath.Dir(filename)),
	})
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("%s: %s", filename, resp.Error)
	}

	for _, file := range resp.Files {
		path := filepath.Join(outDir, file.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteTo(f)
		f.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return writeErr
		}
		slog.Info("wrote", "path", path)
		if !q {
			fmt.Println(path)
		}
	}
	return nil
}

func quiet(cmd *cobra.Command) bool {
	q, _ := cmd.Flags().GetBool("quiet")
	return q
}
