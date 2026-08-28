package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// main=true sets the "main" option, so platforms emit a runnable entry point.
type pipelineOpts struct {
	cliLang string
	cliPlat string
	cliOpts map[string]string
	outDir  string
	main    bool
	quiet   bool
	// onTarget runs after generation, per target. Nil = generate-only.
	onTarget func(target outputTarget, pkg *ir.Package, dir, outDir string) error
}

// One of `--lang`/`--platform` alone is still a target: a platform selected
// without a language loads that platform's package, which carries its
// overrides.
func cliSelectedTargets(lang, plat string) []ir.StaticTarget {
	if lang == "" && plat == "" {
		return nil
	}
	return []ir.StaticTarget{{Platform: plat, Language: lang}}
}

func runPipeline(cmd *cobra.Command, args []string, p pipelineOpts) error {
	cliLang, cliPlat, err := resolveLangPlat(p.cliLang, p.cliPlat)
	if err != nil {
		return err
	}

	for _, name := range codegen.Platforms() {
		pl := codegen.LookupPlatform(name)
		if cfg, ok := pl.(codegen.OptionConfigurable); ok {
			if err := cfg.Configure(p.cliOpts); err != nil {
				return fmt.Errorf("configure platform %s: %w", name, err)
			}
		}
	}

	libs, paths, err := resolveInputs(args)
	if err != nil {
		return err
	}
	for _, in := range libs {
		if err := in.Err(); err != nil {
			return fmt.Errorf("%s: %w", in.Path, err)
		}
		// The package has no project directory of its own — its source is
		// embedded, or the target synthesized it — so "." means generating into
		// the working directory.
		if err := emitPackage(in.Pkg, in.Path, ".", cliLang, cliPlat, p); err != nil {
			return err
		}
	}

	// A package named and no paths left is the whole input: only fall through
	// to the current directory when no argument was given at all.
	if len(paths) == 0 && len(args) > 0 {
		return nil
	}

	explicitFiles := explicitFileSet(paths)

	files, err := discoverFiles(paths)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

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
		pkg, err := checkDoc(doc, dir, true, cliSelectedTargets(cliLang, cliPlat)...)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("check", "dir", dir, "duration", time.Since(start))

		if err := emitPackage(pkg, filename, dir, cliLang, cliPlat, p); err != nil {
			return err
		}
	}
	return nil
}

