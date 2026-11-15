// Package testinputs tells go test what a test read through a subprocess.
//
// go test replays a package's passing result while nothing it depends on
// has changed: the package's own sources and imports, and every file and
// environment variable the test binary itself opened or read. What a
// subprocess reads is invisible to it. A test that runs `go build` on a
// generated program, or on a command of this module its binary does not
// link, depends on sources go test never sees it read, and without this its
// cached result would outlive an edit to them.
package testinputs

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// root is the checkout this file is in.
var root = func() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("testinputs: no caller information")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// listFormat prints a package's directory, then each file it embeds, one
// per line.
const listFormat = `{{.Dir}}{{range .EmbedFiles}}
{{$.Dir}}/{{.}}{{end}}`

// Packages records, as inputs of the running test, the sources of pkgs and
// of every package they import from this checkout, along with its go.mod
// and go.sum, which settle every module from outside it. pkgs are listed
// from dir, so a pattern resolves as the subprocess's own build would:
// dir is where that build runs.
//
// Call it beside the subprocess, before or after: go test hashes what was
// read once the test binary exits. A package that does not build is listed
// all the same (-e), since a test may build it to see it fail, and what its
// tests import is listed too (-test), for a subprocess that runs `go test`.
func Packages(t testing.TB, dir string, pkgs ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-e", "-deps", "-test", "-f", listFormat}, pkgs...)...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testinputs: go list -deps %s in %s: %v\n%s", strings.Join(pkgs, " "), dir, err, stderr.String())
	}
	if err := record(strings.Split(strings.TrimSpace(string(out)), "\n")); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
}

// Module returns a new temporary directory, outside the checkout, holding a
// module that resolves as the checkout does: a copy of its go.mod and go.sum
// with duckfam.us/sngl required and replaced by the checkout. A generated
// program written there imports the sngl runtimes and every module from
// outside at the versions the checkout pins.
//
// Outside, because a directory made and removed inside the checkout changes
// the time of the one holding it, and every test that stats that one --
// resolving a path does -- then reads an input that differs on every run,
// so go test can replay none of them.
func Module(t testing.TB) string {
	t.Helper()
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	m := string(mod)
	const self = "module duckfam.us/sngl\n"
	if !strings.HasPrefix(m, self) {
		t.Fatalf("testinputs: %s does not begin %q", filepath.Join(root, "go.mod"), self)
	}
	// The tools are the checkout's to run, not this module's.
	m = toolBlock.ReplaceAllString(m, "")
	m = "module sngltest.local/generated\n" + m[len(self):] +
		"\nrequire duckfam.us/sngl v0.0.0\n\nreplace duckfam.us/sngl => " + root + "\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(m), 0o644); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	return dir
}

// Beside returns an empty directory in dir for the running test, named from
// prefix, the test's name and how many it has asked for: a place in the
// checkout for a generated program that imports an internal package, which
// only code under the package holding it may, or for a file a tool will only
// read inside the checkout. Module is the place for any other. The test's
// cleanup removes it.
//
// Its name is the same on every run, because go test hashes a path a test
// opened by its name, even once it is gone, and inside the checkout a random
// one would differ every time. dir must be one the package keeps for the
// purpose -- tracked, ignoring all else, and used by no other package --
// because making a directory in it changes its time and go test sees that
// too: Beside puts it back, which only the package's own tests, which do
// not run in parallel, can rely on.
func Beside(t testing.TB, dir, prefix string) string {
	t.Helper()
	besideMu.Lock()
	besideCount[t.Name()]++
	n := besideCount[t.Name()]
	besideMu.Unlock()
	name := prefix + strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' {
			return '_'
		}
		return r
	}, t.Name())
	if n > 1 {
		name += "." + strconv.Itoa(n)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	path, err := filepath.Abs(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	// One an interrupted run left.
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Errorf("testinputs: %v", err)
		}
		if err := os.Chtimes(dir, before.ModTime(), before.ModTime()); err != nil {
			t.Errorf("testinputs: %v", err)
		}
	})
	return path
}

var (
	besideMu    sync.Mutex
	besideCount = map[string]int{}
)

// TempDir returns a new directory outside the checkout for the running test,
// named from the checkout and the test, for a test that changes into it.
//
// go test hashes every directory a test changed into, by its path and
// wherever it is, so the random name t.TempDir gives would make a test that
// changes into one differ on every run. Should a run of the same checkout's
// test hold the name -- two at once -- it is t.TempDir's after all, which
// costs that result its replay and nothing else. The test's cleanup
// removes it.
func TempDir(t testing.TB) string {
	t.Helper()
	sum := sha256.Sum256([]byte(root))
	name := fmt.Sprintf("sngl-%x-%s", sum[:4], strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' {
			return '_'
		}
		return r
	}, t.Name()))
	dir := filepath.Join(os.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("testinputs: %v", err)
		}
		t.Logf("testinputs: %s is in use; this result will not be replayed", dir)
		return t.TempDir()
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("testinputs: %v", err)
		}
	})
	return dir
}

// toolBlock matches a go.mod's tool directives, in a block or one per line.
var toolBlock = regexp.MustCompile(`(?m)^tool (\([^)]*\)|\S+)\n`)

// Tree records, as inputs of the running test, each of paths -- relative to
// the checkout's root -- and, for a directory, everything beneath it: what a
// subprocess reads that is not Go, such as the markdown a site is built from.
func Tree(t testing.TB, paths ...string) {
	t.Helper()
	known, err := tracked()
	if err != nil {
		t.Fatalf("testinputs: %v", err)
	}
	var under []string
	for _, p := range paths {
		p = filepath.Join(root, p)
		if !known[p] {
			t.Fatalf("testinputs: git tracks nothing at %s", p)
		}
		for k := range known {
			if k == p || strings.HasPrefix(k, p+string(filepath.Separator)) {
				under = append(under, k)
			}
		}
	}
	if err := record(under); err != nil {
		t.Fatalf("testinputs: %v", err)
	}
}

// record opens each of paths that git tracks: a directory is hashed by its
// listing, each entry's size and modification time, and a file by its own.
//
// Only what git tracks, because only that has the same time in every
// checkout of the same bytes (go run ./internal/cmd/stamp). The rest is
// outside the checkout, a module go.sum names, or what a test wrote itself,
// such as the temporary package a generated program is built in, whose
// random name would make every run's record differ.
func record(paths []string) error {
	known, err := tracked()
	if err != nil {
		return err
	}
	paths = append(paths, filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"))
	for _, p := range paths {
		if !known[p] {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue // the Stat is the record
		}
		if _, err := os.ReadDir(p); err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
	}
	return nil
}

// tracked is every file git tracks in the checkout and every directory
// holding one, by absolute path.
var tracked = sync.OnceValues(func() (map[string]bool, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	known := map[string]bool{root: true}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		for p := filepath.Join(root, name); !known[p]; p = filepath.Dir(p) {
			known[p] = true
		}
	}
	return known, nil
})
