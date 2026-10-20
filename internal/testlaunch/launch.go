// Package testlaunch runs a checked package's tests by generating a test
// program for one target and launching it -- what `sngl test` does for every
// target but none, and what the html component fixtures do in process.
package testlaunch

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/testharness"
	"duckfam.us/sngl/codegen/testharness/snapshot"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/testrpc"
	"duckfam.us/sngl/internal/trust"
	"duckfam.us/sngl/ir"
)

// Config says what to launch a package's tests on and where its fixture lives.
type Config struct {
	Platform codegen.PlatformGenerator
	Lang     codegen.LangTranslator
	Options  *ir.StructLit
	Trust    *trust.Policy

	// Dir and File name the fixture; its snapshots are Dir/File.snapshots.
	Dir, File string

	// Records, when set, answers each launch from a record of an earlier one
	// with the same inputs.
	Records *Records
}

// Launcher is what launches a pair's tests, or nil when neither half can.
func Launcher(plat codegen.PlatformGenerator, lang codegen.LangTranslator) codegen.TestLauncher {
	if l, ok := plat.(codegen.TestLauncher); ok {
		return l
	}
	if l, ok := lang.(codegen.TestLauncher); ok {
		return l
	}
	return nil
}

// Run runs pkg's tests. They are grouped by their component-under-test (second
// parameter type), and each group runs in its own launcher invocation against
// the original IR package: the component name goes over the rootComponent
// option so the platform builds its Model from it, with no AST round-trip.
func Run(ctx context.Context, pkg *ir.Package, cfg Config) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test launcher: %v", r)
		}
	}()
	plat, lang := cfg.Platform, cfg.Lang
	launcher := Launcher(plat, lang)
	if launcher == nil {
		return nil, fmt.Errorf("no TestLauncher for platform %q lang %q", plat.PlatformIdentifier(), LangIdent(lang))
	}

	opts := cloneOptions(cfg.Options)
	codegen.SetOptionField(opts, "test", true)
	codegen.SetOptionField(opts, "testMode", "agent")

	// Group before lowering, not after: the component under test is what the
	// harness renders, so it is the program's entry point for this build, and
	// lowering has to know that before it inlines anything. Grouping reads the
	// second parameter's type, which the checked IR already carries.
	var testFns []*ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			testFns = append(testFns, f)
		}
	}
	groups := testharness.GroupIR(testFns)

	for _, group := range groups {
		// Each group lowers its own clone, the way each build target does: the
		// pipeline mutates in place, and two groups do not agree on what the
		// root is.
		groupPkg := ir.ClonePackageFor(pkg, plat.PlatformIdentifier(), LangIdent(lang))
		build.IsolateRootComponent(groupPkg, group.Component)
		if group.Component == "" && !groupPkg.IsProgram() {
			// Tests taking no component, in a package whose body renders
			// nothing, render nothing either. Left in, every component was
			// an entry point of its own, and the Go targets emitted its view
			// against a Model holding none of its state.
			groupPkg.Components = nil
		}
		// The group's own tests, before lowering as well as at generation:
		// what a test reaches decides what the lowering stamps -- the key of
		// every copy a test indexes (passCopyKeys) -- and a stamp only another
		// group's test asked for named a map this group's code never declared.
		keep := map[string]bool{}
		for _, tf := range group.Funcs {
			keep[tf.Name] = true
		}
		groupPkg.Funcs = pkgWithTestSubset(groupPkg, keep).Funcs
		assets, err := prepareForLaunch(groupPkg, cfg, group.Component)
		if err != nil {
			return results, err
		}

		groupOpts := cloneOptions(opts)
		if group.Component != "" {
			codegen.SetOptionField(groupOpts, "rootComponent", group.Component)
		}
		grpResults, err := launchOneGroup(ctx, cfg, launcher, groupPkg, groupOpts, assets, group)
		if err != nil {
			return results, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

// Only the Fields slice is duplicated — the value pointers are read-only at
// this stage — so a per-group SetOptionField cannot leak into a sibling group.
func cloneOptions(opts *ir.StructLit) *ir.StructLit {
	if opts == nil {
		return &ir.StructLit{}
	}
	out := &ir.StructLit{Fields: make([]ir.FieldInit, len(opts.Fields))}
	copy(out.Fields, opts.Fields)
	return out
}

// Scopes a per-group launcher invocation so each binary registers only the
// tests it should run.
func pkgWithTestSubset(pkg *ir.Package, keep map[string]bool) *ir.Package {
	out := *pkg
	out.Funcs = make([]*ir.Func, 0, len(pkg.Funcs))
	for _, f := range pkg.Funcs {
		if f.IsTest && !keep[f.Name] {
			continue
		}
		out.Funcs = append(out.Funcs, f)
	}
	return &out
}

func launchOneGroup(ctx context.Context, cfg Config, launcher codegen.TestLauncher, pkg *ir.Package, opts *ir.StructLit, assets []codegen.FileAsset, group testharness.TestGroup) (results []*codegen.TestResult, err error) {
	plat, lang := cfg.Platform, cfg.Lang
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test launcher: %v", r)
		}
	}()
	// Stable, key-derived build directory: the Go build cache keys its
	// compile and link actions on the source directory, so generating
	// into a fresh mktemp dir relinks the whole agent binary every run.
	// The directory is kept after the run (what SNGL_KEEP_TEST_DIR used
	// to ask for) and emptied on the next use of the same key; `sngl -v`
	// logs the path. See codegen.BuildDir.
	tmpDir, release, mkErr := codegen.BuildDir("test",
		plat.PlatformIdentifier(), LangIdent(lang), cfg.File, group.Component)
	if mkErr != nil {
		return nil, fmt.Errorf("build dir: %w", mkErr)
	}
	defer release()
	keep := map[string]bool{}
	for _, tf := range group.Funcs {
		keep[tf.Name] = true
	}
	scopedPkg := pkgWithTestSubset(pkg, keep)

	req := &codegen.Request{
		Pkg:        scopedPkg,
		Lang:       lang,
		Options:    opts,
		Source:     "",
		OutDir:     tmpDir,
		FileAssets: assets,
		ProjectFS:  build.ProjectFS(cfg.Dir),
	}
	if err := plat.Generate(req, codegen.NewDirSink(tmpDir)); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	launch := func() ([]*codegen.TestResult, error) {
		ch, cleanup, err := launcher.LaunchTest(ctx, tmpDir, lang, opts)
		if err != nil {
			if _, ok := errors.AsType[*codegen.SkipError](err); ok {
				return nil, err
			}
			return nil, fmt.Errorf("launch: %w", err)
		}
		defer cleanup()
		return driveRPC(ch, cfg.Dir, cfg.File)
	}
	if cfg.Records != nil {
		key := strings.Join([]string{plat.PlatformIdentifier(), LangIdent(lang), cfg.File, cmp.Or(group.Component, "_")}, "/")
		snapDir := filepath.Join(cfg.Dir, cfg.File+".snapshots")
		return cfg.Records.answer(key, tmpDir, snapDir, launch)
	}
	results, err = launch()
	if skip, ok := errors.AsType[*codegen.SkipError](err); ok {
		return []*codegen.TestResult{{
			Desc:       "<launcher-skip>",
			Skipped:    true,
			SkipReason: skip.Reason,
			Log:        []string{"SKIP: " + skip.Reason},
		}}, nil
	}
	return results, err
}

