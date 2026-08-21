// Package headless runs GUI code inside a cage compositor so it renders
// without a desktop and never flashes on one.
//
// It exists as its own package because two callers need the same knowledge and
// neither should own it: test binaries self-wrap in TestMain
// (testutil.MaybeReexecUnderCage), and `go tool verify` wraps the commands it
// runs. A third caller is coming — every GUI platform needs this, not just
// gtk4 — so the cage discovery, the backend environment and the private
// runtime directory live here rather than being copied a third time.
package headless

import (
	"fmt"
	"os"
	"os/exec"
)

// ActiveEnv marks a process, and its descendants, as already running inside a
// cage compositor so nested wrappers don't double-wrap.
const ActiveEnv = "SNGL_CAGE_ACTIVE"

// Active reports whether this process is already inside a cage compositor.
func Active() bool { return os.Getenv(ActiveEnv) != "" }

// HasDisplay reports whether a Wayland/X11 session is present. This says
// whether rendering would reach a real desktop, not whether rendering is
// possible: cage's headless backend supplies its own display.
func HasDisplay() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// Compositor returns the resolved cage path, or "" when cage is not installed.
func Compositor() string {
	path, err := exec.LookPath("cage")
	if err != nil {
		return ""
	}
	return path
}

// RuntimeDir creates a private XDG_RUNTIME_DIR for one compositor.
//
// Never the ambient one: cage names its socket wayland-0 and locks it, so two
// compositors sharing a runtime directory fight over the lock — and `go test
// ./...` starts one per package that self-wraps. A crashed run also leaves the
// lock behind, which would poison every later run in that directory. A bare
// container has no XDG_RUNTIME_DIR at all, and cage refuses to start without
// one, so this covers that too.
func RuntimeDir() (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "sngl-cage")
	if err != nil {
		return "", func() {}, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", func() {}, err
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

// Env returns the environment a cage child needs: the wlroots headless backend
// and software renderer, so it runs with no real output and no GPU, plus the
// private runtime directory and the marker that stops descendants re-wrapping.
func Env(runtimeDir string) []string {
	return []string{
		"WLR_BACKENDS=headless",
		"WLR_RENDERER=pixman",
		ActiveEnv + "=1",
		"XDG_RUNTIME_DIR=" + runtimeDir,
	}
}

// CanStart reports whether cage runs a trivial command to completion here.
//
// Worth the probe because cage exits with its child's status, which makes a
// cage that cannot start indistinguishable from a failing suite — it would
// surface as a bare failure with no explanation.
func CanStart(compositor string, env []string) bool {
	probe, err := exec.LookPath("true")
	if err != nil {
		return true // can't probe; assume usable rather than skip everything
	}
	cmd := exec.Command(compositor, "--", probe)
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run() == nil
}

// Wrap rewrites (command, args) to run under a headless compositor, returning
// the extra environment and a cleanup to call once the command has finished.
// When cage is unavailable it returns the command unchanged, and GUI code is
// expected to skip itself via SkipReason.
func Wrap(command string, args []string) (cmd string, wrapped []string, env []string, cleanup func()) {
	compositor := Compositor()
	if compositor == "" || Active() {
		return command, args, nil, func() {}
	}
	dir, cleanup, err := RuntimeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "note: no runtime dir for cage; GUI tests will skip:", err)
		return command, args, nil, func() {}
	}
	env = Env(dir)
	if !CanStart(compositor, env) {
		cleanup()
		fmt.Fprintln(os.Stderr, "note: cage is installed but will not start here; GUI tests will skip.")
		return command, args, nil, func() {}
	}
	return compositor, append([]string{"--", command}, args...), env, cleanup
}

// SkipReason returns a non-empty reason when code that presents real windows
// must not run here.
//
// The invariant after a wrapper has run is: not inside cage ⟹ cage was absent
// or would not start, because cage is attempted regardless of whether a
// display is present. So the only reason to be here un-wrapped is that there
// is no compositor to contain the window.
func SkipReason() string {
	if Active() {
		return ""
	}
	if !HasDisplay() {
		return "GUI test needs cage (no display, and cage is unavailable here)"
	}
	return "GUI test needs a headless compositor (cage) to avoid flashing; run via `go tool verify` or install cage"
}
