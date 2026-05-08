package lib

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestTrSimple(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	got := tr.Tr("Hello {name}!", "Hello {name}!", map[string]any{"name": "world"})
	if got != "Hello world!" {
		t.Errorf("got %q, want Hello world!", got)
	}
}

func TestTrManifestHit(t *testing.T) {
	m := Manifest{
		"Login": {Translations: map[string]string{"es": "Iniciar"}},
	}
	tr := NewTranslator(m, "es")
	got := tr.Tr("Login", "Login", nil)
	if got != "Iniciar" {
		t.Errorf("got %q, want Iniciar", got)
	}
}

func TestTrPluralOther(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	tmpl := "{count, plural, =0{no files} one{1 file} other{# files}}"
	got := tr.Tr(tmpl, tmpl, map[string]any{"count": 5})
	if got != "5 files" {
		t.Errorf("got %q, want 5 files", got)
	}
}

func TestTrPluralExactZero(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	tmpl := "{count, plural, =0{no files} one{1 file} other{# files}}"
	got := tr.Tr(tmpl, tmpl, map[string]any{"count": 0})
	if got != "no files" {
		t.Errorf("got %q, want no files", got)
	}
}

func TestTrPluralOne(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	tmpl := "{count, plural, =0{no files} one{1 file} other{# files}}"
	got := tr.Tr(tmpl, tmpl, map[string]any{"count": 1})
	if got != "1 file" {
		t.Errorf("got %q, want 1 file", got)
	}
}

func TestTrSelect(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	tmpl := "{gender, select, male{he} female{she} other{they}}"
	cases := []struct {
		gender string
		want   string
	}{
		{"male", "he"},
		{"female", "she"},
		{"nonbinary", "they"},
	}
	for _, c := range cases {
		got := tr.Tr(tmpl, tmpl, map[string]any{"gender": c.gender})
		if got != c.want {
			t.Errorf("Select(%q) = %q, want %q", c.gender, got, c.want)
		}
	}
}

func TestFormatNoManifestLookup(t *testing.T) {
	tr := NewTranslator(Manifest{
		"Hello": {Translations: map[string]string{"es": "Hola"}},
	}, "es")
	// Format does NOT consult the manifest.
	got := tr.Format("Hello", nil)
	if got != "Hello" {
		t.Errorf("Format should not consult manifest; got %q", got)
	}
}

func TestPluralEnglish(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	forms := map[PluralKey]string{
		Exactly(0):  "no files",
		PluralOne:   "1 file",
		PluralOther: "{n} files",
	}
	cases := []struct {
		n    int
		want string
	}{
		{0, "no files"},
		{1, "1 file"},
		{5, "5 files"},
	}
	for _, c := range cases {
		got := tr.Plural(c.n, forms)
		if got != c.want {
			t.Errorf("Plural(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestSelectFallsBackToOther(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	cases := map[string]string{"male": "he", "female": "she", "other": "they"}
	if got := tr.Select("nonbinary", cases); got != "they" {
		t.Errorf("got %q, want they", got)
	}
}

func TestNumberInt(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	got := tr.NumberInt(1234, "decimal")
	// x/text emits "1,234" for en-US.
	if got != "1234" && got != "1,234" {
		t.Errorf("got %q, want 1234 or 1,234", got)
	}
}

func TestDateMedium(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	d := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	got := tr.Date(d, "medium")
	if got != "May 7, 2026" {
		t.Errorf("got %q, want May 7, 2026", got)
	}
}

func TestDatetimeShort(t *testing.T) {
	tr := NewTranslator(Manifest{}, "en")
	d := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	got := tr.Datetime(d, "short", "short")
	// "5/7/26 12:30 PM"
	if got != "5/7/26 12:30 PM" {
		t.Errorf("got %q, want 5/7/26 12:30 PM", got)
	}
}
