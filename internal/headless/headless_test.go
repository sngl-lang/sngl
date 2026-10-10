package headless

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain lets this binary serve as Wrap's shim, as every caller of Wrap must.
func TestMain(m *testing.M) {
	RunShim()
	os.Exit(m.Run())
}

// GUI tests reach a real compositor only through a wrapper in this package.
// Skipping the wrap when no display is present gave a container a false
// all-clear: it reported the render tests as skipped on exactly the machines
// that could have run them, because cage's headless backend supplies its own
// display.
func TestSkipReason(t *testing.T) {
	t.Run("inside cage runs", func(t *testing.T) {
		t.Setenv(ActiveEnv, "1")
		if r := SkipReason(); r != "" {
			t.Errorf("inside cage should run, got skip reason %q", r)
		}
	})

	t.Run("outside cage skips, display or not", func(t *testing.T) {
		for _, display := range []string{"", ":0"} {
			t.Setenv(ActiveEnv, "")
			os.Unsetenv(ActiveEnv)
			t.Setenv("WAYLAND_DISPLAY", "")
			os.Unsetenv("WAYLAND_DISPLAY")
			t.Setenv("DISPLAY", display)
			if display == "" {
				os.Unsetenv("DISPLAY")
			}
			if r := SkipReason(); r == "" {
				t.Errorf("DISPLAY=%q outside cage: expected a skip reason", display)
			}
		}
	})
}

// hasDisplay answers whether rendering would reach a real desktop, which is
// not the same question as whether rendering is possible.
func TestHasDisplay(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	os.Unsetenv("WAYLAND_DISPLAY")
	t.Setenv("DISPLAY", "")
	os.Unsetenv("DISPLAY")
	if HasDisplay() {
		t.Error("no display vars set: HasDisplay() = true")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if !HasDisplay() {
		t.Error("WAYLAND_DISPLAY set: HasDisplay() = false")
	}
}

// A wrapped command ends when the command does, whatever it leaves behind.
//
// cage learns that its child exited by reading EOF from a pipe whose write end
// the child inherits, and nothing marks that end close-on-exec, so every
// descendant inherits it too. A test binary killed by its timeout left the
// `sngl test` it had started running, holding the pipe, and cage -- and the
// `go tool verify` waiting on it -- sat there for seven hours.
func TestWrapEndsWithItsCommand(t *testing.T) {
	if Compositor() == "" || Active() {
		t.Skip("needs cage, and not already inside it")
	}
	// The command outlasts a second before finishing, so a wrapper that ended
	// with the shim rather than the command would leave the file unwritten.
	done := filepath.Join(t.TempDir(), "done")
	command, args, env, cleanup := Wrap("sh", []string{"-c",
		`sleep 30 >/dev/null 2>&1 </dev/null & sleep 1; touch "$0"`, done})
	defer cleanup()
	if env == nil {
		t.Skip("cage will not start here")
	}
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapped command: %v", err)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("the command did not finish: %v", err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("wrapper ran %v: it waited for the orphan rather than the command", d.Round(time.Second))
	}
}
