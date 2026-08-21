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

// hasDisplay reports whether a Wayland/X11 session is present. This says
// whether rendering would reach a real desktop, not whether rendering is
// possible: cage's headless backend supplies its own display.
func hasDisplay() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// MaybeReexecUnderCage re-runs the current test binary inside a headless cage
// compositor so window-presenting tests (gtk4 snapshots) can render at all,
// and never flash on a developer's desktop. Call it first thing in TestMain.
//
// It is a no-op when already inside cage (SNGL_CAGE_ACTIVE set — e.g. under
// `go tool verify`, or the re-exec'd child) and when cage is absent or cannot
// start here. In those cases the process continues un-wrapped and GUI tests
// skip themselves via GUIRenderSkipReason.
//
// Cage is tried whether or not a display is present. Its headless backend
// creates one, so a container with no desktop can still run the GUI tests —
// skipping on !hasDisplay() gave CI a false all-clear, reporting the gtk4
// render tests as skipped on exactly the machines that could have run them.
//
// When it does re-exec, it runs the child to completion and exits with the
// child's status — it does not return.
func MaybeReexecUnderCage() {
	if os.Getenv(cageActiveEnv) != "" {
		return
	}
	cage, err := exec.LookPath("cage")
	if err != nil {
		fmt.Fprintln(os.Stderr, "note: cage not found; gtk4 GUI tests will skip (install cage to run them headlessly).")
		return
	}

	env := append(os.Environ(), headlessCageEnv...)
	// cage refuses to start without XDG_RUNTIME_DIR, which a bare container
	// typically lacks. Anything this function creates it also removes, and
	// os.Exit skips defers, so cleanup is explicit on every path.
	cleanup := func() {}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		dir, err := os.MkdirTemp("", "sngl-cage-run")
		if err != nil {
			fmt.Fprintln(os.Stderr, "note: no XDG_RUNTIME_DIR for cage; gtk4 GUI tests will skip:", err)
			return
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			os.RemoveAll(dir)
			fmt.Fprintln(os.Stderr, "note: cannot prepare XDG_RUNTIME_DIR for cage; gtk4 GUI tests will skip:", err)
			return
		}
		env = append(env, "XDG_RUNTIME_DIR="+dir)
		cleanup = func() { os.RemoveAll(dir) }
	}

	// cage exits with its child's status, so a cage that cannot start is
	// indistinguishable from a failing test suite. Probe first: without this,
	// an unusable cage turns every gtk4 run into a mystery failure.
	if !cageCanStart(cage, env) {
		cleanup()
		fmt.Fprintln(os.Stderr, "note: cage is installed but will not start here; gtk4 GUI tests will skip.")
		return
	}

	cmd := exec.Command(cage, append([]string{"--", os.Args[0]}, os.Args[1:]...)...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
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

// cageCanStart reports whether cage runs a trivial command to completion in
// this environment.
func cageCanStart(cage string, env []string) bool {
	probe, err := exec.LookPath("true")
	if err != nil {
		return true // can't probe; assume usable rather than skip everything
	}
	cmd := exec.Command(cage, "--", probe)
	cmd.Env = env
	return cmd.Run() == nil
}

// GUIRenderSkipReason returns a non-empty skip reason when a test that presents
// real windows must not run in the current environment.
//
// After MaybeReexecUnderCage the invariant is: not inside cage ⟹ cage was
// absent or would not start, because cage is now attempted regardless of
// whether a display is present. So the only reason to be here un-wrapped is
// that there is no compositor to contain the window — skip, whether or not a
// display exists. Inside cage, rendering is contained → run.
func GUIRenderSkipReason() string {
	if os.Getenv(cageActiveEnv) != "" {
		return ""
	}
	if !hasDisplay() {
		return "gtk4 GUI test needs cage (no display, and cage is unavailable here)"
	}
	return "gtk4 GUI test needs a headless compositor (cage) to avoid flashing; run via `go tool verify` or install cage"
}