// Split out because a package addressed by `sngl://<uri>` arrives already
// checked, the checker having built it under the lib-source rules its own
// imports need: the two entry points share everything after the check and
// nothing before it.
//
// name identifies the input in diagnostics — a file path, or the URI as
// written. dir is what imports and generated paths resolve against.
func emitPackage(pkg *ir.Package, name, dir, cliLang, cliPlat string, p pipelineOpts) error {
	if err := validateOutputs(pkg); err != nil {
		return err
	}

	targets, err := resolveTargets(pkg, cliLang, cliPlat, p.cliOpts)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if len(targets) == 0 {
		return fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", dir)
	}

	// One cache for every target of this compilation: they fold the same
	// source, so a value evaluated for one is the value for all. It is not
	// shared any wider — a folded value has no record of the Go body that
	// produced it, so it must not survive the compilation.
	evalCache := optimize.NewEvalCache()

	for _, target := range targets {
		// Optimize and lower work in place, so every target past the first has
		// to start from the pristine checked IR rather than what the previous
		// one left behind.
		tpkg := pkg
		if len(targets) > 1 {
			tpkg = ir.ClonePackage(pkg)
		}

		if target.Options == nil {
			target.Options = &ir.StructLit{}
		}
		if p.main {
			codegen.SetOptionField(target.Options, "main", true)
		}
		if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
			codegen.SetOptionField(target.Options, "projectDir", dir)
		}

		optCfg := &optimize.Config{
			Platform:    target.Platform,
			Language:    target.Lang,
			Dir:         dir,
			NoCacheBust: optionBool(target.Options, "noCacheBust"),
			Cache:       evalCache,
		}
		start := time.Now()
		if err := optimize.Optimize(tpkg, optCfg); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

		plat := codegen.LookupPlatform(target.Platform)
		lang := codegen.LookupLang(target.Lang)
		if plat == nil {
			return fmt.Errorf("%s: unknown platform %q (available: %v)", name, target.Platform, codegen.Platforms())
		}
		if lang == nil {
			return fmt.Errorf("%s: unknown language %q (available: %v)", name, target.Lang, codegen.Langs())
		}
		caps := plat.Capabilities(lang).ToLowerCaps()
		start = time.Now()
		if err := lower.Lower(tpkg, caps, lower.Options{Platform: target.Platform, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

		if caps != (lower.Caps{}) {
			start = time.Now()
			if err := optimize.Optimize(tpkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize2", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		}

		var fileAssets []codegen.FileAsset
		for _, fa := range optCfg.FileAssets {
			fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
		}

		start = time.Now()
		if err := generateTarget(name, tpkg, target, p.outDir, fileAssets, p.quiet); err != nil {
			return err
		}
		slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

		if p.onTarget != nil {
			if err := p.onTarget(target, tpkg, dir, p.outDir); err != nil {
				return err
			}
		}
	}
	return nil
}

type outputTarget struct {
	Lang     string
	Platform string
	Options  *ir.StructLit
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

func resolveTargets(pkg *ir.Package, cliLang, cliPlat string, cliOpts map[string]string) ([]outputTarget, error) {
	if cliLang != "" && cliPlat != "" {
		// Seed from a matching output declaration so flags select the target
		// without discarding its declared options (e.g. stylesheet="..."). CLI
		// --opt values still override below. Falls back to empty options when
		// the requested target isn't declared in any output block.
		var base *ir.StructLit
		for _, o := range pkg.Outputs {
			if o.Lang == cliLang && o.Platform == cliPlat {
				base = o.Options
				break
			}
		}
		t := outputTarget{Lang: cliLang, Platform: cliPlat, Options: cloneStructLit(base)}
		if err := applyCLIOpts(t.Options, cliOpts); err != nil {
			return nil, err
		}
		return []outputTarget{t}, nil
	}
	var targets []outputTarget
	for _, o := range pkg.Outputs {
		opts := cloneStructLit(o.Options)
		// Errors from per-target validation are deferred — an opt may be
		// valid for one target's schema and unknown to another. Below we
		// only error if no target accepts the key.
		_ = applyCLIOpts(opts, cliOpts)
		targets = append(targets, outputTarget{Lang: o.Lang, Platform: o.Platform, Options: opts})
	}
	if err := validateCLIOptsAcrossTargets(cliOpts, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// A key valid for at least one target passes: per-target ApplyOptions drops it
// on the others on purpose, since an opt name often varies by platform.
func validateCLIOptsAcrossTargets(kv map[string]string, targets []outputTarget) error {
	if len(kv) == 0 {
		return nil
	}
	var anySchema bool
	for _, t := range targets {
		if t.Options != nil && t.Options.Def != nil {
			anySchema = true
			break
		}
	}
	if !anySchema {
		return nil
	}
	for k := range kv {
		matched := false
		for _, t := range targets {
			if t.Options != nil && optionFieldType(t.Options, k) != nil {
				matched = true
				break
			}
		}
		if !matched {
			var available []string
			seen := make(map[string]bool)
			for _, t := range targets {
				if t.Options == nil || t.Options.Def == nil {
					continue
				}
				for _, f := range t.Options.Def.Fields {
					if !seen[f.Name] {
						seen[f.Name] = true
						available = append(available, f.Name)
					}
				}
			}
			return fmt.Errorf("unknown --opt %q (available: %v)", k, available)
		}
	}
	return nil
}

// The Fields slice is copied so a per-target option mutation (CLI overlay,
// projectDir injection) cannot reach the IR shared across targets.
func cloneStructLit(src *ir.StructLit) *ir.StructLit {
	if src == nil {
		return &ir.StructLit{}
	}
	out := &ir.StructLit{Type: src.Type, Def: src.Def}
	out.Fields = append(out.Fields, src.Fields...)
	return out
}

// A non-string field's value is parsed as a SNGL const expression and checked
// against the declared type. An unknown type (no Def, or the field absent from
// it) is wrapped as a raw string literal, so simple cases work with no checker
// pass.
func applyCLIOpts(opts *ir.StructLit, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	for k, v := range kv {
		ft := optionFieldType(opts, k)
		// A key this target does not declare is left to the cross-target
		// validation in resolveTargets, so one --opt can be scoped to
		// whichever output declares it.
		if opts != nil && opts.Def != nil && ft == nil {
			continue
		}
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
	return nil
}

// Mirrors codegen.ApplyOptions, because the optimizer needs the flag before
// any platform Config struct is populated.
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

// Raw is set so codegen.ApplyOptions reads the same shape as a source-declared
// option.
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
	}
	if ir.IsColorStruct(t) {
		// A colour arrives as a raw hex string, which is what ApplyOptions
		// reads out of .Raw anyway.
		return &ir.Literal{Type: ir.TypString, Raw: raw}, nil
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

	req := &codegen.Request{
		Pkg:        pkg,
		Lang:       lang,
		Options:    target.Options,
		Source:     filepath.Base(filename),
		FileAssets: fileAssets,
		ProjectFS:  os.DirFS(filepath.Dir(filename)),
		Maps:       optionBool(target.Options, "maps"),
	}
	mem := codegen.NewMemSink()
	if err := plat.Generate(req, mem); err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}

	// Sorted so the write order, and the printed paths, are deterministic.
	files := mem.Files()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(outDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
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
