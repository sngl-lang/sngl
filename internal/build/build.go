// Package build is the compile path behind the CLI: given a checked package it
// resolves the output targets, runs optimize → lower → codegen for each, and
// returns the generated files in memory.
//
// It is a package rather than part of cmd/sngl so that the golden test harness
// compiles a fixture through the same code `sngl generate` runs. Two build
// paths that could disagree is the whole reason it was extracted: the CLI's
// txtar tests assert on generated code, and a harness with its own build loop
// would let the two drift while both stayed green.
//
// Writing files, printing paths and running anything afterwards stay with the
// caller: Emit only computes.
package build

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Options is everything a build needs that is not the package itself.
type Options struct {
	// Name identifies the input in diagnostics — a file path, or a package URI
	// as written. It also names the generated source header.
	Name string
	// Dir is what imports and the projectDir option resolve against.
	Dir string
	// Lang and Platform are a caller-selected target, from --lang/--platform.
	// Empty leaves the package's own output blocks to name the targets.
	Lang, Platform string
	// Opts are --opt key=value overlays, applied on top of each target's
	// declared options.
	Opts map[string]string
	// Main sets the "main" option, so platforms emit a runnable entry point.
	Main bool
	// OutDir reaches a platform that resolves paths relative to it.
	OutDir string
	// ProjectFS is what a platform reads project files through. Nil serves
	// them off disk relative to Name, which is what the CLI wants; the golden
	// harness passes the fixture's own FS.
	ProjectFS fs.FS
	// Library says the input is a library package the caller named, so the
	// window rule below does not apply to it: `sngl generate sngl:platform/gtk4`
	// is a request for that package's declarations rather than a program to
	// run, and a library has no window by construction.
	Library bool
}

// Result is one target's build.
type Result struct {
	Target Target
	// Pkg is the optimized, lowered IR the files were generated from. An
	// interpreted target has no files and this is what runs.
	Pkg *ir.Package
	// Files are the generated files, keyed by the path relative to the output
	// directory. Empty for an interpreted target.
	Files map[string][]byte
	// Boilerplate names the subset of Files the program had no part in --
	// a project scaffold the platform writes around it. Nothing in a build
	// treats them differently; the golden harness commits the rest and lets
	// a digest stand for these, so a hundred fixtures do not each carry a
	// copy of the same Gradle project.
	Boilerplate map[string]bool
}