// LangIdent is the language's name, as `ir.Language` declares it, or "" for none.
func LangIdent(lang codegen.LangTranslator) string {
	if lang == nil {
		return ""
	}
	return lang.LanguageIdentifier()
}

func driveRPC(ch codegen.RPCChannel, fixtureDir, fixtureFile string) ([]*codegen.TestResult, error) {
	r := testrpc.NewReader(ch)
	w := testrpc.NewWriter(ch)
	store := &snapshot.Store{Dir: fixtureDir, Update: os.Getenv("SNGL_UPDATE_SNAPSHOTS") == "1"}
	// The store appends ".snapshots", so goldens live in
	// <fixtureDir>/<fixtureFile>.snapshots/<name>.<ext>.
	fixtureBase := fixtureFile

	runID, err := w.Request("run", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("request run: %w", err)
	}

	current := map[string]*codegen.TestResult{}
	var results []*codegen.TestResult

	for {
		m, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return results, err
		}
		if m.IsResponse() && m.ID != nil && *m.ID == runID {
			break
		}
		if m.ID != nil && m.Method == "snapshotAssert" {
			handleSnapshotAssert(w, store, m, fixtureBase)
			continue
		}
		switch m.Method {
		case "testStart":
			var p struct{ Test string }
			_ = json.Unmarshal(m.Params, &p)
			current[p.Test] = &codegen.TestResult{Desc: p.Test, Passed: true}
		case "log":
			var p struct{ Test, Msg string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Log = append(cur.Log, p.Msg)
			}
		case "markFail":
			var p struct{ Test string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Passed = false
				// Every agent reports a failed assertion as a log line
				// followed by a bare fail, so the message has already
				// arrived. Recording "fail recorded" instead threw it away
				// and printed a `--- FAIL:` with nothing under it -- the
				// interpreter, which sets Error itself, was the only runner
				// that ever said what went wrong.
				msg := "fail recorded"
				if n := len(cur.Log); n > 0 {
					msg = cur.Log[n-1]
				}
				cur.Failures = append(cur.Failures, codegen.TestFailure{Message: msg})
				if cur.Error == "" {
					cur.Error = msg
				}
			}
		case "markSkip":
			var p struct{ Test, Reason string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
				cur.Skipped = true
				cur.SkipReason = p.Reason
				cur.Log = append(cur.Log, "SKIP: "+p.Reason)
			}
		case "testEnd":
			var p struct {
				Test       string
				Status     string
				DurationMs int
			}
			_ = json.Unmarshal(m.Params, &p)
			cur := current[p.Test]
			if cur == nil {
				cur = &codegen.TestResult{Desc: p.Test}
			}
			cur.Passed = p.Status == "pass"
			if p.Status == "skip" {
				cur.Skipped = true
			}
			cur.Duration = time.Duration(p.DurationMs) * time.Millisecond
			results = append(results, cur)
			delete(current, p.Test)
		}
	}
	return results, nil
}

