package codegen

import (
	"os"
	"path/filepath"
)

// SnglCacheDir returns the base cache directory for SNGL.
func SnglCacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "sngl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "sngl-cache")
	}
	return filepath.Join(home, ".cache", "sngl")
}
