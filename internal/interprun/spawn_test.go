package interprun

import (
	"os"
	"testing"
	"time"

	"duckfam.us/sngl/pkg/go/snglhost"
)

// The test binary doubles as a worker when this is set, so the spawn path can
// be exercised without building one and without a display -- neither of which
// a test suite should need to check that a process is started and driven.
const workerEnv = "SNGL_TEST_ACT_AS_WORKER"

func TestMain(m *testing.M) {
	if os.Getenv(workerEnv) != "" {
		h := snglhost.NewMemHost()
		go func() { _ = snglhost.ServeHost(h, stdio{}) }()
		// A real worker lives until its window is shut. This one lives until
		// the tree arrives, then exits -- which is what closing a window looks
		// like from the driver's side, and the only way this test terminates.
		for range 500 {
			if len(h.Find("out")) > 0 {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(1) // the tree never arrived
	}
	os.Exit(m.Run())
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }

// TestRunSpawnsAWorkerAndDrivesIt covers the part Drive's own tests cannot: a
// real child process, its stdin and stdout joined into one stream, and the
// tree reaching it.
func TestRunSpawnsAWorkerAndDrivesIt(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find the test binary: %v", err)
	}
	t.Setenv(workerEnv, "1")

	done := make(chan error, 1)
	go func() {
		done <- Run(check(t, clickSrc), Options{Component: "main", Worker: self, Dir: t.TempDir()})
	}()

	select {
	case err := <-done:
		// The stub exits 0 only once #out is mounted on it, so a clean return
		// means the process was spawned, the tree was driven across its stdin,
		// and the driver noticed it go away.
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run neither returned nor was driven; the worker was not spawned or the stream never closed")
	}
}

// TestLocatePrefersAnExplicitWorker: a developer working on a worker points at
// it, and nothing else is consulted.
func TestLocatePrefersAnExplicitWorker(t *testing.T) {
	t.Setenv(WorkerEnv, "/nonexistent/worker-under-test")
	got, err := Locate(t.TempDir())
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got != "/nonexistent/worker-under-test" {
		t.Errorf("Locate chose %q, ignoring %s", got, WorkerEnv)
	}
}
