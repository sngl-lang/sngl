package testutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// cageActiveEnv marks a process (and its descendants) as already running inside
// a headless cage compositor, so nested test binaries and the verify wrapper
// don't double-wrap. It is set by MaybeReexecUnderCage and by `go tool verify`.
const cageActiveEnv = "SNGL_CAGE_ACTIVE"

// headlessCageEnv forces cage's wlroots headless backend + software renderer,
// so it runs without a real output or GPU and windows never reach the desktop.
var headlessCageEnv = []string{"WLR_BACKENDS=headless", "WLR_RENDERER=pixman", cageActiveEnv + "=1"}

// hasDisplay reports whether a Wayland/X11 session is present. Without one,
// GTK can't init and window-presenting tests skip themselves, so there is
// nothing to contain.
func hasDisplay() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// MaybeReexecUnderCage re-runs the current test binary inside a headless cage
// compositor so window-presenting tests (gtk4 snapshots) don't flash on the
// developer's desktop. Call it first thing in TestMain.
//
// It is a no-op when already headless (SNGL_CAGE_ACTIVE set — e.g. under
// `go tool verify`, or the re-exec'd child), when no display is present
// (nothing to contain), or when cage is not installed. In the last case the
// process continues un-wrapped; GUI tests then skip themselves via
// GUIRenderSkipReason rather than presenting on the real display.
//
// When it does re-exec, it runs the child to completion and exits with the
// child's status — it does not return.
func MaybeReexecUnderCage() {
	if os.Getenv(cageActiveEnv) != "" || !hasDisplay() {
		return
	}
	cage, err := exec.LookPath("cage")
	if err != nil {
		// No cage: don't wrap. GUIRenderSkipReason will skip GUI tests so
		// they don't flash. Warn once so the coverage gap is visible.
		fmt.Fprintln(os.Stderr, "note: cage not found; gtk4 GUI tests will skip (install cage to run them headlessly).")
		return
	}
	cmd := exec.Command(cage, append([]string{"--", os.Args[0]}, os.Args[1:]...)...)
	cmd.Env = append(os.Environ(), headlessCageEnv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
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
// real windows must not run in the current environment. After
// MaybeReexecUnderCage, the invariant is: a display is present AND we are not
// inside cage ⟹ cage was unavailable, so rendering would flash — skip. With no
// display, GTK can't init anyway. Inside cage, rendering is contained → run.
func GUIRenderSkipReason() string {
	if os.Getenv(cageActiveEnv) != "" {
		return ""
	}
	if !hasDisplay() {
		return "no display available for gtk4 GUI test"
	}
	return "gtk4 GUI test needs a headless compositor (cage) to avoid flashing; run via `go tool verify` or install cage"
}
