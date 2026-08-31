package interprun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// WorkerEnv names an explicit worker binary, which is how a developer points a
// run at one they are working on.
const WorkerEnv = "SNGL_FYNE_WORKER"

// WorkerPkg is the worker's import path.
const WorkerPkg = "git.duckfam.us/jonathan/sngl/cmd/sngl-fyne-worker"

// WorkerDir is where a project's worker is built, relative to it.
//
// Under a dot so Go ignores it: `go build ./...` and `go list ./...` skip
// directories beginning with "." or "_", so a stale or broken worker can never
// break the project's own build or its CI, while an explicit path still builds
// it.
const WorkerDir = ".tmp/sngl/worker"

// Locate finds the worker to render with: one named explicitly, then one built
// for this project.
//
// A worker on PATH is deliberately not consulted. It was built against some
// other module, and the argument in EnsureWorker cuts both ways -- a worker
// that resolved different versions than the program would ship with is a
// preview of a different program, whether it came from a cache directory or
// from PATH. SNGL_FYNE_WORKER stays, because naming one is asking for it.
func Locate(dir string) (string, error) {
	if p := os.Getenv(WorkerEnv); p != "" {
		return p, nil
	}
	return EnsureWorker(dir)
}

// EnsureWorker builds the worker for a project, or returns the cached one.
//
// Built in the project, so it resolves that module's replace directives and
// pinned versions rather than some other module's. Named by content hash, so
// several may coexist and a stale one is never mistaken for fresh.
func EnsureWorker(dir string) (string, error) {
	key, err := workerKey(dir)
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, filepath.FromSlash(WorkerDir), "worker-"+key)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	if err := selfIgnore(filepath.Join(dir, ".tmp", "sngl")); err != nil {
		return "", err
	}

	cmd := exec.Command("go", "build", "-o", out, WorkerPkg)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", buildError(dir, stderr.String(), err)
	}
	collectOldWorkers(filepath.Dir(out), filepath.Base(out))
	return out, nil
}

// workerKey is what the built binary depends on: the module's own resolution
// and the toolchain. The program is *not* part of it -- the worker holds no
// program, only the vocabulary it can render, so editing a file must not
// rebuild it. That is what lets a reload stay instant.
func workerKey(dir string) (string, error) {
	h := sha256.New()
	fmt.Fprintln(h, WorkerPkg, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	// In a checkout of sngl itself the worker's own source is part of the
	// build, and go.mod does not move when it changes. Outside one it is a
	// pinned dependency and go.sum covers it.
	for _, dep := range []string{"pkg/go/fynehost", "cmd/sngl-fyne-worker"} {
		if entries, err := os.ReadDir(filepath.Join(dir, dep)); err == nil {
			for _, e := range entries {
				if info, err := e.Info(); err == nil {
					fmt.Fprintln(h, dep, e.Name(), info.Size(), info.ModTime().UnixNano())
				}
			}
		}
	}
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // absent is a fact about the module like any other
		}
		fmt.Fprintln(h, name)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// selfIgnore makes the generated directory invisible to git without the user
// editing anything. A .gitignore of "*" hides the directory including itself,
// so `git status` stays clean and nothing here can be committed by accident.
func selfIgnore(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte("*\n"), 0o644)
}

// collectOldWorkers removes the binaries of previous builds. They are named by
// content, so anything that is not the current one answers a question nobody
// will ask again.
func collectOldWorkers(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == keep || !strings.HasPrefix(name, "worker-") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// buildError explains the failure a project is most likely to hit: its module
// does not require what the worker imports.
//
// `go mod tidy` does not scan dot-directories, so a module named only by .sngl
// sources gets pruned and stays pruned. The fix is a file the toolchain *can*
// see, which is what codegen/platform/fyne/keep_test_deps.go is for this repo.
func buildError(dir, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	hint := ""
	if strings.Contains(stderr, "no required module provides") || strings.Contains(stderr, "missing go.sum entry") {
		hint = "\n\nThe worker imports modules this one does not require. `go mod tidy` will not\n" +
			"find them either: it does not scan " + WorkerDir + ", because Go ignores dot-directories.\n" +
			"Add a file the toolchain can see, and commit it:\n\n" +
			"    //go:build sngl_worker_deps\n\n" +
			"    package " + filepath.Base(dir) + "\n\n" +
			"    import (\n" +
			"        _ \"" + WorkerPkg + "\"\n" +
			"    )\n\n" +
			"then run `go mod tidy`. The build tag is never satisfied, so nothing compiles it."
	}
	return fmt.Errorf("building a worker in %s failed:\n%s%s\n\nOr set %s to one you have already built.",
		dir, msg, hint, WorkerEnv)
}