// Emit builds pkg for every resolved target.
//
// optimize and lower work in place, so each target past the first starts from
// a clone of the pristine checked IR -- which means a single-target build
// works on pkg itself and leaves it optimized and lowered. A caller that
// needs the checked IR afterwards has to clone before calling.
func Emit(pkg *ir.Package, o Options) ([]Result, error) {
	// A build's rule and not the language's, which is why it is asked here
	// rather than in the checker: `component c { … }` on its own is a
	// perfectly good thing to type-check, and it is only as something to
	// *run* that it has nowhere to draw. The package body is a slot for the
	// root tree, and a window is that tree's one renderable member.
	if !o.Library && !pkg.IsProgram() {
		return nil, fmt.Errorf("%s: a program declares at least one window: the package body renders only what a window holds", o.Dir)
	}
	if err := ValidateOutputs(pkg); err != nil {
		return nil, err
	}

	targets, err := ResolveTargets(pkg, o.Lang, o.Platform, o.Opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", o.Dir, err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s: no output target specified (use --lang/--platform flags or add an output node)", o.Dir)
	}

	// One cache for every target of this compilation: they fold the same
	// source, so a value evaluated for one is the value for all. It is not
	// shared any wider — a folded value has no record of the Go body that
	// produced it, so it must not survive the compilation.
	evalCache := optimize.NewEvalCache()

	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		res, err := emitTarget(pkg, target, len(targets) > 1, evalCache, o)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func emitTarget(pkg *ir.Package, target Target, clone bool, evalCache *optimize.EvalCache, o Options) (Result, error) {
	tpkg := pkg
	if clone {
		tpkg = ir.ClonePackage(pkg)
	}

	if target.Options == nil {
		target.Options = &ir.StructLit{}
	}
	if o.Main {
		codegen.SetOptionField(target.Options, "main", true)
	}
	if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
		codegen.SetOptionField(target.Options, "projectDir", o.Dir)
	}
	// Before optimize, not after: the root decides what the inliner flattens
	// into what, so the isolation has to be in place before anything moves.
	root := codegen.OptionString(target.Options, "rootComponent")
	IsolateRootComponent(tpkg, root)

	optCfg := &optimize.Config{
		Platform:    target.Platform,
		Language:    target.Lang,
		Dir:         o.Dir,
		NoCacheBust: optionBool(target.Options, "noCacheBust"),
		Cache:       evalCache,
	}
	start := time.Now()
	if err := optimize.Optimize(tpkg, optCfg); err != nil {
		return Result{}, fmt.Errorf("%s: %w", o.Dir, err)
	}
	slog.Info("optimize", "dir", o.Dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	plat := codegen.LookupPlatform(target.Platform)
	lang := codegen.LookupLang(target.Lang)
	if plat == nil {
		return Result{}, fmt.Errorf("%s: unknown platform %q (available: %v)", o.Name, target.Platform, codegen.Platforms())
	}
	if lang == nil {
		return Result{}, fmt.Errorf("%s: unknown language %q (available: %v)", o.Name, target.Lang, codegen.Langs())
	}

	if IsInterpreted(target) {
		// Full capabilities: every gated pass is compensation for something a
		// backend cannot emit, and the interpreter can emit everything -- in
		// particular Declarative, whose absence dissolves the visual tree the
		// interpreter mounts. What still runs is the desugaring no target does
		// without, such as turning `:value=x` into a prop and a handler. No
		// platform, so no override is inlined: a host is asked for `text`, not
		// `Label`.
		icaps := lower.AllFeatures().ToLowerCaps()
		if err := lower.Lower(tpkg, icaps, lower.Options{
			Platform:        target.Platform,
			RootComponent:   root,
			ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat),
		}); err != nil {
			return Result{}, fmt.Errorf("%s: %w", o.Dir, err)
		}
		return Result{Target: target, Pkg: tpkg}, nil
	}

	features, err := codegen.CapsFor(target.Lang, target.Platform)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", o.Dir, err)
	}
	caps := features.ToLowerCaps()
	start = time.Now()
	if err := lower.Lower(tpkg, caps, lower.Options{Platform: target.Platform, Language: target.Lang, RootComponent: root, ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat)}); err != nil {
		return Result{}, fmt.Errorf("%s: %w", o.Dir, err)
	}
	slog.Info("lower", "dir", o.Dir, "caps", caps.String(), "duration", time.Since(start))

	// Unconditional. It used to run only when the target had capabilities to lower
	// for, on the reading that a build lowering nothing had nothing new to fold --
	// which stopped being true when passQuery became always-on: it synthesizes a
	// thunk after the optimizer has walked every declaration, so the calls inside
	// one are calls nothing has looked at.
	start = time.Now()
	if err := optimize.Optimize(tpkg, optCfg); err != nil {
		return Result{}, fmt.Errorf("%s: %w", o.Dir, err)
	}
	slog.Info("optimize2", "dir", o.Dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	var fileAssets []codegen.FileAsset
	for _, fa := range optCfg.FileAssets {
		fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}

	start = time.Now()
	files, boilerplate, err := generate(o, tpkg, target, fileAssets)
	if err != nil {
		return Result{}, err
	}
	slog.Info("codegen", "dir", o.Dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	return Result{Target: target, Pkg: tpkg, Files: files, Boilerplate: boilerplate}, nil
}

func generate(o Options, pkg *ir.Package, target Target, fileAssets []codegen.FileAsset) (map[string][]byte, map[string]bool, error) {
	lang := codegen.LookupLang(target.Lang)
	plat := codegen.LookupPlatform(target.Platform)

	if !slices.Contains(plat.SupportedLangs(), target.Lang) {
		return nil, nil, fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", o.Name, target.Platform, target.Lang, plat.SupportedLangs())
	}

	projectFS := o.ProjectFS
	if projectFS == nil {
		projectFS = os.DirFS(filepath.Dir(o.Name))
	}

	req := &codegen.Request{
		Pkg:        pkg,
		Lang:       lang,
		Options:    target.Options,
		Source:     filepath.Base(o.Name),
		FileAssets: fileAssets,
		ProjectFS:  projectFS,
		Maps:       optionBool(target.Options, "maps"),
		OutDir:     o.OutDir,
	}
	mem := codegen.NewMemSink()
	if err := plat.Generate(req, mem); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", o.Name, err)
	}
	return mem.Files(), mem.Boilerplate(), nil
}

// IsolateRootComponent makes comp the program's only entry point, and is what
// the "rootComponent" option means: a harness renders the component it names
// rather than the program around it. Left in place, the inliner flattens that
// component into the program's own root and renames its state per instance, so
// the Model carries `n__inst0` where a test asks for `n` (#136).
//
// A name no component answers to is left alone: the option is a request.
func IsolateRootComponent(pkg *ir.Package, comp string) {
	if comp == "" || pkg == nil {
		return
	}
	if !slices.ContainsFunc(pkg.Components, func(c *ir.Component) bool { return c.Name == comp }) {
		return
	}
	pkg.Body = nil
	pkg.Windows = nil
	// Recorded rather than left implicit: with the windows gone, this is the
	// only thing left that says which declaration the program renders, and
	// AnalyzeCommon runs from the package alone.
	pkg.RootComponent = comp
}
