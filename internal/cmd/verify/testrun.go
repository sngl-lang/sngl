package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// testRunRecord is the file a package keeps when its own tests are answered
// from a record rather than run: the digest of what they build. A package's
// tests compile it, and gtk4rt's link the host's GTK -- which on CI is older
// than a developer's, so a call it lacks failed CI's link while every test of
// the package skipped there anyway, for want of a compositor. The package is
// left out of the test step while the digest stands, as a script's build and
// a fixture's launch are answered from theirs; -update runs its tests and
// writes the digest again.
const testRunRecord = "testdata/test_run"

// recordedTestRuns is those of pkgs that keep a testRunRecord.
func recordedTestRuns(pkgs []string) []string {
	var out []string
	for _, p := range pkgs {
		if _, err := os.Stat(filepath.Join(p, testRunRecord)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// testRunDigest names what pkg's tests build: the files each package of this
// module they import compiles or embeds, pkg's own tests, and the module's
// requirements. A dependency's tests are left out -- gtk4rt's tests import
// much of the compiler, and a test added beside it builds nothing of theirs --
// and so is the record itself. No toolchain moves it, which -verify is for.
//
// The files are those of a cgo build, which the tests are, whatever the host
// would build by default: with cgo off a cgo file is not listed.
func testRunDigest(pkg string) (string, error) {
	list := exec.Command("go", "list", "-deps", "-test", "-json", pkg)
	list.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := list.Output()
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", pkg, err)
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	self, err := filepath.Abs(pkg)
	if err != nil {
		return "", err
	}
	files := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p struct {
			Dir    string
			Module *struct{ Path string }
			GoFiles, CgoFiles, CFiles, HFiles, SFiles, CXXFiles, EmbedFiles, SysoFiles,
			TestGoFiles, XTestGoFiles []string
		}
		if err := dec.Decode(&p); err != nil {
			return "", fmt.Errorf("go list %s: %w", pkg, err)
		}
		if p.Module == nil || p.Module.Path != "duckfam.us/sngl" {
			continue
		}
		lists := [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.HFiles, p.SFiles, p.CXXFiles, p.EmbedFiles, p.SysoFiles}
		if p.Dir == self {
			lists = append(lists, p.TestGoFiles, p.XTestGoFiles)
		}
		for _, l := range lists {
			for _, f := range l {
				// The test main go generates lives in the build cache.
				if !filepath.IsAbs(f) {
					files[filepath.Join(p.Dir, f)] = true
				}
			}
		}
	}
	h := sha256.New()
	add := func(abs string) error {
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, abs)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
		return nil
	}
	// go.mod alone: it fixes every module's version, and CI's `go mod
	// download all` adds lines to go.sum that build nothing.
	if err := add(filepath.Join(root, "go.mod")); err != nil {
		return "", err
	}
	for _, f := range slices.Sorted(maps.Keys(files)) {
		if err := add(f); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16]), nil
}

// staleTestRuns is those of recorded whose record no longer names what their
// tests build, each with why.
func staleTestRuns(recorded []string) (map[string]string, error) {
	stale := map[string]string{}
	for _, p := range recorded {
		want, err := testRunDigest(p)
		if err != nil {
			return nil, err
		}
		have, err := os.ReadFile(filepath.Join(p, testRunRecord))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(have)) != want {
			stale[p] = "its tests' inputs changed since they ran"
		}
	}
	return stale, nil
}

// rerecordTestRuns runs the tests of each of recorded whose record moved,
// under the headless compositor, and writes the digest of each that passes.
func rerecordTestRuns(recorded []string, verbose bool) bool {
	stale, err := staleTestRuns(recorded)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	ok := true
	for _, p := range slices.Sorted(maps.Keys(stale)) {
		args := []string{"test", "-count=1"}
		if verbose {
			args = append(args, "-v")
		}
		args = append(args, p)
		command, wrapped, env := wrapHeadless("go", args)
		if !runStepEnv("test run "+p, env, command, wrapped...) {
			ok = false
			continue
		}
		want, err := testRunDigest(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			ok = false
			continue
		}
		if err := os.WriteFile(filepath.Join(p, testRunRecord), []byte(want+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			ok = false
		}
	}
	return ok
}

// withoutRecordedTestRuns is pkgs less those whose tests their record answers,
// or reports the ones whose record moved.
func withoutRecordedTestRuns(pkgs []string) ([]string, error) {
	recorded := recordedTestRuns(pkgs)
	stale, err := staleTestRuns(recorded)
	if err != nil {
		return nil, err
	}
	if len(stale) > 0 {
		var b bytes.Buffer
		for _, p := range slices.Sorted(maps.Keys(stale)) {
			fmt.Fprintf(&b, "\n  %s: %s", p, stale[p])
		}
		return nil, fmt.Errorf("a package's %s is stale; rerun `go tool verify -update` on a host with its toolchain:%s", testRunRecord, b.String())
	}
	return slices.DeleteFunc(slices.Clone(pkgs), func(p string) bool { return slices.Contains(recorded, p) }), nil
}
