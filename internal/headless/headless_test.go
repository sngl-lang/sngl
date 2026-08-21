package headless

import (
	"os"
	"testing"
)

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
