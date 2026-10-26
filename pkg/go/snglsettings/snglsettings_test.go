package snglsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileKeepsValuesAcrossPlaces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	File("Pokédex").Write("", "language", `"Deutsch"`)
	File("Pokédex").Write("", "dexName", `"kanto"`)
	File("Pokédex").Write("other", "language", `"Français"`)

	again := File("Pokédex")
	if got := again.Read("", "language"); got != `"Deutsch"` {
		t.Errorf("language = %s, want \"Deutsch\"", got)
	}
	if got := again.Read("", "dexName"); got != `"kanto"` {
		t.Errorf("dexName = %s, want \"kanto\"", got)
	}
	if got := again.Read("other", "language"); got != `"Français"` {
		t.Errorf("other's language = %s, want \"Français\"", got)
	}
	if got := again.Read("", "missing"); got != "" {
		t.Errorf("missing = %q, want none", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "Pokédex", "settings.json")); err != nil {
		t.Error(err)
	}
}

func TestNothingIsKeptUntilOpened(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	Open(nil)
	if Persistent() {
		t.Fatal("persistent before Open")
	}
	Write("", "n", "1")
	if got := Read("", "n"); got != "" {
		t.Errorf("read %q before Open", got)
	}
	Open(File("app"))
	defer Open(nil)
	Write("", "n", "1")
	if got := Read("", "n"); got != "1" {
		t.Errorf("read %q, want 1", got)
	}
}

func TestAnUnreadableFileIsNoValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "settings.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := File("app")
	if got := f.Read("", "n"); got != "" {
		t.Errorf("read %q from a broken file", got)
	}
	f.Write("", "n", "2")
	if got := f.Read("", "n"); got != "2" {
		t.Errorf("read %q after writing over a broken file, want 2", got)
	}
}