func handleSnapshotAssert(w *testrpc.Writer, store *snapshot.Store, m *testrpc.Message, fixtureBase string) {
	var p struct {
		Test  string `json:"test"`
		Name  string `json:"name"`
		Mime  string `json:"mime"`
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(p.Bytes)
	if err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32602, Message: "bad base64"})
		return
	}
	res, err := store.Assert(fixtureBase, p.Name, p.Mime, raw)
	if err != nil {
		_ = w.Respond(*m.ID, nil, &testrpc.RPCError{Code: -32603, Message: err.Error()})
		return
	}
	_ = w.Respond(*m.ID, map[string]any{"pass": res.Pass, "diff": res.Diff}, nil)
}

// prepareForLaunch runs the generate pipeline's full optimize→lower→optimize
// over one group's package. lower alone leaves constructs the language codegen
// rejects, e.g. a ternary stranded in the __ctx_* Var.Init passContext
// synthesizes, which NoTernary does not rewrite (it lowers ternaries in
// statement positions only) and the post-lower optimize folds away.
func prepareForLaunch(pkg *ir.Package, cfg Config, root string) ([]codegen.FileAsset, error) {
	plat, lang := cfg.Platform, cfg.Lang
	optCfg := &optimize.Config{
		Trust:    cfg.Trust,
		Platform: plat.PlatformIdentifier(),
		Language: LangIdent(lang),
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return nil, fmt.Errorf("optimize for tests: %w", err)
	}
	feats, err := codegen.CapsFor(lang.LanguageIdentifier(), plat.PlatformIdentifier())
	if err != nil {
		return nil, fmt.Errorf("lower for tests: %w", err)
	}
	// The same options every other caller lowers with. Without the language,
	// passPlatformExtensionBody picks a different body for a component that
	// overrides on both axes, so the program under test was not the program
	// `sngl generate` builds.
	if err := lower.Lower(pkg, feats, lower.Options{
		Platform:      plat.PlatformIdentifier(),
		Language:      LangIdent(lang),
		RootComponent: root,
		// A group rooted at no component emits the tests handed none, so
		// it repoints nothing; another group roots at each other component.
		Tests:           root != "",
		ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat), RendersNative: codegen.RendersNativeFunc(plat),
	}); err != nil {
		return nil, fmt.Errorf("lower for tests: %w", err)
	}
	// Unconditional. It used to run only when the target had capabilities to lower
	// for, on the reading that a build lowering nothing had nothing new to fold --
	// which is not so: an always-on lowering pass may synthesize code after the
	// optimizer has walked every declaration, and the calls inside it are calls
	// nothing has looked at.
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return nil, fmt.Errorf("optimize for tests: %w", err)
	}
	var assets []codegen.FileAsset
	for _, fa := range optCfg.FileAssets {
		assets = append(assets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}
	return assets, nil
}
