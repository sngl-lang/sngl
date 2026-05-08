package lib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestLookupHit(t *testing.T) {
	m := Manifest{
		"Login": {Translations: map[string]string{"es": "Iniciar"}},
	}
	got := m.Lookup("Login", "Login", "es")
	if got != "Iniciar" {
		t.Errorf("got %q, want Iniciar", got)
	}
}

func TestManifestLookupFallbackToInlined(t *testing.T) {
	m := Manifest{}
	got := m.Lookup("Login", "Login", "es")
	if got != "Login" {
		t.Errorf("got %q, want Login (inlined fallback)", got)
	}
}

func TestManifestLookupFallbackToKey(t *testing.T) {
	m := Manifest{}
	got := m.Lookup("Login", "", "es")
	if got != "Login" {
		t.Errorf("got %q, want Login (key fallback)", got)
	}
}

func TestManifestLookupLocaleHierarchy(t *testing.T) {
	m := Manifest{
		"Login": {Translations: map[string]string{"es": "Iniciar"}},
	}
	got := m.Lookup("Login", "Login", "es-MX")
	if got != "Iniciar" {
		t.Errorf("got %q, want Iniciar (via es fallback)", got)
	}
}

func TestManifestLookupNoMatchingLocale(t *testing.T) {
	m := Manifest{
		"Login": {Translations: map[string]string{"es": "Iniciar"}},
	}
	got := m.Lookup("Login", "Login", "fr-FR")
	if got != "Login" {
		t.Errorf("got %q, want Login (no fr translation, fall back to inlined)", got)
	}
}

func TestLoadManifestFromFileMissing(t *testing.T) {
	m, err := LoadManifestFromFile("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty, got %v", m)
	}
}

func TestLoadManifestFromFileValid(t *testing.T) {
	tmp, _ := os.MkdirTemp("", "manifest-test")
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, "i18n.manifest.json")
	err := os.WriteFile(path, []byte(`{"Login":{"translations":{"es":"Iniciar"}}}`), 0644)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := LoadManifestFromFile(path)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := m.Lookup("Login", "Login", "es"); got != "Iniciar" {
		t.Errorf("got %q, want Iniciar", got)
	}
}

func TestLoadManifestFromFileMalformed(t *testing.T) {
	tmp, _ := os.MkdirTemp("", "manifest-test")
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, "i18n.manifest.json")
	err := os.WriteFile(path, []byte(`not json`), 0644)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = LoadManifestFromFile(path)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}
