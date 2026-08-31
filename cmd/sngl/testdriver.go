package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/codegen/testharness/snapshot"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/testrpc"
	"git.duckfam.us/jonathan/sngl/ir"
)

func resolveLauncher(plat codegen.PlatformGenerator, lang codegen.LangTranslator) codegen.TestLauncher {
	if l, ok := plat.(codegen.TestLauncher); ok {
		return l
	}
	if l, ok := lang.(codegen.TestLauncher); ok {
		return l
	}
	return nil
}

// Tests are grouped by their component-under-test (second parameter type), and
// each group runs in its own launcher invocation against the original IR
// package: the component name goes over the rootComponent option so the
// platform builds its Model from it, with no AST round-trip.
func runViaLauncher(ctx context.Context, plat codegen.PlatformGenerator, lang codegen.LangTranslator, pkg *ir.Package, opts *ir.StructLit, fixtureDir, fixtureFile string) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test launcher: %v", r)
		}
	}()
	launcher := resolveLauncher(plat, lang)
	if launcher == nil {
		return nil, fmt.Errorf("no TestLauncher for platform %q lang %q", plat.PlatformIdentifier(), langIdent(lang))
	}

	if opts == nil {
		opts = &ir.StructLit{}
	}
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
	if os.Getenv("SNGL_DEBUG_LAUNCHER") != "" {
		for _, f := range pkg.Funcs {
			fmt.Fprintf(os.Stderr, "DEBUG pkg.Func name=%q receiver=%q isTest=%v\n", f.Name, f.Receiver, f.IsTest)
		}
	}
	groups := testharness.GroupIR(testFns)

	for _, group := range groups {
		// Each group lowers its own clone, the way each build target does: the
		// pipeline mutates in place, and two groups do not agree on what the
		// root is.
		groupPkg := ir.ClonePackage(pkg)
		rootForTests(groupPkg, group.Component)
		if err := prepareForLaunch(groupPkg, plat, lang, group.Component); err != nil {
			return results, err
		}

		groupOpts := cloneOptions(opts)
		if group.Component != "" {
			codegen.SetOptionField(groupOpts, "rootComponent", group.Component)
		}
		grpResults, err := launchOneGroup(ctx, plat, lang, launcher, groupPkg, groupOpts, fixtureDir, fixtureFile, group)
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

func launchOneGroup(ctx context.Context, plat codegen.PlatformGenerator, lang codegen.LangTranslator, launcher codegen.TestLauncher, pkg *ir.Package, opts *ir.StructLit, fixtureDir, fixtureFile string, group testharness.TestGroup) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test launcher: %v", r)
		}
	}()
	tmpDir, mkErr := os.MkdirTemp("", "sngl-test-")
	if mkErr != nil {
		return nil, fmt.Errorf("mktemp: %w", mkErr)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(tmpDir)
	}
	keep := map[string]bool{}
	for _, tf := range group.Funcs {
		keep[tf.Name] = true
	}
	scopedPkg := pkgWithTestSubset(pkg, keep)

	req := &codegen.Request{
		Pkg:     scopedPkg,
		Lang:    lang,
		Options: opts,
		Source:  "",
		OutDir:  tmpDir,
	}
	if err := plat.Generate(req, codegen.NewDirSink(tmpDir)); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	ch, cleanup, err := launcher.LaunchTest(ctx, tmpDir, lang, opts)
	if err != nil {
		if skip, ok := errors.AsType[*codegen.SkipError](err); ok {
			return []*codegen.TestResult{{
				Desc:       "<launcher-skip>",
				Skipped:    true,
				SkipReason: skip.Reason,
				Log:        []string{"SKIP: " + skip.Reason},
			}}, nil
		}
		return nil, fmt.Errorf("launch: %w", err)
	}
	defer cleanup()
	return driveRPC(ch, fixtureDir, fixtureFile)
}

// Best-effort, for error messages: language types spell this differently.
// langIdent is the language's name, as `ir.Language` declares it.
//
// It probed for two method names a LangTranslator does not have —
// LangIdentifier and Identifier — so it answered "" for every language there
// is. The optimizer and the lowering both take that name, and with no name the
// program under test was optimized differently from the one `sngl generate`
// builds: `go test` and `go build` disagreed about the same source.
func langIdent(lang codegen.LangTranslator) string {
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
	starts := map[string]time.Time{}
	var results []*codegen.TestResult
	_ = starts // reserved for richer per-test timing if Agent omits durationMs

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

// rootForTests makes comp the program's only entry point.
//
// A test renders the component it names, not the program around it. Left in
// place, that program is a root the inliner flattens the component under test
// into -- renaming its state per instance, so the Model carries `n__inst0`
// where the agent, written against the declaration, asks for `n`. The symptom
// was that a component test passed only while nothing else in the program
// rendered (#136).
//
// A group naming no component tests plain functions; there is nothing to make
// a root of, and the program is left as it is.
func rootForTests(pkg *ir.Package, comp string) {
	if comp == "" {
		return
	}
	pkg.Body = nil
	pkg.Windows = nil
}

// prepareForLaunch runs the generate pipeline's full optimize→lower→optimize
// over one group's package. lower alone leaves constructs the language codegen
// rejects, e.g. a ternary stranded in the __ctx_* Var.Init passContext
// synthesizes, which NoTernary does not rewrite (it lowers ternaries in
// statement positions only) and the post-lower optimize folds away.
func prepareForLaunch(pkg *ir.Package, plat codegen.PlatformGenerator, lang codegen.LangTranslator, root string) error {
	optCfg := &optimize.Config{
		Platform: plat.PlatformIdentifier(),
		Language: langIdent(lang),
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return fmt.Errorf("optimize for tests: %w", err)
	}
	caps := plat.Capabilities(lang).ToLowerCaps()
	// The same options every other caller lowers with. Without the language,
	// passPlatformExtensionBody picks a different body for a component that
	// overrides on both axes, so the program under test was not the program
	// `sngl generate` builds.
	if err := lower.Lower(pkg, caps, lower.Options{
		Platform:        plat.PlatformIdentifier(),
		Language:        langIdent(lang),
		RootComponent:   root,
		ClaimsIntrinsic: codegen.ClaimsIntrinsicFunc(plat),
	}); err != nil {
		return fmt.Errorf("lower for tests: %w", err)
	}
	if caps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return fmt.Errorf("optimize for tests: %w", err)
		}
	}
	return nil
}
