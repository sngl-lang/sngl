package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/codegen/testharness/snapshot"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/testrpc"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resolveLauncher returns the TestLauncher for a target. Platform takes
// precedence; language is the fallback. Returns nil if neither
// implements it.
func resolveLauncher(plat codegen.PlatformGenerator, lang codegen.LangTranslator) codegen.TestLauncher {
	if l, ok := plat.(codegen.TestLauncher); ok {
		return l
	}
	if l, ok := lang.(codegen.TestLauncher); ok {
		return l
	}
	return nil
}

// runViaLauncher generates the target's sources + testagent main into a
// tmpdir, invokes Launch, and drives the RPC stream until runComplete
// or the agent closes. Returns one TestResult per testEnd notification.
//
// Tests are grouped by their component-under-test (second parameter type).
// Each group runs in its own launcher invocation against a doc with that
// component promoted into a synthetic window, mirroring how the legacy
// per-platform RunTests paths isolated a single test subject.
func runViaLauncher(ctx context.Context, plat codegen.PlatformGenerator, lang codegen.LangTranslator, pkg *ir.Package, opts *ir.StructLit, fixtureDir string) (results []*codegen.TestResult, err error) {
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

	doc := ir.Convert(pkg)
	groups := testharness.Group(doc.TestFuncs())

	for _, group := range groups {
		if group.Component == "" {
			// Tests with no component-under-test: run against the original
			// package as-is (no promotion). Useful for plain unit tests.
			grpResults, err := launchOneGroup(ctx, plat, lang, launcher, pkg, opts, fixtureDir, group)
			if err != nil {
				return results, err
			}
			results = append(results, grpResults...)
			continue
		}
		compDoc := testharness.Promote(doc, group.Component)
		if compDoc == nil {
			continue
		}
		compPkg, diags := checker.Check(compDoc, &checker.Config{IsMain: true})
		hasErr := false
		for _, d := range diags {
			if d.Severity == ir.Error {
				hasErr = true
				break
			}
		}
		if hasErr || compPkg == nil {
			continue
		}
		caps := plat.Capabilities().Merge(lang.Capabilities())
		if err := lower.Lower(compPkg, caps, lower.Options{Platform: plat.PlatformIdentifier()}); err != nil {
			return results, fmt.Errorf("lower %q: %w", group.Component, err)
		}
		// Re-attach the original test funcs to the promoted package so
		// the platform's Generate path (which collects tests off pkg.Funcs)
		// can emit the agent harness. Promotion stripped them because they
		// reference component types that no longer exist post-promotion.
		for _, f := range pkg.Funcs {
			if f.IsTest && belongsToGroup(f, group) {
				compPkg.Funcs = append(compPkg.Funcs, f)
			}
		}
		grpResults, err := launchOneGroup(ctx, plat, lang, launcher, compPkg, opts, fixtureDir, group)
		if err != nil {
			return results, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

// belongsToGroup reports whether a test func's component-under-test
// (second parameter type) matches the named group.
func belongsToGroup(f *ir.Func, group testharness.TestGroup) bool {
	for _, tf := range group.Funcs {
		if tf.Name == f.Name {
			return true
		}
	}
	return false
}

// launchOneGroup runs a single component-group through the launcher.
// It generates the target sources into a tempdir, launches the agent,
// and collects testEnd notifications.
func launchOneGroup(ctx context.Context, plat codegen.PlatformGenerator, lang codegen.LangTranslator, launcher codegen.TestLauncher, pkg *ir.Package, opts *ir.StructLit, fixtureDir string, group testharness.TestGroup) (results []*codegen.TestResult, err error) {
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
	_ = group

	req := &codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: opts,
		Source:  "",
	}
	if err := plat.Generate(req, codegen.NewDirSink(tmpDir)); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	ch, cleanup, err := launcher.LaunchTest(ctx, tmpDir, lang, opts)
	if err != nil {
		return nil, fmt.Errorf("launch: %w", err)
	}
	defer cleanup()
	return driveRPC(ch, fixtureDir)
}

// langIdent returns a best-effort identifier for the language, for
// error messages. Different language types may expose this differently;
// fall back to "" when none of the known methods is available.
func langIdent(lang codegen.LangTranslator) string {
	type identer interface{ LangIdentifier() string }
	if l, ok := lang.(identer); ok {
		return l.LangIdentifier()
	}
	type identer2 interface{ Identifier() string }
	if l, ok := lang.(identer2); ok {
		return l.Identifier()
	}
	return ""
}

// driveRPC sends list+run, then collects testStart/log/testEnd/markFail
// notifications into TestResults. snapshotAssert requests are answered
// via the snapshot.Store backed by fixtureDir.
func driveRPC(ch codegen.RPCChannel, fixtureDir string) ([]*codegen.TestResult, error) {
	r := testrpc.NewReader(ch)
	w := testrpc.NewWriter(ch)
	store := &snapshot.Store{Dir: fixtureDir, Update: os.Getenv("SNGL_UPDATE_SNAPSHOTS") == "1"}

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
			handleSnapshotAssert(w, store, m, filepath.Base(fixtureDir))
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
				cur.Failures = append(cur.Failures, codegen.TestFailure{Message: "fail recorded"})
			}
		case "markSkip":
			var p struct{ Test, Reason string }
			_ = json.Unmarshal(m.Params, &p)
			if cur := current[p.Test]; cur != nil {
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
