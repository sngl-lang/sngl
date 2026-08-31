package interprun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheWorkerIsBuiltInTheProject is the property the whole layout exists for:
// a worker resolves the module it is built in, so it must be built there.
//
// The check is that a replace directive pointing somewhere the build cannot
// possibly succeed makes the build fail. That only happens if the project's
// module is the one being resolved.
func TestTheWorkerIsBuiltInTheProject(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	write(t, dir, "go.mod", `module example.com/proj

go 1.24

require git.duckfam.us/jonathan/sngl v0.0.0

replace git.duckfam.us/jonathan/sngl => ./nowhere
`)
	_, err := EnsureWorker(dir)
	if err == nil {
		t.Fatal("the build succeeded against a replace pointing at nothing; it was not built in the project")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the error does not name where it built: %v", err)
	}
}

// TestTheWorkerDirectoryHidesItself: nothing generated may show up in the
// user's `git status`, and they should not have to add anything for that.
func TestTheWorkerDirectoryHidesItself(t *testing.T) {
	dir := t.TempDir()
	if err := selfIgnore(filepath.Join(dir, ".tmp", "sngl")); err != nil {
		t.Fatalf("selfIgnore: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".tmp", "sngl", ".gitignore"))
	if err != nil {
		t.Fatalf("reading the ignore file: %v", err)
	}
	if strings.TrimSpace(string(b)) != "*" {
		t.Errorf("the ignore file reads %q, want everything ignored including itself", b)
	}
}

// TestTheKeyIgnoresTheProgram: a worker holds no program, only the vocabulary
// it can render, so editing a file must not rebuild it. That is what lets a
// reload stay instant.
func TestTheKeyIgnoresTheProgram(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/proj\n\ngo 1.24\n")
	write(t, dir, "app.sngl", "component main {}\n")
	before, err := workerKey(dir)
	if err != nil {
		t.Fatalf("workerKey: %v", err)
	}

	write(t, dir, "app.sngl", "component main {\n    text(value=\"edited\")\n}\n")
	after, err := workerKey(dir)
	if err != nil {
		t.Fatalf("workerKey: %v", err)
	}
	if before != after {
		t.Error("editing the program changed the worker key; every save would rebuild")
	}

	// The module's own resolution is a different matter: change that and the
	// worker is genuinely a different binary.
	write(t, dir, "go.mod", "module example.com/proj\n\ngo 1.24\n\nrequire example.com/x v1.0.0\n")
	changed, err := workerKey(dir)
	if err != nil {
		t.Fatalf("workerKey: %v", err)
	}
	if changed == before {
		t.Error("changing go.mod did not change the worker key")
	}
}

// TestOldWorkersAreCollected: binaries are named by content so several may
// coexist, which is what keeps an editor's long-lived worker from conflicting
// with a terminal starting one -- but the stale ones should not accumulate.
func TestOldWorkersAreCollected(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"worker-aaaa", "worker-bbbb", "notaworker"} {
		write(t, dir, name, "x")
	}
	collectOldWorkers(dir, "worker-bbbb")
	for name, wantGone := range map[string]bool{"worker-aaaa": true, "worker-bbbb": false, "notaworker": false} {
		_, err := os.Stat(filepath.Join(dir, name))
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s: removed=%v, want removed=%v", name, gone, wantGone)
		}
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheWorkerSourceListIsComplete holds workerSources to what the toolchain
// says the worker is built from.
//
// The list is hashed to decide whether a cached worker is stale, and a package
// missing from it is invisible: the binary keeps being reused while the code it
// was built from has moved. That is not hypothetical -- it shipped that way,
// with pkg/go/snglhost absent, so a change to the wire format left a worker
// speaking the old one with no way to notice.
//
// Asking `go build` every time would be correct by construction and costs 1.9s
// a run, which is the whole of what the cache buys. So the list stays, and this
// is what stops it rotting.
func TestTheWorkerSourceListIsComplete(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", WorkerPkg).Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	const mod = "git.duckfam.us/jonathan/sngl/"
	listed := map[string]bool{}
	for _, d := range workerSources {
		listed[d] = true
	}
	var found int
	for dep := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(dep, mod) {
			continue
		}
		found++
		if pkg := strings.TrimPrefix(dep, mod); !listed[pkg] {
			t.Errorf("the worker is built from %s, which workerSources does not hash: a change there would not rebuild it", pkg)
		}
	}
	if found == 0 {
		t.Fatal("go list reported no first-party dependencies; this test is checking nothing")
	}
	if found != len(workerSources) {
		t.Errorf("workerSources has %d entries for %d dependencies; one of them no longer exists", len(workerSources), found)
	}
}
