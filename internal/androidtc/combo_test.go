package androidtc

import "testing"

func TestDefaultIsKnown(t *testing.T) {
	d := Default()
	if _, ok := ByName(d.Name); !ok {
		t.Fatalf("Default() returned unknown combo %q", d.Name)
	}
	if _, ok := ByName(defaultName); !ok {
		t.Fatalf("defaultName %q is not in the matrix", defaultName)
	}
}

func TestCombosWellFormed(t *testing.T) {
	names := map[string]bool{}
	for _, c := range combos {
		if names[c.Name] {
			t.Errorf("duplicate combo name %q", c.Name)
		}
		names[c.Name] = true

		if c.Name == "" || c.Gradle == "" || c.AGP == "" || c.Kotlin == "" ||
			c.ComposePlugin == "" || c.ComposeBOM == "" || c.ComposeRuntime == "" ||
			c.BuildTools == "" || c.ActivityCompose == "" || c.Coil == "" {
			t.Errorf("combo %q has an empty version field: %+v", c.Name, c)
		}
		if c.CompileSdk <= 0 {
			t.Errorf("combo %q has non-positive CompileSdk %d", c.Name, c.CompileSdk)
		}
		if c.JDKMin < 8 || c.JDKMax < c.JDKMin {
			t.Errorf("combo %q has an invalid JDK window %d–%d", c.Name, c.JDKMin, c.JDKMax)
		}
	}
}

func TestByNameUnknown(t *testing.T) {
	if _, ok := ByName("does-not-exist"); ok {
		t.Fatal("ByName returned ok for an unknown combo")
	}
}
