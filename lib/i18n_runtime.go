package lib

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// ManifestEntry mirrors one entry in i18n.manifest.json.
type ManifestEntry struct {
	Translations map[string]string `json:"translations"`
}

// Manifest maps ICU template keys to their translations.
type Manifest map[string]ManifestEntry

// LoadManifest reads i18n.manifest.json from the given filesystem.
// Returns an empty manifest (no error) if the file does not exist.
func LoadManifest(fsys fs.FS, path string) (Manifest, error) {
	data, err := fs.ReadFile(fsys, path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = Manifest{}
	}
	return m, nil
}

// LoadManifestFromFile loads from the OS filesystem; returns empty
// manifest if the file is missing.
func LoadManifestFromFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = Manifest{}
	}
	return m, nil
}

// Lookup returns the translated string for the given key + locale.
// Falls back through:
//  1. exact-locale translation in the manifest entry
//  2. progressively shorter locale forms (e.g. "es-MX" → "es")
//  3. inlinedTemplate (the template string the lowering passed in)
//  4. key itself (final fallback)
func (m Manifest) Lookup(key, inlinedTemplate, locale string) string {
	e, ok := m[key]
	if !ok {
		if inlinedTemplate != "" {
			return inlinedTemplate
		}
		return key
	}
	// Try locale, then progressively shorter forms.
	for loc := locale; loc != ""; {
		if v, ok := e.Translations[loc]; ok {
			return v
		}
		i := strings.LastIndex(loc, "-")
		if i < 0 {
			break
		}
		loc = loc[:i]
	}
	if inlinedTemplate != "" {
		return inlinedTemplate
	}
	return key
}
