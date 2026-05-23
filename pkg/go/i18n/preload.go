package i18n

import (
	"encoding/json"
	"errors"
)

// SetManifest primes the process-wide default Translator with the
// supplied manifest, bypassing the lazy disk read in GetTranslator.
// Subsequent calls (including the lazy one) are no-ops.
//
// Generated code emitted by Go-targeting platforms (bubbletea, fyne,
// gtk4) calls this from an init() function so that translations land
// in the binary itself rather than requiring i18n.manifest.json to
// sit beside the executable at runtime.
func SetManifest(m Manifest) {
	translatorOnce.Do(func() {
		if m == nil {
			m = Manifest{}
		}
		defaultTranslator = NewTranslator(m, pickLocale())
	})
}

// SetManifestBytes is the convenience wrapper generated code uses
// when the manifest is embedded as raw JSON (e.g. via //go:embed).
// Parses data and delegates to SetManifest; returns an error when
// the JSON is malformed. Nil/empty data installs an empty manifest.
func SetManifestBytes(data []byte) error {
	if len(data) == 0 {
		SetManifest(Manifest{})
		return nil
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return errors.Join(errors.New("i18n: parse manifest"), err)
	}
	SetManifest(m)
	return nil
}
