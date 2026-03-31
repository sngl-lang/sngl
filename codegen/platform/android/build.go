package android

import (
	"fmt"
	"os"
	"path/filepath"
)

// Build implements codegen.Builder. It compiles generated sources into a
// signed debug APK, optionally running gomobile bind first if a Go module
// is present.
func (g *Generator) Build(dir string, opts map[string]string) (string, error) {
	useGradle := opts["gradle"] != "false"
	pkg := opts["package"]
	if pkg == "" {
		pkg = "test.sngl.app"
	}

	// If a Go module is present (go+android), run gomobile bind first.
	goLibDir := filepath.Join(dir, "golib")
	if info, err := os.Stat(goLibDir); err == nil && info.IsDir() {
		aarPath := filepath.Join(dir, "golib.aar")
		if err := gomobileBind(goLibDir, aarPath); err != nil {
			return "", fmt.Errorf("gomobile bind: %w", err)
		}
	}

	if useGradle {
		var err error
		pkg, err = readPackage(dir)
		if err != nil {
			return "", err
		}
		if err := gradleBuild(dir); err != nil {
			return "", err
		}
		return filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk"), nil
	}

	tc, err := resolveToolchain()
	if err != nil {
		return "", fmt.Errorf("resolving toolchain: %w", err)
	}
	return directBuild(dir, tc, pkg)
}
