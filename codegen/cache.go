package codegen

import (
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/internal/gencache"
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

// GeneratedInputs is every stored generated file this process has read, each
// spelled as the `cache.entry` input something derived from it records.
//
// The compile-time evaluator reports it after the calls it ran. A pure
// function may fold the compiler's own generated source into its value -- the
// docs site reads every target's package, gtk4's widget declarations among
// them -- and a value stored without saying so would outlive that source
// being regenerated. It lives here rather than beside the store because the
// evaluator is built in the program's module, which may not import internal/.
func GeneratedInputs() []string {
	used := gencache.Default().Used()
	out := make([]string, len(used))
	for i, in := range used {
		out[i] = in.String()
	}
	return out
}
