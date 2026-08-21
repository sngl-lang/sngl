package testutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"git.duckfam.us/jonathan/sngl/internal/headless"
)

// MaybeReexecUnderCage re-runs the current test binary inside a headless cage
// compositor so window-presenting tests (gtk4 snapshots) can render at all,
// and never flash on a developer's desktop. Call it first thing in TestMain.
//
// It is a no-op when already inside cage (e.g. under `go tool verify`, or the
// re-exec'd child) and when cage is absent or cannot start here. In those
// cases the process continues un-wrapped and GUI tests skip themselves via
// GUIRenderSkipReason.
//
// When it does re-exec, it runs the child to completion and exits with the
// child's status — it does not return.
func MaybeReexecUnderCage() {
	if headless.Active() {
		return
	}
	if headless.Compositor() == "" {
		fmt.Fprintln(os.Stderr, "note: cage not found; gtk4 GUI tests will skip (install cage to run them headlessly).")
		return
	}
	command, args, env, cleanup := headless.Wrap(os.Args[0], os.Args[1:])
	if env == nil {
		return // Wrap reported why; run un-wrapped and let GUI tests skip.
	}
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	// os.Exit skips defers, so cleanup is explicit on every path.
	cleanup()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "cage re-exec failed:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// GUIRenderSkipReason returns a non-empty skip reason when a test that presents
// real windows must not run in the current environment.
func GUIRenderSkipReason() string { return headless.SkipReason() }
