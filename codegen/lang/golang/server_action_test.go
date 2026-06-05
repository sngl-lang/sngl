package golang_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestServerAction drives the FULL CLI generate path (with the Go toolchain
// import resolver) so that the go:// api.Persist call resolves to a real
// backend func and the html placement pass classifies the @click handler as
// server-side. The in-package parity harness (TestParityGolden) cannot do this
// because it has no import resolver, so go:// calls there stay client-side JS.
//
// It asserts the emitted server.go is a CORRECT server-side action handler:
//   - a per-route State struct with `Count int`
//   - a renderRoute-style func that fills the page from state
//   - a GET handler that writes the rendered page
//   - a POST handler running `s.Count = api.Persist(s.Count + 1)` (state via
//     `s.`, the REAL api.Persist — not WASM) then a 303 redirect
//   - NO undefined bare `count`, no `__n0` DOM ref, no unimported `fmt`
//
// Finally it `go build`s the emitted package against the fixture go.mod — the
// key assertion, since the original bug produced non-compiling output.
func TestServerAction(t *testing.T) {
	repoRoot := repoRootDir(t)
	fixture := filepath.Join(repoRoot, "codegen", "lang", "golang", "testdata", "parity", "server_action")

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "snglbin")

	// Build the CLI.
	build := exec.Command("go", "build", "-o", bin, "./cmd/sngl")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	// Copy the fixture (go.mod + api/ + app.sngl) into a writable temp module
	// so we can generate the server INTO the module and `go build` it: the
	// emitted server imports "example.com/route-post/api", which only resolves
	// when the server package lives under the fixture's module.
	work := filepath.Join(tmp, "work")
	copyTree(t, fixture, work)

	// Generate into work/out (a subpackage of the fixture module).
	out := filepath.Join(work, "out")
	gen := exec.Command(bin, "generate", "--platform", "html", "--lang", "go", "-o", out, filepath.Join(work, "app.sngl"))
	gen.Dir = work
	if combined, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, combined)
	}

	serverPath := filepath.Join(out, "server.go")
	srcBytes, err := os.ReadFile(serverPath)
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := string(srcBytes)
	t.Logf("emitted server.go:\n%s", src)

	// --- correctness assertions ---

	mustContain := func(needle string) {
		t.Helper()
		if !strings.Contains(src, needle) {
			t.Errorf("server.go missing %q", needle)
		}
	}
	mustNotContain := func(needle, why string) {
		t.Helper()
		if strings.Contains(src, needle) {
			t.Errorf("server.go must not contain %q (%s)", needle, why)
		}
	}

	// State struct with Count int.
	if !strings.Contains(src, "Count int") {
		t.Errorf("server.go missing State field `Count int`")
	}
	// renderRoute-style func taking the State receiver and reading s.Count.
	mustContain("s.Count")
	// POST runs the real api.Persist against server state.
	mustContain("api.Persist(")
	// 303 redirect (PRG).
	mustContain("http.StatusSeeOther")

	// The broken-path symptoms must be gone.
	mustNotContain("__n0", "DOM element ref has no place in server-side render")
	// `api(` (the broken in-package output) — the WASM/JS call form.
	if strings.Contains(src, "api(state.count") {
		t.Errorf("server.go contains the broken client-side `api(state.count...` call")
	}

	// --- the key assertion: the emitted package must compile ---
	gobuild := exec.Command("go", "build", "./out/...")
	gobuild.Dir = work
	if combined, err := gobuild.CombinedOutput(); err != nil {
		t.Fatalf("emitted server package failed to compile: %v\n%s\n--- server.go ---\n%s", err, combined, src)
	}
}

// copyTree recursively copies src into dst (excluding any pre-existing out/).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
}

// repoRootDir walks up from the test's working directory to the module root
// (the dir containing go.mod with the sngl module path).
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if strings.Contains(string(b), "git.duckfam.us/jonathan/sngl") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod)")
		}
		dir = parent
	}
}
