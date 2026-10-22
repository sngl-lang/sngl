package toolchain

import (
	"testing"

	"duckfam.us/sngl/internal/headless"
)

// A gtk4 program `sngl test` starts draws in this process's display or in the
// cage it is already inside. An installed cage that nothing started is
// neither: CI's container has one that will not start, and every gtk4 test
// there failed as its program exited at gtk_init.
func TestDrawsHere(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		active, wayland, x11 string
		platform             string
		nowhere              bool
	}{
		{name: "no display", platform: "gtk4", nowhere: true},
		{name: "wayland", platform: "gtk4", wayland: "wayland-0"},
		{name: "x11", platform: "gtk4", x11: ":0"},
		{name: "inside cage", platform: "gtk4", active: "1"},
		{name: "fyne draws offscreen", platform: "fyne"},
		{name: "html draws in its browser", platform: "html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(headless.ActiveEnv, tc.active)
			t.Setenv("WAYLAND_DISPLAY", tc.wayland)
			t.Setenv("DISPLAY", tc.x11)
			if got := DrawsHere(tc.platform); (got != "") != tc.nowhere {
				t.Errorf("DrawsHere(%q) = %q, want nowhere to draw: %v", tc.platform, got, tc.nowhere)
			}
		})
	}
}
