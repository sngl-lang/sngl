package i18n

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

// ManifestFileName is the canonical filename for the SNGL i18n
// translation manifest, expected at the project root.
const ManifestFileName = "i18n.manifest.json"

// LoadManifest reads i18n.manifest.json from projectFS (preferred) or
// projectDir on disk and returns its compact-marshaled bytes. Returns
// (nil, nil) when the file is absent — i18n-using packages without a
// manifest fall back to inlined templates at runtime.
//
// projectFS takes precedence so in-memory builds (playground, tests)
// behave the same as on-disk builds. Malformed JSON propagates as a
// non-nil error; callers should surface it rather than silently
// skipping the manifest.
func LoadManifest(projectFS fs.FS, projectDir string) ([]byte, error) {
	raw := readManifestBytes(projectFS, projectDir)
	if raw == nil {
		return nil, nil
	}
	// Re-marshal to compact form so arbitrary whitespace from a
	// hand-edited manifest doesn't bloat embed/asset/inline payloads.
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

func readManifestBytes(projectFS fs.FS, projectDir string) []byte {
	if projectFS != nil {
		if data, err := fs.ReadFile(projectFS, ManifestFileName); err == nil {
			return data
		}
	}
	if projectDir != "" {
		if data, err := os.ReadFile(filepath.Join(projectDir, ManifestFileName)); err == nil {
			return data
		}
	}
	return nil
}
