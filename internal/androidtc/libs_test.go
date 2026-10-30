package androidtc

import (
	"regexp"
	"strings"
	"testing"
)

// TestLibsAreResolved holds libs_gen.go to every combo: a combo added or
// changed without running resolvelibs builds against nothing, or the old set.
func TestLibsAreResolved(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, c := range combos {
		libs := c.Libs()
		if len(libs) == 0 {
			t.Errorf("combo %q has no libraries: run go run ./internal/androidtc/resolvelibs", c.Name)
			continue
		}
		seen := map[string]bool{}
		for _, l := range libs {
			key := l.Group + ":" + l.Module
			if seen[key] {
				t.Errorf("combo %q lists %s twice", c.Name, key)
			}
			seen[key] = true
			if !hex.MatchString(l.SHA256) || !strings.HasPrefix(l.URL, "https://") {
				t.Errorf("combo %q: %s has URL %q and sum %q", c.Name, key, l.URL, l.SHA256)
			}
			if l.Group == "org.jetbrains.kotlin" && strings.HasPrefix(l.Module, "kotlin-stdlib") {
				t.Errorf("combo %q lists %s, which kotlinc carries", c.Name, key)
			}
		}
		// SavedStateHandle reads MutableLiveData from here; missing it, a
		// NavHost failed at launch.
		for _, want := range []string{"androidx.lifecycle:lifecycle-livedata-core", "androidx.savedstate:savedstate", "androidx.navigation:navigation-compose"} {
			if !seen[want] {
				t.Errorf("combo %q does not list %s", c.Name, want)
			}
		}
	}
}
